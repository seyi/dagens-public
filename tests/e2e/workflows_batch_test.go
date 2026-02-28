package e2e

import (
	"net/http"
	"testing"
)

// TestBatchExecute verifies the /api/v1/agents/batch_execute endpoint.
// Note: The server batch API only supports a single agent_id with multiple inputs.
func TestBatchExecute(t *testing.T) {
	server := NewServerRunner(t)
	server.Start(t)

	client := NewClient(server.BaseURL())

	t.Run("batch_executes_multiple_inputs", func(t *testing.T) {
		resp, status, err := client.BatchExecute(&BatchExecuteRequest{
			AgentID: "echo",
			Inputs:  []string{"Hello", "World", "Test"},
		})
		if err != nil {
			t.Fatalf("Batch execute failed: %v", err)
		}

		if status != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, status)
		}

		if len(resp.Results) != 3 {
			t.Fatalf("Expected 3 results, got %d", len(resp.Results))
		}

		// Verify each result
		for i, result := range resp.Results {
			if !result.Success {
				t.Errorf("Result %d: expected success=true, got false. Error: %s", i, result.Error)
			}
		}
	})

	t.Run("batch_preserves_order", func(t *testing.T) {
		resp, _, err := client.BatchExecute(&BatchExecuteRequest{
			AgentID: "echo",
			Inputs:  []string{"first", "second", "third"},
		})
		if err != nil {
			t.Fatalf("Batch execute failed: %v", err)
		}

		if len(resp.Results) != 3 {
			t.Fatalf("Expected 3 results, got %d", len(resp.Results))
		}

		// Echo outputs should match inputs
		expected := []string{"Echo: first", "Echo: second", "Echo: third"}
		for i, result := range resp.Results {
			if !result.Success {
				t.Errorf("Result %d: expected success=true, got false", i)
			}
			if result.Output != expected[i] {
				t.Errorf("Result %d: expected output=%q, got %q", i, expected[i], result.Output)
			}
		}
	})

	t.Run("batch_summarizer_agent", func(t *testing.T) {
		resp, status, err := client.BatchExecute(&BatchExecuteRequest{
			AgentID: "summarizer",
			Inputs:  []string{"one two", "a b c d e"},
		})
		if err != nil {
			t.Fatalf("Batch execute failed: %v", err)
		}

		if status != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, status)
		}

		if len(resp.Results) != 2 {
			t.Errorf("Expected 2 results, got %d", len(resp.Results))
		}

		// Verify agent type in metadata
		for i, result := range resp.Results {
			if result.Metadata != nil {
				if agentType, ok := result.Metadata["agent_type"]; ok {
					if agentType != "summarizer" {
						t.Errorf("Result %d: expected agent_type=summarizer, got %v", i, agentType)
					}
				}
			}
		}
	})

	t.Run("batch_empty_inputs", func(t *testing.T) {
		resp, status, err := client.BatchExecute(&BatchExecuteRequest{
			AgentID: "echo",
			Inputs:  []string{},
		})
		if err != nil {
			t.Fatalf("Batch execute failed: %v", err)
		}

		// Should return 200 with empty results
		if status != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, status)
		}

		if len(resp.Results) != 0 {
			t.Errorf("Expected 0 results for empty request, got %d", len(resp.Results))
		}
	})

	t.Run("batch_single_input", func(t *testing.T) {
		resp, status, err := client.BatchExecute(&BatchExecuteRequest{
			AgentID: "echo",
			Inputs:  []string{"single"},
		})
		if err != nil {
			t.Fatalf("Batch execute failed: %v", err)
		}

		if status != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, status)
		}

		if len(resp.Results) != 1 {
			t.Errorf("Expected 1 result, got %d", len(resp.Results))
		}
	})
}
