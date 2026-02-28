package tools

import (
	"context"
	"fmt"
	"testing"
)

// TestRagRetrievalTool tests the RAG retrieval tool
func TestRagRetrievalTool(t *testing.T) {
	ctx := context.Background()

	// Create a mock search function
	mockSearch := func(ctx context.Context, query string, topK int, minScore float64) (interface{}, error) {
		return map[string]interface{}{
			"query": query,
			"results": []map[string]interface{}{
				{"content": "Paris is the capital of France", "score": 0.9},
				{"content": "Tokyo is the capital of Japan", "score": 0.8},
			},
		}, nil
	}

	// Create RAG retrieval tool
	tool := RagRetrievalTool(RagRetrievalConfig{
		SearchFunc: mockSearch,
	})

	// Test query
	params := map[string]interface{}{
		"query": "What is the capital of France?",
	}

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("RAG retrieval failed: %v", err)
	}

	resultMap := result.(map[string]interface{})
	if resultMap["query"] != "What is the capital of France?" {
		t.Errorf("Query mismatch")
	}
}

// TestRagRetrievalToolParameters tests parameter handling
func TestRagRetrievalToolParameters(t *testing.T) {
	ctx := context.Background()

	mockSearch := func(ctx context.Context, query string, topK int, minScore float64) (interface{}, error) {
		return map[string]interface{}{
			"query":   query,
			"top_k":   topK,
			"min_score": minScore,
		}, nil
	}

	tool := RagRetrievalTool(RagRetrievalConfig{
		SearchFunc: mockSearch,
	})

	// Test missing query
	_, err := tool.Handler(ctx, map[string]interface{}{})
	if err == nil {
		t.Error("Expected error for missing query")
	}

	// Test valid query with parameters
	result, err := tool.Handler(ctx, map[string]interface{}{
		"query":     "test",
		"top_k":     5,
		"min_score": 0.7,
	})
	if err != nil {
		t.Fatalf("Expected success, got error: %v", err)
	}

	resultMap := result.(map[string]interface{})
	if resultMap["query"] != "test" {
		t.Error("Query mismatch")
	}
}

// TestRagRetrievalToolNoSearchFunc tests error when search function is not configured
func TestRagRetrievalToolNoSearchFunc(t *testing.T) {
	ctx := context.Background()

	tool := RagRetrievalTool(RagRetrievalConfig{
		SearchFunc: nil,
	})

	_, err := tool.Handler(ctx, map[string]interface{}{
		"query": "test",
	})
	if err == nil {
		t.Error("Expected error for nil search function")
	}
}

// TestSimpleRagRetrievalTool tests the simple RAG tool
func TestSimpleRagRetrievalTool(t *testing.T) {
	tool := SimpleRagRetrievalTool()

	if tool.Name != "rag_retrieval" {
		t.Errorf("Expected tool name 'rag_retrieval', got %s", tool.Name)
	}

	if !tool.Enabled {
		t.Error("Tool should be enabled")
	}

	// Verify schema
	schema := tool.Schema
	if schema == nil {
		t.Fatal("Tool schema is nil")
	}

	properties, ok := schema.InputSchema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("properties should be map[string]interface{}")
	}

	// Check required fields
	if properties["query"] == nil {
		t.Error("Schema should have 'query' property")
	}
}

// TestRegisterRagTools tests tool registration
func TestRegisterRagTools(t *testing.T) {
	registry := NewToolRegistry()

	mockSearch := func(ctx context.Context, query string, topK int, minScore float64) (interface{}, error) {
		return []map[string]interface{}{
			{"content": "result", "score": 0.9},
		}, nil
	}

	err := RegisterRagTools(registry, mockSearch)
	if err != nil {
		t.Fatalf("Failed to register RAG tools: %v", err)
	}

	// Verify tool registered
	tool, err := registry.Get("rag_retrieval")
	if err != nil {
		t.Fatalf("Failed to get registered tool: %v", err)
	}

	if tool.Name != "rag_retrieval" {
		t.Errorf("Expected 'rag_retrieval', got %s", tool.Name)
	}
}

// TestRagRetrievalToolSearchError tests handling of search errors
func TestRagRetrievalToolSearchError(t *testing.T) {
	ctx := context.Background()

	mockSearch := func(ctx context.Context, query string, topK int, minScore float64) (interface{}, error) {
		return nil, fmt.Errorf("search failed")
	}

	tool := RagRetrievalTool(RagRetrievalConfig{
		SearchFunc: mockSearch,
	})

	_, err := tool.Handler(ctx, map[string]interface{}{
		"query": "test",
	})
	if err == nil {
		t.Error("Expected error from search function")
	}
}
