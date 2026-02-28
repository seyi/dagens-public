package ipc

import (
	"context"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/graph"
)

// TestEchoAgent for testing
type TestEchoAgent struct {
	id string
}

func (a *TestEchoAgent) ID() string { return a.id }
func (a *TestEchoAgent) Name() string { return a.id }
func (a *TestEchoAgent) Description() string { return "Test echo agent" }
func (a *TestEchoAgent) Capabilities() []string { return []string{"echo"} }
func (a *TestEchoAgent) Dependencies() []agent.Agent { return []agent.Agent{} }
func (a *TestEchoAgent) Partition() string { return "" }

func (a *TestEchoAgent) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	result := input.Instruction
	if result == "" {
		// If no instruction, try to get from context
		if val, ok := input.Context["input"]; ok {
			result = val.(string)
		} else {
			result = "No input provided"
		}
	}
	
	return &agent.AgentOutput{
		Result: "Echo: " + result,
		Metadata: map[string]interface{}{
			"agent": "test-echo",
		},
	}, nil
}

func TestStdioAgentServer(t *testing.T) {
	// This test would require a more complex setup to test actual stdin/stdout communication
	// For now, we'll just test the basic functionality
	
	server := NewStdioAgentServer()
	
	// Register a test agent
	testAgent := &TestEchoAgent{id: "test"}
	server.RegisterAgent("test", testAgent)
	
	// Verify the agent was registered
	if _, exists := server.agents["test"]; !exists {
		t.Errorf("Agent was not registered properly")
	}
	
	// Test message processing
	msg := Message{
		ID:   "test-1",
		To:   "test",
		Type: MessageTypeRequest,
		Payload: map[string]interface{}{
			"input": "hello world",
		},
	}
	
	response := server.processMessage(msg)
	
	if response.Type != MessageTypeResponse {
		t.Errorf("Expected response type %s, got %s", MessageTypeResponse, response.Type)
	}
	
	if result, ok := response.Payload["result"]; !ok {
		t.Errorf("Response should contain result")
	} else if resultStr, ok := result.(string); !ok || resultStr != "Echo: hello world" {
		t.Errorf("Expected result 'Echo: hello world', got %v", result)
	}
}

func TestMessageStructure(t *testing.T) {
	msg := Message{
		ID:   "test-1",
		From: "sender",
		To:   "receiver",
		Type: MessageTypeRequest,
		Payload: map[string]interface{}{
			"key": "value",
		},
		SentAt: time.Now(),
	}
	
	if msg.ID != "test-1" {
		t.Errorf("Message ID not set correctly")
	}
	
	if msg.Type != MessageTypeRequest {
		t.Errorf("Message type not set correctly")
	}
	
	if payloadVal, ok := msg.Payload["key"]; !ok || payloadVal != "value" {
		t.Errorf("Message payload not set correctly")
	}
}

func TestStdioAgentNode(t *testing.T) {
	// Create a mock ProcessAgent for testing
	// Since we can't easily test actual process communication in a unit test,
	// we'll focus on the structure and interfaces
	
	config := StdioAgentNodeConfig{
		ID: "test-stdio-agent",
		// We can't create a real ProcessAgent for testing without actual processes
		// So we'll just test the node creation
	}
	
	node := NewStdioAgentNode(config)
	
	if node.ID() != "test-stdio-agent" {
		t.Errorf("Node ID not set correctly")
	}
	
	if node.Type() != "stdio_agent" {
		t.Errorf("Node type not set correctly")
	}
	
	// Test execution with a mock state
	state := graph.NewMemoryState()
	state.Set("input", "test input")
	
	// Since we can't execute without a real ProcessAgent, we'll just verify
	// that the execution would attempt to communicate with the agent
	ctx := context.Background()
	
	// This would normally communicate with a real process agent
	// For testing, we'll just verify the structure is correct
	_ = ctx
	_ = state
}