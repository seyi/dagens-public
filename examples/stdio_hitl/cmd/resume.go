package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/seyi/dagens/pkg/graph"
	"github.com/seyi/dagens/pkg/hitl"
)

func main() {
	var requestID string
	var response string
	var signature string
	var secret string

	flag.StringVar(&requestID, "request-id", "", "The request ID to resume")
	flag.StringVar(&response, "response", "", "The response to submit")
	flag.StringVar(&signature, "signature", "", "The HMAC signature for security")
	flag.StringVar(&secret, "secret", "", "The callback secret (if signature not provided)")
	flag.Parse()

	if requestID == "" || response == "" {
		log.Fatal("Both request-id and response are required")
	}

	// If no signature is provided but a secret is, calculate the signature
	if signature == "" && secret != "" {
		signature = calculateHMAC(fmt.Sprintf("%s:%s", requestID, response), []byte(secret))
		log.Printf("Calculated signature: %s", signature)
	}

	if signature == "" {
		log.Fatal("Either signature or secret must be provided for security")
	}

	// In a real implementation, you would get these from configuration
	// For this demo, we'll use in-memory implementations
	checkpointStore := hitl.NewRedisCheckpointStore()
	idempotencyStore := hitl.NewRedisIdempotencyStore(nil) // Using in-memory for demo
	graphRegistry := hitl.NewSimpleGraphRegistry()
	graphRegistry.RegisterGraph(hitl.GraphDefinition{
		ID:      "stdio_graph", // This should match the graph ID used when creating checkpoint
		Version: "1.0.0",
	})

	// Resume the workflow with the user's response and signature
	log.Println("Resuming workflow with user response...")
	if err := ResumeWorkflowFromStdinInput(requestID, response, signature, checkpointStore, idempotencyStore, graphRegistry, []byte(secret)); err != nil {
		log.Printf("Error resuming workflow: %v", err)
		return
	}

	log.Printf("Successfully resumed workflow with request ID: %s", requestID)
}

// ResumeWorkflowFromStdinInput simulates resuming a workflow with a user's input response
// The signature now includes a secret for security validation
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