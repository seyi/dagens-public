package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/events"
)

// Test Phase 2: AutoFlow Interceptor and Transfer Execution
// Tests for LLM-driven agent delegation with Spark distributed support

func TestAutoFlowCreation(t *testing.T) {
	rootAgent := NewAgent(AgentConfig{
		Name:      "Root",
		Partition: "partition-0",
	})

	config := DefaultAutoFlowConfig()
	autoflow := NewAutoFlow(rootAgent, config)

	if autoflow.rootAgent != rootAgent {
		t.Error("Root agent not set correctly")
	}

	if !autoflow.config.AllowTransfer {
		t.Error("Expected transfers to be allowed by default")
	}

	if autoflow.config.TransferScope != TransferScopeSubAgents {
		t.Errorf("Expected default scope to be SubAgents, got %v", autoflow.config.TransferScope)
	}

	t.Logf("✓ AutoFlow created successfully")
	t.Logf("  Transfer allowed: %v", autoflow.config.AllowTransfer)
	t.Logf("  Transfer scope: %s", autoflow.config.TransferScope)
}

func TestTransferScopeStrings(t *testing.T) {
	scopes := map[TransferScope]string{
		TransferScopeDisabled:    "disabled",
		TransferScopeParent:      "parent",
		TransferScopeSiblings:    "siblings",
		TransferScopeSubAgents:   "sub_agents",
		TransferScopeDescendants: "descendants",
		TransferScopeAll:         "all",
	}

	for scope, expected := range scopes {
		if scope.String() != expected {
			t.Errorf("Expected %v.String() to be '%s', got '%s'", scope, expected, scope.String())
		}
	}

	t.Logf("✓ Transfer scope strings correct")
}

// Test Basic Transfer Execution

func TestBasicTransferExecution(t *testing.T) {
	// Create agent hierarchy
	coordinator := NewAgent(AgentConfig{
		Name:      "Coordinator",
		Partition: "coordinator-partition",
		Executor: &mockExecutor{
			result: map[string]interface{}{
				"transfer_requested": true,
				"target_agent":       "Worker",
				"reason":             "Delegating work",
			},
		},
	})

	worker := NewAgent(AgentConfig{
		Name:      "Worker",
		Partition: "worker-partition",
		Executor: &mockExecutor{
			result: "Work completed",
		},
	})

	coordinator.AddSubAgent(worker)

	// Create AutoFlow
	config := DefaultAutoFlowConfig()
	config.TransferScope = TransferScopeSubAgents
	autoflow := NewAutoFlow(coordinator, config)

	// Execute
	ctx := context.Background()
	input := &AgentInput{
		Instruction: "Do work",
		Context:     make(map[string]interface{}),
	}

	output, err := autoflow.Execute(ctx, coordinator, input)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Verify transfer occurred
	if output.Metadata == nil || !output.Metadata["transfer_occurred"].(bool) {
		t.Error("Expected transfer_occurred to be true")
	}

	// Verify transfer path
	path := output.Metadata["transfer_path"].([]string)
	expectedPath := []string{"Coordinator", "Worker"}
	if len(path) != len(expectedPath) {
		t.Errorf("Expected path %v, got %v", expectedPath, path)
	}

	// Verify cross-partition detection
	if !output.Metadata["cross_partition"].(bool) {
		t.Error("Expected cross_partition to be true")
	}

	t.Logf("✓ Basic transfer execution successful")
	t.Logf("  Transfer path: %v", path)
	t.Logf("  Cross-partition: %v", output.Metadata["cross_partition"])
}

// Test Transfer Scope: SubAgents

func TestTransferScopeSubAgents(t *testing.T) {
	// Create hierarchy: Root -> Child1, Child2
	root := NewAgent(AgentConfig{
		Name:      "Root",
		Partition: "p0",
		Executor: &mockExecutor{
			result: map[string]interface{}{
				"transfer_requested": true,
				"target_agent":       "Child1",
			},
		},
	})

	child1 := NewAgent(AgentConfig{
		Name:      "Child1",
		Partition: "p1",
		Executor:  &mockExecutor{result: "Child1 result"},
	})

	child2 := NewAgent(AgentConfig{
		Name:      "Child2",
		Partition: "p2",
		Executor:  &mockExecutor{result: "Child2 result"},
	})

	root.AddSubAgent(child1)
	root.AddSubAgent(child2)

	// Test transfer to direct child (should succeed)
	config := DefaultAutoFlowConfig()
	config.TransferScope = TransferScopeSubAgents
	autoflow := NewAutoFlow(root, config)

	ctx := context.Background()
	input := &AgentInput{Context: make(map[string]interface{})}

	output, err := autoflow.Execute(ctx, root, input)
	if err != nil {
		t.Fatalf("Transfer to sub-agent failed: %v", err)
	}

	if !output.Metadata["transfer_occurred"].(bool) {
		t.Error("Expected transfer to succeed")
	}

	t.Logf("✓ Transfer to sub-agent successful")
}

func TestTransferScopeSubAgentsRejectsNonChild(t *testing.T) {
	// Create hierarchy where target is not a direct child
	root := NewAgent(AgentConfig{
		Name:      "Root",
		Partition: "p0",
		Executor: &mockExecutor{
			result: map[string]interface{}{
				"transfer_requested": true,
				"target_agent":       "Grandchild",  // Not direct child
			},
		},
	})

	child := NewAgent(AgentConfig{
		Name:      "Child",
		Partition: "p1",
	})

	grandchild := NewAgent(AgentConfig{
		Name:      "Grandchild",
		Partition: "p2",
		Executor:  &mockExecutor{result: "Grandchild result"},
	})

	child.AddSubAgent(grandchild)
	root.AddSubAgent(child)

	// Test transfer to grandchild with SubAgents scope (should fail)
	config := DefaultAutoFlowConfig()
	config.TransferScope = TransferScopeSubAgents
	autoflow := NewAutoFlow(root, config)

	ctx := context.Background()
	input := &AgentInput{Context: make(map[string]interface{})}

	_, err := autoflow.Execute(ctx, root, input)
	if err == nil {
		t.Error("Expected transfer to grandchild to fail with SubAgents scope")
	}

	if !strings.Contains(err.Error(), "not found in direct sub-agents") {
		t.Errorf("Expected error about direct sub-agents, got: %v", err)
	}

	t.Logf("✓ Transfer scope correctly rejects non-direct children")
}

// Test Transfer Scope: Descendants

func TestTransferScopeDescendants(t *testing.T) {
	// Create deep hierarchy
	root := NewAgent(AgentConfig{
		Name:      "Root",
		Partition: "p0",
		Executor: &mockExecutor{
			result: map[string]interface{}{
				"transfer_requested": true,
				"target_agent":       "Grandchild",
			},
		},
	})

	child := NewAgent(AgentConfig{
		Name:      "Child",
		Partition: "p1",
	})

	grandchild := NewAgent(AgentConfig{
		Name:      "Grandchild",
		Partition: "p2",
		Executor:  &mockExecutor{result: "Grandchild result"},
	})

	child.AddSubAgent(grandchild)
	root.AddSubAgent(child)

	// Test transfer to grandchild with Descendants scope (should succeed)
	config := DefaultAutoFlowConfig()
	config.TransferScope = TransferScopeDescendants
	autoflow := NewAutoFlow(root, config)

	ctx := context.Background()
	input := &AgentInput{Context: make(map[string]interface{})}

	output, err := autoflow.Execute(ctx, root, input)
	if err != nil {
		t.Fatalf("Transfer to descendant failed: %v", err)
	}

	if !output.Metadata["transfer_occurred"].(bool) {
		t.Error("Expected transfer to succeed")
	}

	path := output.Metadata["transfer_path"].([]string)
	if path[len(path)-1] != "Grandchild" {
		t.Errorf("Expected final agent to be Grandchild, got %s", path[len(path)-1])
	}

	t.Logf("✓ Transfer to descendant successful")
	t.Logf("  Transfer path: %v", path)
}

// Test Transfer Scope: Parent

func TestTransferScopeParent(t *testing.T) {
	// Create hierarchy where we start from child
	parent := NewAgent(AgentConfig{
		Name:      "Parent",
		Partition: "p0",
		Executor:  &mockExecutor{result: "Parent result"},
	})

	child := NewAgent(AgentConfig{
		Name:      "Child",
		Partition: "p1",
		Executor: &mockExecutor{
			result: map[string]interface{}{
				"transfer_requested": true,
				"target_agent":       "Parent",
			},
		},
	})

	parent.AddSubAgent(child)

	// Test transfer to parent (should succeed)
	config := DefaultAutoFlowConfig()
	config.TransferScope = TransferScopeParent
	autoflow := NewAutoFlow(parent, config)  // Root is parent

	ctx := context.Background()
	input := &AgentInput{Context: make(map[string]interface{})}

	// Execute from child
	output, err := autoflow.Execute(ctx, child, input)
	if err != nil {
		t.Fatalf("Transfer to parent failed: %v", err)
	}

	if !output.Metadata["transfer_occurred"].(bool) {
		t.Error("Expected transfer to succeed")
	}

	t.Logf("✓ Transfer to parent successful")
}

// Test Transfer Scope: Siblings

func TestTransferScopeSiblings(t *testing.T) {
	// Create hierarchy with siblings
	parent := NewAgent(AgentConfig{
		Name:      "Parent",
		Partition: "p0",
	})

	sibling1 := NewAgent(AgentConfig{
		Name:      "Sibling1",
		Partition: "p1",
		Executor: &mockExecutor{
			result: map[string]interface{}{
				"transfer_requested": true,
				"target_agent":       "Sibling2",
			},
		},
	})

	sibling2 := NewAgent(AgentConfig{
		Name:      "Sibling2",
		Partition: "p2",
		Executor:  &mockExecutor{result: "Sibling2 result"},
	})

	parent.AddSubAgent(sibling1)
	parent.AddSubAgent(sibling2)

	// Test transfer between siblings (should succeed)
	config := DefaultAutoFlowConfig()
	config.TransferScope = TransferScopeSiblings
	autoflow := NewAutoFlow(parent, config)

	ctx := context.Background()
	input := &AgentInput{Context: make(map[string]interface{})}

	output, err := autoflow.Execute(ctx, sibling1, input)
	if err != nil {
		t.Fatalf("Transfer to sibling failed: %v", err)
	}

	if !output.Metadata["transfer_occurred"].(bool) {
		t.Error("Expected transfer to succeed")
	}

	path := output.Metadata["transfer_path"].([]string)
	expectedPath := []string{"Sibling1", "Sibling2"}
	if len(path) != len(expectedPath) {
		t.Errorf("Expected path %v, got %v", expectedPath, path)
	}

	t.Logf("✓ Transfer to sibling successful")
}

// Test Transfer Scope: All

func TestTransferScopeAll(t *testing.T) {
	// Create complex hierarchy
	root := NewAgent(AgentConfig{
		Name:      "Root",
		Partition: "p0",
	})

	branch1 := NewAgent(AgentConfig{
		Name:      "Branch1",
		Partition: "p1",
		Executor: &mockExecutor{
			result: map[string]interface{}{
				"transfer_requested": true,
				"target_agent":       "Leaf2",  // In different branch
			},
		},
	})

	branch2 := NewAgent(AgentConfig{
		Name:      "Branch2",
		Partition: "p2",
	})

	leaf1 := NewAgent(AgentConfig{
		Name:      "Leaf1",
		Partition: "p3",
	})

	leaf2 := NewAgent(AgentConfig{
		Name:      "Leaf2",
		Partition: "p4",
		Executor:  &mockExecutor{result: "Leaf2 result"},
	})

	root.AddSubAgent(branch1)
	root.AddSubAgent(branch2)
	branch1.AddSubAgent(leaf1)
	branch2.AddSubAgent(leaf2)

	// Test transfer across branches with All scope (should succeed)
	config := DefaultAutoFlowConfig()
	config.TransferScope = TransferScopeAll
	autoflow := NewAutoFlow(root, config)

	ctx := context.Background()
	input := &AgentInput{Context: make(map[string]interface{})}

	output, err := autoflow.Execute(ctx, branch1, input)
	if err != nil {
		t.Fatalf("Transfer across branches failed: %v", err)
	}

	if !output.Metadata["transfer_occurred"].(bool) {
		t.Error("Expected transfer to succeed")
	}

	t.Logf("✓ Transfer with All scope successful")
}

// Test Transfer Scope: Disabled

func TestTransferScopeDisabled(t *testing.T) {
	root := NewAgent(AgentConfig{
		Name:      "Root",
		Partition: "p0",
		Executor: &mockExecutor{
			result: map[string]interface{}{
				"transfer_requested": true,
				"target_agent":       "Child",
			},
		},
	})

	child := NewAgent(AgentConfig{
		Name:      "Child",
		Partition: "p1",
		Executor:  &mockExecutor{result: "Child result"},
	})

	root.AddSubAgent(child)

	// Test with disabled transfers
	config := DefaultAutoFlowConfig()
	config.TransferScope = TransferScopeDisabled
	autoflow := NewAutoFlow(root, config)

	ctx := context.Background()
	input := &AgentInput{Context: make(map[string]interface{})}

	_, err := autoflow.Execute(ctx, root, input)
	if err == nil {
		t.Error("Expected transfer to fail when disabled")
	}

	if !strings.Contains(err.Error(), "transfers are disabled") {
		t.Errorf("Expected error about disabled transfers, got: %v", err)
	}

	t.Logf("✓ Transfer correctly disabled")
}

// Test Distributed Computing Features

func TestCrossPartitionTransfer(t *testing.T) {
	// Create agents on different partitions
	coordinator := NewAgent(AgentConfig{
		Name:      "Coordinator",
		Partition: "coordinator-partition",  // Partition 1
		Executor: &mockExecutor{
			result: map[string]interface{}{
				"transfer_requested": true,
				"target_agent":       "Worker",
			},
		},
	})

	worker := NewAgent(AgentConfig{
		Name:      "Worker",
		Partition: "worker-partition",  // Partition 2 (different)
		Executor:  &mockExecutor{result: "Worker completed task"},
	})

	coordinator.AddSubAgent(worker)

	// Create AutoFlow with distributed support
	config := DefaultAutoFlowConfig()
	config.EnableDistributedSupport = true
	config.NodeID = "node-1"
	autoflow := NewAutoFlow(coordinator, config)

	ctx := context.Background()
	input := &AgentInput{Context: make(map[string]interface{})}

	output, err := autoflow.Execute(ctx, coordinator, input)
	if err != nil {
		t.Fatalf("Cross-partition transfer failed: %v", err)
	}

	// Verify cross-partition detection
	if !output.Metadata["cross_partition"].(bool) {
		t.Error("Expected cross_partition to be true")
	}

	// Verify invocation context tracks partitions
	invCtx := autoflow.GetInvocationContext()
	if !invCtx.HasTransferredAcrossPartitions() {
		t.Error("Expected invocation context to detect cross-partition transfer")
	}

	if invCtx.GetPartitionTransferCount() != 1 {
		t.Errorf("Expected 1 cross-partition transfer, got %d", invCtx.GetPartitionTransferCount())
	}

	// Check last transfer type
	lastTransfer := invCtx.GetLastTransfer()
	if lastTransfer.TransferType != TransferTypeRemote {
		t.Errorf("Expected TransferTypeRemote, got %s", lastTransfer.TransferType)
	}

	t.Logf("✓ Cross-partition transfer detected correctly")
	t.Logf("  From partition: %s", lastTransfer.FromPartition)
	t.Logf("  To partition: %s", lastTransfer.ToPartition)
	t.Logf("  Transfer type: %s", lastTransfer.TransferType)
}

func TestLocalPartitionTransfer(t *testing.T) {
	// Create agents on same partition
	agent1 := NewAgent(AgentConfig{
		Name:      "Agent1",
		Partition: "shared-partition",  // Same partition
		Executor: &mockExecutor{
			result: map[string]interface{}{
				"transfer_requested": true,
				"target_agent":       "Agent2",
			},
		},
	})

	agent2 := NewAgent(AgentConfig{
		Name:      "Agent2",
		Partition: "shared-partition",  // Same partition
		Executor:  &mockExecutor{result: "Agent2 result"},
	})

	agent1.AddSubAgent(agent2)

	config := DefaultAutoFlowConfig()
	autoflow := NewAutoFlow(agent1, config)

	ctx := context.Background()
	input := &AgentInput{Context: make(map[string]interface{})}

	output, err := autoflow.Execute(ctx, agent1, input)
	if err != nil {
		t.Fatalf("Local partition transfer failed: %v", err)
	}

	// Verify it's NOT cross-partition
	if output.Metadata["cross_partition"].(bool) {
		t.Error("Expected cross_partition to be false for same partition")
	}

	// Verify transfer type is local
	invCtx := autoflow.GetInvocationContext()
	lastTransfer := invCtx.GetLastTransfer()
	if lastTransfer.TransferType != TransferTypeLocal {
		t.Errorf("Expected TransferTypeLocal, got %s", lastTransfer.TransferType)
	}

	t.Logf("✓ Local partition transfer detected correctly")
	t.Logf("  Transfer type: %s", lastTransfer.TransferType)
}

func TestPartitionLocalityCheck(t *testing.T) {
	coordinator := NewAgent(AgentConfig{
		Name:      "Coordinator",
		Partition: "partition-A",
	})

	localAgent := NewAgent(AgentConfig{
		Name:      "LocalAgent",
		Partition: "partition-A",  // Same
	})

	remoteAgent := NewAgent(AgentConfig{
		Name:      "RemoteAgent",
		Partition: "partition-B",  // Different
	})

	coordinator.AddSubAgent(localAgent)
	coordinator.AddSubAgent(remoteAgent)

	config := DefaultAutoFlowConfig()
	autoflow := NewAutoFlow(coordinator, config)

	// Create invocation context
	ctx := context.Background()
	input := &AgentInput{Context: make(map[string]interface{})}
	autoflow.invocationCtx = NewInvocationContext(ctx, input, coordinator)

	// Check local agent
	isLocal, sourcePartition, targetPartition := autoflow.CheckPartitionLocality(localAgent)
	if !isLocal {
		t.Error("Expected local transfer for same partition")
	}
	if sourcePartition != "partition-A" || targetPartition != "partition-A" {
		t.Errorf("Partition mismatch: %s -> %s", sourcePartition, targetPartition)
	}

	// Check remote agent
	isLocal, sourcePartition, targetPartition = autoflow.CheckPartitionLocality(remoteAgent)
	if isLocal {
		t.Error("Expected remote transfer for different partition")
	}
	if sourcePartition != "partition-A" || targetPartition != "partition-B" {
		t.Errorf("Partition mismatch: %s -> %s", sourcePartition, targetPartition)
	}

	t.Logf("✓ Partition locality check works correctly")
}

// Test Event Bus Integration

func TestTransferEventsPublished(t *testing.T) {
	// Create event bus
	eventBus := events.NewMemoryEventBus()
	defer eventBus.Close()

	// Track events
	var requestEvents []events.TransferRequestData
	var completedEvents []events.TransferCompletedData

	eventBus.Subscribe(events.EventTransferRequested, func(ctx context.Context, event events.Event) error {
		if data, ok := event.Data().(events.TransferRequestData); ok {
			requestEvents = append(requestEvents, data)
		}
		return nil
	})

	eventBus.Subscribe(events.EventTransferCompleted, func(ctx context.Context, event events.Event) error {
		if data, ok := event.Data().(events.TransferCompletedData); ok {
			completedEvents = append(completedEvents, data)
		}
		return nil
	})

	// Create agents
	coordinator := NewAgent(AgentConfig{
		Name:      "Coordinator",
		Partition: "p0",
		Executor: &mockExecutor{
			result: map[string]interface{}{
				"transfer_requested": true,
				"target_agent":       "Worker",
				"reason":             "Delegating work",
			},
		},
	})

	worker := NewAgent(AgentConfig{
		Name:      "Worker",
		Partition: "p1",
		Executor:  &mockExecutor{result: "Work done"},
	})

	coordinator.AddSubAgent(worker)

	// Execute with event bus
	config := DefaultAutoFlowConfig()
	config.EventBus = eventBus
	autoflow := NewAutoFlow(coordinator, config)

	ctx := context.Background()
	input := &AgentInput{Context: make(map[string]interface{})}

	_, err := autoflow.Execute(ctx, coordinator, input)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Give events time to be processed
	time.Sleep(10 * time.Millisecond)

	// Verify events were published
	// Note: Transfer request event is published by the transfer_to_agent tool
	// In this test, we use a mock that directly signals transfer via metadata,
	// so we only expect the completed event from AutoFlow

	if len(completedEvents) != 1 {
		t.Errorf("Expected 1 transfer completed event, got %d", len(completedEvents))
	}

	if len(completedEvents) > 0 {
		comp := completedEvents[0]
		if comp.TransferType != "remote" {
			t.Errorf("Expected remote transfer, got %s", comp.TransferType)
		}
		if comp.Duration <= 0 {
			t.Error("Expected positive duration")
		}
	}

	t.Logf("✓ Transfer events published correctly")
	t.Logf("  Completed events: %d", len(completedEvents))
	if len(completedEvents) > 0 {
		comp := completedEvents[0]
		t.Logf("  Duration: %v", comp.Duration)
		t.Logf("  Transfer type: %s", comp.TransferType)
	}
}

// Test Error Handling

func TestTransferMaxLimitEnforced(t *testing.T) {
	// Create circular reference that would cause infinite transfers
	agent1 := NewAgent(AgentConfig{
		Name:      "Agent1",
		Partition: "p1",
		Executor: &mockExecutor{
			result: map[string]interface{}{
				"transfer_requested": true,
				"target_agent":       "Agent2",
			},
		},
	})

	agent2 := NewAgent(AgentConfig{
		Name:      "Agent2",
		Partition: "p2",
		Executor: &mockExecutor{
			result: map[string]interface{}{
				"transfer_requested": true,
				"target_agent":       "Agent1",  // Back to Agent1
			},
		},
	})

	agent1.AddSubAgent(agent2)
	agent2.AddSubAgent(agent1)  // Circular

	// Set low max transfers
	config := DefaultAutoFlowConfig()
	config.MaxTransfers = 3
	config.TransferScope = TransferScopeAll
	autoflow := NewAutoFlow(agent1, config)

	ctx := context.Background()
	input := &AgentInput{Context: make(map[string]interface{})}

	_, err := autoflow.Execute(ctx, agent1, input)
	if err == nil {
		t.Error("Expected error for exceeding max transfers")
	}

	if !strings.Contains(err.Error(), "maximum transfers") {
		t.Errorf("Expected error about max transfers, got: %v", err)
	}

	t.Logf("✓ Max transfer limit enforced")
}

func TestTransferToNonExistentAgent(t *testing.T) {
	root := NewAgent(AgentConfig{
		Name:      "Root",
		Partition: "p0",
		Executor: &mockExecutor{
			result: map[string]interface{}{
				"transfer_requested": true,
				"target_agent":       "NonExistent",
			},
		},
	})

	config := DefaultAutoFlowConfig()
	autoflow := NewAutoFlow(root, config)

	ctx := context.Background()
	input := &AgentInput{Context: make(map[string]interface{})}

	_, err := autoflow.Execute(ctx, root, input)
	if err == nil {
		t.Error("Expected error for non-existent agent")
	}

	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("Expected error about agent not found, got: %v", err)
	}

	t.Logf("✓ Transfer to non-existent agent correctly rejected")
}

// Integration Test: Complex Multi-Agent Workflow

func TestCompleteMultiAgentWorkflow(t *testing.T) {
	// Create event bus for observability
	eventBus := events.NewMemoryEventBus()
	defer eventBus.Close()

	var allEvents []string
	eventBus.Subscribe(events.EventTransferRequested, func(ctx context.Context, event events.Event) error {
		data := event.Data().(events.TransferRequestData)
		allEvents = append(allEvents, fmt.Sprintf("REQUEST: %s->%s", data.FromAgent, data.ToAgent))
		return nil
	})
	eventBus.Subscribe(events.EventTransferCompleted, func(ctx context.Context, event events.Event) error {
		data := event.Data().(events.TransferCompletedData)
		allEvents = append(allEvents, fmt.Sprintf("COMPLETED: %s->%s (%s)", data.FromAgent, data.ToAgent, data.TransferType))
		return nil
	})

	// Create complex agent hierarchy simulating travel booking system
	coordinator := NewAgent(AgentConfig{
		Name:        "TravelCoordinator",
		Partition:   "coordinator-partition",
		Description: "Main travel coordinator",
		Executor: &mockExecutor{
			result: map[string]interface{}{
				"transfer_requested": true,
				"target_agent":       "FlightBooker",
				"reason":             "Book flights first",
			},
		},
	})

	flightBooker := NewAgent(AgentConfig{
		Name:        "FlightBooker",
		Partition:   "booking-partition-1",
		Description: "Handles flight bookings",
		Executor: &mockExecutor{
			result: map[string]interface{}{
				"transfer_requested": true,
				"target_agent":       "HotelBooker",
				"reason":             "Flights booked, now book hotel",
			},
		},
	})

	hotelBooker := NewAgent(AgentConfig{
		Name:        "HotelBooker",
		Partition:   "booking-partition-2",
		Description: "Handles hotel bookings",
		Executor: &mockExecutor{
			result: "All bookings completed",
		},
	})

	coordinator.AddSubAgent(flightBooker)
	coordinator.AddSubAgent(hotelBooker)

	// Execute workflow
	config := DefaultAutoFlowConfig()
	config.EventBus = eventBus
	config.EnableDistributedSupport = true
	config.NodeID = "main-node"
	config.TransferScope = TransferScopeAll  // Allow transfer to any agent in hierarchy
	autoflow := NewAutoFlow(coordinator, config)

	ctx := context.Background()
	input := &AgentInput{
		Instruction: "Book complete travel package",
		Context:     make(map[string]interface{}),
	}

	output, err := autoflow.Execute(ctx, coordinator, input)
	if err != nil {
		t.Fatalf("Workflow failed: %v", err)
	}

	// Wait for events
	time.Sleep(50 * time.Millisecond)

	// Verify workflow completed
	if !output.Metadata["transfer_occurred"].(bool) {
		t.Error("Expected transfers to occur")
	}

	transferCount := output.Metadata["transfer_count"].(int)
	if transferCount != 2 {
		t.Errorf("Expected 2 transfers, got %d", transferCount)
	}

	// Verify transfer path
	path := output.Metadata["transfer_path"].([]string)
	expectedPath := []string{"TravelCoordinator", "FlightBooker", "HotelBooker"}
	if len(path) != len(expectedPath) {
		t.Errorf("Expected path %v, got %v", expectedPath, path)
	}

	for i, expected := range expectedPath {
		if path[i] != expected {
			t.Errorf("Path mismatch at index %d: expected %s, got %s", i, expected, path[i])
		}
	}

	// Verify cross-partition transfers
	if !output.Metadata["cross_partition"].(bool) {
		t.Error("Expected cross-partition transfers")
	}

	invCtx := autoflow.GetInvocationContext()
	if invCtx.GetPartitionTransferCount() != 2 {
		t.Errorf("Expected 2 cross-partition transfers, got %d", invCtx.GetPartitionTransferCount())
	}

	// Verify events
	// Note: We only expect completion events since mocks don't call transfer_to_agent tool
	if len(allEvents) < 2 {  // 2 completions minimum
		t.Errorf("Expected at least 2 completion events, got %d", len(allEvents))
	}

	t.Logf("✓ Complete multi-agent workflow successful")
	t.Logf("  Transfer path: %v", path)
	t.Logf("  Transfer count: %d", transferCount)
	t.Logf("  Cross-partition transfers: %d", invCtx.GetPartitionTransferCount())
	t.Logf("  Events captured: %d", len(allEvents))
	t.Logf("  Event log:")
	for _, event := range allEvents {
		t.Logf("    - %s", event)
	}
}

// Performance Test

func TestAutoFlowPerformance(t *testing.T) {
	// Create chain of 20 agents
	agents := make([]Agent, 20)
	for i := 0; i < 20; i++ {
		nextAgent := ""
		if i < 19 {
			nextAgent = fmt.Sprintf("Agent%d", i+1)
		}

		var executor AgentExecutor
		if nextAgent != "" {
			executor = &mockExecutor{
				result: map[string]interface{}{
					"transfer_requested": true,
					"target_agent":       nextAgent,
				},
			}
		} else {
			executor = &mockExecutor{result: "Final result"}
		}

		agents[i] = NewAgent(AgentConfig{
			Name:      fmt.Sprintf("Agent%d", i),
			Partition: fmt.Sprintf("partition-%d", i%5),  // 5 partitions
			Executor:  executor,
		})
	}

	// Link them in chain
	for i := 0; i < 19; i++ {
		if baseAgent, ok := agents[i].(*BaseAgent); ok {
			baseAgent.AddSubAgent(agents[i+1])
		}
	}

	// Execute with transfers
	config := DefaultAutoFlowConfig()
	config.MaxTransfers = 25
	config.TransferScope = TransferScopeDescendants
	autoflow := NewAutoFlow(agents[0], config)

	ctx := context.Background()
	input := &AgentInput{Context: make(map[string]interface{})}

	start := time.Now()
	output, err := autoflow.Execute(ctx, agents[0], input)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Performance test failed: %v", err)
	}

	transferCount := output.Metadata["transfer_count"].(int)
	if transferCount != 19 {
		t.Errorf("Expected 19 transfers, got %d", transferCount)
	}

	avgPerTransfer := duration / time.Duration(transferCount)

	t.Logf("✓ Performance test passed")
	t.Logf("  Transfers: %d", transferCount)
	t.Logf("  Total duration: %v", duration)
	t.Logf("  Avg per transfer: %v", avgPerTransfer)
}

// Helper: Mock Executor

type mockExecutor struct {
	result interface{}
}

func (m *mockExecutor) Execute(ctx context.Context, agent Agent, input *AgentInput) (*AgentOutput, error) {
	output := &AgentOutput{
		Result:   m.result,
		Metadata: make(map[string]interface{}),
	}

	// If result is a map with transfer_requested, copy to metadata
	if resultMap, ok := m.result.(map[string]interface{}); ok {
		for k, v := range resultMap {
			output.Metadata[k] = v
		}
	}

	return output, nil
}
