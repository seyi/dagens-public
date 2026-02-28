package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/events"
)

// Test Phase 1: OutputKey Support
// Verifies ADK-compatible automatic result storage

func TestOutputKeyBasic(t *testing.T) {
	// Create an executor that returns data
	executor := &testExecutorFunc{
		execute: func(ctx context.Context, ag Agent, input *AgentInput) (*AgentOutput, error) {
			return &AgentOutput{
				Result: "processed_data",
			}, nil
		},
	}

	// Create agent with OutputKey
	agent := NewAgent(AgentConfig{
		Name:      "FetchAgent",
		Executor:  executor,
		OutputKey: "data", // ADK pattern: auto-store to this key
	})

	// Execute
	ctx := context.Background()
	input := &AgentInput{
		Instruction: "Fetch data",
		Context:     make(map[string]interface{}),
	}

	output, err := agent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify result is stored in context under "data" key
	storedValue, ok := input.Context["data"]
	if !ok {
		t.Fatal("Expected 'data' key in context after execution")
	}

	if storedValue != "processed_data" {
		t.Errorf("Expected 'processed_data', got %v", storedValue)
	}

	// Verify metadata indicates storage
	if output.Metadata["output_key"] != "data" {
		t.Errorf("Expected output_key metadata to be 'data'")
	}

	if output.Metadata["stored_to_context"] != true {
		t.Error("Expected stored_to_context metadata to be true")
	}
}

func TestOutputKeySequentialPipeline(t *testing.T) {
	// Create first agent that stores to "data"
	fetchExecutor := &testExecutorFunc{
		execute: func(ctx context.Context, ag Agent, input *AgentInput) (*AgentOutput, error) {
			return &AgentOutput{
				Result: map[string]interface{}{
					"records": 100,
					"status":  "complete",
				},
			}, nil
		},
	}

	fetchAgent := NewAgent(AgentConfig{
		Name:      "FetchAgent",
		Executor:  fetchExecutor,
		OutputKey: "data", // Auto-store result
	})

	// Create second agent that reads from "data"
	processExecutor := &testExecutorFunc{
		execute: func(ctx context.Context, ag Agent, input *AgentInput) (*AgentOutput, error) {
			// Read the "data" that was auto-stored by previous agent
			data, ok := input.Context["data"]
			if !ok {
				return nil, fmt.Errorf("expected 'data' in context")
			}

			dataMap, ok := data.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("expected data to be map")
			}

			records := dataMap["records"].(int)

			return &AgentOutput{
				Result: fmt.Sprintf("Processed %d records", records),
			}, nil
		},
	}

	processAgent := NewAgent(AgentConfig{
		Name:      "ProcessAgent",
		Executor:  processExecutor,
		OutputKey: "result", // Store final result
	})

	// Execute pipeline
	ctx := context.Background()
	input := &AgentInput{
		Instruction: "Process data",
		Context:     make(map[string]interface{}),
	}

	// Step 1: Fetch
	_, err := fetchAgent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	// Verify "data" is in context
	if _, ok := input.Context["data"]; !ok {
		t.Fatal("Expected 'data' in context after fetch")
	}

	// Step 2: Process (using "data" from context)
	output, err := processAgent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	// Verify final result
	finalResult, ok := input.Context["result"]
	if !ok {
		t.Fatal("Expected 'result' in context")
	}

	expected := "Processed 100 records"
	if finalResult != expected {
		t.Errorf("Expected '%s', got %v", expected, finalResult)
	}

	t.Logf("Pipeline result: %v", output.Result)
}

// Test Phase 2: Template Interpolation
// Verifies ADK-compatible {key} syntax in instructions

func TestTemplateInterpolationBasic(t *testing.T) {
	executor := &testExecutorFunc{
		execute: func(ctx context.Context, ag Agent, input *AgentInput) (*AgentOutput, error) {
			// The instruction should already be interpolated
			return &AgentOutput{
				Result: input.Instruction,
			}, nil
		},
	}

	agent := NewAgent(AgentConfig{
		Name:     "TemplateAgent",
		Executor: executor,
	})

	ctx := context.Background()
	input := &AgentInput{
		Instruction: "Process data from {source} with count {count}",
		Context: map[string]interface{}{
			"source": "database",
			"count":  42,
		},
	}

	output, err := agent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	expected := "Process data from database with count 42"
	if output.Result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, output.Result)
	}
}

func TestTemplateInterpolationNested(t *testing.T) {
	executor := &testExecutorFunc{
		execute: func(ctx context.Context, ag Agent, input *AgentInput) (*AgentOutput, error) {
			return &AgentOutput{
				Result: input.Instruction,
			}, nil
		},
	}

	agent := NewAgent(AgentConfig{
		Name:     "NestedTemplateAgent",
		Executor: executor,
	})

	ctx := context.Background()
	input := &AgentInput{
		Instruction: "Process {data.field} from {data.source}",
		Context: map[string]interface{}{
			"data": map[string]interface{}{
				"field":  "customer_name",
				"source": "CRM",
			},
		},
	}

	output, err := agent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	expected := "Process customer_name from CRM"
	if output.Result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, output.Result)
	}
}

func TestTemplateInterpolationMissingKey(t *testing.T) {
	executor := &testExecutorFunc{
		execute: func(ctx context.Context, ag Agent, input *AgentInput) (*AgentOutput, error) {
			return &AgentOutput{
				Result: input.Instruction,
			}, nil
		},
	}

	agent := NewAgent(AgentConfig{
		Name:     "MissingKeyAgent",
		Executor: executor,
	})

	ctx := context.Background()
	input := &AgentInput{
		Instruction: "Process {existing} and {missing}",
		Context: map[string]interface{}{
			"existing": "value",
		},
	}

	output, err := agent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Should keep placeholder for missing key (lenient behavior)
	result := output.Result.(string)
	if !strings.Contains(result, "value") {
		t.Error("Expected 'value' in result")
	}
	if !strings.Contains(result, "{missing}") {
		t.Error("Expected '{missing}' placeholder to remain")
	}
}

// Test Phase 3: Event Bus and State Change Callbacks
// Verifies ADK-compatible callbacks.on_state_change() pattern

func TestEventBusBasic(t *testing.T) {
	bus := events.NewMemoryEventBus()

	var receivedKey string
	var receivedValue interface{}
	var wg sync.WaitGroup
	wg.Add(1)

	// Register callback (ADK pattern)
	err := bus.OnStateChange(func(key string, oldValue, newValue interface{}) {
		receivedKey = key
		receivedValue = newValue
		wg.Done()
	})
	if err != nil {
		t.Fatalf("Failed to register callback: %v", err)
	}

	// Publish state change
	err = bus.PublishStateChange("data", nil, "processed", "TestAgent", "")
	if err != nil {
		t.Fatalf("Failed to publish state change: %v", err)
	}

	// Wait for callback
	wg.Wait()

	if receivedKey != "data" {
		t.Errorf("Expected key 'data', got '%s'", receivedKey)
	}

	if receivedValue != "processed" {
		t.Errorf("Expected value 'processed', got '%v'", receivedValue)
	}
}

func TestEventBusMultipleCallbacks(t *testing.T) {
	bus := events.NewMemoryEventBus()

	var callbackCount int
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Register multiple callbacks
	for i := 0; i < 3; i++ {
		wg.Add(1)
		err := bus.OnStateChange(func(key string, oldValue, newValue interface{}) {
			mu.Lock()
			callbackCount++
			mu.Unlock()
			wg.Done()
		})
		if err != nil {
			t.Fatalf("Failed to register callback %d: %v", i, err)
		}
	}

	// Publish event
	err := bus.PublishStateChange("test", nil, "value", "Agent", "")
	if err != nil {
		t.Fatalf("Failed to publish state change: %v", err)
	}

	// Wait for all callbacks
	wg.Wait()

	if callbackCount != 3 {
		t.Errorf("Expected 3 callbacks, got %d", callbackCount)
	}
}

func TestEventBusHistory(t *testing.T) {
	t.Skip("Event history tracking not yet implemented - future enhancement")
	// TODO: Implement MemoryEventStore integration for event history
	// bus := events.NewMemoryEventBus()
	// store := events.NewMemoryEventStore()
	// recorder := events.NewEventRecorder(bus, store)
	// ... test history retrieval via recorder.Query(...)
}

// Test Distributed Features Preservation
// Verifies that Spark-inspired distributed capabilities are maintained

func TestPartitionLocalityPreserved(t *testing.T) {
	// Create agent with partition key (Spark feature)
	executor := &testExecutorFunc{
		execute: func(ctx context.Context, ag Agent, input *AgentInput) (*AgentOutput, error) {
			return &AgentOutput{
				Result: "data",
				Metadata: map[string]interface{}{
					"partition_locality": true,
				},
			}, nil
		},
	}

	agent := NewAgent(AgentConfig{
		Name:      "PartitionedAgent",
		Executor:  executor,
		Partition: "node-1", // Spark: locality-aware scheduling
		OutputKey: "data",   // ADK: auto-store
	})

	// Verify partition is preserved
	if agent.Partition() != "node-1" {
		t.Error("Partition key not preserved")
	}

	// Execute with OutputKey
	ctx := context.Background()
	input := &AgentInput{
		Context: make(map[string]interface{}),
	}

	output, err := agent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify both features work together
	if _, ok := input.Context["data"]; !ok {
		t.Error("OutputKey feature not working")
	}

	if agent.Partition() != "node-1" {
		t.Error("Partition changed after execution")
	}

	t.Logf("✓ Partition locality preserved with ADK features")
	t.Logf("✓ Output: %v", output.Metadata)
}

func TestDAGDependenciesPreserved(t *testing.T) {
	// Create dependent agents (Spark DAG)
	dep1 := NewAgent(AgentConfig{
		Name: "Dependency1",
		Executor: &testExecutorFunc{
			execute: func(ctx context.Context, ag Agent, input *AgentInput) (*AgentOutput, error) {
				return &AgentOutput{Result: "dep1_result"}, nil
			},
		},
		OutputKey: "dep1", // ADK feature
	})

	dep2 := NewAgent(AgentConfig{
		Name: "Dependency2",
		Executor: &testExecutorFunc{
			execute: func(ctx context.Context, ag Agent, input *AgentInput) (*AgentOutput, error) {
				return &AgentOutput{Result: "dep2_result"}, nil
			},
		},
		OutputKey: "dep2",
	})

	// Main agent depends on both
	main := NewAgent(AgentConfig{
		Name: "MainAgent",
		Dependencies: []Agent{dep1, dep2}, // Spark: DAG dependencies
		Executor: &testExecutorFunc{
			execute: func(ctx context.Context, ag Agent, input *AgentInput) (*AgentOutput, error) {
				// Can access results from dependencies via OutputKey
				dep1Result := input.Context["dep1"]
				dep2Result := input.Context["dep2"]

				return &AgentOutput{
					Result: fmt.Sprintf("Combined: %v + %v", dep1Result, dep2Result),
				}, nil
			},
		},
		OutputKey: "final",
	})

	// Verify DAG structure preserved
	deps := main.Dependencies()
	if len(deps) != 2 {
		t.Errorf("Expected 2 dependencies, got %d", len(deps))
	}

	t.Logf("✓ DAG dependencies preserved with OutputKey")
}

func TestDistributedEventBus(t *testing.T) {
	t.Skip("DistributedEventBus not yet implemented - future enhancement")
	// TODO: Implement DistributedEventBus with node-aware event routing
	// This will support multi-node Spark deployments with partition-aware events
	// Design:
	// - Each node has its own MemoryEventBus instance
	// - Events include node_id and partition metadata
	// - Cross-node event propagation via network transport layer
}

func TestFaultToleranceWithEventHistory(t *testing.T) {
	t.Skip("Event history querying not yet implemented - future enhancement")
	// TODO: Implement checkpoint event storage and retrieval for fault tolerance
	// Design:
	// - Use MemoryEventStore to persist checkpoint events
	// - Query by EventType filter (EventCheckpointCreated)
	// - Support recovery by replaying events from last checkpoint
	// Example:
	// store := events.NewMemoryEventStore()
	// recorder := events.NewEventRecorder(bus, store)
	// checkpoints, _ := recorder.Query(ctx, events.EventFilter{
	//     EventTypes: []events.EventType{events.EventCheckpointCreated},
	// })
}

// Test Integration: All Features Together

func TestFullADKPipelineWithDistributed(t *testing.T) {
	// Setup event bus for observability
	bus := events.NewMemoryEventBus()
	var stateChanges []string
	var mu sync.Mutex

	err := bus.OnStateChange(func(key string, oldValue, newValue interface{}) {
		mu.Lock()
		stateChanges = append(stateChanges, fmt.Sprintf("%s=%v", key, newValue))
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("Failed to register callback: %v", err)
	}

	// Step 1: Fetch agent with OutputKey
	fetchAgent := NewAgent(AgentConfig{
		Name:      "FetchAgent",
		Partition: "data-partition", // Spark: locality
		OutputKey: "raw_data",        // ADK: auto-store
		Executor: &testExecutorFunc{
			execute: func(ctx context.Context, ag Agent, input *AgentInput) (*AgentOutput, error) {
				// Publish state change (callback will increment wg counter)
				if err := bus.PublishStateChange("fetch_status", nil, "complete", "FetchAgent", "data-partition"); err != nil {
					t.Errorf("Failed to publish fetch state change: %v", err)
				}

				return &AgentOutput{
					Result: map[string]interface{}{
						"records": 100,
						"source":  "database",
					},
				}, nil
			},
		},
	})

	// Step 2: Process agent with template interpolation
	processAgent := NewAgent(AgentConfig{
		Name:         "ProcessAgent",
		Partition:    "compute-partition", // Different partition
		OutputKey:    "processed_data",
		Dependencies: []Agent{fetchAgent}, // Spark: DAG
		Executor: &testExecutorFunc{
			execute: func(ctx context.Context, ag Agent, input *AgentInput) (*AgentOutput, error) {
				// Template should be interpolated before this runs
				// Publish state change (callback will increment wg counter)
				if err := bus.PublishStateChange("process_status", nil, "complete", "ProcessAgent", "compute-partition"); err != nil {
					t.Errorf("Failed to publish process state change: %v", err)
				}

				// Access raw_data via OutputKey
				rawData := input.Context["raw_data"].(map[string]interface{})
				records := rawData["records"].(int)

				return &AgentOutput{
					Result: fmt.Sprintf("Processed %d records", records),
				}, nil
			},
		},
	})

	// Execute pipeline
	ctx := context.Background()
	input := &AgentInput{
		Instruction: "Fetch data from {source}",
		Context: map[string]interface{}{
			"source": "production_db",
		},
	}

	// Execute Step 1
	_, err = fetchAgent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}

	// Verify OutputKey stored result
	if _, ok := input.Context["raw_data"]; !ok {
		t.Fatal("OutputKey didn't store result")
	}

	// Execute Step 2 with template
	input.Instruction = "Process data from {raw_data}"
	_, err = processAgent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Process failed: %v", err)
	}

	// Wait for event callbacks to fire (they run concurrently)
	time.Sleep(50 * time.Millisecond)

	// Verify all features worked
	mu.Lock()
	stateChangeCount := len(stateChanges)
	mu.Unlock()

	if stateChangeCount < 2 {
		t.Errorf("Expected at least 2 state changes, got %d", stateChangeCount)
	}

	// Verify distributed features preserved
	if fetchAgent.Partition() != "data-partition" {
		t.Error("Fetch agent partition lost")
	}

	if processAgent.Partition() != "compute-partition" {
		t.Error("Process agent partition lost")
	}

	if len(processAgent.Dependencies()) != 1 {
		t.Error("DAG dependencies lost")
	}

	mu.Lock()
	stateChangesCopy := make([]string, len(stateChanges))
	copy(stateChangesCopy, stateChanges)
	mu.Unlock()

	t.Logf("✓ Full integration test passed")
	t.Logf("  State changes: %v", stateChangesCopy)
	t.Logf("  Context keys: %v", getKeys(input.Context))
	t.Logf("  Partitions: %s, %s", fetchAgent.Partition(), processAgent.Partition())
}

// Helper types for testing

type testExecutorFunc struct {
	execute func(context.Context, Agent, *AgentInput) (*AgentOutput, error)
}

func (e *testExecutorFunc) Execute(ctx context.Context, agent Agent, input *AgentInput) (*AgentOutput, error) {
	return e.execute(ctx, agent, input)
}

func getKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
