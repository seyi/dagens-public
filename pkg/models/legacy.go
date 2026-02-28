// Package models provides legacy model abstraction
// DEPRECATED: These types are maintained for backward compatibility with existing tests.
// New code should use the interfaces defined in interface.go
package models

import (
	"context"
	"fmt"
)

// DEPRECATED: Use the new ModelProvider interface from interface.go
// LegacyModelProvider is kept for backward compatibility
type LegacyModelProvider interface {
	Execute(ctx context.Context, request *LegacyModelRequest) (*LegacyModelResponse, error)
	Name() string
	Provider() string
	MaxTokens() int
	SupportsTools() bool
	SupportsStreaming() bool
}

// DEPRECATED: Use ModelRequest from interface.go
type LegacyModelRequest struct {
	Prompt       string
	Tools        []ToolDefinition
	Temperature  float64
	MaxTokens    int
	SystemPrompt string
	Context      map[string]interface{}
}

// DEPRECATED: Use ModelResponse from interface.go
type LegacyModelResponse struct {
	Content      string
	ToolCalls    []ToolCall
	TokensUsed   int
	FinishReason string
	Metadata     map[string]interface{}
}

// MockModelProvider for testing (legacy)
// DEPRECATED: Use real providers or create mocks using the new interface
type MockModelProvider struct {
	name          string
	provider      string
	maxTokens     int
	supportsTools bool
}

func NewMockModelProvider(name, provider string) *MockModelProvider {
	return &MockModelProvider{
		name:          name,
		provider:      provider,
		maxTokens:     4096,
		supportsTools: true,
	}
}

func (m *MockModelProvider) Name() string            { return m.name }
func (m *MockModelProvider) Provider() string        { return m.provider }
func (m *MockModelProvider) MaxTokens() int          { return m.maxTokens }
func (m *MockModelProvider) SupportsTools() bool     { return m.supportsTools }
func (m *MockModelProvider) SupportsStreaming() bool { return false }

func (m *MockModelProvider) Execute(ctx context.Context, request *LegacyModelRequest) (*LegacyModelResponse, error) {
	return &LegacyModelResponse{
		Content:      fmt.Sprintf("Mock response from %s to: %s", m.name, request.Prompt),
		ToolCalls:    []ToolCall{},
		TokensUsed:   100,
		FinishReason: "stop",
		Metadata:     map[string]interface{}{"model": m.name},
	}, nil
}
