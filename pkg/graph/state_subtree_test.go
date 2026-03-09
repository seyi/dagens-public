package graph

import (
	"testing"
)

func subtreeAsMap(t *testing.T, s State) map[string]interface{} {
	t.Helper()
	out := map[string]interface{}{}
	for _, key := range s.Keys() {
		v, ok := s.Get(key)
		if !ok {
			continue
		}
		out[key] = v
	}
	return out
}

func TestSubtree_Basic(t *testing.T) {
	s := NewMemoryState()
	s.Set("root/a", 1)
	s.Set("root/b", "v")
	s.Set("other/x", 99)

	sub := subtreeAsMap(t, s.Subtree("root/"))
	if len(sub) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(sub), sub)
	}
	if sub["a"] != 1 || sub["b"] != "v" {
		t.Fatalf("unexpected subtree values: %+v", sub)
	}
	if _, ok := sub["root/a"]; ok {
		t.Fatalf("prefix not stripped")
	}
}

func TestSubtree_NoMatch(t *testing.T) {
	s := NewMemoryState()
	s.Set("a", 1)
	sub := subtreeAsMap(t, s.Subtree("none/"))
	if len(sub) != 0 {
		t.Fatalf("expected empty subtree, got %+v", sub)
	}
}

func TestSubtree_Immutability(t *testing.T) {
	s := NewMemoryState()
	s.Set("root/list", []int{1, 2, 3})
	sub := subtreeAsMap(t, s.Subtree("root/"))

	list := sub["list"].([]int)
	list[0] = 99

	orig, _ := s.Get("root/list")
	if orig.([]int)[0] == 99 {
		t.Fatalf("mutation of subtree result affected original state")
	}
}

func TestSubtree_AfterRestore(t *testing.T) {
	s := NewMemoryState()
	s.Set("root/a", "keep")
	s.Set("root/b", "also")

	snap := s.Snapshot()
	restored := NewMemoryState()
	restored.Restore(snap)

	sub := subtreeAsMap(t, restored.Subtree("root/"))
	if len(sub) != 2 || sub["a"] != "keep" || sub["b"] != "also" {
		t.Fatalf("unexpected subtree after restore: %+v", sub)
	}
}

func TestSubtree_EdgeCasesAndNestedPrefixes(t *testing.T) {
	s := NewMemoryState()
	// Setup nested data with potential prefix overlap
	s.Set("app/v1/config", 1)
	s.Set("app/v1/db", 2)
	s.Set("apple/v1/config", 3) // Prefix overlap edge case

	tests := []struct {
		name     string
		prefix   string
		wantKeys []string // Expected keys in result
	}{
		{
			name:     "Standard directory with trailing slash",
			prefix:   "app/v1/",
			wantKeys: []string{"config", "db"},
		},
		{
			name:     "Partial prefix overlap (ambiguous without separator)",
			prefix:   "app",
			wantKeys: []string{"/v1/config", "/v1/db", "le/v1/config"},
		},
		{
			name:     "Empty prefix (all items)",
			prefix:   "",
			wantKeys: []string{"app/v1/config", "app/v1/db", "apple/v1/config"},
		},
		{
			name:     "Exact match",
			prefix:   "app/v1/db",
			wantKeys: []string{""}, // Key becomes empty string
		},
		{
			name:     "No matches",
			prefix:   "nonexistent/",
			wantKeys: []string{}, // Empty result
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := subtreeAsMap(t, s.Subtree(tt.prefix))
			if len(got) != len(tt.wantKeys) {
				t.Errorf("Subtree(%q) returned %d keys, want %d", tt.prefix, len(got), len(tt.wantKeys))
			}
			for _, key := range tt.wantKeys {
				if _, ok := got[key]; !ok {
					t.Errorf("Subtree(%q) missing expected key: %q. Got: %v", tt.prefix, key, got)
				}
			}
		})
	}
}

func TestSubtree_DeeplyNested(t *testing.T) {
	s := NewMemoryState()
	s.Set("services/auth/config/db/host", "localhost")
	s.Set("services/auth/config/db/port", 5432)
	s.Set("services/auth/config/cache/ttl", 60)
	s.Set("services/api/config/port", 8080)

	// Test deeply nested extraction
	sub := subtreeAsMap(t, s.Subtree("services/auth/config/"))
	if len(sub) != 3 {
		t.Fatalf("expected 3 entries, got %d: %+v", len(sub), sub)
	}

	expected := map[string]interface{}{
		"db/host":   "localhost",
		"db/port":   5432,
		"cache/ttl": 60,
	}

	for k, want := range expected {
		got, ok := sub[k]
		if !ok {
			t.Errorf("missing key %q in subtree", k)
			continue
		}
		if got != want {
			t.Errorf("key %q: got %v, want %v", k, got, want)
		}
	}

	// Test even deeper nesting
	dbSub := subtreeAsMap(t, s.Subtree("services/auth/config/db/"))
	if len(dbSub) != 2 {
		t.Fatalf("expected 2 db entries, got %d: %+v", len(dbSub), dbSub)
	}
	if dbSub["host"] != "localhost" || dbSub["port"] != 5432 {
		t.Errorf("unexpected db subtree: %+v", dbSub)
	}
}
