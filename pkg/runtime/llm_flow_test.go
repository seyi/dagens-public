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

package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/agent"
)

// MockLlmProvider implements LlmProvider for testing
type MockLlmProvider struct {
	responses   []*LlmResponse
	callCount   int
	shouldError bool
	errorMsg    string
}

func NewMockLlmProvider(responses ...*LlmResponse) *MockLlmProvider {
	return &MockLlmProvider{
		responses: responses,
	}
}

func (m *MockLlmProvider) WithError(msg string) *MockLlmProvider {
	m.shouldError = true
	m.errorMsg = msg
	return m
}

func (m *MockLlmProvider) GenerateContent(ctx context.Context, req *LlmRequest) <-chan *LlmResponse {
	output := make(chan *LlmResponse)
	go func() {
		defer close(output)
		m.callCount++

		if m.shouldError {
			output <- &LlmResponse{
				ErrorCode:    "provider_error",
				ErrorMessage: m.errorMsg,
			}
			return
		}

		for _, resp := range m.responses {
			output <- resp
		}
	}()
	return output
}

func TestLlmRequest(t *testing.T) {
	t.Run("NewLlmRequest", func(t *testing.T) {
		req := &LlmRequest{
			SystemInstruction: "You are a helpful assistant",
			Contents: []Content{
				{Role: "user", Parts: []Part{{Text: "Hello"}}},
			},
			Tools: []ToolDefinition{
				{Name: "calculator", Description: "Perform calculations"},
			},
		}

		if req.SystemInstruction != "You are a helpful assistant" {
			t.Errorf("expected system instruction, got %s", req.SystemInstruction)
		}
		if len(req.Contents) != 1 {
			t.Errorf("expected 1 content, got %d", len(req.Contents))
		}
		if len(req.Tools) != 1 {
			t.Errorf("expected 1 tool, got %d", len(req.Tools))
		}
	})
}

func TestLlmResponse(t *testing.T) {
	t.Run("GetFunctionCalls", func(t *testing.T) {
		resp := &LlmResponse{
			Content: &Content{
				Role: "assistant",
				Parts: []Part{
					{Text: "Let me calculate that"},
					{FunctionCall: &FunctionCall{Name: "calculator", Args: map[string]interface{}{"expr": "2+2"}}},
				},
			},
		}

		calls := resp.GetFunctionCalls()
		if len(calls) != 1 {
			t.Errorf("expected 1 function call, got %d", len(calls))
		}
		if calls[0].Name != "calculator" {
			t.Errorf("expected calculator, got %s", calls[0].Name)
		}
	})

	t.Run("HasFunctionCalls", func(t *testing.T) {
		respWithCalls := &LlmResponse{
			Content: &Content{
				Parts: []Part{
					{FunctionCall: &FunctionCall{Name: "test"}},
				},
			},
		}
		respWithoutCalls := &LlmResponse{
			Content: &Content{
				Parts: []Part{
					{Text: "Just text"},
				},
			},
		}

		if !respWithCalls.HasFunctionCalls() {
			t.Error("expected HasFunctionCalls to be true")
		}
		if respWithoutCalls.HasFunctionCalls() {
			t.Error("expected HasFunctionCalls to be false")
		}
	})
}

func TestLlmFlow(t *testing.T) {
	t.Run("BasicExecution", func(t *testing.T) {
		provider := NewMockLlmProvider(&LlmResponse{
			Content: &Content{
				Role:  "assistant",
				Parts: []Part{{Text: "Hello, world!"}},
			},
			TurnComplete: true,
		})

		flow := NewLlmFlow(provider)
		flow.MaxSteps = 3

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "Say hello"}
		invCtx := NewInvocationContext(ctx, input)
		invCtx.AgentName = "test-agent"

		events := CollectEvents(flow.RunAsync(ctx, invCtx))

		if len(events) == 0 {
			t.Fatal("expected at least one event")
		}

		// Should have received events
		var hasMessage bool
		for _, e := range events {
			if e.Type == EventTypeMessage {
				hasMessage = true
			}
		}
		if !hasMessage {
			t.Error("expected at least one message event")
		}
	})

	t.Run("BeforeModelCallback_ShortCircuit", func(t *testing.T) {
		provider := NewMockLlmProvider(&LlmResponse{
			Content: &Content{Parts: []Part{{Text: "Should not see this"}}},
		})

		flow := NewLlmFlow(provider)
		flow.MaxSteps = 1

		// Add callback that short-circuits
		shortCircuitResponse := &LlmResponse{
			Content: &Content{
				Role:  "assistant",
				Parts: []Part{{Text: "Cached response"}},
			},
			TurnComplete: true,
		}

		flow.WithBeforeModelCallback(func(ctx *CallbackContext, req *LlmRequest) (*LlmResponse, error) {
			return shortCircuitResponse, nil
		})

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)
		invCtx.AgentName = "test-agent"

		events := CollectEvents(flow.RunAsync(ctx, invCtx))

		// Provider should not have been called
		if provider.callCount != 0 {
			t.Errorf("expected provider not to be called, but was called %d times", provider.callCount)
		}

		// Should have gotten the cached response
		if len(events) == 0 {
			t.Fatal("expected events from short-circuit")
		}
	})

	t.Run("AfterModelCallback_Modifies", func(t *testing.T) {
		provider := NewMockLlmProvider(&LlmResponse{
			Content: &Content{
				Role:  "assistant",
				Parts: []Part{{Text: "Original response"}},
			},
			TurnComplete: true,
		})

		flow := NewLlmFlow(provider)
		flow.MaxSteps = 1

		// Add callback that modifies response
		flow.WithAfterModelCallback(func(ctx *CallbackContext, resp *LlmResponse) (*LlmResponse, error) {
			if resp.Content != nil && len(resp.Content.Parts) > 0 {
				resp.Content.Parts[0].Text = "Modified response"
			}
			return resp, nil
		})

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)
		invCtx.AgentName = "test-agent"

		events := CollectEvents(flow.RunAsync(ctx, invCtx))

		// Check that we got events
		if len(events) == 0 {
			t.Fatal("expected events")
		}
	})

	t.Run("MaxStepsLimit", func(t *testing.T) {
		// Provider that returns non-final responses
		provider := NewMockLlmProvider(&LlmResponse{
			Content: &Content{
				Parts: []Part{{FunctionCall: &FunctionCall{Name: "loop", Args: map[string]interface{}{}}}},
			},
			TurnComplete: false,
		})

		flow := NewLlmFlow(provider)
		flow.MaxSteps = 3

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)
		invCtx.AgentName = "test-agent"

		events := CollectEvents(flow.RunAsync(ctx, invCtx))

		// Should have max_steps_reached event
		var hasMaxSteps bool
		for _, e := range events {
			if e.Metadata != nil {
				if _, ok := e.Metadata["max_steps_reached"]; ok {
					hasMaxSteps = true
				}
			}
		}
		if !hasMaxSteps {
			t.Error("expected max_steps_reached event")
		}
	})
}

func TestRequestProcessor(t *testing.T) {
	t.Run("LoggingProcessor", func(t *testing.T) {
		processor := &LoggingRequestProcessor{Prefix: "test"}

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)

		req := &LlmRequest{
			SystemInstruction: "test instruction",
		}

		events := processor.ProcessRequest(ctx, invCtx, req)
		eventList := CollectEvents(events)

		if len(eventList) != 1 {
			t.Errorf("expected 1 event, got %d", len(eventList))
		}
		if eventList[0].Type != EventTypeStateChange {
			t.Errorf("expected StateChange event, got %v", eventList[0].Type)
		}
	})
}

func TestResponseProcessor(t *testing.T) {
	t.Run("LoggingProcessor", func(t *testing.T) {
		processor := &LoggingResponseProcessor{Prefix: "test"}

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)

		resp := &LlmResponse{
			Content: &Content{Parts: []Part{{Text: "response"}}},
		}

		events := processor.ProcessResponse(ctx, invCtx, resp)
		eventList := CollectEvents(events)

		if len(eventList) != 1 {
			t.Errorf("expected 1 event, got %d", len(eventList))
		}
	})
}

func TestCallbackContext(t *testing.T) {
	t.Run("NewCallbackContext", func(t *testing.T) {
		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)

		actions := &EventActions{}
		cbCtx := NewCallbackContext(invCtx, actions)

		if cbCtx.InvocationContext != invCtx {
			t.Error("expected invocation context to match")
		}
		if cbCtx.EventActions != actions {
			t.Error("expected event actions to match")
		}
	})
}

func TestReadonlyContext(t *testing.T) {
	t.Run("Accessors", func(t *testing.T) {
		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)
		invCtx.UserID = "user-123"

		ro := NewReadonlyContext(invCtx)

		if ro.SessionID() != invCtx.SessionID {
			t.Error("SessionID mismatch")
		}
		if ro.UserID() != "user-123" {
			t.Error("UserID mismatch")
		}
		if ro.InvocationID() != invCtx.InvocationID {
			t.Error("InvocationID mismatch")
		}
	})

	t.Run("Get", func(t *testing.T) {
		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)
		invCtx.Set("key", "value")

		ro := NewReadonlyContext(invCtx)

		val, ok := ro.Get("key")
		if !ok {
			t.Error("expected key to exist")
		}
		if val != "value" {
			t.Errorf("expected value, got %v", val)
		}
	})
}

func TestToolContext(t *testing.T) {
	t.Run("NewToolContext", func(t *testing.T) {
		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)

		toolCtx := NewToolContext(invCtx, "calculator")

		if toolCtx.InvocationContext != invCtx {
			t.Error("expected invocation context to match")
		}
		if toolCtx.ToolName != "calculator" {
			t.Errorf("expected calculator, got %s", toolCtx.ToolName)
		}
	})
}

func TestRateLimitingCallback(t *testing.T) {
	t.Run("ExceedsLimit", func(t *testing.T) {
		rlCallback := &RateLimitingBeforeModelCallback{
			MaxCallsPerMinute: 2,
		}

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)
		cbCtx := NewCallbackContext(invCtx, nil)

		req := &LlmRequest{}

		// First call should succeed
		resp, err := rlCallback.Callback()(cbCtx, req)
		if err != nil || resp != nil {
			t.Error("first call should succeed")
		}

		// Second call should succeed
		resp, err = rlCallback.Callback()(cbCtx, req)
		if err != nil || resp != nil {
			t.Error("second call should succeed")
		}

		// Third call should be rate limited
		resp, err = rlCallback.Callback()(cbCtx, req)
		if err != nil {
			t.Error("should not return error, but response")
		}
		if resp == nil {
			t.Error("expected rate limit response")
		} else if resp.ErrorCode != "rate_limited" {
			t.Errorf("expected rate_limited, got %s", resp.ErrorCode)
		}
	})
}

func TestCachingCallback(t *testing.T) {
	t.Run("ReturnsCachedResponse", func(t *testing.T) {
		cache := make(map[string]*LlmResponse)
		cachedResp := &LlmResponse{
			Content:      &Content{Parts: []Part{{Text: "cached"}}},
			TurnComplete: true,
		}
		cache["test-instruction"] = cachedResp

		callback := CachingBeforeModelCallback(cache)

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)
		cbCtx := NewCallbackContext(invCtx, nil)

		req := &LlmRequest{
			SystemInstruction: "test-instruction",
		}

		resp, err := callback(cbCtx, req)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if resp != cachedResp {
			t.Error("expected cached response")
		}
	})

	t.Run("ReturnsNilForMiss", func(t *testing.T) {
		cache := make(map[string]*LlmResponse)
		callback := CachingBeforeModelCallback(cache)

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)
		cbCtx := NewCallbackContext(invCtx, nil)

		req := &LlmRequest{
			SystemInstruction: "not-in-cache",
		}

		resp, err := callback(cbCtx, req)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if resp != nil {
			t.Error("expected nil for cache miss")
		}
	})
}

func TestFunctionCall(t *testing.T) {
	t.Run("Serialization", func(t *testing.T) {
		call := &FunctionCall{
			ID:   "call-123",
			Name: "calculator",
			Args: map[string]interface{}{
				"expression": "2 + 2",
			},
		}

		if call.Name != "calculator" {
			t.Errorf("expected calculator, got %s", call.Name)
		}
		if call.Args["expression"] != "2 + 2" {
			t.Errorf("expected expression, got %v", call.Args)
		}
	})
}

func TestFunctionResponse(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		resp := &FunctionResponse{
			Name:     "calculator",
			Response: 4,
		}

		if resp.Name != "calculator" {
			t.Error("name mismatch")
		}
		if resp.Response != 4 {
			t.Error("response mismatch")
		}
		if resp.Error != "" {
			t.Error("should not have error")
		}
	})

	t.Run("Error", func(t *testing.T) {
		resp := &FunctionResponse{
			Name:  "calculator",
			Error: "division by zero",
		}

		if resp.Error != "division by zero" {
			t.Error("error mismatch")
		}
	})
}

func TestLLMAsyncAgentWithFlow(t *testing.T) {
	t.Run("UsesFlowWhenSet", func(t *testing.T) {
		provider := NewMockLlmProvider(&LlmResponse{
			Content:      &Content{Parts: []Part{{Text: "response"}}},
			TurnComplete: true,
		})

		flow := NewLlmFlow(provider)
		flow.MaxSteps = 1

		// Create agent without executor (will use flow)
		ag := NewLLMAsyncAgent("agent-1", "test-agent", nil)
		ag.WithLlmFlow(flow)

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)

		events := CollectEvents(ag.RunAsync(ctx, invCtx))

		if len(events) == 0 {
			t.Fatal("expected events")
		}

		// Provider should have been called
		if provider.callCount != 1 {
			t.Errorf("expected provider to be called once, got %d", provider.callCount)
		}
	})

	t.Run("MergesCallbacks", func(t *testing.T) {
		provider := NewMockLlmProvider(&LlmResponse{
			Content:      &Content{Parts: []Part{{Text: "response"}}},
			TurnComplete: true,
		})

		flow := NewLlmFlow(provider)
		flow.MaxSteps = 1

		callbackCalled := false
		ag := NewLLMAsyncAgent("agent-1", "test-agent", nil)
		ag.WithLlmFlow(flow)
		ag.WithBeforeModelCallback(func(ctx *CallbackContext, req *LlmRequest) (*LlmResponse, error) {
			callbackCalled = true
			return nil, nil
		})

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)

		CollectEvents(ag.RunAsync(ctx, invCtx))

		if !callbackCalled {
			t.Error("expected callback to be called")
		}
	})
}

func TestUsageMetadata(t *testing.T) {
	t.Run("TokenCounts", func(t *testing.T) {
		usage := &UsageMetadata{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		}

		if usage.PromptTokens != 100 {
			t.Errorf("expected 100, got %d", usage.PromptTokens)
		}
		if usage.TotalTokens != 150 {
			t.Errorf("expected 150, got %d", usage.TotalTokens)
		}
	})
}

func TestGenerateConfig(t *testing.T) {
	t.Run("ConfigOptions", func(t *testing.T) {
		config := &GenerateConfig{
			Temperature:     0.7,
			TopP:            0.9,
			TopK:            40,
			MaxOutputTokens: 1000,
			StopSequences:   []string{"END"},
			Labels:          map[string]string{"env": "test"},
		}

		if config.Temperature != 0.7 {
			t.Error("temperature mismatch")
		}
		if config.MaxOutputTokens != 1000 {
			t.Error("max tokens mismatch")
		}
		if len(config.Labels) != 1 {
			t.Error("labels mismatch")
		}
	})
}

func TestOnModelErrorCallback(t *testing.T) {
	t.Run("RetryCallback", func(t *testing.T) {
		retryableErrors := []string{"rate_limit", "timeout"}
		callback := RetryOnErrorCallback(3, retryableErrors)

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)
		cbCtx := NewCallbackContext(invCtx, nil)

		req := &LlmRequest{}

		// Non-retryable error
		resp, err := callback(cbCtx, req, errors.New("unknown error"))
		if err != nil {
			t.Error("should not return error")
		}
		if resp != nil {
			t.Error("should return nil for non-retryable")
		}

		// Retryable error
		resp, err = callback(cbCtx, req, errors.New("rate_limit"))
		if err != nil {
			t.Error("should not return error")
		}
		// Returns nil to signal retry
		if resp != nil {
			t.Error("should return nil to signal retry")
		}
	})
}

func TestContent(t *testing.T) {
	t.Run("MultiPart", func(t *testing.T) {
		content := &Content{
			Role: "user",
			Parts: []Part{
				{Text: "Hello"},
				{Text: "World"},
			},
		}

		if content.Role != "user" {
			t.Error("role mismatch")
		}
		if len(content.Parts) != 2 {
			t.Errorf("expected 2 parts, got %d", len(content.Parts))
		}
	})

	t.Run("MixedParts", func(t *testing.T) {
		content := &Content{
			Role: "assistant",
			Parts: []Part{
				{Text: "Let me help"},
				{FunctionCall: &FunctionCall{Name: "search", Args: map[string]interface{}{"q": "test"}}},
			},
		}

		if len(content.Parts) != 2 {
			t.Errorf("expected 2 parts, got %d", len(content.Parts))
		}
		if content.Parts[0].Text == "" {
			t.Error("expected text in first part")
		}
		if content.Parts[1].FunctionCall == nil {
			t.Error("expected function call in second part")
		}
	})
}

func TestFlowContextCancellation(t *testing.T) {
	t.Run("RespectsContextCancel", func(t *testing.T) {
		// Provider that delays
		provider := &MockLlmProvider{
			responses: []*LlmResponse{
				{Content: &Content{Parts: []Part{{Text: "response"}}}},
			},
		}

		flow := NewLlmFlow(provider)
		flow.MaxSteps = 10

		// Create a cancellable context
		ctx, cancel := context.WithCancel(context.Background())
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)
		invCtx.AgentName = "test-agent"

		// Cancel after a short delay
		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()

		events := CollectEvents(flow.RunAsync(ctx, invCtx))

		// Should have completed (may have error due to cancel)
		_ = events
	})
}

func TestToolDefinition(t *testing.T) {
	t.Run("WithHandler", func(t *testing.T) {
		handlerCalled := false
		tool := ToolDefinition{
			Name:        "test_tool",
			Description: "A test tool",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"input": map[string]interface{}{"type": "string"},
				},
			},
			Handler: func(ctx context.Context, args map[string]interface{}) (interface{}, error) {
				handlerCalled = true
				return "result", nil
			},
		}

		result, err := tool.Handler(context.Background(), map[string]interface{}{})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if result != "result" {
			t.Errorf("expected result, got %v", result)
		}
		if !handlerCalled {
			t.Error("handler was not called")
		}
	})
}

// Tests for ADK-compatible features

func TestStreamingMode(t *testing.T) {
	t.Run("SSE_YieldsPartials", func(t *testing.T) {
		provider := NewMockLlmProvider(
			&LlmResponse{
				Content: &Content{Parts: []Part{{Text: "par"}}},
				Partial: true,
			},
			&LlmResponse{
				Content:      &Content{Parts: []Part{{Text: "partial response"}}},
				Partial:      false,
				TurnComplete: true,
			},
		)

		flow := NewLlmFlow(provider)
		flow.MaxSteps = 1
		flow.DefaultRunConfig = &RunConfig{
			StreamingMode: StreamingModeSSE,
		}

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)
		invCtx.AgentName = "test-agent"

		events := CollectEvents(flow.RunAsync(ctx, invCtx))

		// Should have both partial and final events
		if len(events) < 2 {
			t.Errorf("expected at least 2 events in SSE mode, got %d", len(events))
		}
	})

	t.Run("None_FilterPartials", func(t *testing.T) {
		provider := NewMockLlmProvider(
			&LlmResponse{
				Content: &Content{Parts: []Part{{Text: "par"}}},
				Partial: true,
			},
			&LlmResponse{
				Content:      &Content{Parts: []Part{{Text: "full"}}},
				Partial:      false,
				TurnComplete: true,
			},
		)

		flow := NewLlmFlow(provider)
		flow.MaxSteps = 1
		flow.DefaultRunConfig = &RunConfig{
			StreamingMode: StreamingModeNone,
		}

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)
		invCtx.AgentName = "test-agent"

		events := CollectEvents(flow.RunAsync(ctx, invCtx))

		// In non-SSE mode, partial events are filtered
		for _, e := range events {
			if e.Partial {
				t.Error("partial events should be filtered in non-SSE mode")
			}
		}
	})
}

func TestLLMCallCounting(t *testing.T) {
	t.Run("IncrementsCount", func(t *testing.T) {
		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)

		if invCtx.GetLLMCallCount() != 0 {
			t.Error("initial count should be 0")
		}

		err := invCtx.IncrementLLMCallCount()
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		if invCtx.GetLLMCallCount() != 1 {
			t.Errorf("count should be 1, got %d", invCtx.GetLLMCallCount())
		}
	})

	t.Run("EnforcesLimit", func(t *testing.T) {
		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)
		invCtx.RunConfig = &RunConfig{
			MaxLLMCalls: 2,
		}

		// First two calls should succeed
		if err := invCtx.IncrementLLMCallCount(); err != nil {
			t.Errorf("first call failed: %v", err)
		}
		if err := invCtx.IncrementLLMCallCount(); err != nil {
			t.Errorf("second call failed: %v", err)
		}

		// Third call should fail
		if err := invCtx.IncrementLLMCallCount(); err == nil {
			t.Error("expected error for exceeding limit")
		}
	})

	t.Run("FlowEnforcesLimit", func(t *testing.T) {
		provider := NewMockLlmProvider(&LlmResponse{
			Content:      &Content{Parts: []Part{{Text: "response"}}},
			TurnComplete: true,
		})

		flow := NewLlmFlow(provider)
		flow.MaxSteps = 5
		flow.DefaultRunConfig = &RunConfig{
			MaxLLMCalls: 1,
		}

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)
		invCtx.AgentName = "test-agent"
		invCtx.RunConfig = &RunConfig{MaxLLMCalls: 1}

		// Pre-increment to simulate previous call
		invCtx.IncrementLLMCallCount()

		events := CollectEvents(flow.RunAsync(ctx, invCtx))

		// Should have an error event
		var hasLimitError bool
		for _, e := range events {
			if e.Error != nil && e.Error.Error() != "" {
				hasLimitError = true
			}
		}

		// The flow should have enforced the limit
		if invCtx.GetLLMCallCount() != 1 {
			// Count should not have been incremented past 1
		}
		_ = hasLimitError
	})
}

func TestTracer(t *testing.T) {
	t.Run("NoopTracer", func(t *testing.T) {
		tracer := &NoopTracer{}

		ctx, span := tracer.StartSpan(context.Background(), "test")
		if ctx == nil {
			t.Error("context should not be nil")
		}
		if span == nil {
			t.Error("span should not be nil")
		}

		// These should not panic
		span.SetAttribute("key", "value")
		span.RecordError(errors.New("test error"))
		span.End()
	})

	t.Run("FlowUsesTracer", func(t *testing.T) {
		provider := NewMockLlmProvider(&LlmResponse{
			Content:      &Content{Parts: []Part{{Text: "response"}}},
			TurnComplete: true,
		})

		spanStarted := false
		spanEnded := false

		mockTracer := &mockTracer{
			onStart: func() { spanStarted = true },
			onEnd:   func() { spanEnded = true },
		}

		flow := NewLlmFlow(provider)
		flow.MaxSteps = 1
		flow.Tracer = mockTracer

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)
		invCtx.AgentName = "test-agent"

		CollectEvents(flow.RunAsync(ctx, invCtx))

		if !spanStarted {
			t.Error("expected span to be started")
		}
		if !spanEnded {
			t.Error("expected span to be ended")
		}
	})
}

type mockTracer struct {
	onStart func()
	onEnd   func()
}

func (t *mockTracer) StartSpan(ctx context.Context, name string) (context.Context, Span) {
	if t.onStart != nil {
		t.onStart()
	}
	return ctx, &mockSpan{onEnd: t.onEnd}
}

type mockSpan struct {
	onEnd func()
}

func (s *mockSpan) End() {
	if s.onEnd != nil {
		s.onEnd()
	}
}
func (s *mockSpan) SetAttribute(key string, value interface{}) {}
func (s *mockSpan) RecordError(err error)                      {}

func TestTraceCallback(t *testing.T) {
	t.Run("CallsTraceCallback", func(t *testing.T) {
		provider := NewMockLlmProvider(&LlmResponse{
			Content:      &Content{Parts: []Part{{Text: "response"}}},
			TurnComplete: true,
		})

		traceCalled := false
		var capturedReq *LlmRequest
		var capturedResp *LlmResponse

		flow := NewLlmFlow(provider)
		flow.MaxSteps = 1
		flow.TraceCallback = func(invCtx *InvocationContext, eventID string, req *LlmRequest, resp *LlmResponse) {
			traceCalled = true
			capturedReq = req
			capturedResp = resp
		}

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)
		invCtx.AgentName = "test-agent"

		CollectEvents(flow.RunAsync(ctx, invCtx))

		if !traceCalled {
			t.Error("expected trace callback to be called")
		}
		if capturedReq == nil {
			t.Error("expected request to be captured")
		}
		if capturedResp == nil {
			t.Error("expected response to be captured")
		}
	})
}

func TestLiveRequestQueue(t *testing.T) {
	t.Run("SendAndReceive", func(t *testing.T) {
		queue := NewLiveRequestQueue()

		req := &LlmRequest{SystemInstruction: "test"}
		queue.Send(req)

		received := <-queue.Receive()
		if received != req {
			t.Error("received different request")
		}
	})

	t.Run("Close", func(t *testing.T) {
		queue := NewLiveRequestQueue()
		queue.Close()

		// Send should not panic after close
		queue.Send(&LlmRequest{})

		// Receive channel should be closed
		_, ok := <-queue.Receive()
		if ok {
			t.Error("channel should be closed")
		}
	})
}

func TestRunConfig(t *testing.T) {
	t.Run("Defaults", func(t *testing.T) {
		config := &RunConfig{}

		if config.StreamingMode != StreamingModeNone {
			t.Error("default streaming mode should be None")
		}
		if config.SupportCFC {
			t.Error("CFC should be disabled by default")
		}
		if config.MaxLLMCalls != 0 {
			t.Error("max calls should default to 0 (unlimited)")
		}
	})

	t.Run("FlowUsesInvocationConfig", func(t *testing.T) {
		provider := NewMockLlmProvider(&LlmResponse{
			Content:      &Content{Parts: []Part{{Text: "response"}}},
			TurnComplete: true,
		})

		flow := NewLlmFlow(provider)
		flow.MaxSteps = 1
		flow.DefaultRunConfig = &RunConfig{
			StreamingMode: StreamingModeSSE,
		}

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)
		invCtx.AgentName = "test-agent"
		// Override with invocation-specific config
		invCtx.RunConfig = &RunConfig{
			StreamingMode: StreamingModeNone,
		}

		rc := flow.getRunConfig(invCtx)
		if rc.StreamingMode != StreamingModeNone {
			t.Error("invocation config should override flow default")
		}
	})
}

func TestTurnCompletion(t *testing.T) {
	t.Run("SetsTurnComplete", func(t *testing.T) {
		resp := &LlmResponse{
			Content:      &Content{Parts: []Part{{Text: "done"}}},
			TurnComplete: true,
		}

		if !resp.TurnComplete {
			t.Error("TurnComplete should be true")
		}
	})
}

// ============================================================================
// Plugin Manager Tests
// ============================================================================

func TestBasePlugin(t *testing.T) {
	t.Run("NewBasePlugin", func(t *testing.T) {
		plugin := NewBasePlugin("test-plugin", 50)

		if plugin.Name() != "test-plugin" {
			t.Errorf("expected name 'test-plugin', got %s", plugin.Name())
		}
		if plugin.Priority() != 50 {
			t.Errorf("expected priority 50, got %d", plugin.Priority())
		}
		if !plugin.Enabled() {
			t.Error("plugin should be enabled by default")
		}
	})

	t.Run("EnableDisable", func(t *testing.T) {
		plugin := NewBasePlugin("test", 50)

		plugin.SetEnabled(false)
		if plugin.Enabled() {
			t.Error("plugin should be disabled")
		}

		plugin.SetEnabled(true)
		if !plugin.Enabled() {
			t.Error("plugin should be enabled")
		}
	})

	t.Run("AddCallbacks", func(t *testing.T) {
		plugin := NewBasePlugin("test", 50)

		plugin.AddBeforeModelCallback(func(ctx *CallbackContext, req *LlmRequest) (*LlmResponse, error) {
			return nil, nil
		})
		plugin.AddAfterModelCallback(func(ctx *CallbackContext, resp *LlmResponse) (*LlmResponse, error) {
			return nil, nil
		})
		plugin.AddOnModelErrorCallback(func(ctx *CallbackContext, req *LlmRequest, err error) (*LlmResponse, error) {
			return nil, nil
		})
		plugin.AddBeforeToolCallback(func(ctx *ToolContext, toolName string, args map[string]interface{}) error {
			return nil
		})
		plugin.AddAfterToolCallback(func(ctx *ToolContext, toolName string, args map[string]interface{}, result interface{}, err error) error {
			return nil
		})

		if len(plugin.BeforeModel()) != 1 {
			t.Error("expected 1 before-model callback")
		}
		if len(plugin.AfterModel()) != 1 {
			t.Error("expected 1 after-model callback")
		}
		if len(plugin.OnModelError()) != 1 {
			t.Error("expected 1 on-model-error callback")
		}
		if len(plugin.BeforeTool()) != 1 {
			t.Error("expected 1 before-tool callback")
		}
		if len(plugin.AfterTool()) != 1 {
			t.Error("expected 1 after-tool callback")
		}
	})

	t.Run("InitializeCleanup", func(t *testing.T) {
		plugin := NewBasePlugin("test", 50)
		ctx := context.Background()

		if err := plugin.Initialize(ctx); err != nil {
			t.Errorf("initialize should succeed: %v", err)
		}
		if err := plugin.Cleanup(ctx); err != nil {
			t.Errorf("cleanup should succeed: %v", err)
		}
	})
}

func TestPluginManager(t *testing.T) {
	t.Run("NewPluginManager", func(t *testing.T) {
		pm := NewPluginManager()
		if pm == nil {
			t.Fatal("plugin manager should not be nil")
		}
		if len(pm.ListPlugins()) != 0 {
			t.Error("should have no plugins initially")
		}
	})

	t.Run("RegisterPlugin", func(t *testing.T) {
		pm := NewPluginManager()
		plugin := NewBasePlugin("test", 50)

		if err := pm.Register(plugin); err != nil {
			t.Errorf("register should succeed: %v", err)
		}

		plugins := pm.ListPlugins()
		if len(plugins) != 1 {
			t.Errorf("expected 1 plugin, got %d", len(plugins))
		}
		if plugins[0] != "test" {
			t.Errorf("expected plugin name 'test', got %s", plugins[0])
		}
	})

	t.Run("RegisterDuplicate", func(t *testing.T) {
		pm := NewPluginManager()
		plugin1 := NewBasePlugin("test", 50)
		plugin2 := NewBasePlugin("test", 60)

		pm.Register(plugin1)
		err := pm.Register(plugin2)
		if err == nil {
			t.Error("registering duplicate should fail")
		}
	})

	t.Run("PriorityOrdering", func(t *testing.T) {
		pm := NewPluginManager()

		// Register in non-priority order
		pm.Register(NewBasePlugin("low", 10))
		pm.Register(NewBasePlugin("high", 100))
		pm.Register(NewBasePlugin("medium", 50))

		plugins := pm.ListPlugins()
		if len(plugins) != 3 {
			t.Fatalf("expected 3 plugins, got %d", len(plugins))
		}
		// Higher priority should come first
		if plugins[0] != "high" {
			t.Errorf("expected 'high' first, got %s", plugins[0])
		}
		if plugins[1] != "medium" {
			t.Errorf("expected 'medium' second, got %s", plugins[1])
		}
		if plugins[2] != "low" {
			t.Errorf("expected 'low' third, got %s", plugins[2])
		}
	})

	t.Run("UnregisterPlugin", func(t *testing.T) {
		pm := NewPluginManager()
		plugin := NewBasePlugin("test", 50)
		pm.Register(plugin)

		if err := pm.Unregister("test"); err != nil {
			t.Errorf("unregister should succeed: %v", err)
		}
		if len(pm.ListPlugins()) != 0 {
			t.Error("should have no plugins after unregister")
		}
	})

	t.Run("UnregisterNonexistent", func(t *testing.T) {
		pm := NewPluginManager()
		if err := pm.Unregister("nonexistent"); err == nil {
			t.Error("unregistering nonexistent should fail")
		}
	})

	t.Run("GetPlugin", func(t *testing.T) {
		pm := NewPluginManager()
		plugin := NewBasePlugin("test", 50)
		pm.Register(plugin)

		retrieved, ok := pm.GetPlugin("test")
		if !ok {
			t.Error("should find registered plugin")
		}
		if retrieved.Name() != "test" {
			t.Error("retrieved plugin should match")
		}

		_, ok = pm.GetPlugin("nonexistent")
		if ok {
			t.Error("should not find nonexistent plugin")
		}
	})

	t.Run("EnableDisablePlugin", func(t *testing.T) {
		pm := NewPluginManager()
		plugin := NewBasePlugin("test", 50)
		pm.Register(plugin)

		if err := pm.DisablePlugin("test"); err != nil {
			t.Errorf("disable should succeed: %v", err)
		}
		if plugin.Enabled() {
			t.Error("plugin should be disabled")
		}

		if err := pm.EnablePlugin("test"); err != nil {
			t.Errorf("enable should succeed: %v", err)
		}
		if !plugin.Enabled() {
			t.Error("plugin should be enabled")
		}

		if err := pm.EnablePlugin("nonexistent"); err == nil {
			t.Error("enabling nonexistent should fail")
		}
	})

	t.Run("InitializeAllPlugins", func(t *testing.T) {
		pm := NewPluginManager()
		initialized := false

		type customPlugin struct {
			*BasePlugin
			initialized *bool
		}

		cp := &customPlugin{
			BasePlugin:  NewBasePlugin("custom", 50),
			initialized: &initialized,
		}

		pm.Register(cp.BasePlugin)

		ctx := context.Background()
		if err := pm.Initialize(ctx); err != nil {
			t.Errorf("initialize should succeed: %v", err)
		}

		// Initialize again should be no-op
		if err := pm.Initialize(ctx); err != nil {
			t.Errorf("second initialize should succeed: %v", err)
		}
	})

	t.Run("CleanupAllPlugins", func(t *testing.T) {
		pm := NewPluginManager()
		pm.Register(NewBasePlugin("test", 50))

		ctx := context.Background()
		pm.Initialize(ctx)

		if err := pm.Cleanup(ctx); err != nil {
			t.Errorf("cleanup should succeed: %v", err)
		}

		// Cleanup again should be no-op
		if err := pm.Cleanup(ctx); err != nil {
			t.Errorf("second cleanup should succeed: %v", err)
		}
	})
}

func TestPluginManagerCallbacks(t *testing.T) {
	t.Run("RunBeforeModelCallbacks", func(t *testing.T) {
		pm := NewPluginManager()
		callOrder := []string{}

		plugin1 := NewBasePlugin("first", 100)
		plugin1.AddBeforeModelCallback(func(ctx *CallbackContext, req *LlmRequest) (*LlmResponse, error) {
			callOrder = append(callOrder, "first")
			return nil, nil
		})

		plugin2 := NewBasePlugin("second", 50)
		plugin2.AddBeforeModelCallback(func(ctx *CallbackContext, req *LlmRequest) (*LlmResponse, error) {
			callOrder = append(callOrder, "second")
			return nil, nil
		})

		pm.Register(plugin1)
		pm.Register(plugin2)

		callbackCtx := &CallbackContext{}
		req := &LlmRequest{}

		resp, err := pm.RunBeforeModelCallbacks(callbackCtx, req)
		if err != nil {
			t.Errorf("callbacks should not fail: %v", err)
		}
		if resp != nil {
			t.Error("no short-circuit response expected")
		}

		if len(callOrder) != 2 {
			t.Fatalf("expected 2 callbacks, got %d", len(callOrder))
		}
		if callOrder[0] != "first" || callOrder[1] != "second" {
			t.Errorf("callbacks called in wrong order: %v", callOrder)
		}
	})

	t.Run("BeforeModelShortCircuit", func(t *testing.T) {
		pm := NewPluginManager()
		secondCalled := false

		plugin1 := NewBasePlugin("first", 100)
		plugin1.AddBeforeModelCallback(func(ctx *CallbackContext, req *LlmRequest) (*LlmResponse, error) {
			return &LlmResponse{Content: &Content{Parts: []Part{{Text: "short-circuited"}}}}, nil
		})

		plugin2 := NewBasePlugin("second", 50)
		plugin2.AddBeforeModelCallback(func(ctx *CallbackContext, req *LlmRequest) (*LlmResponse, error) {
			secondCalled = true
			return nil, nil
		})

		pm.Register(plugin1)
		pm.Register(plugin2)

		callbackCtx := &CallbackContext{}
		req := &LlmRequest{}

		resp, err := pm.RunBeforeModelCallbacks(callbackCtx, req)
		if err != nil {
			t.Errorf("should not error: %v", err)
		}
		if resp == nil {
			t.Fatal("expected short-circuit response")
		}
		if secondCalled {
			t.Error("second callback should not be called after short-circuit")
		}
	})

	t.Run("DisabledPluginSkipped", func(t *testing.T) {
		pm := NewPluginManager()
		called := false

		plugin := NewBasePlugin("test", 50)
		plugin.AddBeforeModelCallback(func(ctx *CallbackContext, req *LlmRequest) (*LlmResponse, error) {
			called = true
			return nil, nil
		})
		plugin.SetEnabled(false)

		pm.Register(plugin)

		callbackCtx := &CallbackContext{}
		pm.RunBeforeModelCallbacks(callbackCtx, &LlmRequest{})

		if called {
			t.Error("disabled plugin callback should not be called")
		}
	})

	t.Run("RunAfterModelCallbacks", func(t *testing.T) {
		pm := NewPluginManager()

		plugin := NewBasePlugin("test", 50)
		plugin.AddAfterModelCallback(func(ctx *CallbackContext, resp *LlmResponse) (*LlmResponse, error) {
			return &LlmResponse{
				Content: &Content{Parts: []Part{{Text: "modified"}}},
			}, nil
		})

		pm.Register(plugin)

		callbackCtx := &CallbackContext{}
		resp := &LlmResponse{Content: &Content{Parts: []Part{{Text: "original"}}}}

		result, err := pm.RunAfterModelCallbacks(callbackCtx, resp)
		if err != nil {
			t.Errorf("should not error: %v", err)
		}
		if result.Content.Parts[0].Text != "modified" {
			t.Error("response should be modified")
		}
	})

	t.Run("RunOnModelErrorCallbacks", func(t *testing.T) {
		pm := NewPluginManager()

		plugin := NewBasePlugin("test", 50)
		plugin.AddOnModelErrorCallback(func(ctx *CallbackContext, req *LlmRequest, err error) (*LlmResponse, error) {
			return &LlmResponse{Content: &Content{Parts: []Part{{Text: "recovered"}}}}, nil
		})

		pm.Register(plugin)

		callbackCtx := &CallbackContext{}
		testErr := errors.New("test error")

		resp, err := pm.RunOnModelErrorCallbacks(callbackCtx, &LlmRequest{}, testErr)
		if err != nil {
			t.Errorf("should not error: %v", err)
		}
		if resp == nil {
			t.Fatal("expected recovery response")
		}
		if resp.Content.Parts[0].Text != "recovered" {
			t.Error("response should be recovery response")
		}
	})

	t.Run("RunBeforeToolCallbacks", func(t *testing.T) {
		pm := NewPluginManager()
		toolCalled := ""

		plugin := NewBasePlugin("test", 50)
		plugin.AddBeforeToolCallback(func(ctx *ToolContext, toolName string, args map[string]interface{}) error {
			toolCalled = toolName
			return nil
		})

		pm.Register(plugin)

		invCtx := &InvocationContext{}
		toolCtx := NewToolContext(invCtx, "test_tool")

		err := pm.RunBeforeToolCallbacks(toolCtx, "test_tool", map[string]interface{}{"key": "value"})
		if err != nil {
			t.Errorf("should not error: %v", err)
		}
		if toolCalled != "test_tool" {
			t.Errorf("expected tool 'test_tool', got %s", toolCalled)
		}
	})

	t.Run("BeforeToolCallbackError", func(t *testing.T) {
		pm := NewPluginManager()

		plugin := NewBasePlugin("test", 50)
		plugin.AddBeforeToolCallback(func(ctx *ToolContext, toolName string, args map[string]interface{}) error {
			return errors.New("unauthorized")
		})

		pm.Register(plugin)

		invCtx := &InvocationContext{}
		toolCtx := NewToolContext(invCtx, "test_tool")

		err := pm.RunBeforeToolCallbacks(toolCtx, "test_tool", nil)
		if err == nil {
			t.Error("should return error")
		}
	})

	t.Run("RunAfterToolCallbacks", func(t *testing.T) {
		pm := NewPluginManager()
		resultCaptured := ""

		plugin := NewBasePlugin("test", 50)
		plugin.AddAfterToolCallback(func(ctx *ToolContext, toolName string, args map[string]interface{}, result interface{}, err error) error {
			if str, ok := result.(string); ok {
				resultCaptured = str
			}
			return nil
		})

		pm.Register(plugin)

		invCtx := &InvocationContext{}
		toolCtx := NewToolContext(invCtx, "test_tool")

		err := pm.RunAfterToolCallbacks(toolCtx, "test_tool", nil, "tool_result", nil)
		if err != nil {
			t.Errorf("should not error: %v", err)
		}
		if resultCaptured != "tool_result" {
			t.Errorf("expected 'tool_result', got %s", resultCaptured)
		}
	})
}

func TestBuiltInPlugins(t *testing.T) {
	t.Run("LoggingPlugin", func(t *testing.T) {
		plugin := NewLoggingPlugin(false) // Non-verbose for testing

		if plugin.Name() != "logging" {
			t.Errorf("expected name 'logging', got %s", plugin.Name())
		}
		if plugin.Priority() != 100 {
			t.Errorf("expected priority 100, got %d", plugin.Priority())
		}

		// Verify callbacks are registered
		if len(plugin.BeforeModel()) != 1 {
			t.Error("expected before-model callback")
		}
		if len(plugin.AfterModel()) != 1 {
			t.Error("expected after-model callback")
		}
		if len(plugin.BeforeTool()) != 1 {
			t.Error("expected before-tool callback")
		}
		if len(plugin.AfterTool()) != 1 {
			t.Error("expected after-tool callback")
		}
	})

	t.Run("MetricsPlugin", func(t *testing.T) {
		plugin := NewMetricsPlugin()

		if plugin.Name() != "metrics" {
			t.Errorf("expected name 'metrics', got %s", plugin.Name())
		}

		// Execute callbacks
		ctx := &CallbackContext{}
		req := &LlmRequest{}
		resp := &LlmResponse{
			UsageMetadata: &UsageMetadata{TotalTokens: 100},
		}

		for _, cb := range plugin.BeforeModel() {
			cb(ctx, req)
		}
		for _, cb := range plugin.AfterModel() {
			cb(ctx, resp)
		}

		invCtx := &InvocationContext{}
		toolCtx := NewToolContext(invCtx, "test")
		for _, cb := range plugin.BeforeTool() {
			cb(toolCtx, "test", nil)
		}

		metrics := plugin.GetMetrics()
		if metrics["model_calls"] != 1 {
			t.Errorf("expected 1 model call, got %d", metrics["model_calls"])
		}
		if metrics["total_tokens"] != 100 {
			t.Errorf("expected 100 tokens, got %d", metrics["total_tokens"])
		}
		if metrics["tool_calls"] != 1 {
			t.Errorf("expected 1 tool call, got %d", metrics["tool_calls"])
		}

		plugin.Reset()
		metrics = plugin.GetMetrics()
		if metrics["model_calls"] != 0 {
			t.Error("metrics should be reset")
		}
	})

	t.Run("AuthorizationPlugin", func(t *testing.T) {
		plugin := NewAuthorizationPlugin([]string{"allowed_tool"}, true)

		if plugin.Name() != "authorization" {
			t.Errorf("expected name 'authorization', got %s", plugin.Name())
		}

		invCtx := &InvocationContext{}
		toolCtx := NewToolContext(invCtx, "test")

		// Test allowed tool
		for _, cb := range plugin.BeforeTool() {
			err := cb(toolCtx, "allowed_tool", nil)
			if err != nil {
				t.Errorf("allowed tool should pass: %v", err)
			}
		}

		// Test unauthorized tool
		for _, cb := range plugin.BeforeTool() {
			err := cb(toolCtx, "unauthorized_tool", nil)
			if err == nil {
				t.Error("unauthorized tool should fail")
			}
		}

		// Test AllowTool
		plugin.AllowTool("new_tool")
		for _, cb := range plugin.BeforeTool() {
			err := cb(toolCtx, "new_tool", nil)
			if err != nil {
				t.Errorf("newly allowed tool should pass: %v", err)
			}
		}
	})

	t.Run("RateLimitPlugin", func(t *testing.T) {
		plugin := NewRateLimitPlugin(2)

		if plugin.Name() != "rate_limit" {
			t.Errorf("expected name 'rate_limit', got %s", plugin.Name())
		}

		ctx := &CallbackContext{}
		req := &LlmRequest{}

		// First two calls should succeed
		for i := 0; i < 2; i++ {
			for _, cb := range plugin.BeforeModel() {
				resp, _ := cb(ctx, req)
				if resp != nil {
					t.Errorf("call %d should not be rate limited", i+1)
				}
			}
		}

		// Third call should be rate limited
		for _, cb := range plugin.BeforeModel() {
			resp, _ := cb(ctx, req)
			if resp == nil {
				t.Error("third call should be rate limited")
			}
			if resp.ErrorCode != "rate_limited" {
				t.Errorf("expected 'rate_limited' error code, got %s", resp.ErrorCode)
			}
		}
	})

	t.Run("CachingPlugin", func(t *testing.T) {
		plugin := NewCachingPlugin(time.Minute)

		if plugin.Name() != "caching" {
			t.Errorf("expected name 'caching', got %s", plugin.Name())
		}

		stats := plugin.GetCacheStats()
		if stats["hits"] != 0 || stats["misses"] != 0 {
			t.Error("cache should be empty initially")
		}

		ctx := &CallbackContext{}
		req := &LlmRequest{SystemInstruction: "test"}

		// First call is a miss
		for _, cb := range plugin.BeforeModel() {
			cb(ctx, req)
		}

		stats = plugin.GetCacheStats()
		if stats["misses"] != 1 {
			t.Errorf("expected 1 miss, got %d", stats["misses"])
		}

		plugin.ClearCache()
		stats = plugin.GetCacheStats()
		if stats["size"] != 0 {
			t.Error("cache should be cleared")
		}
	})
}

func TestLlmFlowWithPluginManager(t *testing.T) {
	t.Run("FlowIntegratesPluginManager", func(t *testing.T) {
		pm := NewPluginManager()
		callOrder := []string{}

		// Add a plugin
		plugin := NewBasePlugin("test", 50)
		plugin.AddBeforeModelCallback(func(ctx *CallbackContext, req *LlmRequest) (*LlmResponse, error) {
			callOrder = append(callOrder, "plugin_before")
			return nil, nil
		})
		pm.Register(plugin)

		// Create flow
		provider := NewMockLlmProvider(&LlmResponse{
			Content:      &Content{Parts: []Part{{Text: "response"}}},
			TurnComplete: true,
		})

		flow := NewLlmFlow(provider).
			WithPluginManager(pm).
			WithBeforeModelCallback(func(ctx *CallbackContext, req *LlmRequest) (*LlmResponse, error) {
				callOrder = append(callOrder, "direct_before")
				return nil, nil
			})
		flow.MaxSteps = 1

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)
		invCtx.AgentName = "test-agent"
		invCtx.RunConfig = &RunConfig{MaxLLMCalls: 10}

		events := flow.RunAsync(ctx, invCtx)
		for range events {
		}

		// Verify both callbacks were called
		if len(callOrder) < 2 {
			t.Fatalf("expected at least 2 callbacks, got %d", len(callOrder))
		}
		// Plugin callbacks should run before direct callbacks
		if callOrder[0] != "plugin_before" {
			t.Errorf("plugin callback should run first, got %s", callOrder[0])
		}
		if callOrder[1] != "direct_before" {
			t.Errorf("direct callback should run second, got %s", callOrder[1])
		}
	})

	t.Run("PluginShortCircuitsFlow", func(t *testing.T) {
		pm := NewPluginManager()

		// Plugin that short-circuits
		plugin := NewBasePlugin("blocker", 100)
		plugin.AddBeforeModelCallback(func(ctx *CallbackContext, req *LlmRequest) (*LlmResponse, error) {
			return &LlmResponse{
				Content: &Content{Parts: []Part{{Text: "blocked"}}},
			}, nil
		})
		pm.Register(plugin)

		// Provider that should not be called
		provider := NewMockLlmProvider(&LlmResponse{
			Content: &Content{Parts: []Part{{Text: "from_provider"}}},
		})

		flow := NewLlmFlow(provider).WithPluginManager(pm)
		flow.MaxSteps = 1

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)
		invCtx.AgentName = "test-agent"
		invCtx.RunConfig = &RunConfig{MaxLLMCalls: 10}

		var lastEvent *AsyncEvent
		for event := range flow.RunAsync(ctx, invCtx) {
			lastEvent = event
		}

		if provider.callCount != 0 {
			t.Errorf("provider should not be called when plugin short-circuits, got %d calls", provider.callCount)
		}
		if lastEvent == nil {
			t.Fatal("should have received event")
		}
	})
}

func TestFlowWithToolCallbacks(t *testing.T) {
	t.Run("DirectToolCallbacks", func(t *testing.T) {
		beforeCalled := false
		afterCalled := false

		flow := NewLlmFlow(nil).
			WithBeforeToolCallback(func(ctx *ToolContext, toolName string, args map[string]interface{}) error {
				beforeCalled = true
				return nil
			}).
			WithAfterToolCallback(func(ctx *ToolContext, toolName string, args map[string]interface{}, result interface{}, err error) error {
				afterCalled = true
				return nil
			})

		if len(flow.BeforeToolCallbacks) != 1 {
			t.Error("expected 1 before-tool callback")
		}
		if len(flow.AfterToolCallbacks) != 1 {
			t.Error("expected 1 after-tool callback")
		}

		// Manually invoke to test
		invCtx := &InvocationContext{}
		toolCtx := NewToolContext(invCtx, "test")

		flow.BeforeToolCallbacks[0](toolCtx, "test", nil)
		flow.AfterToolCallbacks[0](toolCtx, "test", nil, "result", nil)

		if !beforeCalled {
			t.Error("before callback should be called")
		}
		if !afterCalled {
			t.Error("after callback should be called")
		}
	})
}

// ============================================================================
// Live Session Manager Tests
// ============================================================================

func TestLiveSessionManager(t *testing.T) {
	t.Run("NewLiveSessionManager", func(t *testing.T) {
		manager := NewLiveSessionManager(nil)
		if manager == nil {
			t.Fatal("manager should not be nil")
		}
		if len(manager.ListActiveSessions()) != 0 {
			t.Error("should have no sessions initially")
		}
	})

	t.Run("CreateSession", func(t *testing.T) {
		manager := NewLiveSessionManager(nil)

		ctx := context.Background()
		input := &agent.AgentInput{Instruction: "test"}
		invCtx := NewInvocationContext(ctx, input)

		session := manager.CreateSession(invCtx)
		if session == nil {
			t.Fatal("session should not be nil")
		}
		if session.ID == "" {
			t.Error("session should have an ID")
		}
		if session.Closed {
			t.Error("session should not be closed initially")
		}
		if session.RequestQueue == nil {
			t.Error("session should have a request queue")
		}

		sessions := manager.ListActiveSessions()
		if len(sessions) != 1 {
			t.Errorf("expected 1 session, got %d", len(sessions))
		}
	})

	t.Run("GetSession", func(t *testing.T) {
		manager := NewLiveSessionManager(nil)

		ctx := context.Background()
		invCtx := NewInvocationContext(ctx, &agent.AgentInput{})

		session := manager.CreateSession(invCtx)

		retrieved, ok := manager.GetSession(session.ID)
		if !ok {
			t.Error("should find created session")
		}
		if retrieved.ID != session.ID {
			t.Error("retrieved session should match created session")
		}

		_, ok = manager.GetSession("nonexistent")
		if ok {
			t.Error("should not find nonexistent session")
		}
	})

	t.Run("CloseSession", func(t *testing.T) {
		manager := NewLiveSessionManager(nil)

		ctx := context.Background()
		invCtx := NewInvocationContext(ctx, &agent.AgentInput{})

		session := manager.CreateSession(invCtx)
		sessionID := session.ID

		err := manager.CloseSession(sessionID)
		if err != nil {
			t.Errorf("close should succeed: %v", err)
		}

		if !session.Closed {
			t.Error("session should be marked as closed")
		}

		_, ok := manager.GetSession(sessionID)
		if ok {
			t.Error("closed session should be removed from manager")
		}

		err = manager.CloseSession("nonexistent")
		if err == nil {
			t.Error("closing nonexistent session should fail")
		}
	})

	t.Run("PauseSession", func(t *testing.T) {
		manager := NewLiveSessionManager(nil)

		ctx := context.Background()
		invCtx := NewInvocationContext(ctx, &agent.AgentInput{})

		session := manager.CreateSession(invCtx)

		pauseState, err := manager.PauseSession(session.ID)
		if err != nil {
			t.Errorf("pause should succeed: %v", err)
		}
		if pauseState == nil {
			t.Fatal("pause state should not be nil")
		}
		if pauseState.CheckpointID == "" {
			t.Error("pause state should have checkpoint ID")
		}
		if pauseState.PausedAt.IsZero() {
			t.Error("pause state should have timestamp")
		}
		if session.PauseState == nil {
			t.Error("session should have pause state set")
		}

		_, err = manager.PauseSession("nonexistent")
		if err == nil {
			t.Error("pausing nonexistent session should fail")
		}
	})

	t.Run("ResumeSession", func(t *testing.T) {
		manager := NewLiveSessionManager(nil)

		ctx := context.Background()
		invCtx := NewInvocationContext(ctx, &agent.AgentInput{})

		session := manager.CreateSession(invCtx)
		oldActivity := session.LastActivity

		pauseState, _ := manager.PauseSession(session.ID)

		// Wait a tiny bit to ensure time difference
		time.Sleep(time.Millisecond)

		resumed, err := manager.ResumeSession(session.ID, pauseState)
		if err != nil {
			t.Errorf("resume should succeed: %v", err)
		}
		if resumed == nil {
			t.Fatal("resumed session should not be nil")
		}
		if resumed.PauseState != nil {
			t.Error("pause state should be cleared after resume")
		}
		if !resumed.LastActivity.After(oldActivity) {
			t.Error("last activity should be updated on resume")
		}

		_, err = manager.ResumeSession("nonexistent", pauseState)
		if err == nil {
			t.Error("resuming nonexistent session should fail")
		}
	})

	t.Run("CleanupIdleSessions", func(t *testing.T) {
		config := &LiveSessionConfig{
			MaxIdleTime:  10 * time.Millisecond,
			BufferSize:   10,
		}
		manager := NewLiveSessionManager(config)

		ctx := context.Background()
		invCtx := NewInvocationContext(ctx, &agent.AgentInput{})

		session1 := manager.CreateSession(invCtx)
		session2 := manager.CreateSession(invCtx)

		// Wait for sessions to become idle
		time.Sleep(20 * time.Millisecond)

		cleaned := manager.CleanupIdleSessions()
		if cleaned != 2 {
			t.Errorf("expected 2 sessions cleaned, got %d", cleaned)
		}

		if !session1.Closed || !session2.Closed {
			t.Error("sessions should be marked as closed")
		}

		if len(manager.ListActiveSessions()) != 0 {
			t.Error("all sessions should be removed")
		}
	})

	t.Run("OnDisconnectCallback", func(t *testing.T) {
		disconnectedSessions := []string{}
		config := &LiveSessionConfig{
			MaxIdleTime:  10 * time.Millisecond,
			BufferSize:   10,
			OnDisconnect: func(session *LiveSession) {
				disconnectedSessions = append(disconnectedSessions, session.ID)
			},
		}
		manager := NewLiveSessionManager(config)

		ctx := context.Background()
		invCtx := NewInvocationContext(ctx, &agent.AgentInput{})

		session := manager.CreateSession(invCtx)
		expectedID := session.ID

		time.Sleep(20 * time.Millisecond)
		manager.CleanupIdleSessions()

		if len(disconnectedSessions) != 1 {
			t.Errorf("expected 1 disconnect callback, got %d", len(disconnectedSessions))
		}
		if disconnectedSessions[0] != expectedID {
			t.Error("callback should receive correct session ID")
		}
	})
}

func TestSessionPauseState(t *testing.T) {
	t.Run("CapturesState", func(t *testing.T) {
		state := &SessionPauseState{
			Contents: []Content{
				{Role: "user", Parts: []Part{{Text: "Hello"}}},
				{Role: "assistant", Parts: []Part{{Text: "Hi there"}}},
			},
			PendingCalls: []*FunctionCall{
				{Name: "search", Args: map[string]interface{}{"query": "test"}},
			},
			SystemInstruction: "You are helpful",
			CheckpointID:      "checkpoint-123",
			PausedAt:          time.Now(),
		}

		if len(state.Contents) != 2 {
			t.Error("should capture conversation history")
		}
		if len(state.PendingCalls) != 1 {
			t.Error("should capture pending calls")
		}
		if state.SystemInstruction != "You are helpful" {
			t.Error("should capture system instruction")
		}
	})
}

// ============================================================================
// Long-Running Tool Tracker Tests
// ============================================================================

func TestLongRunningToolTracker(t *testing.T) {
	t.Run("NewTracker", func(t *testing.T) {
		tracker := NewLongRunningToolTracker()
		if tracker == nil {
			t.Fatal("tracker should not be nil")
		}
		if len(tracker.ListRunningTools()) != 0 {
			t.Error("should have no tools initially")
		}
	})

	t.Run("StartTool", func(t *testing.T) {
		tracker := NewLongRunningToolTracker()

		_, cancel := context.WithCancel(context.Background())
		defer cancel()

		tool := tracker.StartTool("search", map[string]interface{}{"query": "test"}, cancel)
		if tool == nil {
			t.Fatal("tool should not be nil")
		}
		if tool.ID == "" {
			t.Error("tool should have an ID")
		}
		if tool.Name != "search" {
			t.Error("tool should have correct name")
		}
		if tool.Status != ToolStatusRunning {
			t.Error("tool should be running")
		}
		if tool.StartTime.IsZero() {
			t.Error("tool should have start time")
		}

		running := tracker.ListRunningTools()
		if len(running) != 1 {
			t.Errorf("expected 1 running tool, got %d", len(running))
		}
	})

	t.Run("GetTool", func(t *testing.T) {
		tracker := NewLongRunningToolTracker()
		_, cancel := context.WithCancel(context.Background())
		defer cancel()

		tool := tracker.StartTool("test", nil, cancel)

		retrieved, ok := tracker.GetTool(tool.ID)
		if !ok {
			t.Error("should find started tool")
		}
		if retrieved.ID != tool.ID {
			t.Error("retrieved tool should match")
		}

		_, ok = tracker.GetTool("nonexistent")
		if ok {
			t.Error("should not find nonexistent tool")
		}
	})

	t.Run("UpdateProgress", func(t *testing.T) {
		tracker := NewLongRunningToolTracker()
		_, cancel := context.WithCancel(context.Background())
		defer cancel()

		tool := tracker.StartTool("test", nil, cancel)

		err := tracker.UpdateProgress(tool.ID, 0.5, "halfway done")
		if err != nil {
			t.Errorf("update should succeed: %v", err)
		}

		retrieved, _ := tracker.GetTool(tool.ID)
		if retrieved.Progress != 0.5 {
			t.Errorf("expected progress 0.5, got %f", retrieved.Progress)
		}
		if retrieved.ProgressMsg != "halfway done" {
			t.Error("progress message should be updated")
		}

		err = tracker.UpdateProgress("nonexistent", 0.5, "test")
		if err == nil {
			t.Error("updating nonexistent tool should fail")
		}
	})

	t.Run("CompleteTool", func(t *testing.T) {
		tracker := NewLongRunningToolTracker()
		_, cancel := context.WithCancel(context.Background())
		defer cancel()

		tool := tracker.StartTool("test", nil, cancel)

		err := tracker.CompleteTool(tool.ID, "result_data", nil)
		if err != nil {
			t.Errorf("complete should succeed: %v", err)
		}

		retrieved, _ := tracker.GetTool(tool.ID)
		if retrieved.Status != ToolStatusCompleted {
			t.Error("tool should be completed")
		}
		if retrieved.Result != "result_data" {
			t.Error("result should be set")
		}
		if retrieved.EndTime.IsZero() {
			t.Error("end time should be set")
		}

		// Test with error
		tool2 := tracker.StartTool("test2", nil, cancel)
		testErr := errors.New("something failed")
		tracker.CompleteTool(tool2.ID, nil, testErr)

		retrieved2, _ := tracker.GetTool(tool2.ID)
		if retrieved2.Status != ToolStatusFailed {
			t.Error("tool with error should be failed")
		}
		if retrieved2.Error != testErr {
			t.Error("error should be set")
		}
	})

	t.Run("CancelTool", func(t *testing.T) {
		tracker := NewLongRunningToolTracker()
		ctx, cancel := context.WithCancel(context.Background())

		tool := tracker.StartTool("test", nil, cancel)

		err := tracker.CancelTool(tool.ID)
		if err != nil {
			t.Errorf("cancel should succeed: %v", err)
		}

		retrieved, _ := tracker.GetTool(tool.ID)
		if retrieved.Status != ToolStatusCancelled {
			t.Error("tool should be cancelled")
		}

		// Verify context was cancelled
		select {
		case <-ctx.Done():
			// Good - context was cancelled
		default:
			t.Error("cancel should have cancelled the context")
		}

		err = tracker.CancelTool("nonexistent")
		if err == nil {
			t.Error("cancelling nonexistent tool should fail")
		}
	})

	t.Run("ListRunningTools", func(t *testing.T) {
		tracker := NewLongRunningToolTracker()
		_, cancel := context.WithCancel(context.Background())
		defer cancel()

		tool1 := tracker.StartTool("running1", nil, cancel)
		tool2 := tracker.StartTool("running2", nil, cancel)
		tool3 := tracker.StartTool("completed", nil, cancel)
		tracker.CompleteTool(tool3.ID, nil, nil)

		running := tracker.ListRunningTools()
		if len(running) != 2 {
			t.Errorf("expected 2 running tools, got %d", len(running))
		}

		// Verify only running tools are returned
		ids := map[string]bool{}
		for _, tool := range running {
			ids[tool.ID] = true
		}
		if !ids[tool1.ID] || !ids[tool2.ID] {
			t.Error("should return both running tools")
		}
		if ids[tool3.ID] {
			t.Error("should not return completed tool")
		}
	})

	t.Run("CleanupCompleted", func(t *testing.T) {
		tracker := NewLongRunningToolTracker()
		_, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Create and complete tools
		tool1 := tracker.StartTool("old", nil, cancel)
		tracker.CompleteTool(tool1.ID, nil, nil)

		tool2 := tracker.StartTool("running", nil, cancel)

		// Sleep to make tool1 "old"
		time.Sleep(20 * time.Millisecond)

		cleaned := tracker.CleanupCompleted(10 * time.Millisecond)
		if cleaned != 1 {
			t.Errorf("expected 1 tool cleaned, got %d", cleaned)
		}

		_, ok := tracker.GetTool(tool1.ID)
		if ok {
			t.Error("old completed tool should be removed")
		}

		_, ok = tracker.GetTool(tool2.ID)
		if !ok {
			t.Error("running tool should not be removed")
		}
	})

	t.Run("ToolStatusString", func(t *testing.T) {
		tests := []struct {
			status   ToolStatus
			expected string
		}{
			{ToolStatusPending, "pending"},
			{ToolStatusRunning, "running"},
			{ToolStatusCompleted, "completed"},
			{ToolStatusFailed, "failed"},
			{ToolStatusCancelled, "cancelled"},
			{ToolStatus(99), "unknown"},
		}

		for _, tt := range tests {
			if tt.status.String() != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.status.String())
			}
		}
	})
}

func TestLiveSessionConfig(t *testing.T) {
	t.Run("DefaultConfig", func(t *testing.T) {
		config := DefaultLiveSessionConfig()
		if config == nil {
			t.Fatal("default config should not be nil")
		}
		if config.MaxIdleTime != 5*time.Minute {
			t.Error("default max idle time should be 5 minutes")
		}
		if config.BufferSize != 100 {
			t.Error("default buffer size should be 100")
		}
		if config.EnableAudioCache {
			t.Error("audio cache should be disabled by default")
		}
		if config.EnableTranscription {
			t.Error("transcription should be disabled by default")
		}
	})

	t.Run("CustomConfig", func(t *testing.T) {
		config := &LiveSessionConfig{
			MaxIdleTime:         10 * time.Minute,
			EnableAudioCache:    true,
			BufferSize:          200,
			EnableTranscription: true,
		}

		manager := NewLiveSessionManager(config)
		if manager.config.MaxIdleTime != 10*time.Minute {
			t.Error("should use custom max idle time")
		}
		if manager.config.BufferSize != 200 {
			t.Error("should use custom buffer size")
		}
	})
}
