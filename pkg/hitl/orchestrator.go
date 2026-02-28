package hitl

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/graph"
)

// ExecuteGraph orchestrates the execution of a graph, handling human interaction checkpoints
func ExecuteGraph(graphID, graphVersion string, initialState graph.State, executor ResumableExecutor, checkpointStore CheckpointStore, responseMgr *HumanResponseManager) (finalState graph.State, err error) {
	for {
		state, err := executor.ExecuteCurrent(initialState)
		if err == nil {
			return state, nil // Graph completed normally
		}

		if errors.Is(err, ErrHumanInteractionPending) {
			// Extract request info from state
			reqIDVal, exists := initialState.Get(StateKeyHumanRequestID)
			if !exists {
				return nil, fmt.Errorf("missing request ID in state")
			}
			reqID, ok := reqIDVal.(string)
			if !ok {
				return nil, fmt.Errorf("invalid request ID type in state")
			}

			timeoutVal, _ := initialState.Get(StateKeyHumanTimeout)
			timeoutStr, _ := timeoutVal.(string)
			timeout, _ := time.ParseDuration(timeoutStr)

			// Serialize the state - SAFETY: Check for correct type before assertion
			var stateData []byte
			if memState, ok := initialState.(*graph.MemoryState); ok {
				stateData, err = memState.Marshal()
				if err != nil {
					return nil, fmt.Errorf("serialize state: %w", err)
				}
			} else {
				// Attempt to serialize through a generic interface if available
				// If the graph.State interface doesn't have a marshal method,
				// we need to implement one or use reflection/json marshaling
				return nil, fmt.Errorf("state type %T does not support marshaling", initialState)
			}

			// Create checkpoint with all metadata
			cp := &ExecutionCheckpoint{
				GraphID:      graphID,
				GraphVersion: graphVersion, // CRITICAL: Store version
				NodeID:       executor.CurrentNodeID(),
				StateData:    stateData,
				RequestID:    reqID,
				CreatedAt:    time.Now(),
				ExpiresAt:    time.Now().Add(timeout + 24*time.Hour), // Grace period
				FailureCount: 0,                                      // Initialize for failure tracking
				LastError:    "",
				LastAttempt:  time.Time{},
			}

			// CRITICAL: Atomic checkpoint creation with transaction
			// Since the interface doesn't expose BeginTransaction, we need to handle this differently
			// For now, we'll use the Create method which should be atomic in the implementation
			// In a complete implementation, we would need to either:
			// 1. Add BeginTransaction to the CheckpointStore interface, or
			// 2. Pass the concrete implementation type that supports transactions
			// For this fix, we'll assume the Create method is atomic in the implementation
			if err := checkpointStore.Create(cp); err != nil {
				return nil, fmt.Errorf("create checkpoint: %w", err)
			}

			// Clean up state keys
			initialState.Delete(StateKeyHumanRequestID)
			initialState.Delete(StateKeyHumanTimeout)

			return nil, ErrWorkflowPending
		}

		// Any other error is a true failure
		return nil, fmt.Errorf("graph execution failed: %w", err)
	}
}

// ResumeGraphFromCheckpoint resumes execution from a checkpoint
func ResumeGraphFromCheckpoint(requestID string, response *HumanResponse, checkpointStore CheckpointStore, idempotencyStore IdempotencyStore, graphRegistry GraphRegistry, executorFactory func(graphID, graphVersion string) ResumableExecutor) error {
	// Load checkpoint
	cp, err := checkpointStore.GetByRequestID(requestID)
	if err != nil {
		if errors.Is(err, ErrCheckpointNotFound) {
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
			ErrGraphVersionMismatch, cp.GraphVersion, currentGraph.Version)
	}

	// Deserialize state with proper error handling
	var state *graph.MemoryState
	if cp.StateData != nil {
		state = &graph.MemoryState{}
		if err := state.Unmarshal(cp.StateData); err != nil {
			return fmt.Errorf("deserialize state: %w", err)
		}
	} else {
		// Create a new empty state if no data exists
		state = graph.NewMemoryState()
	}

	// Inject human response into state
	respData, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	pendingKey := fmt.Sprintf(StateKeyHumanPendingFmt, cp.NodeID)
	state.Set(pendingKey, respData)

	// Mark as processed BEFORE execution (prevent duplicate execution)
	idempotencyKey := fmt.Sprintf("callback-done:%s", requestID)

	// Check if already processed to ensure idempotency
	exists, err := idempotencyStore.Exists(idempotencyKey)
	if err != nil {
		return fmt.Errorf("check idempotency: %w", err)
	}
	if exists {
		// Already processed, return success to maintain idempotency
		return nil
	}

	// Mark as processed
	if err := idempotencyStore.Set(idempotencyKey, 24*time.Hour); err != nil {
		return fmt.Errorf("mark as processed: %w", err)
	}

	// Resume execution from stored node
	executor := executorFactory(cp.GraphID, cp.GraphVersion)
	if err := executor.ResumeFromNode(cp.NodeID, state); err != nil {
		// If execution fails, remove the idempotency marker so it can be retried
		if deleteErr := idempotencyStore.Delete(idempotencyKey); deleteErr != nil {
			// Log the error but don't mask the original error
			fmt.Printf("WARN: failed to delete idempotency key after execution error: %v", deleteErr)
		}
		return fmt.Errorf("resume execution: %w", err)
	}

	// Clean up checkpoint (success path only)
	if err := checkpointStore.Delete(requestID); err != nil {
		// Log but don't fail - idempotency will prevent duplicate execution
		// In a real implementation, you'd want proper logging here
		fmt.Printf("WARN: failed to delete checkpoint %s: %v", requestID, err)
		// Attempt to record the failure in the checkpoint store for monitoring
		if _, recordErr := checkpointStore.RecordFailure(requestID, err); recordErr != nil {
			fmt.Printf("WARN: failed to record checkpoint deletion failure: %v", recordErr)
		}
	}

	return nil
}

var ErrWorkflowPending = errors.New("workflow pending human interaction")
