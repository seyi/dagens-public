package agents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/model"
	"github.com/seyi/dagens/pkg/tools"
	"github.com/seyi/dagens/pkg/types"
)

// TestAgentLevelCallbacks tests BeforeExecute, AfterExecute, and OnError callbacks
func TestAgentLevelCallbacks(t *testing.T) {
	callbackLog := []string{}
	var mu sync.Mutex

	logCallback := func(stage string) agent.AgentCallback {
		return func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) error {
			mu.Lock()
			defer mu.Unlock()
			callbackLog = append(callbackLog, fmt.Sprintf("%s: %s", stage, ag.Name()))
			return nil
		}
	}

	outputCallback := func(ctx context.Context, ag agent.Agent, input *agent.AgentInput, output *agent.AgentOutput) error {
		mu.Lock()
		defer mu.Unlock()
		callbackLog = append(callbackLog, fmt.Sprintf("after_execute: %s -> %v", ag.Name(), output.Result))
		return nil
	}

	// Create mock LLM agent
	mockModel := &MockModelProvider{
		response: "callback test response",
	}

	llmAgent := NewLlmAgent(LlmAgentConfig{
		Name:        "test-agent",
		ModelName:   "mock",
		Instruction: "Test instruction",
		BeforeExecute: []agent.AgentCallback{
			logCallback("before_execute"),
		},
		AfterExecute: []agent.AgentOutputCallback{
			outputCallback,
		},
	}, mockModel, nil)

	// Execute agent
	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Test task",
		Context:     make(map[string]interface{}),
	}

	output, err := llmAgent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if output.Result != "callback test response" {
		t.Errorf("Expected result 'callback test response', got %v", output.Result)
	}

	// Verify callbacks were called in correct order
	mu.Lock()
	defer mu.Unlock()

	if len(callbackLog) != 2 {
		t.Fatalf("Expected 2 callback calls, got %d: %v", len(callbackLog), callbackLog)
	}

	if callbackLog[0] != "before_execute: test-agent" {
		t.Errorf("Expected first callback to be 'before_execute: test-agent', got %s", callbackLog[0])
	}

	if !strings.Contains(callbackLog[1], "after_execute: test-agent") {
		t.Errorf("Expected second callback to contain 'after_execute: test-agent', got %s", callbackLog[1])
	}
}

// TestErrorCallback tests OnError callback execution
func TestErrorCallback(t *testing.T) {
	errorCaught := false
	var errorMessage string

	errorCallback := func(ctx context.Context, ag agent.Agent, input *agent.AgentInput, err error) error {
		errorCaught = true
		errorMessage = err.Error()
		return nil
	}

	// Create mock LLM agent that fails
	mockModel := &MockModelProvider{
		shouldError: true,
		errorMsg:    "mock LLM error",
	}

	llmAgent := NewLlmAgent(LlmAgentConfig{
		Name:        "error-agent",
		ModelName:   "mock",
		Instruction: "Test instruction",
		OnError: []agent.ErrorCallback{
			errorCallback,
		},
	}, mockModel, nil)

	// Execute agent (should fail)
	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Test task",
		Context:     make(map[string]interface{}),
	}

	_, err := llmAgent.Execute(ctx, input)
	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	if !errorCaught {
		t.Error("Error callback was not called")
	}

	if !strings.Contains(errorMessage, "mock LLM error") {
		t.Errorf("Expected error message to contain 'mock LLM error', got %s", errorMessage)
	}
}

// TestBeforeModelAfterModel tests LLM-specific model callbacks
func TestBeforeModelAfterModel(t *testing.T) {
	callbackLog := []string{}
	var mu sync.Mutex

	beforeModelCallback := func(ctx context.Context, input *model.ModelInput) error {
		mu.Lock()
		defer mu.Unlock()
		callbackLog = append(callbackLog, fmt.Sprintf("before_model: prompt_len=%d", len(input.Prompt)))
		return nil
	}

	afterModelCallback := func(ctx context.Context, input *model.ModelInput, output *model.ModelOutput) error {
		mu.Lock()
		defer mu.Unlock()
		callbackLog = append(callbackLog, fmt.Sprintf("after_model: tokens=%d", output.TokensUsed))
		return nil
	}

	mockModel := &MockModelProvider{
		response:   "model response",
		tokensUsed: 42,
	}

	llmAgent := NewLlmAgent(LlmAgentConfig{
		Name:        "model-callback-agent",
		ModelName:   "mock",
		Instruction: "Test instruction",
		BeforeModel: []ModelCallback{beforeModelCallback},
		AfterModel:  []ModelOutputCallback{afterModelCallback},
	}, mockModel, nil)

	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Test task",
		Context:     make(map[string]interface{}),
	}

	_, err := llmAgent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify model callbacks were called
	mu.Lock()
	defer mu.Unlock()

	if len(callbackLog) != 2 {
		t.Fatalf("Expected 2 model callbacks, got %d: %v", len(callbackLog), callbackLog)
	}

	if !strings.Contains(callbackLog[0], "before_model:") {
		t.Errorf("Expected before_model callback, got %s", callbackLog[0])
	}

	if callbackLog[1] != "after_model: tokens=42" {
		t.Errorf("Expected after_model with tokens=42, got %s", callbackLog[1])
	}
}

// TestBeforeToolAfterTool tests tool execution callbacks
func TestBeforeToolAfterTool(t *testing.T) {
	callbackLog := []string{}
	var mu sync.Mutex

	beforeToolCallback := func(ctx context.Context, toolName string, params map[string]interface{}) error {
		mu.Lock()
		defer mu.Unlock()
		callbackLog = append(callbackLog, fmt.Sprintf("before_tool: %s", toolName))
		return nil
	}

	afterToolCallback := func(ctx context.Context, toolName string, params map[string]interface{}, result interface{}) error {
		mu.Lock()
		defer mu.Unlock()
		callbackLog = append(callbackLog, fmt.Sprintf("after_tool: %s -> %v", toolName, result))
		return nil
	}

	// Create mock LLM that requests tool use
	mockModel := &MockModelProvider{
		response: `<tool_use name="calculator">{"operation": "add", "a": 2, "b": 3}</tool_use>`,
	}

	// Create tool registry with calculator tool
	registry := tools.NewToolRegistry()
	registry.Register(&types.ToolDefinition{
		Name:        "calculator",
		Description: "Performs calculations",
		Enabled:     true,
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			return "5", nil
		},
	})

	llmAgent := NewLlmAgent(LlmAgentConfig{
		Name:        "tool-callback-agent",
		ModelName:   "mock",
		Instruction: "Test instruction",
		Tools:       []string{"calculator"},
		BeforeTool:  []ToolCallback{beforeToolCallback},
		AfterTool:   []ToolResultCallback{afterToolCallback},
	}, mockModel, registry)

	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Calculate 2 + 3",
		Context:     make(map[string]interface{}),
	}

	_, err := llmAgent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify tool callbacks were called
	mu.Lock()
	defer mu.Unlock()

	if len(callbackLog) < 2 {
		t.Fatalf("Expected at least 2 tool callbacks, got %d: %v", len(callbackLog), callbackLog)
	}

	foundBefore := false
	foundAfter := false
	for _, log := range callbackLog {
		if strings.Contains(log, "before_tool: calculator") {
			foundBefore = true
		}
		if strings.Contains(log, "after_tool: calculator") {
			foundAfter = true
		}
	}

	if !foundBefore {
		t.Error("before_tool callback was not called")
	}
	if !foundAfter {
		t.Error("after_tool callback was not called")
	}
}

// TestReActCallbacks tests callbacks in ReAct mode
func TestReActCallbacks(t *testing.T) {
	modelCallCount := 0
	var mu sync.Mutex

	beforeModelCallback := func(ctx context.Context, input *model.ModelInput) error {
		mu.Lock()
		defer mu.Unlock()
		modelCallCount++
		return nil
	}

	// Mock model that returns final answer on second call
	callCount := 0
	mockModel := &MockModelProvider{
		responseFunc: func() string {
			callCount++
			if callCount == 1 {
				return "Thought: I need to calculate\nAction: calculator\nAction Input: {\"a\": 2, \"b\": 3}"
			}
			return "Thought: I have the answer\nAction: Final Answer\nAction Input: The result is 5"
		},
	}

	registry := tools.NewToolRegistry()
	registry.Register(&types.ToolDefinition{
		Name:    "calculator",
		Enabled: true,
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			return "5", nil
		},
	})

	llmAgent := NewLlmAgent(LlmAgentConfig{
		Name:         "react-callback-agent",
		ModelName:    "mock",
		Instruction:  "Calculate something",
		Tools:        []string{"calculator"},
		UseReAct:     true,
		MaxIterations: 5,
		BeforeModel:  []ModelCallback{beforeModelCallback},
	}, mockModel, registry)

	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Calculate 2 + 3",
		Context:     make(map[string]interface{}),
	}

	_, err := llmAgent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify model callback was called multiple times (once per ReAct iteration)
	mu.Lock()
	defer mu.Unlock()

	if modelCallCount < 2 {
		t.Errorf("Expected at least 2 model calls in ReAct, got %d", modelCallCount)
	}
}

// TestTokenCounterCallback tests the built-in token counter callback
func TestTokenCounterCallback(t *testing.T) {
	tokenCounter := NewTokenCounterCallback()

	mockModel := &MockModelProvider{
		response:   "response 1",
		tokensUsed: 100,
	}

	llmAgent := NewLlmAgent(LlmAgentConfig{
		Name:        "token-counter-agent",
		ModelName:   "mock",
		Instruction: "Test",
		AfterModel:  []ModelOutputCallback{tokenCounter.AfterModel()},
	}, mockModel, nil)

	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Test task",
		Context:     make(map[string]interface{}),
	}

	// Execute twice
	_, err := llmAgent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	mockModel.tokensUsed = 150
	_, err = llmAgent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify token count
	totalTokens := tokenCounter.GetTotal()
	if totalTokens != 250 {
		t.Errorf("Expected 250 total tokens, got %d", totalTokens)
	}

	// Test reset
	tokenCounter.Reset()
	if tokenCounter.GetTotal() != 0 {
		t.Errorf("Expected 0 tokens after reset, got %d", tokenCounter.GetTotal())
	}
}

// TestMetricsCollectorCallback tests the built-in metrics collector
func TestMetricsCollectorCallback(t *testing.T) {
	metricsCollector := NewMetricsCollectorCallback()

	mockModel := &MockModelProvider{
		response:   `<tool_use name="test_tool">{"arg": "value"}</tool_use>`,
		tokensUsed: 50,
	}

	registry := tools.NewToolRegistry()
	registry.Register(&types.ToolDefinition{
		Name:    "test_tool",
		Enabled: true,
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			return "tool result", nil
		},
	})

	llmAgent := NewLlmAgent(LlmAgentConfig{
		Name:        "metrics-agent",
		ModelName:   "mock",
		Instruction: "Test",
		Tools:       []string{"test_tool"},
		BeforeModel: []ModelCallback{metricsCollector.BeforeModel()},
		AfterModel:  []ModelOutputCallback{metricsCollector.AfterModel()},
		BeforeTool:  []ToolCallback{metricsCollector.BeforeTool()},
	}, mockModel, registry)

	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Test task",
		Context:     make(map[string]interface{}),
	}

	_, err := llmAgent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify metrics
	metrics := metricsCollector.GetMetrics()

	if modelCalls, ok := metrics["model_calls"].(int); !ok || modelCalls < 1 {
		t.Errorf("Expected at least 1 model call, got %v", metrics["model_calls"])
	}

	if toolCalls, ok := metrics["tool_calls"].(int); !ok || toolCalls < 1 {
		t.Errorf("Expected at least 1 tool call, got %v", metrics["tool_calls"])
	}

	if totalTokens, ok := metrics["total_tokens"].(int); !ok || totalTokens < 50 {
		t.Errorf("Expected at least 50 tokens, got %v", metrics["total_tokens"])
	}
}

// TestPromptModifierCallback tests prompt modification callback
func TestPromptModifierCallback(t *testing.T) {
	// Callback that adds a prefix to prompts
	modifier := PromptModifierCallback(func(prompt string) string {
		return "[SYSTEM] " + prompt
	})

	capturedPrompt := ""
	captureCallback := func(ctx context.Context, input *model.ModelInput) error {
		capturedPrompt = input.Prompt
		return nil
	}

	mockModel := &MockModelProvider{
		response: "modified response",
	}

	llmAgent := NewLlmAgent(LlmAgentConfig{
		Name:        "modifier-agent",
		ModelName:   "mock",
		Instruction: "Test",
		BeforeModel: []ModelCallback{modifier, captureCallback},
	}, mockModel, nil)

	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Hello",
		Context:     make(map[string]interface{}),
	}

	_, err := llmAgent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify prompt was modified
	if !strings.HasPrefix(capturedPrompt, "[SYSTEM]") {
		t.Errorf("Expected prompt to start with '[SYSTEM]', got: %s", capturedPrompt)
	}
}

// TestCallbackErrorHandling tests that callback errors don't stop execution
func TestCallbackErrorHandling(t *testing.T) {
	errorCallback := func(ctx context.Context, input *model.ModelInput) error {
		return errors.New("callback error")
	}

	mockModel := &MockModelProvider{
		response: "response despite callback error",
	}

	llmAgent := NewLlmAgent(LlmAgentConfig{
		Name:        "error-tolerant-agent",
		ModelName:   "mock",
		Instruction: "Test",
		BeforeModel: []ModelCallback{errorCallback},
	}, mockModel, nil)

	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Test task",
		Context:     make(map[string]interface{}),
	}

	// Should succeed despite callback error (ADK-compatible behavior)
	output, err := llmAgent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute failed: %v (callbacks should not stop execution)", err)
	}

	if output.Result != "response despite callback error" {
		t.Errorf("Expected result 'response despite callback error', got %v", output.Result)
	}
}

// TestTimingCallback tests the built-in timing callback
func TestTimingCallback(t *testing.T) {
	beforeTiming, afterTiming := agent.TimingCallback()

	mockModel := &MockModelProvider{
		response: "timed response",
		delay:    50 * time.Millisecond,
	}

	llmAgent := NewLlmAgent(LlmAgentConfig{
		Name:         "timing-agent",
		ModelName:    "mock",
		Instruction:  "Test",
		BeforeExecute: []agent.AgentCallback{beforeTiming},
		AfterExecute:  []agent.AgentOutputCallback{afterTiming},
	}, mockModel, nil)

	ctx := context.Background()
	input := &agent.AgentInput{
		Instruction: "Test task",
		Context:     make(map[string]interface{}),
	}

	output, err := llmAgent.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify timing metadata was added
	if timing, ok := output.Metadata["callback_timing"].(float64); !ok {
		t.Error("Expected callback_timing in metadata")
	} else if timing < 0.05 {
		t.Errorf("Expected timing >= 0.05s, got %f", timing)
	}
}

// MockModelProvider for testing
type MockModelProvider struct {
	response     string
	responseFunc func() string
	tokensUsed   int
	shouldError  bool
	errorMsg     string
	delay        time.Duration
}

func (m *MockModelProvider) Generate(ctx context.Context, input *model.ModelInput) (*model.ModelOutput, error) {
	if m.delay > 0 {
		time.Sleep(m.delay)
	}

	if m.shouldError {
		return nil, errors.New(m.errorMsg)
	}

	response := m.response
	if m.responseFunc != nil {
		response = m.responseFunc()
	}

	return &model.ModelOutput{
		Text:         response,
		TokensUsed:   m.tokensUsed,
		FinishReason: "complete",
	}, nil
}

func (m *MockModelProvider) Name() string {
	return "mock-model"
}
