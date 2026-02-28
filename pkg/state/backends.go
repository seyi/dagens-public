// Package state provides state management backends for distributed agent systems.
package state

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// MemoryStateBackend implements StateBackend with in-memory storage
type MemoryStateBackend struct {
	data   map[string]memoryEntry
	mu     sync.RWMutex
	closed bool
}

type memoryEntry struct {
	value     []byte
	expiresAt time.Time
	hasTTL    bool
}

// NewMemoryStateBackend creates a new in-memory state backend
func NewMemoryStateBackend() *MemoryStateBackend {
	backend := &MemoryStateBackend{
		data: make(map[string]memoryEntry),
	}

	// Start background cleanup goroutine
	go backend.cleanupExpired()

	return backend
}

func (m *MemoryStateBackend) cleanupExpired() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return
		}

		now := time.Now()
		for key, entry := range m.data {
			if entry.hasTTL && now.After(entry.expiresAt) {
				delete(m.data, key)
			}
		}
		m.mu.Unlock()
	}
}

// Get retrieves a value by key
func (m *MemoryStateBackend) Get(ctx context.Context, key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, ErrBackendClosed
	}

	entry, exists := m.data[key]
	if !exists {
		return nil, ErrNotFound
	}

	// Check expiration
	if entry.hasTTL && time.Now().After(entry.expiresAt) {
		return nil, ErrNotFound
	}

	return entry.value, nil
}

// Set stores a value with optional TTL
func (m *MemoryStateBackend) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrBackendClosed
	}

	entry := memoryEntry{
		value: value,
	}

	if ttl > 0 {
		entry.hasTTL = true
		entry.expiresAt = time.Now().Add(ttl)
	}

	m.data[key] = entry
	return nil
}

// Delete removes a value
func (m *MemoryStateBackend) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return ErrBackendClosed
	}

	delete(m.data, key)
	return nil
}

// List returns keys matching a pattern (supports * wildcard)
func (m *MemoryStateBackend) List(ctx context.Context, pattern string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return nil, ErrBackendClosed
	}

	keys := make([]string, 0)
	now := time.Now()

	for key, entry := range m.data {
		// Skip expired entries
		if entry.hasTTL && now.After(entry.expiresAt) {
			continue
		}

		if matchPattern(pattern, key) {
			keys = append(keys, key)
		}
	}

	return keys, nil
}

// Close closes the backend
func (m *MemoryStateBackend) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.closed = true
	m.data = nil
	return nil
}

// matchPattern matches a key against a pattern with * wildcard
func matchPattern(pattern, key string) bool {
	if pattern == "*" {
		return true
	}

	// Handle prefix patterns like "sessions:*"
	if strings.HasSuffix(pattern, "*") {
		prefix := pattern[:len(pattern)-1]
		return strings.HasPrefix(key, prefix)
	}

	// Handle suffix patterns like "*:session"
	if strings.HasPrefix(pattern, "*") {
		suffix := pattern[1:]
		return strings.HasSuffix(key, suffix)
	}

	// Exact match
	return pattern == key
}

// MemoryCheckpointBackend implements CheckpointBackend with in-memory storage
type MemoryCheckpointBackend struct {
	checkpoints map[string]*Checkpoint
	mu          sync.RWMutex
}

// NewMemoryCheckpointBackend creates a new memory checkpoint backend
func NewMemoryCheckpointBackend() *MemoryCheckpointBackend {
	return &MemoryCheckpointBackend{
		checkpoints: make(map[string]*Checkpoint),
	}
}

// Save stores a checkpoint
func (m *MemoryCheckpointBackend) Save(ctx context.Context, checkpoint *Checkpoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	checkpoint.Timestamp = time.Now()
	m.checkpoints[checkpoint.ID] = checkpoint
	return nil
}

// Load retrieves a checkpoint
func (m *MemoryCheckpointBackend) Load(ctx context.Context, id string) (*Checkpoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cp, exists := m.checkpoints[id]
	if !exists {
		return nil, ErrNotFound
	}

	return cp, nil
}

// Delete removes a checkpoint
func (m *MemoryCheckpointBackend) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.checkpoints, id)
	return nil
}

// ListByAgent returns checkpoints for an agent
func (m *MemoryCheckpointBackend) ListByAgent(ctx context.Context, agentID string, limit int) ([]*CheckpointMeta, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	metas := make([]*CheckpointMeta, 0)
	for _, cp := range m.checkpoints {
		if cp.JobID == agentID {
			metas = append(metas, &CheckpointMeta{
				ID:        cp.ID,
				AgentID:   cp.JobID,
				Timestamp: cp.Timestamp,
			})
			if limit > 0 && len(metas) >= limit {
				break
			}
		}
	}

	return metas, nil
}

// GetLatest returns the latest checkpoint for an agent
func (m *MemoryCheckpointBackend) GetLatest(ctx context.Context, agentID string) (*Checkpoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var latest *Checkpoint
	for _, cp := range m.checkpoints {
		if cp.JobID == agentID {
			if latest == nil || cp.Timestamp.After(latest.Timestamp) {
				latest = cp
			}
		}
	}

	if latest == nil {
		return nil, ErrNotFound
	}

	return latest, nil
}

// MemorySessionBackend implements SessionBackend with in-memory storage
type MemorySessionBackend struct {
	sessions map[string]*SessionState
	mu       sync.RWMutex
}

// NewMemorySessionBackend creates a new memory session backend
func NewMemorySessionBackend() *MemorySessionBackend {
	return &MemorySessionBackend{
		sessions: make(map[string]*SessionState),
	}
}

// Create creates a new session
func (m *MemorySessionBackend) Create(ctx context.Context, session *SessionState) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sessions[session.ID]; exists {
		return ErrAlreadyExists
	}

	session.CreatedAt = time.Now()
	session.UpdatedAt = session.CreatedAt
	session.Version = 1
	m.sessions[session.ID] = session
	return nil
}

// Get retrieves a session (returns a copy to support optimistic locking)
func (m *MemorySessionBackend) Get(ctx context.Context, id string) (*SessionState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, exists := m.sessions[id]
	if !exists {
		return nil, ErrNotFound
	}

	// Return a copy to support optimistic locking
	copy := &SessionState{
		ID:        session.ID,
		AgentID:   session.AgentID,
		Metadata:  session.Metadata,
		CreatedAt: session.CreatedAt,
		UpdatedAt: session.UpdatedAt,
		Version:   session.Version,
	}
	// Deep copy state map
	if session.State != nil {
		copy.State = make(map[string]interface{})
		for k, v := range session.State {
			copy.State[k] = v
		}
	}
	return copy, nil
}

// Update updates a session with optimistic locking
func (m *MemorySessionBackend) Update(ctx context.Context, session *SessionState) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, exists := m.sessions[session.ID]
	if !exists {
		return ErrNotFound
	}

	// Optimistic locking check
	if existing.Version != session.Version {
		return ErrVersionConflict
	}

	session.Version++
	session.UpdatedAt = time.Now()
	m.sessions[session.ID] = session
	return nil
}

// Delete removes a session
func (m *MemorySessionBackend) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.sessions, id)
	return nil
}

// ListByAgent returns sessions for an agent
func (m *MemorySessionBackend) ListByAgent(ctx context.Context, agentID string) ([]*SessionState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions := make([]*SessionState, 0)
	for _, s := range m.sessions {
		if s.AgentID == agentID {
			sessions = append(sessions, s)
		}
	}

	return sessions, nil
}

// MemoryHistoryBackend implements HistoryBackend with in-memory storage
type MemoryHistoryBackend struct {
	history map[string][]*HistoryEntry
	mu      sync.RWMutex
}

// NewMemoryHistoryBackend creates a new memory history backend
func NewMemoryHistoryBackend() *MemoryHistoryBackend {
	return &MemoryHistoryBackend{
		history: make(map[string][]*HistoryEntry),
	}
}

// Append adds an entry to history
func (m *MemoryHistoryBackend) Append(ctx context.Context, entry *HistoryEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry.Timestamp = time.Now()
	if entry.ID == "" {
		entry.ID = fmt.Sprintf("%s-%d", entry.SessionID, time.Now().UnixNano())
	}

	m.history[entry.SessionID] = append(m.history[entry.SessionID], entry)
	return nil
}

// List returns history entries for a session
func (m *MemoryHistoryBackend) List(ctx context.Context, sessionID string, limit int) ([]*HistoryEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entries := m.history[sessionID]
	if entries == nil {
		return []*HistoryEntry{}, nil
	}

	if limit > 0 && len(entries) > limit {
		// Return most recent entries
		return entries[len(entries)-limit:], nil
	}

	return entries, nil
}

// Clear removes all history for a session
func (m *MemoryHistoryBackend) Clear(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.history, sessionID)
	return nil
}

// CompositeStateStore combines all state backends into a unified store
type CompositeStateStore struct {
	State      StateBackend
	Checkpoint CheckpointBackend
	Session    SessionBackend
	History    HistoryBackend
}

// NewMemoryStateStore creates a complete in-memory state store
func NewMemoryStateStore() *CompositeStateStore {
	return &CompositeStateStore{
		State:      NewMemoryStateBackend(),
		Checkpoint: NewMemoryCheckpointBackend(),
		Session:    NewMemorySessionBackend(),
		History:    NewMemoryHistoryBackend(),
	}
}

// Close closes all backends
func (c *CompositeStateStore) Close() error {
	if closer, ok := c.State.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

// SerializableState provides JSON serialization helpers for state
type SerializableState struct {
	Key       string                 `json:"key"`
	Value     interface{}            `json:"value"`
	Metadata  map[string]string      `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
	Version   int64                  `json:"version"`
}

// Serialize converts state to JSON
func (s *SerializableState) Serialize() ([]byte, error) {
	return json.Marshal(s)
}

// Deserialize populates state from JSON
func (s *SerializableState) Deserialize(data []byte) error {
	return json.Unmarshal(data, s)
}
