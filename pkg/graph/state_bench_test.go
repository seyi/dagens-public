package graph

import (
	"strconv"
	"testing"
)

// Benchmark Iterate with ~1K keys and a shared prefix.
func BenchmarkMemoryState_Iterate_1K(b *testing.B) {
	state := NewMemoryState()
	for i := 0; i < 1000; i++ {
		k := strconv.Itoa(i)
		state.Set("pref/"+k, i)
	}
	for i := 0; i < 1000; i++ {
		k := strconv.Itoa(i)
		state.Set("other/"+k, i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = state.Iterate("pref/")
	}
}

// Benchmark KeysWithPrefix with ~1K keys and a shared prefix.
func BenchmarkMemoryState_KeysWithPrefix_1K(b *testing.B) {
	state := NewMemoryState()
	for i := 0; i < 1000; i++ {
		k := strconv.Itoa(i)
		state.Set("pref/"+k, i)
	}
	for i := 0; i < 1000; i++ {
		k := strconv.Itoa(i)
		state.Set("other/"+k, i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = state.KeysWithPrefix("pref/")
	}
}
