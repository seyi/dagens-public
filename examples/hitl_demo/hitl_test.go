package main

import (
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/graph"
	"github.com/seyi/dagens/pkg/hitl"
)

func TestHITLFlow(t *testing.T) {
	// Create a simple test to verify the HITL components work together
	responseManager := hitl.NewHumanResponseManager()
	
	humanNode := hitl.NewHumanNode(hitl.HumanNodeConfig{
		ID:                 "test_human",
		Prompt:             "Test prompt",
		Options:            []string{"Option1", "Option2"},
		Timeout:            10 * time.Second,
		ShortWaitThreshold: 5 * time.Second,
		MaxConcurrentWaits: 1000,
		ResponseManager:    responseManager,
		CheckpointStore:    hitl.NewRedisCheckpointStore(),
		CallbackSecret:     []byte("test-secret"),
	})
	
	if humanNode.ID() != "test_human" {
		t.Errorf("Expected node ID 'test_human', got '%s'", humanNode.ID())
	}
	
	if humanNode.Type() != "human" {
		t.Errorf("Expected node type 'human', got '%s'", humanNode.Type())
	}
	
	// Test state creation
	state := graph.NewMemoryState()
	state.Set("test_key", "test_value")
	
	if val, exists := state.Get("test_key"); !exists || val != "test_value" {
		t.Errorf("State set/get failed")
	}
	
	// Test checkpoint creation
	checkpointStore := hitl.NewRedisCheckpointStore()

	// Test basic checkpoint operations
	cp := &hitl.ExecutionCheckpoint{
		GraphID:      "test",
		GraphVersion: "v1.0.0",
		NodeID:       "test_node",
		StateData:    []byte("{}"),
		RequestID:    "test_request",
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(24 * time.Hour),
	}

	if err := checkpointStore.Create(cp); err != nil {
		t.Errorf("Failed to create checkpoint: %v", err)
	}

	retrievedCp, err := checkpointStore.GetByRequestID("test_request")
	if err != nil {
		t.Errorf("Failed to get checkpoint: %v", err)
	}

	if retrievedCp.GraphID != "test" {
		t.Errorf("Expected GraphID 'test', got '%s'", retrievedCp.GraphID)
	}

	// Test basic functionality
	registry := hitl.NewSimpleGraphRegistry()
	registry.RegisterGraph(hitl.GraphDefinition{
		ID:      "test",
		Version: "v1.0.0",
	})

	if _, err := registry.GetGraph("test"); err != nil {
		t.Errorf("Failed to get registered graph: %v", err)
	}

	t.Log("HITL components test passed")
}