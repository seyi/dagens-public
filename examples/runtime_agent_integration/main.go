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

// Runtime + Agent Integration Examples
// Demonstrates Phase 2 integration: Agents working with ADK-inspired Runtime
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/agents"
	"github.com/seyi/dagens/pkg/runtime"
)

func main() {
	fmt.Println("=== Runtime + Agent Integration Examples ===")
	fmt.Println()

	// Example 1: Simple Agent with Runtime Orchestration
	fmt.Println("Example 1: Simple Agent with Runtime")
	example1_SimpleAgentWithRuntime()
	fmt.Println()

	// Example 2: Agent with State Management
	fmt.Println("Example 2: State Management")
	example2_StateManagement()
	fmt.Println()

	// Example 3: Streaming Events
	fmt.Println("Example 3: Streaming Events")
	example3_StreamingEvents()
	fmt.Println()

	// Example 4: Sequential Agents with Runtime
	fmt.Println("Example 4: Sequential Agents with Runtime")
	example4_SequentialWithRuntime()
	fmt.Println()

	// Example 5: Distributed Execution (Spark Pattern)
	fmt.Println("Example 5: Distributed Execution")
	example5_DistributedExecution()
	fmt.Println()

	// Example 6: Event Inspection and Debugging
	fmt.Println("Example 6: Event Inspection")
	example6_EventInspection()
	fmt.Println()

	fmt.Println("=== All Examples Complete! ===")
}

// Example 1: Simple agent with runtime orchestration
func example1_SimpleAgentWithRuntime() {
	// Create a simple agent
	executor := &simpleExecutor{
		name: "greeter",
		logic: func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
			// Runtime integration: Emit partial event (streaming)
			runtime.EmitMessage(ctx, "Preparing greeting...", true)

			// Do some work
			time.Sleep(50 * time.Millisecond)

			// Runtime integration: Emit final event with state
			greeting := fmt.Sprintf("Hello, %s!", input.Instruction)
			runtime.EmitEventWithDelta(ctx, runtime.EventTypeMessage, greeting, false, map[string]interface{}{
				"greeting":  greeting,
				"timestamp": time.Now(),
			})

			return &agent.AgentOutput{
				Result: greeting,
			}, nil
		},
	}

	ag := agent.NewAgent(agent.AgentConfig{
		Name:     "greeter",
		Executor: executor,
	})

	// Create runtime (ADK-inspired orchestrator)
	agentRuntime := runtime.NewAgentRuntime(ag, nil)

	// Execute with runtime coordination
	output, err := agentRuntime.Run(context.Background(), &agent.AgentInput{
		Instruction: "World",
	})

	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Result: %v\n", output.Result)

	// Examine runtime metadata
	invCtx := output.Metadata["invocation_context"].(*runtime.InvocationContext)
	fmt.Printf("Events emitted: %d\n", len(invCtx.EventHistory))
	fmt.Printf("State changes: %d\n", len(invCtx.StateDeltas))

	// Check state
	greeting, ok := invCtx.Get("greeting")
	if ok {
		fmt.Printf("Stored greeting: %v\n", greeting)
	}
}

// Example 2: Agent with state management
func example2_StateManagement() {
	executor := &simpleExecutor{
		name: "counter",
		logic: func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
			// Get current count (with dirty read support)
			count, ok := runtime.GetState(ctx, "count")
			if !ok {
				count = 0
			}

			// Increment count
			newCount := count.(int) + 1
			runtime.SetState(ctx, "count", newCount)

			// Set temporary state (task-local, auto-cleaned)
			runtime.SetTempState(ctx, "processing", true)

			// Emit final event with state
			runtime.EmitEventWithDelta(ctx, runtime.EventTypeMessage, fmt.Sprintf("Count: %d", newCount), false, map[string]interface{}{
				"count": newCount,
			})

			return &agent.AgentOutput{
				Result: fmt.Sprintf("Count is now %d", newCount),
			}, nil
		},
	}

	ag := agent.NewAgent(agent.AgentConfig{
		Name:     "counter",
		Executor: executor,
	})

	agentRuntime := runtime.NewAgentRuntime(ag, nil)

	// Run multiple times to see state accumulation
	for i := 0; i < 3; i++ {
		output, _ := agentRuntime.Run(context.Background(), &agent.AgentInput{})
		fmt.Printf("Run %d: %v\n", i+1, output.Result)

		invCtx := output.Metadata["invocation_context"].(*runtime.InvocationContext)
		count, _ := invCtx.Get("count")
		fmt.Printf("  Final count in state: %v\n", count)
	}
}

// Example 3: Streaming events for real-time feedback
func example3_StreamingEvents() {
	executor := &simpleExecutor{
		name: "analyzer",
		logic: func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
			// Emit multiple partial events (like ADK streaming)
			steps := []string{
				"Loading data...",
				"Analyzing patterns...",
				"Computing results...",
				"Finalizing report...",
			}

			for i, step := range steps {
				runtime.EmitMessage(ctx, step, true)
				time.Sleep(30 * time.Millisecond)

				// Update progress
				progress := float64(i+1) / float64(len(steps))
				runtime.SetTempState(ctx, "progress", progress)
			}

			// Emit final result
			runtime.EmitMessage(ctx, "Analysis complete!", false)

			return &agent.AgentOutput{
				Result: "Analysis completed successfully",
			}, nil
		},
	}

	ag := agent.NewAgent(agent.AgentConfig{
		Name:     "analyzer",
		Executor: executor,
	})

	// Use streaming runtime
	streamingRuntime := runtime.NewStreamingRuntime(ag, nil)
	events := streamingRuntime.RunStreaming(context.Background(), &agent.AgentInput{
		Instruction: "Analyze data",
	})

	// Process events in real-time
	fmt.Println("Real-time events:")
	for event := range events {
		if event.Event.Partial {
			fmt.Printf("  [Streaming] %v\n", event.Event.Content)
		} else {
			fmt.Printf("  [Final] %v\n", event.Event.Content)

			if event.Final != nil {
				fmt.Printf("  Result: %v\n", event.Final.Result)
			}
		}
	}
}

// Example 4: Sequential agents with runtime coordination
func example4_SequentialWithRuntime() {
	// Create sub-agents
	agent1 := createSimpleAgent("step1", "Step 1 completed")
	agent2 := createSimpleAgent("step2", "Step 2 completed")
	agent3 := createSimpleAgent("step3", "Step 3 completed")

	// Create sequential agent
	seqAgent := agents.NewSequentialAgent(agents.SequentialAgentConfig{
		Name:       "sequential",
		SubAgents:  []agent.Agent{agent1, agent2, agent3},
		PassOutput: true,
	})

	// Execute with runtime
	agentRuntime := runtime.NewAgentRuntime(seqAgent, nil)
	output, _ := agentRuntime.Run(context.Background(), &agent.AgentInput{
		Instruction: "Run pipeline",
	})

	fmt.Printf("Result: %v\n", output.Result)

	// Examine events
	invCtx := output.Metadata["invocation_context"].(*runtime.InvocationContext)
	fmt.Printf("Total events: %d\n", len(invCtx.EventHistory))

	// Show event timeline
	fmt.Println("Event timeline:")
	for i, evt := range invCtx.EventHistory {
		eventType := "final"
		if evt.Partial {
			eventType = "partial"
		}
		fmt.Printf("  %d. [%s] %s: %v\n", i+1, eventType, evt.Type, evt.Content)
	}
}

// Example 5: Distributed execution (Spark pattern)
func example5_DistributedExecution() {
	executor := &simpleExecutor{
		name: "worker",
		logic: func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
			runtime.EmitMessage(ctx, "Processing task...", true)

			// Simulate work
			time.Sleep(20 * time.Millisecond)

			runtime.EmitMessage(ctx, "Task complete", false)

			return &agent.AgentOutput{
				Result: fmt.Sprintf("Processed: %s", input.Instruction),
			}, nil
		},
	}

	ag := agent.NewAgent(agent.AgentConfig{
		Name:     "worker",
		Executor: executor,
	})

	// Simulate Spark RDD with 3 partitions
	inputs := []agent.AgentInput{
		{Instruction: "Task 1"},
		{Instruction: "Task 2"},
		{Instruction: "Task 3"},
	}

	fmt.Println("Simulating Spark distributed execution:")

	results := make([]*agent.AgentOutput, len(inputs))
	for i, input := range inputs {
		// Each Spark task gets its own Runtime (Spark pattern)
		taskRuntime := runtime.NewAgentRuntime(ag, nil).
			WithPartitionID(fmt.Sprintf("partition-%d", i))

		output, err := taskRuntime.Run(context.Background(), &input)
		if err != nil {
			fmt.Printf("  Partition %d failed: %v\n", i, err)
			continue
		}

		results[i] = output

		// Get partition-specific metrics
		invCtx := output.Metadata["invocation_context"].(*runtime.InvocationContext)
		eventLog := taskRuntime.GetEventLog()

		fmt.Printf("  Partition %d: %v (events: %d)\n", i, output.Result, len(eventLog))
		fmt.Printf("    Context Partition ID: %s\n", invCtx.PartitionID)
	}

	fmt.Printf("Processed %d tasks across %d partitions\n", len(results), len(inputs))
}

// Example 6: Event inspection and debugging
func example6_EventInspection() {
	executor := &simpleExecutor{
		name: "inspector",
		logic: func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
			// Emit various event types
			runtime.EmitMessage(ctx, "Starting inspection", true)

			runtime.EmitToolCall(ctx, "web_search", map[string]interface{}{
				"query": "test",
			})

			runtime.EmitToolResult(ctx, "web_search", []string{"result1", "result2"}, nil)

			runtime.EmitStateChange(ctx, "status", "idle", "working")

			runtime.EmitCheckpoint(ctx, map[string]interface{}{
				"checkpoint": "mid-execution",
			})

			runtime.EmitMessage(ctx, "Inspection complete", false)

			return &agent.AgentOutput{
				Result: "Inspection completed",
			}, nil
		},
	}

	ag := agent.NewAgent(agent.AgentConfig{
		Name:     "inspector",
		Executor: executor,
	})

	agentRuntime := runtime.NewAgentRuntime(ag, nil).
		WithPartitionID("partition-0")

	output, _ := agentRuntime.Run(context.Background(), &agent.AgentInput{})

	invCtx := output.Metadata["invocation_context"].(*runtime.InvocationContext)

	fmt.Println("Event Analysis:")
	fmt.Printf("Total events: %d\n", len(invCtx.EventHistory))

	// Count event types
	eventTypes := make(map[runtime.EventType]int)
	partialCount := 0
	finalCount := 0

	for _, evt := range invCtx.EventHistory {
		eventTypes[evt.Type]++
		if evt.Partial {
			partialCount++
		} else {
			finalCount++
		}
	}

	fmt.Println("\nEvent breakdown:")
	for evtType, count := range eventTypes {
		fmt.Printf("  %s: %d\n", evtType, count)
	}

	fmt.Printf("\nPartial events: %d\n", partialCount)
	fmt.Printf("Final events: %d\n", finalCount)

	// Show event details
	fmt.Println("\nDetailed event log:")
	for i, evt := range invCtx.EventHistory {
		partial := ""
		if evt.Partial {
			partial = " (partial)"
		}
		fmt.Printf("%d. [%s%s] %v\n", i+1, evt.Type, partial, evt.Content)
		if evt.StateDelta != nil {
			fmt.Printf("   State delta: %v\n", evt.StateDelta)
		}
	}

	// Show state
	fmt.Println("\nFinal state:")
	status, ok := invCtx.Get("status")
	if ok {
		fmt.Printf("  status = %v\n", status)
	}
}

// Helper: Create a simple agent
func createSimpleAgent(name, message string) agent.Agent {
	executor := &simpleExecutor{
		name: name,
		logic: func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
			runtime.EmitMessage(ctx, fmt.Sprintf("%s: %s", name, message), false)
			return &agent.AgentOutput{Result: message}, nil
		},
	}

	return agent.NewAgent(agent.AgentConfig{
		Name:     name,
		Executor: executor,
	})
}

// simpleExecutor is a test executor
type simpleExecutor struct {
	name  string
	logic func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error)
}

func (e *simpleExecutor) Execute(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	return e.logic(ctx, input)
}
