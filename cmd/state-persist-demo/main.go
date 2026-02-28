package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"path/filepath"

	"github.com/seyi/dagens/pkg/graph"
)

// A tiny CLI demo that shows state persistence across restarts.
// Modes:
//
//	run      - create state with sample agent outputs and snapshot to disk
//	restore  - load snapshot from disk and print stored values
func main() {
	mode := flag.String("mode", "run", "mode: run or restore")
	storeDir := flag.String("store-dir", "./.state-demo", "directory for snapshots")
	execID := flag.String("id", "demo", "execution ID for the snapshot file")
	flag.Parse()

	ctx := context.Background()

	store, err := graph.NewFileStateStore(*storeDir)
	if err != nil {
		log.Fatalf("create state store: %v", err)
	}

	switch *mode {
	case "run":
		runDemo(ctx, store, *storeDir, *execID)
	case "restore":
		restoreDemo(ctx, store, *storeDir, *execID)
	default:
		log.Fatalf("unknown mode %q; use run or restore", *mode)
	}
}

func runDemo(ctx context.Context, store *graph.FileStateStore, storeDir, execID string) {
	state := graph.NewMemoryState()

	// Simulate agents writing results.
	state.Set("agents/echo/output", "Echo: hello world")
	state.Set("agents/summarizer/output", "Summary: 4 words")
	state.Set("agents/classifier/category", "positive")
	state.Set("agents/classifier/confidence", 0.87)
	state.SetMetadata("run_id", "demo-run-1")

	snap := state.Snapshot()
	if err := store.Save(ctx, execID, snap); err != nil {
		log.Fatalf("save snapshot: %v", err)
	}

	log.Printf("Snapshot written: %s", filepath.Join(storeDir, fmt.Sprintf("%s.json", execID)))
	log.Printf("Keys persisted: %v", state.Keys())
	log.Printf("Example value: %v", must(state.Get("agents/echo/output")))
}

func restoreDemo(ctx context.Context, store *graph.FileStateStore, storeDir, execID string) {
	snap, err := store.Load(ctx, execID)
	if err != nil {
		log.Fatalf("load snapshot: %v", err)
	}

	state := graph.NewMemoryState()
	state.Restore(snap)

	log.Printf("Snapshot loaded from: %s", filepath.Join(storeDir, fmt.Sprintf("%s.json", execID)))
	log.Printf("Keys restored: %v", state.Keys())
	log.Printf("Echo output: %v", must(state.Get("agents/echo/output")))
	log.Printf("Summarizer output: %v", must(state.Get("agents/summarizer/output")))
	log.Printf("Classifier category: %v (conf: %v)", must(state.Get("agents/classifier/category")), must(state.Get("agents/classifier/confidence")))
	if runID, ok := state.GetMetadata("run_id"); ok {
		log.Printf("Metadata run_id: %v", runID)
	}
}

func must(val interface{}, ok bool) interface{} {
	if !ok {
		return "<missing>"
	}
	return val
}
