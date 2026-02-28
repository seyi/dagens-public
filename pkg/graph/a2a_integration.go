// Package graph provides integration with the A2A (Agent-to-Agent) protocol
// to enable distributed graph execution across multiple agent instances.
package graph

import (
	"context"
	"fmt"

	"github.com/seyi/dagens/pkg/a2a"
	"github.com/seyi/dagens/pkg/agent"
)

// A2ANode represents a remote agent node in the graph that communicates via A2A protocol
type A2ANode struct {
	*BaseNode
	client   a2a.A2AClient
	agentID  string
	endpoint string
	card     *a2a.AgentCard
}

// A2ANodeConfig holds configuration for creating an A2A node
type A2ANodeConfig struct {
	ID       string
	Name     string
	Client   a2a.A2AClient
	AgentID  string
	Endpoint string
	Card     *a2a.AgentCard // Optional pre-fetched card
}

// NewA2ANode creates a new A2A node that represents a remote agent
func NewA2ANode(config A2ANodeConfig) (*A2ANode, error) {
	if config.Client == nil {
		return nil, fmt.Errorf("A2A client is required")
	}
	if config.AgentID == "" {
		return nil, fmt.Errorf("agent ID is required")
	}

	node := &A2ANode{
		BaseNode: NewBaseNode(config.ID, "a2a"),
		client:   config.Client,
		agentID:  config.AgentID,
		endpoint: config.Endpoint,
		card:     config.Card,
	}

	// Set name if provided
	if config.Name != "" {
		node.SetMetadata("name", config.Name)
	}

	return node, nil
}

// Execute invokes the remote agent via A2A protocol
func (n *A2ANode) Execute(ctx context.Context, state State) error {
	// Convert graph state to agent input
	input := &agent.AgentInput{
		Instruction: "Execute graph node",
		Context:     make(map[string]interface{}),
	}

	// Copy all state values to agent context
	for _, key := range state.Keys() {
		if val, exists := state.Get(key); exists {
			input.Context[key] = val
		}
	}

	// Add metadata about graph execution
	input.Context["graph_node_id"] = n.ID()
	input.Context["graph_node_type"] = n.Type()

	// Invoke remote agent
	output, err := n.client.InvokeAgent(ctx, n.agentID, input)
	if err != nil {
		return fmt.Errorf("failed to invoke remote agent %s: %w", n.agentID, err)
	}

	// Update state with results from remote agent
	if output != nil {
		// Update metadata
		if output.Metadata != nil {
			for key, value := range output.Metadata {
				state.Set(fmt.Sprintf("_a2a_%s", key), value)
			}
		}

		// Store result if available
		if output.Result != nil {
			state.Set("a2a_result", output.Result)
		}
	}

	return nil
}

// GetAgentCard fetches or returns the cached agent card
func (n *A2ANode) GetAgentCard(ctx context.Context) (*a2a.AgentCard, error) {
	if n.card != nil {
		return n.card, nil
	}

	card, err := n.client.GetAgentCard(ctx, n.agentID)
	if err != nil {
		return nil, fmt.Errorf("failed to get agent card: %w", err)
	}

	n.card = card
	return card, nil
}

// SupportsCapability checks if the remote agent supports a specific capability
func (n *A2ANode) SupportsCapability(ctx context.Context, capability string) (bool, error) {
	card, err := n.GetAgentCard(ctx)
	if err != nil {
		return false, err
	}

	for _, cap := range card.Capabilities {
		if cap.Name == capability {
			return true, nil
		}
	}

	return false, nil
}

// A2AGraphBuilder extends the graph builder with A2A capabilities
type A2AGraphBuilder struct {
	*Builder
	client a2a.A2AClient
}

// NewA2AGraphBuilder creates a graph builder that can add A2A nodes
func NewA2AGraphBuilder(name string, client a2a.A2AClient) *A2AGraphBuilder {
	return &A2AGraphBuilder{
		Builder: NewBuilder(name),
		client:  client,
	}
}

// AddA2ANode adds a remote agent node to the graph
func (b *A2AGraphBuilder) AddA2ANode(config A2ANodeConfig) *A2AGraphBuilder {
	node, err := NewA2ANode(config)
	if err != nil {
		b.errors = append(b.errors, err)
		return b
	}
	b.AddNode(node)
	return b
}

// AddA2ANodeByCapability discovers and adds remote agents with specific capabilities
func (b *A2AGraphBuilder) AddA2ANodeByCapability(ctx context.Context, capability string, prefix string) *A2AGraphBuilder {
	agents, err := b.client.DiscoverAgents(ctx, capability)
	if err != nil {
		b.errors = append(b.errors, fmt.Errorf("failed to discover agents with capability %s: %w", capability, err))
		return b
	}

	// Add each discovered agent as a node with a unique ID
	for i, agentInfo := range agents {
		config := A2ANodeConfig{
			ID:      fmt.Sprintf("%s_%s_%d", prefix, agentInfo.ID, i),
			Name:    agentInfo.Name,
			Client:  b.client,
			AgentID: agentInfo.ID,
			Endpoint: agentInfo.Endpoint,
		}
		b.AddA2ANode(config)
	}

	return b
}

// A2AEdge extends the graph with edges that can route based on A2A agent capabilities
type A2AEdge struct {
	*DirectEdge
	sourceAgentID string
	targetAgentID string
	condition     func(ctx context.Context, client a2a.A2AClient, state State) (bool, error)
}

// A2AEdgeConfig holds configuration for A2A-aware edges
type A2AEdgeConfig struct {
	From      string
	To        string
	Condition func(ctx context.Context, client a2a.A2AClient, state State) (bool, error)
	Client    a2a.A2AClient
}

// NewA2AEdge creates an edge that can make routing decisions based on A2A agent capabilities
func NewA2AEdge(cfg A2AEdgeConfig) *A2AEdge {
	edge := &A2AEdge{
		DirectEdge:    NewDirectEdge(cfg.From, cfg.To),
		sourceAgentID: cfg.From,
		targetAgentID: cfg.To,
		condition:     cfg.Condition,
	}
	return edge
}

// ShouldTraverse evaluates the A2A-aware condition
func (e *A2AEdge) ShouldTraverse(state State) bool {
	if e.condition == nil {
		return e.DirectEdge.ShouldTraverse(state)
	}

	// Create a background context for the condition evaluation
	ctx := context.Background()

	// We need to provide the A2A client somehow - for now, assume it's accessible
	// In a real implementation, this would be more sophisticated
	success, err := e.condition(ctx, nil, state) // Client would be provided in a real implementation
	return err == nil && success
}

// A2AGraphExecutor provides graph execution with A2A integration
type A2AGraphExecutor struct {
	graph  *Graph
	client a2a.A2AClient
}

// NewA2AGraphExecutor creates an executor that can handle A2A nodes
func NewA2AGraphExecutor(graph *Graph, client a2a.A2AClient) *A2AGraphExecutor {
	return &A2AGraphExecutor{
		graph:  graph,
		client: client,
	}
}

// Execute runs the graph with A2A node support
func (e *A2AGraphExecutor) Execute(ctx context.Context, initialState State) (*GraphExecutionResult, error) {
	// Initialize execution state
	executionState := initialState
	if executionState == nil {
		executionState = NewMemoryState()
	}

	// Get the entry node
	entryID := e.graph.Entry()
	if entryID == "" {
		return nil, fmt.Errorf("graph has no entry node")
	}

	// Perform topological traversal starting from the entry node
	visited := make(map[string]bool)
	result := &GraphExecutionResult{
		Success: true,
		Nodes:   make(map[string]NodeExecutionResult),
	}

	err := e.traverseNode(ctx, entryID, executionState, visited, result)
	if err != nil {
		result.Success = false
		result.Error = err
	}

	return result, nil
}

// traverseNode recursively executes nodes in the graph
func (e *A2AGraphExecutor) traverseNode(ctx context.Context, nodeID string, state State, visited map[string]bool, result *GraphExecutionResult) error {
	if visited[nodeID] {
		return nil // Already visited (handles cycles)
	}
	visited[nodeID] = true

	// Get the node
	node, err := e.graph.GetNode(nodeID)
	if err != nil {
		return fmt.Errorf("failed to get node %s: %w", nodeID, err)
	}

	// Execute the node based on its type
	nodeResult := NodeExecutionResult{
		NodeID:    nodeID,
		StartTime: 0, // In a real implementation, track execution time
	}

	switch n := node.(type) {
	case *A2ANode:
		// Special handling for A2A nodes
		err = n.Execute(ctx, state)
		if err != nil {
			nodeResult.Error = err
			result.Nodes[nodeID] = nodeResult
			return fmt.Errorf("A2A node execution failed: %w", err)
		}
	default:
		// Execute regular node
		err = node.Execute(ctx, state)
		if err != nil {
			nodeResult.Error = err
			result.Nodes[nodeID] = nodeResult
			return fmt.Errorf("node execution failed: %w", err)
		}
	}

	nodeResult.Success = true
	result.Nodes[nodeID] = nodeResult

	// Process outgoing edges and continue traversal
	edges := e.graph.GetEdges(nodeID)
	for _, edge := range edges {
		if edge.ShouldTraverse(state) {
			err := e.traverseNode(ctx, edge.To(), state, visited, result)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// GraphExecutionResult holds the result of graph execution
type GraphExecutionResult struct {
	Success bool
	Error   error
	Nodes   map[string]NodeExecutionResult
}

// NodeExecutionResult holds the result of a single node execution
type NodeExecutionResult struct {
	NodeID    string
	Success   bool
	Error     error
	StartTime int64 // In a real implementation, use proper time tracking
}