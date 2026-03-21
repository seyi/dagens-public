package graph

import (
	"testing"
)

func iterateByPrefix(s *MemoryState, prefix string) map[string]interface{} {
	result := make(map[string]interface{})
	for _, key := range s.KeysWithPrefix(prefix) {
		if value, ok := s.Get(key); ok {
			result[key] = value
		}
	}
	if prefix == "" {
		for _, key := range s.Keys() {
			if value, ok := s.Get(key); ok {
				result[key] = value
			}
		}
	}
	return result
}

func TestIterate_Basic(t *testing.T) {
	s := NewMemoryState()
	s.Set("foo/1", 1)
	s.Set("foo/2", 2)
	s.Set("bar/1", 3)

	res := iterateByPrefix(s, "foo/")
	if len(res) != 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(res), res)
	}
	if res["foo/1"] != 1 || res["foo/2"] != 2 {
		t.Fatalf("unexpected values: %+v", res)
	}
}

func TestIterate_EmptyPrefix(t *testing.T) {
	s := NewMemoryState()
	s.Set("a", 1)
	s.Set("b", 2)
	res := iterateByPrefix(s, "")
	if len(res) != 2 {
		t.Fatalf("empty prefix should return all keys, got %d", len(res))
	}
}

func TestIterate_Immutability(t *testing.T) {
	s := NewMemoryState()
	s.Set("foo", []int{1, 2, 3})

	res := iterateByPrefix(s, "foo")
	slice := res["foo"].([]int)
	slice[0] = 99

	// original state should remain unchanged
	orig, _ := s.Get("foo")
	if orig.([]int)[0] == 99 {
		t.Fatalf("mutating iterate result affected original state")
	}
}

func TestKeysWithPrefix_Sorted(t *testing.T) {
	s := NewMemoryState()
	s.Set("p/b", 1)
	s.Set("p/a", 1)
	s.Set("p/c", 1)

	keys := s.KeysWithPrefix("p/")
	want := []string{"p/a", "p/b", "p/c"}
	if len(keys) != len(want) {
		t.Fatalf("expected %d keys, got %d", len(want), len(keys))
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Fatalf("unsorted keys: got %v want %v", keys, want)
		}
	}
}

func TestKeysWithPrefix_NoMatch(t *testing.T) {
	s := NewMemoryState()
	s.Set("x", 1)
	keys := s.KeysWithPrefix("none")
	if len(keys) != 0 {
		t.Fatalf("expected no matches, got %v", keys)
	}
}

func TestDeletePrefix_CountAndVersion(t *testing.T) {
	s := NewMemoryState()
	// initial version starts at 1; each Set increments
	s.Set("keep", 1)      // v2
	s.Set("foo/1", 1)     // v3
	s.Set("foo/2", 2)     // v4
	s.Set("foo/bar/3", 3) // v5
	origVersion := s.Version()

	deleted := s.DeletePrefix("foo/")
	if deleted != 3 {
		t.Fatalf("expected 3 deletions, got %d", deleted)
	}
	if exists := len(iterateByPrefix(s, "foo/")); exists != 0 {
		t.Fatalf("expected no foo/* entries remaining")
	}
	if s.Version() != origVersion+1 { // DeletePrefix increments once when any deletion occurred
		t.Fatalf("version not incremented correctly: got %d want %d", s.Version(), origVersion+1)
	}
}
