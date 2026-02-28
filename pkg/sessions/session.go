// Package sessions provides session management for multi-turn agent interactions
// Inspired by ADK's session management and rewinding capabilities
package sessions

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/google/uuid"
)

// Session represents a conversation session with an agent
type Session struct {
	ID           string
	AgentID      string
	History      []*Invocation
	Context      map[string]interface{}
	CreatedAt    time.Time
	LastAccess   time.Time
	Checkpoints  []*SessionCheckpoint
	Metadata     map[string]interface{}
	mu           sync.RWMutex
}

// Invocation represents a single agent invocation within a session
type Invocation struct {
	ID        string
	SessionID string
	Input     *agent.AgentInput
	Output    *agent.AgentOutput
	Timestamp time.Time
	Duration  time.Duration
	State     map[string]interface{}
	Index     int // Position in history
}

// SessionCheckpoint represents a saved state that can be restored
type SessionCheckpoint struct {
	ID              string
	SessionID       string
	InvocationIndex int
	Context         map[string]interface{}
	Timestamp       time.Time
	Description     string
}

// SessionManager manages agent sessions
type SessionManager interface {
	// CreateSession creates a new session for an agent
	CreateSession(ctx context.Context, agentID string) (*Session, error)

	// GetSession retrieves a session by ID
	GetSession(ctx context.Context, sessionID string) (*Session, error)

	// AddInvocation adds an invocation to the session
	AddInvocation(ctx context.Context, sessionID string, invocation *Invocation) error

	// GetHistory retrieves the session history
	GetHistory(ctx context.Context, sessionID string) ([]*Invocation, error)

	// Rewind rewinds the session to a previous invocation
	Rewind(ctx context.Context, sessionID string, invocationID string) error

	// Checkpoint creates a checkpoint of the current session state
	Checkpoint(ctx context.Context, sessionID string, description string) (*SessionCheckpoint, error)

	// RestoreCheckpoint restores session to a checkpoint
	RestoreCheckpoint(ctx context.Context, sessionID string, checkpointID string) error

	// UpdateContext updates the session context
	UpdateContext(ctx context.Context, sessionID string, updates map[string]interface{}) error

	// DeleteSession removes a session
	DeleteSession(ctx context.Context, sessionID string) error

	// ListSessions lists all sessions for an agent
	ListSessions(ctx context.Context, agentID string) ([]*Session, error)
}

// MemorySessionManager implements in-memory session management
type MemorySessionManager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

// NewMemorySessionManager creates a new in-memory session manager
func NewMemorySessionManager() *MemorySessionManager {
	return &MemorySessionManager{
		sessions: make(map[string]*Session),
	}
}

func (m *MemorySessionManager) CreateSession(ctx context.Context, agentID string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session := &Session{
		ID:          uuid.New().String(),
		AgentID:     agentID,
		History:     make([]*Invocation, 0),
		Context:     make(map[string]interface{}),
		CreatedAt:   time.Now(),
		LastAccess:  time.Now(),
		Checkpoints: make([]*SessionCheckpoint, 0),
		Metadata:    make(map[string]interface{}),
	}

	m.sessions[session.ID] = session
	return session, nil
}

func (m *MemorySessionManager) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	session, exists := m.sessions[sessionID]
	if !exists {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	// Update last access
	session.mu.Lock()
	session.LastAccess = time.Now()
	session.mu.Unlock()

	return session, nil
}

func (m *MemorySessionManager) AddInvocation(ctx context.Context, sessionID string, invocation *Invocation) error {
	m.mu.RLock()
	session, exists := m.sessions[sessionID]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("session %s not found", sessionID)
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	invocation.SessionID = sessionID
	invocation.Index = len(session.History)
	invocation.Timestamp = time.Now()

	session.History = append(session.History, invocation)
	session.LastAccess = time.Now()

	return nil
}

func (m *MemorySessionManager) GetHistory(ctx context.Context, sessionID string) ([]*Invocation, error) {
	session, err := m.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	session.mu.RLock()
	defer session.mu.RUnlock()

	// Return copy
	history := make([]*Invocation, len(session.History))
	copy(history, session.History)

	return history, nil
}

func (m *MemorySessionManager) Rewind(ctx context.Context, sessionID string, invocationID string) error {
	session, err := m.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	// Find the invocation index
	targetIndex := -1
	for i, inv := range session.History {
		if inv.ID == invocationID {
			targetIndex = i
			break
		}
	}

	if targetIndex == -1 {
		return fmt.Errorf("invocation %s not found in session", invocationID)
	}

	// Truncate history to target invocation
	session.History = session.History[:targetIndex+1]

	// Restore context from that invocation
	if session.History[targetIndex].State != nil {
		session.Context = make(map[string]interface{})
		for k, v := range session.History[targetIndex].State {
			session.Context[k] = v
		}
	}

	session.LastAccess = time.Now()

	return nil
}

func (m *MemorySessionManager) Checkpoint(ctx context.Context, sessionID string, description string) (*SessionCheckpoint, error) {
	session, err := m.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	checkpoint := &SessionCheckpoint{
		ID:              uuid.New().String(),
		SessionID:       sessionID,
		InvocationIndex: len(session.History) - 1,
		Context:         make(map[string]interface{}),
		Timestamp:       time.Now(),
		Description:     description,
	}

	// Deep copy context
	for k, v := range session.Context {
		checkpoint.Context[k] = v
	}

	session.Checkpoints = append(session.Checkpoints, checkpoint)
	session.LastAccess = time.Now()

	return checkpoint, nil
}

func (m *MemorySessionManager) RestoreCheckpoint(ctx context.Context, sessionID string, checkpointID string) error {
	session, err := m.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	// Find checkpoint
	var checkpoint *SessionCheckpoint
	for _, cp := range session.Checkpoints {
		if cp.ID == checkpointID {
			checkpoint = cp
			break
		}
	}

	if checkpoint == nil {
		return fmt.Errorf("checkpoint %s not found", checkpointID)
	}

	// Restore history to checkpoint
	if checkpoint.InvocationIndex >= 0 && checkpoint.InvocationIndex < len(session.History) {
		session.History = session.History[:checkpoint.InvocationIndex+1]
	}

	// Restore context
	session.Context = make(map[string]interface{})
	for k, v := range checkpoint.Context {
		session.Context[k] = v
	}

	session.LastAccess = time.Now()

	return nil
}

func (m *MemorySessionManager) UpdateContext(ctx context.Context, sessionID string, updates map[string]interface{}) error {
	session, err := m.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	for k, v := range updates {
		session.Context[k] = v
	}

	session.LastAccess = time.Now()

	return nil
}

func (m *MemorySessionManager) DeleteSession(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sessions[sessionID]; !exists {
		return fmt.Errorf("session %s not found", sessionID)
	}

	delete(m.sessions, sessionID)
	return nil
}

func (m *MemorySessionManager) ListSessions(ctx context.Context, agentID string) ([]*Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sessions := make([]*Session, 0)
	for _, session := range m.sessions {
		if session.AgentID == agentID {
			sessions = append(sessions, session)
		}
	}

	return sessions, nil
}

// DistributedSessionManager implements distributed session management
// backed by a distributed store (Redis, etcd, etc.)
type DistributedSessionManager struct {
	backend SessionStore
	mu      sync.RWMutex
}

// SessionStore abstracts the storage backend
type SessionStore interface {
	Save(ctx context.Context, session *Session) error
	Load(ctx context.Context, sessionID string) (*Session, error)
	Delete(ctx context.Context, sessionID string) error
	List(ctx context.Context, agentID string) ([]*Session, error)
}

func NewDistributedSessionManager(backend SessionStore) *DistributedSessionManager {
	return &DistributedSessionManager{
		backend: backend,
	}
}

func (d *DistributedSessionManager) CreateSession(ctx context.Context, agentID string) (*Session, error) {
	session := &Session{
		ID:          uuid.New().String(),
		AgentID:     agentID,
		History:     make([]*Invocation, 0),
		Context:     make(map[string]interface{}),
		CreatedAt:   time.Now(),
		LastAccess:  time.Now(),
		Checkpoints: make([]*SessionCheckpoint, 0),
		Metadata:    make(map[string]interface{}),
	}

	if err := d.backend.Save(ctx, session); err != nil {
		return nil, err
	}

	return session, nil
}

func (d *DistributedSessionManager) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	session, err := d.backend.Load(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	// Update last access
	session.LastAccess = time.Now()
	d.backend.Save(ctx, session)

	return session, nil
}

func (d *DistributedSessionManager) AddInvocation(ctx context.Context, sessionID string, invocation *Invocation) error {
	session, err := d.backend.Load(ctx, sessionID)
	if err != nil {
		return err
	}

	invocation.SessionID = sessionID
	invocation.Index = len(session.History)
	invocation.Timestamp = time.Now()

	session.History = append(session.History, invocation)
	session.LastAccess = time.Now()

	return d.backend.Save(ctx, session)
}

func (d *DistributedSessionManager) GetHistory(ctx context.Context, sessionID string) ([]*Invocation, error) {
	session, err := d.backend.Load(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	return session.History, nil
}

func (d *DistributedSessionManager) Rewind(ctx context.Context, sessionID string, invocationID string) error {
	session, err := d.backend.Load(ctx, sessionID)
	if err != nil {
		return err
	}

	// Find invocation
	targetIndex := -1
	for i, inv := range session.History {
		if inv.ID == invocationID {
			targetIndex = i
			break
		}
	}

	if targetIndex == -1 {
		return fmt.Errorf("invocation %s not found", invocationID)
	}

	// Truncate history
	session.History = session.History[:targetIndex+1]

	// Restore context
	if session.History[targetIndex].State != nil {
		session.Context = session.History[targetIndex].State
	}

	session.LastAccess = time.Now()

	return d.backend.Save(ctx, session)
}

func (d *DistributedSessionManager) Checkpoint(ctx context.Context, sessionID string, description string) (*SessionCheckpoint, error) {
	session, err := d.backend.Load(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	checkpoint := &SessionCheckpoint{
		ID:              uuid.New().String(),
		SessionID:       sessionID,
		InvocationIndex: len(session.History) - 1,
		Context:         make(map[string]interface{}),
		Timestamp:       time.Now(),
		Description:     description,
	}

	// Deep copy context
	for k, v := range session.Context {
		checkpoint.Context[k] = v
	}

	session.Checkpoints = append(session.Checkpoints, checkpoint)
	session.LastAccess = time.Now()

	if err := d.backend.Save(ctx, session); err != nil {
		return nil, err
	}

	return checkpoint, nil
}

func (d *DistributedSessionManager) RestoreCheckpoint(ctx context.Context, sessionID string, checkpointID string) error {
	session, err := d.backend.Load(ctx, sessionID)
	if err != nil {
		return err
	}

	// Find checkpoint
	var checkpoint *SessionCheckpoint
	for _, cp := range session.Checkpoints {
		if cp.ID == checkpointID {
			checkpoint = cp
			break
		}
	}

	if checkpoint == nil {
		return fmt.Errorf("checkpoint %s not found", checkpointID)
	}

	// Restore
	if checkpoint.InvocationIndex >= 0 {
		session.History = session.History[:checkpoint.InvocationIndex+1]
	}

	session.Context = checkpoint.Context
	session.LastAccess = time.Now()

	return d.backend.Save(ctx, session)
}

func (d *DistributedSessionManager) UpdateContext(ctx context.Context, sessionID string, updates map[string]interface{}) error {
	session, err := d.backend.Load(ctx, sessionID)
	if err != nil {
		return err
	}

	for k, v := range updates {
		session.Context[k] = v
	}

	session.LastAccess = time.Now()

	return d.backend.Save(ctx, session)
}

func (d *DistributedSessionManager) DeleteSession(ctx context.Context, sessionID string) error {
	return d.backend.Delete(ctx, sessionID)
}

func (d *DistributedSessionManager) ListSessions(ctx context.Context, agentID string) ([]*Session, error) {
	return d.backend.List(ctx, agentID)
}
