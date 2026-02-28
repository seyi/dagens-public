package graph

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileStateStore_SaveLoadRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	store, err := NewFileStateStore(dir)
	if err != nil {
		t.Fatalf("NewFileStateStore error: %v", err)
	}

	snap := &StateSnapshot{
		Version:   2,
		Timestamp: time.Now().UTC(),
		Data: map[string]interface{}{
			"foo": "bar",
			"n":   42,
		},
		Metadata: map[string]interface{}{
			"meta": true,
		},
	}

	if err := store.Save(ctx, "exec-1", snap); err != nil {
		t.Fatalf("Save error: %v", err)
	}

	loaded, err := store.Load(ctx, "exec-1")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if loaded.Version != snap.Version {
		t.Fatalf("version mismatch: got %d want %d", loaded.Version, snap.Version)
	}
	if loaded.Data["foo"] != "bar" {
		t.Fatalf("data mismatch: %+v", loaded.Data)
	}
	if loaded.Data["n"] != float64(42) {
		t.Fatalf("number mismatch: %v", loaded.Data["n"])
	}
	if loaded.Metadata["meta"] != true {
		t.Fatalf("metadata mismatch: %+v", loaded.Metadata)
	}
}

func TestFileStateStore_ListExistsDeleteClear(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewFileStateStore(dir)
	if err != nil {
		t.Fatalf("NewFileStateStore error: %v", err)
	}

	snap := (&MemoryState{data: map[string]interface{}{"a": 1}, metadata: map[string]interface{}{}}).Snapshot()
	if err := store.Save(ctx, "exec-a", snap); err != nil {
		t.Fatalf("Save exec-a: %v", err)
	}
	if err := store.Save(ctx, "exec-b", snap); err != nil {
		t.Fatalf("Save exec-b: %v", err)
	}

	exists, err := store.Exists(ctx, "exec-a")
	if err != nil || !exists {
		t.Fatalf("Exists exec-a failed err=%v exists=%v", err, exists)
	}

	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(list), list)
	}

	if err := store.Delete(ctx, "exec-a"); err != nil {
		t.Fatalf("Delete exec-a: %v", err)
	}
	exists, err = store.Exists(ctx, "exec-a")
	if err != nil {
		t.Fatalf("Exists after delete err: %v", err)
	}
	if exists {
		t.Fatalf("exec-a should not exist after delete")
	}

	if err := store.Clear(ctx); err != nil {
		t.Fatalf("Clear error: %v", err)
	}
	list, err = store.List(ctx)
	if err != nil {
		t.Fatalf("List after clear err: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty after clear, got %v", list)
	}
}

func TestFileStateStore_InvalidData(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewFileStateStore(dir)
	if err != nil {
		t.Fatalf("NewFileStateStore error: %v", err)
	}

	// Write corrupted file
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{not-json"), 0o644); err != nil {
		t.Fatalf("write corrupted: %v", err)
	}

	if _, err := store.Load(ctx, "bad"); err == nil {
		t.Fatalf("expected error on corrupted snapshot")
	}
}
