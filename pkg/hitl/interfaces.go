package hitl

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5" // Added this import
	"github.com/seyi/dagens/pkg/graph"
)

// CheckpointStore is the interface for persistent storage of execution checkpoints.
type CheckpointStore interface {
	// Create checkpoint atomically with request registration
	CreateWithTransaction(tx Transaction, cp *ExecutionCheckpoint) error

	Create(cp *ExecutionCheckpoint) error
	GetByRequestID(requestID string) (*ExecutionCheckpoint, error)
	Delete(requestID string) error

	// List orphaned checkpoints for cleanup
	ListOrphaned(olderThan time.Duration) ([]*ExecutionCheckpoint, error)

	// RecordFailure increments the failure count and updates the last error message
	// for a given checkpoint. This is called for transient errors.
	RecordFailure(requestID string, err error) (*ExecutionCheckpoint, error)

	// MoveToCheckpointDLQ atomically moves a checkpoint from the active table
	// to a dead-letter table for manual inspection. This is for permanent failures
	// or after exceeding the retry threshold.
	MoveToCheckpointDLQ(requestID string, finalError string) error
}

// CheckpointReplaceStore is an optional CheckpointStore extension for atomic
// replacement of checkpoints during nested pause transitions.
type CheckpointReplaceStore interface {
	Replace(oldRequestID string, next *ExecutionCheckpoint) error
}

// Transaction represents a database transaction.
type Transaction interface {
	Commit() error
	Rollback() error
	QueryRow(ctx context.Context, query string, args ...interface{}) pgx.Row
}

// IdempotencyStore prevents duplicate callback execution and supports processing locks.
type IdempotencyStore interface {
	Exists(key string) (bool, error)
	Set(key string, ttl time.Duration) error

	// SetNX atomically sets a key if it does not already exist.
	// Returns true if the key was set, false if it already existed.
	// This is used for creating short-lived "processing" locks.
	SetNX(key string, ttl time.Duration) (bool, error)

	// Delete removes a key, used to release a processing lock if enqueuing fails.
	Delete(key string) error
}

// ResumptionQueue is the interface for a durable queue that holds resumption jobs.
type ResumptionQueue interface {
	// Enqueue adds a job to the queue.
	Enqueue(ctx context.Context, job *ResumptionJob) error

	// Dequeue retrieves a job from the queue, blocking until one is available
	// or the context is canceled.
	Dequeue(ctx context.Context) (*ResumptionJob, error)

	// Ack acknowledges successful processing of the job so it is not redelivered.
	// Implementations that do not require ack (in-memory) may no-op.
	Ack(ctx context.Context, jobID string) error
}

// GraphRegistry provides access to graph definitions.
type GraphRegistry interface {
	GetGraph(graphID string) (GraphDefinition, error)
}

// ExecutableGraphRegistry is an optional GraphRegistry extension that provides
// access to executable graph instances for graph-native pause/resume semantics.
type ExecutableGraphRegistry interface {
	GetExecutableGraph(graphID string) (*graph.Graph, error)
}

// GraphDefinition represents a graph definition with its version.
type GraphDefinition struct {
	ID      string
	Version string
}

// ResumableExecutor executes graphs from arbitrary nodes after checkpoints.
type ResumableExecutor interface {
	ExecuteCurrent(state graph.State) (graph.State, error)
	ResumeFromNode(nodeID string, state graph.State) error
	CurrentNodeID() string
}
