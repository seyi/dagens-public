package graph

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	// ErrStateNotFound indicates the requested execution state does not exist.
	// Use IsStateNotFound(err) or errors.Is(err, ErrStateNotFound) to check.
	ErrStateNotFound = errors.New("graph: state snapshot not found")
	// ErrInvalidExecutionID indicates a missing or empty execution ID.
	ErrInvalidExecutionID = errors.New("graph: execution ID is required")
	// ErrNilSnapshot indicates a nil snapshot was provided.
	ErrNilSnapshot = errors.New("graph: snapshot is required")
)

// IsStateNotFound reports whether err indicates a missing state snapshot.
// This is a convenience wrapper around errors.Is(err, ErrStateNotFound).
func IsStateNotFound(err error) bool {
	return errors.Is(err, ErrStateNotFound)
}

// StateStore defines the persistence layer for workflow states.
// Implementations should be thread-safe and handle concurrent access.
type StateStore interface {
	// Save persists a snapshot for the given execution ID.
	// Overwrites any existing snapshot for the same ID.
	Save(ctx context.Context, executionID string, snapshot *StateSnapshot) error

	// SaveBatch persists multiple snapshots atomically (best-effort).
	// Returns an error if any snapshot fails validation.
	// Complexity: O(n) where n is the number of snapshots.
	SaveBatch(ctx context.Context, snapshots map[string]*StateSnapshot) error

	// Load retrieves the snapshot for the given execution ID.
	// Returns ErrStateNotFound if no snapshot exists.
	Load(ctx context.Context, executionID string) (*StateSnapshot, error)

	// Delete removes the snapshot for the given execution ID.
	// Returns nil if the snapshot doesn't exist (idempotent).
	Delete(ctx context.Context, executionID string) error

	// List returns all execution IDs currently stored.
	// Returns an empty slice if no snapshots exist.
	List(ctx context.Context) ([]string, error)

	// Exists checks if a snapshot exists for the given execution ID.
	Exists(ctx context.Context, executionID string) (bool, error)

	// Clear removes all snapshots from the store.
	// Complexity: O(n) where n is the number of stored snapshots.
	Clear(ctx context.Context) error
}

// MemoryStateStore is a thread-safe, in-memory implementation of StateStore.
// Useful for testing and ephemeral workflows.
type MemoryStateStore struct {
	mu        sync.RWMutex
	snapshots map[string]*StateSnapshot
}

// NewMemoryStateStore creates a new instance of MemoryStateStore.
func NewMemoryStateStore() *MemoryStateStore {
	return &MemoryStateStore{
		snapshots: make(map[string]*StateSnapshot),
	}
}

// Save persists a snapshot for a given execution ID.
func (s *MemoryStateStore) Save(ctx context.Context, executionID string, snapshot *StateSnapshot) error {
	if executionID == "" {
		return ErrInvalidExecutionID
	}
	if snapshot == nil {
		return ErrNilSnapshot
	}

	// Check context before proceeding
	if err := ctx.Err(); err != nil {
		return err
	}

	// Create a deep copy of the snapshot to prevent external mutation
	storedSnapshot := snapshot.Clone()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[executionID] = storedSnapshot
	return nil
}

// SaveBatch persists multiple snapshots atomically.
// All snapshots are validated before any are stored.
func (s *MemoryStateStore) SaveBatch(ctx context.Context, snapshots map[string]*StateSnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// Validate and clone all snapshots first
	clones := make(map[string]*StateSnapshot, len(snapshots))
	for id, snap := range snapshots {
		if err := ctx.Err(); err != nil {
			return err
		}
		if id == "" {
			return ErrInvalidExecutionID
		}
		if snap == nil {
			return ErrNilSnapshot
		}
		clones[id] = snap.Clone()
	}

	// Store all clones atomically
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, snap := range clones {
		s.snapshots[id] = snap
	}
	return nil
}

// Load retrieves a snapshot for a given execution ID.
func (s *MemoryStateStore) Load(ctx context.Context, executionID string) (*StateSnapshot, error) {
	if executionID == "" {
		return nil, ErrInvalidExecutionID
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot, ok := s.snapshots[executionID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrStateNotFound, executionID)
	}

	// Return a deep copy so the caller cannot modify the store's internal version
	return snapshot.Clone(), nil
}

// Delete removes the snapshot for a given execution ID.
// This operation is idempotent - returns nil if snapshot doesn't exist.
func (s *MemoryStateStore) Delete(ctx context.Context, executionID string) error {
	if executionID == "" {
		return ErrInvalidExecutionID
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.snapshots, executionID)
	return nil
}

// List returns all execution IDs currently stored.
func (s *MemoryStateStore) List(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0, len(s.snapshots))
	for k := range s.snapshots {
		keys = append(keys, k)
	}

	sort.Strings(keys)
	return keys, nil
}

// Exists checks if a snapshot exists for the given execution ID.
func (s *MemoryStateStore) Exists(ctx context.Context, executionID string) (bool, error) {
	if executionID == "" {
		return false, ErrInvalidExecutionID
	}

	if err := ctx.Err(); err != nil {
		return false, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	_, exists := s.snapshots[executionID]
	return exists, nil
}

// Count returns the number of snapshots stored.
func (s *MemoryStateStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.snapshots)
}

// Clear removes all snapshots from the store.
func (s *MemoryStateStore) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots = make(map[string]*StateSnapshot)
	return nil
}

// Reset is a convenience method that calls Clear with background context.
// Deprecated: Use Clear(ctx) instead for proper context handling.
func (s *MemoryStateStore) Reset() {
	_ = s.Clear(context.Background())
}
