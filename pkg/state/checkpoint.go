// Package state provides state management and checkpointing
// inspired by Spark's RDD checkpointing and lineage tracking.
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/agent"
)

// Common errors
var (
	ErrNotFound       = errors.New("state not found")
	ErrAlreadyExists  = errors.New("state already exists")
	ErrInvalidState   = errors.New("invalid state")
	ErrBackendClosed  = errors.New("backend is closed")
	ErrVersionConflict = errors.New("version conflict")
)

// CheckpointManager manages agent state checkpointing for fault tolerance
type CheckpointManager interface {
	// Checkpoint saves agent state
	Checkpoint(ctx context.Context, checkpoint *Checkpoint) error

	// Restore restores agent state from checkpoint
	Restore(ctx context.Context, checkpointID string) (*Checkpoint, error)

	// List returns all checkpoints for a job
	List(ctx context.Context, jobID string) ([]*CheckpointMetadata, error)

	// Delete removes a checkpoint
	Delete(ctx context.Context, checkpointID string) error
}

// Checkpoint represents a saved agent state
type Checkpoint struct {
	ID           string
	JobID        string
	StageID      int
	TaskID       string
	Timestamp    time.Time
	State        map[string]interface{}
	Lineage      []*LineageNode
	Metadata     map[string]interface{}
}

// CheckpointMetadata holds checkpoint information
type CheckpointMetadata struct {
	ID        string
	JobID     string
	Timestamp time.Time
	Size      int64
}

// LineageNode represents a node in the execution lineage
// This enables recomputation on failure (like Spark's RDD lineage)
type LineageNode struct {
	ID           string
	AgentID      string
	Input        *agent.AgentInput
	Output       *agent.AgentOutput
	Dependencies []*LineageNode
	Timestamp    time.Time
}

// CheckpointStorage abstracts the storage backend for checkpoints
type CheckpointStorage interface {
	Save(ctx context.Context, key string, data []byte) error
	Load(ctx context.Context, key string) ([]byte, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
}

// MemoryCheckpointManager implements in-memory checkpointing
type MemoryCheckpointManager struct {
	checkpoints map[string]*Checkpoint
	mu          sync.RWMutex
}

func NewMemoryCheckpointManager() *MemoryCheckpointManager {
	return &MemoryCheckpointManager{
		checkpoints: make(map[string]*Checkpoint),
	}
}

func (m *MemoryCheckpointManager) Checkpoint(ctx context.Context, checkpoint *Checkpoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	checkpoint.Timestamp = time.Now()
	m.checkpoints[checkpoint.ID] = checkpoint
	return nil
}

func (m *MemoryCheckpointManager) Restore(ctx context.Context, checkpointID string) (*Checkpoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	checkpoint, exists := m.checkpoints[checkpointID]
	if !exists {
		return nil, fmt.Errorf("checkpoint %s not found", checkpointID)
	}

	return checkpoint, nil
}

func (m *MemoryCheckpointManager) List(ctx context.Context, jobID string) ([]*CheckpointMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	metadata := make([]*CheckpointMetadata, 0)
	for _, cp := range m.checkpoints {
		if cp.JobID == jobID {
			metadata = append(metadata, &CheckpointMetadata{
				ID:        cp.ID,
				JobID:     cp.JobID,
				Timestamp: cp.Timestamp,
			})
		}
	}

	return metadata, nil
}

func (m *MemoryCheckpointManager) Delete(ctx context.Context, checkpointID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.checkpoints, checkpointID)
	return nil
}

// FileCheckpointManager implements file-based checkpointing
type FileCheckpointManager struct {
	storage CheckpointStorage
}

func NewFileCheckpointManager(storage CheckpointStorage) *FileCheckpointManager {
	return &FileCheckpointManager{
		storage: storage,
	}
}

func (f *FileCheckpointManager) Checkpoint(ctx context.Context, checkpoint *Checkpoint) error {
	checkpoint.Timestamp = time.Now()

	data, err := json.Marshal(checkpoint)
	if err != nil {
		return fmt.Errorf("failed to marshal checkpoint: %w", err)
	}

	key := fmt.Sprintf("checkpoints/%s/%s", checkpoint.JobID, checkpoint.ID)
	return f.storage.Save(ctx, key, data)
}

func (f *FileCheckpointManager) Restore(ctx context.Context, checkpointID string) (*Checkpoint, error) {
	// In a real implementation, we'd need to know the job ID
	// For simplicity, we'll assume we can list and find it
	return nil, fmt.Errorf("not implemented")
}

func (f *FileCheckpointManager) List(ctx context.Context, jobID string) ([]*CheckpointMetadata, error) {
	prefix := fmt.Sprintf("checkpoints/%s/", jobID)
	keys, err := f.storage.List(ctx, prefix)
	if err != nil {
		return nil, err
	}

	metadata := make([]*CheckpointMetadata, 0)
	for _, key := range keys {
		// In a real implementation, we'd load metadata without loading full checkpoint
		metadata = append(metadata, &CheckpointMetadata{
			ID:    key,
			JobID: jobID,
		})
	}

	return metadata, nil
}

func (f *FileCheckpointManager) Delete(ctx context.Context, checkpointID string) error {
	return fmt.Errorf("not implemented")
}

// LineageTracker tracks agent execution lineage for fault recovery
type LineageTracker struct {
	nodes map[string]*LineageNode
	mu    sync.RWMutex
}

func NewLineageTracker() *LineageTracker {
	return &LineageTracker{
		nodes: make(map[string]*LineageNode),
	}
}

// RecordExecution records an agent execution in the lineage
func (lt *LineageTracker) RecordExecution(
	agentID string,
	input *agent.AgentInput,
	output *agent.AgentOutput,
	dependencies []string,
) *LineageNode {
	lt.mu.Lock()
	defer lt.mu.Unlock()

	node := &LineageNode{
		ID:           fmt.Sprintf("%s-%d", agentID, time.Now().UnixNano()),
		AgentID:      agentID,
		Input:        input,
		Output:       output,
		Dependencies: make([]*LineageNode, 0),
		Timestamp:    time.Now(),
	}

	// Link dependencies
	for _, depID := range dependencies {
		if depNode, exists := lt.nodes[depID]; exists {
			node.Dependencies = append(node.Dependencies, depNode)
		}
	}

	lt.nodes[node.ID] = node
	return node
}

// GetLineage retrieves the execution lineage for a node
func (lt *LineageTracker) GetLineage(nodeID string) (*LineageNode, error) {
	lt.mu.RLock()
	defer lt.mu.RUnlock()

	node, exists := lt.nodes[nodeID]
	if !exists {
		return nil, fmt.Errorf("lineage node %s not found", nodeID)
	}

	return node, nil
}

// RecomputeFrom recomputes execution from a lineage node
// This is used for fault recovery (similar to Spark's RDD recomputation)
func (lt *LineageTracker) RecomputeFrom(ctx context.Context, nodeID string) (*agent.AgentOutput, error) {
	node, err := lt.GetLineage(nodeID)
	if err != nil {
		return nil, err
	}

	// Check if we have cached output
	if node.Output != nil {
		return node.Output, nil
	}

	// Recursively recompute dependencies
	for _, dep := range node.Dependencies {
		if _, err := lt.RecomputeFrom(ctx, dep.ID); err != nil {
			return nil, fmt.Errorf("failed to recompute dependency %s: %w", dep.ID, err)
		}
	}

	// In a real implementation, we would re-execute the agent here
	return nil, fmt.Errorf("recomputation not fully implemented")
}

// MemoryStorage implements in-memory checkpoint storage
type MemoryStorage struct {
	data map[string][]byte
	mu   sync.RWMutex
}

func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		data: make(map[string][]byte),
	}
}

func (m *MemoryStorage) Save(ctx context.Context, key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.data[key] = data
	return nil
}

func (m *MemoryStorage) Load(ctx context.Context, key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, exists := m.data[key]
	if !exists {
		return nil, fmt.Errorf("key %s not found", key)
	}

	return data, nil
}

func (m *MemoryStorage) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.data, key)
	return nil
}

func (m *MemoryStorage) List(ctx context.Context, prefix string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	keys := make([]string, 0)
	for key := range m.data {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			keys = append(keys, key)
		}
	}

	return keys, nil
}

// StateManager manages distributed agent state
type StateManager struct {
	checkpointMgr CheckpointManager
	lineageTracker *LineageTracker
	mu             sync.RWMutex
}

func NewStateManager(checkpointMgr CheckpointManager) *StateManager {
	return &StateManager{
		checkpointMgr:  checkpointMgr,
		lineageTracker: NewLineageTracker(),
	}
}

func (sm *StateManager) CheckpointManager() CheckpointManager {
	return sm.checkpointMgr
}

func (sm *StateManager) LineageTracker() *LineageTracker {
	return sm.lineageTracker
}

// Additional types for distributed state backends

// CheckpointMeta holds checkpoint metadata for listing
type CheckpointMeta struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	SessionID string    `json:"session_id"`
	Timestamp time.Time `json:"timestamp"`
	Version   int64     `json:"version"`
}

// SessionState holds session state data
type SessionState struct {
	ID        string                 `json:"id"`
	AgentID   string                 `json:"agent_id"`
	State     map[string]interface{} `json:"state"`
	Metadata  map[string]string      `json:"metadata"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
	Version   int64                  `json:"version"`
}

// HistoryEntry holds an execution history entry
type HistoryEntry struct {
	ID        string                 `json:"id"`
	SessionID string                 `json:"session_id"`
	Action    string                 `json:"action"`
	Input     interface{}            `json:"input,omitempty"`
	Output    interface{}            `json:"output,omitempty"`
	Metadata  map[string]string      `json:"metadata,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

// StateBackend is the interface for distributed state storage
type StateBackend interface {
	// Get retrieves a value by key
	Get(ctx context.Context, key string) ([]byte, error)
	// Set stores a value with optional TTL
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Delete removes a value
	Delete(ctx context.Context, key string) error
	// List returns keys matching a pattern
	List(ctx context.Context, pattern string) ([]string, error)
	// Close closes the backend connection
	Close() error
}

// CheckpointBackend is the interface for checkpoint storage
type CheckpointBackend interface {
	// Save stores a checkpoint
	Save(ctx context.Context, checkpoint *Checkpoint) error
	// Load retrieves a checkpoint
	Load(ctx context.Context, id string) (*Checkpoint, error)
	// Delete removes a checkpoint
	Delete(ctx context.Context, id string) error
	// List returns checkpoints for an agent
	ListByAgent(ctx context.Context, agentID string, limit int) ([]*CheckpointMeta, error)
	// GetLatest returns the latest checkpoint for an agent
	GetLatest(ctx context.Context, agentID string) (*Checkpoint, error)
}

// SessionBackend is the interface for session state storage
type SessionBackend interface {
	// Create creates a new session
	Create(ctx context.Context, session *SessionState) error
	// Get retrieves a session
	Get(ctx context.Context, id string) (*SessionState, error)
	// Update updates a session (with optimistic locking)
	Update(ctx context.Context, session *SessionState) error
	// Delete removes a session
	Delete(ctx context.Context, id string) error
	// List returns sessions for an agent
	ListByAgent(ctx context.Context, agentID string) ([]*SessionState, error)
}

// HistoryBackend is the interface for execution history storage
type HistoryBackend interface {
	// Append adds an entry to history
	Append(ctx context.Context, entry *HistoryEntry) error
	// List returns history entries for a session
	List(ctx context.Context, sessionID string, limit int) ([]*HistoryEntry, error)
	// Clear removes all history for a session
	Clear(ctx context.Context, sessionID string) error
}
