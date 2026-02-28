// Package model provides a simple model interface for LLM agents
package model

import "context"

// ModelProvider is the interface for model implementations
type ModelProvider interface {
	// Generate generates text based on input
	Generate(ctx context.Context, input *ModelInput) (*ModelOutput, error)

	// Name returns the model name
	Name() string
}

// ModelInput represents input to the model
type ModelInput struct {
	Prompt      string
	Temperature float64
	Context     map[string]interface{}
}

// ModelOutput represents output from the model
type ModelOutput struct {
	Text         string
	TokensUsed   int
	FinishReason string
}
