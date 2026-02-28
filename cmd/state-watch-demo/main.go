package main

import (
	"context"
	"log"
	"time"

	"github.com/seyi/dagens/pkg/graph"
)

// A minimal CLI demo for STATE-603:
// - Two "agents" write to shared MemoryState
// - A watcher subscribes and prints state events in real time
//
// Run:
//
//	go run ./cmd/state-watch-demo
func main() {
	state := graph.NewMemoryState()
	defer state.Close() // Clean up resources

	// Start watcher
	events, stop := state.Watch(context.Background())
	done := make(chan struct{})
	go func() {
		for evt := range events {
			log.Printf("[watcher] type=%v key=%s version=%d value=%v", evt.Type, evt.Key, evt.Version, evt.Value)
		}
		close(done)
	}()

	// Simulate two agents writing to shared state
	agentA(state)
	agentB(state)

	// Allow events to flush
	time.Sleep(200 * time.Millisecond)

	// Clean shutdown: stop watcher and wait
	stop()
	<-done
}

func agentA(state *graph.MemoryState) {
	state.Set("agents/alpha/status", "starting")
	state.Set("agents/alpha/progress", 0.1)
	state.Set("agents/alpha/status", "running")
	state.Set("agents/alpha/progress", 0.5)
	state.Set("agents/alpha/status", "done")
	state.Set("agents/alpha/progress", 1.0)
}

func agentB(state *graph.MemoryState) {
	state.Set("agents/beta/status", "boot")
	state.Set("agents/beta/context", map[string]interface{}{
		"task": "summarize",
		"id":   "req-42",
	})
	state.Set("agents/beta/status", "waiting-input")
	state.Delete("agents/beta/context") // simulate cleanup
	state.Set("agents/beta/status", "done")
}
