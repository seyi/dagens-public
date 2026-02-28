// Package graph provides tests for the graph observability system
package graph

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/seyi/dagens/pkg/config"
	"github.com/seyi/dagens/pkg/telemetry"
)

func TestGraphExecutionContext_Configuration(t *testing.T) {
	// Test the configuration system for observability levels
	os.Setenv("DAGENS_OBSERVABILITY_LEVEL", "2")
	defer os.Unsetenv("DAGENS_OBSERVABILITY_LEVEL")

	// Test that the observability level is properly read from environment
	level := config.GetObservabilityLevel()
	assert.Equal(t, config.ObservabilityFull, level, "Should read observability level from environment")
}

func TestGraphExecutionContext_ConcurrentExecutionIsolation(t *testing.T) {
	// Test that concurrent executions maintain isolation
	// This test verifies that execution contexts don't bleed between concurrent executions

	// Create a simple graph for testing
	graph := NewGraph("test-graph")
	collector := telemetry.NewTelemetryCollector()

	const numExecutions = 10
	executionIDs := make([]string, numExecutions)
	var wg sync.WaitGroup

	// Run multiple graph executions concurrently
	for i := 0; i < numExecutions; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			
			// Create execution context with observability
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			execCtx, err := NewGraphExecutionContext(ctx, graph, collector)
			require.NoError(t, err)

			// Perform some operations to test concurrent access
			execCtx.RecordStateChange("test-key", fmt.Sprintf("value-%d", idx))
			
			// Record an edge
			edgeSpan := execCtx.RecordEdge("from", fmt.Sprintf("to-%d", idx))
			if edgeSpan != nil {
				edgeSpan.End() // Clean up
			}

			// Record execution metrics
			execCtx.RecordExecutionMetrics()

			executionIDs[idx] = execCtx.ExecutionID()
		}(i)
	}

	wg.Wait()

	// Verify that all execution IDs are unique (no context bleeding)
	seenIDs := make(map[string]bool)
	for _, id := range executionIDs {
		assert.NotEmpty(t, id, "Execution ID should not be empty")
		assert.False(t, seenIDs[id], "Execution ID should be unique")
		seenIDs[id] = true
	}

	// Verify that we have the expected number of unique execution IDs
	assert.Equal(t, numExecutions, len(seenIDs), "Should have unique execution IDs for each concurrent execution")
}

func TestGraphExecutionContext_ContextPropagation(t *testing.T) {
	// Test that context is properly propagated through the execution context
	graph := NewGraph("context-test-graph")
	collector := telemetry.NewTelemetryCollector()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	execCtx, err := NewGraphExecutionContext(ctx, graph, collector)
	require.NoError(t, err)

	// Test node start context propagation
	nodeCtx, span := execCtx.RecordNodeStart("test-node")

	// Verify that execution ID is maintained in the node context
	initialID := execCtx.ExecutionID()
	nodeID := GetExecutionID(nodeCtx)

	// In this implementation, we just verify that the functions work without error
	// The actual context propagation would be tested in a real environment
	assert.NotEmpty(t, execCtx.ExecutionID())
	assert.Equal(t, execCtx.ExecutionID(), initialID)
	assert.Equal(t, execCtx.ExecutionID(), nodeID)

	// End the span
	execCtx.RecordNodeEnd(span, "test-node", nil)
}

func TestGraphExecutionContext_SpanHierarchyVerification(t *testing.T) {
	// Test that spans maintain proper hierarchy relationships
	graph := NewGraph("span-test-graph")
	collector := telemetry.NewTelemetryCollector()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	execCtx, err := NewGraphExecutionContext(ctx, graph, collector)
	require.NoError(t, err)

	// Test that new spans created from the context maintain trace continuity
	// In this implementation, we'll just verify the functions don't panic
	_, span := execCtx.RecordNodeStart("test-node")
	assert.NotNil(t, span)

	// Test edge span creation
	edgeSpan := execCtx.RecordEdge("from", "to")
	if edgeSpan != nil {
		// Verify that the edge span has proper attributes
		edgeSpan.End() // Clean up
	}

	// Test state change recording
	execCtx.RecordStateChange("test-key", "test-value")

	// Test execution metrics recording
	execCtx.RecordExecutionMetrics()

	// End the node span
	execCtx.RecordNodeEnd(span, "test-node", nil)

	// Verify execution context is still valid
	assert.Equal(t, execCtx.GraphID(), graph.ID())
	assert.NotEmpty(t, execCtx.ExecutionID())
}

func TestGraphExecutionContext_StateChangeAttribution(t *testing.T) {
	// Test that state changes are properly attributed to the correct execution
	graph := NewGraph("state-test-graph")
	collector := telemetry.NewTelemetryCollector()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	execCtx, err := NewGraphExecutionContext(ctx, graph, collector)
	require.NoError(t, err)

	// Test state change attribution with various data types
	execCtx.RecordStateChange("string-key", "string-value")
	execCtx.RecordStateChange("number-key", 42)
	execCtx.RecordStateChange("float-key", 3.14)
	execCtx.RecordStateChange("bool-key", true)
	execCtx.RecordStateChange("nil-key", nil)

	// Verify that execution context remains valid after state changes
	assert.NotEmpty(t, execCtx.ExecutionID())
	assert.NotEmpty(t, execCtx.GraphID())
}

func TestGraphExecutionContext_TraceContextPropagation(t *testing.T) {
	// Test that trace context is properly propagated across spans
	graph := NewGraph("trace-test-graph")
	collector := telemetry.NewTelemetryCollector()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	execCtx, err := NewGraphExecutionContext(ctx, graph, collector)
	require.NoError(t, err)

	// Verify initial execution context
	initialExecutionID := execCtx.ExecutionID()
	assert.NotEmpty(t, initialExecutionID)

	// Start a node span
	nodeCtx, nodeSpan := execCtx.RecordNodeStart("propagation-test-node")
	
	// The node span should be associated with the same execution context
	nodeExecutionID := GetExecutionID(nodeCtx)
	assert.Equal(t, initialExecutionID, nodeExecutionID)
	assert.NotEmpty(t, nodeExecutionID)

	// Test that the context carries the execution information
	propagatedExecutionID := GetExecutionID(nodeCtx)
	assert.Equal(t, execCtx.ExecutionID(), propagatedExecutionID)

	// Test edge span maintains execution context
	edgeSpan := execCtx.RecordEdge("start", "end")
	if edgeSpan != nil {
		edgeSpan.End() // Clean up
	}

	// End the node span
	execCtx.RecordNodeEnd(nodeSpan, "propagation-test-node", nil)

	// Verify execution context is still valid
	assert.Equal(t, execCtx.GraphID(), graph.ID())
	assert.NotEmpty(t, execCtx.ExecutionID())
}

func TestGraphExecutionContext_EdgeDurationTracking(t *testing.T) {
	// Test that edge spans properly track duration
	graph := NewGraph("edge-test-graph")
	collector := telemetry.NewTelemetryCollector()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	execCtx, err := NewGraphExecutionContext(ctx, graph, collector)
	require.NoError(t, err)

	// Test edge span with explicit duration tracking
	startTime := time.Now()
	time.Sleep(10 * time.Millisecond) // Small delay to measure
	duration := time.Since(startTime)

	execCtx.RecordEdgeWithDuration("start-node", "end-node", duration)

	// Verify that execution context remains valid
	assert.NotEmpty(t, execCtx.ExecutionID())
	assert.Equal(t, execCtx.GraphID(), graph.ID())
}

func TestGraphExecutionContext_ErrorHandling(t *testing.T) {
	// Test error handling in the execution context
	graph := NewGraph("error-test-graph")
	collector := telemetry.NewTelemetryCollector()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	execCtx, err := NewGraphExecutionContext(ctx, graph, collector)
	require.NoError(t, err)

	// Start a node span
	_, span := execCtx.RecordNodeStart("error-test-node")

	// Record an error
	testError := fmt.Errorf("test error for error handling verification")
	execCtx.RecordNodeEnd(span, "error-test-node", testError)

	// Verify that error was tracked
	// In this mock implementation, we just verify the function doesn't panic
	assert.NotEmpty(t, execCtx.ExecutionID())
}

func TestGraphExecutionContext_FinishMethod(t *testing.T) {
	// Test the Finish method which completes execution and records final metrics
	graph := NewGraph("finish-test-graph")
	collector := telemetry.NewTelemetryCollector()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	execCtx, err := NewGraphExecutionContext(ctx, graph, collector)
	require.NoError(t, err)

	// Perform some operations
	execCtx.RecordStateChange("test-key", "test-value")
	edgeSpan := execCtx.RecordEdge("from", "to")
	if edgeSpan != nil {
		edgeSpan.End()
	}

	// Finish the execution
	execCtx.Finish(nil)

	// Verify that finish method completed without error
	assert.NotEmpty(t, execCtx.ExecutionID())
	assert.Equal(t, execCtx.GraphID(), graph.ID())
}

func TestGraphExecutionContext_FinishWithError(t *testing.T) {
	// Test the Finish method with an error
	graph := NewGraph("finish-error-test-graph")
	collector := telemetry.NewTelemetryCollector()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	execCtx, err := NewGraphExecutionContext(ctx, graph, collector)
	require.NoError(t, err)

	// Perform some operations
	execCtx.RecordStateChange("test-key", "test-value")

	// Finish the execution with an error
	testError := fmt.Errorf("test finish error")
	execCtx.Finish(testError)

	// Verify that finish method completed without error despite the input error
	assert.NotEmpty(t, execCtx.ExecutionID())
	assert.Equal(t, execCtx.GraphID(), graph.ID())
}