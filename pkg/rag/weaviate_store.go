package rag

import (
	"context"
	"fmt"
	"time"
)

// WeaviateStore implements VectorStore using Weaviate
// NOTE: This is a reference implementation. For production use:
// - Install Weaviate Go client: go get github.com/weaviate/weaviate-go-client/v4
// - Uncomment the import and implementation
// - For now, we provide an in-memory implementation for testing

type WeaviateStore struct {
	config      *WeaviateConfig
	documents   map[string]*Document // In-memory storage for demo
	collections map[string]*CollectionConfig
}

// WeaviateConfig contains Weaviate-specific configuration
type WeaviateConfig struct {
	Host               string
	Scheme             string // http or https
	APIKey             string
	DefaultCollection  string
	Timeout            time.Duration
	VectorIndexConfig  *VectorIndexConfig
}

// VectorIndexConfig contains vector index configuration
type VectorIndexConfig struct {
	Distance            string // cosine, dot, l2-squared
	EfConstruction      int    // HNSW ef construction
	MaxConnections      int    // HNSW max connections
	EfSearch            int    // HNSW ef search
	VectorCacheMaxSize  int    // Vector cache size
}

// NewWeaviateStore creates a new Weaviate vector store
// For production: replace with actual Weaviate client
func NewWeaviateStore(config *WeaviateConfig) (*WeaviateStore, error) {
	if config == nil {
		config = DefaultWeaviateConfig()
	}

	// TODO: In production, initialize actual Weaviate client here:
	// client, err := weaviate.NewClient(weaviate.Config{
	//     Host:   config.Host,
	//     Scheme: config.Scheme,
	//     AuthConfig: auth.ApiKey{Value: config.APIKey},
	// })

	return &WeaviateStore{
		config:      config,
		documents:   make(map[string]*Document),
		collections: make(map[string]*CollectionConfig),
	}, nil
}

// DefaultWeaviateConfig returns default Weaviate configuration
func DefaultWeaviateConfig() *WeaviateConfig {
	return &WeaviateConfig{
		Host:              "localhost:8080",
		Scheme:            "http",
		DefaultCollection: "documents",
		Timeout:           30 * time.Second,
		VectorIndexConfig: &VectorIndexConfig{
			Distance:       "cosine",
			EfConstruction: 128,
			MaxConnections: 64,
			EfSearch:       64,
		},
	}
}

func (w *WeaviateStore) Store(ctx context.Context, doc *Document) error {
	if doc.ID == "" {
		return fmt.Errorf("document ID is required")
	}

	if doc.Collection == "" {
		doc.Collection = w.config.DefaultCollection
	}

	// TODO: In production, use actual Weaviate client:
	// err := w.client.Data().Creator().
	//     WithClassName(doc.Collection).
	//     WithID(doc.ID).
	//     WithProperties(w.docToProperties(doc)).
	//     WithVector(doc.Embedding).
	//     Do(ctx)

	// For now, store in memory
	w.documents[doc.ID] = doc
	return nil
}

func (w *WeaviateStore) StoreBatch(ctx context.Context, docs []*Document) error {
	// TODO: In production, use Weaviate batch API for efficiency
	for _, doc := range docs {
		if err := w.Store(ctx, doc); err != nil {
			return fmt.Errorf("failed to store document %s: %w", doc.ID, err)
		}
	}
	return nil
}

func (w *WeaviateStore) Search(ctx context.Context, query *SearchQuery) ([]*SearchResult, error) {
	if query.Embedding == nil {
		return nil, fmt.Errorf("query embedding is required")
	}

	collection := query.Collection
	if collection == "" {
		collection = w.config.DefaultCollection
	}

	// TODO: In production, use Weaviate nearVector search:
	// result, err := w.client.GraphQL().Get().
	//     WithClassName(collection).
	//     WithNearVector(&graphql.NearVectorArgumentBuilder{}.
	//         WithVector(query.Embedding)).
	//     WithLimit(query.TopK).
	//     WithFields(graphql.Field{Name: "_additional { certainty distance }"}).
	//     Do(ctx)

	// For now, implement simple in-memory search
	results := make([]*SearchResult, 0)

	for _, doc := range w.documents {
		if doc.Collection != collection {
			continue
		}

		// Calculate cosine similarity
		similarity := cosineSimilarity(query.Embedding, doc.Embedding)

		if similarity >= query.MinScore {
			results = append(results, &SearchResult{
				Document: doc,
				Score:    similarity,
				Distance: 1.0 - similarity,
			})
		}
	}

	// Sort by score (descending)
	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	// Limit to top-k
	if len(results) > query.TopK {
		results = results[:query.TopK]
	}

	return results, nil
}

func (w *WeaviateStore) Delete(ctx context.Context, docID string) error {
	// TODO: In production, use Weaviate delete API
	delete(w.documents, docID)
	return nil
}

func (w *WeaviateStore) DeleteBatch(ctx context.Context, docIDs []string) error {
	for _, id := range docIDs {
		if err := w.Delete(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (w *WeaviateStore) GetDocument(ctx context.Context, docID string) (*Document, error) {
	doc, exists := w.documents[docID]
	if !exists {
		return nil, fmt.Errorf("document not found: %s", docID)
	}
	return doc, nil
}

func (w *WeaviateStore) ListDocuments(ctx context.Context, opts *ListOptions) ([]*Document, error) {
	collection := opts.Collection
	if collection == "" {
		collection = w.config.DefaultCollection
	}

	docs := make([]*Document, 0)
	for _, doc := range w.documents {
		if doc.Collection == collection {
			docs = append(docs, doc)
		}
	}

	// Simple pagination
	start := opts.Offset
	end := opts.Offset + opts.Limit

	if start >= len(docs) {
		return []*Document{}, nil
	}

	if end > len(docs) {
		end = len(docs)
	}

	if opts.Limit == 0 {
		return docs[start:], nil
	}

	return docs[start:end], nil
}

func (w *WeaviateStore) CreateCollection(ctx context.Context, config *CollectionConfig) error {
	// TODO: In production, create Weaviate schema:
	// schema := &models.Class{
	//     Class:             config.Name,
	//     Description:       config.Description,
	//     VectorIndexConfig: w.buildVectorIndexConfig(config),
	//     Properties:        w.buildProperties(config.Properties),
	// }
	// err := w.client.Schema().ClassCreator().WithClass(schema).Do(ctx)

	w.collections[config.Name] = config
	return nil
}

func (w *WeaviateStore) DeleteCollection(ctx context.Context, collectionName string) error {
	// TODO: In production, delete Weaviate class
	delete(w.collections, collectionName)

	// Remove all documents in this collection
	for id, doc := range w.documents {
		if doc.Collection == collectionName {
			delete(w.documents, id)
		}
	}

	return nil
}

func (w *WeaviateStore) Close() error {
	// TODO: In production, close Weaviate client connection
	return nil
}

func (w *WeaviateStore) HealthCheck(ctx context.Context) error {
	// TODO: In production, check Weaviate health endpoint
	return nil
}

// Helper functions

func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0.0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0.0
	}

	return dotProduct / (sqrt(normA) * sqrt(normB))
}

func sqrt(x float64) float64 {
	if x == 0 {
		return 0
	}
	z := x
	for i := 0; i < 10; i++ {
		z = (z + x/z) / 2
	}
	return z
}

// Production Implementation Note:
//
// To use actual Weaviate in production:
//
// 1. Install Weaviate Go client:
//    go get github.com/weaviate/weaviate-go-client/v4
//
// 2. Import the client:
//    import "github.com/weaviate/weaviate-go-client/v4/weaviate"
//
// 3. Initialize client in NewWeaviateStore:
//    client, err := weaviate.NewClient(weaviate.Config{
//        Host:   config.Host,
//        Scheme: config.Scheme,
//        AuthConfig: auth.ApiKey{Value: config.APIKey},
//    })
//
// 4. Replace in-memory operations with actual Weaviate API calls
//
// 5. For local development, run Weaviate with Docker:
//    docker run -d -p 8080:8080 semitechnologies/weaviate:latest
//
// See: https://weaviate.io/developers/weaviate/client-libraries/go
