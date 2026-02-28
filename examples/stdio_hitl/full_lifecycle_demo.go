package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/seyi/dagens/pkg/graph"
	"github.com/seyi/dagens/pkg/hitl"
)

// This demo shows the complete lifecycle: checkpoint creation, process simulation, and resume
func runFullLifecycleDemo() {
	log.Println("Starting Full Lifecycle Stdio HITL Demo")

	// Create checkpoint store (using in-memory for demo)
	checkpointStore := hitl.NewRedisCheckpointStore() // Using in-memory implementation
	
	// Create response manager
	responseManager := hitl.NewHumanResponseManager()

	// Create idempotency store
	idempotencyStore := hitl.NewRedisIdempotencyStore(nil) // Using in-memory for demo

	// Create graph registry
	graphRegistry := hitl.NewSimpleGraphRegistry()
	graphRegistry.RegisterGraph(hitl.GraphDefinition{
		ID:      "full-lifecycle-demo",
		Version: "v1.0.0",
	})

	// Simulate the initial workflow execution that creates a checkpoint
	log.Println("Simulating initial workflow execution that creates a checkpoint...")
	
	// Create the graph
	graphBuilder := graph.NewGraph("full-lifecycle-demo")

	// Add nodes to the graph
	startNode := graph.NewFunctionNode("start", func(ctx context.Context, state graph.State) error {
		log.Println("Starting full lifecycle workflow")
		state.Set("workflow_id", "full-lifecycle-demo-1")
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

	// Durable checkpoint node that will create a checkpoint
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
		GraphID:            "full-lifecycle-demo",
		GraphVersion:       "v1.0.0",
	})
	graphBuilder.AddNode(stdioCheckpointNode)

	// Create initial state
	initialState := graph.NewMemoryState()
	initialState.Set("graph_id", "full-lifecycle-demo-1")
	initialState.Set("user_id", "demo-user")

	// Execute up to the checkpoint node
	log.Println("Executing workflow up to checkpoint node...")
	
	// Execute the start node
	if err := startNode.Execute(context.Background(), initialState); err != nil {
		log.Printf("Error executing start node: %v", err)
		return
	}
	
	// Execute the process node
	if err := processNode.Execute(context.Background(), initialState); err != nil {
		log.Printf("Error executing process node: %v", err)
		return
	}
	
	// Execute the display node
	if err := displayNode.Execute(context.Background(), initialState); err != nil {
		log.Printf("Error executing display node: %v", err)
		return
	}
	
	// Execute the checkpoint node - this should create a checkpoint
	log.Println("Executing checkpoint node (this will create a checkpoint)...")

	// For this demo, we'll track the request ID manually
	requestID := fmt.Sprintf("stdio_%s_%d", stdioCheckpointNode.ID(), time.Now().UnixNano())

	if err := stdioCheckpointNode.Execute(context.Background(), initialState); err != nil {
		if err == hitl.ErrHumanInteractionPending {
			log.Println("✓ Checkpoint created successfully")
			log.Printf("Checkpoint request ID: %s", requestID)
		} else {
			log.Printf("Error executing checkpoint node: %v", err)
			return
		}
	}

	// At this point, the workflow is paused and a checkpoint has been created
	// Now simulate the resume operation with a user response
	log.Println("\n--- Simulating Process Restart and Resume ---")

	// In a real implementation, the request ID would be provided by the user or system
	// For this demo, we'll use the one we generated
	log.Printf("Using checkpoint with request ID: %s", requestID)

	// Simulate user providing response
	userResponse := "continue"
	log.Printf("Simulating user response: %s", userResponse)

	// Create a signature for the resume request (in a real system, this would be generated by the client)
	callbackSecret := []byte("demo-secret-key")
	signature := calculateHMAC(fmt.Sprintf("%s:%s", requestID, userResponse), callbackSecret)

	// Resume the workflow with the user's response and signature
	log.Println("Resuming workflow with user response...")
	if err := ResumeWorkflowFromStdinInput(requestID, userResponse, signature, checkpointStore, idempotencyStore, graphRegistry, callbackSecret); err != nil {
		log.Printf("Error resuming workflow: %v", err)
		return
	}

	log.Println("✓ Full lifecycle demo completed successfully!")
}

// ResumeWorkflowFromStdinInput simulates resuming a workflow with a user's input response
func ResumeWorkflowFromStdinInput(requestID, response, signature string, checkpointStore hitl.CheckpointStore, idempotencyStore hitl.IdempotencyStore, graphRegistry hitl.GraphRegistry, callbackSecret []byte) error {
	// Validate the signature to ensure the request is authorized
	if !validateResumeRequest(requestID, response, signature, callbackSecret) {
		return fmt.Errorf("invalid signature - unauthorized resume attempt")
	}

	// Load checkpoint
	cp, err := checkpointStore.GetByRequestID(requestID)
	if err != nil {
		if err == hitl.ErrCheckpointNotFound {
			return fmt.Errorf("checkpoint not found for request %s", requestID)
		}
		return fmt.Errorf("load checkpoint: %w", err)
	}

	// Validate graph version compatibility
	currentGraph, err := graphRegistry.GetGraph(cp.GraphID)
	if err != nil {
		return fmt.Errorf("get current graph: %w", err)
	}

	if currentGraph.Version != cp.GraphVersion {
		return fmt.Errorf("%w: checkpoint=%s, current=%s",
			hitl.ErrGraphVersionMismatch, cp.GraphVersion, currentGraph.Version)
	}

	// Deserialize state
	state := &graph.MemoryState{}
	if err := state.Unmarshal(cp.StateData); err != nil {
		return fmt.Errorf("deserialize state: %w", err)
	}

	// Create a HumanResponse with the user's input
	humanResponse := &hitl.HumanResponse{
		SelectedOption: response, // Use the response as the selected option
		FreeformText:   response, // Also store as freeform text
		Payload:        map[string]interface{}{"user_input": response, "timestamp": time.Now().Unix()},
		Timestamp:      time.Now(),
	}

	// Inject human response into state
	respData, err := json.Marshal(humanResponse)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	pendingKey := fmt.Sprintf(hitl.StateKeyHumanPendingFmt, cp.NodeID)
	state.Set(pendingKey, respData)

	// Mark as processed BEFORE execution (prevent duplicate execution)
	idempotencyKey := fmt.Sprintf("callback-done:%s", requestID)
	if err := idempotencyStore.Set(idempotencyKey, 24*time.Hour); err != nil {
		return fmt.Errorf("mark as processed: %w", err)
	}

	// Resume execution from stored node using the appropriate executor
	// In a real implementation, this would use a proper ResumableExecutor
	// For this demo, we'll create a simple one
	executor := &SimpleResumableExecutor{
		graphID:      cp.GraphID,
		graphVersion: cp.GraphVersion,
	}

	if err := executor.ResumeFromNode(cp.NodeID, state); err != nil {
		// If execution fails, remove the idempotency marker so it can be retried
		if deleteErr := idempotencyStore.Delete(idempotencyKey); deleteErr != nil {
			// Log the error but don't mask the original error
			log.Printf("WARN: failed to delete idempotency key after execution error: %v", deleteErr)
		}
		return fmt.Errorf("resume execution: %w", err)
	}

	// Clean up checkpoint (success path only)
	if err := checkpointStore.Delete(requestID); err != nil {
		log.Printf("WARN: failed to delete checkpoint %s: %v", requestID, err)
	}

	log.Printf("Successfully resumed workflow with request ID: %s", requestID)
	return nil
}

// validateResumeRequest validates the resume request using HMAC signature
func validateResumeRequest(requestID, response, signature string, secret []byte) bool {
	// Create the payload to sign: requestID + response
	payload := fmt.Sprintf("%s:%s", requestID, response)

	// Calculate expected HMAC
	expectedSignature := calculateHMAC(payload, secret)

	// Compare signatures securely
	return hmac.Equal([]byte(signature), []byte(expectedSignature))
}

// calculateHMAC calculates an HMAC signature for the given payload and secret
func calculateHMAC(payload string, secret []byte) string {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}

// SimpleResumableExecutor is a basic implementation for the demo
type SimpleResumableExecutor struct {
	graphID      string
	graphVersion string
}

func (s *SimpleResumableExecutor) ExecuteCurrent(state graph.State) (graph.State, error) {
	// In a real implementation, this would execute the current node in the graph
	return state, nil
}

func (s *SimpleResumableExecutor) ResumeFromNode(nodeID string, state graph.State) error {
	// In a real implementation, this would continue the graph execution from the given node
	// For this demo, we'll just log that the resume was successful
	log.Printf("Resumed execution from node: %s", nodeID)

	// Check if there's a pending response in the state and process it
	pendingKey := fmt.Sprintf(hitl.StateKeyHumanPendingFmt, nodeID)
	if respData, exists := state.Get(pendingKey); exists {
		log.Printf("Processing pending response: %v", respData)
		// Clean up the pending key
		state.Delete(pendingKey)
	}

	return nil
}

func (s *SimpleResumableExecutor) CurrentNodeID() string {
	return ""
}