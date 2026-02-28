// Package graph provides graph-based agent execution with enhanced observability.
//
// This file provides the core observability infrastructure for graph execution,
// including distributed tracing, metrics, and execution context propagation.
package graph

import (
	"context"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/config"
	"github.com/seyi/dagens/pkg/telemetry"
	"go.opentelemetry.io/otel/trace"
)

// GraphExecutionContext holds the observability context for a graph execution.
type GraphExecutionContext struct {
	ctx          context.Context
	graphID      string
	executionID  string
	rootSpan     telemetry.Span
	collector    *telemetry.TelemetryCollector
	startTime    time.Time
	nodeCount    int
	errorCount   int
}

// NewGraphExecutionContext creates a new execution context with observability.
func NewGraphExecutionContext(ctx context.Context, graph *Graph, collector *telemetry.TelemetryCollector) (*GraphExecutionContext, error) {
	// Check if observability is enabled
	level := config.GetObservabilityLevel()
	if level == config.ObservabilityNone {
		// Return a minimal context when observability is disabled
		executionID := generateExecutionID()
		return &GraphExecutionContext{
			ctx:         ctx,
			graphID:     graph.ID(),
			executionID: executionID,
			rootSpan:    &NoOpSpan{}, // Use no-op span when disabled
			collector:   collector,
			startTime:   time.Now(),
			nodeCount:   graph.NodeCount(),
		}, nil
	}

	if collector == nil {
		collector = telemetry.NewTelemetryCollector()
	}

	// Create tracer from the collector's tracer
	otelTracer := collector.GetTracer()

	// Start root span for the entire graph execution
	ctx, span := otelTracer.StartSpan(ctx, "graph.execute")
	span.SetAttribute("graph.id", graph.ID())
	span.SetAttribute("graph.name", graph.Name())
	span.SetAttribute("graph.nodes", fmt.Sprintf("%d", graph.NodeCount()))
	span.SetAttribute("graph.entry", graph.Entry())

	executionID := generateExecutionID()
	gctx := &GraphExecutionContext{
		ctx:         ctx,
		graphID:     graph.ID(),
		executionID: executionID,
		rootSpan:    span,
		collector:   collector,
		startTime:   time.Now(),
		nodeCount:   graph.NodeCount(),
	}

	// Add execution ID to context for propagation
	gctx.ctx = context.WithValue(gctx.ctx, executionIDKey{}, executionID)

	return gctx, nil
}

// executionIDKey is the key type for execution ID in context
type executionIDKey struct{}

// GetExecutionID retrieves the execution ID from context
func GetExecutionID(ctx context.Context) string {
	if id, ok := ctx.Value(executionIDKey{}).(string); ok {
		return id
	}
	return ""
}

// Context returns the execution context with tracing
func (g *GraphExecutionContext) Context() context.Context {
	return g.ctx
}

// GraphID returns the graph ID
func (g *GraphExecutionContext) GraphID() string {
	return g.graphID
}

// ExecutionID returns the execution ID
func (g *GraphExecutionContext) ExecutionID() string {
	return g.executionID
}

// RecordNodeStart records the start of a node execution
func (g *GraphExecutionContext) RecordNodeStart(nodeID string) (context.Context, telemetry.Span) {
	level := config.GetObservabilityLevel()
	if level == config.ObservabilityNone {
		return g.ctx, &NoOpSpan{}
	}

	// Use the current context (which should contain the trace context) to create child span
	ctx, span := g.collector.GetTracer().StartSpan(g.ctx, "node.execute")
	span.SetAttribute("node.id", nodeID)
	span.SetAttribute("graph.id", g.graphID)
	span.SetAttribute("graph.execution_id", g.executionID)
	return ctx, span
}

// RecordNodeEnd records the end of a node execution
func (g *GraphExecutionContext) RecordNodeEnd(span telemetry.Span, nodeID string, err error) {
	if err != nil {
		span.SetStatus(telemetry.StatusError, err.Error())
		span.SetAttribute("error.message", err.Error())
		g.errorCount++
	} else {
		span.SetStatus(telemetry.StatusOK, "success")
	}
	span.End()
}

// EdgeSpan represents a span for edge transfer between nodes
type EdgeSpan struct {
	span      telemetry.Span
	startTime time.Time
}

// End completes the edge span and records duration
func (e *EdgeSpan) End() {
	duration := time.Since(e.startTime)
	e.span.SetAttribute("edge.duration_ms", duration.Milliseconds())
	e.span.End()
}

// RecordEdge records the execution of an edge between nodes and returns a span for tracking duration
func (g *GraphExecutionContext) RecordEdge(fromNode, toNode string) *EdgeSpan {
	level := config.GetObservabilityLevel()
	if level == config.ObservabilityNone {
		return nil // Skip edge recording when observability is disabled
	}

	// Create a span for the edge transfer
	_, span := g.collector.GetTracer().StartSpan(g.ctx, "edge.transfer")
	span.SetAttribute("edge.from", fromNode)
	span.SetAttribute("edge.to", toNode)
	span.SetAttribute("graph.execution_id", g.executionID)
	span.SetAttribute("timestamp", time.Now().Unix())

	edgeSpan := &EdgeSpan{
		span:      span,
		startTime: time.Now(),
	}

	return edgeSpan
}

// RecordEdgeWithDuration records the execution of an edge with explicit duration tracking
func (g *GraphExecutionContext) RecordEdgeWithDuration(fromNode, toNode string, duration time.Duration) {
	level := config.GetObservabilityLevel()
	if level == config.ObservabilityNone {
		return // Skip edge recording when observability is disabled
	}

	// Create a span for the edge transfer
	_, span := g.collector.GetTracer().StartSpan(g.ctx, "edge.transfer")
	span.SetAttribute("edge.from", fromNode)
	span.SetAttribute("edge.to", toNode)
	span.SetAttribute("graph.execution_id", g.executionID)
	span.SetAttribute("timestamp", time.Now().Unix())
	span.SetAttribute("edge.duration_ms", duration.Milliseconds())
	span.End()
}

// RecordStateChange records a state change during execution
func (g *GraphExecutionContext) RecordStateChange(key string, value interface{}) {
	// Add event to root span for state changes
	g.rootSpan.AddEvent("state.change", map[string]interface{}{
		"state.key":   key,
		"state.value": fmt.Sprintf("%v", value),
		"timestamp":   time.Now().Unix(),
	})
}

// RecordExecutionMetrics records execution metrics
func (g *GraphExecutionContext) RecordExecutionMetrics() {
	duration := time.Since(g.startTime)

	// Record metrics using the collector
	metrics := g.collector.GetMeter()
	if metrics != nil {
		// Record execution duration
		metrics.Histogram("graph.execution.duration").Record(duration.Seconds())

		// Record node count
		metrics.Counter("graph.nodes.processed").Add(float64(g.nodeCount))

		// Record error count
		if g.errorCount > 0 {
			metrics.Counter("graph.errors").Add(float64(g.errorCount))
		}
	} else {
		// Log when metrics collection is not available
		fmt.Printf("WARNING: Metrics collector not available for graph %s, execution %s\n", g.graphID, g.executionID)
	}
}

// Finish completes the graph execution and records final metrics
func (g *GraphExecutionContext) Finish(err error) {
	if err != nil {
		g.rootSpan.SetStatus(telemetry.StatusError, err.Error())
		g.rootSpan.SetAttribute("error.message", err.Error())
	} else {
		g.rootSpan.SetStatus(telemetry.StatusOK, "graph execution completed successfully")
	}

	// Record final metrics
	g.RecordExecutionMetrics()

	// Add final attributes
	g.rootSpan.SetAttribute("graph.execution_id", g.executionID)
	g.rootSpan.SetAttribute("graph.duration_seconds", time.Since(g.startTime).Seconds())
	g.rootSpan.SetAttribute("graph.nodes_processed", g.nodeCount)
	g.rootSpan.SetAttribute("graph.errors", g.errorCount)

	g.rootSpan.End()
}

// NoOpSpan is a no-op implementation of the telemetry.Span interface
type NoOpSpan struct{}

func (n *NoOpSpan) SetAttribute(key string, value interface{}) {}

func (n *NoOpSpan) SetStatus(code telemetry.StatusCode, message string) {}

func (n *NoOpSpan) AddEvent(name string, attributes map[string]interface{}) {}

func (n *NoOpSpan) End() {}

func (n *NoOpSpan) Context() context.Context {
	return context.Background()
}

func (n *NoOpSpan) TraceID() string {
	return ""
}

func (n *NoOpSpan) SpanID() string {
	return ""
}

func (n *NoOpSpan) SpanContext() trace.SpanContext {
	return trace.SpanContext{}
}

// generateExecutionID creates a unique execution ID
func generateExecutionID() string {
	return fmt.Sprintf("exec-%d", time.Now().UnixNano())
}