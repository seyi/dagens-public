package agents

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/agent"
)

// TestRemoteAgent tests remote agent functionality
func TestRemoteAgent(t *testing.T) {
	t.Run("Builder API", func(t *testing.T) {
		remoteAgent, err := NewRemote("test-agent", "http://localhost:8080").
			WithName("my-remote").
			WithTimeout(10 * time.Second).
			WithRetryCount(2).
			WithCaching(true, 5*time.Minute).
			Build()

		if err != nil {
			t.Fatalf("Failed to build remote agent: %v", err)
		}

		if remoteAgent.Name() != "my-remote" {
			t.Errorf("Expected name 'my-remote', got %s", remoteAgent.Name())
		}

		if remoteAgent.timeout != 10*time.Second {
			t.Errorf("Expected timeout 10s, got %v", remoteAgent.timeout)
		}

		if remoteAgent.retryCount != 2 {
			t.Errorf("Expected retry count 2, got %d", remoteAgent.retryCount)
		}
	})

	t.Run("Error Retryability", func(t *testing.T) {
		tests := []struct {
			err        error
			retryable  bool
		}{
			{fmt.Errorf("connection refused"), true},
			{fmt.Errorf("timeout"), true},
			{fmt.Errorf("500 internal server error"), true},
			{context.DeadlineExceeded, true},
			{fmt.Errorf("404 not found"), false},
			{fmt.Errorf("invalid input"), false},
		}

		for _, tt := range tests {
			result := isRetryableError(tt.err)
			if result != tt.retryable {
				t.Errorf("Error %v: expected retryable=%v, got %v",
					tt.err, tt.retryable, result)
			}
		}
	})
}

// TestMapReduceAgent tests map-reduce functionality
func TestMapReduceAgent(t *testing.T) {
	t.Run("Basic MapReduce", func(t *testing.T) {
		// Create mapper that doubles numbers
		mapper := agent.NewAgent(agent.AgentConfig{
			Name: "doubler",
			Executor: agent.ExecutorFunc(func(ctx context.Context, _ agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				num := input.Context["number"].(int)
				return &agent.AgentOutput{
					Result: num * 2,
				}, nil
			}),
		})

		// Create reducer that sums results
		reducer := agent.NewAgent(agent.AgentConfig{
			Name: "summer",
			Executor: agent.ExecutorFunc(func(ctx context.Context, _ agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				mapResults := input.Context["map_results"].([]*agent.AgentOutput)
				sum := 0
				for _, result := range mapResults {
					sum += result.Result.(int)
				}
				return &agent.AgentOutput{
					Result: sum,
				}, nil
			}),
		})

		// Splitter that creates tasks for numbers 1-10
		splitter := func(input *agent.AgentInput) ([]*agent.AgentInput, error) {
			numbers := input.Context["numbers"].([]int)
			splits := make([]*agent.AgentInput, len(numbers))
			for i, num := range numbers {
				splits[i] = &agent.AgentInput{
					Instruction: "double",
					Context: map[string]interface{}{
						"number": num,
					},
				}
			}
			return splits, nil
		}

		// Create map-reduce agent
		mrAgent := NewMapReduce(mapper, reducer, splitter).
			WithName("sum-doubler").
			WithParallelism(3).
			Build()

		ctx := context.Background()
		input := &agent.AgentInput{
			Instruction: "double and sum",
			Context: map[string]interface{}{
				"numbers": []int{1, 2, 3, 4, 5},
			},
		}

		output, err := mrAgent.Execute(ctx, input)
		if err != nil {
			t.Fatalf("MapReduce failed: %v", err)
		}

		// Expected: (1+2+3+4+5) * 2 = 30
		expected := 30
		if output.Result != expected {
			t.Errorf("Expected %d, got %v", expected, output.Result)
		}

		if output.Metadata["pattern"] != "MapReduce" {
			t.Error("MapReduce pattern metadata not set")
		}

		splits := output.Metadata["splits"].(int)
		if splits != 5 {
			t.Errorf("Expected 5 splits, got %d", splits)
		}
	})

	t.Run("LineSplitter", func(t *testing.T) {
		splitter := LineSplitter(10)

		input := &agent.AgentInput{
			Instruction: "process",
			Context: map[string]interface{}{
				"text": "0123456789abcdefghij",
			},
		}

		splits, err := splitter(input)
		if err != nil {
			t.Fatalf("LineSplitter failed: %v", err)
		}

		if len(splits) != 2 {
			t.Errorf("Expected 2 splits, got %d", len(splits))
		}

		// Check first chunk
		firstText := splits[0].Context["text"].(string)
		if firstText != "0123456789" {
			t.Errorf("First chunk incorrect: %s", firstText)
		}
	})

	t.Run("ListSplitter", func(t *testing.T) {
		splitter := ListSplitter("items", 3)

		input := &agent.AgentInput{
			Instruction: "process",
			Context: map[string]interface{}{
				"items": []interface{}{1, 2, 3, 4, 5, 6, 7},
			},
		}

		splits, err := splitter(input)
		if err != nil {
			t.Fatalf("ListSplitter failed: %v", err)
		}

		if len(splits) != 3 {
			t.Errorf("Expected 3 splits, got %d", len(splits))
		}

		// Check first chunk has 3 items
		firstChunk := splits[0].Context["items"].([]interface{})
		if len(firstChunk) != 3 {
			t.Errorf("Expected 3 items in first chunk, got %d", len(firstChunk))
		}
	})

	t.Run("With Error Collection", func(t *testing.T) {
		failCount := 0

		// Mapper that fails on first 2 calls
		mapper := agent.NewAgent(agent.AgentConfig{
			Name: "flaky-mapper",
			Executor: agent.ExecutorFunc(func(ctx context.Context, _ agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				failCount++
				if failCount <= 2 {
					return nil, fmt.Errorf("mapper failed")
				}
				return &agent.AgentOutput{
					Result:    "success",
					
				}, nil
			}),
		})

		reducer := agent.NewAgent(agent.AgentConfig{
			Name: "reducer",
			Executor: agent.ExecutorFunc(func(ctx context.Context, _ agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{
					Result:    "reduced",
					
				}, nil
			}),
		})

		splitter := ListSplitter("items", 1)

		mrAgent := NewMapReduce(mapper, reducer, splitter).
			WithCollectErrors(true).
			Build()

		ctx := context.Background()
		input := &agent.AgentInput{
			Instruction: "process",
			Context: map[string]interface{}{
				"items": []interface{}{1, 2, 3, 4, 5},
			},
		}

		output, err := mrAgent.Execute(ctx, input)
		if err != nil {
			t.Fatalf("MapReduce with error collection failed: %v", err)
		}

		failedMaps := output.Metadata["failed_maps"].(int)
		if failedMaps != 2 {
			t.Errorf("Expected 2 failed maps, got %d", failedMaps)
		}
	})
}

// TestRouterAgent tests routing functionality
func TestRouterAgent(t *testing.T) {
	t.Run("Basic Routing", func(t *testing.T) {
		// Create agents for different intents
		searchAgent := agent.NewAgent(agent.AgentConfig{
			Name:     "search",
			Executor: &mockExecutor{result: "search result"},
		})

		calculateAgent := agent.NewAgent(agent.AgentConfig{
			Name:     "calculate",
			Executor: &mockExecutor{result: "calc result"},
		})

		defaultAgent := agent.NewAgent(agent.AgentConfig{
			Name:     "default",
			Executor: &mockExecutor{result: "default result"},
		})

		// Create router
		router := NewRouter().
			WithName("intent-router").
			AddRoute("search", KeywordCondition("search", "find", "lookup"), searchAgent).
			AddRoute("calc", KeywordCondition("calculate", "compute", "math"), calculateAgent).
			WithDefault(defaultAgent).
			Build()

		ctx := context.Background()

		// Test search route
		output1, err := router.Execute(ctx, &agent.AgentInput{
			Instruction: "search for information",
		})
		if err != nil {
			t.Fatalf("Search routing failed: %v", err)
		}
		if output1.Result != "search result" {
			t.Errorf("Expected search result, got %v", output1.Result)
		}
		if output1.Metadata["route_name"] != "search" {
			t.Error("Route name not set correctly")
		}

		// Test calc route
		output2, err := router.Execute(ctx, &agent.AgentInput{
			Instruction: "calculate 2+2",
		})
		if err != nil {
			t.Fatalf("Calc routing failed: %v", err)
		}
		if output2.Result != "calc result" {
			t.Errorf("Expected calc result, got %v", output2.Result)
		}

		// Test default route
		output3, err := router.Execute(ctx, &agent.AgentInput{
			Instruction: "something else",
		})
		if err != nil {
			t.Fatalf("Default routing failed: %v", err)
		}
		if output3.Result != "default result" {
			t.Errorf("Expected default result, got %v", output3.Result)
		}
	})

	t.Run("Priority Routing", func(t *testing.T) {
		highPriorityAgent := agent.NewAgent(agent.AgentConfig{
			Name:     "high-priority",
			Executor: &mockExecutor{result: "high"},
		})

		lowPriorityAgent := agent.NewAgent(agent.AgentConfig{
			Name:     "low-priority",
			Executor: &mockExecutor{result: "low"},
		})

		// Both routes match "test", but high priority should win
		router := NewRouter().
			AddRouteWithPriority("low", KeywordCondition("test"), lowPriorityAgent, 1).
			AddRouteWithPriority("high", KeywordCondition("test"), highPriorityAgent, 10).
			Build()

		ctx := context.Background()
		output, err := router.Execute(ctx, &agent.AgentInput{
			Instruction: "test",
		})

		if err != nil {
			t.Fatalf("Priority routing failed: %v", err)
		}

		if output.Result != "high" {
			t.Errorf("Expected high priority result, got %v", output.Result)
		}
	})

	t.Run("Condition Combinators", func(t *testing.T) {
		agent1 := agent.NewAgent(agent.AgentConfig{
			Name:     "agent1",
			Executor: &mockExecutor{result: "result1"},
		})

		// AND condition: must have both keywords
		andCondition := AndCondition(
			KeywordCondition("search"),
			KeywordCondition("fast"),
		)

		// OR condition: either keyword
		orCondition := OrCondition(
			KeywordCondition("calculate"),
			KeywordCondition("compute"),
		)

		// NOT condition: doesn't have keyword
		notCondition := NotCondition(KeywordCondition("slow"))

		router := NewRouter().
			AddRoute("and", andCondition, agent1).
			AddRoute("or", orCondition, agent1).
			AddRoute("not", notCondition, agent1).
			Build()

		ctx := context.Background()

		// Test AND (should match)
		output1, _ := router.Execute(ctx, &agent.AgentInput{
			Instruction: "search fast",
		})
		if output1.Metadata["route_name"] != "and" {
			t.Error("AND condition failed to match")
		}

		// Test OR (should match)
		output2, _ := router.Execute(ctx, &agent.AgentInput{
			Instruction: "compute values",
		})
		if output2.Metadata["route_name"] != "or" {
			t.Error("OR condition failed to match")
		}
	})

	t.Run("Context-Based Routing", func(t *testing.T) {
		premiumAgent := agent.NewAgent(agent.AgentConfig{
			Name:     "premium",
			Executor: &mockExecutor{result: "premium service"},
		})

		basicAgent := agent.NewAgent(agent.AgentConfig{
			Name:     "basic",
			Executor: &mockExecutor{result: "basic service"},
		})

		router := NewRouter().
			AddRoute("premium", ContextKeyCondition("tier", "premium"), premiumAgent).
			WithDefault(basicAgent).
			Build()

		ctx := context.Background()

		// Test premium tier
		output1, err := router.Execute(ctx, &agent.AgentInput{
			Instruction: "process",
			Context: map[string]interface{}{
				"tier": "premium",
			},
		})
		if err != nil {
			t.Fatalf("Premium routing failed: %v", err)
		}
		if output1.Result != "premium service" {
			t.Error("Expected premium service")
		}

		// Test basic tier (default)
		output2, _ := router.Execute(ctx, &agent.AgentInput{
			Instruction: "process",
			Context: map[string]interface{}{
				"tier": "basic",
			},
		})
		if output2.Result != "basic service" {
			t.Error("Expected basic service")
		}
	})
}

// TestLoadBalancerAgent tests load balancing
func TestLoadBalancerAgent(t *testing.T) {
	t.Run("Round Robin", func(t *testing.T) {
		agent1 := agent.NewAgent(agent.AgentConfig{
			Name:     "agent1",
			Executor: &mockExecutor{result: "agent1"},
		})

		agent2 := agent.NewAgent(agent.AgentConfig{
			Name:     "agent2",
			Executor: &mockExecutor{result: "agent2"},
		})

		agent3 := agent.NewAgent(agent.AgentConfig{
			Name:     "agent3",
			Executor: &mockExecutor{result: "agent3"},
		})

		lb := NewLoadBalancerAgent(LoadBalancerAgentConfig{
			Name:     "load-balancer",
			Agents:   []agent.Agent{agent1, agent2, agent3},
			Strategy: RoundRobin,
		})

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}

		// Execute 6 times and verify round-robin
		results := []string{}
		for i := 0; i < 6; i++ {
			output, err := lb.Execute(ctx, input)
			if err != nil {
				t.Fatalf("Load balancer failed: %v", err)
			}
			results = append(results, output.Result.(string))
		}

		expected := []string{"agent1", "agent2", "agent3", "agent1", "agent2", "agent3"}
		for i, result := range results {
			if result != expected[i] {
				t.Errorf("Round robin failed at position %d: expected %s, got %s",
					i, expected[i], result)
			}
		}
	})
}

// TestFanOutAgent tests fan-out pattern
func TestFanOutAgent(t *testing.T) {
	t.Run("Basic FanOut", func(t *testing.T) {
		// Create 3 worker agents
		workers := []agent.Agent{
			agent.NewAgent(agent.AgentConfig{
				Name: "worker1",
				Executor: agent.ExecutorFunc(func(ctx context.Context, _ agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
					task := input.Context["task"].(string)
					return &agent.AgentOutput{
						Result:    fmt.Sprintf("worker1:%s", task),
						
					}, nil
				}),
			}),
			agent.NewAgent(agent.AgentConfig{
				Name: "worker2",
				Executor: agent.ExecutorFunc(func(ctx context.Context, _ agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
					task := input.Context["task"].(string)
					return &agent.AgentOutput{
						Result:    fmt.Sprintf("worker2:%s", task),
						
					}, nil
				}),
			}),
			agent.NewAgent(agent.AgentConfig{
				Name: "worker3",
				Executor: agent.ExecutorFunc(func(ctx context.Context, _ agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
					task := input.Context["task"].(string)
					return &agent.AgentOutput{
						Result:    fmt.Sprintf("worker3:%s", task),
						
					}, nil
				}),
			}),
		}

		// Splitter creates 3 tasks
		splitter := func(input *agent.AgentInput) ([]*agent.AgentInput, error) {
			return []*agent.AgentInput{
				{Instruction: "task1", Context: map[string]interface{}{"task": "A"}},
				{Instruction: "task2", Context: map[string]interface{}{"task": "B"}},
				{Instruction: "task3", Context: map[string]interface{}{"task": "C"}},
			}, nil
		}

		fanOut := NewFanOutAgent(FanOutAgentConfig{
			Name:        "fanout",
			Workers:     workers,
			Splitter:    splitter,
			Parallelism: 3,
		})

		ctx := context.Background()
		output, err := fanOut.Execute(ctx, &agent.AgentInput{
			Instruction: "distribute work",
		})

		if err != nil {
			t.Fatalf("FanOut failed: %v", err)
		}

		results := output.Result.([]*agent.AgentOutput)
		if len(results) != 3 {
			t.Errorf("Expected 3 results, got %d", len(results))
		}

		// Verify each worker got its task
		expectedResults := map[string]bool{
			"worker1:A": false,
			"worker2:B": false,
			"worker3:C": false,
		}

		for _, result := range results {
			resultStr := result.Result.(string)
			if _, exists := expectedResults[resultStr]; exists {
				expectedResults[resultStr] = true
			}
		}

		for result, found := range expectedResults {
			if !found {
				t.Errorf("Expected result %s not found", result)
			}
		}
	})
}

// TestRemoteAgentPool tests remote agent pooling
func TestRemoteAgentPool(t *testing.T) {
	t.Run("Round Robin Pool", func(t *testing.T) {
		// Create mock remote agents
		agent1, _ := NewRemoteAgent(RemoteAgentConfig{
			Name:    "remote1",
			AgentID: "agent1",
		})
		// Override executor for testing
		agent1.BaseAgent = agent.NewAgent(agent.AgentConfig{
			Name:     "remote1",
			Executor: &mockExecutor{result: "agent1"},
		})

		agent2, _ := NewRemoteAgent(RemoteAgentConfig{
			Name:    "remote2",
			AgentID: "agent2",
		})
		agent2.BaseAgent = agent.NewAgent(agent.AgentConfig{
			Name:     "remote2",
			Executor: &mockExecutor{result: "agent2"},
		})

		pool := NewRemoteAgentPool(agent1, agent2)

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}

		// Test round-robin
		results := []string{}
		for i := 0; i < 4; i++ {
			output, err := pool.Execute(ctx, input)
			if err != nil {
				t.Fatalf("Pool execute failed: %v", err)
			}
			results = append(results, output.Result.(string))
		}

		expected := []string{"agent1", "agent2", "agent1", "agent2"}
		for i, result := range results {
			if result != expected[i] {
				t.Errorf("Position %d: expected %s, got %s", i, expected[i], result)
			}
		}
	})

	t.Run("Fallback Pool", func(t *testing.T) {
		// First agent fails
		agent1, _ := NewRemoteAgent(RemoteAgentConfig{
			Name:    "failing",
			AgentID: "agent1",
		})
		agent1.BaseAgent = agent.NewAgent(agent.AgentConfig{
			Name: "failing",
			Executor: agent.ExecutorFunc(func(ctx context.Context, _ agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return nil, fmt.Errorf("agent failed")
			}),
		})

		// Second agent succeeds
		agent2, _ := NewRemoteAgent(RemoteAgentConfig{
			Name:    "working",
			AgentID: "agent2",
		})
		agent2.BaseAgent = agent.NewAgent(agent.AgentConfig{
			Name:     "working",
			Executor: &mockExecutor{result: "success"},
		})

		pool := NewRemoteAgentPool(agent1, agent2)

		ctx := context.Background()
		output, err := pool.ExecuteWithFallback(ctx, &agent.AgentInput{
			Instruction: "test",
		})

		if err != nil {
			t.Fatalf("Fallback failed: %v", err)
		}

		if output.Result != "success" {
			t.Errorf("Expected success from fallback, got %v", output.Result)
		}
	})
}

// TestDocumentSplitter tests document splitting
func TestDocumentSplitter(t *testing.T) {
	splitter := DocumentSplitter("documents")

	input := &agent.AgentInput{
		Instruction: "process",
		Context: map[string]interface{}{
			"documents": []interface{}{
				"doc1 content",
				"doc2 content",
				"doc3 content",
			},
		},
	}

	splits, err := splitter(input)
	if err != nil {
		t.Fatalf("DocumentSplitter failed: %v", err)
	}

	if len(splits) != 3 {
		t.Errorf("Expected 3 splits, got %d", len(splits))
	}

	// Verify each split has correct document
	for i, split := range splits {
		doc := split.Context["document"].(string)
		expected := fmt.Sprintf("doc%d content", i+1)
		if doc != expected {
			t.Errorf("Split %d: expected '%s', got '%s'", i, expected, doc)
		}

		docIndex := split.Context["doc_index"].(int)
		if docIndex != i {
			t.Errorf("Split %d: incorrect doc_index %d", i, docIndex)
		}
	}
}
