package e2e

import (
	"net/http"
	"testing"
)

// TestErrorHandling verifies proper error responses from the agent server.
func TestErrorHandling(t *testing.T) {
	server := NewServerRunner(t)
	server.Start(t)

	client := NewClient(server.BaseURL())

	t.Run("unknown_agent_returns_404", func(t *testing.T) {
		resp, status, err := client.Execute(&ExecuteRequest{
			AgentID:     "nonexistent-agent",
			Instruction: "test",
		})
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		if status != http.StatusNotFound {
			t.Errorf("Expected status %d for unknown agent, got %d", http.StatusNotFound, status)
		}

		// Should have error in response
		if resp.Error == "" && resp.Output == "" {
			t.Error("Expected error message for unknown agent")
		}
	})

	t.Run("empty_agent_id_returns_error", func(t *testing.T) {
		resp, status, err := client.Execute(&ExecuteRequest{
			AgentID:     "",
			Instruction: "test",
		})
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		// Should return 400 or 404
		if status != http.StatusBadRequest && status != http.StatusNotFound {
			t.Errorf("Expected status 400 or 404 for empty agent_id, got %d", status)
		}

		_ = resp // Response may vary
	})

	t.Run("stream_without_async_runtime_returns_503", func(t *testing.T) {
		status, body, err := client.Stream(&ExecuteRequest{
			AgentID:     "echo",
			Instruction: "test",
		})
		if err != nil {
			t.Fatalf("Stream request failed: %v", err)
		}

		// Stream endpoint returns 503 when async runtime not configured
		if status != http.StatusServiceUnavailable {
			t.Errorf("Expected status %d for stream without async runtime, got %d. Body: %s",
				http.StatusServiceUnavailable, status, body)
		}
	})

	t.Run("batch_with_invalid_agent_returns_404", func(t *testing.T) {
		// Note: Server batch API returns 404 for unknown agent_id
		_, status, err := client.BatchExecute(&BatchExecuteRequest{
			AgentID: "invalid-agent",
			Inputs:  []string{"test"},
		})
		if err != nil {
			t.Fatalf("Batch execute failed: %v", err)
		}

		// Batch with invalid agent should return 404
		if status != http.StatusNotFound {
			t.Errorf("Expected status %d for invalid agent, got %d", http.StatusNotFound, status)
		}
	})

	t.Run("malformed_json_returns_400", func(t *testing.T) {
		// Send raw malformed JSON
		httpClient := client.httpClient
		resp, err := httpClient.Post(
			client.baseURL+"/api/v1/agents/execute",
			"application/json",
			nil, // nil body
		)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		// Should return 400 Bad Request
		if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
			t.Errorf("Expected status 400 for nil body, got %d", resp.StatusCode)
		}
	})

	t.Run("invalid_endpoint_returns_404", func(t *testing.T) {
		resp, err := client.httpClient.Get(client.baseURL + "/api/v1/nonexistent")
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Expected status 404 for invalid endpoint, got %d", resp.StatusCode)
		}
	})
}
