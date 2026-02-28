// Example demonstrating graph observability in Dagens
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/seyi/dagens/pkg/graph"
	"github.com/seyi/dagens/pkg/telemetry"
)

func main() {
	// Initialize telemetry collector
	collector, err := telemetry.NewTelemetryCollectorWithConfig(telemetry.CollectorConfig{
		TracerType:    telemetry.TracerOTEL,
		ExporterType:  telemetry.ExporterConsole, // Use console for demo
		ServiceName:   "graph-observability-demo",
		EnableMetrics: true,
		EnableLogging: true,
	})
	if err != nil {
		log.Fatal("Failed to initialize telemetry: ", err)
	}
	defer collector.Shutdown(context.Background())

	fmt.Println("Starting Graph Observability Demo...")
	
	// Create a simple graph with multiple nodes
	builder := graph.NewBuilder("observability-demo-graph")
	
	// Add nodes to the graph
	builder.AddFunction("start-node", func(ctx context.Context, state graph.State) error {
		fmt.Println("Executing start node")
		state.Set("start_time", time.Now().Unix())
		return nil
	})
	
	builder.AddFunction("process-node", func(ctx context.Context, state graph.State) error {
		fmt.Println("Executing process node")
		state.Set("processed", true)
		time.Sleep(100 * time.Millisecond) // Simulate work
		return nil
	})
	
	builder.AddFunction("end-node", func(ctx context.Context, state graph.State) error {
		fmt.Println("Executing end node")
		state.Set("end_time", time.Now().Unix())
		return nil
	})
	
	// Add edges between nodes
	builder.AddEdge("start-node", "process-node")
	builder.AddEdge("process-node", "end-node")
	
	// Set entry and finish nodes
	builder.SetEntry("start-node")
	builder.AddFinish("end-node")
	
	// Build the graph
	graphObj, err := builder.Build()
	if err != nil {
		log.Fatal("Failed to build graph: ", err)
	}
	
	// Create initial state
	state := graph.NewMemoryState()
	state.Set("demo", "graph-observability")
	
	// Execute the graph with observability
	fmt.Println("\nExecuting graph with observability...")
	err = graphObj.ExecuteWithObservability(context.Background(), state, collector)
	if err != nil {
		log.Fatal("Graph execution failed: ", err)
	}
	
	fmt.Println("\nGraph execution completed successfully!")
	fmt.Println("Check the console output for trace spans and metrics.")
	
	// Demonstrate state changes being tracked
	startTime, _ := state.Get("start_time")
	processed, _ := state.Get("processed")
	endTime, _ := state.Get("end_time")
	
	fmt.Printf("State values: startTime=%v, processed=%t, endTime=%v\n", 
		startTime, processed, endTime)
	
	// Force flush metrics
	collector.ForceFlush(context.Background())
	
	fmt.Println("\nDemo completed!")
}