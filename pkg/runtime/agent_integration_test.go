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

package runtime_test

import (
	"context"
	"strings"
	"testing"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/runtime"
)

// TestRuntimeWithSimpleAgent tests runtime orchestration with a simple agent
func TestRuntimeWithSimpleAgent(t *testing.T) {
	// Create a simple agent that emits events
	executor := &testExecutor{
		executeFunc: func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
			// Emit partial events (streaming)
			runtime.EmitMessage(ctx, "Processing...", true)
			runtime.EmitMessage(ctx, "Almost done...", true)

			// Emit final event with state
			runtime.EmitEventWithDelta(ctx, runtime.EventTypeMessage, "Done!", false, map[string]interface{}{
				"result": "success",
			})

			return &agent.AgentOutput{
				Result: "Task completed",
			}, nil
		},
	}

	ag := agent.NewAgent(agent.AgentConfig{
		Name:     "test-agent",
		Executor: executor,
	})

	// Create runtime
	agentRuntime := runtime.NewAgentRuntime(ag, nil)

	// Execute with runtime
	output, err := agentRuntime.Run(context.Background(), &agent.AgentInput{
		Instruction: "Do something",
	})

	if err != nil {
		t.Fatalf("Runtime execution failed: %v", err)
	}

	if output.Result != "Task completed" {
		t.Errorf("Expected 'Task completed', got %v", output.Result)
	}

	// Check that InvocationContext was created
	invCtx, ok := output.Metadata["invocation_context"].(*runtime.InvocationContext)
	if !ok {
		t.Fatal("InvocationContext not found in output metadata")
	}

	// Check events were emitted
	if len(invCtx.EventHistory) != 3 {
		t.Errorf("Expected 3 events, got %d", len(invCtx.EventHistory))
	}

	// Check partial events
	partialCount := 0
	finalCount := 0
	for _, event := range invCtx.EventHistory {
		if event.Partial {
			partialCount++
		} else {
			finalCount++
		}
	}

	if partialCount != 2 {
		t.Errorf("Expected 2 partial events, got %d", partialCount)
	}

	if finalCount != 1 {
		t.Errorf("Expected 1 final event, got %d", finalCount)
	}

	// Check state was updated
	result, ok := invCtx.Get("result")
	if !ok {
		t.Error("State 'result' not found")
	}
	if result != "success" {
		t.Errorf("Expected state 'result' = 'success', got %v", result)
	}
}

// TestRuntimeWithStateManagement tests state management across invocation
func TestRuntimeWithStateManagement(t *testing.T) {
	executor := &testExecutor{
		executeFunc: func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
			// Set state (uncommitted)
			runtime.SetState(ctx, "counter", 1)

			// Dirty read - should see uncommitted change
			counter, ok := runtime.GetState(ctx, "counter")
			if !ok {
				t.Error("Counter not found in dirty read")
			}
			if counter != 1 {
				t.Errorf("Expected counter = 1, got %v", counter)
			}

			// Update state again
			runtime.SetState(ctx, "counter", 2)

			// Dirty read should see latest uncommitted value
			counter, ok = runtime.GetState(ctx, "counter")
			if !ok {
				t.Error("Counter not found in second dirty read")
			}
			if counter != 2 {
				t.Errorf("Expected counter = 2 after update, got %v", counter)
			}

			// Set temporary state
			runtime.SetTempState(ctx, "processing", true)

			// Check temp state
			processing, ok := runtime.GetTempState(ctx, "processing")
			if !ok {
				t.Error("Temp state 'processing' not found")
			}
			if processing != true {
				t.Errorf("Expected processing = true, got %v", processing)
			}

			// Emit final event (triggers commit)
			runtime.EmitEventWithDelta(ctx, runtime.EventTypeMessage, "State updated", false, map[string]interface{}{
				"final_counter": 2,
			})

			return &agent.AgentOutput{
				Result: "State management test passed",
			}, nil
		},
	}

	ag := agent.NewAgent(agent.AgentConfig{
		Name:     "stateful-agent",
		Executor: executor,
	})

	agentRuntime := runtime.NewAgentRuntime(ag, nil)
	output, err := agentRuntime.Run(context.Background(), &agent.AgentInput{})

	if err != nil {
		t.Fatalf("Runtime execution failed: %v", err)
	}

	// Check state was committed
	invCtx := output.Metadata["invocation_context"].(*runtime.InvocationContext)

	counter, ok := invCtx.Get("counter")
	if !ok {
		t.Fatal("Counter not found in committed state")
	}
	if counter != 2 {
		t.Errorf("Expected counter = 2 in committed state, got %v", counter)
	}

	// Temp state should still exist (cleared after invocation by Runtime)
	// But it's accessible from InvocationContext
}

// TestRuntimeWithToolCalls tests event emission for tool calls
func TestRuntimeWithToolCalls(t *testing.T) {
	executor := &testExecutor{
		executeFunc: func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
			// Simulate tool call
			runtime.EmitToolCall(ctx, "web_search", map[string]interface{}{
				"query": "test query",
			})

			// Simulate tool result
			runtime.EmitToolResult(ctx, "web_search", []string{"result1", "result2"}, nil)

			return &agent.AgentOutput{
				Result: "Tool calls completed",
			}, nil
		},
	}

	ag := agent.NewAgent(agent.AgentConfig{
		Name:     "tool-agent",
		Executor: executor,
	})

	agentRuntime := runtime.NewAgentRuntime(ag, nil)
	output, err := agentRuntime.Run(context.Background(), &agent.AgentInput{})

	if err != nil {
		t.Fatalf("Runtime execution failed: %v", err)
	}

	invCtx := output.Metadata["invocation_context"].(*runtime.InvocationContext)

	// Check tool call event
	toolCallFound := false
	toolResultFound := false

	for _, event := range invCtx.EventHistory {
		if event.Type == runtime.EventTypeToolCall {
			toolCallFound = true
		}
		if event.Type == runtime.EventTypeToolResult {
			toolResultFound = true
		}
	}

	if !toolCallFound {
		t.Error("Tool call event not found")
	}

	if !toolResultFound {
		t.Error("Tool result event not found")
	}
}

// TestRuntimeWithSparkPartition tests partition awareness
func TestRuntimeWithSparkPartition(t *testing.T) {
	executor := &testExecutor{
		executeFunc: func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
			runtime.EmitMessage(ctx, "Processing in partition", false)
			return &agent.AgentOutput{Result: "done"}, nil
		},
	}

	ag := agent.NewAgent(agent.AgentConfig{
		Name:     "partition-agent",
		Executor: executor,
	})

	// Create runtime with partition ID (Spark pattern)
	agentRuntime := runtime.NewAgentRuntime(ag, nil).
		WithPartitionID("partition-0")

	output, err := agentRuntime.Run(context.Background(), &agent.AgentInput{})

	if err != nil {
		t.Fatalf("Runtime execution failed: %v", err)
	}

	invCtx := output.Metadata["invocation_context"].(*runtime.InvocationContext)

	// Check partition ID
	if invCtx.PartitionID != "partition-0" {
		t.Errorf("Expected partition ID 'partition-0', got %s", invCtx.PartitionID)
	}

	// Check events have partition ID
	for _, event := range invCtx.EventHistory {
		if event.PartitionID != "partition-0" {
			t.Errorf("Event partition ID mismatch: expected 'partition-0', got %s", event.PartitionID)
		}
	}
}

// TestRuntimeEventLog tests event log retrieval
func TestRuntimeEventLog(t *testing.T) {
	executor := &testExecutor{
		executeFunc: func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
			runtime.EmitMessage(ctx, "Event 1", true)
			runtime.EmitMessage(ctx, "Event 2", true)
			runtime.EmitMessage(ctx, "Event 3", false)
			return &agent.AgentOutput{Result: "done"}, nil
		},
	}

	ag := agent.NewAgent(agent.AgentConfig{
		Name:     "event-agent",
		Executor: executor,
	})

	agentRuntime := runtime.NewAgentRuntime(ag, nil)
	_, err := agentRuntime.Run(context.Background(), &agent.AgentInput{})

	if err != nil {
		t.Fatalf("Runtime execution failed: %v", err)
	}

	// Get event log from runtime
	eventLog := agentRuntime.GetEventLog()

	if len(eventLog) != 3 {
		t.Errorf("Expected 3 events in log, got %d", len(eventLog))
	}
}

// TestStreamingRuntime tests streaming execution
func TestStreamingRuntime(t *testing.T) {
	executor := &testExecutor{
		executeFunc: func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
			runtime.EmitMessage(ctx, "Streaming 1", true)
			runtime.EmitMessage(ctx, "Streaming 2", true)
			runtime.EmitMessage(ctx, "Final result", false)
			return &agent.AgentOutput{Result: "Streaming completed"}, nil
		},
	}

	ag := agent.NewAgent(agent.AgentConfig{
		Name:     "streaming-agent",
		Executor: executor,
	})

	streamingRuntime := runtime.NewStreamingRuntime(ag, nil)
	events := streamingRuntime.RunStreaming(context.Background(), &agent.AgentInput{})

	// Collect events
	var partialEvents []runtime.StreamEvent
	var finalEvents []runtime.StreamEvent

	for event := range events {
		if event.Event.Partial {
			partialEvents = append(partialEvents, event)
		} else {
			finalEvents = append(finalEvents, event)
		}
	}

	// StreamingRuntime emits: "Starting execution..." (partial) + agent events (2 partial, 1 final) + completion event (final)
	// So we expect 3 partial events (1 from runtime, 2 from agent) and 2 final events (1 from agent, 1 from runtime)
	if len(partialEvents) < 2 {
		t.Errorf("Expected at least 2 partial events, got %d", len(partialEvents))
	}

	if len(finalEvents) < 1 {
		t.Errorf("Expected at least 1 final event, got %d", len(finalEvents))
	}

	// Check that at least one final event has output
	hasOutput := false
	for _, event := range finalEvents {
		if event.Final != nil && event.Final.Result == "Streaming completed" {
			hasOutput = true
			break
		}
	}

	if !hasOutput {
		t.Error("No final event with expected output found")
	}
}

// TestRuntimeBackwardCompatibility tests that agents work without runtime
func TestRuntimeBackwardCompatibility(t *testing.T) {
	executor := &testExecutor{
		executeFunc: func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
			// Try to emit events (should be no-op if not runtime context)
			runtime.EmitMessage(ctx, "This is a no-op", true)
			runtime.SetState(ctx, "key", "value") // No-op

			return &agent.AgentOutput{
				Result: "Works without runtime too",
			}, nil
		},
	}

	ag := agent.NewAgent(agent.AgentConfig{
		Name:     "backward-compat-agent",
		Executor: executor,
	})

	// Execute WITHOUT runtime (direct call)
	output, err := ag.Execute(context.Background(), &agent.AgentInput{})

	if err != nil {
		t.Fatalf("Direct execution failed: %v", err)
	}

	if output.Result != "Works without runtime too" {
		t.Errorf("Expected 'Works without runtime too', got %v", output.Result)
	}

	// Should not have InvocationContext
	if _, ok := output.Metadata["invocation_context"]; ok {
		t.Error("Should not have InvocationContext when not using runtime")
	}
}

// TestRuntimeIsRuntimeContext tests context type checking
func TestRuntimeIsRuntimeContext(t *testing.T) {
	// Regular context
	ctx := context.Background()
	if runtime.IsRuntimeContext(ctx) {
		t.Error("Regular context should not be runtime context")
	}

	// InvocationContext
	invCtx := runtime.NewInvocationContext(context.Background(), &agent.AgentInput{})
	if !runtime.IsRuntimeContext(invCtx) {
		t.Error("InvocationContext should be runtime context")
	}

	// GetInvocationContext
	if runtime.GetInvocationContext(ctx) != nil {
		t.Error("Should return nil for regular context")
	}

	if runtime.GetInvocationContext(invCtx) == nil {
		t.Error("Should return InvocationContext for runtime context")
	}
}

// testExecutor is a mock executor for testing
type testExecutor struct {
	executeFunc func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error)
}

func (e *testExecutor) Execute(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	if e.executeFunc != nil {
		return e.executeFunc(ctx, ag, input)
	}
	return &agent.AgentOutput{Result: "test"}, nil
}

// TestEventFiltering tests event filtering helpers
func TestEventFiltering(t *testing.T) {
	executor := &testExecutor{
		executeFunc: func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
			runtime.EmitMessage(ctx, "Partial 1", true)
			runtime.EmitMessage(ctx, "Partial 2", true)
			runtime.EmitMessage(ctx, "Final 1", false)
			runtime.EmitMessage(ctx, "Partial 3", true)
			runtime.EmitMessage(ctx, "Final 2", false)
			return &agent.AgentOutput{Result: "done"}, nil
		},
	}

	ag := agent.NewAgent(agent.AgentConfig{
		Name:     "filter-agent",
		Executor: executor,
	})

	streamingRuntime := runtime.NewStreamingRuntime(ag, nil)
	allEvents := streamingRuntime.RunStreaming(context.Background(), &agent.AgentInput{})

	// Test OnlyFinalEvents filter
	finalEvents := runtime.OnlyFinalEvents(allEvents)

	finalCount := 0
	for range finalEvents {
		finalCount++
	}

	// StreamingRuntime adds a completion event, so we expect at least 2 final events from agent
	if finalCount < 2 {
		t.Errorf("Expected at least 2 final events, got %d", finalCount)
	}
}

// TestMultipleRuntimes tests multiple runtimes (simulating Spark partitions)
func TestMultipleRuntimes(t *testing.T) {
	executor := &testExecutor{
		executeFunc: func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
			runtime.EmitMessage(ctx, "Processing...", false)
			runtime.SetState(ctx, "processed", true)
			return &agent.AgentOutput{Result: "done"}, nil
		},
	}

	ag := agent.NewAgent(agent.AgentConfig{
		Name:     "multi-runtime-agent",
		Executor: executor,
	})

	// Simulate 3 Spark partitions
	partitions := []string{"partition-0", "partition-1", "partition-2"}
	results := make([]*agent.AgentOutput, len(partitions))

	for i, partitionID := range partitions {
		// Each partition gets its own runtime (Spark pattern)
		partitionRuntime := runtime.NewAgentRuntime(ag, nil).
			WithPartitionID(partitionID)

		output, err := partitionRuntime.Run(context.Background(), &agent.AgentInput{
			Instruction: "Process data",
		})

		if err != nil {
			t.Fatalf("Partition %s failed: %v", partitionID, err)
		}

		results[i] = output

		// Verify partition ID
		invCtx := output.Metadata["invocation_context"].(*runtime.InvocationContext)
		if invCtx.PartitionID != partitionID {
			t.Errorf("Expected partition %s, got %s", partitionID, invCtx.PartitionID)
		}
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}
}

// TestEventLineage tests event lineage tracking (Spark-style)
func TestEventLineage(t *testing.T) {
	executor := &testExecutor{
		executeFunc: func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
			// Emit events that could form lineage
			runtime.EmitMessage(ctx, "Step 1", false)
			runtime.EmitMessage(ctx, "Step 2", false)
			return &agent.AgentOutput{Result: "done"}, nil
		},
	}

	ag := agent.NewAgent(agent.AgentConfig{
		Name:     "lineage-agent",
		Executor: executor,
	})

	agentRuntime := runtime.NewAgentRuntime(ag, nil)
	output, err := agentRuntime.Run(context.Background(), &agent.AgentInput{})

	if err != nil {
		t.Fatalf("Runtime execution failed: %v", err)
	}

	invCtx := output.Metadata["invocation_context"].(*runtime.InvocationContext)

	// Check event IDs are unique
	seenIDs := make(map[string]bool)
	for _, event := range invCtx.EventHistory {
		if event.ID == "" {
			t.Error("Event should have non-empty ID")
		}

		if seenIDs[event.ID] {
			t.Errorf("Duplicate event ID: %s", event.ID)
		}
		seenIDs[event.ID] = true

		// Check event has author
		if event.Author == "" {
			t.Error("Event should have author")
		}

		// Check timestamp
		if event.Timestamp.IsZero() {
			t.Error("Event should have timestamp")
		}
	}
}

// TestErrorHandling tests error event emission
func TestErrorHandling(t *testing.T) {
	executor := &testExecutor{
		executeFunc: func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
			// Simulate error
			runtime.EmitEvent(ctx, runtime.EventTypeError, map[string]interface{}{
				"error": "Something went wrong",
				"code":  500,
			}, false)

			// Can still return success after logging error
			return &agent.AgentOutput{Result: "recovered"}, nil
		},
	}

	ag := agent.NewAgent(agent.AgentConfig{
		Name:     "error-agent",
		Executor: executor,
	})

	agentRuntime := runtime.NewAgentRuntime(ag, nil)
	output, err := agentRuntime.Run(context.Background(), &agent.AgentInput{})

	if err != nil {
		t.Fatalf("Runtime execution failed: %v", err)
	}

	invCtx := output.Metadata["invocation_context"].(*runtime.InvocationContext)

	// Check error event was logged
	errorFound := false
	for _, event := range invCtx.EventHistory {
		if event.Type == runtime.EventTypeError {
			errorFound = true

			content, ok := event.Content.(map[string]interface{})
			if !ok {
				t.Error("Error event content should be map")
			}

			if !strings.Contains(content["error"].(string), "Something went wrong") {
				t.Error("Error message not found in event")
			}
		}
	}

	if !errorFound {
		t.Error("Error event not found")
	}
}
