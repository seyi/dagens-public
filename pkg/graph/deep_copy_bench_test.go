package graph

import (
	"testing"
)

// BenchmarkDeepCopyViaJSON benchmarks the deep copy operation for complex structs
// that would trigger the JSON-based copying path
func BenchmarkDeepCopyViaJSON(b *testing.B) {
	// Create a complex struct that will trigger JSON-based copying
	complexData := map[string]interface{}{
		"nested_map": map[string]interface{}{
			"field1": "value1",
			"field2": 42,
			"nested_slice": []interface{}{
				map[string]interface{}{"item": 1},
				map[string]interface{}{"item": 2},
				map[string]interface{}{"item": 3},
			},
		},
		"slice_of_maps": []map[string]interface{}{
			{"id": 1, "name": "item1", "data": map[string]interface{}{"a": 1, "b": 2}},
			{"id": 2, "name": "item2", "data": map[string]interface{}{"c": 3, "d": 4}},
			{"id": 3, "name": "item3", "data": map[string]interface{}{"e": 5, "f": 6}},
		},
		"deeply_nested": map[string]interface{}{
			"level1": map[string]interface{}{
				"level2": map[string]interface{}{
					"level3": map[string]interface{}{
						"level4": map[string]interface{}{
							"data": "deep_value",
						},
					},
				},
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = deepCopyValue(complexData)
	}
}

// BenchmarkStateOperationsWithComplexData benchmarks state operations
// with complex data that triggers JSON-based deep copying
func BenchmarkStateOperationsWithComplexData(b *testing.B) {
	state := NewMemoryState()

	complexData := map[string]interface{}{
		"user_profile": map[string]interface{}{
			"id":   12345,
			"name": "John Doe",
			"settings": map[string]interface{}{
				"preferences": map[string]interface{}{
					"theme":         "dark",
					"notifications": true,
				},
				"permissions": []string{"read", "write", "admin"},
			},
			"history": []map[string]interface{}{
				{"action": "login", "timestamp": "2023-01-01T00:00:00Z"},
				{"action": "update", "timestamp": "2023-01-02T00:00:00Z"},
				{"action": "logout", "timestamp": "2023-01-03T00:00:00Z"},
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := "complex_data_" + string(rune(i%100))
		state.Set(key, complexData)

		// Get the value back (triggers deep copy)
		_, ok := state.Get(key)
		if !ok {
			b.Fatalf("Failed to get value for key: %s", key)
		}
	}
}

// BenchmarkStateCloneWithComplexData benchmarks cloning state with complex data
func BenchmarkStateCloneWithComplexData(b *testing.B) {
	state := NewMemoryState()

	complexData := map[string]interface{}{
		"dataset": []map[string]interface{}{
			{"id": 1, "values": []float64{1.1, 2.2, 3.3, 4.4, 5.5}},
			{"id": 2, "values": []float64{6.6, 7.7, 8.8, 9.9, 10.0}},
			{"id": 3, "values": []float64{11.1, 12.2, 13.3, 14.4, 15.5}},
		},
		"metadata": map[string]interface{}{
			"source":          "api",
			"version":         "1.0",
			"transformations": []string{"normalize", "scale", "encode"},
		},
	}

	// Pre-populate the state with complex data
	for i := 0; i < 10; i++ {
		state.Set("data_"+string(rune(i)), complexData)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clone := state.Clone()
		if clone == nil {
			b.Fatalf("Clone returned nil")
		}
	}
}
