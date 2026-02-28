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
	"testing"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/model"
)

// ============================================================================
// Mock Model Provider for Testing (Enhanced LLM Agent)
// ============================================================================

type enhancedMockModelProvider struct {
	name      string
	responses []string
	callCount int
}

func newEnhancedMockModelProvider(responses ...string) *enhancedMockModelProvider {
	return &enhancedMockModelProvider{
		name:      "mock-model",
		responses: responses,
	}
}

func (m *enhancedMockModelProvider) Name() string {
	return m.name
}

func (m *enhancedMockModelProvider) Generate(ctx context.Context, input *model.ModelInput) (*model.ModelOutput, error) {
	response := "default response"
	if m.callCount < len(m.responses) {
		response = m.responses[m.callCount]
	}
	m.callCount++
	return &model.ModelOutput{
		Text:         response,
		TokensUsed:   10,
		FinishReason: "stop",
	}, nil
}

// ============================================================================
// InstructionProvider Tests
// ============================================================================

func TestEnhancedLlmAgent_StaticInstruction(t *testing.T) {
	modelProvider := newEnhancedMockModelProvider("Hello! I'm here to help.")

	agent := NewEnhancedLlmAgent(
		EnhancedLlmAgentConfig{
			Name:        "test-agent",
			Instruction: "You are a helpful assistant.",
		},
		modelProvider,
		nil,
	)

	if agent == nil {
		t.Fatal("Agent should not be nil")
	}
	if agent.instruction != "You are a helpful assistant." {
		t.Errorf("Instruction = %v, want 'You are a helpful assistant.'", agent.instruction)
	}
}

func TestEnhancedLlmAgent_InstructionProvider(t *testing.T) {
	modelProvider := newEnhancedMockModelProvider("Hello! I'm here to help.")

	// Dynamic instruction based on input
	instructionProvider := func(ctx context.Context, input *agent.AgentInput) (string, error) {
		if input.Context != nil {
			if lang, ok := input.Context["language"].(string); ok {
				return "Respond in " + lang, nil
			}
		}
		return "Respond in English", nil
	}

	enhancedAgent := NewEnhancedLlmAgent(
		EnhancedLlmAgentConfig{
			Name:                "test-agent",
			Instruction:         "Static instruction (should be ignored)",
			InstructionProvider: instructionProvider,
		},
		modelProvider,
		nil,
	)

	if enhancedAgent.instructionProvider == nil {
		t.Error("InstructionProvider should be set")
	}
}

func TestEnhancedLlmAgent_GlobalInstruction(t *testing.T) {
	modelProvider := newEnhancedMockModelProvider("Hello!")

	enhancedAgent := NewEnhancedLlmAgent(
		EnhancedLlmAgentConfig{
			Name:              "test-agent",
			Instruction:       "You are a helpful assistant.",
			GlobalInstruction: "Always be polite and professional.",
		},
		modelProvider,
		nil,
	)

	if enhancedAgent.globalInstruction != "Always be polite and professional." {
		t.Errorf("GlobalInstruction = %v, want 'Always be polite and professional.'", enhancedAgent.globalInstruction)
	}
}

func TestEnhancedLlmAgent_GlobalInstructionProvider(t *testing.T) {
	modelProvider := newEnhancedMockModelProvider("Hello!")

	globalProvider := func(ctx context.Context) (string, error) {
		return "Dynamic global instruction", nil
	}

	enhancedAgent := NewEnhancedLlmAgent(
		EnhancedLlmAgentConfig{
			Name:                      "test-agent",
			Instruction:               "You are a helpful assistant.",
			GlobalInstructionProvider: globalProvider,
		},
		modelProvider,
		nil,
	)

	if enhancedAgent.globalInstructionProvider == nil {
		t.Error("GlobalInstructionProvider should be set")
	}
}

// ============================================================================
// OutputKey Tests
// ============================================================================

func TestEnhancedLlmAgent_OutputKey(t *testing.T) {
	modelProvider := newEnhancedMockModelProvider("Result to store")

	enhancedAgent := NewEnhancedLlmAgent(
		EnhancedLlmAgentConfig{
			Name:        "test-agent",
			Instruction: "You are a helpful assistant.",
			OutputKey:   "agent_result",
		},
		modelProvider,
		nil,
	)

	if enhancedAgent.outputKey != "agent_result" {
		t.Errorf("OutputKey = %v, want 'agent_result'", enhancedAgent.outputKey)
	}
}

// ============================================================================
// Short-Circuit Callback Integration Tests
// ============================================================================

func TestEnhancedLlmAgent_ShortCircuitBeforeModel(t *testing.T) {
	modelProvider := newEnhancedMockModelProvider("Should not be called")

	cachedResponse := "Cached response from callback"
	shortCircuitCallback := func(ctx context.Context, input *model.ModelInput) *CallbackResult {
		return ShortCircuit(cachedResponse)
	}

	enhancedAgent := NewEnhancedLlmAgent(
		EnhancedLlmAgentConfig{
			Name:                    "test-agent",
			Instruction:             "You are a helpful assistant.",
			ShortCircuitBeforeModel: []ShortCircuitModelCallback{shortCircuitCallback},
		},
		modelProvider,
		nil,
	)

	// Execute the agent
	input := &agent.AgentInput{
		Instruction: "Hello",
		Context:     make(map[string]interface{}),
	}

	output, err := enhancedAgent.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if output.Result != cachedResponse {
		t.Errorf("Result = %v, want %v", output.Result, cachedResponse)
	}

	// Verify model was NOT called
	if modelProvider.callCount != 0 {
		t.Errorf("Model was called %d times, should be 0 (short-circuited)", modelProvider.callCount)
	}
}

func TestEnhancedLlmAgent_ShortCircuitAfterModel(t *testing.T) {
	modelProvider := newEnhancedMockModelProvider("Original response")

	modifyCallback := func(ctx context.Context, input *model.ModelInput, output *model.ModelOutput) *CallbackResult {
		modified := &model.ModelOutput{
			Text:         output.Text + " [modified by callback]",
			FinishReason: output.FinishReason,
		}
		return ShortCircuit(modified)
	}

	enhancedAgent := NewEnhancedLlmAgent(
		EnhancedLlmAgentConfig{
			Name:                   "test-agent",
			Instruction:            "You are a helpful assistant.",
			ShortCircuitAfterModel: []ShortCircuitAfterModelCallback{modifyCallback},
		},
		modelProvider,
		nil,
	)

	input := &agent.AgentInput{
		Instruction: "Hello",
		Context:     make(map[string]interface{}),
	}

	output, err := enhancedAgent.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	expected := "Original response [modified by callback]"
	if output.Result != expected {
		t.Errorf("Result = %v, want %v", output.Result, expected)
	}

	// Verify model WAS called (before after-model callback)
	if modelProvider.callCount != 1 {
		t.Errorf("Model was called %d times, should be 1", modelProvider.callCount)
	}
}

func TestEnhancedLlmAgent_ShortCircuitBeforeAgent(t *testing.T) {
	modelProvider := newEnhancedMockModelProvider("Should not be called")

	cachedAgentResult := "Cached agent result"
	shortCircuitCallback := func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) *CallbackResult {
		return ShortCircuit(cachedAgentResult)
	}

	enhancedAgent := NewEnhancedLlmAgent(
		EnhancedLlmAgentConfig{
			Name:                    "test-agent",
			Instruction:             "You are a helpful assistant.",
			ShortCircuitBeforeAgent: []ShortCircuitAgentCallback{shortCircuitCallback},
		},
		modelProvider,
		nil,
	)

	input := &agent.AgentInput{
		Instruction: "Hello",
		Context:     make(map[string]interface{}),
	}

	output, err := enhancedAgent.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if output.Result != cachedAgentResult {
		t.Errorf("Result = %v, want %v", output.Result, cachedAgentResult)
	}

	// Verify model was NOT called
	if modelProvider.callCount != 0 {
		t.Errorf("Model was called %d times, should be 0 (agent short-circuited)", modelProvider.callCount)
	}
}

// ============================================================================
// Builder Method Tests
// ============================================================================

func TestEnhancedLlmAgent_WithInstructionProvider(t *testing.T) {
	modelProvider := newEnhancedMockModelProvider("Hello!")

	enhancedAgent := NewEnhancedLlmAgent(
		EnhancedLlmAgentConfig{
			Name:        "test-agent",
			Instruction: "Original instruction",
		},
		modelProvider,
		nil,
	)

	newProvider := func(ctx context.Context, input *agent.AgentInput) (string, error) {
		return "Dynamic instruction", nil
	}

	newAgent := enhancedAgent.WithInstructionProvider(newProvider)

	if newAgent.instructionProvider == nil {
		t.Error("WithInstructionProvider should set the provider")
	}
	if enhancedAgent.instructionProvider != nil {
		t.Error("Original agent should not be modified")
	}
}

func TestEnhancedLlmAgent_WithGlobalInstruction(t *testing.T) {
	modelProvider := newEnhancedMockModelProvider("Hello!")

	enhancedAgent := NewEnhancedLlmAgent(
		EnhancedLlmAgentConfig{
			Name:        "test-agent",
			Instruction: "Agent instruction",
		},
		modelProvider,
		nil,
	)

	newAgent := enhancedAgent.WithGlobalInstruction("Global instruction")

	if newAgent.globalInstruction != "Global instruction" {
		t.Errorf("GlobalInstruction = %v, want 'Global instruction'", newAgent.globalInstruction)
	}
	if enhancedAgent.globalInstruction != "" {
		t.Error("Original agent should not be modified")
	}
}

func TestEnhancedLlmAgent_WithOutputKey(t *testing.T) {
	modelProvider := newEnhancedMockModelProvider("Hello!")

	enhancedAgent := NewEnhancedLlmAgent(
		EnhancedLlmAgentConfig{
			Name:        "test-agent",
			Instruction: "Agent instruction",
		},
		modelProvider,
		nil,
	)

	newAgent := enhancedAgent.WithOutputKey("result_key")

	if newAgent.outputKey != "result_key" {
		t.Errorf("OutputKey = %v, want 'result_key'", newAgent.outputKey)
	}
	if enhancedAgent.outputKey != "" {
		t.Error("Original agent should not be modified")
	}
}

func TestEnhancedLlmAgent_WithShortCircuitBeforeModel(t *testing.T) {
	modelProvider := newEnhancedMockModelProvider("Hello!")

	enhancedAgent := NewEnhancedLlmAgent(
		EnhancedLlmAgentConfig{
			Name:        "test-agent",
			Instruction: "Agent instruction",
		},
		modelProvider,
		nil,
	)

	callback := func(ctx context.Context, input *model.ModelInput) *CallbackResult {
		return Continue()
	}

	newAgent := enhancedAgent.WithShortCircuitBeforeModel(callback)

	if len(newAgent.shortCircuitBeforeModel) != 1 {
		t.Errorf("Should have 1 short-circuit callback, got %d", len(newAgent.shortCircuitBeforeModel))
	}
	if len(enhancedAgent.shortCircuitBeforeModel) != 0 {
		t.Error("Original agent should not be modified")
	}
}

func TestEnhancedLlmAgent_WithTools(t *testing.T) {
	modelProvider := newEnhancedMockModelProvider("Hello!")

	enhancedAgent := NewEnhancedLlmAgent(
		EnhancedLlmAgentConfig{
			Name:        "test-agent",
			Instruction: "Agent instruction",
			Tools:       []string{"tool1"},
		},
		modelProvider,
		nil,
	)

	newAgent := enhancedAgent.WithTools("tool2", "tool3")

	if len(newAgent.enabledTools) != 2 {
		t.Errorf("Should have 2 tools, got %d", len(newAgent.enabledTools))
	}
	if len(enhancedAgent.enabledTools) != 1 {
		t.Error("Original agent should not be modified")
	}
}

func TestEnhancedLlmAgent_WithTemperature(t *testing.T) {
	modelProvider := newEnhancedMockModelProvider("Hello!")

	enhancedAgent := NewEnhancedLlmAgent(
		EnhancedLlmAgentConfig{
			Name:        "test-agent",
			Instruction: "Agent instruction",
			Temperature: 0.7,
		},
		modelProvider,
		nil,
	)

	newAgent := enhancedAgent.WithTemperature(0.3)

	if newAgent.temperature != 0.3 {
		t.Errorf("Temperature = %v, want 0.3", newAgent.temperature)
	}
	if enhancedAgent.temperature != 0.7 {
		t.Error("Original agent should not be modified")
	}
}

func TestEnhancedLlmAgent_WithReAct(t *testing.T) {
	modelProvider := newEnhancedMockModelProvider("Hello!")

	enhancedAgent := NewEnhancedLlmAgent(
		EnhancedLlmAgentConfig{
			Name:        "test-agent",
			Instruction: "Agent instruction",
			UseReAct:    false,
		},
		modelProvider,
		nil,
	)

	newAgent := enhancedAgent.WithReAct(true)

	if !newAgent.useReAct {
		t.Error("UseReAct should be true")
	}
	if enhancedAgent.useReAct {
		t.Error("Original agent should not be modified")
	}
}

// ============================================================================
// Instruction Resolution Tests
// ============================================================================

func TestEnhancedLlmExecutor_ResolveInstruction_Static(t *testing.T) {
	modelProvider := newEnhancedMockModelProvider("Hello!")

	enhancedAgent := NewEnhancedLlmAgent(
		EnhancedLlmAgentConfig{
			Name:        "test-agent",
			Instruction: "Static instruction",
		},
		modelProvider,
		nil,
	)

	executor := &enhancedLlmExecutor{agent: enhancedAgent}
	input := &agent.AgentInput{Instruction: "test"}

	instruction, err := executor.resolveInstruction(context.Background(), input)
	if err != nil {
		t.Fatalf("resolveInstruction failed: %v", err)
	}

	if instruction != "Static instruction" {
		t.Errorf("Instruction = %v, want 'Static instruction'", instruction)
	}
}

func TestEnhancedLlmExecutor_ResolveInstruction_Provider(t *testing.T) {
	modelProvider := newEnhancedMockModelProvider("Hello!")

	provider := func(ctx context.Context, input *agent.AgentInput) (string, error) {
		return "Dynamic: " + input.Instruction, nil
	}

	enhancedAgent := NewEnhancedLlmAgent(
		EnhancedLlmAgentConfig{
			Name:                "test-agent",
			Instruction:         "Static (ignored)",
			InstructionProvider: provider,
		},
		modelProvider,
		nil,
	)

	executor := &enhancedLlmExecutor{agent: enhancedAgent}
	input := &agent.AgentInput{Instruction: "user input"}

	instruction, err := executor.resolveInstruction(context.Background(), input)
	if err != nil {
		t.Fatalf("resolveInstruction failed: %v", err)
	}

	if instruction != "Dynamic: user input" {
		t.Errorf("Instruction = %v, want 'Dynamic: user input'", instruction)
	}
}

func TestEnhancedLlmExecutor_ResolveInstruction_GlobalAndLocal(t *testing.T) {
	modelProvider := newEnhancedMockModelProvider("Hello!")

	enhancedAgent := NewEnhancedLlmAgent(
		EnhancedLlmAgentConfig{
			Name:              "test-agent",
			Instruction:       "Local instruction",
			GlobalInstruction: "Global instruction",
		},
		modelProvider,
		nil,
	)

	executor := &enhancedLlmExecutor{agent: enhancedAgent}
	input := &agent.AgentInput{Instruction: "test"}

	instruction, err := executor.resolveInstruction(context.Background(), input)
	if err != nil {
		t.Fatalf("resolveInstruction failed: %v", err)
	}

	expected := "Global instruction\n\nLocal instruction"
	if instruction != expected {
		t.Errorf("Instruction = %v, want %v", instruction, expected)
	}
}
