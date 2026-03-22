package hitl

import (
	"context"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/graph"
)

const checkpointExpiryGrace = 24 * time.Hour
const minCheckpointLifetime = 1 * time.Hour
const maxCheckpointLifetime = 7 * 24 * time.Hour

func resumeFromCheckpoint(
	ctx context.Context,
	cp *ExecutionCheckpoint,
	state graph.State,
	graphRegistry GraphRegistry,
	executorFactory func(graphID, graphVersion string) ResumableExecutor,
) (*graph.ExecutionResult, error) {
	if cp == nil {
		return nil, fmt.Errorf("resume checkpoint is nil")
	}
	if err := ensureContextActive(ctx, "resume checkpoint"); err != nil {
		return nil, err
	}

	if executableRegistry, ok := graphRegistry.(ExecutableGraphRegistry); ok {
		g, err := executableRegistry.GetExecutableGraph(cp.GraphID)
		if err != nil {
			return nil, fmt.Errorf("get executable graph: %w", err)
		}
		return g.ResumeFromPaused(ctx, state, &graph.PausedResult{
			RequestID:    cp.RequestID,
			NodeID:       cp.NodeID,
			GraphVersion: cp.GraphVersion,
		})
	}

	if executorFactory == nil {
		return nil, fmt.Errorf("executor factory is nil and graph registry does not provide executable graphs")
	}

	executor := executorFactory(cp.GraphID, cp.GraphVersion)
	if executor == nil {
		return nil, fmt.Errorf("executor factory returned nil for graph_id=%s graph_version=%s", cp.GraphID, cp.GraphVersion)
	}
	if err := executor.ResumeFromNode(cp.NodeID, state); err != nil {
		return nil, err
	}
	// Legacy executor path has no first-class execution result contract.
	// A successful resume is treated as a non-paused completion signal here.
	return &graph.ExecutionResult{}, nil
}

func checkpointExpiryForState(now time.Time, state graph.State) time.Time {
	timeout := time.Duration(0)
	if timeoutVal, exists := state.Get(StateKeyHumanTimeout); exists {
		if timeoutStr, ok := timeoutVal.(string); ok {
			if parsed, err := time.ParseDuration(timeoutStr); err == nil {
				timeout = parsed
			}
		}
	}
	lifetime := timeout + checkpointExpiryGrace
	if lifetime < minCheckpointLifetime {
		lifetime = minCheckpointLifetime
	}
	if lifetime > maxCheckpointLifetime {
		lifetime = maxCheckpointLifetime
	}
	return now.Add(lifetime)
}

func marshalGraphState(state graph.State) ([]byte, error) {
	marshalableState, ok := state.(interface{ Marshal() ([]byte, error) })
	if !ok {
		return nil, fmt.Errorf("state type %T does not support Marshal", state)
	}
	return marshalableState.Marshal()
}

func ensureContextActive(ctx context.Context, operation string) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s interrupted: %w", operation, err)
	}
	return nil
}

func persistNestedPauseCheckpoint(
	ctx context.Context,
	checkpointStore CheckpointStore,
	cp *ExecutionCheckpoint,
	state graph.State,
	paused *graph.PausedResult,
) (*ExecutionCheckpoint, error) {
	if paused == nil {
		return nil, fmt.Errorf("paused result is nil")
	}
	if paused.RequestID == "" {
		return nil, fmt.Errorf("paused result missing request_id")
	}
	if paused.NodeID == "" {
		return nil, fmt.Errorf("paused result missing node_id")
	}
	if paused.RequestID == cp.RequestID {
		return nil, fmt.Errorf("nested pause produced same request_id as checkpoint: %s", paused.RequestID)
	}
	if err := ensureContextActive(ctx, "persist nested pause checkpoint"); err != nil {
		return nil, err
	}

	stateData, err := marshalGraphState(state)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	next := &ExecutionCheckpoint{
		GraphID:      cp.GraphID,
		GraphVersion: cp.GraphVersion,
		NodeID:       paused.NodeID,
		StateData:    stateData,
		RequestID:    paused.RequestID,
		CreatedAt:    now,
		ExpiresAt:    checkpointExpiryForState(now, state),
		FailureCount: 0,
		LastError:    "",
		LastAttempt:  time.Time{},
	}
	logger := hitlLogger()
	if replacer, ok := checkpointStore.(CheckpointReplaceStore); ok {
		if err := replacer.Replace(cp.RequestID, next); err != nil {
			return nil, fmt.Errorf("replace nested pause checkpoint: %w", err)
		}
		logger.Info("nested pause checkpoint replaced atomically", safeLogFields(map[string]interface{}{
			"operation":      "checkpoint.replace_nested_pause",
			"old_request_id": cp.RequestID,
			"new_request_id": next.RequestID,
			"graph_id":       next.GraphID,
			"node_id":        next.NodeID,
			"expires_at":     next.ExpiresAt.UTC().Format(time.RFC3339),
		}))
		return next, nil
	}
	if err := ensureContextActive(ctx, "create nested pause checkpoint"); err != nil {
		return nil, err
	}
	if err := checkpointStore.Create(next); err != nil {
		return nil, fmt.Errorf("create nested pause checkpoint (non-atomic fallback): %w", err)
	}
	if err := ensureContextActive(ctx, "delete original checkpoint after nested pause"); err != nil {
		return nil, err
	}
	if err := checkpointStore.Delete(cp.RequestID); err != nil {
		logger.Warn("nested pause checkpoint fallback delete failed", safeLogFields(map[string]interface{}{
			"operation":      "checkpoint.replace_nested_pause_fallback",
			"old_request_id": cp.RequestID,
			"new_request_id": next.RequestID,
			"graph_id":       next.GraphID,
			"node_id":        next.NodeID,
			"error":          err.Error(),
		}))
		return next, nil
	}
	logger.Info("nested pause checkpoint replaced (non-atomic fallback)", safeLogFields(map[string]interface{}{
		"operation":      "checkpoint.replace_nested_pause_fallback",
		"old_request_id": cp.RequestID,
		"new_request_id": next.RequestID,
		"graph_id":       next.GraphID,
		"node_id":        next.NodeID,
		"expires_at":     next.ExpiresAt.UTC().Format(time.RFC3339),
	}))
	return next, nil
}
