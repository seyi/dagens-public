package interaction

import (
	"context"
	"testing"
)

func TestMemorySessionManager_Basic(t *testing.T) {
	ctx := context.Background()
	mgr := NewMemorySessionManager()

	sessionID := "test-session-1"

	// Initially empty
	history, err := mgr.LoadHistory(ctx, sessionID)
	if err != nil {
		t.Fatalf("LoadHistory failed: %v", err)
	}
	if len(history) != 0 {
		t.Errorf("expected empty history, got %d messages", len(history))
	}

	// Append messages
	msgs := []Message{
		NewUserMessage("Hello"),
		NewAssistantMessage("Hi there!"),
	}
	if err := mgr.Append(ctx, sessionID, msgs...); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Load and verify
	history, err = mgr.LoadHistory(ctx, sessionID)
	if err != nil {
		t.Fatalf("LoadHistory failed: %v", err)
	}
	if len(history) != 2 {
		t.Errorf("expected 2 messages, got %d", len(history))
	}
	if history[0].Content != "Hello" {
		t.Errorf("unexpected first message: %s", history[0].Content)
	}

	// Verify returned slice is a copy (mutation safety)
	history[0].Content = "MUTATED"
	reloaded, _ := mgr.LoadHistory(ctx, sessionID)
	if reloaded[0].Content == "MUTATED" {
		t.Error("LoadHistory should return a copy, not the original slice")
	}
}

func TestMemorySessionManager_MultipleSession(t *testing.T) {
	ctx := context.Background()
	mgr := NewMemorySessionManager()

	// Append to different sessions
	mgr.Append(ctx, "session-a", NewUserMessage("Message A"))
	mgr.Append(ctx, "session-b", NewUserMessage("Message B"))

	historyA, _ := mgr.LoadHistory(ctx, "session-a")
	historyB, _ := mgr.LoadHistory(ctx, "session-b")

	if len(historyA) != 1 || historyA[0].Content != "Message A" {
		t.Error("session-a has incorrect history")
	}
	if len(historyB) != 1 || historyB[0].Content != "Message B" {
		t.Error("session-b has incorrect history")
	}
}

func TestMemorySessionManager_MaxHistory(t *testing.T) {
	ctx := context.Background()
	mgr := NewMemorySessionManager(WithMaxHistory(3))

	sessionID := "limited-session"

	// Append 5 messages
	for i := 0; i < 5; i++ {
		mgr.Append(ctx, sessionID, NewUserMessage("msg"))
	}

	history, _ := mgr.LoadHistory(ctx, sessionID)
	if len(history) != 3 {
		t.Errorf("expected 3 messages (max), got %d", len(history))
	}
}

func TestMemorySessionManager_Checkpoint(t *testing.T) {
	ctx := context.Background()
	mgr := NewMemorySessionManager()

	sessionID := "checkpoint-session"
	state := []byte(`{"step": 5, "status": "pending_approval"}`)

	// Save checkpoint
	if err := mgr.Checkpoint(ctx, sessionID, state); err != nil {
		t.Fatalf("Checkpoint failed: %v", err)
	}

	// Load checkpoint
	loaded, err := mgr.LoadCheckpoint(ctx, sessionID)
	if err != nil {
		t.Fatalf("LoadCheckpoint failed: %v", err)
	}
	if string(loaded) != string(state) {
		t.Errorf("checkpoint mismatch: got %s", string(loaded))
	}

	// Verify returned slice is a copy
	loaded[0] = 'X'
	reloaded, _ := mgr.LoadCheckpoint(ctx, sessionID)
	if reloaded[0] == 'X' {
		t.Error("LoadCheckpoint should return a copy")
	}
}

func TestMemorySessionManager_CheckpointNotFound(t *testing.T) {
	ctx := context.Background()
	mgr := NewMemorySessionManager()

	_, err := mgr.LoadCheckpoint(ctx, "nonexistent")
	if err != ErrCheckpointNotFound {
		t.Errorf("expected ErrCheckpointNotFound, got %v", err)
	}
}

func TestMemorySessionManager_DeleteSession(t *testing.T) {
	ctx := context.Background()
	mgr := NewMemorySessionManager()

	sessionID := "to-delete"

	mgr.Append(ctx, sessionID, NewUserMessage("Hello"))
	mgr.Checkpoint(ctx, sessionID, []byte("state"))

	// Verify data exists
	history, _ := mgr.LoadHistory(ctx, sessionID)
	if len(history) != 1 {
		t.Fatal("history should exist before delete")
	}

	// Delete
	if err := mgr.DeleteSession(ctx, sessionID); err != nil {
		t.Fatalf("DeleteSession failed: %v", err)
	}

	// Verify data is gone
	history, _ = mgr.LoadHistory(ctx, sessionID)
	if len(history) != 0 {
		t.Error("history should be empty after delete")
	}

	_, err := mgr.LoadCheckpoint(ctx, sessionID)
	if err != ErrCheckpointNotFound {
		t.Error("checkpoint should be gone after delete")
	}
}

func TestMemorySessionManager_SessionCount(t *testing.T) {
	ctx := context.Background()
	mgr := NewMemorySessionManager()

	if mgr.SessionCount() != 0 {
		t.Error("expected 0 sessions initially")
	}

	mgr.Append(ctx, "s1", NewUserMessage("a"))
	mgr.Append(ctx, "s2", NewUserMessage("b"))

	if mgr.SessionCount() != 2 {
		t.Errorf("expected 2 sessions, got %d", mgr.SessionCount())
	}

	mgr.Clear()
	if mgr.SessionCount() != 0 {
		t.Error("expected 0 sessions after Clear")
	}
}

func TestNoOpSessionManager(t *testing.T) {
	ctx := context.Background()
	mgr := NoOpSessionManager{}

	// All operations should succeed without error (except LoadCheckpoint)
	history, err := mgr.LoadHistory(ctx, "any")
	if err != nil || len(history) != 0 {
		t.Error("LoadHistory should return empty without error")
	}

	if err := mgr.Append(ctx, "any", NewUserMessage("msg")); err != nil {
		t.Error("Append should not error")
	}

	if err := mgr.Checkpoint(ctx, "any", []byte("state")); err != nil {
		t.Error("Checkpoint should not error")
	}

	_, err = mgr.LoadCheckpoint(ctx, "any")
	if err != ErrCheckpointNotFound {
		t.Error("LoadCheckpoint should return ErrCheckpointNotFound")
	}

	if err := mgr.DeleteSession(ctx, "any"); err != nil {
		t.Error("DeleteSession should not error")
	}
}
