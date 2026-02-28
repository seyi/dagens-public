package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/seyi/dagens/pkg/graph"
	"github.com/seyi/dagens/pkg/hitl"
)

func runDurableDemo() {
	log.Println("Starting Durable Stdio HITL Demo")

	// Create checkpoint store (using in-memory for demo)
	checkpointStore := hitl.NewRedisCheckpointStore() // Using in-memory implementation
	
	// Create response manager
	responseManager := hitl.NewHumanResponseManager()

	// Create a graph with nodes that demonstrate durable stdin/stdout checkpointing
	graphBuilder := graph.NewGraph("durable-stdio-hitl-demo")

	// Add nodes to the graph
	startNode := graph.NewFunctionNode("start", func(ctx context.Context, state graph.State) error {
		log.Println("Starting durable stdio HITL workflow")
		state.Set("workflow_id", "durable-stdio-demo-1")
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

	// Durable checkpoint node that will survive process restart
	// Using a 30-second timeout to trigger durable checkpointing
	stdioCheckpointNode := hitl.NewStdioCheckpointNode(hitl.StdioCheckpointNodeConfig{
		ID:                 "durable_stdin",
		Prompt:             "Please provide feedback on the processed data (or type 'continue' to proceed): ",
		StateKey:           "human_input",
		ContinueKey:        "should_continue",
		Timeout:            30 * time.Second, // Long timeout to trigger durable checkpointing
		ShortWaitThreshold: 5 * time.Second,  // Short threshold to force checkpointing
		CheckpointStore:    checkpointStore,
		ResponseManager:    responseManager,
		CallbackSecret:     []byte("demo-secret-key"), // In production, use a secure key
		GraphID:            "durable-stdio-hitl-demo",
		GraphVersion:       "v1.0.0",
	})
	graphBuilder.AddNode(stdioCheckpointNode)

	// Node to process the human input
	validateNode := graph.NewFunctionNode("validate_input", func(ctx context.Context, state graph.State) error {
		if input, exists := state.Get("human_input"); exists {
			log.Printf("Received human input: %v", input)
			inputStr := fmt.Sprintf("%v", input)

			// Update status based on input
			if inputStr == "continue" {
				state.Set("status", "approved")
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
				return status == "approved"
			}
			return false
		},
		TrueNode: graph.NewFunctionNode("continue_processing", func(ctx context.Context, state graph.State) error {
			log.Println("Continuing with processing based on human approval")
			state.Set("next_action", "continue")
			return nil
		}),
		FalseNode: graph.NewFunctionNode("halt_processing", func(ctx context.Context, state graph.State) error {
			log.Println("Halt processing based on human input")
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
	graphBuilder.AddEdge(graph.NewDirectEdge("display_state", "durable_stdin")) // Note: node ID
	graphBuilder.AddEdge(graph.NewDirectEdge("durable_stdin", "validate_input"))
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
	initialState.Set("graph_id", "durable-stdio-hitl-demo-1")
	initialState.Set("user_id", "demo-user")

	// Execute the graph using the HITL orchestrator for proper checkpoint handling
	log.Println("Executing durable stdio HITL graph...")
	
	// For this demo, we'll execute directly, but in a real implementation,
	// this would use the HITL orchestrator for proper checkpoint/resume handling
	if err := executeGraphDurable(context.Background(), graphBuilder, initialState); err != nil {
		log.Printf("Error executing graph: %v", err)
	}

	log.Println("Durable demo completed")
}

// executeGraphDurable executes the graph starting from the entry node
func executeGraphDurable(ctx context.Context, g *graph.Graph, initialState graph.State) error {
	// Get the entry node
	entryID := g.Entry()
	if entryID == "" {
		return fmt.Errorf("graph has no entry node")
	}

	// Execute starting from the entry node
	return traverseGraphDurable(ctx, g, entryID, initialState)
}

// traverseGraphDurable recursively executes nodes in the graph
func traverseGraphDurable(ctx context.Context, g *graph.Graph, nodeID string, state graph.State) error {
	// Get the node
	node, err := g.GetNode(nodeID)
	if err != nil {
		return fmt.Errorf("failed to get node %s: %w", nodeID, err)
	}

	// Execute the node
	if err := node.Execute(ctx, state); err != nil {
		// Check if this is a checkpoint pending error from the StdioCheckpointNode
		if err == hitl.ErrHumanInteractionPending {
			log.Printf("Checkpoint created for node %s, execution paused", nodeID)
			// In a real implementation, this would trigger the checkpoint mechanism
			// and the process would resume later with the saved state
			return nil
		}
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
				if err := traverseGraphDurable(ctx, g, targetID, state); err != nil {
					return err
				}
			}
		}
	}

	return nil
}