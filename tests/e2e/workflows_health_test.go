package e2e

import (
	"net/http"
	"testing"
)

// TestHealthCheck verifies the /health endpoint returns healthy status.
func TestHealthCheck(t *testing.T) {
	server := NewServerRunner(t)
	server.Start(t)

	client := NewClient(server.BaseURL())

	t.Run("returns_healthy_status", func(t *testing.T) {
		resp, status, err := client.Health()
		if err != nil {
			t.Fatalf("Health check failed: %v", err)
		}

		if status != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, status)
		}

		if resp.Status != "healthy" {
			t.Errorf("Expected status 'healthy', got '%s'", resp.Status)
		}
	})

	t.Run("responds_quickly", func(t *testing.T) {
		// Health check should respond in under 100ms
		for i := 0; i < 10; i++ {
			_, status, err := client.Health()
			if err != nil {
				t.Fatalf("Health check failed on iteration %d: %v", i, err)
			}
			if status != http.StatusOK {
				t.Errorf("Health check returned non-200 on iteration %d: %d", i, status)
			}
		}
	})
}
