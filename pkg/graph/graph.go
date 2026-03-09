package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// ClonableNode is an optional extension for node implementations that support
// true deep cloning semantics.
type ClonableNode interface {
	Node
	Clone() Node
}

// ClonableEdge is an optional extension for edge implementations that support
// true deep cloning semantics.
type ClonableEdge interface {
	Edge
	Clone() Edge
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

// Clone creates a structural copy of the graph.
//
// By default, Node/Edge instances are shared by reference unless they implement
// ClonableNode/ClonableEdge. This avoids breaking existing node implementations
// while enabling callers to opt into deep clone behavior.
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

	// Copy nodes (prefer deep clone when supported).
	for id, node := range g.nodes {
		if clonable, ok := node.(ClonableNode); ok {
			newGraph.nodes[id] = clonable.Clone()
			continue
		}
		newGraph.nodes[id] = node
	}

	// Copy edges (prefer deep clone when supported).
	for from, edgeList := range g.edges {
		newGraph.edges[from] = make([]Edge, len(edgeList))
		for i, edge := range edgeList {
			if clonable, ok := edge.(ClonableEdge); ok {
				newGraph.edges[from][i] = clonable.Clone()
				continue
			}
			newGraph.edges[from][i] = edge
		}
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

const (
	// PauseRequestIDStateKey is the well-known state key set by HITL-style nodes
	// before yielding execution for human interaction.
	PauseRequestIDStateKey = "_hitl_request_id"
)

// PausedResult is returned when graph execution yields for human interaction.
type PausedResult struct {
	RequestID    string `json:"request_id"`
	NodeID       string `json:"node_id"`
	GraphVersion string `json:"graph_version"`
	TraceID      string `json:"trace_id,omitempty"`
	TraceParent  string `json:"trace_parent,omitempty"`
}

// ExecutionResult captures terminal graph execution outcomes.
type ExecutionResult struct {
	Paused *PausedResult `json:"paused,omitempty"`
}

// IsPaused reports whether execution yielded for a pause/resume boundary.
func (r *ExecutionResult) IsPaused() bool {
	return r != nil && r.Paused != nil
}

type graphPausedError struct {
	result *PausedResult
	cause  error
}

func (e *graphPausedError) Error() string { return e.cause.Error() }
func (e *graphPausedError) Unwrap() error { return e.cause }

var graphExecutionContextFactory = NewGraphExecutionContext

// GraphPauseMetricsEvent is a graph-neutral pause transition signal.
type GraphPauseMetricsEvent struct {
	GraphID      string
	NodeID       string
	RequestID    string
	GraphVersion string
	TraceID      string
	TraceParent  string
}

// GraphFailureMetricsEvent is a graph-neutral execution failure signal.
type GraphFailureMetricsEvent struct {
	GraphID   string
	RequestID string
	Operation string
	Error     string
}

// GraphMetricsHooks allows external metrics bridges to consume graph execution transitions
// without introducing direct pkg/graph -> pkg/hitl coupling.
type GraphMetricsHooks struct {
	OnPause   func(GraphPauseMetricsEvent)
	OnFailure func(GraphFailureMetricsEvent)
}

var graphMetricsHooks = GraphMetricsHooks{
	OnPause:   func(GraphPauseMetricsEvent) {},
	OnFailure: func(GraphFailureMetricsEvent) {},
}
var graphMetricsHooksMu sync.RWMutex

// SetGraphMetricsHooks sets graph-neutral execution hooks and returns a restore function.
// Passing nil handlers leaves their previous values unchanged.
func SetGraphMetricsHooks(hooks GraphMetricsHooks) func() {
	graphMetricsHooksMu.Lock()
	prev := graphMetricsHooks
	if hooks.OnPause != nil {
		graphMetricsHooks.OnPause = hooks.OnPause
	}
	if hooks.OnFailure != nil {
		graphMetricsHooks.OnFailure = hooks.OnFailure
	}
	graphMetricsHooksMu.Unlock()
	return func() {
		graphMetricsHooksMu.Lock()
		graphMetricsHooks = prev
		graphMetricsHooksMu.Unlock()
	}
}

func emitPauseHook(event GraphPauseMetricsEvent) {
	graphMetricsHooksMu.RLock()
	onPause := graphMetricsHooks.OnPause
	graphMetricsHooksMu.RUnlock()
	onPause(event)
}

func emitFailureHook(event GraphFailureMetricsEvent) {
	graphMetricsHooksMu.RLock()
	onFailure := graphMetricsHooks.OnFailure
	graphMetricsHooksMu.RUnlock()
	onFailure(event)
}

// ExecuteWithResult executes the graph and returns a first-class paused contract
// when a node yields for human interaction.
func (g *Graph) ExecuteWithResult(ctx context.Context, state State) (*ExecutionResult, error) {
	logger := telemetry.GetGlobalTelemetry().GetLogger()
	requestID := requestIDFromState(state)
	if err := g.Validate(); err != nil {
		logger.Error("graph validation failed", map[string]interface{}{
			"operation":  "graph.execute.validate",
			"graph_id":   g.ID(),
			"request_id": requestID,
			"error":      err.Error(),
		})
		emitFailureHook(GraphFailureMetricsEvent{
			GraphID:   g.ID(),
			RequestID: requestID,
			Operation: "graph.execute.validate",
			Error:     err.Error(),
		})
		return nil, fmt.Errorf("graph validation failed: %w", err)
	}

	entryNodeID := g.Entry()
	if entryNodeID == "" {
		return &ExecutionResult{}, nil
	}

	err := g.traverseNode(ctx, entryNodeID, state, make(map[string]bool))
	if err == nil {
		return &ExecutionResult{}, nil
	}

	var pausedErr *graphPausedError
	if errors.As(err, &pausedErr) {
		emitPauseHook(GraphPauseMetricsEvent{
			GraphID:      g.ID(),
			NodeID:       pausedErr.result.NodeID,
			RequestID:    pausedErr.result.RequestID,
			GraphVersion: pausedErr.result.GraphVersion,
			TraceID:      pausedErr.result.TraceID,
			TraceParent:  pausedErr.result.TraceParent,
		})
		return &ExecutionResult{Paused: pausedErr.result}, nil
	}
	logger.Error("graph execution failed", map[string]interface{}{
		"operation":  "graph.execute.run",
		"graph_id":   g.ID(),
		"request_id": requestID,
		"error":      err.Error(),
	})
	emitFailureHook(GraphFailureMetricsEvent{
		GraphID:   g.ID(),
		RequestID: requestID,
		Operation: "graph.execute.run",
		Error:     err.Error(),
	})

	return nil, err
}

// ResumeFromPaused resumes graph traversal from a previously paused node.
// The paused node itself is not re-executed; traversal continues from its
// outgoing edges.
func (g *Graph) ResumeFromPaused(ctx context.Context, state State, paused *PausedResult) (*ExecutionResult, error) {
	if paused == nil {
		return nil, fmt.Errorf("paused result is required")
	}
	if paused.NodeID == "" {
		return nil, fmt.Errorf("paused node_id is required")
	}

	if err := g.Validate(); err != nil {
		return nil, fmt.Errorf("graph validation failed: %w", err)
	}

	// Validate node exists before attempting resume traversal.
	if _, err := g.GetNode(paused.NodeID); err != nil {
		return nil, fmt.Errorf("resume node lookup failed: %w", err)
	}

	// Optional compatibility check to prevent resuming with a mismatched graph build.
	currentVersion := g.graphVersion()
	if paused.GraphVersion != "" && currentVersion != "" && paused.GraphVersion != currentVersion {
		return nil, fmt.Errorf("graph version mismatch: paused=%s current=%s", paused.GraphVersion, currentVersion)
	}

	// Preserve request_id context for downstream pause/resume boundaries.
	if paused.RequestID != "" {
		state.Set(PauseRequestIDStateKey, paused.RequestID)
	}

	edges := g.GetEdges(paused.NodeID)
	if len(edges) == 0 {
		return &ExecutionResult{}, nil
	}

	visited := map[string]bool{paused.NodeID: true}
	for _, edge := range edges {
		if err := g.traverseNode(ctx, edge.To(), state, visited); err != nil {
			var pausedErr *graphPausedError
			if errors.As(err, &pausedErr) {
				emitPauseHook(GraphPauseMetricsEvent{
					GraphID:      g.ID(),
					NodeID:       pausedErr.result.NodeID,
					RequestID:    pausedErr.result.RequestID,
					GraphVersion: pausedErr.result.GraphVersion,
					TraceID:      pausedErr.result.TraceID,
					TraceParent:  pausedErr.result.TraceParent,
				})
				return &ExecutionResult{Paused: pausedErr.result}, nil
			}
			emitFailureHook(GraphFailureMetricsEvent{
				GraphID:   g.ID(),
				RequestID: requestIDFromState(state),
				Operation: "graph.resume.run",
				Error:     err.Error(),
			})
			return nil, err
		}
	}

	return &ExecutionResult{}, nil
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

// traverseNode recursively executes nodes in the graph.
func (g *Graph) traverseNode(ctx context.Context, nodeID string, state State, visited map[string]bool) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if visited[nodeID] {
		return nil // Already visited
	}
	visited[nodeID] = true

	node, err := g.GetNode(nodeID)
	if err != nil {
		return err
	}

	if err := node.Execute(ctx, state); err != nil {
		if paused, ok := g.extractPausedResult(ctx, err, nodeID, state); ok {
			return &graphPausedError{result: paused, cause: err}
		}
		return err
	}

	edges := g.GetEdges(nodeID)
	for _, edge := range edges {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := g.traverseNode(ctx, edge.To(), state, visited); err != nil {
			return err
		}
	}

	return nil
}

// ExecuteWithObservability executes the graph with full observability.
func (g *Graph) ExecuteWithObservability(ctx context.Context, state State, collector *telemetry.TelemetryCollector) error {
	// Validate the graph first
	if err := g.Validate(); err != nil {
		return fmt.Errorf("graph validation failed: %w", err)
	}

	// Create execution context with observability
	execCtx, err := graphExecutionContextFactory(ctx, g, collector)
	if err != nil {
		// Graceful degradation: continue execution via non-observability path.
		result, runErr := g.ExecuteWithResult(ctx, state)
		if runErr != nil {
			return runErr
		}
		if result != nil && result.IsPaused() {
			return NewPauseSignal(*result.Paused, nil)
		}
		return nil
	}

	var runErr error
	defer func() {
		// Pause is a yield boundary, not an execution failure.
		var pauseSig PauseSignal
		if runErr != nil && (errors.As(runErr, &pauseSig) || isLegacyPauseSignal(runErr)) {
			execCtx.Finish(nil)
			return
		}
		execCtx.Finish(runErr)
	}()

	// Execute the graph
	entryNodeID := g.Entry()
	if entryNodeID == "" {
		return nil
	}

	// Execute from entry node with observability
	runErr = g.traverseNodeWithObservability(execCtx.Context(), entryNodeID, state, make(map[string]bool), execCtx)
	return runErr
}

func (g *Graph) extractPausedResult(ctx context.Context, err error, nodeID string, state State) (*PausedResult, bool) {
	requestID := requestIDFromState(state)
	graphVersion := g.graphVersion()
	traceID, traceParent := traceContextFromContext(ctx)

	// Preferred: typed pause signal contract.
	var pauseSig PauseSignal
	if errors.As(err, &pauseSig) {
		paused := normalizePausedResult(pauseSig.PauseResult(), nodeID, graphVersion, requestID)
		if paused.TraceID == "" {
			paused.TraceID = traceID
		}
		if paused.TraceParent == "" {
			paused.TraceParent = traceParent
		}
		return paused, true
	}

	// Compatibility: explicit request marker in state for legacy pause errors.
	if requestID != "" && isLegacyPauseSignal(err) {
		return &PausedResult{
			RequestID:    requestID,
			NodeID:       nodeID,
			GraphVersion: graphVersion,
			TraceID:      traceID,
			TraceParent:  traceParent,
		}, true
	}

	// Last-resort compatibility bridge for legacy error-only pause signals.
	if isLegacyPauseSignal(err) {
		return &PausedResult{
			RequestID:    requestID,
			NodeID:       nodeID,
			GraphVersion: graphVersion,
			TraceID:      traceID,
			TraceParent:  traceParent,
		}, true
	}

	return nil, false
}

func (g *Graph) graphVersion() string {
	if v, ok := g.GetMetadata("graph_version"); ok {
		return fmt.Sprint(v)
	}
	if v, ok := g.GetMetadata("version"); ok {
		return fmt.Sprint(v)
	}
	return ""
}

func traceContextFromContext(ctx context.Context) (string, string) {
	if ctx == nil {
		return "", ""
	}
	span := telemetry.GetGlobalTelemetry().GetTracer().GetSpan(ctx)
	if span == nil {
		return "", ""
	}
	traceID := span.TraceID()
	spanID := span.SpanID()
	if traceID == "" {
		return "", ""
	}
	traceParent := ""
	if isW3CTraceID(traceID) && isW3CSpanID(spanID) {
		traceParent = fmt.Sprintf("00-%s-%s-01", strings.ToLower(traceID), strings.ToLower(spanID))
	} else if spanID != "" {
		traceParent = fmt.Sprintf("trace_id=%s;span_id=%s", traceID, spanID)
	}
	return traceID, traceParent
}

func isW3CTraceID(v string) bool {
	return len(v) == 32 && isHexString(v)
}

func isW3CSpanID(v string) bool {
	return len(v) == 16 && isHexString(v)
}

func isHexString(v string) bool {
	for _, ch := range v {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') && (ch < 'A' || ch > 'F') {
			return false
		}
	}
	return true
}

// traverseNodeWithObservability recursively executes nodes in the graph with observability.
func (g *Graph) traverseNodeWithObservability(ctx context.Context, nodeID string, state State, visited map[string]bool, execCtx *GraphExecutionContext) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
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
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
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
