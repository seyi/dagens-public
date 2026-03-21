// Copyright 2025 Apache Spark AI Agents
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Example demonstrating state management for distributed AI agent systems.
// Includes checkpointing for fault tolerance, session management, and execution history.
//
// Run with: go run state_management_example.go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/state"
)

// State_Example1_BasicStateOperations demonstrates basic key-value state storage.
// This is the foundation for distributed agent state management.
func State_Example1_BasicStateOperations() {
	fmt.Println("=== Example 1: Basic State Operations ===")

	ctx := context.Background()
	backend := state.NewMemoryStateBackend()
	defer backend.Close()

	// Store agent state
	agentState := []byte(`{"model": "gpt-4", "temperature": 0.7, "context_length": 4096}`)
	err := backend.Set(ctx, "agent:data-processor:config", agentState, 0)
	if err != nil {
		fmt.Printf("Failed to set state: %v\n", err)
		return
	}
	fmt.Println("  Stored agent configuration")

	// Retrieve state
	value, err := backend.Get(ctx, "agent:data-processor:config")
	if err != nil {
		fmt.Printf("Failed to get state: %v\n", err)
		return
	}
	fmt.Printf("  Retrieved config: %s\n", string(value))

	// List keys by pattern
	backend.Set(ctx, "agent:analyzer:config", []byte(`{}`), 0)
	backend.Set(ctx, "agent:writer:config", []byte(`{}`), 0)

	keys, _ := backend.List(ctx, "agent:*")
	fmt.Printf("  Found %d agent configs\n", len(keys))

	// Delete state
	backend.Delete(ctx, "agent:writer:config")
	fmt.Println("  Deleted writer config")
	fmt.Println()
}

// State_Example2_StateWithTTL demonstrates state expiration for automatic cleanup.
// Useful for temporary data, caches, and rate limiting state.
func State_Example2_StateWithTTL() {
	fmt.Println("=== Example 2: State with TTL (Time-To-Live) ===")

	ctx := context.Background()
	backend := state.NewMemoryStateBackend()
	defer backend.Close()

	// Store temporary state with 500ms TTL
	err := backend.Set(ctx, "temp:processing-lock:task-123", []byte("locked"), 500*time.Millisecond)
	if err != nil {
		fmt.Printf("Failed to set state: %v\n", err)
		return
	}
	fmt.Println("  Set temporary lock with 500ms TTL")

	// Verify it exists
	_, err = backend.Get(ctx, "temp:processing-lock:task-123")
	if err == nil {
		fmt.Println("  Lock exists (before expiration)")
	}

	// Wait for expiration
	fmt.Println("  Waiting for TTL expiration...")
	time.Sleep(600 * time.Millisecond)

	// Verify it expired
	_, err = backend.Get(ctx, "temp:processing-lock:task-123")
	if err == state.ErrNotFound {
		fmt.Println("  Lock expired automatically")
	}

	fmt.Println("\nUse cases for TTL:")
	fmt.Println("  - Distributed locks")
	fmt.Println("  - Rate limiting counters")
	fmt.Println("  - Temporary caches")
	fmt.Println("  - Session timeouts")
	fmt.Println()
}

// State_Example3_Checkpointing demonstrates checkpoint creation and restoration.
// Checkpoints enable fault tolerance by saving agent state for recovery.
func State_Example3_Checkpointing() {
	fmt.Println("=== Example 3: Checkpointing for Fault Tolerance ===")

	ctx := context.Background()
	checkpointMgr := state.NewMemoryCheckpointManager()

	// Create checkpoints during long-running operations
	fmt.Println("Creating checkpoints during multi-step processing:")

	for step := 1; step <= 3; step++ {
		checkpoint := &state.Checkpoint{
			ID:      fmt.Sprintf("cp-%d", step),
			JobID:   "data-pipeline-001",
			StageID: step,
			TaskID:  "processing-task",
			State: map[string]interface{}{
				"progress":        step * 33,
				"records_processed": step * 1000,
				"last_record_id":   fmt.Sprintf("rec-%d", step*1000),
			},
		}

		err := checkpointMgr.Checkpoint(ctx, checkpoint)
		if err != nil {
			fmt.Printf("  Checkpoint failed: %v\n", err)
			continue
		}
		fmt.Printf("  Step %d: Checkpoint saved (progress: %d%%)\n", step, step*33)
	}

	// Simulate failure and recovery
	fmt.Println("\nSimulating failure and recovery:")
	checkpoints, _ := checkpointMgr.List(ctx, "data-pipeline-001")
	fmt.Printf("  Found %d checkpoints for recovery\n", len(checkpoints))

	// Restore from latest checkpoint
	if len(checkpoints) > 0 {
		latestID := checkpoints[len(checkpoints)-1].ID
		restored, _ := checkpointMgr.Restore(ctx, latestID)
		fmt.Printf("  Restored from checkpoint %s\n", restored.ID)
		fmt.Printf("  Resume from: %v%% progress\n", restored.State["progress"])
	}
	fmt.Println()
}

// State_Example4_CheckpointBackend demonstrates the CheckpointBackend interface.
// This provides a more flexible API for checkpoint management.
func State_Example4_CheckpointBackend() {
	fmt.Println("=== Example 4: Checkpoint Backend Interface ===")

	ctx := context.Background()
	backend := state.NewMemoryCheckpointBackend()

	// Save multiple checkpoints for an agent
	agentID := "analysis-agent"
	for i := 1; i <= 3; i++ {
		checkpoint := &state.Checkpoint{
			ID:    fmt.Sprintf("checkpoint-%d", i),
			JobID: agentID,
			State: map[string]interface{}{
				"iteration": i,
				"model_weights": fmt.Sprintf("weights-v%d", i),
			},
		}
		backend.Save(ctx, checkpoint)
		time.Sleep(10 * time.Millisecond) // Ensure different timestamps
	}
	fmt.Printf("  Saved 3 checkpoints for %s\n", agentID)

	// Get the latest checkpoint
	latest, err := backend.GetLatest(ctx, agentID)
	if err != nil {
		fmt.Printf("  Error: %v\n", err)
		return
	}
	fmt.Printf("  Latest checkpoint: %s (iteration %v)\n", latest.ID, latest.State["iteration"])

	// List all checkpoints
	metas, _ := backend.ListByAgent(ctx, agentID, 10)
	fmt.Printf("  Total checkpoints: %d\n", len(metas))
	fmt.Println()
}

// State_Example5_SessionManagement demonstrates session state with optimistic locking.
// Sessions track user or agent interactions with concurrency control.
func State_Example5_SessionManagement() {
	fmt.Println("=== Example 5: Session Management with Optimistic Locking ===")

	ctx := context.Background()
	backend := state.NewMemorySessionBackend()

	// Create a new session
	session := &state.SessionState{
		ID:      "session-abc123",
		AgentID: "assistant-agent",
		State: map[string]interface{}{
			"conversation_turns": 0,
			"context":            []string{},
			"user_preferences":   map[string]string{"language": "en"},
		},
		Metadata: map[string]string{
			"client_type": "web",
			"region":      "us-west-2",
		},
	}

	err := backend.Create(ctx, session)
	if err != nil {
		fmt.Printf("  Failed to create session: %v\n", err)
		return
	}
	fmt.Printf("  Created session: %s (version %d)\n", session.ID, session.Version)

	// Update session with optimistic locking
	retrieved, _ := backend.Get(ctx, session.ID)
	retrieved.State["conversation_turns"] = 1
	retrieved.State["context"] = []string{"User asked about weather"}

	err = backend.Update(ctx, retrieved)
	if err != nil {
		fmt.Printf("  Update failed: %v\n", err)
		return
	}
	fmt.Printf("  Updated session (version %d)\n", retrieved.Version)

	// Demonstrate optimistic locking conflict
	fmt.Println("\nDemonstrating optimistic locking:")
	copy1, _ := backend.Get(ctx, session.ID)
	copy2, _ := backend.Get(ctx, session.ID)

	// First update succeeds
	copy1.State["conversation_turns"] = 2
	backend.Update(ctx, copy1)
	fmt.Println("  First concurrent update: SUCCESS")

	// Second update fails (version conflict)
	copy2.State["conversation_turns"] = 2
	err = backend.Update(ctx, copy2)
	if err == state.ErrVersionConflict {
		fmt.Println("  Second concurrent update: CONFLICT DETECTED")
		fmt.Println("  -> Optimistic locking prevented data corruption!")
	}
	fmt.Println()
}

// State_Example6_ExecutionHistory demonstrates tracking agent execution history.
// History is essential for debugging, auditing, and replay capabilities.
func State_Example6_ExecutionHistory() {
	fmt.Println("=== Example 6: Execution History Tracking ===")

	ctx := context.Background()
	backend := state.NewMemoryHistoryBackend()
	sessionID := "session-xyz789"

	// Record execution history
	actions := []struct {
		action string
		input  string
		output string
	}{
		{"user_message", "What's the weather?", ""},
		{"tool_call", "weather_api", `{"temp": 72, "conditions": "sunny"}`},
		{"agent_response", "", "It's 72F and sunny today!"},
		{"user_message", "Thanks!", ""},
		{"agent_response", "", "You're welcome! Let me know if you need anything else."},
	}

	fmt.Println("Recording execution history:")
	for _, a := range actions {
		entry := &state.HistoryEntry{
			SessionID: sessionID,
			Action:    a.action,
			Input:     a.input,
			Output:    a.output,
			Metadata: map[string]string{
				"model": "gpt-4",
			},
		}
		backend.Append(ctx, entry)
		fmt.Printf("  Recorded: %s\n", a.action)
	}

	// Retrieve full history
	history, _ := backend.List(ctx, sessionID, 0)
	fmt.Printf("\nFull history: %d entries\n", len(history))

	// Retrieve recent history (last 2 entries)
	recent, _ := backend.List(ctx, sessionID, 2)
	fmt.Printf("Recent history: %d entries\n", len(recent))
	for _, entry := range recent {
		fmt.Printf("  - %s: %s\n", entry.Action, truncateString(entry.Output.(string), 40))
	}
	fmt.Println()
}

// State_Example7_CompositeStateStore demonstrates the unified state store.
// This combines all state backends into a single interface.
func State_Example7_CompositeStateStore() {
	fmt.Println("=== Example 7: Composite State Store ===")

	ctx := context.Background()

	// Create unified state store
	store := state.NewMemoryStateStore()
	defer store.Close()

	fmt.Println("Composite store provides access to:")

	// 1. Key-Value State
	store.State.Set(ctx, "config:global", []byte(`{"version": "1.0"}`), 0)
	fmt.Println("  1. Key-Value State (config, cache, etc.)")

	// 2. Checkpoints
	store.Checkpoint.Save(ctx, &state.Checkpoint{
		ID:    "cp-001",
		JobID: "main-pipeline",
		State: map[string]interface{}{"step": 1},
	})
	fmt.Println("  2. Checkpoints (fault tolerance)")

	// 3. Sessions
	store.Session.Create(ctx, &state.SessionState{
		ID:      "sess-001",
		AgentID: "assistant",
	})
	fmt.Println("  3. Sessions (user/agent interactions)")

	// 4. History
	store.History.Append(ctx, &state.HistoryEntry{
		SessionID: "sess-001",
		Action:    "start",
	})
	fmt.Println("  4. History (audit trail)")

	fmt.Println("\nBenefits of composite store:")
	fmt.Println("  - Single point of configuration")
	fmt.Println("  - Consistent error handling")
	fmt.Println("  - Easy to swap backends (memory -> Redis -> DynamoDB)")
	fmt.Println()
}

// State_Example8_DistributedStatePattern demonstrates patterns for distributed state.
// These patterns work across Spark partitions and nodes.
func State_Example8_DistributedStatePattern() {
	fmt.Println("=== Example 8: Distributed State Patterns ===")

	ctx := context.Background()
	backend := state.NewMemoryStateBackend()
	defer backend.Close()

	// Pattern 1: Partitioned State Keys
	fmt.Println("Pattern 1: Partitioned State Keys")
	partitions := []int{0, 1, 2}
	for _, p := range partitions {
		key := fmt.Sprintf("partition:%d:offset", p)
		value := []byte(fmt.Sprintf(`{"offset": %d}`, p*1000))
		backend.Set(ctx, key, value, 0)
	}
	keys, _ := backend.List(ctx, "partition:*")
	fmt.Printf("  Created %d partition states\n", len(keys))

	// Pattern 2: Agent-Specific State
	fmt.Println("\nPattern 2: Agent-Specific State")
	agents := []string{"agent-a", "agent-b", "agent-c"}
	for _, a := range agents {
		key := fmt.Sprintf("agent:%s:metrics", a)
		backend.Set(ctx, key, []byte(`{"requests": 100}`), 0)
	}
	agentKeys, _ := backend.List(ctx, "agent:*")
	fmt.Printf("  Created %d agent states\n", len(agentKeys))

	// Pattern 3: Hierarchical State
	fmt.Println("\nPattern 3: Hierarchical State")
	hierarchy := []string{
		"job:001:stage:1:task:a",
		"job:001:stage:1:task:b",
		"job:001:stage:2:task:a",
	}
	for _, h := range hierarchy {
		backend.Set(ctx, h, []byte(`{}`), 0)
	}
	stage1Keys, _ := backend.List(ctx, "job:001:stage:1:*")
	fmt.Printf("  Stage 1 tasks: %d\n", len(stage1Keys))

	fmt.Println("\nKey naming conventions:")
	fmt.Println("  - partition:{id}:{type} - Spark partition state")
	fmt.Println("  - agent:{name}:{type} - Per-agent state")
	fmt.Println("  - job:{id}:stage:{id}:task:{id} - DAG state")
	fmt.Println("  - temp:{type}:{id} - Temporary state with TTL")
	fmt.Println()
}

// truncateString truncates a string to max length
func truncateString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// RunStateManagementExamples runs all state management examples
func RunStateManagementExamples() {
	fmt.Println("========================================")
	fmt.Println("State Management Examples")
	fmt.Println("========================================")

	State_Example1_BasicStateOperations()
	State_Example2_StateWithTTL()
	State_Example3_Checkpointing()
	State_Example4_CheckpointBackend()
	State_Example5_SessionManagement()
	State_Example6_ExecutionHistory()
	State_Example7_CompositeStateStore()
	State_Example8_DistributedStatePattern()

	fmt.Println("========================================")
	fmt.Println("Key Takeaways:")
	fmt.Println("========================================")
	fmt.Println("1. Use checkpoints for fault-tolerant long-running operations")
	fmt.Println("2. Sessions provide user context with optimistic locking")
	fmt.Println("3. History enables debugging and audit trails")
	fmt.Println("4. TTL automates cleanup of temporary state")
	fmt.Println("5. Composite stores simplify state management")
	fmt.Println("6. Use consistent key naming for distributed state")
}

func main() {
	RunStateManagementExamples()
}
