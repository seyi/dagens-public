// Package models provides model abstraction for multi-model support
//
// This package has been refactored to support modern Chat Completion APIs.
// The new interface supports:
// - Message-based conversations (not just prompts)
// - Tool calling with structured definitions
// - Multi-modal content (text + images)
// - Streaming responses
// - Multiple provider backends (OpenAI, Anthropic, Google, Ollama, etc.)
//
// For the new interface, see interface.go
// For legacy compatibility, see legacy.go
package models

// Re-export commonly used types for convenience
type (
	// Modern interface (from interface.go)
	Provider       = ModelProvider
	Request        = ModelRequest
	Response       = ModelResponse
	Msg            = Message
	Role           = MessageRole
	Usage          = TokenUsage
	StreamCallback = StreamHandler
)

// Common message roles
const (
	System    = RoleSystem
	User      = RoleUser
	Assistant = RoleAssistant
	Tool      = RoleTool
)
