package interaction

import (
	"context"
	"sync"
)

// MemorySessionManager is an in-memory implementation of SessionManager.
// Suitable for development, testing, and single-instance deployments.
// For distributed systems, use Redis or PostgreSQL implementations.
type MemorySessionManager struct {
	mu          sync.RWMutex
	sessions    map[string][]Message
	checkpoints map[string][]byte

	// maxHistory limits the number of messages per session (0 = unlimited)
	maxHistory int
}

// MemorySessionOption configures a MemorySessionManager.
type MemorySessionOption func(*MemorySessionManager)

// WithMaxHistory sets the maximum number of messages to retain per session.
// Oldest messages are dropped when the limit is exceeded.
func WithMaxHistory(max int) MemorySessionOption {
	return func(m *MemorySessionManager) {
		m.maxHistory = max
	}
}

// NewMemorySessionManager creates a new in-memory session manager.
func NewMemorySessionManager(opts ...MemorySessionOption) *MemorySessionManager {
	m := &MemorySessionManager{
		sessions:    make(map[string][]Message),
		checkpoints: make(map[string][]byte),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// LoadHistory fetches the conversation history for a session.
func (m *MemorySessionManager) LoadHistory(_ context.Context, sessionID string) ([]Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	history, exists := m.sessions[sessionID]
	if !exists {
		return nil, nil // Return empty, not error - session will be created on first append
	}

	// Return a copy to prevent mutation
	result := make([]Message, len(history))
	copy(result, history)
	return result, nil
}

// Append adds new messages to the session's conversation history.
func (m *MemorySessionManager) Append(_ context.Context, sessionID string, msgs ...Message) error {
	if len(msgs) == 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	history := m.sessions[sessionID]
	history = append(history, msgs...)

	// Enforce max history limit if set
	if m.maxHistory > 0 && len(history) > m.maxHistory {
		// Keep the most recent messages
		history = history[len(history)-m.maxHistory:]
	}

	m.sessions[sessionID] = history
	return nil
}

// Checkpoint saves execution state for HITL resumption.
func (m *MemorySessionManager) Checkpoint(_ context.Context, sessionID string, state []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Store a copy to prevent mutation
	stateCopy := make([]byte, len(state))
	copy(stateCopy, state)
	m.checkpoints[sessionID] = stateCopy
	return nil
}

// LoadCheckpoint retrieves the saved execution state.
func (m *MemorySessionManager) LoadCheckpoint(_ context.Context, sessionID string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	state, exists := m.checkpoints[sessionID]
	if !exists {
		return nil, ErrCheckpointNotFound
	}

	// Return a copy to prevent mutation
	result := make([]byte, len(state))
	copy(result, state)
	return result, nil
}

// DeleteSession removes all data for a session.
func (m *MemorySessionManager) DeleteSession(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.sessions, sessionID)
	delete(m.checkpoints, sessionID)
	return nil
}

// SessionCount returns the number of active sessions (useful for monitoring).
func (m *MemorySessionManager) SessionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// Clear removes all sessions and checkpoints (useful for testing).
func (m *MemorySessionManager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions = make(map[string][]Message)
	m.checkpoints = make(map[string][]byte)
}

// Ensure MemorySessionManager implements SessionManager.
var _ SessionManager = (*MemorySessionManager)(nil)
