package e2e

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/seyi/dagens/pkg/graph"
)

// TestE2E_StateSerialization_ProcessRestart verifies STATE-601 acceptance criteria:
// "State can be snapshotted and restored across process restarts; covered by E2E test."
//
// Improvements over initial version:
// 1. Ignores Timestamp during comparison (timestamps always differ across instances).
// 2. Normalizes types for comparison (JSON round-trips int to float64).
// 3. Tests concurrency safety before serialization.
// 4. Verifies individual fields (Version, Data, Metadata) with clear errors.
// 5. Handles nil vs empty map comparisons correctly.
func TestE2E_StateSerialization_ProcessRestart(t *testing.T) {
	testCases := []struct {
		name       string
		setupFunc  func(s *graph.MemoryState)
		verifyFunc func(t *testing.T, orig, restored *graph.MemoryState)
	}{
		{
			name: "EmptyState",
			setupFunc: func(s *graph.MemoryState) {
				// No data added - verify empty state round-trips correctly
			},
			verifyFunc: func(t *testing.T, orig, restored *graph.MemoryState) {
				assertStatesMatch(t, orig, restored)
			},
		},
		{
			name: "SimpleState_VariousTypes",
			setupFunc: func(s *graph.MemoryState) {
				// Populate with different data types
				s.Set("stringKey", "hello world")
				s.Set("numberKey", 42) // Will become float64 after JSON round-trip
				s.Set("floatKey", 3.14)
				s.Set("boolKey", true)
				s.Set("sliceKey", []int{1, 2, 3})
				s.Set("mapKey", map[string]interface{}{
					"nestedString": "nested",
					"nestedNum":    99,
				})
				s.SetMetadata("user.role", "admin")
			},
			verifyFunc: func(t *testing.T, orig, restored *graph.MemoryState) {
				assertStatesMatch(t, orig, restored)

				// Verify deep copy semantics: mutating restored slice shouldn't affect original
				restSlice, ok := restored.Get("sliceKey")
				if !ok {
					t.Fatalf("restored sliceKey not found")
				}
				// JSON unmarshal converts []int to []interface{}
				restSliceIface, ok := restSlice.([]interface{})
				if !ok {
					t.Fatalf("expected sliceKey to be []interface{} after restore, got %T", restSlice)
				}

				// Modify the slice in place
				restSliceIface = append(restSliceIface, 99)
				restored.Set("sliceKey", restSliceIface)

				// Original should still have 3 elements
				origSlice, ok := orig.Get("sliceKey")
				if !ok {
					t.Fatalf("original sliceKey not found")
				}

				// Note: Original kept []int.
				val := reflect.ValueOf(origSlice)
				if val.Kind() != reflect.Slice || val.Len() != 3 {
					t.Errorf("Deep copy failed: expected original slice length 3, got %v", val.Len())
				}
			},
		},
		{
			name: "LargeState_100PlusKeys",
			setupFunc: func(s *graph.MemoryState) {
				// Insert 150 key-value pairs to test scalability
				for i := 0; i < 150; i++ {
					key := "key" + strconv.Itoa(i)
					s.Set(key, i)
				}
			},
			verifyFunc: func(t *testing.T, orig, restored *graph.MemoryState) {
				assertStatesMatch(t, orig, restored)
			},
		},
		{
			name: "DeeplyNestedStructures",
			setupFunc: func(s *graph.MemoryState) {
				// Create a deeply nested structure
				nested := map[string]interface{}{
					"level1": map[string]interface{}{
						"level2": []interface{}{
							map[string]interface{}{
								"key": "value",
							},
							42,
							[]string{"a", "b", "c"},
						},
					},
				}
				s.Set("nested", nested)
			},
			verifyFunc: func(t *testing.T, orig, restored *graph.MemoryState) {
				assertStatesMatch(t, orig, restored)
			},
		},
		{
			name: "ConcurrentOperations_PreSerialization",
			setupFunc: func(s *graph.MemoryState) {
				// Simulate heavy concurrent usage before serialization
				var wg sync.WaitGroup
				workers := 10
				opsPerWorker := 50

				for i := 0; i < workers; i++ {
					wg.Add(1)
					go func(id int) {
						defer wg.Done()
						for j := 0; j < opsPerWorker; j++ {
							key := fmt.Sprintf("worker-%d-op-%d", id, j)
							s.Set(key, j)
							// Occasional read to simulate mixed workload
							if j%5 == 0 {
								s.Get(key)
							}
						}
					}(i)
				}
				wg.Wait()
			},
			verifyFunc: func(t *testing.T, orig, restored *graph.MemoryState) {
				assertStatesMatch(t, orig, restored)

				// Verify count
				origKeys := orig.Keys()
				restKeys := restored.Keys()
				if len(origKeys) != len(restKeys) {
					t.Errorf("Concurrent state key count mismatch: orig %d, rest %d", len(origKeys), len(restKeys))
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Step 1: Create original state and populate
			origState := graph.NewMemoryState()
			tc.setupFunc(origState)

			// Step 2: Marshal to bytes (simulates writing to disk/network at process boundary)
			data, err := origState.Marshal()
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}

			// Step 3: Simulate process restart - create NEW instance
			newState := graph.NewMemoryState()

			// Step 4: Unmarshal bytes into new instance (simulates reading from disk on restart)
			if err := newState.Unmarshal(data); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}

			// Step 5: Verify data integrity
			tc.verifyFunc(t, origState, newState)
		})
	}
}

// assertStatesMatch compares two states for equality, ignoring timestamps.
// It handles JSON type normalization (int -> float64) to ensure accurate comparison across the serialization boundary.
func assertStatesMatch(t *testing.T, orig, restored *graph.MemoryState) {
	t.Helper()

	origSnap := orig.Snapshot()
	restSnap := restored.Snapshot()

	// 1. Check Version (Should be exact match as it's an int64 field in struct)
	if origSnap.Version != restSnap.Version {
		t.Errorf("Version mismatch: expected %d, got %d", origSnap.Version, restSnap.Version)
	}

	// 2. Check Data
	// Normalize original data through JSON round-trip to match Unmarshal behavior (e.g. ints becoming floats).
	// Note: nil and empty maps are treated as equivalent (see normalizedMapsEqual).
	expectedData := normalizeMapViaJSON(t, origSnap.Data)
	if !normalizedMapsEqual(expectedData, restSnap.Data) {
		t.Errorf("Data mismatch after restore.\nExpected (normalized): %+v\nGot: %+v", expectedData, restSnap.Data)
	}

	// 3. Check Metadata
	// Normalize and then compare, treating nil and empty maps as equivalent.
	expectedMeta := normalizeMapViaJSON(t, origSnap.Metadata)
	if !normalizedMapsEqual(expectedMeta, restSnap.Metadata) {
		t.Errorf("Metadata mismatch after restore.\nExpected (normalized): %+v\nGot: %+v", expectedMeta, restSnap.Metadata)
	}

	// 4. Sanity check Timestamp (should exist in restored snapshot, even if different)
	if restSnap.Timestamp.IsZero() {
		t.Error("Restored snapshot has zero timestamp, expected valid time")
	}
}

// normalizedMapsEqual compares two maps that have already been normalized via JSON.
// It treats nil and empty maps as equivalent, since JSON marshalling typically
// produces {} for an empty object, not null.
func normalizedMapsEqual(a, b map[string]interface{}) bool {
	// Treat nil and empty maps as equal.
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// normalizeMapViaJSON performs a JSON round-trip on a map to convert types to their JSON equivalents
// (e.g., int -> float64, []int -> []interface{}), allowing strict DeepEqual comparison with unmarshaled data.
func normalizeMapViaJSON(t *testing.T, input map[string]interface{}) map[string]interface{} {
	t.Helper()
	if input == nil {
		// Return an empty map rather than nil so that comparisons with maps
		// produced by JSON unmarshalling (which tend to be non-nil) don't fail
		// purely on nil vs empty semantics.
		return map[string]interface{}{}
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("Validation error: failed to marshal input for normalization: %v", err)
	}

	var output map[string]interface{}
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatalf("Validation error: failed to unmarshal for normalization: %v", err)
	}
	// Ensure we never return nil: treat "no keys" as empty map.
	if output == nil {
		output = map[string]interface{}{}
	}
	return output
}
