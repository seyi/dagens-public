package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Test Phase 1: Invocation Context and Transfer Tracking
// Tests for LLM-driven agent delegation infrastructure with Spark distributed support

func TestInvocationContextCreation(t *testing.T) {
	// Create test agents
	rootAgent := NewAgent(AgentConfig{
		Name:        "RootAgent",
		Description: "Root coordinator agent",
		Partition:   "partition-0",
	})

	ctx := context.Background()
	input := &AgentInput{
		Instruction: "Test task",
		Context:     make(map[string]interface{}),
	}

	// Create invocation context
	invocationCtx := NewInvocationContext(ctx, input, rootAgent)

	// Verify initial state
	if invocationCtx.CurrentAgent.Name() != "RootAgent" {
		t.Errorf("Expected current agent to be RootAgent, got %s", invocationCtx.CurrentAgent.Name())
	}

	if invocationCtx.RootAgent.Name() != "RootAgent" {
		t.Errorf("Expected root agent to be RootAgent, got %s", invocationCtx.RootAgent.Name())
	}

	if len(invocationCtx.TransferHistory) != 0 {
		t.Errorf("Expected empty transfer history, got %d transfers", len(invocationCtx.TransferHistory))
	}

	if invocationCtx.MaxTransfers != 10 {
		t.Errorf("Expected default max transfers to be 10, got %d", invocationCtx.MaxTransfers)
	}

	if invocationCtx.CurrentPartition != "partition-0" {
		t.Errorf("Expected current partition to be partition-0, got %s", invocationCtx.CurrentPartition)
	}

	t.Logf("✓ InvocationContext created successfully")
	t.Logf("  Root Agent: %s", invocationCtx.RootAgent.Name())
	t.Logf("  Current Partition: %s", invocationCtx.CurrentPartition)
}

func TestBasicTransfer(t *testing.T) {
	// Create coordinator and sub-agents
	coordinator := NewAgent(AgentConfig{
		Name:        "Coordinator",
		Description: "Main coordinator",
		Partition:   "coordinator-partition",
	})

	bookingAgent := NewAgent(AgentConfig{
		Name:        "Booker",
		Description: "Handles bookings",
		Partition:   "booking-partition",
	})

	// Set up hierarchy
	coordinator.AddSubAgent(bookingAgent)

	// Create invocation context
	ctx := context.Background()
	input := &AgentInput{Instruction: "Book a flight"}
	invocationCtx := NewInvocationContext(ctx, input, coordinator)

	// Perform transfer
	err := invocationCtx.TransferTo(bookingAgent, "User requested booking")
	if err != nil {
		t.Fatalf("Transfer failed: %v", err)
	}

	// Verify transfer occurred
	if invocationCtx.CurrentAgent.Name() != "Booker" {
		t.Errorf("Expected current agent to be Booker after transfer, got %s", invocationCtx.CurrentAgent.Name())
	}

	if invocationCtx.GetTransferCount() != 1 {
		t.Errorf("Expected 1 transfer, got %d", invocationCtx.GetTransferCount())
	}

	// Check transfer history
	lastTransfer := invocationCtx.GetLastTransfer()
	if lastTransfer == nil {
		t.Fatal("Expected transfer in history")
	}

	if lastTransfer.FromAgent != "Coordinator" {
		t.Errorf("Expected transfer from Coordinator, got %s", lastTransfer.FromAgent)
	}

	if lastTransfer.ToAgent != "Booker" {
		t.Errorf("Expected transfer to Booker, got %s", lastTransfer.ToAgent)
	}

	if lastTransfer.Reason != "User requested booking" {
		t.Errorf("Expected reason 'User requested booking', got %s", lastTransfer.Reason)
	}

	t.Logf("✓ Basic transfer successful")
	t.Logf("  Transfer path: %v", invocationCtx.GetTransferPath())
}

func TestMaxTransferLimit(t *testing.T) {
	// Create agents in a chain
	agent1 := NewAgent(AgentConfig{Name: "Agent1", Partition: "p1"})
	agent2 := NewAgent(AgentConfig{Name: "Agent2", Partition: "p2"})
	agent3 := NewAgent(AgentConfig{Name: "Agent3", Partition: "p3"})

	ctx := context.Background()
	input := &AgentInput{Instruction: "Test"}
	invocationCtx := NewInvocationContext(ctx, input, agent1)

	// Set low max transfers for testing
	invocationCtx.SetMaxTransfers(2)

	// First transfer - should succeed
	err := invocationCtx.TransferTo(agent2, "First transfer")
	if err != nil {
		t.Fatalf("First transfer failed: %v", err)
	}

	// Second transfer - should succeed
	err = invocationCtx.TransferTo(agent3, "Second transfer")
	if err != nil {
		t.Fatalf("Second transfer failed: %v", err)
	}

	// Third transfer - should fail (exceeded limit)
	err = invocationCtx.TransferTo(agent1, "Third transfer")
	if err == nil {
		t.Error("Expected error for exceeding max transfers, got nil")
	}

	if invocationCtx.GetTransferCount() != 2 {
		t.Errorf("Expected 2 transfers, got %d", invocationCtx.GetTransferCount())
	}

	t.Logf("✓ Max transfer limit enforced")
	t.Logf("  Transfers completed: %d", invocationCtx.GetTransferCount())
	t.Logf("  Max allowed: %d", invocationCtx.MaxTransfers)
}

func TestTransferPath(t *testing.T) {
	// Create agent chain
	coordinator := NewAgent(AgentConfig{Name: "Coordinator", Partition: "p0"})
	agent1 := NewAgent(AgentConfig{Name: "Agent1", Partition: "p1"})
	agent2 := NewAgent(AgentConfig{Name: "Agent2", Partition: "p2"})
	agent3 := NewAgent(AgentConfig{Name: "Agent3", Partition: "p3"})

	ctx := context.Background()
	input := &AgentInput{Instruction: "Test"}
	invocationCtx := NewInvocationContext(ctx, input, coordinator)

	// Initial path should just be coordinator
	path := invocationCtx.GetTransferPath()
	if len(path) != 1 || path[0] != "Coordinator" {
		t.Errorf("Expected initial path [Coordinator], got %v", path)
	}

	// Transfer through multiple agents
	invocationCtx.TransferTo(agent1, "First")
	invocationCtx.TransferTo(agent2, "Second")
	invocationCtx.TransferTo(agent3, "Third")

	// Verify full path
	expectedPath := []string{"Coordinator", "Agent1", "Agent2", "Agent3"}
	path = invocationCtx.GetTransferPath()

	if len(path) != len(expectedPath) {
		t.Errorf("Expected path length %d, got %d", len(expectedPath), len(path))
	}

	for i, expected := range expectedPath {
		if path[i] != expected {
			t.Errorf("Expected path[%d] = %s, got %s", i, expected, path[i])
		}
	}

	t.Logf("✓ Transfer path tracked correctly")
	t.Logf("  Path: %v", path)
}

// Test Distributed Features: Cross-Partition Transfers

func TestCrossPartitionTransferDetection(t *testing.T) {
	// Create agents on different partitions (Spark distributed scenario)
	coordinatorAgent := NewAgent(AgentConfig{
		Name:        "Coordinator",
		Partition:   "partition-0",  // Node 1
		Description: "Main coordinator",
	})

	dataAgent := NewAgent(AgentConfig{
		Name:        "DataProcessor",
		Partition:   "partition-0",  // Same node
		Description: "Data processing",
	})

	computeAgent := NewAgent(AgentConfig{
		Name:        "ComputeEngine",
		Partition:   "partition-1",  // Different node
		Description: "Heavy computation",
	})

	ctx := context.Background()
	input := &AgentInput{Instruction: "Process data"}
	invocationCtx := NewInvocationContext(ctx, input, coordinatorAgent)

	// Local transfer (same partition)
	err := invocationCtx.TransferTo(dataAgent, "Local data processing")
	if err != nil {
		t.Fatalf("Local transfer failed: %v", err)
	}

	// Verify it's local
	if invocationCtx.GetLastTransfer().TransferType != TransferTypeLocal {
		t.Error("Expected local transfer type for same partition")
	}

	// Remote transfer (different partition)
	err = invocationCtx.TransferTo(computeAgent, "Remote computation")
	if err != nil {
		t.Fatalf("Remote transfer failed: %v", err)
	}

	// Verify it's remote
	if invocationCtx.GetLastTransfer().TransferType != TransferTypeRemote {
		t.Error("Expected remote transfer type for different partition")
	}

	// Check cross-partition detection
	if !invocationCtx.HasTransferredAcrossPartitions() {
		t.Error("Expected HasTransferredAcrossPartitions to be true")
	}

	if invocationCtx.GetPartitionTransferCount() != 1 {
		t.Errorf("Expected 1 cross-partition transfer, got %d", invocationCtx.GetPartitionTransferCount())
	}

	t.Logf("✓ Cross-partition transfer detection works")
	t.Logf("  Local transfers: %d", invocationCtx.GetTransferCount()-invocationCtx.GetPartitionTransferCount())
	t.Logf("  Remote transfers: %d", invocationCtx.GetPartitionTransferCount())
}

func TestTransferTypeClassification(t *testing.T) {
	// Test all transfer types
	agent1 := NewAgent(AgentConfig{Name: "Agent1", Partition: "partition-A"})
	agent2 := NewAgent(AgentConfig{Name: "Agent2", Partition: "partition-A"}) // Same
	agent3 := NewAgent(AgentConfig{Name: "Agent3", Partition: "partition-B"}) // Different

	ctx := context.Background()
	input := &AgentInput{Instruction: "Test"}
	invocationCtx := NewInvocationContext(ctx, input, agent1)
	invocationCtx.SetNodeID("node-1")

	// Transfer 1: Same partition (local)
	invocationCtx.TransferTo(agent2, "Same partition")
	if invocationCtx.GetLastTransfer().TransferType != TransferTypeLocal {
		t.Error("Expected TransferTypeLocal for same partition")
	}

	// Transfer 2: Different partition (remote)
	invocationCtx.TransferTo(agent3, "Different partition")
	if invocationCtx.GetLastTransfer().TransferType != TransferTypeRemote {
		t.Error("Expected TransferTypeRemote for different partition")
	}

	t.Logf("✓ Transfer type classification correct")
}

// Test Serialization for Distributed Execution

func TestInvocationContextSerialization(t *testing.T) {
	// Create context with some transfers
	coordinator := NewAgent(AgentConfig{
		Name:        "Coordinator",
		Partition:   "partition-0",
		Description: "Main coordinator",
	})

	agent1 := NewAgent(AgentConfig{
		Name:        "Agent1",
		Partition:   "partition-1",
		Description: "Worker agent",
	})

	ctx := context.Background()
	input := &AgentInput{
		Instruction: "Test task",
		Context: map[string]interface{}{
			"key1": "value1",
			"key2": 42,
		},
	}
	invocationCtx := NewInvocationContext(ctx, input, coordinator)
	invocationCtx.SetNodeID("node-1")
	invocationCtx.SetMaxTransfers(5)

	// Add some metadata
	invocationCtx.Metadata["test_key"] = "test_value"

	// Perform a transfer
	invocationCtx.TransferTo(agent1, "Transfer for testing")

	// Serialize
	serialized, err := invocationCtx.Serialize()
	if err != nil {
		t.Fatalf("Serialization failed: %v", err)
	}

	// Verify serialized data
	if serialized.CurrentAgentName != "Agent1" {
		t.Errorf("Expected current agent Agent1, got %s", serialized.CurrentAgentName)
	}

	if serialized.RootAgentName != "Coordinator" {
		t.Errorf("Expected root agent Coordinator, got %s", serialized.RootAgentName)
	}

	if len(serialized.TransferHistory) != 1 {
		t.Errorf("Expected 1 transfer in serialized history, got %d", len(serialized.TransferHistory))
	}

	if serialized.MaxTransfers != 5 {
		t.Errorf("Expected max transfers 5, got %d", serialized.MaxTransfers)
	}

	if serialized.NodeID != "node-1" {
		t.Errorf("Expected node ID node-1, got %s", serialized.NodeID)
	}

	if serialized.Metadata["test_key"] != "test_value" {
		t.Error("Metadata not preserved in serialization")
	}

	t.Logf("✓ Context serialization successful")
	t.Logf("  Serialized agent: %s", serialized.CurrentAgentName)
	t.Logf("  Transfer count: %d", len(serialized.TransferHistory))
}

func TestInvocationContextDeserialization(t *testing.T) {
	// Create original context
	coordinator := NewAgent(AgentConfig{
		Name:        "Coordinator",
		Partition:   "partition-0",
	})

	agent1 := NewAgent(AgentConfig{
		Name:        "Agent1",
		Partition:   "partition-1",
	})

	ctx := context.Background()
	input := &AgentInput{
		Instruction: "Test task",
		Context:     make(map[string]interface{}),
	}
	originalCtx := NewInvocationContext(ctx, input, coordinator)
	originalCtx.TransferTo(agent1, "Test transfer")

	// Serialize
	serialized, err := originalCtx.Serialize()
	if err != nil {
		t.Fatalf("Serialization failed: %v", err)
	}

	// Create agent resolver
	agentRegistry := map[string]Agent{
		coordinator.ID(): coordinator,
		agent1.ID():      agent1,
	}

	agentResolver := func(id string) (Agent, error) {
		agent, ok := agentRegistry[id]
		if !ok {
			return nil, nil
		}
		return agent, nil
	}

	// Deserialize
	deserializedCtx, err := serialized.Deserialize(ctx, agentResolver)
	if err != nil {
		t.Fatalf("Deserialization failed: %v", err)
	}

	// Verify deserialized context
	if deserializedCtx.CurrentAgent.Name() != "Agent1" {
		t.Errorf("Expected current agent Agent1, got %s", deserializedCtx.CurrentAgent.Name())
	}

	if deserializedCtx.RootAgent.Name() != "Coordinator" {
		t.Errorf("Expected root agent Coordinator, got %s", deserializedCtx.RootAgent.Name())
	}

	if len(deserializedCtx.TransferHistory) != 1 {
		t.Errorf("Expected 1 transfer in deserialized history, got %d", len(deserializedCtx.TransferHistory))
	}

	t.Logf("✓ Context deserialization successful")
	t.Logf("  Deserialized agent: %s", deserializedCtx.CurrentAgent.Name())
}

// Test Context Cloning for Parallel Execution

func TestInvocationContextClone(t *testing.T) {
	// Create original context
	coordinator := NewAgent(AgentConfig{Name: "Coordinator", Partition: "p0"})
	agent1 := NewAgent(AgentConfig{Name: "Agent1", Partition: "p1"})

	ctx := context.Background()
	input := &AgentInput{
		Instruction: "Test",
		Context: map[string]interface{}{
			"key": "value",
		},
	}
	originalCtx := NewInvocationContext(ctx, input, coordinator)
	originalCtx.SetNodeID("node-1")
	originalCtx.Metadata["original"] = true
	originalCtx.TransferTo(agent1, "Transfer")

	// Clone the context
	clonedCtx := originalCtx.Clone()

	// Verify clone has same state
	if clonedCtx.CurrentAgent.Name() != originalCtx.CurrentAgent.Name() {
		t.Error("Cloned context has different current agent")
	}

	if clonedCtx.GetTransferCount() != originalCtx.GetTransferCount() {
		t.Error("Cloned context has different transfer count")
	}

	if clonedCtx.NodeID != originalCtx.NodeID {
		t.Error("Cloned context has different node ID")
	}

	// Verify clone is independent (modifying clone doesn't affect original)
	agent2 := NewAgent(AgentConfig{Name: "Agent2", Partition: "p2"})
	clonedCtx.TransferTo(agent2, "Clone transfer")

	if originalCtx.GetTransferCount() == clonedCtx.GetTransferCount() {
		t.Error("Modifying cloned context affected original")
	}

	if originalCtx.GetTransferCount() != 1 {
		t.Errorf("Original context should have 1 transfer, got %d", originalCtx.GetTransferCount())
	}

	if clonedCtx.GetTransferCount() != 2 {
		t.Errorf("Cloned context should have 2 transfers, got %d", clonedCtx.GetTransferCount())
	}

	t.Logf("✓ Context cloning works correctly")
	t.Logf("  Original transfers: %d", originalCtx.GetTransferCount())
	t.Logf("  Cloned transfers: %d", clonedCtx.GetTransferCount())
}

// Test JSON Export for Debugging

func TestInvocationContextToJSON(t *testing.T) {
	coordinator := NewAgent(AgentConfig{Name: "Coordinator", Partition: "p0"})
	agent1 := NewAgent(AgentConfig{Name: "Agent1", Partition: "p1"})

	ctx := context.Background()
	input := &AgentInput{Instruction: "Test"}
	invocationCtx := NewInvocationContext(ctx, input, coordinator)
	invocationCtx.SetNodeID("test-node")
	invocationCtx.TransferTo(agent1, "Test transfer")

	// Export to JSON
	jsonStr, err := invocationCtx.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}

	// Verify JSON contains expected data
	expectedStrings := []string{
		"Coordinator",
		"Agent1",
		"test-node",
		"transfer_count",
		"transfer_path",
	}

	for _, expected := range expectedStrings {
		if !containsString(jsonStr, expected) {
			t.Errorf("Expected JSON to contain '%s'", expected)
		}
	}

	t.Logf("✓ JSON export successful")
	t.Logf("JSON:\n%s", jsonStr)
}

// Integration Test: Complete Transfer Scenario

func TestCompleteTransferScenario(t *testing.T) {
	// Simulate a multi-agent workflow with distributed execution
	// Coordinator on partition-0, specialized agents on other partitions

	coordinator := NewAgent(AgentConfig{
		Name:        "TravelCoordinator",
		Description: "Main travel coordinator",
		Partition:   "coordinator-partition",
	})

	flightAgent := NewAgent(AgentConfig{
		Name:        "FlightBooker",
		Description: "Handles flight bookings",
		Partition:   "booking-partition-1",
	})

	hotelAgent := NewAgent(AgentConfig{
		Name:        "HotelBooker",
		Description: "Handles hotel bookings",
		Partition:   "booking-partition-2",
	})

	infoAgent := NewAgent(AgentConfig{
		Name:        "InfoProvider",
		Description: "Provides travel information",
		Partition:   "info-partition",
	})

	// Set up hierarchy
	coordinator.AddSubAgent(flightAgent)
	coordinator.AddSubAgent(hotelAgent)
	coordinator.AddSubAgent(infoAgent)

	// Create invocation context
	ctx := context.Background()
	input := &AgentInput{
		Instruction: "Book a complete travel package",
		Context:     make(map[string]interface{}),
	}
	invocationCtx := NewInvocationContext(ctx, input, coordinator)
	invocationCtx.SetNodeID("main-node")
	invocationCtx.SetMaxTransfers(10)

	// Simulate LLM-driven transfers
	// Step 1: Coordinator transfers to FlightBooker
	err := invocationCtx.TransferTo(flightAgent, "User wants to book flights")
	if err != nil {
		t.Fatalf("Transfer to FlightBooker failed: %v", err)
	}

	// Step 2: After flights, transfer to HotelBooker
	err = invocationCtx.TransferTo(hotelAgent, "Flights booked, now booking hotel")
	if err != nil {
		t.Fatalf("Transfer to HotelBooker failed: %v", err)
	}

	// Step 3: Finally, transfer to InfoProvider for travel tips
	err = invocationCtx.TransferTo(infoAgent, "Provide travel information")
	if err != nil {
		t.Fatalf("Transfer to InfoProvider failed: %v", err)
	}

	// Verify complete scenario
	if invocationCtx.GetTransferCount() != 3 {
		t.Errorf("Expected 3 transfers, got %d", invocationCtx.GetTransferCount())
	}

	expectedPath := []string{"TravelCoordinator", "FlightBooker", "HotelBooker", "InfoProvider"}
	path := invocationCtx.GetTransferPath()

	if len(path) != len(expectedPath) {
		t.Errorf("Expected path length %d, got %d", len(expectedPath), len(path))
	}

	for i, expected := range expectedPath {
		if path[i] != expected {
			t.Errorf("Path mismatch at index %d: expected %s, got %s", i, expected, path[i])
		}
	}

	// Verify all transfers were cross-partition (distributed)
	if !invocationCtx.HasTransferredAcrossPartitions() {
		t.Error("Expected cross-partition transfers")
	}

	if invocationCtx.GetPartitionTransferCount() != 3 {
		t.Errorf("Expected 3 cross-partition transfers, got %d", invocationCtx.GetPartitionTransferCount())
	}

	// Verify last transfer details
	lastTransfer := invocationCtx.GetLastTransfer()
	if lastTransfer.ToPartition != "info-partition" {
		t.Errorf("Expected final partition to be info-partition, got %s", lastTransfer.ToPartition)
	}

	t.Logf("✓ Complete transfer scenario successful")
	t.Logf("  Transfer path: %v", path)
	t.Logf("  Total transfers: %d", invocationCtx.GetTransferCount())
	t.Logf("  Cross-partition: %d", invocationCtx.GetPartitionTransferCount())
	t.Logf("  Final agent: %s", invocationCtx.CurrentAgent.Name())
	t.Logf("  Final partition: %s", invocationCtx.CurrentPartition)
}

// Test Performance: Many Transfers

func TestManyTransfers(t *testing.T) {
	// Create 100 agents
	agents := make([]Agent, 100)
	for i := 0; i < 100; i++ {
		agents[i] = NewAgent(AgentConfig{
			Name:      fmt.Sprintf("Agent%d", i),
			Partition: fmt.Sprintf("partition-%d", i%10), // 10 partitions
		})
	}

	ctx := context.Background()
	input := &AgentInput{Instruction: "Test"}
	invocationCtx := NewInvocationContext(ctx, input, agents[0])
	invocationCtx.SetMaxTransfers(100)

	start := time.Now()

	// Perform many transfers
	for i := 1; i < 100; i++ {
		err := invocationCtx.TransferTo(agents[i], fmt.Sprintf("Transfer %d", i))
		if err != nil {
			t.Fatalf("Transfer %d failed: %v", i, err)
		}
	}

	duration := time.Since(start)

	if invocationCtx.GetTransferCount() != 99 {
		t.Errorf("Expected 99 transfers, got %d", invocationCtx.GetTransferCount())
	}

	t.Logf("✓ Performance test passed")
	t.Logf("  Transfers: %d", invocationCtx.GetTransferCount())
	t.Logf("  Duration: %v", duration)
	t.Logf("  Avg per transfer: %v", duration/99)
}

// Helper function
func containsString(s, substr string) bool {
	return strings.Contains(s, substr)
}
