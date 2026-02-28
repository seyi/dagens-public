package main

import (
	"context"
	"fmt"
	"log"

	"github.com/seyi/dagens/pkg/graph"
)

func main() {
	fmt.Println("=== Simple Graph Example ===")

	// Create a simple graph using the builder API
	g, err := graph.NewBuilder("greeting-bot").
		// Add nodes
		AddFunction("greet", greetingFunction).
		AddFunction("respond", responseFunction).
		// Connect nodes
		AddEdge("greet", "respond").
		// Set entry and finish
		SetEntry("greet").
		AddFinish("respond").
		// Build
		Build()

	if err != nil {
		log.Fatalf("Failed to build graph: %v", err)
	}

	fmt.Printf("Graph '%s' created successfully!\n", g.Name())
	fmt.Printf("- Nodes: %d\n", g.NodeCount())
	fmt.Printf("- Entry: %s\n", g.Entry())
	fmt.Printf("- Finish nodes: %v\n\n", g.FinishNodes())

	// Create state and set input
	state := graph.NewMemoryState()
	state.Set("user_name", "Alice")

	// Execute the graph manually (backend not yet implemented)
	ctx := context.Background()

	fmt.Println("Executing graph...")

	// Execute entry node
	entryNode, _ := g.GetNode(g.Entry())
	if err := entryNode.Execute(ctx, state); err != nil {
		log.Fatalf("Entry node execution failed: %v", err)
	}

	// Get next nodes via edges
	edges := g.GetEdges(g.Entry())
	for _, edge := range edges {
		if edge.ShouldTraverse(state) {
			nextNode, _ := g.GetNode(edge.To())
			if err := nextNode.Execute(ctx, state); err != nil {
				log.Fatalf("Node execution failed: %v", err)
			}
		}
	}

	// Display final state
	fmt.Println("\nFinal state:")
	for _, key := range state.Keys() {
		val, _ := state.Get(key)
		fmt.Printf("  %s: %v\n", key, val)
	}
}

func greetingFunction(ctx context.Context, state graph.State) error {
	name, exists := state.Get("user_name")
	if !exists {
		name = "stranger"
	}

	greeting := fmt.Sprintf("Hello, %v! Welcome to the graph execution system.", name)
	state.Set("greeting", greeting)

	fmt.Printf("[greet] Generated: %s\n", greeting)
	return nil
}

func responseFunction(ctx context.Context, state graph.State) error {
	greeting, exists := state.Get("greeting")
	if !exists {
		return fmt.Errorf("no greeting found in state")
	}

	response := fmt.Sprintf("%s How can I help you today?", greeting)
	state.Set("response", response)

	fmt.Printf("[respond] Generated: %s\n", response)
	return nil
}
