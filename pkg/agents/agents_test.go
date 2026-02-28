package agents

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/model"
	"github.com/seyi/dagens/pkg/tools"
)

// Mock model provider for testing
type mockModelProvider struct {
	response string
}

func (m *mockModelProvider) Name() string {
	return "mock-model"
}

func (m *mockModelProvider) Generate(ctx context.Context, input *model.ModelInput) (*model.ModelOutput, error) {
	return &model.ModelOutput{
		Text:         m.response,
		TokensUsed:   10,
		FinishReason: "complete",
	}, nil
}

// Mock executor for testing
type mockExecutor struct {
	result interface{}
	delay  time.Duration
}

func (e *mockExecutor) Execute(ctx context.Context, _ agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	if e.delay > 0 {
		time.Sleep(e.delay)
	}

	return &agent.AgentOutput{
		Result: e.result,
	}, nil
}

// TestLlmAgent tests LLM agent functionality
func TestLlmAgent(t *testing.T) {
	t.Run("Simple Generation", func(t *testing.T) {
		mockModel := &mockModelProvider{response: "Hello, I am an AI assistant."}
		toolRegistry := tools.NewToolRegistry()

		llmAgent := NewLlmAgent(LlmAgentConfig{
			Name:        "test-llm",
			ModelName:   "mock",
			Instruction: "You are a helpful assistant",
			Temperature: 0.7,
		}, mockModel, toolRegistry)

		ctx := context.Background()
		input := &agent.AgentInput{
			Instruction: "Say hello",
		}

		output, err := llmAgent.Execute(ctx, input)
		if err != nil {
			t.Fatalf("LLM execution failed: %v", err)
		}

		if output.Result == nil {
			t.Error("Expected non-nil result")
		}

		if output.Metadata["model"] != "mock-model" {
			t.Error("Model metadata not set correctly")
		}
	})

	t.Run("With Temperature", func(t *testing.T) {
		mockModel := &mockModelProvider{response: "Response"}
		toolRegistry := tools.NewToolRegistry()

		llmAgent := NewLlmAgent(LlmAgentConfig{
			Name:        "test-llm",
			ModelName:   "mock",
			Temperature: 0.9,
		}, mockModel, toolRegistry)

		if llmAgent.temperature != 0.9 {
			t.Errorf("Expected temperature 0.9, got %f", llmAgent.temperature)
		}

		newAgent := llmAgent.WithTemperature(0.2)
		if newAgent.temperature != 0.2 {
			t.Errorf("WithTemperature failed: expected 0.2, got %f", newAgent.temperature)
		}
	})

	t.Run("ReAct Pattern", func(t *testing.T) {
		mockModel := &mockModelProvider{
			response: "Thought: I should answer\nAction: Final Answer\nAction Input: The answer is 42",
		}
		toolRegistry := tools.NewToolRegistry()

		llmAgent := NewLlmAgent(LlmAgentConfig{
			Name:          "test-react",
			ModelName:     "mock",
			UseReAct:      true,
			MaxIterations: 3,
		}, mockModel, toolRegistry)

		ctx := context.Background()
		input := &agent.AgentInput{
			Instruction: "What is the answer?",
		}

		output, err := llmAgent.Execute(ctx, input)
		if err != nil {
			t.Fatalf("ReAct execution failed: %v", err)
		}

		if output.Metadata["pattern"] != "ReAct" {
			t.Error("ReAct pattern metadata not set")
		}

		if output.Metadata["iterations"] == nil {
			t.Error("Iterations metadata not set")
		}
	})
}

// TestSequentialAgent tests sequential execution
func TestSequentialAgent(t *testing.T) {
	t.Run("Basic Sequential", func(t *testing.T) {
		agent1 := agent.NewAgent(agent.AgentConfig{
			Name:     "agent1",
			Executor: &mockExecutor{result: "Result 1"},
		})

		agent2 := agent.NewAgent(agent.AgentConfig{
			Name:     "agent2",
			Executor: &mockExecutor{result: "Result 2"},
		})

		agent3 := agent.NewAgent(agent.AgentConfig{
			Name:     "agent3",
			Executor: &mockExecutor{result: "Result 3"},
		})

		seqAgent := NewSequentialAgent(SequentialAgentConfig{
			Name:   "sequential",
			Agents: []agent.Agent{agent1, agent2, agent3},
		})

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "Run pipeline"}

		output, err := seqAgent.Execute(ctx, input)
		if err != nil {
			t.Fatalf("Sequential execution failed: %v", err)
		}

		if output.Metadata["pattern"] != "Sequential" {
			t.Error("Sequential pattern metadata not set")
		}

		if output.Metadata["steps_completed"] != 3 {
			t.Errorf("Expected 3 steps completed, got %v", output.Metadata["steps_completed"])
		}
	})

	t.Run("With Output Passing", func(t *testing.T) {
		agent1 := agent.NewAgent(agent.AgentConfig{
			Name:     "agent1",
			Executor: &mockExecutor{result: "Step 1"},
		})

		agent2 := agent.NewAgent(agent.AgentConfig{
			Name:     "agent2",
			Executor: &mockExecutor{result: "Step 2"},
		})

		seqAgent := NewSequentialAgent(SequentialAgentConfig{
			Name:       "sequential",
			Agents:     []agent.Agent{agent1, agent2},
			PassOutput: true,
		})

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "Run"}

		output, err := seqAgent.Execute(ctx, input)
		if err != nil {
			t.Fatalf("Sequential execution failed: %v", err)
		}

		// With PassOutput, should return last result
		if output.Result != "Step 2" {
			t.Errorf("Expected 'Step 2', got %v", output.Result)
		}
	})

	t.Run("Builder API", func(t *testing.T) {
		agent1 := agent.NewAgent(agent.AgentConfig{
			Name:     "agent1",
			Executor: &mockExecutor{result: "A"},
		})

		agent2 := agent.NewAgent(agent.AgentConfig{
			Name:     "agent2",
			Executor: &mockExecutor{result: "B"},
		})

		seqAgent := NewSequential().
			WithName("pipeline").
			Add(agent1).
			Add(agent2).
			WithPassOutput(true).
			Build()

		if seqAgent.Name() != "pipeline" {
			t.Errorf("Expected name 'pipeline', got %s", seqAgent.Name())
		}

		if !seqAgent.passOutput {
			t.Error("PassOutput not set")
		}
	})
}

// TestParallelAgent tests parallel execution
func TestParallelAgent(t *testing.T) {
	t.Run("All Aggregation", func(t *testing.T) {
		agent1 := agent.NewAgent(agent.AgentConfig{
			Name:     "agent1",
			Executor: &mockExecutor{result: "Result 1"},
		})

		agent2 := agent.NewAgent(agent.AgentConfig{
			Name:     "agent2",
			Executor: &mockExecutor{result: "Result 2"},
		})

		agent3 := agent.NewAgent(agent.AgentConfig{
			Name:     "agent3",
			Executor: &mockExecutor{result: "Result 3"},
		})

		parallelAgent := NewParallelAgent(ParallelAgentConfig{
			Name:        "parallel",
			Agents:      []agent.Agent{agent1, agent2, agent3},
			Aggregation: AllAggregation,
		})

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "Run parallel"}

		output, err := parallelAgent.Execute(ctx, input)
		if err != nil {
			t.Fatalf("Parallel execution failed: %v", err)
		}

		results, ok := output.Result.([]interface{})
		if !ok {
			t.Fatal("Expected array result")
		}

		if len(results) != 3 {
			t.Errorf("Expected 3 results, got %d", len(results))
		}

		if output.Metadata["pattern"] != "Parallel" {
			t.Error("Parallel pattern metadata not set")
		}
	})

	t.Run("First Aggregation", func(t *testing.T) {
		agent1 := agent.NewAgent(agent.AgentConfig{
			Name:     "slow",
			Executor: &mockExecutor{result: "Slow result", delay: 100 * time.Millisecond},
		})

		agent2 := agent.NewAgent(agent.AgentConfig{
			Name:     "fast",
			Executor: &mockExecutor{result: "Fast result", delay: 10 * time.Millisecond},
		})

		parallelAgent := NewParallelAgent(ParallelAgentConfig{
			Name:        "parallel-first",
			Agents:      []agent.Agent{agent1, agent2},
			Aggregation: FirstAggregation,
		})

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "Run"}

		start := time.Now()
		output, err := parallelAgent.Execute(ctx, input)
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("Parallel execution failed: %v", err)
		}

		// Should complete quickly (around 10ms, not 100ms)
		if elapsed > 80*time.Millisecond {
			t.Errorf("First aggregation took too long: %v", elapsed)
		}

		// Result should be from fast agent
		if output.Result != "Fast result" {
			t.Errorf("Expected 'Fast result', got %v", output.Result)
		}
	})

	t.Run("Concat Aggregation", func(t *testing.T) {
		agent1 := agent.NewAgent(agent.AgentConfig{
			Name:     "agent1",
			Executor: &mockExecutor{result: "Part 1"},
		})

		agent2 := agent.NewAgent(agent.AgentConfig{
			Name:     "agent2",
			Executor: &mockExecutor{result: "Part 2"},
		})

		parallelAgent := NewParallelAgent(ParallelAgentConfig{
			Name:        "parallel-concat",
			Agents:      []agent.Agent{agent1, agent2},
			Aggregation: ConcatAggregation,
		})

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "Run"}

		output, err := parallelAgent.Execute(ctx, input)
		if err != nil {
			t.Fatalf("Parallel execution failed: %v", err)
		}

		result, ok := output.Result.(string)
		if !ok {
			t.Fatal("Expected string result")
		}

		if result != "Part 1\n\nPart 2" && result != "Part 2\n\nPart 1" {
			t.Errorf("Unexpected concatenated result: %s", result)
		}
	})

	t.Run("Builder API", func(t *testing.T) {
		agent1 := agent.NewAgent(agent.AgentConfig{
			Name:     "agent1",
			Executor: &mockExecutor{result: "A"},
		})

		agent2 := agent.NewAgent(agent.AgentConfig{
			Name:     "agent2",
			Executor: &mockExecutor{result: "B"},
		})

		parallelAgent := NewParallel(AllAggregation).
			WithName("parallel-test").
			Add(agent1).
			Add(agent2).
			WithTimeout(30 * time.Second).
			WithMinSuccessful(1).
			Build()

		if parallelAgent.Name() != "parallel-test" {
			t.Errorf("Expected name 'parallel-test', got %s", parallelAgent.Name())
		}

		if parallelAgent.minSuccessful != 1 {
			t.Errorf("Expected minSuccessful 1, got %d", parallelAgent.minSuccessful)
		}
	})
}

// TestLoopAgent tests iterative execution
func TestLoopAgent(t *testing.T) {
	t.Run("Until Condition", func(t *testing.T) {
		iteration := 0

		// Update result based on iteration
		innerAgent := agent.NewAgent(agent.AgentConfig{
			Name: "counter",
			Executor: agent.ExecutorFunc(func(ctx context.Context, _ agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				iteration++
				return &agent.AgentOutput{
					Result: iteration,
					Metadata: map[string]interface{}{
						"count": iteration,
					},
				}, nil
			}),
		})

		loopAgent := NewLoopAgent(LoopAgentConfig{
			Name:       "loop-until",
			InnerAgent: innerAgent,
			LoopType:   UntilLoop,
			Condition: func(output *agent.AgentOutput, iter int) bool {
				count := output.Metadata["count"].(int)
				return count >= 5
			},
			MaxIterations: 10,
		})

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "Count"}

		output, err := loopAgent.Execute(ctx, input)
		if err != nil {
			t.Fatalf("Loop execution failed: %v", err)
		}

		if output.Metadata["pattern"] != "Loop" {
			t.Error("Loop pattern metadata not set")
		}

		iterations := output.Metadata["iterations"].(int)
		if iterations != 5 {
			t.Errorf("Expected 5 iterations, got %d", iterations)
		}
	})

	t.Run("Max Iterations", func(t *testing.T) {
		innerAgent := agent.NewAgent(agent.AgentConfig{
			Name:     "inner",
			Executor: &mockExecutor{result: "iteration"},
		})

		loopAgent := NewLoopAgent(LoopAgentConfig{
			Name:       "loop-max",
			InnerAgent: innerAgent,
			LoopType:   WhileLoop,
			Condition: func(output *agent.AgentOutput, iter int) bool {
				return true // Always continue
			},
			MaxIterations: 3,
		})

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "Loop"}

		output, err := loopAgent.Execute(ctx, input)
		if err != nil {
			t.Fatalf("Loop execution failed: %v", err)
		}

		iterations := output.Metadata["iterations"].(int)
		if iterations != 3 {
			t.Errorf("Expected 3 iterations (max), got %d", iterations)
		}
	})

	t.Run("Accumulate All", func(t *testing.T) {
		count := 0
		innerAgent := agent.NewAgent(agent.AgentConfig{
			Name: "incrementer",
			Executor: agent.ExecutorFunc(func(ctx context.Context, _ agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				count++
				return &agent.AgentOutput{
					Result: fmt.Sprintf("Result %d", count),
				}, nil
			}),
		})

		loopAgent := NewLoopAgent(LoopAgentConfig{
			Name:           "loop-accumulate",
			InnerAgent:     innerAgent,
			MaxIterations:  3,
			AccumulateMode: AccumulateAll,
			Condition: func(output *agent.AgentOutput, iter int) bool {
				return iter < 3
			},
		})

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "Loop"}

		output, err := loopAgent.Execute(ctx, input)
		if err != nil {
			t.Fatalf("Loop execution failed: %v", err)
		}

		results, ok := output.Result.([]interface{})
		if !ok {
			t.Fatal("Expected array of results")
		}

		if len(results) != 3 {
			t.Errorf("Expected 3 accumulated results, got %d", len(results))
		}
	})

	t.Run("Builder API", func(t *testing.T) {
		innerAgent := agent.NewAgent(agent.AgentConfig{
			Name:     "inner",
			Executor: &mockExecutor{result: "test"},
		})

		loopAgent := NewLoop(innerAgent).
			WithName("test-loop").
			Until(func(output *agent.AgentOutput, iter int) bool {
				return iter >= 5
			}).
			MaxIterations(10).
			AccumulateAll().
			Build()

		if loopAgent.Name() != "test-loop" {
			t.Errorf("Expected name 'test-loop', got %s", loopAgent.Name())
		}

		if loopAgent.loopType != UntilLoop {
			t.Errorf("Expected UntilLoop, got %v", loopAgent.loopType)
		}

		if loopAgent.accumulateMode != AccumulateAll {
			t.Errorf("Expected AccumulateAll, got %v", loopAgent.accumulateMode)
		}
	})
}

// TestConditionHelpers tests loop condition helpers
func TestConditionHelpers(t *testing.T) {
	t.Run("ScoreThresholdCondition", func(t *testing.T) {
		condition := ScoreThresholdCondition("quality_score", 0.9)

		output1 := &agent.AgentOutput{
			Metadata: map[string]interface{}{
				"quality_score": 0.7,
			},
		}

		output2 := &agent.AgentOutput{
			Metadata: map[string]interface{}{
				"quality_score": 0.95,
			},
		}

		if !condition(output1, 1) {
			t.Error("Expected true for score < threshold")
		}

		if condition(output2, 2) {
			t.Error("Expected false for score >= threshold")
		}
	})

	t.Run("ConvergenceCondition", func(t *testing.T) {
		condition := ConvergenceCondition(0.01)

		output1 := &agent.AgentOutput{Result: 1.0}
		output2 := &agent.AgentOutput{Result: 1.005}
		_ = &agent.AgentOutput{Result: 1.006} // output3 for future use

		// First iteration always continues
		if !condition(output1, 1) {
			t.Error("First iteration should continue")
		}

		// Small change, should stop
		if condition(output2, 2) {
			t.Error("Should stop when change < tolerance")
		}
	})
}

// TestAgentComposition tests complex agent compositions
func TestAgentComposition(t *testing.T) {
	t.Run("Sequential of Parallel", func(t *testing.T) {
		// Create parallel agents
		parallel1 := NewParallel(AllAggregation).
			Add(agent.NewAgent(agent.AgentConfig{
				Name:     "p1-a1",
				Executor: &mockExecutor{result: "P1-A"},
			})).
			Add(agent.NewAgent(agent.AgentConfig{
				Name:     "p1-a2",
				Executor: &mockExecutor{result: "P1-B"},
			})).
			Build()

		parallel2 := NewParallel(AllAggregation).
			Add(agent.NewAgent(agent.AgentConfig{
				Name:     "p2-a1",
				Executor: &mockExecutor{result: "P2-A"},
			})).
			Add(agent.NewAgent(agent.AgentConfig{
				Name:     "p2-a2",
				Executor: &mockExecutor{result: "P2-B"},
			})).
			Build()

		// Create sequential of parallels
		sequential := NewSequential().
			WithName("seq-of-parallel").
			Add(parallel1).
			Add(parallel2).
			Build()

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "Complex workflow"}

		output, err := sequential.Execute(ctx, input)
		if err != nil {
			t.Fatalf("Complex composition failed: %v", err)
		}

		if output == nil {
			t.Error("Expected non-nil output")
		}
	})
}
