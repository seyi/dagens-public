package interaction

import (
	"context"
	"testing"
)

func TestWithSessionID(t *testing.T) {
	ctx := context.Background()

	// Initially empty
	if id := GetSessionID(ctx); id != "" {
		t.Errorf("expected empty session ID, got %s", id)
	}

	// Set and retrieve
	ctx = WithSessionID(ctx, "session-123")
	if id := GetSessionID(ctx); id != "session-123" {
		t.Errorf("expected 'session-123', got '%s'", id)
	}
}

func TestWithHistory(t *testing.T) {
	ctx := context.Background()

	// Initially nil
	if history := GetHistory(ctx); history != nil {
		t.Error("expected nil history initially")
	}

	// Set and retrieve
	msgs := []Message{
		NewUserMessage("Hello"),
		NewAssistantMessage("Hi"),
	}
	ctx = WithHistory(ctx, msgs)

	history := GetHistory(ctx)
	if len(history) != 2 {
		t.Errorf("expected 2 messages, got %d", len(history))
	}
	if history[0].Role != RoleUser {
		t.Error("first message should be user role")
	}
}

func TestContextHelpers_ChainedCalls(t *testing.T) {
	ctx := context.Background()

	// Chain multiple context modifications
	ctx = WithSessionID(ctx, "my-session")
	ctx = WithHistory(ctx, []Message{NewUserMessage("test")})

	// Both should be retrievable
	if GetSessionID(ctx) != "my-session" {
		t.Error("session ID lost after adding history")
	}
	if len(GetHistory(ctx)) != 1 {
		t.Error("history lost after adding session ID")
	}
}
