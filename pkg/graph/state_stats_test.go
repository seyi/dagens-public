package graph

import "testing"

func TestMemoryState_Stats_Increments(t *testing.T) {
	state := NewMemoryState()

	state.Set("immutable", "x")
	state.Set("mutable", map[string]interface{}{"k": "v"})

	if _, ok := state.Get("immutable"); !ok {
		t.Fatal("expected immutable key")
	}
	if _, ok := state.Get("mutable"); !ok {
		t.Fatal("expected mutable key")
	}

	stats := state.Stats()
	if stats.Sets != 2 {
		t.Fatalf("sets = %d, want 2", stats.Sets)
	}
	if stats.Gets != 2 {
		t.Fatalf("gets = %d, want 2", stats.Gets)
	}
	if stats.CacheHits < 1 {
		t.Fatalf("cache_hits = %d, want >=1", stats.CacheHits)
	}
	if stats.Copies < 1 {
		t.Fatalf("copies = %d, want >=1", stats.Copies)
	}
}
