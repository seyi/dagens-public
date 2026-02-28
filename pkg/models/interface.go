// Package models provides a unified interface for multiple LLM providers.
// This interface supports modern Chat Completion APIs with messages, tools,
// and multi-modal content.
package models

import (
	"context"
	"time"
)

// MessageRole represents the role of a message participant
type MessageRole string

const (
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
)

// MessagePart represents a part of a multi-modal message (text, image, etc.)
type MessagePart struct {
	Type      string // "text", "image_url", "image_base64"
	Text      string
	ImageURL  string
	ImageData []byte
	MIMEType  string // MIME type for images (e.g., "image/png", "image/jpeg", "image/webp")
}

// ToolCall represents an assistant's request to call a tool
type ToolCall struct {
	ID        string
	Type      string // "function"
	Function  FunctionCall
}

// FunctionCall represents a function call with name and arguments
type FunctionCall struct {
	Name      string
	Arguments string // JSON string
}

// Message represents a single turn in a conversation
type Message struct {
	Role       MessageRole
	Content    string
	Name       string        // Optional: speaker/tool name
	ToolCalls  []ToolCall    // When assistant calls tools
	ToolCallID string        // When tool responds to a call
	Parts      []MessagePart // Multi-modal content (text + images)
}

// ResponseFormat specifies the desired output format
type ResponseFormat struct {
	Type string // "text" or "json_object"
}

// ModelRequest represents a request to any LLM provider
type ModelRequest struct {
	// Messages in the conversation
	Messages []Message

	// Tools available to the model
	Tools []ToolDefinition

	// Generation Parameters
	Temperature     float64
	TopP            float64
	MaxOutputTokens int
	StopSequences   []string
	ResponseFormat  ResponseFormat

	// Metadata for tracking and logging
	Metadata map[string]string

	// Provider-specific extensions (e.g., Claude "thinking", specialized params)
	ExtensionParams map[string]interface{}
}

// TokenUsage tracks token consumption and estimated cost
type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	EstimatedCostUSD float64
}

// ModelResponse represents a response from any LLM provider
type ModelResponse struct {
	Message      Message
	FinishReason string // "stop", "length", "tool_calls", "content_filter"
	Usage        TokenUsage
	ProviderID   string
	ModelName    string
	RequestID    string // Provider's request ID for debugging
	Latency      time.Duration
	Metadata     map[string]interface{}
}

// StreamChunk represents a chunk of streaming response
type StreamChunk struct {
	Delta        Message
	FinishReason string
	Usage        *TokenUsage
}

// StreamHandler is called for each chunk in a streaming response
type StreamHandler func(chunk *StreamChunk) error

// ProviderStatus represents the health status of a provider
type ProviderStatus struct {
	Available     bool
	LastChecked   time.Time
	ErrorCount    int
	LastError     error
	AverageLatency time.Duration
}

// ModelProvider is the unified interface for all LLM providers
type ModelProvider interface {
	// Identification
	ID() string   // Unique instance ID (e.g., "openai-gpt4-production")
	Type() string // Provider type (e.g., "openai", "anthropic", "google")
	Name() string // Model name (e.g., "gpt-4-turbo", "claude-3-opus")

	// Execution
	Chat(ctx context.Context, req *ModelRequest) (*ModelResponse, error)
	Stream(ctx context.Context, req *ModelRequest, handler StreamHandler) error

	// Capabilities
	SupportsTools() bool
	SupportsVision() bool
	SupportsStreaming() bool
	SupportsJSONMode() bool
	ContextWindow() int
	MaxOutputTokens() int

	// Health & Status
	HealthCheck(ctx context.Context) error
	GetStatus() ProviderStatus

	// Lifecycle
	Close() error
}

// ToolDefinition defines a tool/function available to the model
type ToolDefinition struct {
	Type     string              // "function"
	Function FunctionDefinition
}

// FunctionDefinition describes a function's schema
type FunctionDefinition struct {
	Name        string
	Description string
	Parameters  map[string]interface{} // JSON Schema
}
