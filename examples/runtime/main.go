//go:build ignore

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

// Example demonstrating ADK-inspired runtime with Spark distributed execution
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/agents"
	"github.com/seyi/dagens/pkg/model"
	"github.com/seyi/dagens/pkg/runtime"
	"github.com/seyi/dagens/pkg/tools"
)

// Example1_BasicRuntime demonstrates the Runtime/Runner pattern
func Example1_BasicRuntime() {
	ctx := context.Background()

	// Create agent
	modelProvider := model.NewOpenAIProvider("gpt-4", "your-key")
	toolRegistry := tools.NewToolRegistry()

	agent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "assistant",
		Instruction: "Help the user with their request",
	}, modelProvider, toolRegistry)

	// Create runtime (ADK-inspired orchestrator)
	agentRuntime := runtime.NewAgentRuntime(agent, nil)

	// Execute with runtime coordination
	output, err := agentRuntime.Run(ctx, &agent.AgentInput{
		Instruction: "What is 2+2?",
	})

	if err != nil {
		log.Fatalf("Execution failed: %v", err)
	}

	fmt.Printf("Result: %v\n", output.Result)

	// Examine execution metadata
	if invCtx, ok := output.Metadata["invocation_context"].(*runtime.InvocationContext); ok {
		fmt.Printf("Events: %d\n", len(invCtx.EventHistory))
		fmt.Printf("State changes: %d\n", len(invCtx.StateDeltas))
	}
}

// Example2_StreamingExecution demonstrates real-time event streaming
func Example2_StreamingExecution() {
	ctx := context.Background()

	// Create agent
	modelProvider := model.NewOpenAIProvider("gpt-4", "your-key")
	agent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "writer",
		Instruction: "Write a detailed response",
	}, modelProvider, nil)

	// Create streaming runtime
	streamingRuntime := runtime.NewStreamingRuntime(agent, nil)

	// Execute with streaming
	events := streamingRuntime.RunStreaming(ctx, &agent.AgentInput{
		Instruction: "Explain quantum computing",
	})

	// Process events in real-time
	for event := range events {
		if event.Event.Partial {
			// Stream to UI (like ADK's partial events)
			fmt.Printf("Streaming: %v\n", event.Event.Content)
		} else {
			// Final event - commit to database
			fmt.Printf("Final: %v\n", event.Event.Content)

			if event.Final != nil {
				fmt.Printf("Execution complete: %v\n", event.Final.Result)
			}
		}
	}
}

// Example3_StateManagement demonstrates ADK-style state management
func Example3_StateManagement() {
	ctx := context.Background()

	// Create custom agent that uses state
	type StatefulAgent struct {
		*agent.BaseAgent
	}

	statefulAgent := &StatefulAgent{
		BaseAgent: agent.NewAgent(agent.AgentConfig{
			Name:        "stateful",
			Description: "Agent with state",
		}).(*agent.BaseAgent),
	}

	// Implement Execute with state usage
	execute := func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
		// Get invocation context (ADK pattern)
		invCtx, ok := ctx.(*runtime.InvocationContext)
		if !ok {
			return &agent.AgentOutput{Result: "No context"}, nil
		}

		// Use state (ADK pattern)
		count, _ := invCtx.Get("visit_count")
		if count == nil {
			count = 0
		}

		// Update state (uncommitted)
		visitCount := count.(int) + 1
		invCtx.Set("visit_count", visitCount)

		// Dirty read - see uncommitted change
		newCount, _ := invCtx.Get("visit_count")

		// Set temporary state (task-local, ADK pattern)
		invCtx.Set("temp:processing", true)

		return &agent.AgentOutput{
			Result: fmt.Sprintf("Visit %d", newCount),
		}, nil
	}

	_ = execute // Use in real implementation

	// Runtime will commit state after execution
	agentRuntime := runtime.NewAgentRuntime(statefulAgent, nil)
	output, _ := agentRuntime.Run(ctx, &agent.AgentInput{})

	fmt.Printf("Result: %v\n", output.Result)
}

// Example4_DistributedWithSpark demonstrates Spark integration
func Example4_DistributedWithSpark() {
	// Simulate Spark RDD operation
	inputs := []agent.AgentInput{
		{Instruction: "Task 1"},
		{Instruction: "Task 2"},
		{Instruction: "Task 3"},
	}

	// Create agent
	modelProvider := model.NewOpenAIProvider("gpt-4", "your-key")
	ag := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name: "worker",
	}, modelProvider, nil)

	// Simulate Spark map operation
	// In real Spark: inputRDD.map(processWithRuntime)
	results := make([]agent.AgentOutput, len(inputs))

	for i, input := range inputs {
		// Each Spark task gets its own Runtime
		taskRuntime := runtime.NewAgentRuntime(ag, nil).
			WithPartitionID(fmt.Sprintf("partition-%d", i))

		output, err := taskRuntime.Run(context.Background(), &input)
		if err != nil {
			log.Printf("Task %d failed: %v", i, err)
			continue
		}

		results[i] = *output

		// Get partition-specific metrics
		eventLog := taskRuntime.GetEventLog()
		fmt.Printf("Partition %d: %d events\n", i, len(eventLog))
	}

	fmt.Printf("Processed %d tasks\n", len(results))
}

// Example5_EventDrivenCheckpoints demonstrates checkpointing
func Example5_EventDrivenCheckpoints() {
	ctx := context.Background()

	// Create custom agent that emits checkpoint events
	type CheckpointAgent struct {
		*agent.BaseAgent
	}

	checkpointAgent := &CheckpointAgent{
		BaseAgent: agent.NewAgent(agent.AgentConfig{
			Name: "checkpoint-agent",
		}).(*agent.BaseAgent),
	}

	// Runtime with checkpoint support
	agentRuntime := runtime.NewAgentRuntime(checkpointAgent, nil).
		WithPartitionID("partition-0")

	// Execute - runtime will handle checkpoint events
	output, err := agentRuntime.Run(ctx, &agent.AgentInput{
		Instruction: "Long running task",
	})

	if err != nil {
		log.Fatalf("Failed: %v", err)
	}

	// Events logged for fault tolerance (Spark-style)
	eventLog := agentRuntime.GetEventLog()
	fmt.Printf("Checkpoints: %d\n", countCheckpoints(eventLog))

	_ = output
}

func countCheckpoints(events []runtime.Event) int {
	count := 0
	for _, e := range events {
		if e.Type == runtime.EventTypeCheckpoint {
			count++
		}
	}
	return count
}

// Example6_StreamingWithFilters demonstrates event filtering
func Example6_StreamingWithFilters() {
	ctx := context.Background()

	modelProvider := model.NewOpenAIProvider("gpt-4", "your-key")
	agent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name: "talker",
	}, modelProvider, nil)

	streamingRuntime := runtime.NewStreamingRuntime(agent, nil)

	// Get streaming events
	allEvents := streamingRuntime.RunStreaming(ctx, &agent.AgentInput{
		Instruction: "Tell me a story",
	})

	// Filter to only final events (commits only)
	finalEvents := runtime.OnlyFinalEvents(allEvents)

	// Process only commits, ignore UI streaming
	for event := range finalEvents {
		fmt.Printf("Committed: %v\n", event.Event.Content)

		if event.Event.StateDelta != nil {
			fmt.Printf("State changes: %v\n", event.Event.StateDelta)
		}
	}
}

// Example7_DistributedStateAggregation shows cross-partition aggregation
func Example7_DistributedStateAggregation() {
	partitionOutputs := []agent.AgentOutput{
		{Metadata: map[string]interface{}{"partition_id": "0", "count": 10}},
		{Metadata: map[string]interface{}{"partition_id": "1", "count": 15}},
		{Metadata: map[string]interface{}{"partition_id": "2", "count": 8}},
	}

	// Aggregate across partitions (Spark reduce pattern)
	totalCount := 0
	for _, output := range partitionOutputs {
		count := output.Metadata["count"].(int)
		totalCount += count
	}

	fmt.Printf("Total across partitions: %d\n", totalCount)

	// This demonstrates how ADK's per-invocation state
	// works with Spark's cross-partition aggregation
}

// Example8_HybridExecutionModel shows ADK + Spark together
func Example8_HybridExecutionModel() {
	// ADK Runtime handles intra-task execution
	// Spark handles inter-task coordination

	fmt.Println("=== Hybrid Execution Model ===")
	fmt.Println()
	fmt.Println("Within Spark Task:")
	fmt.Println("  - AgentRuntime orchestrates agent")
	fmt.Println("  - InvocationContext manages state")
	fmt.Println("  - Events provide pause/resume")
	fmt.Println("  - Checkpoints for fault tolerance")
	fmt.Println()
	fmt.Println("Across Spark Tasks:")
	fmt.Println("  - RDD transformations (map, reduce)")
	fmt.Println("  - Partition-aware execution")
	fmt.Println("  - Distributed state aggregation")
	fmt.Println("  - DAG optimization")
	fmt.Println()
	fmt.Println("Best of both worlds!")
}
