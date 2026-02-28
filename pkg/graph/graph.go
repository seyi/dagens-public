package graph

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/seyi/dagens/pkg/telemetry"
)

// Graph represents a directed graph of nodes (agents/tools) with execution semantics.
// It defines the workflow structure for agent execution.
type Graph struct {
	id          string
	name        string
	nodes       map[string]Node
	edges       map[string][]Edge
	entryNode   string
	finishNodes []string
	mu          sync.RWMutex
	metadata    map[string]interface{}
}

// GraphConfig holds configuration for creating a new graph.
type GraphConfig struct {
	ID       string                 // Optional custom ID
	Name     string                 // Graph name
	Metadata map[string]interface{} // Optional metadata
}

// NewGraph creates a new empty graph with the given name.
func NewGraph(name string) *Graph {
	return NewGraphWithConfig(GraphConfig{
		ID:       uuid.New().String(),
		Name:     name,
		Metadata: make(map[string]interface{}),
	})
}

// NewGraphWithConfig creates a new graph with the given configuration.
func NewGraphWithConfig(cfg GraphConfig) *Graph {
	id := cfg.ID
	if id == "" {
		id = uuid.New().String()
	}

	metadata := cfg.Metadata
	if metadata == nil {
		metadata = make(map[string]interface{})
	}

	return &Graph{
		id:          id,
		name:        cfg.Name,
		nodes:       make(map[string]Node),
		edges:       make(map[string][]Edge),
		finishNodes: make([]string, 0),
		metadata:    metadata,
	}
}

// ID returns the graph's unique identifier.
func (g *Graph) ID() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.id
}

// Name returns the graph's name.
func (g *Graph) Name() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.name
}

// AddNode adds a node to the graph.
func (g *Graph) AddNode(node Node) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if node == nil {
		return fmt.Errorf("cannot add nil node")
	}

	nodeID := node.ID()
	if _, exists := g.nodes[nodeID]; exists {
		return fmt.Errorf("node %s already exists in graph", nodeID)
	}

	g.nodes[nodeID] = node
	return nil
}

// GetNode retrieves a node by ID.
func (g *Graph) GetNode(nodeID string) (Node, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	node, exists := g.nodes[nodeID]
	if !exists {
		return nil, fmt.Errorf("node %s not found", nodeID)
	}
	return node, nil
}

// AddEdge adds an edge from one node to another.
func (g *Graph) AddEdge(edge Edge) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if edge == nil {
		return fmt.Errorf("cannot add nil edge")
	}

	fromID := edge.From()
	toID := edge.To()

	// Validate that both nodes exist
	if _, exists := g.nodes[fromID]; !exists {
		return fmt.Errorf("source node %s not found", fromID)
	}
	if _, exists := g.nodes[toID]; !exists {
		return fmt.Errorf("target node %s not found", toID)
	}

	// Add edge to adjacency list
	g.edges[fromID] = append(g.edges[fromID], edge)
	return nil
}

// GetEdges returns all edges from a given node.
func (g *Graph) GetEdges(fromID string) []Edge {
	g.mu.RLock()
	defer g.mu.RUnlock()

	edges, exists := g.edges[fromID]
	if !exists {
		return []Edge{}
	}

	// Return a copy to prevent external modification
	result := make([]Edge, len(edges))
	copy(result, edges)
	return result
}

// SetEntry sets the entry point node for the graph.
func (g *Graph) SetEntry(nodeID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.nodes[nodeID]; !exists {
		return fmt.Errorf("entry node %s not found", nodeID)
	}

	g.entryNode = nodeID
	return nil
}

// Entry returns the entry node ID.
func (g *Graph) Entry() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.entryNode
}

// AddFinish marks a node as a potential finish node.
func (g *Graph) AddFinish(nodeID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.nodes[nodeID]; !exists {
		return fmt.Errorf("finish node %s not found", nodeID)
	}

	// Check if already in finish nodes
	for _, fn := range g.finishNodes {
		if fn == nodeID {
			return nil // Already added
		}
	}

	g.finishNodes = append(g.finishNodes, nodeID)
	return nil
}

// FinishNodes returns all nodes marked as finish nodes.
func (g *Graph) FinishNodes() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := make([]string, len(g.finishNodes))
	copy(result, g.finishNodes)
	return result
}

// AllNodes returns all nodes in the graph.
func (g *Graph) AllNodes() []Node {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := make([]Node, 0, len(g.nodes))
	for _, node := range g.nodes {
		result = append(result, node)
	}
	return result
}

// NodeCount returns the number of nodes in the graph.
func (g *Graph) NodeCount() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.nodes)
}

// Validate checks if the graph is valid for execution.
func (g *Graph) Validate() error {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// Must have at least one node
	if len(g.nodes) == 0 {
		return fmt.Errorf("graph has no nodes")
	}

	// Must have an entry node
	if g.entryNode == "" {
		return fmt.Errorf("graph has no entry node")
	}

	// Entry node must exist
	if _, exists := g.nodes[g.entryNode]; !exists {
		return fmt.Errorf("entry node %s does not exist", g.entryNode)
	}

	// Must have at least one finish node
	if len(g.finishNodes) == 0 {
		return fmt.Errorf("graph has no finish nodes")
	}

	// All finish nodes must exist
	for _, fn := range g.finishNodes {
		if _, exists := g.nodes[fn]; !exists {
			return fmt.Errorf("finish node %s does not exist", fn)
		}
	}

	// Check for reachability (entry must be able to reach at least one finish node)
	if !g.canReachFinish() {
		return fmt.Errorf("entry node cannot reach any finish node")
	}

	return nil
}

// canReachFinish checks if the entry node can reach at least one finish node.
// This is a simplified reachability check using DFS.
func (g *Graph) canReachFinish() bool {
	visited := make(map[string]bool)
	finishMap := make(map[string]bool)
	for _, fn := range g.finishNodes {
		finishMap[fn] = true
	}

	var dfs func(nodeID string) bool
	dfs = func(nodeID string) bool {
		if visited[nodeID] {
			return false
		}
		visited[nodeID] = true

		// Check if this is a finish node
		if finishMap[nodeID] {
			return true
		}

		// Check outgoing edges
		for _, edge := range g.edges[nodeID] {
			if dfs(edge.To()) {
				return true
			}
		}

		return false
	}

	return dfs(g.entryNode)
}

// SetMetadata sets a metadata value.
func (g *Graph) SetMetadata(key string, value interface{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.metadata[key] = value
}

// GetMetadata retrieves a metadata value.
func (g *Graph) GetMetadata(key string) (interface{}, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	val, exists := g.metadata[key]
	return val, exists
}

// Clone creates a deep copy of the graph.
func (g *Graph) Clone() *Graph {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// Create new graph
	newGraph := &Graph{
		id:          uuid.New().String(), // New ID for clone
		name:        g.name + "-clone",
		nodes:       make(map[string]Node),
		edges:       make(map[string][]Edge),
		entryNode:   g.entryNode,
		finishNodes: make([]string, len(g.finishNodes)),
		metadata:    make(map[string]interface{}),
	}

	// Copy nodes
	for id, node := range g.nodes {
		newGraph.nodes[id] = node
	}

	// Copy edges
	for from, edgeList := range g.edges {
		newGraph.edges[from] = make([]Edge, len(edgeList))
		copy(newGraph.edges[from], edgeList)
	}

	// Copy finish nodes
	copy(newGraph.finishNodes, g.finishNodes)

	// Copy metadata
	for k, v := range g.metadata {
		newGraph.metadata[k] = v
	}

	return newGraph
}

// ExecutionContext provides context for graph execution.
type ExecutionContext struct {
	GraphID     string
	ExecutionID string
	Metadata    map[string]interface{}
	Context     context.Context
}

// NewExecutionContext creates a new execution context.
func NewExecutionContext(ctx context.Context, graphID string) *ExecutionContext {
	return &ExecutionContext{
		GraphID:     graphID,
		ExecutionID: uuid.New().String(),
		Metadata:    make(map[string]interface{}),
		Context:     ctx,
	}
}

// ExecuteWithObservability executes the graph with full observability.
func (g *Graph) ExecuteWithObservability(ctx context.Context, state State, collector *telemetry.TelemetryCollector) error {
	// Validate the graph first
	if err := g.Validate(); err != nil {
		return fmt.Errorf("graph validation failed: %w", err)
	}

	// Create execution context with observability
	execCtx, err := NewGraphExecutionContext(ctx, g, collector)
	if err != nil {
		return fmt.Errorf("failed to create execution context: %w", err)
	}
	defer execCtx.Finish(nil) // Will be updated with actual error

	// Execute the graph
	entryNodeID := g.Entry()
	if entryNodeID == "" {
		return nil
	}

	// Execute from entry node with observability
	err = g.traverseNodeWithObservability(execCtx.Context(), entryNodeID, state, make(map[string]bool), execCtx)
	if err != nil {
		execCtx.Finish(err) // Update with actual error
		return err
	}

	return nil
}

// traverseNodeWithObservability recursively executes nodes in the graph with observability.
func (g *Graph) traverseNodeWithObservability(ctx context.Context, nodeID string, state State, visited map[string]bool, execCtx *GraphExecutionContext) error {
	if visited[nodeID] {
		return nil // Already visited
	}
	visited[nodeID] = true

	node, err := g.GetNode(nodeID)
	if err != nil {
		return err
	}

	// Record node execution start
	nodeCtx, span := execCtx.RecordNodeStart(nodeID)
	defer func() {
		execCtx.RecordNodeEnd(span, nodeID, err)
	}()

	// Execute the node
	err = node.Execute(nodeCtx, state)
	if err != nil {
		return err
	}

	// Get edges from this node
	edges := g.GetEdges(nodeID)
	for _, edge := range edges {
		// Record edge execution with duration tracking
		edgeSpan := execCtx.RecordEdge(nodeID, edge.To())

		// Continue traversal to the next node
		if err := g.traverseNodeWithObservability(ctx, edge.To(), state, visited, execCtx); err != nil {
			// End the edge span if there's an error
			if edgeSpan != nil {
				edgeSpan.End()
			}
			return err
		}

		// End the edge span after successful traversal
		if edgeSpan != nil {
			edgeSpan.End()
		}
	}

	return nil
}
