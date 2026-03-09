package graph

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
)

type deepCopyablePayload struct {
	Counter int
	Values  []int
	Meta    map[string]string
}

func (p *deepCopyablePayload) DeepCopy() any {
	if p == nil {
		return (*deepCopyablePayload)(nil)
	}
	cp := &deepCopyablePayload{
		Counter: p.Counter,
		Values:  append([]int(nil), p.Values...),
		Meta:    make(map[string]string, len(p.Meta)),
	}
	for k, v := range p.Meta {
		cp.Meta[k] = v
	}
	return cp
}

// TestDeepCopy_NonJSONTypes_NoPanic verifies non-JSON types do not panic during
// best-effort deep-copy fallback.
func TestDeepCopy_NonJSONTypes_NoPanic(t *testing.T) {
	testCases := []struct {
		name  string
		value interface{}
	}{
		{
			name:  "channel",
			value: make(chan int),
		},
		{
			name:  "buffered_channel",
			value: make(chan string, 10),
		},
		{
			name:  "function",
			value: func() {},
		},
		{
			name:  "function_with_params",
			value: func(a int, b string) bool { return true },
		},
		{
			name: "struct_with_unexported_fields",
			value: struct {
				Public  string
				private string
			}{
				Public:  "visible",
				private: "hidden",
			},
		},
		{
			name:  "sync_mutex",
			value: sync.Mutex{},
		},
		{
			name:  "sync_rwmutex",
			value: sync.RWMutex{},
		},
		{
			name: "struct_containing_mutex",
			value: struct {
				Data string
				mu   sync.RWMutex
			}{
				Data: "test data",
			},
		},
		{
			name: "struct_containing_channel",
			value: func() interface{} {
				type structWithChannel struct {
					Name    string
					Channel chan int
				}
				return structWithChannel{
					Name:    "test",
					Channel: make(chan int),
				}
			}(),
		},
		{
			name: "pointer_to_struct_with_unexported",
			value: &struct {
				Exported   string
				unexported int
			}{
				Exported:   "visible",
				unexported: 42,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			state := NewMemoryState()
			state.Set("test_key", tc.value)
			if _, ok := state.Get("test_key"); !ok {
				t.Fatalf("expected stored key for %s", tc.name)
			}
		})
	}
}

// TestDeepCopy_JSONCompatibleTypes_Success verifies that JSON-compatible types
// continue to work correctly and don't panic.
func TestDeepCopy_JSONCompatibleTypes_Success(t *testing.T) {
	state := NewMemoryState()

	// Primitives
	state.Set("string", "hello world")
	state.Set("int", 42)
	state.Set("int64", int64(9223372036854775807))
	state.Set("float64", 3.14159)
	state.Set("bool", true)

	// Collections
	state.Set("map", map[string]interface{}{
		"key1": "value1",
		"key2": 123,
		"key3": true,
	})
	state.Set("slice", []interface{}{1, "two", 3.0, true})
	state.Set("string_slice", []string{"a", "b", "c"})
	state.Set("int_slice", []int{1, 2, 3})

	// Struct with exported fields
	state.Set("struct", struct {
		Name   string `json:"name"`
		Age    int    `json:"age"`
		Active bool   `json:"active"`
	}{
		Name:   "Test User",
		Age:    30,
		Active: true,
	})

	// Nested structures
	state.Set("nested", map[string]interface{}{
		"level1": map[string]interface{}{
			"level2": []interface{}{
				map[string]interface{}{
					"key": "value",
				},
				42,
				[]string{"a", "b"},
			},
		},
	})

	// Nil value (should be handled correctly)
	state.Set("nil_value", nil)

	// Verify all can be retrieved
	testKeys := []string{
		"string", "int", "int64", "float64", "bool",
		"map", "slice", "string_slice", "int_slice",
		"struct", "nested", "nil_value",
	}

	for _, key := range testKeys {
		if _, ok := state.Get(key); !ok {
			t.Errorf("Failed to get key: %s", key)
		}
	}

	// Verify isolation (mutations don't affect original)
	originalMap := map[string]interface{}{"key": "original"}
	state.Set("isolation_test", originalMap)

	retrieved, ok := state.Get("isolation_test")
	if !ok {
		t.Fatal("Failed to retrieve isolation_test")
	}

	// Mutate the retrieved value
	retrievedMap, ok := retrieved.(map[string]interface{})
	if !ok {
		t.Fatal("Expected map[string]interface{}")
	}
	retrievedMap["key"] = "mutated"

	// Original should be unchanged
	if originalMap["key"] != "original" {
		t.Error("Deep copy failed: original map was mutated")
	}
}

// TestDeepCopy_NonJSONTypes_GetRoundTrip verifies non-JSON types survive
// set/get without crashing the state system.
func TestDeepCopy_NonJSONTypes_GetRoundTrip(t *testing.T) {
	testCases := []struct {
		name  string
		value interface{}
	}{
		{
			name:  "channel_type",
			value: make(chan int),
		},
		{
			name:  "function_type",
			value: func() {},
		},
		{
			name: "struct_with_channel_type",
			value: func() interface{} {
				type structWithChan struct {
					Name string
					Ch   chan int
				}
				return structWithChan{Name: "test", Ch: make(chan int)}
			}(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			state := NewMemoryState()
			state.Set("key", tc.value)
			if _, ok := state.Get("key"); !ok {
				t.Fatalf("expected key to be retrievable for %s", tc.name)
			}
		})
	}
}

func TestDeepCopy_NonSerializableChannel_FallbackReturnsOriginal(t *testing.T) {
	state := NewMemoryState()
	ch := make(chan int, 1)
	state.Set("ch", ch)

	got, ok := state.Get("ch")
	if !ok {
		t.Fatal("expected channel value")
	}
	gotCh, ok := got.(chan int)
	if !ok {
		t.Fatalf("expected chan int, got %T", got)
	}
	if gotCh != ch {
		t.Fatal("expected fallback to return original channel reference for non-serializable type")
	}
}

func TestDeepCopy_DeepCopyable_UsesCustomPath(t *testing.T) {
	state := NewMemoryState()
	original := &deepCopyablePayload{
		Counter: 1,
		Values:  []int{1, 2, 3},
		Meta:    map[string]string{"k": "v"},
	}
	state.Set("payload", original)

	got, ok := state.Get("payload")
	if !ok {
		t.Fatal("expected payload key")
	}
	copyPayload, ok := got.(*deepCopyablePayload)
	if !ok {
		t.Fatalf("expected *deepCopyablePayload, got %T", got)
	}
	if copyPayload == original {
		t.Fatal("expected DeepCopyable path to return distinct instance")
	}

	copyPayload.Values[0] = 999
	copyPayload.Meta["k"] = "changed"
	copyPayload.Counter = 7

	if original.Values[0] == 999 || original.Meta["k"] == "changed" || original.Counter == 7 {
		t.Fatal("mutating copied payload should not mutate original value")
	}
}

func TestDeepCopy_NonJSONTypes_IsolationLimitation(t *testing.T) {
	type nonSerializable struct {
		Name string
		Fn   func()
	}

	state := NewMemoryState()
	original := &nonSerializable{Name: "before", Fn: func() {}}
	state.Set("non_json", original)

	got, ok := state.Get("non_json")
	if !ok {
		t.Fatal("expected non_json key")
	}
	ptr, ok := got.(*nonSerializable)
	if !ok {
		t.Fatalf("expected *nonSerializable, got %T", got)
	}
	if ptr != original {
		t.Fatal("expected best-effort fallback to return original pointer for non-serializable value")
	}

	// This documents the known limitation for non-serializable values.
	ptr.Name = "mutated"
	again, ok := state.Get("non_json")
	if !ok {
		t.Fatal("expected non_json key on second read")
	}
	if again.(*nonSerializable).Name != "mutated" {
		t.Fatal("expected in-place mutation to be observable for non-serializable fallback values")
	}
}

func TestIsImmutableType_EdgeCases(t *testing.T) {
	i := 10
	testCases := []struct {
		name  string
		value interface{}
		want  bool
	}{
		{name: "json_number", value: json.Number("42"), want: true},
		{name: "complex64", value: complex64(1 + 2i), want: true},
		{name: "complex128", value: complex128(3 + 4i), want: true},
		{name: "uintptr", value: uintptr(0x123), want: true},
		{name: "nil", value: nil, want: true},
		{name: "pointer", value: &i, want: false},
		{name: "empty_struct", value: struct{}{}, want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isImmutableType(tc.value); got != tc.want {
				t.Fatalf("isImmutableType(%T)=%v want %v", tc.value, got, tc.want)
			}
		})
	}
}

// TestDeepCopy_EmptyInterface verifies handling of empty interfaces
func TestDeepCopy_EmptyInterface(t *testing.T) {
	state := NewMemoryState()

	// Empty interface with JSON-compatible value should work
	var emptyInterface interface{} = "string value"
	state.Set("empty_interface", emptyInterface)

	retrieved, ok := state.Get("empty_interface")
	if !ok {
		t.Fatal("Failed to retrieve empty_interface")
	}

	if retrieved != "string value" {
		t.Errorf("Expected 'string value', got %v", retrieved)
	}
}

// TestDeepCopy_NilValues verifies correct handling of nil
func TestDeepCopy_NilValues(t *testing.T) {
	state := NewMemoryState()

	// Direct nil
	state.Set("nil", nil)

	// Nil pointer
	var nilPtr *string
	state.Set("nil_pointer", nilPtr)

	// Nil slice
	var nilSlice []string
	state.Set("nil_slice", nilSlice)

	// Nil map
	var nilMap map[string]interface{}
	state.Set("nil_map", nilMap)

	// All should be retrievable
	for _, key := range []string{"nil", "nil_pointer", "nil_slice", "nil_map"} {
		if _, ok := state.Get(key); !ok {
			t.Errorf("Failed to retrieve %s", key)
		}
	}
}

// TestDeepCopy_ComplexNestedStructures tests deeply nested JSON-compatible structures
func TestDeepCopy_ComplexNestedStructures(t *testing.T) {
	state := NewMemoryState()

	// Create a complex nested structure
	complex := map[string]interface{}{
		"users": []interface{}{
			map[string]interface{}{
				"name": "Alice",
				"age":  30,
				"tags": []string{"admin", "power-user"},
				"metadata": map[string]interface{}{
					"lastLogin": "2025-12-16",
					"settings": map[string]interface{}{
						"theme":  "dark",
						"locale": "en-US",
					},
				},
			},
			map[string]interface{}{
				"name": "Bob",
				"age":  25,
				"tags": []string{"user"},
			},
		},
		"config": map[string]interface{}{
			"version": "1.0",
			"features": []interface{}{
				"feature1",
				"feature2",
				map[string]interface{}{
					"name":    "feature3",
					"enabled": true,
				},
			},
		},
	}

	state.Set("complex", complex)

	retrieved, ok := state.Get("complex")
	if !ok {
		t.Fatal("Failed to retrieve complex structure")
	}

	// Verify it's a deep copy by mutating
	retrievedMap := retrieved.(map[string]interface{})
	users := retrievedMap["users"].([]interface{})
	firstUser := users[0].(map[string]interface{})
	firstUser["name"] = "MUTATED"

	// Original should be unchanged
	originalFirstUser := complex["users"].([]interface{})[0].(map[string]interface{})
	if originalFirstUser["name"] != "Alice" {
		t.Error("Deep copy failed: nested structure was mutated")
	}
}

func TestDeepCopyValue_ConcurrentStress(t *testing.T) {
	state := NewMemoryState()
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			state.Set("complex", map[string]interface{}{
				"data": []interface{}{i, i + 1, i + 2},
				"meta": map[string]interface{}{
					"version": fmt.Sprintf("v%d", i),
				},
			})
		}
	}()

	for r := 0; r < 5; r++ {
		readerID := r
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 400; i++ {
				got, ok := state.Get("complex")
				if !ok {
					continue
				}
				m, ok := got.(map[string]interface{})
				if !ok {
					t.Errorf("expected map payload, got %T", got)
					return
				}
				if arr, ok := m["data"].([]interface{}); ok && len(arr) > 0 {
					arr[0] = readerID
				}
			}
		}()
	}

	wg.Wait()
}
