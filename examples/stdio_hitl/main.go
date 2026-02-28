package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/seyi/dagens/pkg/graph"
)

func main() {
	log.Println("Starting Stdio HITL Demo")

	// Create a graph with nodes that demonstrate stdin/stdout interaction
	graphBuilder := graph.NewGraph("stdio-hitl-demo")

	// Add nodes to the graph
	startNode := graph.NewFunctionNode("start", func(ctx context.Context, state graph.State) error {
		log.Println("Starting stdio HITL workflow")
		state.Set("workflow_id", "stdio-demo-1")
		state.Set("status", "started")
		return nil
	})
	graphBuilder.AddNode(startNode)

	// Node that processes some data and stores it in state
	processNode := graph.NewFunctionNode("process", func(ctx context.Context, state graph.State) error {
		log.Println("Processing data...")
		if workflowID, exists := state.Get("workflow_id"); exists {
			processedData := fmt.Sprintf("processed_data_from_%v", workflowID)
			state.Set("processed_data", processedData)
			log.Printf("Set processed data: %s", processedData)
		}
		return nil
	})
	graphBuilder.AddNode(processNode)

	// Stdout node to display current state
	displayNode := graph.NewStdoutNode(graph.StdoutNodeConfig{
		ID:       "display_state",
		Template: "Current status: {{.status}}\nProcessed data: {{.processed_data}}\n",
	})
	graphBuilder.AddNode(displayNode)

	// Stdin node to get human input
	inputNode := graph.NewStdinNode(graph.StdinNodeConfig{
		ID:       "get_input",
		Prompt:   "Please provide feedback on the processed data (or type 'continue' to proceed): ",
		StateKey: "human_input",
		TrimSpace: true,
	})
	graphBuilder.AddNode(inputNode)

	// Node to process the human input
	validateNode := graph.NewFunctionNode("validate_input", func(ctx context.Context, state graph.State) error {
		if input, exists := state.Get("human_input"); exists {
			log.Printf("Received human input: %v", input)
			inputStr := fmt.Sprintf("%v", input)
			
			// Update status based on input
			if strings.ToLower(inputStr) == "continue" || strings.Contains(strings.ToLower(inputStr), "ok") {
				state.Set("status", "approved")
			} else if strings.Contains(strings.ToLower(inputStr), "reject") {
				state.Set("status", "rejected")
			} else {
				state.Set("status", "reviewed")
			}
			
			// Store the feedback
			state.Set("human_feedback", inputStr)
		}
		return nil
	})
	graphBuilder.AddNode(validateNode)

	// Conditional node to decide next steps based on input
	decisionNode := graph.NewConditionalNode(graph.ConditionalConfig{
		ID:        "decision",
		Condition: func(s graph.State) bool { 
			if status, exists := s.Get("status"); exists {
				return status == "approved" || status == "reviewed"
			}
			return false
		},
		TrueNode: graph.NewFunctionNode("continue_processing", func(ctx context.Context, state graph.State) error {
			log.Println("Continuing with processing based on human approval")
			state.Set("next_action", "continue")
			return nil
		}),
		FalseNode: graph.NewFunctionNode("halt_processing", func(ctx context.Context, state graph.State) error {
			log.Println("Halt processing based on human rejection")
			state.Set("next_action", "halt")
			return nil
		}),
	})
	graphBuilder.AddNode(decisionNode)

	// Final display node
	finalDisplayNode := graph.NewStdoutNode(graph.StdoutNodeConfig{
		ID:       "final_display",
		Template: "Workflow completed.\nFinal status: {{.status}}\nNext action: {{.next_action}}\n",
	})
	graphBuilder.AddNode(finalDisplayNode)

	// Set up graph structure
	graphBuilder.AddEdge(graph.NewDirectEdge("start", "process"))
	graphBuilder.AddEdge(graph.NewDirectEdge("process", "display_state"))
	graphBuilder.AddEdge(graph.NewDirectEdge("display_state", "get_input"))
	graphBuilder.AddEdge(graph.NewDirectEdge("get_input", "validate_input"))
	graphBuilder.AddEdge(graph.NewDirectEdge("validate_input", "decision"))
	graphBuilder.AddEdge(graph.NewDirectEdge("decision", "final_display"))
	graphBuilder.SetEntry("start")
	graphBuilder.AddFinish("final_display")

	// Validate the graph
	if err := graphBuilder.Validate(); err != nil {
		log.Fatalf("Invalid graph: %v", err)
	}

	log.Printf("Graph created with %d nodes", graphBuilder.NodeCount())

	// Create initial state
	initialState := graph.NewMemoryState()
	initialState.Set("graph_id", "stdio-hitl-demo-1")
	initialState.Set("user_id", "demo-user")

	// Execute the graph
	log.Println("Executing stdio HITL graph...")
	if err := executeGraphMain(context.Background(), graphBuilder, initialState); err != nil {
		log.Printf("Error executing graph: %v", err)
	}

	log.Println("Demo completed")

	fmt.Println("\n\n=== Starting Advanced Demo ===")
	runAdvancedDemo()

	fmt.Println("\n\n=== Starting Durable Demo ===")
	runDurableDemo()

	fmt.Println("\n\n=== Starting Full Lifecycle Demo ===")
	runFullLifecycleDemo()
}

// executeGraphMain executes the graph starting from the entry node
func executeGraphMain(ctx context.Context, g *graph.Graph, initialState graph.State) error {
	// Get the entry node
	entryID := g.Entry()
	if entryID == "" {
		return fmt.Errorf("graph has no entry node")
	}

	// Execute starting from the entry node
	return traverseGraphMain(ctx, g, entryID, initialState)
}

// traverseGraphMain recursively executes nodes in the graph
func traverseGraphMain(ctx context.Context, g *graph.Graph, nodeID string, state graph.State) error {
	// Get the node
	node, err := g.GetNode(nodeID)
	if err != nil {
		return fmt.Errorf("failed to get node %s: %w", nodeID, err)
	}

	// Execute the node
	if err := node.Execute(ctx, state); err != nil {
		return fmt.Errorf("failed to execute node %s: %w", nodeID, err)
	}

	// Get outgoing edges
	edges := g.GetEdges(nodeID)

	// Traverse each edge that should be followed
	for _, edge := range edges {
		if edge.ShouldTraverse(state) {
			// For dynamic edges, we need to get the target with state
			targetID := edge.To()
			if edge.Type() == "dynamic" {
				if dynamicEdge, ok := edge.(*graph.DynamicEdge); ok {
					targetID = dynamicEdge.ToWithState(state)
				}
			}

			if targetID != "" {
				if err := traverseGraphMain(ctx, g, targetID, state); err != nil {
					return err
				}
			}
		}
	}

	return nil
}