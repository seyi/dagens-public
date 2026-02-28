package graph

import (
	"context"
	"testing"
	"time"
)

// Integration-style test: simulate an agent writing results to state,
// snapshot to disk, then restore into a fresh MemoryState.
func TestFileStateStore_AgentRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	store, err := NewFileStateStore(dir)
	if err != nil {
		t.Fatalf("NewFileStateStore error: %v", err)
	}

	// Simulate agent outputs written to state.
	orig := NewMemoryState()
	orig.Set("agents/echo/output", "Echo: hello")
	orig.Set("agents/summarizer/output", "Summary: 5 words")
	orig.Set("agents/classifier/score", 0.92)
	orig.SetMetadata("run_id", "run-123")

	// Snapshot and persist.
	snap := orig.Snapshot()
	if err := store.Save(ctx, "demo", snap); err != nil {
		t.Fatalf("Save snapshot: %v", err)
	}

	// Load into a new state and restore.
	loadedSnap, err := store.Load(ctx, "demo")
	if err != nil {
		t.Fatalf("Load snapshot: %v", err)
	}

	restored := NewMemoryState()
	restored.Restore(loadedSnap)

	// Validate data persisted.
	if v, ok := restored.Get("agents/echo/output"); !ok || v.(string) != "Echo: hello" {
		t.Fatalf("echo output mismatch: %v", v)
	}
	if v, ok := restored.Get("agents/summarizer/output"); !ok || v.(string) != "Summary: 5 words" {
		t.Fatalf("summarizer output mismatch: %v", v)
	}
	if v, ok := restored.Get("agents/classifier/score"); !ok || v.(float64) != 0.92 {
		t.Fatalf("classifier score mismatch: %v", v)
	}
	if m, ok := restored.GetMetadata("run_id"); !ok || m.(string) != "run-123" {
		t.Fatalf("metadata run_id mismatch: %v", m)
	}

	// Ensure version carried over.
	if restored.Version() != snap.Version {
		t.Fatalf("version mismatch: got %d want %d", restored.Version(), snap.Version)
	}

	// Ensure timestamp preserved (within sane bounds).
	if snap.Timestamp.IsZero() || time.Since(snap.Timestamp) < 0 {
		t.Fatalf("snapshot timestamp invalid: %v", snap.Timestamp)
	}
}
