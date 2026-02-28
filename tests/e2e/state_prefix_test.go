package e2e

import (
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/graph"
	"strconv"
)

// TestE2E_PrefixIteration_ScopedRetrieval ensures prefix iteration returns only scoped keys.
func TestE2E_PrefixIteration_ScopedRetrieval(t *testing.T) {
	state := graph.NewMemoryState()
	state.Set("session/123/agent/echo/output", "Echo: hi")
	state.Set("session/123/agent/summarizer/output", "Summary: ok")
	state.Set("session/999/agent/echo/output", "Other")

	res := state.Iterate("session/123/")
	if len(res) != 2 {
		t.Fatalf("expected 2 keys for session/123, got %d: %+v", len(res), res)
	}
	if _, ok := res["session/999/agent/echo/output"]; ok {
		t.Fatalf("unexpected key from other session present")
	}
}

// TestE2E_PrefixIteration_PerfSmoke does a small perf smoke: warn >1ms avg, fail >5ms.
func TestE2E_PrefixIteration_PerfSmoke(t *testing.T) {
	state := graph.NewMemoryState()
	for i := 0; i < 2000; i++ {
		key := "batch/" + strconv.Itoa(i/10) + "/" + strconv.Itoa(i)
		state.Set(key, i)
	}
	start := time.Now()
	loops := 500
	for i := 0; i < loops; i++ {
		_ = state.Iterate("batch/")
	}
	avg := time.Since(start) / time.Duration(loops)

	if avg > 5*time.Millisecond {
		t.Fatalf("perf fail: avg %s > 5ms", avg)
	}
	if avg > time.Millisecond {
		t.Logf("perf warn: avg %s > 1ms (non-fatal soft threshold)", avg)
	}
}
