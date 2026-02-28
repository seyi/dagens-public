package rag

import (
	"context"
	"fmt"
	"sync"
)

// InMemoryVectorStore is a simple in-memory vector store for testing and demos
// NOT suitable for production use (no persistence, limited scale)
type InMemoryVectorStore struct {
	documents   map[string]*Document
	collections map[string]*CollectionConfig
	mu          sync.RWMutex
}

// NewInMemoryVectorStore creates a new in-memory vector store
func NewInMemoryVectorStore() *InMemoryVectorStore {
	return &InMemoryVectorStore{
		documents:   make(map[string]*Document),
		collections: make(map[string]*CollectionConfig),
	}
}

func (m *InMemoryVectorStore) Store(ctx context.Context, doc *Document) error {
	if doc.ID == "" {
		return fmt.Errorf("document ID is required")
	}

	if doc.Collection == "" {
		doc.Collection = "default"
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.documents[doc.ID] = doc
	return nil
}

func (m *InMemoryVectorStore) StoreBatch(ctx context.Context, docs []*Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, doc := range docs {
		if doc.ID == "" {
			return fmt.Errorf("document ID is required")
		}
		if doc.Collection == "" {
			doc.Collection = "default"
		}
		m.documents[doc.ID] = doc
	}

	return nil
}

func (m *InMemoryVectorStore) Search(ctx context.Context, query *SearchQuery) ([]*SearchResult, error) {
	if query.Embedding == nil {
		return nil, fmt.Errorf("query embedding is required for search")
	}

	collection := query.Collection
	if collection == "" {
		collection = "default"
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	results := make([]*SearchResult, 0)

	// Search through all documents
	for _, doc := range m.documents {
		// Filter by collection
		if doc.Collection != collection {
			continue
		}

		// Filter by metadata if specified
		if !matchesMetadataFilter(doc.Metadata, query.MetadataFilter) {
			continue
		}

		// Calculate similarity
		similarity := cosineSimilarity(query.Embedding, doc.Embedding)

		// Apply minimum score threshold
		if similarity >= query.MinScore {
			// Create result (copy document if embeddings not included)
			resultDoc := doc
			if !query.IncludeEmbeddings {
				// Copy document without embedding
				resultDoc = &Document{
					ID:         doc.ID,
					Content:    doc.Content,
					Metadata:   doc.Metadata,
					Collection: doc.Collection,
					Source:     doc.Source,
					CreatedAt:  doc.CreatedAt,
					UpdatedAt:  doc.UpdatedAt,
				}
			}

			results = append(results, &SearchResult{
				Document: resultDoc,
				Score:    similarity,
				Distance: 1.0 - similarity,
			})
		}
	}

	// Sort by score (descending)
	sortSearchResults(results)

	// Limit to top-k
	if len(results) > query.TopK {
		results = results[:query.TopK]
	}

	return results, nil
}

func (m *InMemoryVectorStore) Delete(ctx context.Context, docID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.documents[docID]; !exists {
		return fmt.Errorf("document not found: %s", docID)
	}

	delete(m.documents, docID)
	return nil
}

func (m *InMemoryVectorStore) DeleteBatch(ctx context.Context, docIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, id := range docIDs {
		delete(m.documents, id)
	}

	return nil
}

func (m *InMemoryVectorStore) GetDocument(ctx context.Context, docID string) (*Document, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	doc, exists := m.documents[docID]
	if !exists {
		return nil, fmt.Errorf("document not found: %s", docID)
	}

	return doc, nil
}

func (m *InMemoryVectorStore) ListDocuments(ctx context.Context, opts *ListOptions) ([]*Document, error) {
	collection := opts.Collection
	if collection == "" {
		collection = "default"
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	docs := make([]*Document, 0)
	for _, doc := range m.documents {
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

	if opts.Limit == 0 {
		return docs[start:], nil
	}

	if end > len(docs) {
		end = len(docs)
	}

	return docs[start:end], nil
}

func (m *InMemoryVectorStore) CreateCollection(ctx context.Context, config *CollectionConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.collections[config.Name]; exists {
		return fmt.Errorf("collection already exists: %s", config.Name)
	}

	m.collections[config.Name] = config
	return nil
}

func (m *InMemoryVectorStore) DeleteCollection(ctx context.Context, collectionName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.collections, collectionName)

	// Remove all documents in this collection
	for id, doc := range m.documents {
		if doc.Collection == collectionName {
			delete(m.documents, id)
		}
	}

	return nil
}

func (m *InMemoryVectorStore) Close() error {
	// No resources to clean up for in-memory store
	return nil
}

func (m *InMemoryVectorStore) HealthCheck(ctx context.Context) error {
	// In-memory store is always healthy
	return nil
}

// GetStats returns statistics about the in-memory store
func (m *InMemoryVectorStore) GetStats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	collectionCounts := make(map[string]int)
	for _, doc := range m.documents {
		collectionCounts[doc.Collection]++
	}

	return map[string]interface{}{
		"total_documents": len(m.documents),
		"total_collections": len(m.collections),
		"collection_counts": collectionCounts,
	}
}

// Helper functions

func matchesMetadataFilter(metadata, filter map[string]interface{}) bool {
	if filter == nil || len(filter) == 0 {
		return true
	}

	for key, filterValue := range filter {
		metadataValue, exists := metadata[key]
		if !exists {
			return false
		}

		// Simple equality check
		if metadataValue != filterValue {
			return false
		}
	}

	return true
}

func sortSearchResults(results []*SearchResult) {
	// Simple bubble sort by score (descending)
	n := len(results)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			if results[j].Score < results[j+1].Score {
				results[j], results[j+1] = results[j+1], results[j]
			}
		}
	}
}
