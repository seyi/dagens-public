package interaction

import (
	"context"
	"errors"
)

// ErrSessionNotFound is returned when a session doesn't exist.
var ErrSessionNotFound = errors.New("session not found")

// ErrCheckpointNotFound is returned when a checkpoint doesn't exist for a session.
var ErrCheckpointNotFound = errors.New("checkpoint not found")

// ChatAdapter defines how a strictly-typed agent input converts to/from chat messages.
// This enables agents to maintain type-safe internal logic while communicating
// via standard LLM chat protocols.
//
// Type parameter T is the agent's input type.
// Type parameter R is the agent's output type (for FromResponse conversion).
type ChatAdapter[T any, R any] interface {
	// SystemPrompt returns the static system instruction for this agent.
	// This establishes the agent's role and behavioral constraints.
	SystemPrompt() string

	// ToMessages converts the strictly typed input T into chat messages.
	// This allows the agent's structured input to be presented to an LLM.
	ToMessages(input T) ([]Message, error)

	// FromResponse extracts the typed output R from the LLM's response message.
	// This parses unstructured LLM output back into the agent's typed format.
	FromResponse(response Message) (R, error)
}

// SessionManager handles the persistence of conversation state in a distributed system.
// Implementations can use Redis, PostgreSQL, or any distributed store.
type SessionManager interface {
	// LoadHistory fetches the conversation history for a session.
	// Returns ErrSessionNotFound if the session doesn't exist.
	LoadHistory(ctx context.Context, sessionID string) ([]Message, error)

	// Append adds new messages to the session's conversation history.
	// Creates the session if it doesn't exist.
	Append(ctx context.Context, sessionID string, msgs ...Message) error

	// Checkpoint saves execution state for HITL (Human-in-the-Loop) resumption.
	// The state is opaque bytes that the caller defines.
	Checkpoint(ctx context.Context, sessionID string, state []byte) error

	// LoadCheckpoint retrieves the saved execution state.
	// Returns ErrCheckpointNotFound if no checkpoint exists.
	LoadCheckpoint(ctx context.Context, sessionID string) ([]byte, error)

	// DeleteSession removes all data for a session (history and checkpoints).
	DeleteSession(ctx context.Context, sessionID string) error
}

// NoOpSessionManager is a SessionManager that does nothing.
// Use this for batch mode where conversation history isn't tracked.
type NoOpSessionManager struct{}

// LoadHistory returns an empty history (batch mode doesn't track history).
func (n NoOpSessionManager) LoadHistory(_ context.Context, _ string) ([]Message, error) {
	return nil, nil
}

// Append does nothing in batch mode.
func (n NoOpSessionManager) Append(_ context.Context, _ string, _ ...Message) error {
	return nil
}

// Checkpoint does nothing in batch mode.
func (n NoOpSessionManager) Checkpoint(_ context.Context, _ string, _ []byte) error {
	return nil
}

// LoadCheckpoint returns not found in batch mode.
func (n NoOpSessionManager) LoadCheckpoint(_ context.Context, _ string) ([]byte, error) {
	return nil, ErrCheckpointNotFound
}

// DeleteSession does nothing in batch mode.
func (n NoOpSessionManager) DeleteSession(_ context.Context, _ string) error {
	return nil
}

// Ensure NoOpSessionManager implements SessionManager.
var _ SessionManager = NoOpSessionManager{}

// SessionKey is the context key type for session IDs.
type sessionKeyType struct{}

// SessionKey is used to store/retrieve session IDs from context.
var SessionKey = sessionKeyType{}

// WithSessionID returns a context with the session ID attached.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, SessionKey, sessionID)
}

// GetSessionID extracts the session ID from context.
// Returns empty string if not present.
func GetSessionID(ctx context.Context) string {
	if v := ctx.Value(SessionKey); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// HistoryKey is the context key for message history.
type historyKeyType struct{}

// HistoryKey is used to store/retrieve message history from context.
var HistoryKey = historyKeyType{}

// WithHistory returns a context with message history attached.
func WithHistory(ctx context.Context, history []Message) context.Context {
	return context.WithValue(ctx, HistoryKey, history)
}

// GetHistory extracts message history from context.
// Returns nil if not present.
func GetHistory(ctx context.Context) []Message {
	if v := ctx.Value(HistoryKey); v != nil {
		if h, ok := v.([]Message); ok {
			return h
		}
	}
	return nil
}
