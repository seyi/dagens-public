package tools

import (
	"context"
	"testing"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/events"
)

// Test Phase 1: Transfer Tool Functionality
// Tests for transfer_to_agent tool and related helpers

func TestTransferToAgentToolCreation(t *testing.T) {
	tool := TransferToAgentTool()

	if tool.Name != "transfer_to_agent" {
		t.Errorf("Expected tool name 'transfer_to_agent', got '%s'", tool.Name)
	}

	if !tool.Enabled {
		t.Error("Expected transfer tool to be enabled")
	}

	if tool.Handler == nil {
		t.Error("Expected transfer tool to have a handler")
	}

	if tool.Schema == nil {
		t.Error("Expected transfer tool to have a schema")
	}

	t.Logf("✓ Transfer tool created successfully")
	t.Logf("  Name: %s", tool.Name)
	t.Logf("  Description: %s", tool.Description)
}

func TestTransferToAgentToolExecution(t *testing.T) {
	tool := TransferToAgentTool()

	// Create test context
	ctx := context.Background()
	params := map[string]interface{}{
		"agent_name": "TargetAgent",
		"reason":     "Test transfer",
	}

	// Execute tool
	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("Tool execution failed: %v", err)
	}

	// Verify result
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("Expected result to be a map")
	}

	if !resultMap["transfer_requested"].(bool) {
		t.Error("Expected transfer_requested to be true")
	}

	if resultMap["target_agent"].(string) != "TargetAgent" {
		t.Errorf("Expected target_agent to be 'TargetAgent', got '%v'", resultMap["target_agent"])
	}

	if resultMap["reason"].(string) != "Test transfer" {
		t.Errorf("Expected reason to be 'Test transfer', got '%v'", resultMap["reason"])
	}

	t.Logf("✓ Transfer tool execution successful")
	t.Logf("  Transfer requested: %v", resultMap["transfer_requested"])
	t.Logf("  Target agent: %s", resultMap["target_agent"])
}

func TestTransferToAgentToolMissingAgentName(t *testing.T) {
	tool := TransferToAgentTool()

	ctx := context.Background()
	params := map[string]interface{}{
		"reason": "Test",
		// Missing agent_name
	}

	// Execute tool - should fail
	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Error("Expected error for missing agent_name parameter")
	}

	t.Logf("✓ Tool correctly rejects missing agent_name")
}

func TestTransferToAgentToolEmptyAgentName(t *testing.T) {
	tool := TransferToAgentTool()

	ctx := context.Background()
	params := map[string]interface{}{
		"agent_name": "",
		"reason":     "Test",
	}

	// Execute tool - should fail
	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Error("Expected error for empty agent_name")
	}

	t.Logf("✓ Tool correctly rejects empty agent_name")
}

func TestTransferToAgentToolWithInvocationContext(t *testing.T) {
	tool := TransferToAgentTool()

	// Create test agents
	coordinator := agent.NewAgent(agent.AgentConfig{
		Name:        "Coordinator",
		Partition:   "partition-0",
		Description: "Main coordinator",
	})

	// Create invocation context
	ctx := context.Background()
	input := &agent.AgentInput{Instruction: "Test"}
	invocationCtx := agent.NewInvocationContext(ctx, input, coordinator)

	// Add invocation context to params
	params := map[string]interface{}{
		"agent_name":               "TargetAgent",
		"reason":                   "Test with context",
		"__invocation_context__":   invocationCtx,
	}

	// Execute tool
	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("Tool execution failed: %v", err)
	}

	// Verify result
	resultMap := result.(map[string]interface{})
	if !resultMap["transfer_requested"].(bool) {
		t.Error("Expected transfer_requested to be true")
	}

	t.Logf("✓ Transfer tool works with invocation context")
}

func TestTransferToAgentToolWithEventBus(t *testing.T) {
	tool := TransferToAgentTool()

	// Create event bus
	eventBus := events.NewMemoryEventBus()
	defer eventBus.Close()

	// Track transfer request events
	var receivedEvent *events.TransferRequestData
	eventBus.Subscribe(events.EventTransferRequested, func(ctx context.Context, event events.Event) error {
		if data, ok := event.Data().(events.TransferRequestData); ok {
			receivedEvent = &data
		}
		return nil
	})

	// Create test agents and context
	coordinator := agent.NewAgent(agent.AgentConfig{
		Name:      "Coordinator",
		Partition: "partition-0",
	})

	ctx := context.Background()
	input := &agent.AgentInput{Instruction: "Test"}
	invocationCtx := agent.NewInvocationContext(ctx, input, coordinator)

	// Execute tool with event bus
	params := map[string]interface{}{
		"agent_name":             "TargetAgent",
		"reason":                 "Event test",
		"__invocation_context__": invocationCtx,
		"__event_bus__":          eventBus,
	}

	_, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("Tool execution failed: %v", err)
	}

	// Give event time to be published
	// Note: In production, event publishing is async
	// For testing, we'll add a small delay
	// (In real implementation, events are published synchronously in tests)

	// Verify event was published
	if receivedEvent == nil {
		t.Fatal("Expected transfer request event to be published")
	}

	if receivedEvent.FromAgent != "Coordinator" {
		t.Errorf("Expected from agent 'Coordinator', got '%s'", receivedEvent.FromAgent)
	}

	if receivedEvent.ToAgent != "TargetAgent" {
		t.Errorf("Expected to agent 'TargetAgent', got '%s'", receivedEvent.ToAgent)
	}

	if receivedEvent.Reason != "Event test" {
		t.Errorf("Expected reason 'Event test', got '%s'", receivedEvent.Reason)
	}

	t.Logf("✓ Transfer tool publishes events correctly")
	t.Logf("  Event from: %s", receivedEvent.FromAgent)
	t.Logf("  Event to: %s", receivedEvent.ToAgent)
}

func TestGetAvailableAgentsTool(t *testing.T) {
	tool := GetAvailableAgentsTool()

	if tool.Name != "get_available_agents" {
		t.Errorf("Expected tool name 'get_available_agents', got '%s'", tool.Name)
	}

	// Create test hierarchy
	coordinator := agent.NewAgent(agent.AgentConfig{
		Name:        "Coordinator",
		Description: "Main coordinator",
		Partition:   "partition-0",
	})

	agent1 := agent.NewAgent(agent.AgentConfig{
		Name:         "Agent1",
		Description:  "First agent",
		Partition:    "partition-1",
		Capabilities: []string{"capability1"},
	})

	agent2 := agent.NewAgent(agent.AgentConfig{
		Name:         "Agent2",
		Description:  "Second agent",
		Partition:    "partition-2",
		Capabilities: []string{"capability2"},
	})

	coordinator.AddSubAgent(agent1)
	coordinator.AddSubAgent(agent2)

	// Create invocation context
	ctx := context.Background()
	input := &agent.AgentInput{Instruction: "Test"}
	invocationCtx := agent.NewInvocationContext(ctx, input, coordinator)

	// Execute tool
	params := map[string]interface{}{
		"__invocation_context__": invocationCtx,
	}

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("Tool execution failed: %v", err)
	}

	// Verify result
	resultMap := result.(map[string]interface{})
	agents := resultMap["agents"].([]map[string]interface{})

	if len(agents) != 2 {
		t.Errorf("Expected 2 available agents, got %d", len(agents))
	}

	// Verify agent details
	foundAgent1 := false
	foundAgent2 := false

	for _, ag := range agents {
		name := ag["name"].(string)
		if name == "Agent1" {
			foundAgent1 = true
			if ag["description"].(string) != "First agent" {
				t.Error("Agent1 description not correct")
			}
		} else if name == "Agent2" {
			foundAgent2 = true
			if ag["description"].(string) != "Second agent" {
				t.Error("Agent2 description not correct")
			}
		}
	}

	if !foundAgent1 || !foundAgent2 {
		t.Error("Not all expected agents found in result")
	}

	t.Logf("✓ Get available agents tool works")
	t.Logf("  Found %d agents", len(agents))
}

func TestTransferScopeValidatorTool(t *testing.T) {
	tool := ValidateTransferScopeTool()

	if tool.Name != "validate_transfer_scope" {
		t.Errorf("Expected tool name 'validate_transfer_scope', got '%s'", tool.Name)
	}

	// Create test hierarchy
	root := agent.NewAgent(agent.AgentConfig{
		Name:      "Root",
		Partition: "p0",
	})

	child := agent.NewAgent(agent.AgentConfig{
		Name:      "Child",
		Partition: "p1",
	})

	root.AddSubAgent(child)

	// Create invocation context
	ctx := context.Background()
	input := &agent.AgentInput{Instruction: "Test"}
	invocationCtx := agent.NewInvocationContext(ctx, input, root)

	// Test valid agent
	params := map[string]interface{}{
		"agent_name":             "Child",
		"__invocation_context__": invocationCtx,
		"__transfer_scope__":     "sub_agents",
	}

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("Tool execution failed: %v", err)
	}

	resultMap := result.(map[string]interface{})
	if !resultMap["valid"].(bool) {
		t.Error("Expected valid to be true for existing agent")
	}

	// Test invalid agent
	params["agent_name"] = "NonExistentAgent"
	result, err = tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("Tool execution failed: %v", err)
	}

	resultMap = result.(map[string]interface{})
	if resultMap["valid"].(bool) {
		t.Error("Expected valid to be false for non-existent agent")
	}

	t.Logf("✓ Transfer scope validator tool works")
}

func TestRegisterTransferTools(t *testing.T) {
	registry := NewToolRegistry()

	// Register all transfer tools
	err := RegisterTransferTools(registry)
	if err != nil {
		t.Fatalf("Failed to register transfer tools: %v", err)
	}

	// Verify all tools registered
	expectedTools := []string{
		"transfer_to_agent",
		"validate_transfer_scope",
		"get_available_agents",
	}

	for _, toolName := range expectedTools {
		tool, err := registry.Get(toolName)
		if err != nil {
			t.Errorf("Expected tool '%s' to be registered", toolName)
		}

		if tool.Name != toolName {
			t.Errorf("Tool name mismatch: expected '%s', got '%s'", toolName, tool.Name)
		}
	}

	t.Logf("✓ All transfer tools registered successfully")
	t.Logf("  Registered tools: %v", expectedTools)
}

// Integration Test: Complete Transfer Tool Workflow

func TestCompleteTransferToolWorkflow(t *testing.T) {
	// Create tool registry
	registry := NewToolRegistry()
	RegisterTransferTools(registry)

	// Create event bus for observability
	eventBus := events.NewMemoryEventBus()
	defer eventBus.Close()

	var transferEvents []events.TransferRequestData
	eventBus.Subscribe(events.EventTransferRequested, func(ctx context.Context, event events.Event) error {
		if data, ok := event.Data().(events.TransferRequestData); ok {
			transferEvents = append(transferEvents, data)
		}
		return nil
	})

	// Create agent hierarchy
	coordinator := agent.NewAgent(agent.AgentConfig{
		Name:        "Coordinator",
		Description: "Main coordinator",
		Partition:   "coordinator-partition",
	})

	bookingAgent := agent.NewAgent(agent.AgentConfig{
		Name:        "BookingAgent",
		Description: "Handles bookings",
		Partition:   "booking-partition",
		Capabilities: []string{"book_flight", "book_hotel"},
	})

	infoAgent := agent.NewAgent(agent.AgentConfig{
		Name:        "InfoAgent",
		Description: "Provides information",
		Partition:   "info-partition",
		Capabilities: []string{"search", "lookup"},
	})

	coordinator.AddSubAgent(bookingAgent)
	coordinator.AddSubAgent(infoAgent)

	// Create invocation context
	ctx := context.Background()
	input := &agent.AgentInput{Instruction: "Book a flight"}
	invocationCtx := agent.NewInvocationContext(ctx, input, coordinator)

	// Step 1: Get available agents
	getAgentsTool, _ := registry.Get("get_available_agents")
	availableAgentsResult, err := getAgentsTool.Handler(ctx, map[string]interface{}{
		"__invocation_context__": invocationCtx,
	})
	if err != nil {
		t.Fatalf("Get available agents failed: %v", err)
	}

	availableAgents := availableAgentsResult.(map[string]interface{})
	if availableAgents["count"].(int) != 2 {
		t.Errorf("Expected 2 available agents, got %d", availableAgents["count"])
	}

	// Step 2: Validate transfer scope
	validateTool, _ := registry.Get("validate_transfer_scope")
	validationResult, err := validateTool.Handler(ctx, map[string]interface{}{
		"agent_name":             "BookingAgent",
		"__invocation_context__": invocationCtx,
		"__transfer_scope__":     "sub_agents",
	})
	if err != nil {
		t.Fatalf("Validation failed: %v", err)
	}

	validation := validationResult.(map[string]interface{})
	if !validation["valid"].(bool) {
		t.Error("Expected BookingAgent to be valid for transfer")
	}

	// Step 3: Request transfer
	transferTool, _ := registry.Get("transfer_to_agent")
	transferResult, err := transferTool.Handler(ctx, map[string]interface{}{
		"agent_name":             "BookingAgent",
		"reason":                 "User wants to book a flight",
		"__invocation_context__": invocationCtx,
		"__event_bus__":          eventBus,
	})
	if err != nil {
		t.Fatalf("Transfer request failed: %v", err)
	}

	transfer := transferResult.(map[string]interface{})
	if !transfer["transfer_requested"].(bool) {
		t.Error("Expected transfer to be requested")
	}

	// Verify event was published
	if len(transferEvents) != 1 {
		t.Errorf("Expected 1 transfer event, got %d", len(transferEvents))
	}

	if len(transferEvents) > 0 {
		if transferEvents[0].ToAgent != "BookingAgent" {
			t.Errorf("Expected transfer to BookingAgent, got %s", transferEvents[0].ToAgent)
		}
	}

	t.Logf("✓ Complete transfer tool workflow successful")
	t.Logf("  Available agents: %d", availableAgents["count"])
	t.Logf("  Validation passed: %v", validation["valid"])
	t.Logf("  Transfer requested: %v", transfer["transfer_requested"])
	t.Logf("  Events published: %d", len(transferEvents))
}
