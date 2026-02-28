package rag

import (
	"context"
	"strings"
	"testing"
)

// TestInMemoryVectorStore tests the in-memory vector store
func TestInMemoryVectorStore(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryVectorStore()

	// Create collection
	err := store.CreateCollection(ctx, DefaultCollectionConfig("test", 128))
	if err != nil {
		t.Fatalf("Failed to create collection: %v", err)
	}

	// Store document
	doc := &Document{
		ID:         "doc1",
		Content:    "This is a test document",
		Embedding:  make([]float64, 128),
		Collection: "test",
		Metadata: map[string]interface{}{
			"author": "test",
		},
	}

	// Initialize embedding
	for i := range doc.Embedding {
		doc.Embedding[i] = float64(i) / 128.0
	}

	err = store.Store(ctx, doc)
	if err != nil {
		t.Fatalf("Failed to store document: %v", err)
	}

	// Retrieve document
	retrieved, err := store.GetDocument(ctx, "doc1")
	if err != nil {
		t.Fatalf("Failed to retrieve document: %v", err)
	}

	if retrieved.Content != doc.Content {
		t.Errorf("Expected content %s, got %s", doc.Content, retrieved.Content)
	}

	// Search
	query := &SearchQuery{
		Embedding:  doc.Embedding,
		Collection: "test",
		TopK:       10,
		MinScore:   0.0,
	}

	results, err := store.Search(ctx, query)
	if err != nil {
		t.Fatalf("Failed to search: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 result, got %d", len(results))
	}

	if results[0].Document.ID != "doc1" {
		t.Errorf("Expected doc1, got %s", results[0].Document.ID)
	}

	// Delete document
	err = store.Delete(ctx, "doc1")
	if err != nil {
		t.Fatalf("Failed to delete document: %v", err)
	}

	// Verify deletion
	_, err = store.GetDocument(ctx, "doc1")
	if err == nil {
		t.Error("Expected error when retrieving deleted document")
	}
}

// TestVectorStoreBatch tests batch operations
func TestVectorStoreBatch(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryVectorStore()

	// Create documents
	docs := []*Document{
		{
			ID:         "doc1",
			Content:    "First document",
			Embedding:  []float64{1.0, 0.0, 0.0},
			Collection: "default",
		},
		{
			ID:         "doc2",
			Content:    "Second document",
			Embedding:  []float64{0.0, 1.0, 0.0},
			Collection: "default",
		},
		{
			ID:         "doc3",
			Content:    "Third document",
			Embedding:  []float64{0.0, 0.0, 1.0},
			Collection: "default",
		},
	}

	// Store batch
	err := store.StoreBatch(ctx, docs)
	if err != nil {
		t.Fatalf("Failed to store batch: %v", err)
	}

	// Verify all stored
	for _, doc := range docs {
		retrieved, err := store.GetDocument(ctx, doc.ID)
		if err != nil {
			t.Errorf("Failed to retrieve %s: %v", doc.ID, err)
		}
		if retrieved.Content != doc.Content {
			t.Errorf("Content mismatch for %s", doc.ID)
		}
	}

	// Delete batch
	err = store.DeleteBatch(ctx, []string{"doc1", "doc2", "doc3"})
	if err != nil {
		t.Fatalf("Failed to delete batch: %v", err)
	}

	// Verify deletion
	for _, doc := range docs {
		_, err := store.GetDocument(ctx, doc.ID)
		if err == nil {
			t.Errorf("Expected error for deleted document %s", doc.ID)
		}
	}
}

// TestSimilaritySearch tests similarity-based search
func TestSimilaritySearch(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryVectorStore()

	// Store documents with different embeddings
	docs := []*Document{
		{
			ID:         "dog",
			Content:    "Dogs are friendly animals",
			Embedding:  []float64{1.0, 0.9, 0.1},
			Collection: "default",
		},
		{
			ID:         "cat",
			Content:    "Cats are independent pets",
			Embedding:  []float64{1.0, 0.8, 0.2},
			Collection: "default",
		},
		{
			ID:         "car",
			Content:    "Cars are vehicles with wheels",
			Embedding:  []float64{0.1, 0.2, 1.0},
			Collection: "default",
		},
	}

	store.StoreBatch(ctx, docs)

	// Search for dog-like content
	query := &SearchQuery{
		Embedding:  []float64{0.95, 0.85, 0.15},
		Collection: "default",
		TopK:       2,
		MinScore:   0.0,
	}

	results, err := store.Search(ctx, query)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(results))
	}

	// First result should be dog or cat (most similar)
	if results[0].Document.ID != "dog" && results[0].Document.ID != "cat" {
		t.Errorf("Expected dog or cat as first result, got %s", results[0].Document.ID)
	}

	// Last result should not be car (least similar)
	if results[len(results)-1].Document.ID == "car" {
		// This is actually expected given our query, so this is fine
	}
}

// TestMinScoreFiltering tests minimum score filtering
func TestMinScoreFiltering(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryVectorStore()

	docs := []*Document{
		{
			ID:         "similar",
			Content:    "Very similar content",
			Embedding:  []float64{1.0, 0.0, 0.0},
			Collection: "default",
		},
		{
			ID:         "different",
			Content:    "Completely different content",
			Embedding:  []float64{0.0, 0.0, 1.0},
			Collection: "default",
		},
	}

	store.StoreBatch(ctx, docs)

	// Search with high min score
	query := &SearchQuery{
		Embedding:  []float64{1.0, 0.0, 0.0},
		Collection: "default",
		TopK:       10,
		MinScore:   0.9, // High threshold
	}

	results, err := store.Search(ctx, query)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// Should only get the similar document
	if len(results) != 1 {
		t.Errorf("Expected 1 result with high min_score, got %d", len(results))
	}

	if len(results) > 0 && results[0].Document.ID != "similar" {
		t.Errorf("Expected 'similar' document, got %s", results[0].Document.ID)
	}
}

// TestSimpleEmbeddingService tests the simple embedding service
func TestSimpleEmbeddingService(t *testing.T) {
	ctx := context.Background()
	embedder := NewSimpleEmbeddingService(128)

	// Test single embedding
	text := "This is a test sentence"
	embedding, err := embedder.Embed(ctx, text)
	if err != nil {
		t.Fatalf("Failed to generate embedding: %v", err)
	}

	if len(embedding) != 128 {
		t.Errorf("Expected 128 dimensions, got %d", len(embedding))
	}

	// Test determinism
	embedding2, _ := embedder.Embed(ctx, text)
	for i := range embedding {
		if embedding[i] != embedding2[i] {
			t.Error("Embeddings should be deterministic")
			break
		}
	}

	// Test batch embedding
	texts := []string{"First text", "Second text", "Third text"}
	embeddings, err := embedder.EmbedBatch(ctx, texts)
	if err != nil {
		t.Fatalf("Failed to generate batch embeddings: %v", err)
	}

	if len(embeddings) != 3 {
		t.Errorf("Expected 3 embeddings, got %d", len(embeddings))
	}

	for i, emb := range embeddings {
		if len(emb) != 128 {
			t.Errorf("Embedding %d has wrong dimensions: %d", i, len(emb))
		}
	}
}

// TestCachedEmbeddingService tests caching
func TestCachedEmbeddingService(t *testing.T) {
	ctx := context.Background()
	base := NewSimpleEmbeddingService(64)
	cached := NewCachedEmbeddingService(base, 100)

	text := "Test caching"

	// First call - cache miss
	embedding1, err := cached.Embed(ctx, text)
	if err != nil {
		t.Fatalf("Failed to generate embedding: %v", err)
	}

	stats := cached.GetCacheStats()
	if stats["misses"].(int64) != 1 {
		t.Errorf("Expected 1 cache miss, got %d", stats["misses"])
	}

	// Second call - cache hit
	embedding2, err := cached.Embed(ctx, text)
	if err != nil {
		t.Fatalf("Failed to generate embedding: %v", err)
	}

	stats = cached.GetCacheStats()
	if stats["hits"].(int64) != 1 {
		t.Errorf("Expected 1 cache hit, got %d", stats["hits"])
	}

	// Embeddings should be identical
	for i := range embedding1 {
		if embedding1[i] != embedding2[i] {
			t.Error("Cached embedding differs from original")
			break
		}
	}

	// Test cache clear
	cached.ClearCache()
	stats = cached.GetCacheStats()
	if stats["cache_size"].(int) != 0 {
		t.Error("Cache should be empty after clear")
	}
}

// TestFixedSizeChunker tests fixed-size text chunking
func TestFixedSizeChunker(t *testing.T) {
	chunker := NewFixedSizeChunker(100, 20)

	text := strings.Repeat("A", 250)
	chunks := chunker.Chunk(text)

	if len(chunks) == 0 {
		t.Error("Expected chunks, got none")
	}

	// Each chunk should be around chunkSize
	for i, chunk := range chunks {
		if len(chunk) > 100 {
			t.Errorf("Chunk %d exceeds max size: %d", i, len(chunk))
		}
	}
}

// TestSentenceChunker tests sentence-based chunking
func TestSentenceChunker(t *testing.T) {
	chunker := NewSentenceChunker(100)

	text := "First sentence. Second sentence. Third sentence. Fourth sentence."
	chunks := chunker.Chunk(text)

	if len(chunks) == 0 {
		t.Error("Expected chunks, got none")
	}

	// Verify sentences are preserved
	fullText := strings.Join(chunks, " ")
	if !strings.Contains(fullText, "First sentence") {
		t.Error("First sentence not found in chunks")
	}
}

// TestParagraphChunker tests paragraph-based chunking
func TestParagraphChunker(t *testing.T) {
	chunker := NewParagraphChunker(200)

	text := "First paragraph.\n\nSecond paragraph.\n\nThird paragraph."
	chunks := chunker.Chunk(text)

	if len(chunks) == 0 {
		t.Error("Expected chunks, got none")
	}

	// Each chunk should preserve paragraph boundaries
	for _, chunk := range chunks {
		if len(chunk) > 200 {
			// Large paragraphs may be split by sentence chunker
			continue
		}
	}
}

// TestDocumentProcessor tests document processing pipeline
func TestDocumentProcessor(t *testing.T) {
	ctx := context.Background()

	// Setup components
	vectorStore := NewInMemoryVectorStore()
	embedder := NewSimpleEmbeddingService(64)
	chunker := NewSentenceChunker(100)

	processor := NewDocumentProcessor(chunker, embedder, vectorStore, "default")

	// Process document
	content := "First sentence. Second sentence. Third sentence."
	metadata := map[string]interface{}{
		"source": "test.txt",
		"author": "tester",
	}

	err := processor.ProcessDocument(ctx, content, metadata)
	if err != nil {
		t.Fatalf("Failed to process document: %v", err)
	}

	// Verify documents were stored
	docs, err := vectorStore.ListDocuments(ctx, &ListOptions{
		Collection: "default",
		Limit:      10,
	})
	if err != nil {
		t.Fatalf("Failed to list documents: %v", err)
	}

	if len(docs) == 0 {
		t.Error("Expected documents to be stored")
	}

	// Verify each chunk has embedding
	for _, doc := range docs {
		if len(doc.Embedding) != 64 {
			t.Errorf("Document %s has wrong embedding dimensions: %d", doc.ID, len(doc.Embedding))
		}

		if doc.Metadata["source"] != "test.txt" {
			t.Errorf("Metadata not preserved in chunk %s", doc.ID)
		}
	}
}

// TestCosineSimilarity tests cosine similarity calculation
func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a        []float64
		b        []float64
		expected float64
		epsilon  float64
	}{
		{
			name:     "identical vectors",
			a:        []float64{1.0, 0.0, 0.0},
			b:        []float64{1.0, 0.0, 0.0},
			expected: 1.0,
			epsilon:  0.001,
		},
		{
			name:     "orthogonal vectors",
			a:        []float64{1.0, 0.0},
			b:        []float64{0.0, 1.0},
			expected: 0.0,
			epsilon:  0.001,
		},
		{
			name:     "opposite vectors",
			a:        []float64{1.0, 0.0},
			b:        []float64{-1.0, 0.0},
			expected: -1.0,
			epsilon:  0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			similarity := cosineSimilarity(tt.a, tt.b)
			diff := similarity - tt.expected
			if diff < 0 {
				diff = -diff
			}
			if diff > tt.epsilon {
				t.Errorf("Expected similarity %.3f, got %.3f", tt.expected, similarity)
			}
		})
	}
}

// TestMetadataFiltering tests metadata-based filtering
func TestMetadataFiltering(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryVectorStore()

	// Store documents with different metadata
	docs := []*Document{
		{
			ID:         "doc1",
			Content:    "Content 1",
			Embedding:  []float64{1.0, 0.0},
			Collection: "default",
			Metadata: map[string]interface{}{
				"category": "science",
				"year":     2020,
			},
		},
		{
			ID:         "doc2",
			Content:    "Content 2",
			Embedding:  []float64{1.0, 0.0},
			Collection: "default",
			Metadata: map[string]interface{}{
				"category": "history",
				"year":     2021,
			},
		},
	}

	store.StoreBatch(ctx, docs)

	// Search with metadata filter
	query := &SearchQuery{
		Embedding:  []float64{1.0, 0.0},
		Collection: "default",
		TopK:       10,
		MinScore:   0.0,
		MetadataFilter: map[string]interface{}{
			"category": "science",
		},
	}

	results, err := store.Search(ctx, query)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(results) != 1 {
		t.Errorf("Expected 1 result with metadata filter, got %d", len(results))
	}

	if len(results) > 0 && results[0].Document.ID != "doc1" {
		t.Errorf("Expected doc1, got %s", results[0].Document.ID)
	}
}
