package interaction

import (
	"testing"
)

func TestNewSystemMessage(t *testing.T) {
	msg := NewSystemMessage("You are a helpful assistant")

	if msg.Role != RoleSystem {
		t.Errorf("expected role %s, got %s", RoleSystem, msg.Role)
	}
	if msg.Content != "You are a helpful assistant" {
		t.Errorf("unexpected content: %s", msg.Content)
	}
}

func TestNewUserMessage(t *testing.T) {
	msg := NewUserMessage("Hello, world!")

	if msg.Role != RoleUser {
		t.Errorf("expected role %s, got %s", RoleUser, msg.Role)
	}
	if msg.Content != "Hello, world!" {
		t.Errorf("unexpected content: %s", msg.Content)
	}
}

func TestNewAssistantMessage(t *testing.T) {
	msg := NewAssistantMessage("Hello! How can I help?")

	if msg.Role != RoleAssistant {
		t.Errorf("expected role %s, got %s", RoleAssistant, msg.Role)
	}
	if msg.Content != "Hello! How can I help?" {
		t.Errorf("unexpected content: %s", msg.Content)
	}
}

func TestNewToolMessage(t *testing.T) {
	msg := NewToolMessage("call_123", `{"result": "success"}`)

	if msg.Role != RoleTool {
		t.Errorf("expected role %s, got %s", RoleTool, msg.Role)
	}
	if msg.ToolCallID != "call_123" {
		t.Errorf("expected tool_call_id 'call_123', got '%s'", msg.ToolCallID)
	}
	if msg.Content != `{"result": "success"}` {
		t.Errorf("unexpected content: %s", msg.Content)
	}
}

func TestNewAssistantToolCallMessage(t *testing.T) {
	toolCalls := []ToolCall{
		{
			ID:   "call_1",
			Type: "function",
			Function: Function{
				Name:      "get_weather",
				Arguments: `{"location": "London"}`,
			},
		},
	}

	msg := NewAssistantToolCallMessage(toolCalls)

	if msg.Role != RoleAssistant {
		t.Errorf("expected role %s, got %s", RoleAssistant, msg.Role)
	}
	if len(msg.ToolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("unexpected function name: %s", msg.ToolCalls[0].Function.Name)
	}
}

func TestMessage_IsToolCall(t *testing.T) {
	tests := []struct {
		name     string
		msg      Message
		expected bool
	}{
		{
			name:     "with tool calls",
			msg:      NewAssistantToolCallMessage([]ToolCall{{ID: "1"}}),
			expected: true,
		},
		{
			name:     "without tool calls",
			msg:      NewAssistantMessage("Hello"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.msg.IsToolCall(); got != tt.expected {
				t.Errorf("IsToolCall() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestMessage_IsToolResponse(t *testing.T) {
	tests := []struct {
		name     string
		msg      Message
		expected bool
	}{
		{
			name:     "tool response",
			msg:      NewToolMessage("call_123", "result"),
			expected: true,
		},
		{
			name:     "not tool response",
			msg:      NewUserMessage("Hello"),
			expected: false,
		},
		{
			name:     "tool role but no ID",
			msg:      Message{Role: RoleTool, Content: "result"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.msg.IsToolResponse(); got != tt.expected {
				t.Errorf("IsToolResponse() = %v, want %v", got, tt.expected)
			}
		})
	}
}
