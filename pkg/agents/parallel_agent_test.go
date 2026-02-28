package agents

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/agent"
)

// TestParallelAgentSubAgents tests ADK-compatible SubAgents configuration
func TestParallelAgentSubAgents(t *testing.T) {
	// Create sub-agents
	agent1 := createTestAgent("Agent1", 100*time.Millisecond, "Result1")
	agent2 := createTestAgent("Agent2", 150*time.Millisecond, "Result2")
	agent3 := createTestAgent("Agent3", 50*time.Millisecond, "Result3")

	// Create parallel orchestrator with SubAgents
	parallel := NewParallelAgent(ParallelAgentConfig{
		Name:        "Orchestrator",
		SubAgents:   []agent.Agent{agent1, agent2, agent3},
		Aggregation: AllAggregation,
		Timeout:     5 * time.Second,
	})

	if parallel == nil {
		t.Fatal("Failed to create parallel agent")
	}

	// Verify sub-agents are registered
	subAgents := parallel.SubAgents()
	if len(subAgents) != 3 {
		t.Errorf("Expected 3 sub-agents, got %d", len(subAgents))
	}

	// Execute orchestrator
	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Test parallel execution",
	}

	output, err := parallel.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Verify results
	results, ok := output.Result.([]interface{})
	if !ok {
		t.Fatalf("Expected []interface{} result, got %T", output.Result)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}

	// Verify metadata
	if output.Metadata["pattern"] != "Parallel" {
		t.Errorf("Expected pattern 'Parallel', got %v", output.Metadata["pattern"])
	}

	if output.Metadata["successful"] != 3 {
		t.Errorf("Expected 3 successful agents, got %v", output.Metadata["successful"])
	}
}

// TestParallelAgentInterleavedEvents verifies events from sub-agents are interleaved
func TestParallelAgentInterleavedEvents(t *testing.T) {
	// Create agents with different execution times
	// We'll track completion order to verify interleaving
	var completionMu sync.Mutex
	completionOrder := []string{}

	agent1 := createTrackingAgent("SlowAgent", 300*time.Millisecond, "Result1", &completionOrder, &completionMu)
	agent2 := createTrackingAgent("MediumAgent", 150*time.Millisecond, "Result2", &completionOrder, &completionMu)
	agent3 := createTrackingAgent("FastAgent", 50*time.Millisecond, "Result3", &completionOrder, &completionMu)

	// Create parallel orchestrator
	parallel := NewParallelAgent(ParallelAgentConfig{
		Name:        "InterleavedOrchestrator",
		SubAgents:   []agent.Agent{agent1, agent2, agent3},
		Aggregation: AllAggregation,
		Timeout:     5 * time.Second,
	})

	// Execute
	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Test interleaved execution",
	}

	startTime := time.Now()
	_, err := parallel.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}
	duration := time.Since(startTime)

	// Verify completion order shows interleaving
	// FastAgent should complete first, SlowAgent last
	completionMu.Lock()
	defer completionMu.Unlock()

	if len(completionOrder) != 3 {
		t.Errorf("Expected 3 completions, got %d", len(completionOrder))
	}

	// FastAgent should complete first
	if completionOrder[0] != "FastAgent" {
		t.Errorf("Expected FastAgent to complete first, got %s", completionOrder[0])
	}

	// SlowAgent should complete last
	if completionOrder[2] != "SlowAgent" {
		t.Errorf("Expected SlowAgent to complete last, got %s", completionOrder[2])
	}

	// Total execution time should be close to slowest agent (parallel)
	// Not sum of all agents (sequential)
	if duration > 500*time.Millisecond {
		t.Errorf("Parallel execution took too long: %v (expected ~300ms)", duration)
	}

	t.Logf("Completion order: %v", completionOrder)
	t.Logf("Total duration: %v", duration)
}

// TestParallelAgentFirstAggregation tests race condition pattern
func TestParallelAgentFirstAggregation(t *testing.T) {
	// Create agents with different speeds
	fast := createTestAgent("FastAgent", 50*time.Millisecond, "FastResult")
	slow := createTestAgent("SlowAgent", 500*time.Millisecond, "SlowResult")

	parallel := NewParallelAgent(ParallelAgentConfig{
		Name:        "RaceOrchestrator",
		SubAgents:   []agent.Agent{slow, fast}, // Slow first
		Aggregation: FirstAggregation,
		Timeout:     2 * time.Second,
	})

	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Test first aggregation",
	}

	startTime := time.Now()
	output, err := parallel.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}
	duration := time.Since(startTime)

	// Should get fast result
	if output.Result != "FastResult" {
		t.Errorf("Expected 'FastResult', got %v", output.Result)
	}

	// Should complete quickly (not wait for slow agent)
	if duration > 200*time.Millisecond {
		t.Errorf("First aggregation took too long: %v (expected ~50ms)", duration)
	}

	if output.Metadata["pattern"] != "Parallel-First" {
		t.Errorf("Expected pattern 'Parallel-First', got %v", output.Metadata["pattern"])
	}

	t.Logf("Duration: %v", duration)
}

// TestParallelAgentMinSuccessful tests partial success requirements
func TestParallelAgentMinSuccessful(t *testing.T) {
	// Create agents where some will fail
	success1 := createTestAgent("Success1", 50*time.Millisecond, "Result1")
	success2 := createTestAgent("Success2", 100*time.Millisecond, "Result2")
	failing := createFailingAgent("FailingAgent", 75*time.Millisecond)

	// Require only 2 out of 3 to succeed
	parallel := NewParallelAgent(ParallelAgentConfig{
		Name:          "PartialSuccessOrchestrator",
		SubAgents:     []agent.Agent{success1, failing, success2},
		Aggregation:   AllAggregation,
		MinSuccessful: 2,
		Timeout:       2 * time.Second,
	})

	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Test partial success",
	}

	output, err := parallel.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Expected success with 2/3 agents, got error: %v", err)
	}

	// Should have 2 results (non-nil)
	results, ok := output.Result.([]interface{})
	if !ok {
		t.Fatalf("Expected []interface{} result")
	}

	successCount := 0
	for _, r := range results {
		if r != nil {
			successCount++
		}
	}

	if successCount != 2 {
		t.Errorf("Expected 2 successful results, got %d", successCount)
	}

	if output.Metadata["successful"] != 2 {
		t.Errorf("Expected 2 successful agents in metadata, got %v", output.Metadata["successful"])
	}
}

// TestParallelAgentFailFast tests fail-fast behavior
func TestParallelAgentFailFast(t *testing.T) {
	// Create agents with one that fails quickly
	success := createTestAgent("SuccessAgent", 500*time.Millisecond, "Result")
	failing := createFailingAgent("FailingAgent", 50*time.Millisecond)

	parallel := NewParallelAgent(ParallelAgentConfig{
		Name:        "FailFastOrchestrator",
		SubAgents:   []agent.Agent{success, failing},
		Aggregation: AllAggregation,
		FailFast:    true,
		Timeout:     2 * time.Second,
	})

	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Test fail-fast",
	}

	startTime := time.Now()
	_, err := parallel.Execute(ctx, input)
	duration := time.Since(startTime)

	// Should fail quickly
	if err == nil {
		t.Fatal("Expected error with fail-fast enabled")
	}

	// Should not wait for slow agent to complete
	if duration > 200*time.Millisecond {
		t.Errorf("Fail-fast took too long: %v (expected ~50ms)", duration)
	}

	t.Logf("Failed fast in %v: %v", duration, err)
}

// TestParallelAgentConcatAggregation tests string concatenation
func TestParallelAgentConcatAggregation(t *testing.T) {
	agent1 := createTestAgent("Agent1", 50*time.Millisecond, "First result")
	agent2 := createTestAgent("Agent2", 100*time.Millisecond, "Second result")
	agent3 := createTestAgent("Agent3", 75*time.Millisecond, "Third result")

	parallel := NewParallelAgent(ParallelAgentConfig{
		Name:        "ConcatOrchestrator",
		SubAgents:   []agent.Agent{agent1, agent2, agent3},
		Aggregation: ConcatAggregation,
		Timeout:     2 * time.Second,
	})

	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Test concatenation",
	}

	output, err := parallel.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Should be concatenated string
	result, ok := output.Result.(string)
	if !ok {
		t.Fatalf("Expected string result, got %T", output.Result)
	}

	// All three results should be present
	if len(result) < 30 { // Rough check
		t.Errorf("Concatenated result seems too short: %s", result)
	}

	t.Logf("Concatenated result: %s", result)
}

// TestParallelAgentBuilder tests fluent builder API
func TestParallelAgentBuilder(t *testing.T) {
	agent1 := createTestAgent("Agent1", 50*time.Millisecond, "Result1")
	agent2 := createTestAgent("Agent2", 100*time.Millisecond, "Result2")

	// Use builder API
	parallel := NewParallel(AllAggregation).
		WithName("BuilderOrchestrator").
		Add(agent1).
		Add(agent2).
		WithTimeout(2 * time.Second).
		WithMinSuccessful(1).
		Build()

	if parallel == nil {
		t.Fatal("Builder failed to create agent")
	}

	if parallel.Name() != "BuilderOrchestrator" {
		t.Errorf("Expected name 'BuilderOrchestrator', got %s", parallel.Name())
	}

	// Execute
	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Test builder",
	}

	output, err := parallel.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	results, ok := output.Result.([]interface{})
	if !ok || len(results) != 2 {
		t.Errorf("Expected 2 results, got %v", output.Result)
	}
}

// TestParallelAgentHierarchyIntegration tests integration with hierarchy
func TestParallelAgentHierarchyIntegration(t *testing.T) {
	// Create sub-agents
	agent1 := createTestAgent("SubAgent1", 50*time.Millisecond, "Result1")
	agent2 := createTestAgent("SubAgent2", 100*time.Millisecond, "Result2")

	// Create parallel orchestrator
	parallel := NewParallelAgent(ParallelAgentConfig{
		Name:        "ParallelOrchestrator",
		SubAgents:   []agent.Agent{agent1, agent2},
		Aggregation: AllAggregation,
		Timeout:     2 * time.Second,
	})

	// Verify hierarchy is set up
	subAgents := parallel.SubAgents()
	if len(subAgents) != 2 {
		t.Errorf("Expected 2 sub-agents in hierarchy, got %d", len(subAgents))
	}

	// Verify parent references
	for _, sub := range subAgents {
		if baseAgent, ok := sub.(*agent.BaseAgent); ok {
			parent := baseAgent.Parent()
			if parent == nil {
				t.Error("Sub-agent parent is nil")
			} else if parent.ID() != parallel.ID() {
				t.Error("Sub-agent parent reference is incorrect")
			}
		}
	}

	// Test FindAgent
	found, err := parallel.FindAgent("SubAgent1")
	if err != nil {
		t.Errorf("Failed to find SubAgent1: %v", err)
	}
	if found.Name() != "SubAgent1" {
		t.Errorf("Found wrong agent: %s", found.Name())
	}
}

// Helper functions

func createTestAgent(name string, duration time.Duration, result interface{}) agent.Agent {
	executor := &testExecutor{
		name:     name,
		duration: duration,
		result:   result,
	}

	return agent.NewAgent(agent.AgentConfig{
		Name:        name,
		Description: fmt.Sprintf("Test agent %s", name),
		Executor:    executor,
	})
}

func createTrackingAgent(name string, duration time.Duration, result interface{},
	completionOrder *[]string, mu *sync.Mutex) agent.Agent {
	executor := &trackingExecutor{
		name:            name,
		duration:        duration,
		result:          result,
		completionOrder: completionOrder,
		mu:              mu,
	}

	return agent.NewAgent(agent.AgentConfig{
		Name:        name,
		Description: fmt.Sprintf("Tracking agent %s", name),
		Executor:    executor,
	})
}

func createFailingAgent(name string, duration time.Duration) agent.Agent {
	executor := &failingExecutor{
		name:     name,
		duration: duration,
	}

	return agent.NewAgent(agent.AgentConfig{
		Name:        name,
		Description: fmt.Sprintf("Failing agent %s", name),
		Executor:    executor,
	})
}

// testExecutor is a simple executor for testing
type testExecutor struct {
	name     string
	duration time.Duration
	result   interface{}
}

func (e *testExecutor) Execute(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	select {
	case <-time.After(e.duration):
		return &agent.AgentOutput{
			Result: e.result,
			Metadata: map[string]interface{}{
				"agent":    e.name,
				"duration": e.duration.Seconds(),
			},
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// trackingExecutor tracks completion order
type trackingExecutor struct {
	name            string
	duration        time.Duration
	result          interface{}
	completionOrder *[]string
	mu              *sync.Mutex
}

func (e *trackingExecutor) Execute(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	select {
	case <-time.After(e.duration):
		// Record completion
		e.mu.Lock()
		*e.completionOrder = append(*e.completionOrder, e.name)
		e.mu.Unlock()

		return &agent.AgentOutput{
			Result: e.result,
			Metadata: map[string]interface{}{
				"agent":    e.name,
				"duration": e.duration.Seconds(),
			},
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// failingExecutor always fails
type failingExecutor struct {
	name     string
	duration time.Duration
}

func (e *failingExecutor) Execute(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	select {
	case <-time.After(e.duration):
		return nil, fmt.Errorf("agent %s failed intentionally", e.name)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
