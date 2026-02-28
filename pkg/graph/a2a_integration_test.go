package graph

import (
	"context"
	"testing"

	"github.com/seyi/dagens/pkg/a2a"
	"github.com/seyi/dagens/pkg/agent"
)

// MockA2AClient implements a2a.A2AClient for testing
type MockA2AClient struct {
	invocations map[string]*agent.AgentInput
	responses   map[string]*agent.AgentOutput
	registry    *a2a.DiscoveryRegistry
}

func NewMockA2AClient() *MockA2AClient {
	return &MockA2AClient{
		invocations: make(map[string]*agent.AgentInput),
		responses:   make(map[string]*agent.AgentOutput),
		registry:    a2a.NewDiscoveryRegistry(),
	}
}

func (m *MockA2AClient) InvokeAgent(ctx context.Context, agentID string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	m.invocations[agentID] = input
	
	response, exists := m.responses[agentID]
	if !exists {
		return &agent.AgentOutput{
			Result: "mock response from " + agentID,
			Metadata: map[string]interface{}{
				"agent_id": agentID,
				"status":   "success",
			},
		}, nil
	}
	
	return response, nil
}

func (m *MockA2AClient) DiscoverAgents(ctx context.Context, capability string) ([]*a2a.AgentInfo, error) {
	return m.registry.FindByCapability(capability), nil
}

func (m *MockA2AClient) GetAgentCard(ctx context.Context, agentID string) (*a2a.AgentCard, error) {
	return m.registry.GetAgentCard(agentID)
}

func (m *MockA2AClient) StreamInvocation(ctx context.Context, agentID string, input *agent.AgentInput) (<-chan *a2a.StreamChunk, error) {
	ch := make(chan *a2a.StreamChunk, 1)
	close(ch)
	return ch, nil
}

func TestA2ANodeCreation(t *testing.T) {
	client := NewMockA2AClient()
	
	config := A2ANodeConfig{
		ID:      "test-a2a-node",
		Name:    "Test A2A Node",
		Client:  client,
		AgentID: "remote-agent-1",
	}
	
	node, err := NewA2ANode(config)
	if err != nil {
		t.Fatalf("Failed to create A2A node: %v", err)
	}
	
	if node.ID() != "test-a2a-node" {
		t.Errorf("Expected node ID 'test-a2a-node', got '%s'", node.ID())
	}
	
	if node.Type() != "a2a" {
		t.Errorf("Expected node type 'a2a', got '%s'", node.Type())
	}
	
	if node.client != client {
		t.Error("Client not properly set")
	}
	
	if node.agentID != "remote-agent-1" {
		t.Errorf("Expected agent ID 'remote-agent-1', got '%s'", node.agentID)
	}
}

func TestA2ANodeValidation(t *testing.T) {
	// Test with nil client
	_, err := NewA2ANode(A2ANodeConfig{
		ID:      "test-node",
		AgentID: "agent-1",
		Client:  nil,
	})
	
	if err == nil {
		t.Error("Expected error for nil client")
	}
	
	if err.Error() != "A2A client is required" {
		t.Errorf("Expected 'A2A client is required' error, got: %v", err)
	}
	
	// Test with empty agent ID
	_, err = NewA2ANode(A2ANodeConfig{
		ID:     "test-node",
		Client: NewMockA2AClient(),
	})
	
	if err == nil {
		t.Error("Expected error for empty agent ID")
	}
	
	if err.Error() != "agent ID is required" {
		t.Errorf("Expected 'agent ID is required' error, got: %v", err)
	}
}

func TestA2AGraphBuilder(t *testing.T) {
	client := NewMockA2AClient()
	
	builder := NewA2AGraphBuilder("test-graph", client)
	
	// Add A2A node
	builder.AddA2ANode(A2ANodeConfig{
		ID:      "remote-node-1",
		Name:    "Remote Node 1",
		Client:  client,
		AgentID: "agent-1",
	})
	
	// Set entry and finish since the graph needs them
	builder.SetEntry("remote-node-1")
	builder.AddFinish("remote-node-1")

	// Build graph
	graph, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}
	
	if graph.NodeCount() != 1 {
		t.Errorf("Expected 1 node, got %d", graph.NodeCount())
	}
	
	// Try to get the node
	node, err := graph.GetNode("remote-node-1")
	if err != nil {
		t.Fatalf("Failed to get node: %v", err)
	}
	
	if node.ID() != "remote-node-1" {
		t.Errorf("Expected node ID 'remote-node-1', got '%s'", node.ID())
	}
}

func TestA2ANodeExecute(t *testing.T) {
	client := NewMockA2AClient()
	
	// Set up a mock response
	client.responses["test-agent"] = &agent.AgentOutput{
		Result: "processed data",
		Metadata: map[string]interface{}{
			"status": "completed",
			"value":  42,
		},
	}
	
	config := A2ANodeConfig{
		ID:      "a2a-test-node",
		Client:  client,
		AgentID: "test-agent",
	}
	
	node, err := NewA2ANode(config)
	if err != nil {
		t.Fatalf("Failed to create A2A node: %v", err)
	}
	
	// Create state with some initial values
	state := NewMemoryState()
	state.Set("input_data", "test value")
	state.Set("user_id", "user-123")
	
	ctx := context.Background()
	err = node.Execute(ctx, state)
	if err != nil {
		t.Fatalf("Node execution failed: %v", err)
	}
	
	// Verify the remote agent was called
	if _, exists := client.invocations["test-agent"]; !exists {
		t.Error("Remote agent was not invoked")
	}
	
	// Check that invocation had the expected context
	invocation := client.invocations["test-agent"]
	if invocation == nil {
		t.Fatal("No invocation found")
	}
	
	if input, ok := invocation.Context["input_data"]; !ok || input != "test value" {
		t.Errorf("Expected 'input_data' in invocation context, got: %v", invocation.Context)
	}
	
	if result, exists := state.Get("a2a_result"); !exists || result != "processed data" {
		t.Errorf("Expected 'processed data' in state, got: %v", result)
	}
	
	if status, exists := state.Get("_a2a_status"); !exists || status != "completed" {
		t.Errorf("Expected 'completed' status in state, got: %v", status)
	}
}

func TestA2ANodeGetAgentCard(t *testing.T) {
	client := NewMockA2AClient()
	
	// Register an agent card in the mock registry
	card := &a2a.AgentCard{
		ID:          "test-agent",
		Name:        "Test Agent",
		Description: "A test agent",
		Capabilities: []a2a.Capability{
			{Name: "data_processing"},
			{Name: "report_generation"},
		},
	}
	client.registry.Register(card)
	
	config := A2ANodeConfig{
		ID:      "test-node",
		Client:  client,
		AgentID: "test-agent",
	}
	
	node, err := NewA2ANode(config)
	if err != nil {
		t.Fatalf("Failed to create A2A node: %v", err)
	}
	
	ctx := context.Background()
	retrievedCard, err := node.GetAgentCard(ctx)
	if err != nil {
		t.Fatalf("Failed to get agent card: %v", err)
	}
	
	if retrievedCard.ID != "test-agent" {
		t.Errorf("Expected card ID 'test-agent', got '%s'", retrievedCard.ID)
	}
	
	if len(retrievedCard.Capabilities) != 2 {
		t.Errorf("Expected 2 capabilities, got %d", len(retrievedCard.Capabilities))
	}
}

func TestA2ANodeSupportsCapability(t *testing.T) {
	client := NewMockA2AClient()
	
	// Register an agent card with capabilities
	card := &a2a.AgentCard{
		ID:          "capability-test-agent",
		Name:        "Capability Test Agent",
		Capabilities: []a2a.Capability{
			{Name: "data_processing"},
			{Name: "report_generation"},
		},
	}
	client.registry.Register(card)
	
	config := A2ANodeConfig{
		ID:      "capability-test-node",
		Client:  client,
		AgentID: "capability-test-agent",
	}
	
	node, err := NewA2ANode(config)
	if err != nil {
		t.Fatalf("Failed to create A2A node: %v", err)
	}
	
	ctx := context.Background()
	
	// Test capability that exists
	supports, err := node.SupportsCapability(ctx, "data_processing")
	if err != nil {
		t.Fatalf("Error checking capability: %v", err)
	}
	
	if !supports {
		t.Error("Expected agent to support 'data_processing' capability")
	}
	
	// Test capability that doesn't exist
	supports, err = node.SupportsCapability(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Error checking nonexistent capability: %v", err)
	}
	
	if supports {
		t.Error("Expected agent not to support 'nonexistent' capability")
	}
}

func TestA2AGraphExecutor(t *testing.T) {
	client := NewMockA2AClient()
	
	// Create a graph with both regular and A2A nodes
	builder := NewBuilder("test-executor-graph")
	
	// Add a regular function node
	builder.AddFunction("regular-node", func(ctx context.Context, state State) error {
		state.Set("regular_result", "executed")
		return nil
	})
	
	// Add an A2A node
	config := A2ANodeConfig{
		ID:      "a2a-node",
		Client:  client,
		AgentID: "remote-agent",
	}
	a2aNode, err := NewA2ANode(config)
	if err != nil {
		t.Fatalf("Failed to create A2A node: %v", err)
	}
	builder.AddNode(a2aNode)
	
	// Add edges
	builder.AddEdge("regular-node", "a2a-node")
	
	// Set entry and finish
	builder.SetEntry("regular-node")
	builder.AddFinish("a2a-node")
	
	graph, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}
	
	// Create executor
	executor := NewA2AGraphExecutor(graph, client)
	
	// Execute
	initialState := NewMemoryState()
	result, err := executor.Execute(context.Background(), initialState)
	if err != nil {
		t.Fatalf("Graph execution failed: %v", err)
	}
	
	if !result.Success {
		t.Error("Expected graph execution to succeed")
	}
	
	if len(result.Nodes) != 2 {
		t.Errorf("Expected 2 node results, got %d", len(result.Nodes))
	}
	
	// Check that both nodes were executed successfully
	regularResult, exists := result.Nodes["regular-node"]
	if !exists || !regularResult.Success {
		t.Error("Regular node should have executed successfully")
	}
	
	a2aResult, exists := result.Nodes["a2a-node"]
	if !exists || !a2aResult.Success {
		t.Error("A2A node should have executed successfully")
	}
	
	// Verify the remote agent was invoked
	if _, exists := client.invocations["remote-agent"]; !exists {
		t.Error("Remote agent should have been invoked")
	}
}

func TestA2AGraphBuilderWithDiscovery(t *testing.T) {
	client := NewMockA2AClient()
	
	// Register agents with specific capabilities
	card1 := &a2a.AgentCard{
		ID:       "research-agent",
		Name:     "Research Agent",
		Endpoint: "http://research:8080",
		Capabilities: []a2a.Capability{
			{Name: "web_research"},
		},
	}
	card2 := &a2a.AgentCard{
		ID:       "analysis-agent",
		Name:     "Analysis Agent",
		Endpoint: "http://analysis:8080",
		Capabilities: []a2a.Capability{
			{Name: "data_analysis"},
		},
	}
	
	client.registry.Register(card1)
	client.registry.Register(card2)
	
	builder := NewA2AGraphBuilder("discovery-test-graph", client)
	
	// Add nodes by capability
	ctx := context.Background()
	builder.AddA2ANodeByCapability(ctx, "web_research", "research")

	// Since we know the node ID that will be created, set up the entry/finish
	builder.SetEntry("research_research-agent_0")
	builder.AddFinish("research_research-agent_0")

	graph, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build graph: %v", err)
	}
	
	// Should have added the research agent
	nodes := graph.AllNodes()
	
	// Look for a node with ID containing "research-agent"
	found := false
	for _, node := range nodes {
		if node.ID() == "research_research-agent_0" {
			found = true
			break
		}
	}
	
	if !found {
		t.Error("Expected to find a node created from discovered agent")
	}
}

func TestA2ANodeStatePropagation(t *testing.T) {
	client := NewMockA2AClient()
	
	// Create A2A node
	config := A2ANodeConfig{
		ID:      "state-test-node",
		Client:  client,
		AgentID: "state-agent",
	}
	
	node, err := NewA2ANode(config)
	if err != nil {
		t.Fatalf("Failed to create A2A node: %v", err)
	}
	
	// Create state with various data types
	state := NewMemoryState()
	state.Set("string_value", "hello")
	state.Set("int_value", 42)
	state.Set("bool_value", true)
	state.Set("list_value", []string{"a", "b", "c"})
	state.Set("map_value", map[string]interface{}{
		"nested": "value",
		"number": 123,
	})
	
	ctx := context.Background()
	err = node.Execute(ctx, state)
	if err != nil {
		t.Fatalf("Node execution failed: %v", err)
	}
	
	// Verify remote agent received all state values in its context
	invocation := client.invocations["state-agent"]
	if invocation == nil {
		t.Fatal("Remote agent was not invoked")
	}
	
	// Check that state values were passed to remote agent
	if val, ok := invocation.Context["string_value"]; !ok || val != "hello" {
		t.Errorf("String value not passed to remote agent: %v", val)
	}
	
	if val, ok := invocation.Context["int_value"]; !ok || val != 42 {
		t.Errorf("Int value not passed to remote agent: %v", val)
	}
	
	if val, ok := invocation.Context["bool_value"]; !ok || val != true {
		t.Errorf("Bool value not passed to remote agent: %v", val)
	}
	
	// Note: The actual values will be JSON-serialized, so complex types may change slightly
	// That's expected behavior for JSON-RPC communication
}