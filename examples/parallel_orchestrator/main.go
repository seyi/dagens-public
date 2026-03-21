package main

import (
	"context"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/agents"
)

// Example demonstrating ParallelAgent as an orchestrator with SubAgents
// This matches ADK's ParallelAgent pattern where events are interleaved

func main() {
	// Create sub-agents that will execute in parallel
	searchAgent := createSearchAgent()
	analysisAgent := createAnalysisAgent()
	summaryAgent := createSummaryAgent()

	// Method 1: ADK-compatible SubAgents configuration
	parallelOrchestrator := agents.NewParallelAgent(agents.ParallelAgentConfig{
		Name:        "DataProcessingOrchestrator",
		SubAgents:   []agent.Agent{searchAgent, analysisAgent, summaryAgent},
		Aggregation: agents.AllAggregation,
		Timeout:     30 * time.Second,
	})

	fmt.Println("=== ParallelAgent Orchestrator Example ===")
	fmt.Println("This demonstrates ADK-compatible parallel execution with interleaved events")

	// Execute the orchestrator
	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Process customer feedback data",
		Context: map[string]interface{}{
			"dataset": "customer_reviews_2024",
			"filters": map[string]interface{}{
				"sentiment": "negative",
				"priority":  "high",
			},
		},
	}

	fmt.Println("Executing parallel orchestrator...")
	fmt.Println("Sub-agents will execute concurrently with interleaved events")

	startTime := time.Now()
	output, err := parallelOrchestrator.Execute(ctx, input)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("\n=== Results ===\n")
	fmt.Printf("Total execution time: %.2fs\n", time.Since(startTime).Seconds())
	fmt.Printf("Pattern: %v\n", output.Metadata["pattern"])
	fmt.Printf("Successful agents: %v/%v\n",
		output.Metadata["successful"],
		output.Metadata["total_agents"])

	// Show individual results (demonstrating interleaved completion)
	if results, ok := output.Result.([]interface{}); ok {
		fmt.Printf("\nIndividual Results (in completion order):\n")
		for i, result := range results {
			fmt.Printf("%d. %v\n", i+1, result)
		}
	}

	// Method 2: Using builder API with SubAgents
	fmt.Println("\n=== Builder API Example ===")
	builderOrchestrator := agents.NewParallel(agents.AllAggregation).
		WithName("MultiStageAnalyzer").
		AddMultiple(searchAgent, analysisAgent, summaryAgent).
		WithTimeout(30 * time.Second).
		WithMinSuccessful(2). // Only 2 out of 3 need to succeed
		Build()

	output2, err := builderOrchestrator.Execute(ctx, input)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("Builder orchestrator completed successfully\n")
	fmt.Printf("Results: %v\n", output2.Result)

	// Method 3: First aggregation (race condition pattern)
	fmt.Println("\n=== First Aggregation (Race) Example ===")
	raceOrchestrator := agents.NewParallelAgent(agents.ParallelAgentConfig{
		Name:        "FastestResponseOrchestrator",
		SubAgents:   []agent.Agent{searchAgent, analysisAgent, summaryAgent},
		Aggregation: agents.FirstAggregation, // Return first successful result
		Timeout:     10 * time.Second,
	})

	output3, err := raceOrchestrator.Execute(ctx, input)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("First agent completed with result: %v\n", output3.Result)
	fmt.Printf("Pattern: %v\n", output3.Metadata["pattern"])

	// Method 4: Demonstrating interleaved events with streaming
	fmt.Println("\n=== Interleaved Event Stream Example ===")
	demonstrateInterleavedEvents()
}

// createSearchAgent creates a search agent with simulated work
func createSearchAgent() agent.Agent {
	executor := &mockExecutor{
		name:     "SearchAgent",
		duration: 2 * time.Second,
		work: func(input *agent.AgentInput) interface{} {
			return map[string]interface{}{
				"found_records": 150,
				"query_time":    "2.1s",
			}
		},
	}

	return agent.NewAgent(agent.AgentConfig{
		Name:        "SearchAgent",
		Description: "Searches database for relevant records",
		Executor:    executor,
	})
}

// createAnalysisAgent creates an analysis agent with simulated work
func createAnalysisAgent() agent.Agent {
	executor := &mockExecutor{
		name:     "AnalysisAgent",
		duration: 3 * time.Second,
		work: func(input *agent.AgentInput) interface{} {
			return map[string]interface{}{
				"sentiment_score": -0.35,
				"key_issues":      []string{"shipping delays", "product quality"},
			}
		},
	}

	return agent.NewAgent(agent.AgentConfig{
		Name:        "AnalysisAgent",
		Description: "Analyzes sentiment and extracts insights",
		Executor:    executor,
	})
}

// createSummaryAgent creates a summary agent with simulated work
func createSummaryAgent() agent.Agent {
	executor := &mockExecutor{
		name:     "SummaryAgent",
		duration: 1 * time.Second,
		work: func(input *agent.AgentInput) interface{} {
			return map[string]interface{}{
				"summary": "Critical issues identified in shipping and quality",
				"urgency": "high",
			}
		},
	}

	return agent.NewAgent(agent.AgentConfig{
		Name:        "SummaryAgent",
		Description: "Generates executive summary",
		Executor:    executor,
	})
}

// mockExecutor simulates agent work with configurable duration
type mockExecutor struct {
	name     string
	duration time.Duration
	work     func(*agent.AgentInput) interface{}
}

func (e *mockExecutor) Execute(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	fmt.Printf("[%s] Starting execution...\n", e.name)

	// Simulate work with sleep
	select {
	case <-time.After(e.duration):
		result := e.work(input)
		fmt.Printf("[%s] Completed (took %v)\n", e.name, e.duration)

		return &agent.AgentOutput{
			Result: result,
			Metadata: map[string]interface{}{
				"agent":     e.name,
				"duration":  e.duration.Seconds(),
			},
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// demonstrateInterleavedEvents shows how events from different agents interleave
func demonstrateInterleavedEvents() {
	fmt.Println("Creating agents with different execution times...")

	// Create agents with staggered durations to show interleaving
	fast := createMockAgentWithDuration("FastAgent", 500*time.Millisecond)
	medium := createMockAgentWithDuration("MediumAgent", 1500*time.Millisecond)
	slow := createMockAgentWithDuration("SlowAgent", 2500*time.Millisecond)

	parallel := agents.NewParallelAgent(agents.ParallelAgentConfig{
		Name:        "InterleavedOrchestrator",
		SubAgents:   []agent.Agent{slow, medium, fast}, // Intentionally out of order
		Aggregation: agents.AllAggregation,
		Timeout:     5 * time.Second,
	})

	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Execute with interleaved events",
	}

	fmt.Println("\nStarting parallel execution (watch for interleaved completions):")
	startTime := time.Now()

	output, err := parallel.Execute(ctx, input)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Printf("\nAll agents completed in %.2fs\n", time.Since(startTime).Seconds())
	fmt.Println("Notice how agents completed in different order than they were added!")
	fmt.Println("This demonstrates interleaved event streams from concurrent execution.")

	// Show completion order
	if individualResults, ok := output.Metadata["individual_results"].([]*agent.AgentOutput); ok {
		fmt.Println("\nCompletion order:")
		for i, result := range individualResults {
			if result != nil {
				fmt.Printf("%d. %v (duration: %v)\n",
					i+1,
					result.Metadata["agent"],
					result.Metadata["duration"])
			}
		}
	}
}

func createMockAgentWithDuration(name string, duration time.Duration) agent.Agent {
	executor := &mockExecutor{
		name:     name,
		duration: duration,
		work: func(input *agent.AgentInput) interface{} {
			return fmt.Sprintf("Result from %s", name)
		},
	}

	return agent.NewAgent(agent.AgentConfig{
		Name:        name,
		Description: fmt.Sprintf("Mock agent with %v duration", duration),
		Executor:    executor,
	})
}
