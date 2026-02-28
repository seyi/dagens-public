# RAG (Retrieval-Augmented Generation) Package

## Overview

This package provides vendor-neutral **Retrieval-Augmented Generation (RAG)** capabilities for AI agents, inspired by Google's ADK but designed for distributed systems with support for multiple vector databases.

## Architecture

```
┌───────────────────────────────────────┐
│     Agent with RAG Tool               │
│  (retrieve_documents tool)            │
└─────────────┬─────────────────────────┘
              │
              ↓
┌───────────────────────────────────────┐
│    VectorStore Interface              │
│  (Vendor-neutral abstraction)         │
└─────────────┬─────────────────────────┘
              │
      ┌───────┼───────┬──────────────┐
      ↓       ↓       ↓              ↓
 ┌─────────┬─────────┬─────────┬─────────┐
 │Weaviate │ Memory  │Pinecone │  Custom │
 │ Store   │  Store  │ Client  │   VDB   │
 └─────────┴─────────┴─────────┴─────────┘
```

## Key Components

### 1. VectorStore Interface (`vectorstore.go`)

Vendor-neutral abstraction for vector databases:

```go
type VectorStore interface {
    Store(ctx context.Context, doc *Document) error
    Search(ctx context.Context, query *SearchQuery) ([]*SearchResult, error)
    Delete(ctx context.Context, docID string) error
    // ... more methods
}
```

**Implementations:**
- `WeaviateStore` - Weaviate integration (default, reference implementation)
- `InMemoryVectorStore` - In-memory storage for testing/demos

### 2. Embedding Service (`embeddings.go`)

Generates vector embeddings from text:

```go
type EmbeddingService interface {
    Embed(ctx context.Context, text string) ([]float64, error)
    EmbedBatch(ctx context.Context, texts []string) ([][]float64, error)
    Dimensions() int
    ModelName() string
}
```

**Implementations:**
- `SimpleEmbeddingService` - Basic bag-of-words embeddings (demo/testing)
- `CachedEmbeddingService` - Wrapper with LRU caching
- `MockEmbeddingService` - Mock for testing

**Production Integrations (to be added):**
- OpenAI Embeddings API
- Cohere Embeddings
- Sentence Transformers (local)

### 3. Document Processor (`document_processor.go`)

Handles document chunking and ingestion:

```go
type DocumentProcessor struct {
    chunker     TextChunker
    embedder    EmbeddingService
    vectorStore VectorStore
}
```

**Chunking Strategies:**
- `FixedSizeChunker` - Fixed character count with overlap
- `SentenceChunker` - Sentence-based chunking
- `ParagraphChunker` - Paragraph-based chunking (default)

## Quick Start

### Basic Usage

```go
import (
    "github.com/apache/spark/spark-ai-agents/pkg/rag"
    "github.com/apache/spark/spark-ai-agents/pkg/tools"
)

// 1. Create vector store
vectorStore := rag.NewInMemoryVectorStore()

// 2. Create embedding service
embedder := rag.NewSimpleEmbeddingService(384)

// 3. Ingest documents
processor := rag.NewDocumentProcessor(
    rag.DefaultChunker(),
    embedder,
    vectorStore,
    "my-collection",
)

err := processor.ProcessDocument(ctx,
    "Paris is the capital of France...",
    map[string]interface{}{"source": "facts.txt"},
)

// 4. Create RAG retrieval tool for agents
ragTool := tools.RagRetrievalTool(tools.RagRetrievalConfig{
    VectorStore:      vectorStore,
    EmbeddingService: embedder,
    DefaultTopK:      5,
    DefaultMinScore:  0.7,
})

// 5. Register with agent
registry := tools.NewToolRegistry()
registry.Register(ragTool)

agent := agents.NewLlmAgent(agents.LlmAgentConfig{
    Name:  "rag-agent",
    Tools: []string{"retrieve_documents"},
}, modelProvider, registry)
```

### With Weaviate (Production)

```go
// Create Weaviate store
weaviateStore, err := rag.NewWeaviateStore(&rag.WeaviateConfig{
    Host:   "localhost:8080",
    Scheme: "http",
})

// Use with RAG tool
ragTool := tools.RagRetrievalTool(tools.RagRetrievalConfig{
    VectorStore:      weaviateStore,
    EmbeddingService: embedder,
})
```

## Document Ingestion

### Single Document

```go
manager := tools.NewRagCorpusManager(
    vectorStore,
    embedder,
    rag.DefaultChunker(),
    "knowledge-base",
)

err := manager.IngestDocument(ctx,
    "Document content...",
    map[string]interface{}{
        "source": "doc1.txt",
        "author": "Alice",
        "date": "2025-01-01",
    },
)
```

### Batch Ingestion

```go
contents := []string{
    "Document 1 content...",
    "Document 2 content...",
    "Document 3 content...",
}

metadataList := []map[string]interface{}{
    {"source": "doc1.txt"},
    {"source": "doc2.txt"},
    {"source": "doc3.txt"},
}

err := manager.IngestDocuments(ctx, contents, metadataList)
```

## Searching

### Direct Search

```go
query := &rag.SearchQuery{
    QueryText:  "What is the capital of France?",
    Embedding:  queryEmbedding, // Optional: provide pre-computed
    Collection: "knowledge-base",
    TopK:       10,
    MinScore:   0.7,
    MetadataFilter: map[string]interface{}{
        "category": "geography",
    },
}

results, err := vectorStore.Search(ctx, query)

for _, result := range results {
    fmt.Printf("Score: %.3f - %s\n", result.Score, result.Document.Content)
}
```

### Via Agent Tool

Agent automatically uses the tool when needed:

```
User: "What is the capital of France?"

Agent (internal):
1. Calls retrieve_documents tool with query
2. Gets relevant documents from RAG corpus
3. Uses retrieved context to answer question

Agent: "Based on the retrieved documents, Paris is the capital of France..."
```

## Configuration

### Vector Store Config

```go
config := &rag.WeaviateConfig{
    Host:              "localhost:8080",
    Scheme:            "http",
    APIKey:            "your-api-key",
    DefaultCollection: "documents",
    Timeout:           30 * time.Second,
    VectorIndexConfig: &rag.VectorIndexConfig{
        Distance:       "cosine",
        EfConstruction: 128,
        MaxConnections: 64,
    },
}
```

### Embedding Service Config

```go
// With caching
baseEmbedder := NewSimpleEmbeddingService(384)
cachedEmbedder := NewCachedEmbeddingService(baseEmbedder, 10000)

// Check cache stats
stats := cachedEmbedder.GetCacheStats()
fmt.Printf("Hit rate: %.2f%%\n", stats["hit_rate"].(float64) * 100)
```

### Chunking Config

```go
// Fixed size with overlap
chunker := rag.NewFixedSizeChunker(1000, 200)

// Sentence-based
chunker := rag.NewSentenceChunker(1000)

// Paragraph-based (recommended)
chunker := rag.NewParagraphChunker(1000)
```

## Implementing Custom Vector Store

```go
type MyVectorDB struct {
    // Your vector DB client
}

func (m *MyVectorDB) Store(ctx context.Context, doc *rag.Document) error {
    // Implement storage logic
    return nil
}

func (m *MyVectorDB) Search(ctx context.Context, query *rag.SearchQuery) ([]*rag.SearchResult, error) {
    // Implement search logic
    return results, nil
}

// Implement other VectorStore interface methods...
```

## Testing

### Run All Tests

```bash
go test ./pkg/rag -v
go test ./pkg/tools -run Rag -v
```

### Test Coverage

- ✅ Vector store operations (store, search, delete)
- ✅ Batch operations
- ✅ Similarity search
- ✅ Metadata filtering
- ✅ Min score filtering
- ✅ Embedding generation and caching
- ✅ Document chunking (fixed, sentence, paragraph)
- ✅ Document processing pipeline
- ✅ RAG retrieval tool integration

## Performance Considerations

### Caching

```go
// Cache embeddings to avoid redundant API calls
embedder := NewCachedEmbeddingService(baseEmbedder, 10000)
```

### Batch Operations

```go
// More efficient than individual stores
vectorStore.StoreBatch(ctx, documents)
```

### Chunking Strategy

- **Fixed Size**: Fastest, but may split sentences
- **Sentence**: Better semantic coherence
- **Paragraph**: Best for document structure, recommended

## Comparison with ADK

| Feature | Our Implementation | ADK |
|---------|-------------------|-----|
| **Vector Store** | Vendor-neutral (Weaviate, custom) | Vertex AI only |
| **Embeddings** | Pluggable (any service) | Vertex AI Embeddings |
| **Chunking** | 3 strategies | Basic chunking |
| **Caching** | Built-in embedding cache | Not specified |
| **Distributed** | Partition-aware | Single machine |
| **Metadata Filter** | Full support | Full support |
| **Min Score** | Full support | Full support (similarity_top_k) |
| **Tool Integration** | `retrieve_documents` | `vertex_ai_rag_retrieval` |

## Production Recommendations

### For Small Scale (< 10K documents)

```go
// Use in-memory store
store := rag.NewInMemoryVectorStore()
```

### For Medium Scale (10K - 1M documents)

```go
// Use Weaviate with Docker
// docker run -d -p 8080:8080 semitechnologies/weaviate:latest
store := rag.NewWeaviateStore(config)
```

### For Large Scale (> 1M documents)

Consider:
- Weaviate cluster (distributed)
- Pinecone (managed, scalable)
- Milvus (open-source, distributed)

### Embedding Services

**For Production:**
- OpenAI `text-embedding-ada-002` (1536 dims, $0.0001/1K tokens)
- Cohere Embeddings (1024 dims, $0.0001/1K tokens)
- Sentence Transformers (local, free, 384-768 dims)

**For Testing:**
- `SimpleEmbeddingService` (basic, not semantic)
- `MockEmbeddingService` (deterministic)

## Roadmap

### Planned Features

- [ ] OpenAI Embeddings integration
- [ ] Cohere Embeddings integration
- [ ] Pinecone vector store implementation
- [ ] Qdrant vector store implementation
- [ ] Hybrid search (vector + keyword)
- [ ] Re-ranking support
- [ ] Multi-modal embeddings (text + images)
- [ ] Automatic embedding model selection
- [ ] Persistent caching (Redis, etc.)

## License

Apache License 2.0
