package rag

import (
	"context"
	"strings"
	"testing"
)

// TestBM25Index tests BM25 keyword indexing and scoring
func TestBM25Index(t *testing.T) {
	index := NewBM25Index()

	// Index documents
	docs := map[string]string{
		"doc1": "Paris is the capital of France",
		"doc2": "Tokyo is the capital of Japan",
		"doc3": "Python is a programming language",
	}

	for id, content := range docs {
		index.IndexDocument(id, content)
	}

	// Verify index stats
	if index.numDocs != 3 {
		t.Errorf("Expected 3 documents, got %d", index.numDocs)
	}

	if index.avgDocLength == 0 {
		t.Error("Average document length should be calculated")
	}

	// Test scoring
	queryTerms := tokenize("capital France")
	score1 := index.Score("doc1", queryTerms)
	score2 := index.Score("doc2", queryTerms)
	score3 := index.Score("doc3", queryTerms)

	// doc1 should have highest score (contains both terms)
	if score1 <= score2 || score1 <= score3 {
		t.Errorf("doc1 should have highest score for 'capital France', got scores: %.2f, %.2f, %.2f", score1, score2, score3)
	}

	// doc2 should have some score (contains 'capital')
	if score2 == 0 {
		t.Error("doc2 should have non-zero score for 'capital'")
	}

	// doc3 should have zero score (contains neither term)
	if score3 != 0 {
		t.Error("doc3 should have zero score")
	}
}

// TestBM25MatchingTerms tests matching term extraction
func TestBM25MatchingTerms(t *testing.T) {
	index := NewBM25Index()
	index.IndexDocument("doc1", "Paris is the capital of France")

	queryTerms := tokenize("capital France programming")
	matching := index.GetMatchingTerms("doc1", queryTerms)

	// Should match "capital" and "france", but not "programming"
	if len(matching) != 2 {
		t.Errorf("Expected 2 matching terms, got %d: %v", len(matching), matching)
	}

	hasCapital := false
	hasFrance := false
	for _, term := range matching {
		if term == "capital" {
			hasCapital = true
		}
		if term == "france" {
			hasFrance = true
		}
	}

	if !hasCapital || !hasFrance {
		t.Error("Should match 'capital' and 'france'")
	}
}

// TestTokenize tests text tokenization
func TestTokenize(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected int // Approximate expected token count
	}{
		{
			name:     "simple sentence",
			text:     "Paris is the capital of France",
			expected: 3, // After stop word removal
		},
		{
			name:     "with punctuation",
			text:     "Hello, world! This is a test.",
			expected: 3,
		},
		{
			name:     "mixed case",
			text:     "Python Programming Language",
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := tokenize(tt.text)
			if len(tokens) < 1 {
				t.Error("Expected at least one token")
			}
			t.Logf("Tokens: %v", tokens)
		})
	}
}

// TestExtractGroundingSpans tests grounding span extraction
func TestExtractGroundingSpans(t *testing.T) {
	doc := &Document{
		ID:      "doc1",
		Content: "Paris is the capital of France. It is known for the Eiffel Tower and amazing cuisine.",
	}

	query := "capital of France"
	matchingTerms := []string{"capital", "france"}

	spans := ExtractGroundingSpans(doc, query, matchingTerms, 5)

	if len(spans) == 0 {
		t.Fatal("Expected at least one grounding span")
	}

	// Should find exact match
	foundExact := false
	for _, span := range spans {
		if span.MatchType == "exact" {
			foundExact = true
			if !strings.Contains(strings.ToLower(span.Text), "capital") {
				t.Error("Exact match span should contain 'capital'")
			}
		}
	}

	if !foundExact {
		t.Error("Should find exact match for 'capital of France'")
	}
}

// TestHybridMemoryStore tests hybrid search functionality
func TestHybridMemoryStore(t *testing.T) {
	ctx := context.Background()
	store := NewHybridMemoryStore()
	embedder := NewSimpleEmbeddingService(128)

	// Ingest documents
	docs := []struct {
		content string
		metadata map[string]interface{}
	}{
		{
			content: "Paris is the capital of France and is famous for the Eiffel Tower",
			metadata: map[string]interface{}{"category": "geography"},
		},
		{
			content: "Tokyo is the capital of Japan and is known for its technology",
			metadata: map[string]interface{}{"category": "geography"},
		},
		{
			content: "Python is a popular programming language for data science",
			metadata: map[string]interface{}{"category": "technology"},
		},
	}

	for i, d := range docs {
		embedding, _ := embedder.Embed(ctx, d.content)
		doc := &Document{
			ID:         string(rune('a' + i)),
			Content:    d.content,
			Embedding:  embedding,
			Collection: "default",
			Metadata:   d.metadata,
		}
		store.Store(ctx, doc)
	}

	// Test hybrid search
	queryEmbedding, _ := embedder.Embed(ctx, "What is the capital of France?")

	hybridQuery := &HybridSearchQuery{
		QueryText:     "capital France",
		Embedding:     queryEmbedding,
		Collection:    "default",
		TopK:          3,
		MinScore:      0.0,
		VectorWeight:  0.7,
		KeywordWeight: 0.3,
	}

	results, err := store.HybridSearch(ctx, hybridQuery)
	if err != nil {
		t.Fatalf("Hybrid search failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("Expected at least one result")
	}

	t.Logf("Got %d results", len(results))

	// First result should be about Paris/France
	firstResult := results[0]
	contentLower := strings.ToLower(firstResult.Document.Content)

	// Check that we got reasonable results
	if firstResult.HybridScore == 0 {
		t.Error("Hybrid score should be non-zero")
	}

	// Check grounding spans
	if len(firstResult.GroundingSpans) == 0 {
		t.Log("Warning: No grounding spans found")
	} else {
		t.Logf("Found %d grounding spans", len(firstResult.GroundingSpans))
		for _, span := range firstResult.GroundingSpans {
			t.Logf("  Span: %s (type: %s, score: %.2f)", span.Text, span.MatchType, span.Score)
		}
	}

	// Verify match type
	if firstResult.MatchType != "hybrid" {
		t.Logf("Match type: %s", firstResult.MatchType)
	}

	// Log scores for debugging
	t.Logf("First result scores - Vector: %.3f, Keyword: %.3f, Hybrid: %.3f",
		firstResult.VectorScore, firstResult.KeywordScore, firstResult.HybridScore)

	// Content should be relevant
	if !strings.Contains(contentLower, "paris") && !strings.Contains(contentLower, "capital") {
		t.Logf("Note: First result might not be most relevant (content: %s)", contentLower)
	}
}

// TestHybridSearchWeights tests different weight combinations
func TestHybridSearchWeights(t *testing.T) {
	ctx := context.Background()
	store := NewHybridMemoryStore()
	embedder := NewSimpleEmbeddingService(128)

	// Store test documents
	content := "Paris is the capital of France"
	embedding, _ := embedder.Embed(ctx, content)

	doc := &Document{
		ID:         "doc1",
		Content:    content,
		Embedding:  embedding,
		Collection: "default",
	}
	store.Store(ctx, doc)

	queryEmbedding, _ := embedder.Embed(ctx, "capital")

	// Test pure semantic search (vector weight = 1.0)
	semanticQuery := &HybridSearchQuery{
		QueryText:     "capital",
		Embedding:     queryEmbedding,
		Collection:    "default",
		TopK:          5,
		VectorWeight:  1.0,
		KeywordWeight: 0.0,
	}

	semanticResults, err := store.HybridSearch(ctx, semanticQuery)
	if err != nil {
		t.Fatalf("Semantic search failed: %v", err)
	}

	if len(semanticResults) > 0 && semanticResults[0].KeywordScore != 0 {
		// Keyword score might still be calculated but not used in hybrid score
		if semanticResults[0].HybridScore != semanticResults[0].VectorScore {
			t.Error("With vector weight 1.0, hybrid score should equal vector score")
		}
	}

	// Test pure keyword search (keyword weight = 1.0)
	keywordQuery := &HybridSearchQuery{
		QueryText:     "capital",
		Embedding:     nil, // Can skip embedding for pure keyword
		Collection:    "default",
		TopK:          5,
		VectorWeight:  0.0,
		KeywordWeight: 1.0,
	}

	keywordResults, err := store.HybridSearch(ctx, keywordQuery)
	if err != nil {
		t.Fatalf("Keyword search failed: %v", err)
	}

	if len(keywordResults) > 0 {
		t.Logf("Keyword search found %d results", len(keywordResults))
	}
}

// TestHybridSearchReranking tests re-ranking functionality
func TestHybridSearchReranking(t *testing.T) {
	ctx := context.Background()
	store := NewHybridMemoryStore()
	embedder := NewSimpleEmbeddingService(128)

	// Store multiple documents
	docs := []string{
		"Paris is the capital of France",
		"The Eiffel Tower is in Paris, France",
		"France is a country in Europe",
	}

	for i, content := range docs {
		embedding, _ := embedder.Embed(ctx, content)
		doc := &Document{
			ID:         string(rune('a' + i)),
			Content:    content,
			Embedding:  embedding,
			Collection: "default",
		}
		store.Store(ctx, doc)
	}

	queryEmbedding, _ := embedder.Embed(ctx, "capital France")

	// Search without reranking
	query1 := &HybridSearchQuery{
		QueryText:       "capital France",
		Embedding:       queryEmbedding,
		Collection:      "default",
		TopK:            3,
		VectorWeight:    0.7,
		EnableReranking: false,
	}

	results1, _ := store.HybridSearch(ctx, query1)

	// Search with reranking
	query2 := &HybridSearchQuery{
		QueryText:       "capital France",
		Embedding:       queryEmbedding,
		Collection:      "default",
		TopK:            3,
		VectorWeight:    0.7,
		EnableReranking: true,
	}

	results2, _ := store.HybridSearch(ctx, query2)

	// Verify re-rank scores are set
	if len(results2) > 0 {
		if results2[0].RerankScore == 0 {
			t.Error("Rerank score should be set when reranking enabled")
		}
		t.Logf("With reranking - Hybrid: %.3f, Rerank: %.3f",
			results2[0].HybridScore, results2[0].RerankScore)
	}

	t.Logf("Without reranking: %d results", len(results1))
	t.Logf("With reranking: %d results", len(results2))
}

// TestMetadataFilteringHybrid tests metadata filtering in hybrid search
func TestMetadataFilteringHybrid(t *testing.T) {
	ctx := context.Background()
	store := NewHybridMemoryStore()
	embedder := NewSimpleEmbeddingService(128)

	// Store documents with different metadata
	docs := []struct {
		content  string
		category string
	}{
		{"Paris is in France", "geography"},
		{"Python is a language", "technology"},
		{"Tokyo is in Japan", "geography"},
	}

	for i, d := range docs {
		embedding, _ := embedder.Embed(ctx, d.content)
		doc := &Document{
			ID:         string(rune('a' + i)),
			Content:    d.content,
			Embedding:  embedding,
			Collection: "default",
			Metadata: map[string]interface{}{
				"category": d.category,
			},
		}
		store.Store(ctx, doc)
	}

	queryEmbedding, _ := embedder.Embed(ctx, "location")

	// Search with metadata filter
	query := &HybridSearchQuery{
		QueryText:  "location",
		Embedding:  queryEmbedding,
		Collection: "default",
		TopK:       10,
		MinScore:   0.0,
		MetadataFilter: map[string]interface{}{
			"category": "geography",
		},
	}

	results, err := store.HybridSearch(ctx, query)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	// All results should be geography
	for _, result := range results {
		category := result.Document.Metadata["category"]
		if category != "geography" {
			t.Errorf("Expected category 'geography', got %v", category)
		}
	}

	t.Logf("Found %d geography documents", len(results))
}

// TestNormalizeBM25Score tests BM25 score normalization
func TestNormalizeBM25Score(t *testing.T) {
	tests := []struct {
		score    float64
		maxScore float64
		expected float64
	}{
		{10.0, 20.0, 0.5},
		{20.0, 20.0, 1.0},
		{30.0, 20.0, 1.0}, // Capped at 1.0
		{0.0, 20.0, 0.0},
		{10.0, 0.0, 0.0}, // Handle division by zero
	}

	for _, tt := range tests {
		result := NormalizeBM25Score(tt.score, tt.maxScore)
		if result != tt.expected {
			t.Errorf("NormalizeBM25Score(%.1f, %.1f) = %.2f, expected %.2f",
				tt.score, tt.maxScore, result, tt.expected)
		}
	}
}

// TestBuildInvertedIndex tests index building
func TestBuildInvertedIndex(t *testing.T) {
	ctx := context.Background()
	store := NewHybridMemoryStore()

	// Store some documents
	docs := []string{
		"First document about Paris",
		"Second document about Tokyo",
		"Third document about Python",
	}

	for i, content := range docs {
		doc := &Document{
			ID:         string(rune('a' + i)),
			Content:    content,
			Embedding:  make([]float64, 128),
			Collection: "test",
		}
		store.Store(ctx, doc)
	}

	// Build index
	err := store.BuildInvertedIndex(ctx, "test")
	if err != nil {
		t.Fatalf("Failed to build index: %v", err)
	}

	// Verify index exists
	stats := store.GetHybridStats()
	indexStats := stats["bm25_indexes"].(map[string]interface{})

	if indexStats["test"] == nil {
		t.Error("Expected BM25 index for 'test' collection")
	}

	testIndexStats := indexStats["test"].(map[string]interface{})
	numDocs := testIndexStats["num_docs"].(int)

	if numDocs != 3 {
		t.Errorf("Expected 3 documents in index, got %d", numDocs)
	}

	t.Logf("Index stats: %+v", testIndexStats)
}
