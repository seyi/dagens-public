package agents

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/events"
	"github.com/seyi/dagens/pkg/tools"
)

// Test Phase 3: LLM Integration with AutoFlow
// Critical testing of LLM-driven agent transfers with Spark distributed computing support

// Test 1: Basic LlmAgentWithAutoFlow Creation
func TestLlmAgentWithAutoFlowCreation(t *testing.T) {
	modelProvider := &mockModelProvider{response: "test response"}
	toolRegistry := tools.NewToolRegistry()

	config := LlmAgentWithAutoFlowConfig{
		Name:          "TestAgent",
		Description:   "Test LLM agent",
		ModelName:     "mock-model",
		Instruction:   "You are a test agent",
		Tools:         []string{},
		Temperature:   0.7,
		MaxIterations: 5,
		AllowTransfer: true,
		TransferScope: agent.TransferScopeSubAgents,
		MaxTransfers:  10,
		Partition:     "test-partition",
	}

	llmAgent := NewLlmAgentWithAutoFlow(config, modelProvider, toolRegistry)

	if llmAgent.Name() != "TestAgent" {
		t.Errorf("Expected name 'TestAgent', got '%s'", llmAgent.Name())
	}

	if llmAgent.Description() != "Test LLM agent" {
		t.Errorf("Expected description 'Test LLM agent', got '%s'", llmAgent.Description())
	}

	if llmAgent.Partition() != "test-partition" {
		t.Errorf("Expected partition 'test-partition', got '%s'", llmAgent.Partition())
	}

	if !llmAgent.IsTransferEnabled() {
		t.Error("Expected transfers to be enabled")
	}

	if llmAgent.GetTransferScope() != agent.TransferScopeSubAgents {
		t.Errorf("Expected transfer scope 'sub_agents', got '%s'", llmAgent.GetTransferScope())
	}

	if llmAgent.GetAutoFlow() == nil {
		t.Error("Expected AutoFlow to be initialized")
	}

	t.Logf("✓ LlmAgentWithAutoFlow created successfully")
	t.Logf("  Name: %s", llmAgent.Name())
	t.Logf("  Partition: %s", llmAgent.Partition())
	t.Logf("  Transfer enabled: %v", llmAgent.IsTransferEnabled())
}

// Test 2: Auto-Registration of Transfer Tools
func TestAutoRegisterTransferTools(t *testing.T) {
	modelProvider := &mockModelProvider{response: "test"}
	toolRegistry := tools.NewToolRegistry()

	config := LlmAgentWithAutoFlowConfig{
		Name:                      "TestAgent",
		Description:               "Test",
		AllowTransfer:             true,
		AutoRegisterTransferTools: true,
		Tools:                     []string{"custom_tool"},
	}

	llmAgent := NewLlmAgentWithAutoFlow(config, modelProvider, toolRegistry)

	// Verify transfer tools were auto-registered
	expectedTools := []string{"transfer_to_agent", "get_available_agents", "validate_transfer_scope"}

	for _, toolName := range expectedTools {
		tool, err := toolRegistry.Get(toolName)
		if err != nil {
			t.Errorf("Expected tool '%s' to be registered", toolName)
		}
		if tool.Name != toolName {
			t.Errorf("Tool name mismatch: expected '%s', got '%s'", toolName, tool.Name)
		}
	}

	// Verify custom tool plus transfer tools are in enabled list
	if len(llmAgent.enabledTools) < 4 {
		t.Errorf("Expected at least 4 tools (custom + 3 transfer), got %d", len(llmAgent.enabledTools))
	}

	t.Logf("✓ Transfer tools auto-registered successfully")
	t.Logf("  Total enabled tools: %d", len(llmAgent.enabledTools))
	t.Logf("  Tools: %v", llmAgent.enabledTools)
}

// Test 3: Transfer Detection from LLM Output
func TestTransferDetectionFromLLMOutput(t *testing.T) {
	// Mock LLM response with transfer_to_agent call
	modelProvider := &mockModelProvider{
		response: `I need to transfer this to the booking agent.
<tool_use name="transfer_to_agent">{"agent_name": "BookingAgent", "reason": "User wants to book a flight"}</tool_use>`,
	}

	toolRegistry := tools.NewToolRegistry()

	config := LlmAgentWithAutoFlowConfig{
		Name:                      "Coordinator",
		Description:               "Main coordinator",
		AllowTransfer:             true,
		AutoRegisterTransferTools: true,
		Partition:                 "coordinator-partition",
	}

	llmAgent := NewLlmAgentWithAutoFlow(config, modelProvider, toolRegistry)

	// Create executor
	executor := &llmWithAutoFlowExecutor{llmAgent: llmAgent}

	// Execute and detect transfer
	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Book a flight to Paris",
	}

	output, err := executor.executeLLMAndDetectTransfer(ctx, input)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Verify transfer was detected
	if !output.Metadata["transfer_requested"].(bool) {
		t.Error("Expected transfer_requested to be true")
	}

	if output.Metadata["target_agent"].(string) != "BookingAgent" {
		t.Errorf("Expected target_agent 'BookingAgent', got '%v'", output.Metadata["target_agent"])
	}

	if output.Metadata["reason"].(string) != "User wants to book a flight" {
		t.Errorf("Expected reason 'User wants to book a flight', got '%v'", output.Metadata["reason"])
	}

	t.Logf("✓ Transfer detection from LLM output successful")
	t.Logf("  Target agent: %s", output.Metadata["target_agent"])
	t.Logf("  Reason: %s", output.Metadata["reason"])
}

// Test 4: Integration with AutoFlow - Basic Transfer
func TestLLMAgentAutoFlowIntegration(t *testing.T) {
	toolRegistry := tools.NewToolRegistry()
	eventBus := events.NewMemoryEventBus()
	defer eventBus.Close()

	// Create coordinator that detects need to transfer
	coordinatorModel := &mockModelProvider{
		response: `<tool_use name="transfer_to_agent">{"agent_name": "Worker", "reason": "Specialized task"}</tool_use>`,
	}

	coordinator := NewLlmAgentWithAutoFlow(LlmAgentWithAutoFlowConfig{
		Name:                      "Coordinator",
		Description:               "Main coordinator",
		AllowTransfer:             true,
		AutoRegisterTransferTools: true,
		TransferScope:             agent.TransferScopeSubAgents,
		EventBus:                  eventBus,
		Partition:                 "coordinator-partition",
		MaxTransfers:              5,
	}, coordinatorModel, toolRegistry)

	// Create worker that returns final result
	workerModel := &mockModelProvider{
		response: "Task completed successfully",
	}

	worker := NewLlmAgentWithAutoFlow(LlmAgentWithAutoFlowConfig{
		Name:          "Worker",
		Description:   "Worker agent",
		AllowTransfer: false, // No further transfers
		Partition:     "worker-partition",
	}, workerModel, toolRegistry)

	coordinator.AddSubAgent(worker)

	// Execute through AutoFlow
	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Execute specialized task",
	}

	output, err := coordinator.GetAutoFlow().Execute(ctx, coordinator, input)
	if err != nil {
		t.Fatalf("AutoFlow execution failed: %v", err)
	}

	// Verify transfer occurred
	if !output.Metadata["transfer_occurred"].(bool) {
		t.Error("Expected transfer to occur")
	}

	transferPath := output.Metadata["transfer_path"].([]string)
	if len(transferPath) != 2 {
		t.Errorf("Expected transfer path length 2, got %d", len(transferPath))
	}

	if transferPath[0] != "Coordinator" || transferPath[1] != "Worker" {
		t.Errorf("Expected transfer path [Coordinator, Worker], got %v", transferPath)
	}

	t.Logf("✓ AutoFlow integration successful")
	t.Logf("  Transfer path: %v", transferPath)
	t.Logf("  Transfer count: %d", output.Metadata["transfer_count"])
}

// Test 5: Distributed Scenario - Cross-Partition Transfer
func TestCrossPartitionLLMTransfer(t *testing.T) {
	toolRegistry := tools.NewToolRegistry()
	eventBus := events.NewMemoryEventBus()
	defer eventBus.Close()

	// Track transfer events
	var transferEvents []events.TransferCompletedData
	eventBus.Subscribe(events.EventTransferCompleted, func(ctx context.Context, event events.Event) error {
		if data, ok := event.Data().(events.TransferCompletedData); ok {
			transferEvents = append(transferEvents, data)
		}
		return nil
	})

	// Create coordinator on partition-0
	coordinatorModel := &mockModelProvider{
		response: `<tool_use name="transfer_to_agent">{"agent_name": "DataProcessor", "reason": "Process data on worker partition"}</tool_use>`,
	}

	coordinator := NewLlmAgentWithAutoFlow(LlmAgentWithAutoFlowConfig{
		Name:                      "Coordinator",
		Description:               "Coordinator on partition 0",
		AllowTransfer:             true,
		AutoRegisterTransferTools: true,
		TransferScope:             agent.TransferScopeSubAgents,
		EventBus:                  eventBus,
		Partition:                 "partition-0",
		NodeID:                    "node-1",
		MaxTransfers:              10,
	}, coordinatorModel, toolRegistry)

	// Create data processor on partition-1
	processorModel := &mockModelProvider{
		response: "Data processed on partition-1",
	}

	dataProcessor := NewLlmAgentWithAutoFlow(LlmAgentWithAutoFlowConfig{
		Name:          "DataProcessor",
		Description:   "Data processor on partition 1",
		AllowTransfer: false,
		Partition:     "partition-1",
		NodeID:        "node-2",
	}, processorModel, toolRegistry)

	coordinator.AddSubAgent(dataProcessor)

	// Execute
	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Process data",
	}

	output, err := coordinator.GetAutoFlow().Execute(ctx, coordinator, input)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Verify cross-partition transfer
	if !output.Metadata["cross_partition"].(bool) {
		t.Error("Expected cross-partition transfer")
	}

	// Verify event contains partition information
	if len(transferEvents) != 1 {
		t.Errorf("Expected 1 transfer event, got %d", len(transferEvents))
	}

	if len(transferEvents) > 0 {
		event := transferEvents[0]
		if event.FromPartition != "partition-0" {
			t.Errorf("Expected from partition-0, got '%s'", event.FromPartition)
		}
		if event.ToPartition != "partition-1" {
			t.Errorf("Expected to partition-1, got '%s'", event.ToPartition)
		}
		if event.TransferType != "remote" {
			t.Errorf("Expected transfer type 'remote', got '%s'", event.TransferType)
		}
	}

	t.Logf("✓ Cross-partition LLM transfer successful")
	t.Logf("  From partition: %s", transferEvents[0].FromPartition)
	t.Logf("  To partition: %s", transferEvents[0].ToPartition)
	t.Logf("  Transfer type: %s", transferEvents[0].TransferType)
}

// Test 6: Multi-Step Distributed Workflow
func TestMultiStepDistributedLLMWorkflow(t *testing.T) {
	toolRegistry := tools.NewToolRegistry()
	eventBus := events.NewMemoryEventBus()
	defer eventBus.Close()

	// Create travel coordinator on partition-0
	coordinatorModel := &mockModelProvider{
		response: `<tool_use name="transfer_to_agent">{"agent_name": "FlightBooker", "reason": "Book flight"}</tool_use>`,
	}

	coordinator := NewLlmAgentWithAutoFlow(LlmAgentWithAutoFlowConfig{
		Name:                      "TravelCoordinator",
		Description:               "Coordinates travel bookings",
		AllowTransfer:             true,
		AutoRegisterTransferTools: true,
		TransferScope:             agent.TransferScopeAll,
		EventBus:                  eventBus,
		Partition:                 "coordinator-partition",
		NodeID:                    "node-0",
		MaxTransfers:              10,
	}, coordinatorModel, toolRegistry)

	// Create flight booker on partition-1
	flightModel := &mockModelProvider{
		response: `Flight booked. <tool_use name="transfer_to_agent">{"agent_name": "HotelBooker", "reason": "Book hotel"}</tool_use>`,
	}

	flightBooker := NewLlmAgentWithAutoFlow(LlmAgentWithAutoFlowConfig{
		Name:                      "FlightBooker",
		Description:               "Books flights",
		AllowTransfer:             true,
		AutoRegisterTransferTools: true,
		TransferScope:             agent.TransferScopeAll,
		EventBus:                  eventBus,
		Partition:                 "flight-partition",
		NodeID:                    "node-1",
		MaxTransfers:              10,
	}, flightModel, toolRegistry)

	// Create hotel booker on partition-2
	hotelModel := &mockModelProvider{
		response: "Hotel booked successfully",
	}

	hotelBooker := NewLlmAgentWithAutoFlow(LlmAgentWithAutoFlowConfig{
		Name:          "HotelBooker",
		Description:   "Books hotels",
		AllowTransfer: false,
		Partition:     "hotel-partition",
		NodeID:        "node-2",
	}, hotelModel, toolRegistry)

	// Setup hierarchy (coordinator can reach both via TransferScopeAll)
	coordinator.AddSubAgent(flightBooker)
	coordinator.AddSubAgent(hotelBooker)

	// Execute workflow
	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Book complete travel package",
	}

	output, err := coordinator.GetAutoFlow().Execute(ctx, coordinator, input)
	if err != nil {
		t.Fatalf("Workflow execution failed: %v", err)
	}

	// Verify multi-step transfer
	transferPath := output.Metadata["transfer_path"].([]string)
	expectedPath := []string{"TravelCoordinator", "FlightBooker", "HotelBooker"}

	if len(transferPath) != len(expectedPath) {
		t.Errorf("Expected transfer path length %d, got %d", len(expectedPath), len(transferPath))
	}

	for i, expected := range expectedPath {
		if transferPath[i] != expected {
			t.Errorf("Transfer path[%d]: expected '%s', got '%s'", i, expected, transferPath[i])
		}
	}

	// Verify cross-partition occurred
	if !output.Metadata["cross_partition"].(bool) {
		t.Error("Expected cross-partition transfer in workflow")
	}

	transferCount := output.Metadata["transfer_count"].(int)
	if transferCount != 2 {
		t.Errorf("Expected 2 transfers, got %d", transferCount)
	}

	t.Logf("✓ Multi-step distributed workflow successful")
	t.Logf("  Transfer path: %v", transferPath)
	t.Logf("  Transfer count: %d", transferCount)
	t.Logf("  Cross-partition: %v", output.Metadata["cross_partition"])
}

// Test 7: Transfer Scope Configuration
func TestLLMAgentTransferScopeConfiguration(t *testing.T) {
	toolRegistry := tools.NewToolRegistry()

	// Create parent agent
	parentModel := &mockModelProvider{
		response: `<tool_use name="transfer_to_agent">{"agent_name": "Child", "reason": "Delegate"}</tool_use>`,
	}

	parent := NewLlmAgentWithAutoFlow(LlmAgentWithAutoFlowConfig{
		Name:                      "Parent",
		Description:               "Parent agent",
		AllowTransfer:             true,
		AutoRegisterTransferTools: true,
		TransferScope:             agent.TransferScopeSubAgents,
		MaxTransfers:              5,
	}, parentModel, toolRegistry)

	// Create child agent
	childModel := &mockModelProvider{
		response: "Child completed task",
	}

	child := NewLlmAgentWithAutoFlow(LlmAgentWithAutoFlowConfig{
		Name:          "Child",
		Description:   "Child agent",
		AllowTransfer: false,
	}, childModel, toolRegistry)

	parent.AddSubAgent(child)

	// Test 1: SubAgents scope should allow transfer to child
	ctx := context.Background()
	input := &agent.AgentInput{Instruction: "Test"}

	output, err := parent.GetAutoFlow().Execute(ctx, parent, input)
	if err != nil {
		t.Errorf("Transfer should succeed with SubAgents scope: %v", err)
	}

	transferPath := output.Metadata["transfer_path"].([]string)
	if len(transferPath) != 2 || transferPath[1] != "Child" {
		t.Errorf("Expected transfer to Child, got path: %v", transferPath)
	}

	// Test 2: Change scope to Disabled
	parent.SetTransferScope(agent.TransferScopeDisabled)

	output, err = parent.GetAutoFlow().Execute(ctx, parent, input)
	if err == nil {
		t.Error("Expected error when transfers disabled")
	}

	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("Expected 'disabled' error, got: %v", err)
	}

	t.Logf("✓ Transfer scope configuration working correctly")
	t.Logf("  SubAgents scope: transfer successful")
	t.Logf("  Disabled scope: transfer blocked")
}

// Test 8: No Transfer When AllowTransfer=false
func TestLLMAgentWithoutTransfer(t *testing.T) {
	modelProvider := &mockModelProvider{
		response: "Direct response without transfer",
	}

	toolRegistry := tools.NewToolRegistry()

	config := LlmAgentWithAutoFlowConfig{
		Name:          "SimpleAgent",
		Description:   "Agent without transfer support",
		AllowTransfer: false, // Transfers disabled
	}

	llmAgent := NewLlmAgentWithAutoFlow(config, modelProvider, toolRegistry)

	if llmAgent.IsTransferEnabled() {
		t.Error("Expected transfers to be disabled")
	}

	if llmAgent.GetAutoFlow() != nil {
		t.Error("Expected AutoFlow to be nil when transfers disabled")
	}

	// Execute - should work without AutoFlow
	ctx := context.Background()
	input := &agent.AgentInput{Instruction: "Test"}

	output, err := llmAgent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	if output.Result != "Direct response without transfer" {
		t.Errorf("Expected direct response, got '%v'", output.Result)
	}

	// No transfer metadata should be present
	if _, ok := output.Metadata["transfer_occurred"]; ok {
		t.Error("Expected no transfer metadata")
	}

	t.Logf("✓ LLM agent without transfer support working correctly")
}

// Test 9: Max Transfer Limit with LLM Agents
func TestLLMAgentMaxTransferLimit(t *testing.T) {
	toolRegistry := tools.NewToolRegistry()

	// Create agents that always transfer to each other (circular)
	agent1Model := &mockModelProvider{
		response: `<tool_use name="transfer_to_agent">{"agent_name": "Agent2", "reason": "Transfer"}</tool_use>`,
	}

	agent1 := NewLlmAgentWithAutoFlow(LlmAgentWithAutoFlowConfig{
		Name:                      "Agent1",
		Description:               "First agent",
		AllowTransfer:             true,
		AutoRegisterTransferTools: true,
		TransferScope:             agent.TransferScopeAll,
		MaxTransfers:              3, // Low limit
	}, agent1Model, toolRegistry)

	agent2Model := &mockModelProvider{
		response: `<tool_use name="transfer_to_agent">{"agent_name": "Agent1", "reason": "Transfer back"}</tool_use>`,
	}

	agent2 := NewLlmAgentWithAutoFlow(LlmAgentWithAutoFlowConfig{
		Name:                      "Agent2",
		Description:               "Second agent",
		AllowTransfer:             true,
		AutoRegisterTransferTools: true,
		TransferScope:             agent.TransferScopeAll,
		MaxTransfers:              3,
	}, agent2Model, toolRegistry)

	// Make them siblings
	root := agent.NewAgent(agent.AgentConfig{Name: "Root"})
	root.AddSubAgent(agent1)
	root.AddSubAgent(agent2)

	// Execute - should hit max transfer limit
	ctx := context.Background()
	input := &agent.AgentInput{Instruction: "Test"}

	_, err := agent1.GetAutoFlow().Execute(ctx, agent1, input)
	if err == nil {
		t.Error("Expected error due to max transfer limit")
	}

	if !strings.Contains(err.Error(), "maximum") && !strings.Contains(err.Error(), "limit") {
		t.Errorf("Expected max transfer error, got: %v", err)
	}

	t.Logf("✓ Max transfer limit enforced correctly")
	t.Logf("  Error: %v", err)
}

// Test 10: Event Bus Integration
func TestLLMAgentEventBusIntegration(t *testing.T) {
	toolRegistry := tools.NewToolRegistry()
	eventBus := events.NewMemoryEventBus()
	defer eventBus.Close()

	// Track all transfer events
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

	// Create agents with event bus
	coordinatorModel := &mockModelProvider{
		response: `<tool_use name="transfer_to_agent">{"agent_name": "Worker", "reason": "Event test"}</tool_use>`,
	}

	coordinator := NewLlmAgentWithAutoFlow(LlmAgentWithAutoFlowConfig{
		Name:                      "Coordinator",
		Description:               "Test coordinator",
		AllowTransfer:             true,
		AutoRegisterTransferTools: true,
		TransferScope:             agent.TransferScopeSubAgents,
		EventBus:                  eventBus,
		Partition:                 "p0",
	}, coordinatorModel, toolRegistry)

	workerModel := &mockModelProvider{
		response: "Work complete",
	}

	worker := NewLlmAgentWithAutoFlow(LlmAgentWithAutoFlowConfig{
		Name:          "Worker",
		Description:   "Test worker",
		AllowTransfer: false,
		Partition:     "p1",
	}, workerModel, toolRegistry)

	coordinator.AddSubAgent(worker)

	// Execute
	ctx := context.Background()
	input := &agent.AgentInput{Instruction: "Test events"}

	_, err := coordinator.GetAutoFlow().Execute(ctx, coordinator, input)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Verify events were published
	// Note: Request events come from tool handler, completion from AutoFlow
	if len(completedEvents) != 1 {
		t.Errorf("Expected 1 completion event, got %d", len(completedEvents))
	}

	if len(completedEvents) > 0 {
		event := completedEvents[0]
		if event.FromAgent != "Coordinator" {
			t.Errorf("Expected from 'Coordinator', got '%s'", event.FromAgent)
		}
		if event.ToAgent != "Worker" {
			t.Errorf("Expected to 'Worker', got '%s'", event.ToAgent)
		}
		if event.TransferType != "remote" { // Different partitions
			t.Errorf("Expected transfer type 'remote', got '%s'", event.TransferType)
		}
	}

	t.Logf("✓ Event bus integration working correctly")
	t.Logf("  Completion events: %d", len(completedEvents))
}

// Test 11: Complete End-to-End LLM-Driven Delegation
func TestCompleteEndToEndLLMDelegation(t *testing.T) {
	toolRegistry := tools.NewToolRegistry()
	eventBus := events.NewMemoryEventBus()
	defer eventBus.Close()

	// Scenario: Customer service with specialized agents
	// MainAgent -> identifies need -> TechnicalSupport or BillingSupport

	mainModel := &mockModelProvider{
		response: `The user has a billing question. <tool_use name="transfer_to_agent">{"agent_name": "BillingSupport", "reason": "User inquiring about invoice"}</tool_use>`,
	}

	mainAgent := NewLlmAgentWithAutoFlow(LlmAgentWithAutoFlowConfig{
		Name:                      "CustomerService",
		Description:               "Main customer service agent",
		Instruction:               "Help customers and delegate to specialists",
		AllowTransfer:             true,
		AutoRegisterTransferTools: true,
		TransferScope:             agent.TransferScopeSubAgents,
		EventBus:                  eventBus,
		Partition:                 "main-partition",
		Temperature:               0.7,
		MaxIterations:             5,
		MaxTransfers:              5,
	}, mainModel, toolRegistry)

	technicalModel := &mockModelProvider{
		response: "Technical issue resolved",
	}

	technicalSupport := NewLlmAgentWithAutoFlow(LlmAgentWithAutoFlowConfig{
		Name:          "TechnicalSupport",
		Description:   "Handles technical issues",
		AllowTransfer: false,
		Partition:     "technical-partition",
	}, technicalModel, toolRegistry)

	billingModel := &mockModelProvider{
		response: "Billing inquiry resolved. Invoice sent.",
	}

	billingSupport := NewLlmAgentWithAutoFlow(LlmAgentWithAutoFlowConfig{
		Name:          "BillingSupport",
		Description:   "Handles billing inquiries",
		AllowTransfer: false,
		Partition:     "billing-partition",
	}, billingModel, toolRegistry)

	mainAgent.AddSubAgent(technicalSupport)
	mainAgent.AddSubAgent(billingSupport)

	// Execute
	startTime := time.Now()
	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "I have a question about my invoice",
	}

	output, err := mainAgent.GetAutoFlow().Execute(ctx, mainAgent, input)
	duration := time.Since(startTime)

	if err != nil {
		t.Fatalf("End-to-end execution failed: %v", err)
	}

	// Verify correct agent was selected
	transferPath := output.Metadata["transfer_path"].([]string)
	if len(transferPath) != 2 {
		t.Errorf("Expected 2 agents in path, got %d: %v", len(transferPath), transferPath)
	}

	if transferPath[1] != "BillingSupport" {
		t.Errorf("Expected transfer to BillingSupport, got '%s'", transferPath[1])
	}

	// Verify final result contains billing response
	if !strings.Contains(fmt.Sprintf("%v", output.Result), "Billing") {
		t.Errorf("Expected billing response, got: %v", output.Result)
	}

	// Verify cross-partition transfer occurred
	if !output.Metadata["cross_partition"].(bool) {
		t.Error("Expected cross-partition transfer")
	}

	t.Logf("✓ Complete end-to-end LLM-driven delegation successful")
	t.Logf("  Transfer path: %v", transferPath)
	t.Logf("  Final result: %v", output.Result)
	t.Logf("  Cross-partition: %v", output.Metadata["cross_partition"])
	t.Logf("  Duration: %v", duration)
}

// Test 12: Performance Benchmark - Multiple LLM Transfers
func TestLLMAgentPerformance(t *testing.T) {
	toolRegistry := tools.NewToolRegistry()

	// Create a chain of 10 agents that transfer sequentially
	agents := make([]*LlmAgentWithAutoFlow, 10)

	for i := 0; i < 10; i++ {
		var response string
		if i < 9 {
			// Transfer to next agent
			response = fmt.Sprintf(`<tool_use name="transfer_to_agent">{"agent_name": "Agent%d", "reason": "Chain transfer"}</tool_use>`, i+1)
		} else {
			// Last agent returns result
			response = "Chain complete"
		}

		model := &mockModelProvider{response: response}

		agents[i] = NewLlmAgentWithAutoFlow(LlmAgentWithAutoFlowConfig{
			Name:                      fmt.Sprintf("Agent%d", i),
			Description:               fmt.Sprintf("Agent %d in chain", i),
			AllowTransfer:             i < 9, // Last agent doesn't transfer
			AutoRegisterTransferTools: true,
			TransferScope:             agent.TransferScopeAll,
			Partition:                 fmt.Sprintf("partition-%d", i%3), // 3 partitions
			MaxTransfers:              15,
		}, model, toolRegistry)
	}

	// Link agents
	root := agent.NewAgent(agent.AgentConfig{Name: "Root"})
	for i := 0; i < 10; i++ {
		root.AddSubAgent(agents[i])
	}

	// Execute and measure performance
	startTime := time.Now()
	ctx := context.Background()
	input := &agent.AgentInput{Instruction: "Start chain"}

	output, err := agents[0].GetAutoFlow().Execute(ctx, agents[0], input)
	duration := time.Since(startTime)

	if err != nil {
		t.Fatalf("Performance test failed: %v", err)
	}

	// Verify all transfers occurred
	transferCount := output.Metadata["transfer_count"].(int)
	if transferCount != 9 {
		t.Errorf("Expected 9 transfers, got %d", transferCount)
	}

	// Calculate average transfer time
	avgTransferTime := duration / time.Duration(transferCount)

	t.Logf("✓ Performance benchmark completed")
	t.Logf("  Total transfers: %d", transferCount)
	t.Logf("  Total duration: %v", duration)
	t.Logf("  Average transfer time: %v", avgTransferTime)
	t.Logf("  Cross-partition: %v", output.Metadata["cross_partition"])

	// Performance assertion (should be reasonably fast)
	if duration > 10*time.Millisecond {
		t.Logf("  Warning: Performance slower than expected: %v", duration)
	}
}

// Test 13: Error Handling - Invalid Agent Name
func TestLLMAgentInvalidTargetAgent(t *testing.T) {
	toolRegistry := tools.NewToolRegistry()

	model := &mockModelProvider{
		response: `<tool_use name="transfer_to_agent">{"agent_name": "NonExistentAgent", "reason": "Invalid transfer"}</tool_use>`,
	}

	llmAgent := NewLlmAgentWithAutoFlow(LlmAgentWithAutoFlowConfig{
		Name:                      "TestAgent",
		Description:               "Test agent",
		AllowTransfer:             true,
		AutoRegisterTransferTools: true,
		TransferScope:             agent.TransferScopeSubAgents,
	}, model, toolRegistry)

	// Execute - should fail with clear error
	ctx := context.Background()
	input := &agent.AgentInput{Instruction: "Test"}

	_, err := llmAgent.GetAutoFlow().Execute(ctx, llmAgent, input)
	if err == nil {
		t.Error("Expected error for non-existent agent")
	}

	if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "NonExistentAgent") {
		t.Errorf("Expected 'not found' error, got: %v", err)
	}

	t.Logf("✓ Invalid agent name handled correctly")
	t.Logf("  Error: %v", err)
}

// Test 14: Distributed Context Serialization
func TestLLMAgentContextSerialization(t *testing.T) {
	toolRegistry := tools.NewToolRegistry()

	model := &mockModelProvider{
		response: `<tool_use name="transfer_to_agent">{"agent_name": "Worker", "reason": "Test serialization"}</tool_use>`,
	}

	coordinator := NewLlmAgentWithAutoFlow(LlmAgentWithAutoFlowConfig{
		Name:                      "Coordinator",
		Description:               "Test coordinator",
		AllowTransfer:             true,
		AutoRegisterTransferTools: true,
		TransferScope:             agent.TransferScopeSubAgents,
		Partition:                 "partition-0",
		NodeID:                    "node-1",
	}, model, toolRegistry)

	workerModel := &mockModelProvider{response: "Done"}
	worker := NewLlmAgentWithAutoFlow(LlmAgentWithAutoFlowConfig{
		Name:          "Worker",
		Description:   "Test worker",
		AllowTransfer: false,
		Partition:     "partition-1",
		NodeID:        "node-2",
	}, workerModel, toolRegistry)

	coordinator.AddSubAgent(worker)

	// Execute
	ctx := context.Background()
	input := &agent.AgentInput{Instruction: "Test"}

	_, err := coordinator.GetAutoFlow().Execute(ctx, coordinator, input)
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}

	// Get invocation context
	invocationCtx := coordinator.GetAutoFlow().GetInvocationContext()
	if invocationCtx == nil {
		t.Fatal("Expected invocation context to exist")
	}

	// Serialize context
	serialized, err := invocationCtx.Serialize()
	if err != nil {
		t.Fatalf("Context serialization failed: %v", err)
	}

	// Verify serialized data
	if serialized.CurrentAgentName != "Worker" {
		t.Errorf("Expected current agent 'Worker', got '%s'", serialized.CurrentAgentName)
	}

	if serialized.CurrentPartition != "partition-1" {
		t.Errorf("Expected partition 'partition-1', got '%s'", serialized.CurrentPartition)
	}

	if serialized.NodeID != "node-1" { // Original node ID
		t.Errorf("Expected node ID 'node-1', got '%s'", serialized.NodeID)
	}

	if len(serialized.TransferHistory) != 1 {
		t.Errorf("Expected 1 transfer in history, got %d", len(serialized.TransferHistory))
	}

	t.Logf("✓ Context serialization successful")
	t.Logf("  Current agent: %s", serialized.CurrentAgentName)
	t.Logf("  Current partition: %s", serialized.CurrentPartition)
	t.Logf("  Transfer history: %d entries", len(serialized.TransferHistory))
}
