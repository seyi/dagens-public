package graph

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/telemetry"
)

func BenchmarkGraphExecutionContext_Creation(b *testing.B) {
	os.Setenv("DAGENS_OBSERVABILITY_LEVEL", "2")
	defer os.Unsetenv("DAGENS_OBSERVABILITY_LEVEL")

	graph := NewGraph("benchmark-graph")
	collector := telemetry.NewTelemetryCollector()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := NewGraphExecutionContext(ctx, graph, collector)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGraphExecutionContext_RecordNodeStart(b *testing.B) {
	os.Setenv("DAGENS_OBSERVABILITY_LEVEL", "2")
	defer os.Unsetenv("DAGENS_OBSERVABILITY_LEVEL")

	graph := NewGraph("benchmark-graph")
	collector := telemetry.NewTelemetryCollector()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	execCtx, err := NewGraphExecutionContext(ctx, graph, collector)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, span := execCtx.RecordNodeStart("test-node")
		span.End()
	}
}

func BenchmarkGraphExecutionContext_RecordEdge(b *testing.B) {
	os.Setenv("DAGENS_OBSERVABILITY_LEVEL", "2")
	defer os.Unsetenv("DAGENS_OBSERVABILITY_LEVEL")

	graph := NewGraph("benchmark-graph")
	collector := telemetry.NewTelemetryCollector()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	execCtx, err := NewGraphExecutionContext(ctx, graph, collector)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		execCtx.RecordEdge("from-node", "to-node")
	}
}

func BenchmarkGraphExecutionContext_RecordStateChange(b *testing.B) {
	os.Setenv("DAGENS_OBSERVABILITY_LEVEL", "2")
	defer os.Unsetenv("DAGENS_OBSERVABILITY_LEVEL")

	graph := NewGraph("benchmark-graph")
	collector := telemetry.NewTelemetryCollector()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	execCtx, err := NewGraphExecutionContext(ctx, graph, collector)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		execCtx.RecordStateChange("key", "value")
	}
}

func BenchmarkGraphExecutionContext_RecordExecutionMetrics(b *testing.B) {
	os.Setenv("DAGENS_OBSERVABILITY_LEVEL", "2")
	defer os.Unsetenv("DAGENS_OBSERVABILITY_LEVEL")

	graph := NewGraph("benchmark-graph")
	collector := telemetry.NewTelemetryCollector()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	execCtx, err := NewGraphExecutionContext(ctx, graph, collector)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		execCtx.RecordExecutionMetrics()
	}
}

func BenchmarkGraphExecutionContext_Finish(b *testing.B) {
	os.Setenv("DAGENS_OBSERVABILITY_LEVEL", "2")
	defer os.Unsetenv("DAGENS_OBSERVABILITY_LEVEL")

	graph := NewGraph("benchmark-graph")
	collector := telemetry.NewTelemetryCollector()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		execCtx, err := NewGraphExecutionContext(ctx, graph, collector)
		if err != nil {
			b.Fatal(err)
		}
		execCtx.Finish(nil)
	}
}

// Benchmark to compare performance with and without observability
func BenchmarkGraphExecution_WithObservability(b *testing.B) {
	os.Setenv("DAGENS_OBSERVABILITY_LEVEL", "2")
	defer os.Unsetenv("DAGENS_OBSERVABILITY_LEVEL")

	graph := NewGraph("benchmark-graph")
	collector := telemetry.NewTelemetryCollector()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		execCtx, err := NewGraphExecutionContext(ctx, graph, collector)
		if err != nil {
			b.Fatal(err)
		}

		// Simulate some node execution
		_, span := execCtx.RecordNodeStart("test-node")
		execCtx.RecordStateChange("test-key", "test-value")
		execCtx.RecordEdge("from", "to")
		execCtx.RecordExecutionMetrics()
		execCtx.RecordNodeEnd(span, "test-node", nil)

		execCtx.Finish(nil)
	}
}

func BenchmarkGraphExecution_WithoutObservability(b *testing.B) {
	os.Setenv("DAGENS_OBSERVABILITY_LEVEL", "0")
	defer os.Unsetenv("DAGENS_OBSERVABILITY_LEVEL")

	graph := NewGraph("benchmark-graph")
	collector := telemetry.NewTelemetryCollector()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		execCtx, err := NewGraphExecutionContext(ctx, graph, collector)
		if err != nil {
			b.Fatal(err)
		}

		// Simulate some node execution (should be no-op with observability disabled)
		_, span := execCtx.RecordNodeStart("test-node")
		execCtx.RecordStateChange("test-key", "test-value")
		execCtx.RecordEdge("from", "to")
		execCtx.RecordExecutionMetrics()
		execCtx.RecordNodeEnd(span, "test-node", nil)

		execCtx.Finish(nil)
	}
}

// Benchmark to measure overhead of observability
func BenchmarkGraphExecution_OverheadComparison(b *testing.B) {
	graph := NewGraph("benchmark-graph")
	collector := telemetry.NewTelemetryCollector()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Run with observability enabled
	os.Setenv("DAGENS_OBSERVABILITY_LEVEL", "2")
	b.Run("WithObservability", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			execCtx, _ := NewGraphExecutionContext(ctx, graph, collector)
			_, span := execCtx.RecordNodeStart("test-node")
			execCtx.RecordStateChange("test-key", "test-value")
			execCtx.RecordEdge("from", "to")
			execCtx.RecordExecutionMetrics()
			execCtx.RecordNodeEnd(span, "test-node", nil)
			execCtx.Finish(nil)
		}
	})

	// Run with observability disabled
	os.Setenv("DAGENS_OBSERVABILITY_LEVEL", "0")
	b.Run("WithoutObservability", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			execCtx, _ := NewGraphExecutionContext(ctx, graph, collector)
			_, span := execCtx.RecordNodeStart("test-node")
			execCtx.RecordStateChange("test-key", "test-value")
			execCtx.RecordEdge("from", "to")
			execCtx.RecordExecutionMetrics()
			execCtx.RecordNodeEnd(span, "test-node", nil)
			execCtx.Finish(nil)
		}
	})
}