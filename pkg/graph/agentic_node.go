// Package graph provides graph-based agent execution with agentic capabilities.
//
// This file provides agentic wrappers for graph nodes that delegate to the
// pkg/agentic package for self-monitoring, self-healing, and feedback.
package graph

import (
	"context"
	"time"

	"github.com/seyi/dagens/pkg/agentic"
	"github.com/seyi/dagens/pkg/telemetry"
)

// AgenticNode wraps a graph node with self-monitoring and self-healing capabilities.
type AgenticNode struct {
	original   Node
	monitoring *agentic.MonitoringContext
	healing    *agentic.SelfHealing
	feedback   agentic.FeedbackReceiver
}

// AgenticNodeOption configures an AgenticNode.
type AgenticNodeOption func(*AgenticNode)

// WithNodeMonitoring sets a custom monitoring context.
func WithNodeMonitoring(mc *agentic.MonitoringContext) AgenticNodeOption {
	return func(n *AgenticNode) {
		n.monitoring = mc
	}
}

// WithNodeHealing sets the maximum retries for self-healing.
func WithNodeHealing(maxRetries int) AgenticNodeOption {
	return func(n *AgenticNode) {
		config := agentic.DefaultSelfHealingConfig()
		config.MaxRetries = maxRetries
		n.healing = agentic.NewSelfHealing(config, n.monitoring)
	}
}

// WithNodeFeedback sets a feedback receiver.
func WithNodeFeedback(fb agentic.FeedbackReceiver) AgenticNodeOption {
	return func(n *AgenticNode) {
		n.feedback = fb
	}
}

// NewAgenticNode wraps a graph node with agentic capabilities.
func NewAgenticNode(node Node, opts ...AgenticNodeOption) Node {
	collector := telemetry.NewTelemetryCollector()
	monitoring := agentic.NewMonitoringContext(
		node.ID(),
		node.Type(),
		"graph_node",
		collector,
	)

	an := &AgenticNode{
		original:   node,
		monitoring: monitoring,
		healing:    agentic.NewSelfHealing(agentic.DefaultSelfHealingConfig(), monitoring),
		feedback:   agentic.NewSimpleFeedbackReceiver(),
	}

	for _, opt := range opts {
		opt(an)
	}

	// Ensure healing has monitoring reference
	if an.healing != nil && an.healing.CurrentRetry() == 0 {
		an.healing = agentic.NewSelfHealing(agentic.DefaultSelfHealingConfig(), an.monitoring)
	}

	return an
}

// Execute runs the node with self-monitoring and self-healing.
func (n *AgenticNode) Execute(ctx context.Context, state State) error {
	startTime := time.Now()

	// Start trace span
	ctx, span := n.monitoring.StartSpan(ctx, "node.execute")
	defer span.End()

	// Record execution start
	n.monitoring.RecordExecutionCount()

	// Execute with self-healing
	err := n.healing.ExecuteWithHealing(ctx, func(ctx context.Context) error {
		return n.original.Execute(ctx, state)
	})

	// Record execution duration
	duration := time.Since(startTime)
	n.monitoring.RecordExecutionDuration(duration)

	if err != nil {
		span.SetStatus(telemetry.StatusError, err.Error())
		n.monitoring.LogError("node execution failed", map[string]interface{}{
			"error":    err.Error(),
			"duration": duration.Seconds(),
			"retries":  n.healing.CurrentRetry(),
		})
		return err
	}

	// Record success
	span.SetStatus(telemetry.StatusOK, "success")
	n.monitoring.RecordHealth(agentic.HealthOK)

	return nil
}

// RecordOutcome records the node's outcome score.
func (n *AgenticNode) RecordOutcome(score float64) {
	n.monitoring.RecordOutcome(score)
}

// RecordDomainMetric records a domain-specific metric.
func (n *AgenticNode) RecordDomainMetric(name string, value float64) {
	n.monitoring.RecordDomainGauge(name, value)
}

// Monitoring returns the monitoring context for advanced usage.
func (n *AgenticNode) Monitoring() *agentic.MonitoringContext {
	return n.monitoring
}

// =============================================================================
// DELEGATE METHODS TO ORIGINAL NODE
// =============================================================================

// ID returns the node's ID.
func (n *AgenticNode) ID() string { return n.original.ID() }

// Name returns the node's name.
func (n *AgenticNode) Name() string { return n.original.Name() }

// Type returns the node's type with "-agentic" suffix.
func (n *AgenticNode) Type() string { return n.original.Type() + "-agentic" }

// Metadata returns the node's metadata with agentic info added.
func (n *AgenticNode) Metadata() map[string]interface{} {
	meta := n.original.Metadata()
	if meta == nil {
		meta = make(map[string]interface{})
	}
	meta["agentic"] = true
	meta["capabilities"] = []string{"monitoring", "healing"}
	meta["original_type"] = n.original.Type()
	return meta
}

// =============================================================================
// AGENTIC GRAPH WRAPPER
// =============================================================================

// AgenticGraph wraps an entire graph with agentic capabilities applied to all nodes.
type AgenticGraph struct {
	*Graph
}

// NewAgenticGraph creates a graph with agentic capabilities applied to all nodes.
func NewAgenticGraph(graph *Graph, opts ...AgenticNodeOption) *AgenticGraph {
	// Clone the graph to avoid modifying the original
	graphCopy := graph.Clone()

	// Get all nodes
	nodes := graphCopy.AllNodes()

	// Create a new graph
	newGraph := NewGraphWithConfig(GraphConfig{
		ID:       graphCopy.ID(),
		Name:     graphCopy.Name() + "-agentic",
		Metadata: graphCopy.metadata,
	})

	// Add agentic versions of all nodes
	for _, node := range nodes {
		agenticNode := NewAgenticNode(node, opts...)
		newGraph.AddNode(agenticNode)
	}

	// Add all edges
	for _, node := range nodes {
		edges := graph.GetEdges(node.ID())
		for _, edge := range edges {
			newEdge := NewDirectEdge(edge.From(), edge.To())
			newGraph.AddEdge(newEdge)
		}
	}

	// Set entry and finish nodes
	newGraph.SetEntry(graphCopy.Entry())
	for _, finishNode := range graphCopy.FinishNodes() {
		newGraph.AddFinish(finishNode)
	}

	return &AgenticGraph{Graph: newGraph}
}

// ExecuteGraphWithAgenticCapabilities executes a graph with agentic capabilities.
func ExecuteGraphWithAgenticCapabilities(ctx context.Context, graph *Graph, state State, opts ...AgenticNodeOption) error {
	agenticGraph := NewAgenticGraph(graph, opts...)
	return executeGraph(ctx, agenticGraph.Graph, state)
}

// executeGraph executes a graph from its entry node.
func executeGraph(ctx context.Context, graph *Graph, state State) error {
	if err := graph.Validate(); err != nil {
		return err
	}

	entryNodeID := graph.Entry()
	if entryNodeID == "" {
		return nil
	}

	return traverseNode(ctx, graph, entryNodeID, state, make(map[string]bool))
}

// traverseNode recursively executes nodes in the graph.
func traverseNode(ctx context.Context, graph *Graph, nodeID string, state State, visited map[string]bool) error {
	if visited[nodeID] {
		return nil // Already visited
	}
	visited[nodeID] = true

	node, err := graph.GetNode(nodeID)
	if err != nil {
		return err
	}

	if err := node.Execute(ctx, state); err != nil {
		return err
	}

	edges := graph.GetEdges(nodeID)
	for _, edge := range edges {
		if err := traverseNode(ctx, graph, edge.To(), state, visited); err != nil {
			return err
		}
	}

	return nil
}

// =============================================================================
// BACKWARD COMPATIBILITY - Deprecated types (use pkg/agentic instead)
// =============================================================================

// Deprecated: Use agentic.MonitoringContext instead
type SelfMonitoringCapability = agentic.MonitoringContext

// Deprecated: Use agentic.SelfHealing instead
type SelfHealingCapability = agentic.SelfHealing

// Deprecated: Use agentic.FeedbackReceiver instead
type SelfImprovingCapability = agentic.SimpleFeedbackReceiver
