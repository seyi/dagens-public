package state

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStateBackend_GetSet(t *testing.T) {
	backend := NewMemoryStateBackend()
	defer backend.Close()

	ctx := context.Background()

	// Test Set and Get
	err := backend.Set(ctx, "test-key", []byte("test-value"), 0)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	value, err := backend.Get(ctx, "test-key")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if string(value) != "test-value" {
		t.Errorf("Expected 'test-value', got '%s'", string(value))
	}
}

func TestMemoryStateBackend_NotFound(t *testing.T) {
	backend := NewMemoryStateBackend()
	defer backend.Close()

	ctx := context.Background()

	_, err := backend.Get(ctx, "nonexistent")
	if err != ErrNotFound {
		t.Errorf("Expected ErrNotFound, got %v", err)
	}
}

func TestMemoryStateBackend_Delete(t *testing.T) {
	backend := NewMemoryStateBackend()
	defer backend.Close()

	ctx := context.Background()

	backend.Set(ctx, "key-to-delete", []byte("value"), 0)
	err := backend.Delete(ctx, "key-to-delete")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = backend.Get(ctx, "key-to-delete")
	if err != ErrNotFound {
		t.Errorf("Expected ErrNotFound after delete, got %v", err)
	}
}

func TestMemoryStateBackend_TTL(t *testing.T) {
	backend := NewMemoryStateBackend()
	defer backend.Close()

	ctx := context.Background()

	// Set with short TTL
	err := backend.Set(ctx, "expiring-key", []byte("value"), 50*time.Millisecond)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Should exist immediately
	_, err = backend.Get(ctx, "expiring-key")
	if err != nil {
		t.Fatalf("Get should succeed immediately: %v", err)
	}

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Should be expired now
	_, err = backend.Get(ctx, "expiring-key")
	if err != ErrNotFound {
		t.Errorf("Expected ErrNotFound after TTL, got %v", err)
	}
}

func TestMemoryStateBackend_List(t *testing.T) {
	backend := NewMemoryStateBackend()
	defer backend.Close()

	ctx := context.Background()

	// Add several keys
	backend.Set(ctx, "sessions:1", []byte("s1"), 0)
	backend.Set(ctx, "sessions:2", []byte("s2"), 0)
	backend.Set(ctx, "agents:1", []byte("a1"), 0)

	// List with prefix pattern
	keys, err := backend.List(ctx, "sessions:*")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(keys) != 2 {
		t.Errorf("Expected 2 keys, got %d", len(keys))
	}

	// List all
	keys, err = backend.List(ctx, "*")
	if err != nil {
		t.Fatalf("List all failed: %v", err)
	}

	if len(keys) != 3 {
		t.Errorf("Expected 3 keys, got %d", len(keys))
	}
}

func TestMemoryStateBackend_Closed(t *testing.T) {
	backend := NewMemoryStateBackend()
	backend.Close()

	ctx := context.Background()

	_, err := backend.Get(ctx, "key")
	if err != ErrBackendClosed {
		t.Errorf("Expected ErrBackendClosed, got %v", err)
	}

	err = backend.Set(ctx, "key", []byte("value"), 0)
	if err != ErrBackendClosed {
		t.Errorf("Expected ErrBackendClosed, got %v", err)
	}
}

func TestMemoryCheckpointBackend_SaveLoad(t *testing.T) {
	backend := NewMemoryCheckpointBackend()
	ctx := context.Background()

	checkpoint := &Checkpoint{
		ID:    "cp-1",
		JobID: "agent-1",
		State: map[string]interface{}{"key": "value"},
	}

	err := backend.Save(ctx, checkpoint)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := backend.Load(ctx, "cp-1")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.ID != "cp-1" {
		t.Errorf("Expected ID 'cp-1', got '%s'", loaded.ID)
	}
}

func TestMemoryCheckpointBackend_GetLatest(t *testing.T) {
	backend := NewMemoryCheckpointBackend()
	ctx := context.Background()

	// Save multiple checkpoints
	backend.Save(ctx, &Checkpoint{ID: "cp-1", JobID: "agent-1"})
	time.Sleep(10 * time.Millisecond) // Ensure different timestamps
	backend.Save(ctx, &Checkpoint{ID: "cp-2", JobID: "agent-1"})
	time.Sleep(10 * time.Millisecond)
	backend.Save(ctx, &Checkpoint{ID: "cp-3", JobID: "agent-1"})

	latest, err := backend.GetLatest(ctx, "agent-1")
	if err != nil {
		t.Fatalf("GetLatest failed: %v", err)
	}

	if latest.ID != "cp-3" {
		t.Errorf("Expected latest to be 'cp-3', got '%s'", latest.ID)
	}
}

func TestMemoryCheckpointBackend_ListByAgent(t *testing.T) {
	backend := NewMemoryCheckpointBackend()
	ctx := context.Background()

	backend.Save(ctx, &Checkpoint{ID: "cp-1", JobID: "agent-1"})
	backend.Save(ctx, &Checkpoint{ID: "cp-2", JobID: "agent-1"})
	backend.Save(ctx, &Checkpoint{ID: "cp-3", JobID: "agent-2"})

	metas, err := backend.ListByAgent(ctx, "agent-1", 10)
	if err != nil {
		t.Fatalf("ListByAgent failed: %v", err)
	}

	if len(metas) != 2 {
		t.Errorf("Expected 2 checkpoints for agent-1, got %d", len(metas))
	}
}

func TestMemorySessionBackend_CRUD(t *testing.T) {
	backend := NewMemorySessionBackend()
	ctx := context.Background()

	session := &SessionState{
		ID:      "session-1",
		AgentID: "agent-1",
		State:   map[string]interface{}{"counter": 0},
	}

	// Create
	err := backend.Create(ctx, session)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Create duplicate should fail
	err = backend.Create(ctx, session)
	if err != ErrAlreadyExists {
		t.Errorf("Expected ErrAlreadyExists, got %v", err)
	}

	// Get
	retrieved, err := backend.Get(ctx, "session-1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if retrieved.ID != "session-1" {
		t.Errorf("Expected ID 'session-1', got '%s'", retrieved.ID)
	}

	// Update
	retrieved.State["counter"] = 1
	err = backend.Update(ctx, retrieved)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// Verify update
	updated, _ := backend.Get(ctx, "session-1")
	if updated.Version != 2 {
		t.Errorf("Expected version 2, got %d", updated.Version)
	}

	// Delete
	err = backend.Delete(ctx, "session-1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = backend.Get(ctx, "session-1")
	if err != ErrNotFound {
		t.Errorf("Expected ErrNotFound after delete, got %v", err)
	}
}

func TestMemorySessionBackend_OptimisticLocking(t *testing.T) {
	backend := NewMemorySessionBackend()
	ctx := context.Background()

	session := &SessionState{
		ID:      "session-1",
		AgentID: "agent-1",
	}

	backend.Create(ctx, session)

	// Get two copies
	copy1, _ := backend.Get(ctx, "session-1")
	copy2, _ := backend.Get(ctx, "session-1")

	// Update first copy
	err := backend.Update(ctx, copy1)
	if err != nil {
		t.Fatalf("First update failed: %v", err)
	}

	// Second update should fail (version conflict)
	err = backend.Update(ctx, copy2)
	if err != ErrVersionConflict {
		t.Errorf("Expected ErrVersionConflict, got %v", err)
	}
}

func TestMemoryHistoryBackend(t *testing.T) {
	backend := NewMemoryHistoryBackend()
	ctx := context.Background()

	// Append entries
	for i := 0; i < 5; i++ {
		entry := &HistoryEntry{
			SessionID: "session-1",
			Action:    "action",
		}
		err := backend.Append(ctx, entry)
		if err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	// List all
	entries, err := backend.List(ctx, "session-1", 0)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(entries) != 5 {
		t.Errorf("Expected 5 entries, got %d", len(entries))
	}

	// List with limit
	entries, err = backend.List(ctx, "session-1", 3)
	if err != nil {
		t.Fatalf("List with limit failed: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("Expected 3 entries, got %d", len(entries))
	}

	// Clear
	err = backend.Clear(ctx, "session-1")
	if err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	entries, _ = backend.List(ctx, "session-1", 0)
	if len(entries) != 0 {
		t.Errorf("Expected 0 entries after clear, got %d", len(entries))
	}
}

func TestCompositeStateStore(t *testing.T) {
	store := NewMemoryStateStore()
	defer store.Close()

	if store.State == nil {
		t.Error("State backend is nil")
	}
	if store.Checkpoint == nil {
		t.Error("Checkpoint backend is nil")
	}
	if store.Session == nil {
		t.Error("Session backend is nil")
	}
	if store.History == nil {
		t.Error("History backend is nil")
	}
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		pattern  string
		key      string
		expected bool
	}{
		{"*", "anything", true},
		{"prefix:*", "prefix:key", true},
		{"prefix:*", "other:key", false},
		{"*:suffix", "key:suffix", true},
		{"*:suffix", "key:other", false},
		{"exact", "exact", true},
		{"exact", "different", false},
	}

	for _, tt := range tests {
		result := matchPattern(tt.pattern, tt.key)
		if result != tt.expected {
			t.Errorf("matchPattern(%q, %q) = %v, expected %v", tt.pattern, tt.key, result, tt.expected)
		}
	}
}

func TestSerializableState(t *testing.T) {
	state := &SerializableState{
		Key:       "test-key",
		Value:     map[string]string{"nested": "value"},
		Metadata:  map[string]string{"meta": "data"},
		CreatedAt: time.Now(),
		Version:   1,
	}

	data, err := state.Serialize()
	if err != nil {
		t.Fatalf("Serialize failed: %v", err)
	}

	loaded := &SerializableState{}
	err = loaded.Deserialize(data)
	if err != nil {
		t.Fatalf("Deserialize failed: %v", err)
	}

	if loaded.Key != "test-key" {
		t.Errorf("Expected Key 'test-key', got '%s'", loaded.Key)
	}
}
