package tools

import (
	"context"
	"fmt"

	"github.com/seyi/dagens/pkg/rag"
	"github.com/seyi/dagens/pkg/types"
)

// HybridRagRetrievalConfig configures hybrid RAG retrieval
type HybridRagRetrievalConfig struct {
	// HybridStore is the hybrid vector store
	HybridStore rag.HybridVectorStore

	// EmbeddingService generates embeddings for queries
	EmbeddingService rag.EmbeddingService

	// DefaultCollection is the default collection to search
	DefaultCollection string

	// DefaultTopK is the default number of results
	DefaultTopK int

	// DefaultMinScore is the default minimum score
	DefaultMinScore float64

	// DefaultVectorWeight for hybrid search (0.0-1.0)
	// 0.0 = keyword only, 1.0 = semantic only
	// Default: 0.7 (70% semantic, 30% keyword)
	DefaultVectorWeight float64

	// EnableReranking enables result re-ranking
	EnableReranking bool

	// Name is the tool name
	Name string

	// Description is the tool description
	Description string

	// IncludeGrounding whether to include grounding spans in results
	IncludeGrounding bool
}

// HybridRagRetrievalTool creates a hybrid RAG retrieval tool
// Combines semantic vector search with BM25 keyword search for better results
func HybridRagRetrievalTool(config HybridRagRetrievalConfig) *types.ToolDefinition {
	// Apply defaults
	if config.Name == "" {
		config.Name = "retrieve_documents"
	}
	if config.Description == "" {
		config.Description = "Retrieve relevant documents using hybrid search (semantic + keyword). Returns documents with grounding citations showing where information was found."
	}
	if config.DefaultTopK == 0 {
		config.DefaultTopK = 5
	}
	if config.DefaultMinScore == 0 {
		config.DefaultMinScore = 0.3 // Lower for hybrid search
	}
	if config.DefaultVectorWeight == 0 {
		config.DefaultVectorWeight = 0.7
	}

	return &types.ToolDefinition{
		Name:        config.Name,
		Description: config.Description,
		Schema: &types.ToolSchema{
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "The search query (used for both semantic and keyword search)",
					},
					"collection": map[string]interface{}{
						"type":        "string",
						"description": "Optional: Collection to search (defaults to default collection)",
					},
					"top_k": map[string]interface{}{
						"type":        "integer",
						"description": fmt.Sprintf("Optional: Number of results (default: %d)", config.DefaultTopK),
					},
					"min_score": map[string]interface{}{
						"type":        "number",
						"description": fmt.Sprintf("Optional: Minimum relevance score 0.0-1.0 (default: %.2f)", config.DefaultMinScore),
					},
					"vector_weight": map[string]interface{}{
						"type":        "number",
						"description": fmt.Sprintf("Optional: Weight for semantic search 0.0-1.0 (default: %.2f, keyword weight = 1.0 - vector_weight)", config.DefaultVectorWeight),
					},
					"enable_reranking": map[string]interface{}{
						"type":        "boolean",
						"description": "Optional: Enable result re-ranking for better relevance (default: false)",
					},
				},
				"required": []string{"query"},
			},
		},
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			return handleHybridRagRetrieval(ctx, params, config)
		},
		Enabled: true,
	}
}

// handleHybridRagRetrieval handles hybrid RAG retrieval
func handleHybridRagRetrieval(ctx context.Context, params map[string]interface{}, config HybridRagRetrievalConfig) (interface{}, error) {
	// Extract query
	query, ok := params["query"].(string)
	if !ok || query == "" {
		return nil, fmt.Errorf("query parameter is required and must be a non-empty string")
	}

	// Extract optional parameters
	collection := config.DefaultCollection
	if coll, ok := params["collection"].(string); ok && coll != "" {
		collection = coll
	}

	topK := config.DefaultTopK
	if k, ok := params["top_k"].(float64); ok {
		topK = int(k)
	}

	minScore := config.DefaultMinScore
	if score, ok := params["min_score"].(float64); ok {
		minScore = score
	}

	vectorWeight := config.DefaultVectorWeight
	if weight, ok := params["vector_weight"].(float64); ok {
		vectorWeight = weight
	}

	enableReranking := config.EnableReranking
	if rerank, ok := params["enable_reranking"].(bool); ok {
		enableReranking = rerank
	}

	// Validate parameters
	if topK <= 0 || topK > 100 {
		return nil, fmt.Errorf("top_k must be between 1 and 100")
	}
	if minScore < 0.0 || minScore > 1.0 {
		return nil, fmt.Errorf("min_score must be between 0.0 and 1.0")
	}
	if vectorWeight < 0.0 || vectorWeight > 1.0 {
		return nil, fmt.Errorf("vector_weight must be between 0.0 and 1.0")
	}

	// Generate embedding for query
	queryEmbedding, err := config.EmbeddingService.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// Perform hybrid search
	hybridQuery := &rag.HybridSearchQuery{
		QueryText:       query,
		Embedding:       queryEmbedding,
		Collection:      collection,
		TopK:            topK,
		MinScore:        minScore,
		VectorWeight:    vectorWeight,
		KeywordWeight:   1.0 - vectorWeight,
		EnableReranking: enableReranking,
		RerankTopK:      topK * 2,
	}

	results, err := config.HybridStore.HybridSearch(ctx, hybridQuery)
	if err != nil {
		return nil, fmt.Errorf("hybrid search failed: %w", err)
	}

	// Format results for agent
	formattedResults := make([]map[string]interface{}, len(results))
	for i, result := range results {
		resultMap := map[string]interface{}{
			"content":        result.Document.Content,
			"score":          result.HybridScore,
			"vector_score":   result.VectorScore,
			"keyword_score":  result.KeywordScore,
			"confidence":     result.Confidence,
			"match_type":     result.MatchType,
			"source":         result.Document.Source,
			"metadata":       result.Document.Metadata,
		}

		// Add grounding information if enabled
		if config.IncludeGrounding && len(result.GroundingSpans) > 0 {
			spans := make([]map[string]interface{}, len(result.GroundingSpans))
			for j, span := range result.GroundingSpans {
				spans[j] = map[string]interface{}{
					"text":        span.Text,
					"start_char":  span.StartChar,
					"end_char":    span.EndChar,
					"score":       span.Score,
					"match_type":  span.MatchType,
					"match_terms": span.MatchTerms,
				}
			}
			resultMap["grounding_spans"] = spans
		}

		// Add re-rank score if enabled
		if enableReranking {
			resultMap["rerank_score"] = result.RerankScore
		}

		formattedResults[i] = resultMap
	}

	return map[string]interface{}{
		"query":             query,
		"num_results":       len(results),
		"results":           formattedResults,
		"collection":        collection,
		"search_type":       "hybrid",
		"vector_weight":     vectorWeight,
		"keyword_weight":    1.0 - vectorWeight,
		"reranking_enabled": enableReranking,
		"embedding_model":   config.EmbeddingService.ModelName(),
	}, nil
}

// SimpleHybridRagTool creates a simple hybrid RAG tool with in-memory storage
func SimpleHybridRagTool() *types.ToolDefinition {
	hybridStore := rag.NewHybridMemoryStore()
	embeddingService := rag.NewSimpleEmbeddingService(384)

	return HybridRagRetrievalTool(HybridRagRetrievalConfig{
		HybridStore:         hybridStore,
		EmbeddingService:    embeddingService,
		DefaultCollection:   "default",
		DefaultTopK:         5,
		DefaultMinScore:     0.3,
		DefaultVectorWeight: 0.7,
		EnableReranking:     false,
		IncludeGrounding:    true,
	})
}

// RegisterHybridRagTools registers hybrid RAG tools in the tool registry
func RegisterHybridRagTools(
	registry *ToolRegistry,
	hybridStore rag.HybridVectorStore,
	embeddingService rag.EmbeddingService,
	defaultCollection string,
) error {
	// Create hybrid RAG retrieval tool
	ragTool := HybridRagRetrievalTool(HybridRagRetrievalConfig{
		HybridStore:         hybridStore,
		EmbeddingService:    embeddingService,
		DefaultCollection:   defaultCollection,
		DefaultTopK:         10,
		DefaultMinScore:     0.3,
		DefaultVectorWeight: 0.7,
		EnableReranking:     true,
		IncludeGrounding:    true,
	})

	return registry.Register(ragTool)
}

// HybridRagCorpusManager helps manage document ingestion for hybrid search
type HybridRagCorpusManager struct {
	processor   *rag.DocumentProcessor
	hybridStore rag.HybridVectorStore
}

// NewHybridRagCorpusManager creates a hybrid RAG corpus manager
func NewHybridRagCorpusManager(
	hybridStore rag.HybridVectorStore,
	embeddingService rag.EmbeddingService,
	chunker rag.TextChunker,
	defaultCollection string,
) *HybridRagCorpusManager {
	// Cast to VectorStore for DocumentProcessor
	vectorStore, ok := hybridStore.(rag.VectorStore)
	if !ok {
		// This shouldn't happen since HybridVectorStore extends VectorStore
		panic("hybridStore does not implement VectorStore interface")
	}

	processor := rag.NewDocumentProcessor(
		chunker,
		embeddingService,
		vectorStore,
		defaultCollection,
	)

	return &HybridRagCorpusManager{
		processor:   processor,
		hybridStore: hybridStore,
	}
}

// IngestDocument ingests a document with both vector and keyword indexing
func (m *HybridRagCorpusManager) IngestDocument(ctx context.Context, content string, metadata map[string]interface{}) error {
	// Process document (chunks, embeds, stores)
	// HybridStore.Store() automatically updates both indexes
	return m.processor.ProcessDocument(ctx, content, metadata)
}

// IngestDocuments ingests multiple documents
func (m *HybridRagCorpusManager) IngestDocuments(ctx context.Context, contents []string, metadataList []map[string]interface{}) error {
	return m.processor.ProcessDocuments(ctx, contents, metadataList)
}

// RebuildKeywordIndex rebuilds the BM25 keyword index for a collection
func (m *HybridRagCorpusManager) RebuildKeywordIndex(ctx context.Context, collection string) error {
	return m.hybridStore.BuildInvertedIndex(ctx, collection)
}