package memory

import (
	"context"
	"testing"
	"time"
)

func TestInMemoryStore(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()

	t.Run("Store and Retrieve", func(t *testing.T) {
		err := store.Store(ctx, "agent-1", "key1", "value1", nil)
		if err != nil {
			t.Fatalf("Failed to store: %v", err)
		}

		entry, err := store.Retrieve(ctx, "agent-1", "key1")
		if err != nil {
			t.Fatalf("Failed to retrieve: %v", err)
		}

		if entry.Value != "value1" {
			t.Errorf("Expected value 'value1', got '%v'", entry.Value)
		}

		if entry.Key != "key1" {
			t.Errorf("Expected key 'key1', got '%s'", entry.Key)
		}
	})

	t.Run("Store with Metadata", func(t *testing.T) {
		metadata := map[string]interface{}{
			"tags":       []string{"important", "user-preference"},
			"importance": 0.9,
		}

		err := store.Store(ctx, "agent-2", "pref1", "detailed explanations", metadata)
		if err != nil {
			t.Fatalf("Failed to store with metadata: %v", err)
		}

		entry, _ := store.Retrieve(ctx, "agent-2", "pref1")

		if entry.Importance != 0.9 {
			t.Errorf("Expected importance 0.9, got %f", entry.Importance)
		}

		if len(entry.Tags) != 2 {
			t.Errorf("Expected 2 tags, got %d", len(entry.Tags))
		}
	})

	t.Run("Update Memory", func(t *testing.T) {
		store.Store(ctx, "agent-3", "key1", "original", nil)

		err := store.Update(ctx, "agent-3", "key1", "updated")
		if err != nil {
			t.Fatalf("Failed to update: %v", err)
		}

		entry, _ := store.Retrieve(ctx, "agent-3", "key1")
		if entry.Value != "updated" {
			t.Errorf("Expected 'updated', got '%v'", entry.Value)
		}
	})

	t.Run("Delete Memory", func(t *testing.T) {
		store.Store(ctx, "agent-4", "key1", "value", nil)

		err := store.Delete(ctx, "agent-4", "key1")
		if err != nil {
			t.Fatalf("Failed to delete: %v", err)
		}

		_, err = store.Retrieve(ctx, "agent-4", "key1")
		if err == nil {
			t.Error("Expected error when retrieving deleted memory")
		}
	})

	t.Run("List Memories", func(t *testing.T) {
		agentID := "agent-5"

		// Store multiple memories
		for i := 0; i < 5; i++ {
			key := "key" + string(rune(i))
			store.Store(ctx, agentID, key, "value", nil)
		}

		entries, err := store.List(ctx, agentID, 0, 0)
		if err != nil {
			t.Fatalf("Failed to list: %v", err)
		}

		if len(entries) != 5 {
			t.Errorf("Expected 5 entries, got %d", len(entries))
		}
	})

	t.Run("List with Pagination", func(t *testing.T) {
		agentID := "agent-6"

		for i := 0; i < 10; i++ {
			key := "key" + string(rune(i))
			store.Store(ctx, agentID, key, "value", nil)
		}

		// Get first 5
		entries, _ := store.List(ctx, agentID, 5, 0)
		if len(entries) != 5 {
			t.Errorf("Expected 5 entries, got %d", len(entries))
		}

		// Get next 5
		entries, _ = store.List(ctx, agentID, 5, 5)
		if len(entries) != 5 {
			t.Errorf("Expected 5 entries, got %d", len(entries))
		}
	})

	t.Run("Clear All Memories", func(t *testing.T) {
		agentID := "agent-7"

		for i := 0; i < 3; i++ {
			store.Store(ctx, agentID, "key"+string(rune(i)), "value", nil)
		}

		err := store.Clear(ctx, agentID)
		if err != nil {
			t.Fatalf("Failed to clear: %v", err)
		}

		entries, _ := store.List(ctx, agentID, 0, 0)
		if len(entries) != 0 {
			t.Errorf("Expected 0 entries after clear, got %d", len(entries))
		}
	})
}

func TestMemorySearch(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()
	agentID := "search-agent"

	// Store test data
	testData := []struct {
		key        string
		value      string
		tags       []string
		importance float64
	}{
		{"mem1", "Apache Spark uses DAG scheduling", []string{"spark", "technical"}, 0.8},
		{"mem2", "User prefers detailed explanations", []string{"preference"}, 0.9},
		{"mem3", "Learned about distributed systems", []string{"technical"}, 0.7},
		{"mem4", "Previous conversation about AI", []string{"context"}, 0.5},
	}

	for _, td := range testData {
		metadata := map[string]interface{}{
			"tags":       td.tags,
			"importance": td.importance,
		}
		store.Store(ctx, agentID, td.key, td.value, metadata)
	}

	t.Run("Search by Tag", func(t *testing.T) {
		query := &MemoryQuery{
			Tags:  []string{"technical"},
			Limit: 10,
		}

		results, err := store.Search(ctx, agentID, query)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) != 2 {
			t.Errorf("Expected 2 results with 'technical' tag, got %d", len(results))
		}
	})

	t.Run("Search by Importance", func(t *testing.T) {
		query := &MemoryQuery{
			MinImportance: 0.8,
			Limit:         10,
		}

		results, err := store.Search(ctx, agentID, query)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		// Should find 2 entries with importance >= 0.8
		if len(results) != 2 {
			t.Errorf("Expected 2 results with importance >= 0.8, got %d", len(results))
		}
	})

	t.Run("Search by Text", func(t *testing.T) {
		query := &MemoryQuery{
			Text:  "Spark",
			Limit: 10,
		}

		results, err := store.Search(ctx, agentID, query)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) < 1 {
			t.Error("Expected at least 1 result containing 'Spark'")
		}
	})

	t.Run("Search with Limit", func(t *testing.T) {
		query := &MemoryQuery{
			Limit: 2,
		}

		results, err := store.Search(ctx, agentID, query)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		if len(results) != 2 {
			t.Errorf("Expected 2 results with limit=2, got %d", len(results))
		}
	})

	t.Run("Search by Time Range", func(t *testing.T) {
		now := time.Now()
		past := now.Add(-1 * time.Hour)

		query := &MemoryQuery{
			TimeRange: &TimeRange{
				Start: past,
				End:   now.Add(1 * time.Hour),
			},
			Limit: 10,
		}

		results, err := store.Search(ctx, agentID, query)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		// All recent entries should be found
		if len(results) != 4 {
			t.Errorf("Expected 4 results in time range, got %d", len(results))
		}
	})
}

func TestMemoryAccessTracking(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()

	store.Store(ctx, "agent-1", "key1", "value", nil)

	entry1, _ := store.Retrieve(ctx, "agent-1", "key1")
	if entry1.AccessCount != 1 {
		t.Errorf("Expected access count 1, got %d", entry1.AccessCount)
	}

	// Save the first access count and time for comparison
	firstAccessCount := entry1.AccessCount
	firstAccessTime := entry1.LastAccess

	// Access again (add small delay to ensure timestamp difference)
	time.Sleep(2 * time.Millisecond)
	entry2, _ := store.Retrieve(ctx, "agent-1", "key1")
	if entry2.AccessCount != 2 {
		t.Errorf("Expected access count 2, got %d", entry2.AccessCount)
	}

	// Verify access count incremented from saved value
	if entry2.AccessCount <= firstAccessCount {
		t.Errorf("Access count not incremented: first=%d, second=%d", firstAccessCount, entry2.AccessCount)
	}

	// LastAccess should be after the saved time
	if !entry2.LastAccess.After(firstAccessTime) && !entry2.LastAccess.Equal(firstAccessTime) {
		t.Errorf("LastAccess not updated: first=%v, second=%v", firstAccessTime, entry2.LastAccess)
	}
}

func TestVectorMemoryStore(t *testing.T) {
	baseStore := NewInMemoryStore()

	// Mock embedding function
	embeddingFn := func(text string) ([]float64, error) {
		// Simple mock: convert text length to embedding
		return []float64{float64(len(text)), 1.0, 2.0}, nil
	}

	vectorStore := NewVectorMemoryStore(baseStore, embeddingFn)
	ctx := context.Background()
	agentID := "vector-agent"

	t.Run("Store with Embedding", func(t *testing.T) {
		err := vectorStore.Store(ctx, agentID, "key1", "test value", nil)
		if err != nil {
			t.Fatalf("Failed to store: %v", err)
		}

		// Verify it's in base store
		entry, _ := baseStore.Retrieve(ctx, agentID, "key1")
		if entry == nil {
			t.Error("Entry not found in base store")
		}
	})

	t.Run("Semantic Search", func(t *testing.T) {
		// Store some entries
		entries := map[string]string{
			"key1": "distributed systems",
			"key2": "apache spark",
			"key3": "machine learning",
		}

		for key, value := range entries {
			vectorStore.Store(ctx, agentID, key, value, nil)
		}

		query := &MemoryQuery{
			Text:  "distributed",
			Limit: 2,
		}

		results, err := vectorStore.Search(ctx, agentID, query)
		if err != nil {
			t.Fatalf("Semantic search failed: %v", err)
		}

		if len(results) == 0 {
			t.Error("Expected search results")
		}
	})

	t.Run("Update with Embedding", func(t *testing.T) {
		vectorStore.Store(ctx, agentID, "update-key", "original", nil)

		err := vectorStore.Update(ctx, agentID, "update-key", "updated")
		if err != nil {
			t.Fatalf("Failed to update: %v", err)
		}

		entry, _ := vectorStore.Retrieve(ctx, agentID, "update-key")
		if entry.Value != "updated" {
			t.Error("Value not updated")
		}
	})

	t.Run("Delete with Embedding", func(t *testing.T) {
		vectorStore.Store(ctx, agentID, "delete-key", "value", nil)

		err := vectorStore.Delete(ctx, agentID, "delete-key")
		if err != nil {
			t.Fatalf("Failed to delete: %v", err)
		}

		_, err = vectorStore.Retrieve(ctx, agentID, "delete-key")
		if err == nil {
			t.Error("Expected error when retrieving deleted entry")
		}
	})
}

func TestCosineSimilarity(t *testing.T) {
	t.Run("Identical Vectors", func(t *testing.T) {
		v1 := []float64{1.0, 2.0, 3.0}
		v2 := []float64{1.0, 2.0, 3.0}

		sim := cosineSimilarity(v1, v2)
		if sim < 0.99 || sim > 1.01 {
			t.Errorf("Expected similarity ~1.0, got %f", sim)
		}
	})

	t.Run("Orthogonal Vectors", func(t *testing.T) {
		v1 := []float64{1.0, 0.0}
		v2 := []float64{0.0, 1.0}

		sim := cosineSimilarity(v1, v2)
		if sim != 0.0 {
			t.Errorf("Expected similarity 0.0, got %f", sim)
		}
	})

	t.Run("Different Length Vectors", func(t *testing.T) {
		v1 := []float64{1.0, 2.0}
		v2 := []float64{1.0, 2.0, 3.0}

		sim := cosineSimilarity(v1, v2)
		if sim != 0.0 {
			t.Errorf("Expected similarity 0.0 for different lengths, got %f", sim)
		}
	})
}

func TestConcurrentMemoryAccess(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()
	agentID := "concurrent-agent"

	const numGoroutines = 20
	errors := make(chan error, numGoroutines)

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			key := "key-" + string(rune(idx))
			errors <- store.Store(ctx, agentID, key, "value", nil)
		}(i)
	}

	for i := 0; i < numGoroutines; i++ {
		if err := <-errors; err != nil {
			t.Errorf("Concurrent write failed: %v", err)
		}
	}

	// Verify all written
	entries, _ := store.List(ctx, agentID, 0, 0)
	if len(entries) != numGoroutines {
		t.Errorf("Expected %d entries, got %d", numGoroutines, len(entries))
	}
}

func TestMemoryMetadata(t *testing.T) {
	store := NewInMemoryStore()
	ctx := context.Background()

	metadata := map[string]interface{}{
		"source":     "user_conversation",
		"confidence": 0.95,
		"tags":       []string{"important", "verified"},
		"importance": 0.85,
	}

	err := store.Store(ctx, "agent-1", "key1", "test value", metadata)
	if err != nil {
		t.Fatalf("Failed to store with metadata: %v", err)
	}

	entry, _ := store.Retrieve(ctx, "agent-1", "key1")

	if entry.Importance != 0.85 {
		t.Errorf("Expected importance 0.85, got %f", entry.Importance)
	}

	if len(entry.Tags) != 2 {
		t.Errorf("Expected 2 tags, got %d", len(entry.Tags))
	}

	if entry.Metadata["source"] != "user_conversation" {
		t.Error("Metadata not preserved correctly")
	}
}
