package e2e

import (
	"net/http"
	"strings"
	"testing"
)

// TestSingleExecute verifies the /api/v1/agents/execute endpoint for single agent execution.
func TestSingleExecute(t *testing.T) {
	server := NewServerRunner(t)
	server.Start(t)

	client := NewClient(server.BaseURL())

	t.Run("echo_agent_echoes_input", func(t *testing.T) {
		resp, status, err := client.Execute(&ExecuteRequest{
			AgentID:     "echo",
			Instruction: "Hello, World!",
		})
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		if status != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, status)
		}

		if !resp.Success {
			t.Errorf("Expected success=true, got false. Error: %s", resp.Error)
		}

		if !strings.Contains(resp.Output, "Echo:") {
			t.Errorf("Expected output to contain 'Echo:', got '%s'", resp.Output)
		}

		if resp.Metadata == nil {
			t.Error("Expected metadata to be present")
		} else if resp.Metadata["agent_type"] != "echo" {
			t.Errorf("Expected agent_type='echo', got '%v'", resp.Metadata["agent_type"])
		}
	})

	t.Run("summarizer_agent_counts_words", func(t *testing.T) {
		resp, status, err := client.Execute(&ExecuteRequest{
			AgentID:     "summarizer",
			Instruction: "The quick brown fox jumps over the lazy dog",
		})
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		if status != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, status)
		}

		if !resp.Success {
			t.Errorf("Expected success=true, got false")
		}

		if !strings.Contains(resp.Output, "Summary:") {
			t.Errorf("Expected output to contain 'Summary:', got '%s'", resp.Output)
		}

		if resp.Metadata != nil {
			if _, ok := resp.Metadata["word_count"]; !ok {
				t.Error("Expected metadata to contain 'word_count'")
			}
		}
	})

	t.Run("classifier_agent_classifies_positive", func(t *testing.T) {
		resp, status, err := client.Execute(&ExecuteRequest{
			AgentID:     "classifier",
			Instruction: "This product is excellent and great!",
		})
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		if status != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, status)
		}

		if !resp.Success {
			t.Errorf("Expected success=true, got false")
		}

		if resp.Output != "positive" {
			t.Errorf("Expected output 'positive', got '%s'", resp.Output)
		}
	})

	t.Run("classifier_agent_classifies_negative", func(t *testing.T) {
		resp, status, err := client.Execute(&ExecuteRequest{
			AgentID:     "classifier",
			Instruction: "This product is bad and terrible!",
		})
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		if status != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, status)
		}

		if resp.Output != "negative" {
			t.Errorf("Expected output 'negative', got '%s'", resp.Output)
		}
	})

	t.Run("sentiment_agent_analyzes_sentiment", func(t *testing.T) {
		resp, status, err := client.Execute(&ExecuteRequest{
			AgentID:     "sentiment",
			Instruction: "I love this amazing wonderful product!",
		})
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		if status != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, status)
		}

		if !resp.Success {
			t.Errorf("Expected success=true, got false")
		}

		if resp.Output != "positive" {
			t.Errorf("Expected output 'positive', got '%s'", resp.Output)
		}

		if resp.Metadata != nil {
			if score, ok := resp.Metadata["score"]; ok {
				if scoreFloat, ok := score.(float64); ok && scoreFloat <= 0.5 {
					t.Errorf("Expected positive score > 0.5, got %v", scoreFloat)
				}
			}
		}
	})

	t.Run("execute_returns_duration", func(t *testing.T) {
		resp, _, err := client.Execute(&ExecuteRequest{
			AgentID:     "echo",
			Instruction: "test",
		})
		if err != nil {
			t.Fatalf("Execute failed: %v", err)
		}

		// Duration should be >= 0 (it's tracked even if 0ms)
		if resp.DurationMS < 0 {
			t.Errorf("Expected duration_ms >= 0, got %d", resp.DurationMS)
		}
	})
}
