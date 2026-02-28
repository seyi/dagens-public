package a2a

import (
	"context"
	"testing"
	"time"
)

func TestNewDiscoveryRegistry(t *testing.T) {
	registry := NewDiscoveryRegistry()

	if registry == nil {
		t.Fatal("Registry should not be nil")
	}
}

func TestRegisterAgent(t *testing.T) {
	registry := NewDiscoveryRegistry()

	card := &AgentCard{
		ID:          "agent-1",
		Name:        "Test Agent",
		Description: "A test agent",
		Endpoint:    "http://localhost:8001",
		Capabilities: []Capability{
			{
				Name:        "test_capability",
				Description: "Test capability",
			},
		},
		Modalities: []Modality{ModalityText},
		AuthScheme: AuthScheme{Type: AuthTypeNone},
	}

	err := registry.Register(card)
	if err != nil {
		t.Fatalf("Failed to register agent: %v", err)
	}

	// Retrieve agent info
	info, err := registry.GetAgent("agent-1")
	if err != nil {
		t.Fatalf("Failed to get agent: %v", err)
	}

	if info.Name != "Test Agent" {
		t.Errorf("Expected name 'Test Agent', got '%s'", info.Name)
	}

	if len(info.Capabilities) != 1 {
		t.Errorf("Expected 1 capability, got %d", len(info.Capabilities))
	}
}

func TestGetAgentCard(t *testing.T) {
	registry := NewDiscoveryRegistry()

	card := &AgentCard{
		ID:       "agent-1",
		Name:     "Test Agent",
		Endpoint: "http://localhost:8001",
	}

	registry.Register(card)

	retrieved, err := registry.GetAgentCard("agent-1")
	if err != nil {
		t.Fatalf("Failed to get agent card: %v", err)
	}

	if retrieved.ID != "agent-1" {
		t.Errorf("Expected ID 'agent-1', got '%s'", retrieved.ID)
	}
}

func TestFindByCapability(t *testing.T) {
	registry := NewDiscoveryRegistry()

	// Register agents with different capabilities
	card1 := &AgentCard{
		ID:       "agent-1",
		Name:     "Research Agent",
		Endpoint: "http://localhost:8001",
		Capabilities: []Capability{
			{Name: "research"},
			{Name: "summarize"},
		},
	}

	card2 := &AgentCard{
		ID:       "agent-2",
		Name:     "Code Agent",
		Endpoint: "http://localhost:8002",
		Capabilities: []Capability{
			{Name: "code_generation"},
		},
	}

	registry.Register(card1)
	registry.Register(card2)

	// Find agents with research capability
	researchers := registry.FindByCapability("research")

	if len(researchers) != 1 {
		t.Errorf("Expected 1 agent with research capability, got %d", len(researchers))
	}

	if researchers[0].ID != "agent-1" {
		t.Error("Wrong agent returned")
	}
}

func TestUnregisterAgent(t *testing.T) {
	registry := NewDiscoveryRegistry()

	card := &AgentCard{
		ID:       "agent-1",
		Name:     "Test Agent",
		Endpoint: "http://localhost:8001",
	}

	registry.Register(card)

	err := registry.Unregister("agent-1")
	if err != nil {
		t.Fatalf("Failed to unregister agent: %v", err)
	}

	_, err = registry.GetAgent("agent-1")
	if err == nil {
		t.Error("Unregistered agent should not be found")
	}
}

func TestListAllAgents(t *testing.T) {
	registry := NewDiscoveryRegistry()

	// Register multiple agents
	for i := 1; i <= 3; i++ {
		card := &AgentCard{
			ID:       "agent-" + string(rune(i+'0')),
			Name:     "Agent " + string(rune(i+'0')),
			Endpoint: "http://localhost:800" + string(rune(i+'0')),
		}
		registry.Register(card)
	}

	all := registry.ListAll()

	if len(all) != 3 {
		t.Errorf("Expected 3 agents, got %d", len(all))
	}
}

func TestAgentCardSerialization(t *testing.T) {
	card := &AgentCard{
		ID:          "agent-1",
		Name:        "Test Agent",
		Description: "Test",
		Version:     "1.0.0",
		Endpoint:    "http://localhost:8001",
		Capabilities: []Capability{
			{
				Name:        "test",
				Description: "Test capability",
			},
		},
		Modalities: []Modality{ModalityText, ModalityStream},
		AuthScheme: AuthScheme{
			Type: AuthTypeBearer,
		},
		SupportedPatterns: []CommunicationPattern{
			PatternRequestResponse,
			PatternSSE,
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Test that all fields are properly set
	if card.ID == "" {
		t.Error("ID should be set")
	}

	if len(card.Capabilities) != 1 {
		t.Error("Capabilities should be set")
	}

	if len(card.Modalities) != 2 {
		t.Error("Modalities should be set")
	}
}

func TestNewHTTPA2AClient(t *testing.T) {
	registry := NewDiscoveryRegistry()
	client := NewHTTPA2AClient(registry)

	if client == nil {
		t.Fatal("Client should not be nil")
	}
}

func TestDiscoverAgentsViaClient(t *testing.T) {
	registry := NewDiscoveryRegistry()

	card := &AgentCard{
		ID:       "agent-1",
		Name:     "Test Agent",
		Endpoint: "http://localhost:8001",
		Capabilities: []Capability{
			{Name: "search"},
		},
	}

	registry.Register(card)

	client := NewHTTPA2AClient(registry)
	ctx := context.Background()

	agents, err := client.DiscoverAgents(ctx, "search")
	if err != nil {
		t.Fatalf("Failed to discover agents: %v", err)
	}

	if len(agents) != 1 {
		t.Errorf("Expected 1 agent, got %d", len(agents))
	}
}
