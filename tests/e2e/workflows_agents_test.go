package e2e

import (
	"net/http"
	"testing"
)

// TestListAgents verifies the /api/v1/agents endpoint returns all registered agents.
func TestListAgents(t *testing.T) {
	server := NewServerRunner(t)
	server.Start(t)

	client := NewClient(server.BaseURL())

	t.Run("returns_all_registered_agents", func(t *testing.T) {
		resp, status, err := client.ListAgents()
		if err != nil {
			t.Fatalf("List agents failed: %v", err)
		}

		if status != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, status)
		}

		// Verify we have the expected agents
		expectedAgents := map[string]bool{
			"echo":       false,
			"summarizer": false,
			"classifier": false,
			"sentiment":  false,
		}

		for _, agent := range resp.Agents {
			if _, exists := expectedAgents[agent.ID]; exists {
				expectedAgents[agent.ID] = true
			}
		}

		for agentID, found := range expectedAgents {
			if !found {
				t.Errorf("Expected agent '%s' not found in response", agentID)
			}
		}
	})

	t.Run("agents_have_required_fields", func(t *testing.T) {
		resp, _, err := client.ListAgents()
		if err != nil {
			t.Fatalf("List agents failed: %v", err)
		}

		for _, agent := range resp.Agents {
			if agent.ID == "" {
				t.Error("Agent has empty ID")
			}
			if agent.Name == "" {
				t.Error("Agent has empty Name")
			}
			if agent.Type == "" {
				t.Error("Agent has empty Type")
			}
		}
	})

	t.Run("returns_at_least_4_agents", func(t *testing.T) {
		resp, _, err := client.ListAgents()
		if err != nil {
			t.Fatalf("List agents failed: %v", err)
		}

		if len(resp.Agents) < 4 {
			t.Errorf("Expected at least 4 agents, got %d", len(resp.Agents))
		}
	})
}
