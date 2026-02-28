package backend

import (
	"context"
	"time"

	"github.com/seyi/dagens/pkg/graph"
)

// Backend defines the interface for executing graphs.
// Different backends can provide local, distributed, or cloud execution.
type Backend interface {
	// Execute runs the graph with the given state and returns the result.
	Execute(ctx context.Context, g *graph.Graph, state graph.State) (*ExecutionResult, error)

	// ExecuteAsync runs the graph asynchronously and returns an execution ID.
	ExecuteAsync(ctx context.Context, g *graph.Graph, state graph.State) (string, error)

	// GetResult retrieves the result of an async execution.
	GetResult(executionID string) (*ExecutionResult, error)

	// Name returns the backend name.
	Name() string

	// Close closes the backend and releases resources.
	Close() error
}

// ExecutionResult contains the result of a graph execution.
type ExecutionResult struct {
	// ExecutionID is the unique identifier for this execution.
	ExecutionID string

	// GraphID is the ID of the executed graph.
	GraphID string

	// State is the final state after execution.
	State graph.State

	// Status indicates the execution status.
	Status ExecutionStatus

	// Error contains any error that occurred during execution.
	Error error

	// StartTime is when execution started.
	StartTime time.Time

	// EndTime is when execution completed.
	EndTime time.Time

	// Duration is the total execution time.
	Duration time.Duration

	// NodesExecuted is the number of nodes that were executed.
	NodesExecuted int

	// ExecutionPath is the sequence of node IDs that were executed.
	ExecutionPath []string

	// Metadata contains additional execution metadata.
	Metadata map[string]interface{}
}

// ExecutionStatus represents the status of a graph execution.
type ExecutionStatus string

const (
	// StatusPending indicates the execution has been queued but not started.
	StatusPending ExecutionStatus = "pending"

	// StatusRunning indicates the execution is currently in progress.
	StatusRunning ExecutionStatus = "running"

	// StatusCompleted indicates the execution completed successfully.
	StatusCompleted ExecutionStatus = "completed"

	// StatusFailed indicates the execution failed with an error.
	StatusFailed ExecutionStatus = "failed"

	// StatusCanceled indicates the execution was canceled.
	StatusCanceled ExecutionStatus = "canceled"
)

// NewExecutionResult creates a new execution result.
func NewExecutionResult(executionID, graphID string) *ExecutionResult {
	return &ExecutionResult{
		ExecutionID:   executionID,
		GraphID:       graphID,
		Status:        StatusPending,
		StartTime:     time.Now(),
		NodesExecuted: 0,
		ExecutionPath: make([]string, 0),
		Metadata:      make(map[string]interface{}),
	}
}

// Complete marks the execution as completed.
func (r *ExecutionResult) Complete() {
	r.Status = StatusCompleted
	r.EndTime = time.Now()
	r.Duration = r.EndTime.Sub(r.StartTime)
}

// Fail marks the execution as failed with the given error.
func (r *ExecutionResult) Fail(err error) {
	r.Status = StatusFailed
	r.Error = err
	r.EndTime = time.Now()
	r.Duration = r.EndTime.Sub(r.StartTime)
}

// Cancel marks the execution as canceled.
func (r *ExecutionResult) Cancel() {
	r.Status = StatusCanceled
	r.EndTime = time.Now()
	r.Duration = r.EndTime.Sub(r.StartTime)
}

// AddNode records that a node was executed.
func (r *ExecutionResult) AddNode(nodeID string) {
	r.NodesExecuted++
	r.ExecutionPath = append(r.ExecutionPath, nodeID)
}

// SetMetadata sets a metadata value.
func (r *ExecutionResult) SetMetadata(key string, value interface{}) {
	r.Metadata[key] = value
}

// GetMetadata retrieves a metadata value.
func (r *ExecutionResult) GetMetadata(key string) (interface{}, bool) {
	val, exists := r.Metadata[key]
	return val, exists
}
