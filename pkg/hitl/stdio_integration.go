// Package hitl provides integration between the graph package and HITL checkpoint system
package hitl

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/seyi/dagens/pkg/graph"
)

// StdioCheckpointNode integrates with the graph package to provide true durable checkpointing
// that survives process restart for stdin/stdout interactions.
type StdioCheckpointNode struct {
	*graph.BaseNode
	prompt             string
	stateKey           string
	continueKey        string // State key that determines if execution should continue
	timeout            time.Duration // Duration before timeout
	shortWaitThreshold time.Duration // Threshold to decide between short wait and checkpointing
	checkpointStore    CheckpointStore
	responseManager    *HumanResponseManager
	callbackSecret     []byte
	graphID            string
	graphVersion       string
}

// StdioCheckpointNodeConfig holds configuration for creating a StdioCheckpointNode
type StdioCheckpointNodeConfig struct {
	ID                 string
	Prompt             string
	StateKey           string
	ContinueKey        string
	Timeout            time.Duration
	ShortWaitThreshold time.Duration
	CheckpointStore    CheckpointStore
	ResponseManager    *HumanResponseManager
	CallbackSecret     []byte
	GraphID            string
	GraphVersion       string
}

// NewStdioCheckpointNode creates a new StdioCheckpointNode with durable checkpointing
func NewStdioCheckpointNode(config StdioCheckpointNodeConfig) *StdioCheckpointNode {
	if config.StateKey == "" {
		config.StateKey = "checkpoint_input"
	}
	if config.ContinueKey == "" {
		config.ContinueKey = "checkpoint_continue"
	}
	if config.ShortWaitThreshold == 0 {
		config.ShortWaitThreshold = 5 * time.Second // Default 5 seconds
	}
	
	node := &StdioCheckpointNode{
		BaseNode:           graph.NewBaseNode(config.ID, "stdio_checkpoint"),
		prompt:             config.Prompt,
		stateKey:           config.StateKey,
		continueKey:        config.ContinueKey,
		timeout:            config.Timeout,
		shortWaitThreshold: config.ShortWaitThreshold,
		checkpointStore:    config.CheckpointStore,
		responseManager:    config.ResponseManager,
		callbackSecret:     config.CallbackSecret,
		graphID:            config.GraphID,
		graphVersion:       config.GraphVersion,
	}
	
	return node
}

// Execute handles checkpointing with stdin/stdout interaction using durable checkpointing
func (n *StdioCheckpointNode) Execute(ctx context.Context, state graph.State) error {
	// Check if we're resuming from a previous checkpoint
	requestID, exists := state.Get(StateKeyHumanRequestID)
	if exists && requestID != nil {
		// This is a resumed execution - process the response that was injected into state
		return n.handleResume(ctx, state)
	}

	// DECISION POINT: Short wait or durable checkpoint?
	if n.timeout > 0 && n.timeout <= n.shortWaitThreshold {
		// For short waits, use direct input with signal handling
		return n.directInput(ctx, state)
	}

	// For longer waits, create a durable checkpoint that survives process restart
	return n.createDurableCheckpoint(ctx, state)
}

// directInput handles direct input for short waits without durable checkpointing
func (n *StdioCheckpointNode) directInput(ctx context.Context, state graph.State) error {
	// Display prompt
	if n.prompt != "" {
		fmt.Print(n.prompt)
	}

	// Create a context with timeout
	readCtx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()

	// Use channels for communication
	resultChan := make(chan string, 1)
	errChan := make(chan error, 1)
	
	// Set up signal handling for clean interruption
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signalChan)

	// Start the scanner in a separate goroutine with proper cleanup to prevent goroutine leak
	done := make(chan struct{})
	go func() {
		defer close(done) // Signal completion to prevent goroutine leak
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			// Use select to avoid blocking if the receiving goroutine has exited
			select {
			case resultChan <- scanner.Text():
			case <-done:
			case <-readCtx.Done():
			case <-ctx.Done():
			}
		} else {
			var err error
			if err = scanner.Err(); err != nil {
				select {
				case errChan <- fmt.Errorf("error reading from stdin: %w", err):
				case <-done:
				case <-readCtx.Done():
				case <-ctx.Done():
				}
			} else {
				select {
				case resultChan <- "":
				case <-done:
				case <-readCtx.Done():
				case <-ctx.Done():
				}
			}
		}
	}()

	// Wait for input, timeout, signal, or context cancellation
	select {
	case input := <-resultChan:
		processedInput := strings.TrimSpace(input)

		// Store input in state
		state.Set(n.stateKey, processedInput)

		// Determine if execution should continue based on input
		shouldContinue := strings.ToLower(processedInput) == "continue" ||
		                  strings.ToLower(processedInput) == "yes" ||
		                  strings.ToLower(processedInput) == "y"

		state.Set(n.continueKey, shouldContinue)

		return nil
	case err := <-errChan:
		return err
	case <-signalChan:
		cancel() // Cancel the timeout context
		return fmt.Errorf("stdin read interrupted by signal")
	case <-readCtx.Done():
		return fmt.Errorf("timeout waiting for stdin input: %w", readCtx.Err())
	case <-ctx.Done():
		return ctx.Err()
	}
}

// createDurableCheckpoint creates a checkpoint that survives process restart
func (n *StdioCheckpointNode) createDurableCheckpoint(ctx context.Context, state graph.State) error {
	requestID := fmt.Sprintf("stdio_%s_%d", n.ID(), time.Now().UnixNano())

	// Serialize the current state for checkpointing
	// Check if the state implements the AdvancedState interface which has Marshal/Unmarshal
	advancedState, ok := state.(interface{ Marshal() ([]byte, error) })
	if !ok {
		// If it doesn't support marshaling directly, try to check if it's a MemoryState
		memState, ok := state.(*graph.MemoryState)
		if !ok {
			return fmt.Errorf("state type %T does not support marshaling - implement AdvancedState interface", state)
		}
		// Use the MemoryState's marshal method as a fallback
		advancedState = memState
	}

	stateData, err := advancedState.Marshal()
	if err != nil {
		return fmt.Errorf("failed to serialize state for checkpoint: %w", err)
	}

	// Create checkpoint with all metadata
	checkpoint := &ExecutionCheckpoint{
		GraphID:      n.graphID,
		GraphVersion: n.graphVersion,
		NodeID:       n.ID(),
		StateData:    stateData,
		RequestID:    requestID,
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(n.timeout + 24*time.Hour), // Grace period
	}

	// Store checkpoint durably
	if err := n.checkpointStore.Create(checkpoint); err != nil {
		return fmt.Errorf("failed to create durable checkpoint: %w", err)
	}

	// Register the request with the ResponseManager to properly integrate with HITL system
	if n.responseManager != nil {
		// Create a human request to track the interaction
		humanReq := &HumanRequest{
			RequestID:   requestID,
			AgentName:   n.ID(),
			Instruction: n.prompt,
			Options:     []string{}, // No predefined options for freeform stdin
			Timeout:     n.timeout,
			Timestamp:   time.Now(),
			Context:     map[string]interface{}{"node_type": "stdio_checkpoint"},
		}

		// Notify human - in a real implementation this might send an email, push notification, etc.
		if err := n.responseManager.NotifyHuman(humanReq); err != nil {
			log.Printf("Warning: failed to notify human for request %s: %v", requestID, err)
			// Don't fail the checkpoint creation if notification fails
		}
	}

	// Store request ID in state for checkpoint creation mechanism
	// This follows the same pattern as the HumanNode
	state.Set(StateKeyHumanRequestID, requestID)
	state.Set(StateKeyHumanTimeout, n.timeout.String())

	// Instead of trying to read from stdin (which won't work after restart),
	// output instructions for the user to continue the workflow out-of-band
	fmt.Printf("\n--- WORKFLOW PAUSED ---\n")
	fmt.Printf("Workflow paused for human input. To resume, run:\n")
	fmt.Printf("dagens-cli resume --request-id \"%s\" --response \"<your_response>\"\n", requestID)
	fmt.Printf("Or visit: http://localhost:8080/workflow/%s\n", requestID)
	fmt.Printf("-----------------------\n\n")

	// Return special error to trigger checkpoint - this follows the same pattern as HumanNode
	return ErrHumanInteractionPending
}

// handleResume processes a resumed execution after checkpoint
func (n *StdioCheckpointNode) handleResume(ctx context.Context, state graph.State) error {
	// The state should already be restored from the checkpoint at this point
	// Look for the pending response that was injected during resume
	
	// Check if the required keys exist in the state
	if _, exists := state.Get(n.stateKey); !exists {
		// If the input key doesn't exist, this might be the first resume
		// Look for any pending human response that was injected during resume
		pendingKey := fmt.Sprintf(StateKeyHumanPendingFmt, n.ID())
		if respData, exists := state.Get(pendingKey); exists {
			// Process the response that was injected during resume
			if respBytes, ok := respData.([]byte); ok {
				// For stdio checkpoint, we expect the response to contain the input
				// This would be set by the resume mechanism
				state.Set(n.stateKey, string(respBytes))
				
				// Determine continuation based on the input
				inputStr := string(respBytes)
				shouldContinue := strings.ToLower(inputStr) == "continue" ||
				                  strings.ToLower(inputStr) == "yes" ||
				                  strings.ToLower(inputStr) == "y"
				state.Set(n.continueKey, shouldContinue)
			}
		}
	}
	
	// Clean up the checkpoint request ID from state
	state.Delete(StateKeyHumanRequestID)
	
	// Clean up pending key if it exists
	pendingKey := fmt.Sprintf(StateKeyHumanPendingFmt, n.ID())
	state.Delete(pendingKey)
	
	return nil
}