package hitl

import (
	"context"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/graph"
)

// RedisCheckpointStore is a Redis-based implementation of CheckpointStore
type RedisCheckpointStore struct {
	// In a real implementation, this would contain a Redis client
	// For now, using a simple map for demonstration
	store map[string]*ExecutionCheckpoint
}

// NewRedisCheckpointStore creates a new Redis-based checkpoint store
func NewRedisCheckpointStore() *RedisCheckpointStore {
	return &RedisCheckpointStore{
		store: make(map[string]*ExecutionCheckpoint),
	}
}

func (r *RedisCheckpointStore) CreateWithTransaction(tx Transaction, cp *ExecutionCheckpoint) error {
	// In a real implementation, this would use Redis MULTI/EXEC
	return r.Create(cp)
}

func (r *RedisCheckpointStore) Create(cp *ExecutionCheckpoint) error {
	r.store[cp.RequestID] = cp
	return nil
}

func (r *RedisCheckpointStore) GetByRequestID(requestID string) (*ExecutionCheckpoint, error) {
	cp, exists := r.store[requestID]
	if !exists {
		return nil, ErrCheckpointNotFound
	}
	return cp, nil
}

func (r *RedisCheckpointStore) Delete(requestID string) error {
	delete(r.store, requestID)
	return nil
}

func (r *RedisCheckpointStore) ListOrphaned(olderThan time.Duration) ([]*ExecutionCheckpoint, error) {
	var orphaned []*ExecutionCheckpoint
	cutoff := time.Now().Add(-olderThan)

	for _, cp := range r.store {
		if cp.CreatedAt.Before(cutoff) {
			orphaned = append(orphaned, cp)
		}
	}

	return orphaned, nil
}

func (r *RedisCheckpointStore) RecordFailure(requestID string, err error) (*ExecutionCheckpoint, error) {
	cp, exists := r.store[requestID]
	if !exists {
		return nil, ErrCheckpointNotFound
	}

	cp.FailureCount++
	cp.LastError = err.Error()
	cp.LastAttempt = time.Now()

	return cp, nil
}

func (r *RedisCheckpointStore) MoveToCheckpointDLQ(requestID string, finalError string) error {
	_, exists := r.store[requestID]
	if !exists {
		return ErrCheckpointNotFound
	}

	// In a real implementation, this would move the checkpoint to a DLQ
	// For now, we'll just delete it
	delete(r.store, requestID)

	// Log the move to DLQ (in real implementation)
	fmt.Printf("Moved checkpoint %s to DLQ: %s\n", requestID, finalError)

	return nil
}

// NOTE: RedisIdempotencyStore moved to redis_store.go for production implementation

// SimpleInMemoryQueue is a simple in-memory implementation of ResumptionQueue
// In production, you'd use Redis, RabbitMQ, or similar
type SimpleInMemoryQueue struct {
	queue chan *ResumptionJob
}

// NewSimpleInMemoryQueue creates a new in-memory queue
func NewSimpleInMemoryQueue(size int) *SimpleInMemoryQueue {
	return &SimpleInMemoryQueue{
		queue: make(chan *ResumptionJob, size),
	}
}

func (s *SimpleInMemoryQueue) Enqueue(ctx context.Context, job *ResumptionJob) error {
	if job.JobID == "" {
		job.JobID = fmt.Sprintf("inmem-%d", time.Now().UnixNano())
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.queue <- job:
		return nil
	default:
		return ErrServiceOverloaded
	}
}

func (s *SimpleInMemoryQueue) Dequeue(ctx context.Context) (*ResumptionJob, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case job := <-s.queue:
		return job, nil
	}
}

// Ack is a no-op for in-memory queue because items are removed on Dequeue.
func (s *SimpleInMemoryQueue) Ack(ctx context.Context, jobID string) error {
	return nil
}

// SimpleGraphRegistry is a simple implementation of GraphRegistry
type SimpleGraphRegistry struct {
	graphs map[string]GraphDefinition
}

// NewSimpleGraphRegistry creates a new simple graph registry
func NewSimpleGraphRegistry() *SimpleGraphRegistry {
	return &SimpleGraphRegistry{
		graphs: make(map[string]GraphDefinition),
	}
}

func (s *SimpleGraphRegistry) GetGraph(graphID string) (GraphDefinition, error) {
	graphDef, exists := s.graphs[graphID]
	if !exists {
		return GraphDefinition{}, fmt.Errorf("graph not found: %s", graphID)
	}
	return graphDef, nil
}

func (s *SimpleGraphRegistry) RegisterGraph(graphDef GraphDefinition) {
	s.graphs[graphDef.ID] = graphDef
}

// SimpleResumableExecutor is a simple implementation of ResumableExecutor
type SimpleResumableExecutor struct {
	graphID      string
	graphVersion string
	nodes        map[string]graph.Node
	currentNode  string
}

// NewSimpleResumableExecutor creates a new simple resumable executor
func NewSimpleResumableExecutor(graphID, graphVersion string, nodes map[string]graph.Node) *SimpleResumableExecutor {
	return &SimpleResumableExecutor{
		graphID:      graphID,
		graphVersion: graphVersion,
		nodes:        nodes,
		currentNode:  "", // Will be set during execution
	}
}

func (s *SimpleResumableExecutor) ExecuteCurrent(state graph.State) (graph.State, error) {
	// This is a simplified implementation
	// In a real implementation, this would traverse the graph from the current position
	return state, nil
}

func (s *SimpleResumableExecutor) ResumeFromNode(nodeID string, state graph.State) error {
	node, exists := s.nodes[nodeID]
	if !exists {
		return fmt.Errorf("node not found: %s", nodeID)
	}

	// Execute the node
	ctx := context.Background()
	return node.Execute(ctx, state)
}

func (s *SimpleResumableExecutor) CurrentNodeID() string {
	return s.currentNode
}

// DeserializeState properly implements the state deserialization
func DeserializeState(data []byte) (graph.State, error) {
	// Create a new MemoryState
	state := graph.NewMemoryState()

	// Unmarshal the data into the state
	if err := state.Unmarshal(data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal state: %w", err)
	}

	return state, nil
}
