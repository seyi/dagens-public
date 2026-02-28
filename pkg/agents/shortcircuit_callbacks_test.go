// Copyright 2025 Apache Spark AI Agents
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package agents

import (
	"context"
	"errors"
	"testing"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/model"
)

// ============================================================================
// CallbackResult Tests
// ============================================================================

func TestContinue(t *testing.T) {
	result := Continue()
	if result.ShouldStop {
		t.Error("Continue() should have ShouldStop=false")
	}
	if result.Content != nil {
		t.Error("Continue() should have nil Content")
	}
}

func TestShortCircuit(t *testing.T) {
	content := "cached response"
	result := ShortCircuit(content)

	if !result.ShouldStop {
		t.Error("ShortCircuit() should have ShouldStop=true")
	}
	if result.Content != content {
		t.Errorf("ShortCircuit() Content = %v, want %v", result.Content, content)
	}
}

func TestShortCircuitWithError(t *testing.T) {
	err := errors.New("rate limited")
	result := ShortCircuitWithError(err)

	if !result.ShouldStop {
		t.Error("ShortCircuitWithError() should have ShouldStop=true")
	}
	if result.Error != err {
		t.Errorf("ShortCircuitWithError() Error = %v, want %v", result.Error, err)
	}
}

// ============================================================================
// Short-Circuit Model Callback Tests
// ============================================================================

func TestExecuteBeforeModel_Continue(t *testing.T) {
	executor := NewShortCircuitExecutor(false)

	callbacks := []ShortCircuitModelCallback{
		func(ctx context.Context, input *model.ModelInput) *CallbackResult {
			return Continue()
		},
	}

	input := &model.ModelInput{Prompt: "test"}
	output, shouldShortCircuit, err := executor.ExecuteBeforeModel(context.Background(), callbacks, input)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if shouldShortCircuit {
		t.Error("Should not short-circuit when Continue() is returned")
	}
	if output != nil {
		t.Error("Output should be nil when not short-circuiting")
	}
}

func TestExecuteBeforeModel_ShortCircuit(t *testing.T) {
	executor := NewShortCircuitExecutor(false)

	cachedResponse := "cached: hello world"
	callbacks := []ShortCircuitModelCallback{
		func(ctx context.Context, input *model.ModelInput) *CallbackResult {
			return ShortCircuit(cachedResponse)
		},
	}

	input := &model.ModelInput{Prompt: "test"}
	output, shouldShortCircuit, err := executor.ExecuteBeforeModel(context.Background(), callbacks, input)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if !shouldShortCircuit {
		t.Error("Should short-circuit when ShortCircuit() is returned")
	}
	if output == nil {
		t.Fatal("Output should not be nil when short-circuiting")
	}
	if output.Text != cachedResponse {
		t.Errorf("Output.Text = %v, want %v", output.Text, cachedResponse)
	}
}

func TestExecuteBeforeModel_ShortCircuitWithError(t *testing.T) {
	executor := NewShortCircuitExecutor(true) // Stop on error

	expectedErr := errors.New("rate limit exceeded")
	callbacks := []ShortCircuitModelCallback{
		func(ctx context.Context, input *model.ModelInput) *CallbackResult {
			return ShortCircuitWithError(expectedErr)
		},
	}

	input := &model.ModelInput{Prompt: "test"}
	_, _, err := executor.ExecuteBeforeModel(context.Background(), callbacks, input)

	if err != expectedErr {
		t.Errorf("Error = %v, want %v", err, expectedErr)
	}
}

func TestExecuteBeforeModel_MultipleCallbacks(t *testing.T) {
	executor := NewShortCircuitExecutor(false)

	callCount := 0
	callbacks := []ShortCircuitModelCallback{
		func(ctx context.Context, input *model.ModelInput) *CallbackResult {
			callCount++
			return Continue() // First callback continues
		},
		func(ctx context.Context, input *model.ModelInput) *CallbackResult {
			callCount++
			return ShortCircuit("second callback response") // Second short-circuits
		},
		func(ctx context.Context, input *model.ModelInput) *CallbackResult {
			callCount++
			return Continue() // Should not be called
		},
	}

	input := &model.ModelInput{Prompt: "test"}
	output, shouldShortCircuit, _ := executor.ExecuteBeforeModel(context.Background(), callbacks, input)

	if callCount != 2 {
		t.Errorf("Expected 2 callbacks to be called, got %d", callCount)
	}
	if !shouldShortCircuit {
		t.Error("Should short-circuit")
	}
	if output.Text != "second callback response" {
		t.Errorf("Output.Text = %v, want 'second callback response'", output.Text)
	}
}

// ============================================================================
// Short-Circuit After Model Callback Tests
// ============================================================================

func TestExecuteAfterModel_ModifyResponse(t *testing.T) {
	executor := NewShortCircuitExecutor(false)

	callbacks := []ShortCircuitAfterModelCallback{
		func(ctx context.Context, input *model.ModelInput, output *model.ModelOutput) *CallbackResult {
			// Modify the response
			modified := &model.ModelOutput{
				Text:         output.Text + " [filtered]",
				FinishReason: output.FinishReason,
			}
			return ShortCircuit(modified)
		},
	}

	input := &model.ModelInput{Prompt: "test"}
	originalOutput := &model.ModelOutput{Text: "original response", FinishReason: "stop"}

	output, err := executor.ExecuteAfterModel(context.Background(), callbacks, input, originalOutput)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if output.Text != "original response [filtered]" {
		t.Errorf("Output.Text = %v, want 'original response [filtered]'", output.Text)
	}
}

// ============================================================================
// Short-Circuit Agent Callback Tests
// ============================================================================

func TestExecuteBeforeAgent_ShortCircuit(t *testing.T) {
	executor := NewShortCircuitExecutor(false)

	cachedResult := "cached agent result"
	callbacks := []ShortCircuitAgentCallback{
		func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) *CallbackResult {
			return ShortCircuit(cachedResult)
		},
	}

	input := &agent.AgentInput{Instruction: "test"}
	output, shouldShortCircuit, err := executor.ExecuteBeforeAgent(context.Background(), callbacks, nil, input)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if !shouldShortCircuit {
		t.Error("Should short-circuit")
	}
	if output == nil {
		t.Fatal("Output should not be nil")
	}
	if output.Result != cachedResult {
		t.Errorf("Output.Result = %v, want %v", output.Result, cachedResult)
	}
}

// ============================================================================
// Short-Circuit Tool Callback Tests
// ============================================================================

func TestExecuteBeforeTool_ShortCircuit(t *testing.T) {
	executor := NewShortCircuitExecutor(false)

	cachedToolResult := "cached tool result"
	callbacks := []ShortCircuitToolCallback{
		func(ctx context.Context, toolName string, args map[string]interface{}) *CallbackResult {
			if toolName == "cached_tool" {
				return ShortCircuit(cachedToolResult)
			}
			return Continue()
		},
	}

	args := map[string]interface{}{"input": "test"}

	// Test cached tool
	result, shouldShortCircuit, err := executor.ExecuteBeforeTool(context.Background(), callbacks, "cached_tool", args)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if !shouldShortCircuit {
		t.Error("Should short-circuit for cached_tool")
	}
	if result != cachedToolResult {
		t.Errorf("Result = %v, want %v", result, cachedToolResult)
	}

	// Test non-cached tool
	result, shouldShortCircuit, err = executor.ExecuteBeforeTool(context.Background(), callbacks, "other_tool", args)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if shouldShortCircuit {
		t.Error("Should not short-circuit for other_tool")
	}
}

// ============================================================================
// Built-in Callback Tests
// ============================================================================

func TestCacheCallback(t *testing.T) {
	cache := NewCacheShortCircuitCallback()

	// First call - should continue (not cached)
	input := &model.ModelInput{Prompt: "hello"}
	result := cache.BeforeModel()(context.Background(), input)
	if result.ShouldStop {
		t.Error("First call should not short-circuit")
	}

	// Simulate model response and cache it
	output := &model.ModelOutput{Text: "Hello! How can I help?"}
	cache.AfterModel()(context.Background(), input, output)

	// Second call - should short-circuit (cached)
	result = cache.BeforeModel()(context.Background(), input)
	if !result.ShouldStop {
		t.Error("Second call should short-circuit")
	}
	if cachedOutput, ok := result.Content.(*model.ModelOutput); ok {
		if cachedOutput.Text != output.Text {
			t.Errorf("Cached response = %v, want %v", cachedOutput.Text, output.Text)
		}
	} else {
		t.Error("Cached content should be *model.ModelOutput")
	}
}

func TestContentFilterCallback(t *testing.T) {
	filter := ContentFilterCallback(func(prompt string) (bool, string) {
		if prompt == "bad content" {
			return false, "inappropriate content"
		}
		return true, ""
	})

	// Good content - should continue
	input := &model.ModelInput{Prompt: "good content"}
	result := filter(context.Background(), input)
	if result.ShouldStop {
		t.Error("Good content should not be filtered")
	}

	// Bad content - should short-circuit with error
	input = &model.ModelInput{Prompt: "bad content"}
	result = filter(context.Background(), input)
	if !result.ShouldStop {
		t.Error("Bad content should be filtered")
	}
	if result.Error == nil {
		t.Error("Filtered content should have an error")
	}
}

func TestConditionalShortCircuit(t *testing.T) {
	callback := ConditionalShortCircuit(
		func(ctx context.Context, input *model.ModelInput) bool {
			return input.Prompt == "trigger"
		},
		func(ctx context.Context, input *model.ModelInput) interface{} {
			return "conditional response"
		},
	)

	// Non-trigger - should continue
	input := &model.ModelInput{Prompt: "normal"}
	result := callback(context.Background(), input)
	if result.ShouldStop {
		t.Error("Non-trigger should not short-circuit")
	}

	// Trigger - should short-circuit
	input = &model.ModelInput{Prompt: "trigger"}
	result = callback(context.Background(), input)
	if !result.ShouldStop {
		t.Error("Trigger should short-circuit")
	}
	if result.Content != "conditional response" {
		t.Errorf("Content = %v, want 'conditional response'", result.Content)
	}
}

// ============================================================================
// Content Conversion Tests
// ============================================================================

func TestContentToModelOutput(t *testing.T) {
	tests := []struct {
		name     string
		content  interface{}
		expected string
	}{
		{"string", "hello", "hello"},
		{"model output", &model.ModelOutput{Text: "direct"}, "direct"},
		{"map with text", map[string]interface{}{"text": "from map"}, "from map"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := contentToModelOutput(tt.content)
			if output.Text != tt.expected {
				t.Errorf("contentToModelOutput(%v).Text = %v, want %v", tt.content, output.Text, tt.expected)
			}
		})
	}
}

func TestContentToAgentOutput(t *testing.T) {
	tests := []struct {
		name     string
		content  interface{}
		expected interface{}
	}{
		{"string", "hello", "hello"},
		{"agent output", &agent.AgentOutput{Result: "direct"}, "direct"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := contentToAgentOutput(tt.content)
			if output.Result != tt.expected {
				t.Errorf("contentToAgentOutput(%v).Result = %v, want %v", tt.content, output.Result, tt.expected)
			}
		})
	}
}
