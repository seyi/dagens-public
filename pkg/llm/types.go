// Package interaction provides types and interfaces for LLM chat protocol compatibility.
// It enables dagens agents to communicate using standard chat message formats while
// maintaining type-safe, structured data flow internally.
package interaction

// Role constants for standard chat roles (OpenAI/Anthropic compatible)
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message represents a standardized LLM chat message.
// Compatible with OpenAI, Anthropic, and other major LLM providers.
type Message struct {
	// Role identifies the message sender (system, user, assistant, tool)
	Role string `json:"role"`

	// Content is the text content of the message
	Content string `json:"content"`

	// Name is an optional identifier for the participant (used in multi-agent scenarios)
	Name string `json:"name,omitempty"`

	// ToolCalls contains function/tool invocation requests from the assistant
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`

	// ToolCallID links a tool response to its original request
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// ToolCall represents a request from the LLM to execute a tool/function.
type ToolCall struct {
	// ID uniquely identifies this tool call for response matching
	ID string `json:"id"`

	// Type is the kind of tool (typically "function")
	Type string `json:"type"`

	// Function contains the function call details
	Function Function `json:"function"`
}

// Function represents the function call details within a ToolCall.
type Function struct {
	// Name is the function identifier
	Name string `json:"name"`

	// Arguments is a JSON-encoded string of function arguments
	Arguments string `json:"arguments"`
}

// NewSystemMessage creates a system message with the given content.
func NewSystemMessage(content string) Message {
	return Message{Role: RoleSystem, Content: content}
}

// NewUserMessage creates a user message with the given content.
func NewUserMessage(content string) Message {
	return Message{Role: RoleUser, Content: content}
}

// NewAssistantMessage creates an assistant message with the given content.
func NewAssistantMessage(content string) Message {
	return Message{Role: RoleAssistant, Content: content}
}

// NewToolMessage creates a tool response message.
func NewToolMessage(toolCallID, content string) Message {
	return Message{
		Role:       RoleTool,
		Content:    content,
		ToolCallID: toolCallID,
	}
}

// NewAssistantToolCallMessage creates an assistant message with tool calls.
func NewAssistantToolCallMessage(toolCalls []ToolCall) Message {
	return Message{
		Role:      RoleAssistant,
		ToolCalls: toolCalls,
	}
}

// IsToolCall returns true if this message contains tool call requests.
func (m Message) IsToolCall() bool {
	return len(m.ToolCalls) > 0
}

// IsToolResponse returns true if this message is a tool response.
func (m Message) IsToolResponse() bool {
	return m.Role == RoleTool && m.ToolCallID != ""
}
