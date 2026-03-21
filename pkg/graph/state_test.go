package graph

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
)

// ============================================
// Phase 2A: Serialization Tests
// ============================================

func TestMemoryState_MarshalUnmarshal_RoundTrip(t *testing.T) {
	state := NewMemoryState()
	state.Set("user.name", "Alice")
	state.Set("user.age", 30)
	state.Set("user.tags", []string{"admin", "active"})
	state.SetMetadata("env", "test")

	payload, err := state.Marshal()
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	restored := NewMemoryState()
	if err := restored.Unmarshal(payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	// Verify data
	if got, ok := restored.Get("user.name"); !ok || got != "Alice" {
		t.Fatalf("expected user.name to be Alice, got %v", got)
	}

	if got, ok := restored.Get("user.age"); !ok {
		t.Fatalf("expected user.age to exist")
	} else {
		// JSON unmarshals numbers as float64
		if age, ok := got.(float64); !ok || age != 30 {
			t.Fatalf("expected user.age to be 30, got %v (%T)", got, got)
		}
	}

	// Verify metadata
	if meta, ok := restored.GetMetadata("env"); !ok || meta != "test" {
		t.Fatalf("expected env metadata to be test, got %v", meta)
	}

	// Verify version preserved
	if state.Version() != restored.Version() {
		t.Fatalf("expected version %d, got %d", state.Version(), restored.Version())
	}
}

func TestMemoryState_Unmarshal_EmptyPayload(t *testing.T) {
	state := NewMemoryState()

	if err := state.Unmarshal(nil); err == nil {
		t.Fatal("expected error for nil payload")
	}

	if err := state.Unmarshal([]byte{}); err == nil {
		t.Fatal("expected error for empty payload")
	}
}

func TestMemoryState_Unmarshal_InvalidJSON(t *testing.T) {
	state := NewMemoryState()
	if err := state.Unmarshal([]byte("not json")); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestMemoryState_JSONMarshaler_Interface(t *testing.T) {
	state := NewMemoryState()
	state.Set("key", "value")

	// Test that it implements json.Marshaler
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	// Verify the output is valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	if _, ok := parsed["data"]; !ok {
		t.Fatal("expected 'data' field in JSON output")
	}
}

// ============================================
// Phase 2A: Scope Iteration Tests
// ============================================

func TestMemoryState_Iterate(t *testing.T) {
	state := NewMemoryState()
	state.Set("agent.memory.fact1", "Go is great")
	state.Set("agent.memory.fact2", "Rust is fast")
	state.Set("agent.plan", "deploy")
	state.Set("user.name", "Bob")

	scoped := iterateByPrefix(state, "agent.memory.")
	if len(scoped) != 2 {
		t.Fatalf("expected 2 scoped entries, got %d", len(scoped))
	}

	if _, ok := scoped["agent.memory.fact1"]; !ok {
		t.Fatal("expected agent.memory.fact1 in result")
	}
	if _, ok := scoped["agent.memory.fact2"]; !ok {
		t.Fatal("expected agent.memory.fact2 in result")
	}
}

func TestMemoryState_Iterate_DeepCopy(t *testing.T) {
	state := NewMemoryState()
	state.Set("key", map[string]interface{}{"nested": "value"})

	scoped := iterateByPrefix(state, "key")

	// Mutate the returned map
	if nested, ok := scoped["key"].(map[string]interface{}); ok {
		nested["nested"] = "mutated"
	}

	// Verify internal state wasn't affected
	val, _ := state.Get("key")
	if nested, ok := val.(map[string]interface{}); ok {
		if nested["nested"] != "value" {
			t.Fatal("iterate should return deep copy - internal state was mutated")
		}
	}
}

func TestMemoryState_Iterate_EmptyPrefix(t *testing.T) {
	state := NewMemoryState()
	state.Set("a", 1)
	state.Set("b", 2)

	// Empty prefix should return all keys
	all := iterateByPrefix(state, "")
	if len(all) != 2 {
		t.Fatalf("expected 2 entries with empty prefix, got %d", len(all))
	}
}

func TestMemoryState_KeysWithPrefix(t *testing.T) {
	state := NewMemoryState()
	state.Set("agent.memory.fact1", "Go")
	state.Set("agent.memory.fact2", "Rust")
	state.Set("agent.plan", "deploy")
	state.Set("user.name", "Bob")

	keys := state.KeysWithPrefix("agent.")
	expected := []string{"agent.memory.fact1", "agent.memory.fact2", "agent.plan"}

	if !reflect.DeepEqual(expected, keys) {
		t.Fatalf("unexpected keys: got %v, want %v", keys, expected)
	}
}

func TestMemoryState_KeysWithPrefix_Sorted(t *testing.T) {
	state := NewMemoryState()
	state.Set("z.key", 1)
	state.Set("a.key", 2)
	state.Set("m.key", 3)

	keys := state.KeysWithPrefix("")
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}

	// Verify sorted order
	for i := 1; i < len(keys); i++ {
		if keys[i-1] > keys[i] {
			t.Fatalf("keys not sorted: %v", keys)
		}
	}
}

func TestMemoryState_DeletePrefix(t *testing.T) {
	state := NewMemoryState()
	state.Set("agent.memory.fact1", "Go")
	state.Set("agent.memory.fact2", "Rust")
	state.Set("agent.plan", "deploy")
	state.Set("user.name", "Bob")

	prevVersion := state.Version()

	deleted := state.DeletePrefix("agent.memory.")
	if deleted != 2 {
		t.Fatalf("expected to delete 2 keys, got %d", deleted)
	}

	// Verify keys deleted
	if _, ok := state.Get("agent.memory.fact1"); ok {
		t.Fatal("agent.memory.fact1 should be deleted")
	}
	if _, ok := state.Get("agent.memory.fact2"); ok {
		t.Fatal("agent.memory.fact2 should be deleted")
	}

	// Verify other keys preserved
	if _, ok := state.Get("agent.plan"); !ok {
		t.Fatal("agent.plan should still exist")
	}
	if _, ok := state.Get("user.name"); !ok {
		t.Fatal("user.name should still exist")
	}

	// Verify version incremented once
	if state.Version() != prevVersion+1 {
		t.Fatalf("version should increment once, got %d (was %d)", state.Version(), prevVersion)
	}
}

func TestMemoryState_DeletePrefix_NoMatch(t *testing.T) {
	state := NewMemoryState()
	state.Set("foo", "bar")

	prevVersion := state.Version()

	deleted := state.DeletePrefix("missing.")
	if deleted != 0 {
		t.Fatalf("expected 0 deletions, got %d", deleted)
	}

	// Version should not change
	if state.Version() != prevVersion {
		t.Fatal("version should not change when nothing deleted")
	}
}

// ============================================
// Deep Copy Tests
// ============================================

func TestMemoryState_Get_DeepCopy(t *testing.T) {
	state := NewMemoryState()
	original := map[string]interface{}{
		"nested": map[string]interface{}{
			"value": "original",
		},
	}
	state.Set("key", original)

	// Get and mutate
	got, _ := state.Get("key")
	if nested, ok := got.(map[string]interface{}); ok {
		if inner, ok := nested["nested"].(map[string]interface{}); ok {
			inner["value"] = "mutated"
		}
	}

	// Verify internal state unchanged
	val, _ := state.Get("key")
	if nested, ok := val.(map[string]interface{}); ok {
		if inner, ok := nested["nested"].(map[string]interface{}); ok {
			if inner["value"] != "original" {
				t.Fatal("Get should return deep copy - internal state was mutated")
			}
		}
	}
}

func TestMemoryState_Set_DeepCopy(t *testing.T) {
	state := NewMemoryState()
	original := map[string]interface{}{"key": "original"}

	state.Set("data", original)

	// Mutate original
	original["key"] = "mutated"

	// Verify internal state unchanged
	val, _ := state.Get("data")
	if m, ok := val.(map[string]interface{}); ok {
		if m["key"] != "original" {
			t.Fatal("Set should deep copy - internal state was affected by external mutation")
		}
	}
}

func TestMemoryState_Clone_DeepCopy(t *testing.T) {
	state := NewMemoryState()
	state.Set("nested", map[string]interface{}{"value": "original"})

	cloned := state.Clone().(*MemoryState)

	// Mutate clone
	cloned.Set("nested", map[string]interface{}{"value": "mutated"})

	// Verify original unchanged
	val, _ := state.Get("nested")
	if m, ok := val.(map[string]interface{}); ok {
		if m["value"] != "original" {
			t.Fatal("Clone should deep copy - original was affected")
		}
	}
}

// ============================================
// Version Tracking Tests
// ============================================

func TestMemoryState_Version_IncrementOnChanges(t *testing.T) {
	state := NewMemoryState()
	initialVersion := state.Version()

	// Set increments
	state.Set("key", "value")
	if state.Version() != initialVersion+1 {
		t.Fatal("Set should increment version")
	}

	// Delete increments (when key exists)
	state.Delete("key")
	if state.Version() != initialVersion+2 {
		t.Fatal("Delete should increment version when key exists")
	}

	// Delete doesn't increment when key doesn't exist
	state.Delete("nonexistent")
	if state.Version() != initialVersion+2 {
		t.Fatal("Delete should not increment version when key doesn't exist")
	}

	// SetMetadata increments
	state.SetMetadata("meta", "value")
	if state.Version() != initialVersion+3 {
		t.Fatal("SetMetadata should increment version")
	}
}

// ============================================
// Concurrency Tests
// ============================================

func TestMemoryState_Concurrent_ReadWrite(t *testing.T) {
	state := NewMemoryState()
	var wg sync.WaitGroup
	iterations := 1000

	// Writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				state.Set("key", j)
			}
		}(i)
	}

	// Readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				state.Get("key")
				state.Keys()
				state.KeysWithPrefix("k")
			}
		}()
	}

	wg.Wait()
}

func TestMemoryState_Concurrent_Iterate(t *testing.T) {
	state := NewMemoryState()
	for i := 0; i < 100; i++ {
		state.Set("key"+string(rune('a'+i%26)), i)
	}

	var wg sync.WaitGroup

	// Concurrent iterators
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = iterateByPrefix(state, "key")
			}
		}()
	}

	// Concurrent writers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				state.Set("new"+string(rune('a'+id)), j)
			}
		}(i)
	}

	wg.Wait()
}

// ============================================
// StateStore Tests
// ============================================

func TestMemoryStateStore_SaveLoad(t *testing.T) {
	store := NewMemoryStateStore()
	ctx := context.Background()

	state := NewMemoryState()
	state.Set("key", "value")

	if err := store.Save(ctx, "exec-1", state.Snapshot()); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded, err := store.Load(ctx, "exec-1")
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if loaded == nil {
		t.Fatal("expected snapshot, got nil")
	}

	if loaded.Data["key"] != "value" {
		t.Fatalf("expected data to contain key=value, got %v", loaded.Data)
	}
}

func TestMemoryStateStore_Load_DeepCopy(t *testing.T) {
	store := NewMemoryStateStore()
	ctx := context.Background()

	state := NewMemoryState()
	state.Set("key", "value")

	if err := store.Save(ctx, "exec-1", state.Snapshot()); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded, _ := store.Load(ctx, "exec-1")

	// Mutate loaded snapshot
	loaded.Data["new"] = "mutation"

	// Reload and verify store wasn't affected
	reloaded, _ := store.Load(ctx, "exec-1")
	if _, exists := reloaded.Data["new"]; exists {
		t.Fatal("mutating loaded snapshot should not affect store")
	}
}

func TestMemoryStateStore_Load_NotFound(t *testing.T) {
	store := NewMemoryStateStore()
	ctx := context.Background()

	_, err := store.Load(ctx, "nonexistent")
	if !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("expected ErrStateNotFound, got %v", err)
	}
}

func TestMemoryStateStore_Delete(t *testing.T) {
	store := NewMemoryStateStore()
	ctx := context.Background()

	state := NewMemoryState()
	state.Set("key", "value")

	store.Save(ctx, "exec-1", state.Snapshot())

	if err := store.Delete(ctx, "exec-1"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	_, err := store.Load(ctx, "exec-1")
	if !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("expected ErrStateNotFound after delete, got %v", err)
	}
}

func TestMemoryStateStore_Delete_Idempotent(t *testing.T) {
	store := NewMemoryStateStore()
	ctx := context.Background()

	// Delete non-existent should not error
	if err := store.Delete(ctx, "nonexistent"); err != nil {
		t.Fatalf("delete should be idempotent, got error: %v", err)
	}
}

func TestMemoryStateStore_List(t *testing.T) {
	store := NewMemoryStateStore()
	ctx := context.Background()

	state := NewMemoryState()
	store.Save(ctx, "exec-2", state.Snapshot())
	store.Save(ctx, "exec-1", state.Snapshot())
	store.Save(ctx, "exec-3", state.Snapshot())

	ids, err := store.List(ctx)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}

	if len(ids) != 3 {
		t.Fatalf("expected 3 IDs, got %d", len(ids))
	}

	// Verify sorted
	expected := []string{"exec-1", "exec-2", "exec-3"}
	if !reflect.DeepEqual(ids, expected) {
		t.Fatalf("expected sorted IDs %v, got %v", expected, ids)
	}
}

func TestMemoryStateStore_Exists(t *testing.T) {
	store := NewMemoryStateStore()
	ctx := context.Background()

	state := NewMemoryState()
	store.Save(ctx, "exec-1", state.Snapshot())

	exists, err := store.Exists(ctx, "exec-1")
	if err != nil {
		t.Fatalf("exists failed: %v", err)
	}
	if !exists {
		t.Fatal("expected exec-1 to exist")
	}

	exists, err = store.Exists(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("exists failed: %v", err)
	}
	if exists {
		t.Fatal("expected nonexistent to not exist")
	}
}

func TestMemoryStateStore_ContextCancellation(t *testing.T) {
	store := NewMemoryStateStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	state := NewMemoryState()
	state.Set("key", "value")

	if err := store.Save(ctx, "exec-1", state.Snapshot()); err == nil {
		t.Fatal("expected error with cancelled context")
	}

	if _, err := store.Load(ctx, "exec-1"); err == nil {
		t.Fatal("expected error with cancelled context")
	}
}

func TestMemoryStateStore_ValidationErrors(t *testing.T) {
	store := NewMemoryStateStore()
	ctx := context.Background()

	// Empty execution ID
	if err := store.Save(ctx, "", &StateSnapshot{}); !errors.Is(err, ErrInvalidExecutionID) {
		t.Fatalf("expected ErrInvalidExecutionID for empty ID, got %v", err)
	}

	// Nil snapshot
	if err := store.Save(ctx, "exec-1", nil); !errors.Is(err, ErrNilSnapshot) {
		t.Fatalf("expected ErrNilSnapshot for nil snapshot, got %v", err)
	}

	// Empty ID for load
	if _, err := store.Load(ctx, ""); !errors.Is(err, ErrInvalidExecutionID) {
		t.Fatalf("expected ErrInvalidExecutionID for empty ID, got %v", err)
	}
}

// ============================================
// StateSnapshot Tests
// ============================================

func TestStateSnapshot_Clone(t *testing.T) {
	original := &StateSnapshot{
		Version: 5,
		Data: map[string]interface{}{
			"nested": map[string]interface{}{
				"value": "original",
			},
		},
		Metadata: map[string]interface{}{
			"env": "test",
		},
	}

	cloned := original.Clone()

	// Verify it's a different pointer
	if cloned == original {
		t.Fatal("Clone should return a new pointer")
	}

	// Mutate clone
	if nested, ok := cloned.Data["nested"].(map[string]interface{}); ok {
		nested["value"] = "mutated"
	}

	// Verify original unchanged
	if nested, ok := original.Data["nested"].(map[string]interface{}); ok {
		if nested["value"] != "original" {
			t.Fatal("Clone should deep copy - original was affected")
		}
	}
}

func TestStateSnapshot_Clone_Nil(t *testing.T) {
	var snapshot *StateSnapshot
	cloned := snapshot.Clone()
	if cloned != nil {
		t.Fatal("Clone of nil should return nil")
	}
}

// ============================================
// StateHistory Tests
// ============================================

func TestStateHistory_Record_DeepCopy(t *testing.T) {
	history := NewStateHistory(10)

	snapshot := &StateSnapshot{
		Version: 1,
		Data:    map[string]interface{}{"key": "original"},
	}

	history.Record(snapshot)

	// Mutate original
	snapshot.Data["key"] = "mutated"

	// Verify history has original value
	recorded, ok := history.Get(0)
	if !ok {
		t.Fatal("expected to get snapshot at index 0")
	}
	if recorded.Data["key"] != "original" {
		t.Fatal("Record should deep copy - history was affected by mutation")
	}
}

func TestStateHistory_Get_DeepCopy(t *testing.T) {
	history := NewStateHistory(10)
	history.Record(&StateSnapshot{
		Version: 1,
		Data:    map[string]interface{}{"key": "original"},
	})

	// Get and mutate
	snapshot, _ := history.Get(0)
	snapshot.Data["key"] = "mutated"

	// Verify history unchanged
	reget, _ := history.Get(0)
	if reget.Data["key"] != "original" {
		t.Fatal("Get should return deep copy - history was affected")
	}
}

// ============================================
// Benchmark Tests
// ============================================

func BenchmarkMemoryState_Set(b *testing.B) {
	state := NewMemoryState()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		state.Set("key", "value")
	}
}

func BenchmarkMemoryState_Get(b *testing.B) {
	state := NewMemoryState()
	state.Set("key", "value")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		state.Get("key")
	}
}

func BenchmarkMemoryState_Iterate_100Keys(b *testing.B) {
	state := NewMemoryState()
	for i := 0; i < 100; i++ {
		state.Set("prefix."+string(rune('a'+i%26))+string(rune('0'+i/26)), i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = iterateByPrefix(state, "prefix.")
	}
}

func BenchmarkMemoryState_Iterate_1000Keys(b *testing.B) {
	state := NewMemoryState()
	for i := 0; i < 1000; i++ {
		state.Set("prefix."+string(rune('a'+i%26))+string(rune('0'+i%10))+string(rune('0'+i/260)), i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = iterateByPrefix(state, "prefix.")
	}
}

func BenchmarkMemoryState_Marshal_100Keys(b *testing.B) {
	state := NewMemoryState()
	for i := 0; i < 100; i++ {
		state.Set("key"+string(rune('a'+i%26)), map[string]interface{}{
			"nested": "value",
			"count":  i,
		})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = state.Marshal()
	}
}

func BenchmarkMemoryStateStore_SaveLoad(b *testing.B) {
	store := NewMemoryStateStore()
	ctx := context.Background()

	state := NewMemoryState()
	for i := 0; i < 100; i++ {
		state.Set("key"+string(rune('a'+i%26)), i)
	}
	snapshot := state.Snapshot()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Save(ctx, "exec-1", snapshot)
		store.Load(ctx, "exec-1")
	}
}

// ============================================
// Phase 2A Enhancement Tests
// ============================================

func TestDeepCopy_SliceOfMaps(t *testing.T) {
	state := NewMemoryState()

	// Store slice of maps
	original := []map[string]interface{}{
		{"name": "Alice", "age": 30},
		{"name": "Bob", "age": 25},
	}
	state.Set("users", original)

	// Mutate original after storing
	original[0]["name"] = "Mutated"

	// Get should return deep copy unaffected by mutation
	retrieved, _ := state.Get("users")
	users := retrieved.([]map[string]interface{})
	if users[0]["name"] != "Alice" {
		t.Fatalf("expected 'Alice', got %v - deep copy failed for slice of maps", users[0]["name"])
	}
}

func TestDeepCopy_StructViaJSON(t *testing.T) {
	type Person struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	state := NewMemoryState()

	// Store struct
	original := Person{Name: "Alice", Age: 30}
	state.Set("person", original)

	// Mutate original
	original.Name = "Mutated"

	// Get should return deep copy unaffected by mutation
	retrieved, _ := state.Get("person")

	// For structs, JSON copy returns the same type
	if p, ok := retrieved.(Person); ok {
		if p.Name != "Alice" {
			t.Fatalf("expected name 'Alice', got %v - struct was not deep copied", p.Name)
		}
		if p.Age != 30 {
			t.Fatalf("expected age 30, got %v", p.Age)
		}
	} else {
		t.Fatalf("expected Person struct, got %T", retrieved)
	}
}

func TestIsStateNotFound(t *testing.T) {
	store := NewMemoryStateStore()
	ctx := context.Background()

	_, err := store.Load(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent")
	}

	// Test IsStateNotFound helper
	if !IsStateNotFound(err) {
		t.Fatalf("expected IsStateNotFound to return true, got false for: %v", err)
	}

	// Test errors.Is directly
	if !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("expected errors.Is(err, ErrStateNotFound) to return true")
	}

	// Test with different error
	if IsStateNotFound(ErrInvalidExecutionID) {
		t.Fatal("IsStateNotFound should return false for ErrInvalidExecutionID")
	}
}

func TestMemoryStateStore_SaveBatch(t *testing.T) {
	store := NewMemoryStateStore()
	ctx := context.Background()

	// Create snapshots
	state1 := NewMemoryState()
	state1.Set("key", "value1")
	state2 := NewMemoryState()
	state2.Set("key", "value2")

	batch := map[string]*StateSnapshot{
		"exec-1": state1.Snapshot(),
		"exec-2": state2.Snapshot(),
	}

	// Save batch
	if err := store.SaveBatch(ctx, batch); err != nil {
		t.Fatalf("SaveBatch failed: %v", err)
	}

	// Verify both saved
	if store.Count() != 2 {
		t.Fatalf("expected 2 snapshots, got %d", store.Count())
	}

	// Load and verify
	snap1, _ := store.Load(ctx, "exec-1")
	if snap1.Data["key"] != "value1" {
		t.Fatalf("expected value1, got %v", snap1.Data["key"])
	}

	snap2, _ := store.Load(ctx, "exec-2")
	if snap2.Data["key"] != "value2" {
		t.Fatalf("expected value2, got %v", snap2.Data["key"])
	}
}

func TestMemoryStateStore_SaveBatch_ValidationErrors(t *testing.T) {
	store := NewMemoryStateStore()
	ctx := context.Background()

	// Empty ID in batch
	batch := map[string]*StateSnapshot{
		"":       &StateSnapshot{Data: map[string]interface{}{}},
		"exec-1": &StateSnapshot{Data: map[string]interface{}{}},
	}

	if err := store.SaveBatch(ctx, batch); !errors.Is(err, ErrInvalidExecutionID) {
		t.Fatalf("expected ErrInvalidExecutionID, got %v", err)
	}

	// Nil snapshot in batch
	batch2 := map[string]*StateSnapshot{
		"exec-1": nil,
	}

	if err := store.SaveBatch(ctx, batch2); !errors.Is(err, ErrNilSnapshot) {
		t.Fatalf("expected ErrNilSnapshot, got %v", err)
	}
}

func TestMemoryStateStore_Clear(t *testing.T) {
	store := NewMemoryStateStore()
	ctx := context.Background()

	// Add some snapshots
	state := NewMemoryState()
	state.Set("key", "value")
	store.Save(ctx, "exec-1", state.Snapshot())
	store.Save(ctx, "exec-2", state.Snapshot())

	if store.Count() != 2 {
		t.Fatalf("expected 2 snapshots, got %d", store.Count())
	}

	// Clear
	if err := store.Clear(ctx); err != nil {
		t.Fatalf("Clear failed: %v", err)
	}

	if store.Count() != 0 {
		t.Fatalf("expected 0 snapshots after clear, got %d", store.Count())
	}
}

func TestMemoryStateStore_Clear_ContextCancellation(t *testing.T) {
	store := NewMemoryStateStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := store.Clear(ctx); err == nil {
		t.Fatal("expected error with cancelled context")
	}
}
