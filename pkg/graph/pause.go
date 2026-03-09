package graph

import (
	"fmt"

	"github.com/seyi/dagens/pkg/workflow"
)

// PauseSignal is the typed pause contract for node execution.
// Nodes that need to yield execution (e.g. HITL boundaries) should return an
// error implementing this interface.
type PauseSignal interface {
	error
	PauseResult() *PausedResult
}

type pauseSignalError struct {
	paused PausedResult
	cause  error
}

func (e *pauseSignalError) Error() string {
	if e.cause != nil {
		return e.cause.Error()
	}
	return "graph execution paused"
}

func (e *pauseSignalError) Unwrap() error {
	return e.cause
}

func (e *pauseSignalError) PauseResult() *PausedResult {
	cp := e.paused
	return &cp
}

// NewPauseSignal creates a typed pause signal error for graph execution.
func NewPauseSignal(paused PausedResult, cause error) error {
	return &pauseSignalError{
		paused: paused,
		cause:  cause,
	}
}

func requestIDFromState(state State) string {
	if raw, ok := state.Get(PauseRequestIDStateKey); ok {
		if s, ok := raw.(string); ok {
			return s
		}
	}
	return ""
}

func isLegacyPauseSignal(err error) bool {
	return workflow.IsHumanPauseError(err)
}

func normalizePausedResult(paused *PausedResult, nodeID, graphVersion, requestID string) *PausedResult {
	if paused == nil {
		paused = &PausedResult{}
	}
	if paused.RequestID == "" {
		paused.RequestID = requestID
	}
	if paused.NodeID == "" {
		paused.NodeID = nodeID
	}
	if paused.GraphVersion == "" {
		paused.GraphVersion = graphVersion
	}
	return paused
}

func (r *PausedResult) String() string {
	return fmt.Sprintf(
		"request_id=%s node_id=%s graph_version=%s trace_id=%s trace_parent=%s",
		r.RequestID,
		r.NodeID,
		r.GraphVersion,
		r.TraceID,
		r.TraceParent,
	)
}
