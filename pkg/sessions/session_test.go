package sessions

import (
	"context"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/agent"
)

func TestMemorySessionManager(t *testing.T) {
	manager := NewMemorySessionManager()
	ctx := context.Background()

	t.Run("Create Session", func(t *testing.T) {
		session, err := manager.CreateSession(ctx, "agent-1")
		if err != nil {
			t.Fatalf("Failed to create session: %v", err)
		}

		if session.ID == "" {
			t.Error("Expected non-empty session ID")
		}

		if session.AgentID != "agent-1" {
			t.Errorf("Expected agent ID 'agent-1', got '%s'", session.AgentID)
		}

		if len(session.History) != 0 {
			t.Error("Expected empty history for new session")
		}
	})

	t.Run("Get Session", func(t *testing.T) {
		session, _ := manager.CreateSession(ctx, "agent-2")

		retrieved, err := manager.GetSession(ctx, session.ID)
		if err != nil {
			t.Fatalf("Failed to get session: %v", err)
		}

		if retrieved.ID != session.ID {
			t.Errorf("Expected session ID '%s', got '%s'", session.ID, retrieved.ID)
		}
	})

	t.Run("Get Non-Existent Session", func(t *testing.T) {
		_, err := manager.GetSession(ctx, "non-existent")
		if err == nil {
			t.Error("Expected error for non-existent session")
		}
	})

	t.Run("Add Invocation", func(t *testing.T) {
		session, _ := manager.CreateSession(ctx, "agent-3")

		invocation := &Invocation{
			ID: "inv-1",
			Input: &agent.AgentInput{
				Instruction: "test instruction",
			},
			Output: &agent.AgentOutput{
				TaskID: "task-1",
			},
		}

		err := manager.AddInvocation(ctx, session.ID, invocation)
		if err != nil {
			t.Fatalf("Failed to add invocation: %v", err)
		}

		history, _ := manager.GetHistory(ctx, session.ID)
		if len(history) != 1 {
			t.Errorf("Expected 1 invocation, got %d", len(history))
		}

		if history[0].Index != 0 {
			t.Errorf("Expected index 0, got %d", history[0].Index)
		}
	})

	t.Run("Multiple Invocations", func(t *testing.T) {
		session, _ := manager.CreateSession(ctx, "agent-4")

		for i := 0; i < 5; i++ {
			invocation := &Invocation{
				ID: "inv-" + string(rune(i)),
				Input: &agent.AgentInput{
					Instruction: "test",
				},
			}
			manager.AddInvocation(ctx, session.ID, invocation)
		}

		history, _ := manager.GetHistory(ctx, session.ID)
		if len(history) != 5 {
			t.Errorf("Expected 5 invocations, got %d", len(history))
		}
	})

	t.Run("Session Rewind", func(t *testing.T) {
		session, _ := manager.CreateSession(ctx, "agent-5")

		// Add 3 invocations
		invocationIDs := []string{"inv-1", "inv-2", "inv-3"}
		for _, id := range invocationIDs {
			inv := &Invocation{
				ID: id,
				Input: &agent.AgentInput{
					Instruction: "test",
				},
				State: map[string]interface{}{
					"step": id,
				},
			}
			manager.AddInvocation(ctx, session.ID, inv)
		}

		// Rewind to inv-1
		err := manager.Rewind(ctx, session.ID, "inv-1")
		if err != nil {
			t.Fatalf("Failed to rewind: %v", err)
		}

		history, _ := manager.GetHistory(ctx, session.ID)
		if len(history) != 1 {
			t.Errorf("Expected 1 invocation after rewind, got %d", len(history))
		}
	})

	t.Run("Checkpoint and Restore", func(t *testing.T) {
		session, _ := manager.CreateSession(ctx, "agent-6")

		// Add some invocations
		for i := 0; i < 3; i++ {
			inv := &Invocation{
				ID:    "inv-" + string(rune(i)),
				Input: &agent.AgentInput{},
			}
			manager.AddInvocation(ctx, session.ID, inv)
		}

		// Create checkpoint
		checkpoint, err := manager.Checkpoint(ctx, session.ID, "test checkpoint")
		if err != nil {
			t.Fatalf("Failed to create checkpoint: %v", err)
		}

		if checkpoint.Description != "test checkpoint" {
			t.Errorf("Expected description 'test checkpoint', got '%s'", checkpoint.Description)
		}

		// Add more invocations
		for i := 3; i < 5; i++ {
			inv := &Invocation{
				ID:    "inv-" + string(rune(i)),
				Input: &agent.AgentInput{},
			}
			manager.AddInvocation(ctx, session.ID, inv)
		}

		// Restore checkpoint
		err = manager.RestoreCheckpoint(ctx, session.ID, checkpoint.ID)
		if err != nil {
			t.Fatalf("Failed to restore checkpoint: %v", err)
		}

		history, _ := manager.GetHistory(ctx, session.ID)
		if len(history) != 3 {
			t.Errorf("Expected 3 invocations after restore, got %d", len(history))
		}
	})

	t.Run("Update Context", func(t *testing.T) {
		session, _ := manager.CreateSession(ctx, "agent-7")

		updates := map[string]interface{}{
			"key1": "value1",
			"key2": 42,
		}

		err := manager.UpdateContext(ctx, session.ID, updates)
		if err != nil {
			t.Fatalf("Failed to update context: %v", err)
		}

		retrieved, _ := manager.GetSession(ctx, session.ID)
		if retrieved.Context["key1"] != "value1" {
			t.Error("Context not updated correctly")
		}
	})

	t.Run("Delete Session", func(t *testing.T) {
		session, _ := manager.CreateSession(ctx, "agent-8")

		err := manager.DeleteSession(ctx, session.ID)
		if err != nil {
			t.Fatalf("Failed to delete session: %v", err)
		}

		_, err = manager.GetSession(ctx, session.ID)
		if err == nil {
			t.Error("Expected error when getting deleted session")
		}
	})

	t.Run("List Sessions", func(t *testing.T) {
		agentID := "agent-9"

		// Create multiple sessions for same agent
		for i := 0; i < 3; i++ {
			manager.CreateSession(ctx, agentID)
		}

		sessions, err := manager.ListSessions(ctx, agentID)
		if err != nil {
			t.Fatalf("Failed to list sessions: %v", err)
		}

		if len(sessions) < 3 {
			t.Errorf("Expected at least 3 sessions, got %d", len(sessions))
		}
	})
}

func TestSessionTimestamps(t *testing.T) {
	manager := NewMemorySessionManager()
	ctx := context.Background()

	session, _ := manager.CreateSession(ctx, "agent-1")

	if session.CreatedAt.IsZero() {
		t.Error("Expected non-zero created timestamp")
	}

	if session.LastAccess.IsZero() {
		t.Error("Expected non-zero last access timestamp")
	}

	time.Sleep(10 * time.Millisecond)

	// Access should update LastAccess
	retrieved, _ := manager.GetSession(ctx, session.ID)
	if retrieved.LastAccess.Before(session.LastAccess) {
		t.Error("Expected LastAccess to be updated or same")
	}
}

func TestConcurrentSessionAccess(t *testing.T) {
	manager := NewMemorySessionManager()
	ctx := context.Background()

	session, _ := manager.CreateSession(ctx, "agent-concurrent")

	const numGoroutines = 10
	errors := make(chan error, numGoroutines)

	// Concurrent invocation additions
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			inv := &Invocation{
				ID:    "inv-" + string(rune(idx)),
				Input: &agent.AgentInput{},
			}
			errors <- manager.AddInvocation(ctx, session.ID, inv)
		}(i)
	}

	for i := 0; i < numGoroutines; i++ {
		if err := <-errors; err != nil {
			t.Errorf("Concurrent invocation failed: %v", err)
		}
	}

	history, _ := manager.GetHistory(ctx, session.ID)
	if len(history) != numGoroutines {
		t.Errorf("Expected %d invocations, got %d", numGoroutines, len(history))
	}
}

func TestSessionContext(t *testing.T) {
	manager := NewMemorySessionManager()
	ctx := context.Background()

	session, _ := manager.CreateSession(ctx, "agent-context")

	t.Run("Empty Context Initially", func(t *testing.T) {
		if len(session.Context) != 0 {
			t.Error("Expected empty context")
		}
	})

	t.Run("Update Complex Context", func(t *testing.T) {
		updates := map[string]interface{}{
			"string":  "value",
			"int":     123,
			"float":   45.67,
			"bool":    true,
			"nested": map[string]interface{}{
				"key": "nested value",
			},
		}

		err := manager.UpdateContext(ctx, session.ID, updates)
		if err != nil {
			t.Fatalf("Failed to update context: %v", err)
		}

		retrieved, _ := manager.GetSession(ctx, session.ID)

		if retrieved.Context["string"] != "value" {
			t.Error("String value not stored correctly")
		}

		if retrieved.Context["int"] != 123 {
			t.Error("Int value not stored correctly")
		}
	})
}

func TestInvocationMetadata(t *testing.T) {
	manager := NewMemorySessionManager()
	ctx := context.Background()

	session, _ := manager.CreateSession(ctx, "agent-meta")

	invocation := &Invocation{
		ID: "inv-with-meta",
		Input: &agent.AgentInput{
			Instruction: "test",
		},
		Output: &agent.AgentOutput{
			TaskID: "task-1",
			Metadata: map[string]interface{}{
				"tokens": 100,
				"model":  "gpt-4",
			},
		},
		State: map[string]interface{}{
			"step": 1,
		},
	}

	err := manager.AddInvocation(ctx, session.ID, invocation)
	if err != nil {
		t.Fatalf("Failed to add invocation: %v", err)
	}

	history, _ := manager.GetHistory(ctx, session.ID)
	retrieved := history[0]

	if retrieved.Output.Metadata["tokens"] != 100 {
		t.Error("Metadata not preserved")
	}

	if retrieved.State["step"] != 1 {
		t.Error("State not preserved")
	}
}
