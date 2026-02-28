package rag

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// HybridMemoryStore extends InMemoryVectorStore with hybrid search
type HybridMemoryStore struct {
	*InMemoryVectorStore
	bm25Indexes map[string]*BM25Index // collection -> BM25 index
	reranker    ReRanker
	mu          sync.RWMutex
}

// NewHybridMemoryStore creates a hybrid memory store
func NewHybridMemoryStore() *HybridMemoryStore {
	return &HybridMemoryStore{
		InMemoryVectorStore: NewInMemoryVectorStore(),
		bm25Indexes:         make(map[string]*BM25Index),
		reranker:            NewSimpleReRanker(),
	}
}

// Store stores a document and updates both vector and keyword indexes
func (h *HybridMemoryStore) Store(ctx context.Context, doc *Document) error {
	// Store in vector store
	if err := h.InMemoryVectorStore.Store(ctx, doc); err != nil {
		return err
	}

	// Update BM25 index
	return h.indexDocumentForKeywordSearch(doc)
}

// StoreBatch stores documents in batch
func (h *HybridMemoryStore) StoreBatch(ctx context.Context, docs []*Document) error {
	// Store in vector store
	if err := h.InMemoryVectorStore.StoreBatch(ctx, docs); err != nil {
		return err
	}

	// Update BM25 indexes
	for _, doc := range docs {
		if err := h.indexDocumentForKeywordSearch(doc); err != nil {
			return err
		}
	}

	return nil
}

// HybridSearch performs combined vector + keyword search
func (h *HybridMemoryStore) HybridSearch(ctx context.Context, query *HybridSearchQuery) ([]*HybridSearchResult, error) {
	if query.QueryText == "" {
		return nil, fmt.Errorf("query text is required")
	}

	collection := query.Collection
	if collection == "" {
		collection = "default"
	}

	// Set weights if not specified
	if query.VectorWeight == 0 && query.KeywordWeight == 0 {
		query.VectorWeight = 0.7
		query.KeywordWeight = 0.3
	} else if query.KeywordWeight == 0 {
		query.KeywordWeight = 1.0 - query.VectorWeight
	} else if query.VectorWeight == 0 {
		query.VectorWeight = 1.0 - query.KeywordWeight
	}

	// Ensure weights sum to 1.0
	totalWeight := query.VectorWeight + query.KeywordWeight
	if totalWeight > 0 {
		query.VectorWeight /= totalWeight
		query.KeywordWeight /= totalWeight
	}

	// Get candidate documents (more than needed for reranking)
	candidateK := query.TopK
	if query.EnableReranking && query.RerankTopK > 0 {
		candidateK = query.RerankTopK
	}

	// Perform vector search if vector weight > 0
	vectorResults := make(map[string]*SearchResult)
	if query.VectorWeight > 0 && query.Embedding != nil {
		vectorQuery := &SearchQuery{
			Embedding:      query.Embedding,
			Collection:     collection,
			TopK:           candidateK * 2, // Get more for merging
			MinScore:       0.0,            // We'll filter after hybrid scoring
			MetadataFilter: query.MetadataFilter,
		}

		results, err := h.InMemoryVectorStore.Search(ctx, vectorQuery)
		if err != nil {
			return nil, fmt.Errorf("vector search failed: %w", err)
		}

		for _, r := range results {
			vectorResults[r.Document.ID] = r
		}
	}

	// Perform keyword search if keyword weight > 0
	keywordResults := make(map[string]float64)
	var maxBM25Score float64

	if query.KeywordWeight > 0 {
		bm25Index := h.getBM25Index(collection)
		if bm25Index == nil {
			// Build index if it doesn't exist
			if err := h.BuildInvertedIndex(ctx, collection); err != nil {
				return nil, fmt.Errorf("failed to build BM25 index: %w", err)
			}
			bm25Index = h.getBM25Index(collection)
		}

		if bm25Index != nil {
			queryTerms := tokenize(query.QueryText)

			// Score all documents in collection
			h.InMemoryVectorStore.mu.RLock()
			for _, doc := range h.InMemoryVectorStore.documents {
				if doc.Collection != collection {
					continue
				}

				// Check metadata filter
				if !matchesMetadataFilter(doc.Metadata, query.MetadataFilter) {
					continue
				}

				score := bm25Index.Score(doc.ID, queryTerms)
				if score > 0 {
					keywordResults[doc.ID] = score
					if score > maxBM25Score {
						maxBM25Score = score
					}
				}
			}
			h.InMemoryVectorStore.mu.RUnlock()
		}
	}

	// Combine results
	hybridResults := make([]*HybridSearchResult, 0)

	// Collect all unique document IDs
	docIDs := make(map[string]bool)
	for id := range vectorResults {
		docIDs[id] = true
	}
	for id := range keywordResults {
		docIDs[id] = true
	}

	// Calculate hybrid scores
	for docID := range docIDs {
		var vectorScore float64
		var keywordScore float64
		var doc *Document

		// Get vector score
		if vr, exists := vectorResults[docID]; exists {
			vectorScore = vr.Score
			doc = vr.Document
		}

		// Get keyword score (normalized)
		if kr, exists := keywordResults[docID]; exists {
			keywordScore = NormalizeBM25Score(kr, maxBM25Score)

			if doc == nil {
				// Get document
				h.InMemoryVectorStore.mu.RLock()
				doc = h.InMemoryVectorStore.documents[docID]
				h.InMemoryVectorStore.mu.RUnlock()
			}
		}

		if doc == nil {
			continue
		}

		// Calculate hybrid score
		hybridScore := (query.VectorWeight * vectorScore) + (query.KeywordWeight * keywordScore)

		// Apply minimum score filter
		if hybridScore < query.MinScore {
			continue
		}

		// Determine match type
		matchType := "hybrid"
		if query.VectorWeight == 1.0 {
			matchType = "semantic"
		} else if query.KeywordWeight == 1.0 {
			matchType = "keyword"
		}

		// Get BM25 index for grounding spans
		bm25Index := h.getBM25Index(collection)
		var matchingTerms []string
		if bm25Index != nil {
			matchingTerms = bm25Index.GetMatchingTerms(docID, tokenize(query.QueryText))
		}

		// Extract grounding spans
		groundingSpans := ExtractGroundingSpans(doc, query.QueryText, matchingTerms, 3)

		// Calculate confidence (higher for better matches)
		confidence := hybridScore
		if len(groundingSpans) > 0 {
			confidence = (confidence + groundingSpans[0].Score) / 2.0
		}

		hybridResults = append(hybridResults, &HybridSearchResult{
			Document:       doc,
			VectorScore:    vectorScore,
			KeywordScore:   keywordScore,
			HybridScore:    hybridScore,
			RerankScore:    hybridScore, // Will be updated if reranking enabled
			GroundingSpans: groundingSpans,
			Confidence:     confidence,
			MatchType:      matchType,
		})
	}

	// Sort by hybrid score
	sort.Slice(hybridResults, func(i, j int) bool {
		return hybridResults[i].HybridScore > hybridResults[j].HybridScore
	})

	// Re-rank if enabled
	if query.EnableReranking && h.reranker != nil && len(hybridResults) > 0 {
		reranked, err := h.reranker.Rerank(ctx, query.QueryText, hybridResults)
		if err == nil {
			hybridResults = reranked
		}
	}

	// Limit to top-k
	if len(hybridResults) > query.TopK {
		hybridResults = hybridResults[:query.TopK]
	}

	return hybridResults, nil
}

// BuildInvertedIndex builds BM25 keyword index for a collection
func (h *HybridMemoryStore) BuildInvertedIndex(ctx context.Context, collection string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Create new BM25 index
	bm25Index := NewBM25Index()

	// Index all documents in collection
	h.InMemoryVectorStore.mu.RLock()
	for _, doc := range h.InMemoryVectorStore.documents {
		if doc.Collection == collection {
			bm25Index.IndexDocument(doc.ID, doc.Content)
		}
	}
	h.InMemoryVectorStore.mu.RUnlock()

	h.bm25Indexes[collection] = bm25Index
	return nil
}

// DeleteCollection deletes collection and its indexes
func (h *HybridMemoryStore) DeleteCollection(ctx context.Context, collectionName string) error {
	// Delete from base store
	if err := h.InMemoryVectorStore.DeleteCollection(ctx, collectionName); err != nil {
		return err
	}

	// Delete BM25 index
	h.mu.Lock()
	delete(h.bm25Indexes, collectionName)
	h.mu.Unlock()

	return nil
}

// SetReranker sets a custom re-ranker
func (h *HybridMemoryStore) SetReranker(reranker ReRanker) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reranker = reranker
}

// indexDocumentForKeywordSearch indexes a document for BM25 search
func (h *HybridMemoryStore) indexDocumentForKeywordSearch(doc *Document) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	collection := doc.Collection
	if collection == "" {
		collection = "default"
	}

	// Get or create BM25 index
	bm25Index, exists := h.bm25Indexes[collection]
	if !exists {
		bm25Index = NewBM25Index()
		h.bm25Indexes[collection] = bm25Index
	}

	// Index document
	bm25Index.IndexDocument(doc.ID, doc.Content)

	return nil
}

// getBM25Index gets the BM25 index for a collection
func (h *HybridMemoryStore) getBM25Index(collection string) *BM25Index {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.bm25Indexes[collection]
}

// GetHybridStats returns statistics about hybrid indexes
func (h *HybridMemoryStore) GetHybridStats() map[string]interface{} {
	h.mu.RLock()
	defer h.mu.RUnlock()

	baseStats := h.InMemoryVectorStore.GetStats()

	indexStats := make(map[string]interface{})
	for collection, index := range h.bm25Indexes {
		index.mu.RLock()
		indexStats[collection] = map[string]interface{}{
			"num_docs":       index.numDocs,
			"avg_doc_length": index.avgDocLength,
			"num_terms":      len(index.docFreq),
		}
		index.mu.RUnlock()
	}

	baseStats["bm25_indexes"] = indexStats
	baseStats["reranker_enabled"] = h.reranker != nil

	return baseStats
}
