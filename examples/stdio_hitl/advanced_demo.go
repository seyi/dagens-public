package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/seyi/dagens/pkg/graph"
)

// This example demonstrates a more complex workflow with checkpoint/resume functionality
func runAdvancedDemo() {
	log.Println("Starting Advanced Stdio HITL Demo with Checkpoint/Resume")

	// Create a graph with nodes that demonstrate checkpoint/resume with stdin/stdout
	graphBuilder := graph.NewGraph("stdio-checkpoint-demo")

	// Start node
	startNode := graph.NewFunctionNode("start", func(ctx context.Context, state graph.State) error {
		log.Println("Starting advanced stdio HITL workflow")
		state.Set("workflow_id", "stdio-checkpoint-demo-1")
		state.Set("status", "started")
		state.Set("step", "start")
		return nil
	})
	graphBuilder.AddNode(startNode)

	// Process some initial data
	processNode := graph.NewFunctionNode("process", func(ctx context.Context, state graph.State) error {
		log.Println("Processing initial data...")
		if workflowID, exists := state.Get("workflow_id"); exists {
			processedData := fmt.Sprintf("processed_data_from_%v", workflowID)
			state.Set("processed_data", processedData)
			log.Printf("Set processed data: %s", processedData)
		}
		state.Set("step", "processing")
		return nil
	})
	graphBuilder.AddNode(processNode)

	// Display current state
	displayNode := graph.NewStdoutNode(graph.StdoutNodeConfig{
		ID:       "display_state",
		Template: "Current step: {{.step}}\nProcessed data: {{.processed_data}}\n",
	})
	graphBuilder.AddNode(displayNode)

	// Checkpoint node that waits for human input with potential for checkpointing
	// In a real implementation, this would integrate with the HITL checkpoint system
	checkpointNode := graph.NewStdioCheckpointNode(graph.StdioCheckpointNodeConfig{
		ID:       "checkpoint_input",
		Prompt:   "Do you want to continue? (type 'continue' to proceed, or 'checkpoint' to pause): ",
		StateKey: "checkpoint_input",
		ContinueKey: "should_continue",
		Timeout: 30, // 30 seconds timeout
	})
	graphBuilder.AddNode(checkpointNode)

	// Process the checkpoint input
	processCheckpointInput := graph.NewFunctionNode("process_checkpoint_input", func(ctx context.Context, state graph.State) error {
		if input, exists := state.Get("checkpoint_input"); exists {
			inputStr := fmt.Sprintf("%v", input)
			log.Printf("Processing checkpoint input: %s", inputStr)
			
			if strings.ToLower(inputStr) == "checkpoint" {
				// In a real implementation, this would trigger checkpoint creation
				// For this demo, we'll just simulate the checkpoint behavior
				log.Println("Checkpoint requested - simulating checkpoint creation")
				state.Set("status", "checkpointed")
				state.Set("step", "checkpointed")
				
				// This would normally pause execution and wait for resume
				// In this demo, we'll just continue but mark as checkpointed
				return nil
			} else {
				log.Println("Continuing without checkpoint")
				state.Set("status", "running")
				state.Set("step", "running")
			}
		}
		return nil
	})
	graphBuilder.AddNode(processCheckpointInput)

	// Conditional node to decide based on checkpoint status
	decisionNode := graph.NewConditionalNode(graph.ConditionalConfig{
		ID:        "checkpoint_decision",
		Condition: func(s graph.State) bool { 
			if status, exists := s.Get("status"); exists {
				return status != "checkpointed" // Continue if not checkpointed
			}
			return true // Default to continue
		},
		TrueNode: graph.NewFunctionNode("continue_execution", func(ctx context.Context, state graph.State) error {
			log.Println("Continuing execution after checkpoint decision")
			state.Set("next_action", "continue")
			return nil
		}),
		FalseNode: graph.NewFunctionNode("handle_checkpoint", func(ctx context.Context, state graph.State) error {
			log.Println("Handling checkpoint case - in real system, this would pause and wait for resume")
			state.Set("next_action", "checkpoint_pause")
			
			// Simulate that we're waiting for resume
			// In a real system, this would trigger the checkpoint and pause
			state.Set("is_resume_checkpoint", true)
			return nil
		}),
	})
	graphBuilder.AddNode(decisionNode)

	// Another input node to simulate resuming from checkpoint
	resumeInputNode := graph.NewStdinNode(graph.StdinNodeConfig{
		ID:       "resume_input",
		Prompt:   "Resume from checkpoint - enter resume command (type 'resume' to continue): ",
		StateKey: "resume_input",
		TrimSpace: true,
	})
	graphBuilder.AddNode(resumeInputNode)

	// Process resume input
	processResumeNode := graph.NewFunctionNode("process_resume_input", func(ctx context.Context, state graph.State) error {
		if input, exists := state.Get("resume_input"); exists {
			inputStr := fmt.Sprintf("%v", input)
			log.Printf("Processing resume input: %s", inputStr)
			
			if strings.ToLower(inputStr) == "resume" {
				log.Println("Resuming from checkpoint")
				state.Set("status", "resumed")
				state.Set("step", "resumed")
			} else {
				log.Println("Invalid resume command")
				state.Set("status", "error")
			}
		}
		return nil
	})
	graphBuilder.AddNode(processResumeNode)

	// Final display
	finalDisplayNode := graph.NewStdoutNode(graph.StdoutNodeConfig{
		ID:       "final_display",
		Template: "\n--- Final Results ---\nStatus: {{.status}}\nStep: {{.step}}\nNext action: {{.next_action}}\nWorkflow completed.\n",
	})
	graphBuilder.AddNode(finalDisplayNode)

	// Set up graph structure
	graphBuilder.AddEdge(graph.NewDirectEdge("start", "process"))
	graphBuilder.AddEdge(graph.NewDirectEdge("process", "display_state"))
	graphBuilder.AddEdge(graph.NewDirectEdge("display_state", "checkpoint_input"))
	graphBuilder.AddEdge(graph.NewDirectEdge("checkpoint_input", "process_checkpoint_input"))
	graphBuilder.AddEdge(graph.NewDirectEdge("process_checkpoint_input", "checkpoint_decision"))
	graphBuilder.AddEdge(graph.NewDirectEdge("checkpoint_decision", "resume_input")) // Always go to resume input for demo
	graphBuilder.AddEdge(graph.NewDirectEdge("resume_input", "process_resume_input"))
	graphBuilder.AddEdge(graph.NewDirectEdge("process_resume_input", "final_display"))
	graphBuilder.SetEntry("start")
	graphBuilder.AddFinish("final_display")

	// Validate the graph
	if err := graphBuilder.Validate(); err != nil {
		log.Fatalf("Invalid graph: %v", err)
	}

	log.Printf("Graph created with %d nodes", graphBuilder.NodeCount())

	// Create initial state
	initialState := graph.NewMemoryState()
	initialState.Set("graph_id", "stdio-checkpoint-demo-1")
	initialState.Set("user_id", "demo-user")

	// Execute the graph
	log.Println("Executing advanced stdio HITL graph...")
	if err := executeGraphAdvanced(context.Background(), graphBuilder, initialState); err != nil {
		log.Printf("Error executing graph: %v", err)
	}

	log.Println("Advanced demo completed")
}

// executeGraphAdvanced executes the graph starting from the entry node
func executeGraphAdvanced(ctx context.Context, g *graph.Graph, initialState graph.State) error {
	// Get the entry node
	entryID := g.Entry()
	if entryID == "" {
		return fmt.Errorf("graph has no entry node")
	}

	// Execute starting from the entry node
	return traverseGraphAdvanced(ctx, g, entryID, initialState)
}

// traverseGraphAdvanced recursively executes nodes in the graph
func traverseGraphAdvanced(ctx context.Context, g *graph.Graph, nodeID string, state graph.State) error {
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
				if err := traverseGraphAdvanced(ctx, g, targetID, state); err != nil {
					return err
				}
			}
		}
	}

	return nil
}