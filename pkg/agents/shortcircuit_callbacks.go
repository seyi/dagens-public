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

// Package agents provides short-circuit callback support matching Google ADK Go patterns.
//
// Short-circuit callbacks can return content to stop normal execution flow.
// This enables use cases like:
// - Caching: Return cached response instead of calling LLM
// - Content filtering: Block or modify inappropriate requests
// - Rate limiting: Return error before expensive operations
// - A/B testing: Route to different implementations
package agents

import (
	"context"
	"errors"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/model"
)

// ============================================================================
// Short-Circuit Callback Types (ADK Pattern)
// ============================================================================

// ShortCircuitError signals that a callback wants to short-circuit execution
var ErrShortCircuit = errors.New("callback short-circuit")

// CallbackResult represents the result of a short-circuit capable callback
type CallbackResult struct {
	// Content to use instead of normal execution (nil = continue normally)
	Content interface{}
	// ShouldStop indicates execution should stop after this callback
	ShouldStop bool
	// Error if callback encountered an error
	Error error
}

// Continue returns a result that continues normal execution
func Continue() *CallbackResult {
	return &CallbackResult{ShouldStop: false}
}

// ShortCircuit returns a result that stops execution with the given content
func ShortCircuit(content interface{}) *CallbackResult {
	return &CallbackResult{
		Content:    content,
		ShouldStop: true,
	}
}

// ShortCircuitWithError returns a result that stops execution with an error
func ShortCircuitWithError(err error) *CallbackResult {
	return &CallbackResult{
		ShouldStop: true,
		Error:      err,
	}
}

// ============================================================================
// Short-Circuit Model Callbacks (ADK Pattern)
// ============================================================================

// ShortCircuitModelCallback is called before model invocation and can short-circuit.
// If it returns Content != nil, the model call is skipped and Content is used as response.
// This matches Google ADK Go's BeforeModelCallback behavior.
type ShortCircuitModelCallback func(ctx context.Context, input *model.ModelInput) *CallbackResult

// ShortCircuitAfterModelCallback is called after model invocation and can modify/replace response.
// If it returns Content != nil, the model response is replaced with Content.
// This matches Google ADK Go's AfterModelCallback behavior.
type ShortCircuitAfterModelCallback func(ctx context.Context, input *model.ModelInput, output *model.ModelOutput) *CallbackResult

// ============================================================================
// Short-Circuit Agent Callbacks (ADK Pattern)
// ============================================================================

// ShortCircuitAgentCallback is called before agent execution and can short-circuit.
// If it returns Content != nil, agent execution is skipped.
// This matches Google ADK Go's BeforeAgentCallback behavior.
type ShortCircuitAgentCallback func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) *CallbackResult

// ShortCircuitAfterAgentCallback is called after agent execution and can modify/replace output.
// If it returns Content != nil, the agent output is replaced.
// This matches Google ADK Go's AfterAgentCallback behavior.
type ShortCircuitAfterAgentCallback func(ctx context.Context, ag agent.Agent, input *agent.AgentInput, output *agent.AgentOutput) *CallbackResult

// ============================================================================
// Short-Circuit Tool Callbacks (ADK Pattern)
// ============================================================================

// ShortCircuitToolCallback is called before tool execution and can short-circuit.
// If it returns Content != nil, tool execution is skipped.
type ShortCircuitToolCallback func(ctx context.Context, toolName string, args map[string]interface{}) *CallbackResult

// ShortCircuitAfterToolCallback is called after tool execution and can modify/replace result.
// If it returns Content != nil, the tool result is replaced.
type ShortCircuitAfterToolCallback func(ctx context.Context, toolName string, args map[string]interface{}, result interface{}) *CallbackResult

// ============================================================================
// Short-Circuit Callback Executor
// ============================================================================

// ShortCircuitExecutor executes callbacks with short-circuit support
type ShortCircuitExecutor struct {
	// StopOnError stops execution if a callback returns an error
	StopOnError bool
}

// NewShortCircuitExecutor creates a new short-circuit executor
func NewShortCircuitExecutor(stopOnError bool) *ShortCircuitExecutor {
	return &ShortCircuitExecutor{StopOnError: stopOnError}
}

// ExecuteBeforeModel executes before-model callbacks with short-circuit support.
// Returns (shortCircuitOutput, shouldShortCircuit, error)
func (e *ShortCircuitExecutor) ExecuteBeforeModel(
	ctx context.Context,
	callbacks []ShortCircuitModelCallback,
	input *model.ModelInput,
) (*model.ModelOutput, bool, error) {
	for _, callback := range callbacks {
		result := callback(ctx, input)
		if result == nil {
			continue
		}

		if result.Error != nil {
			if e.StopOnError {
				return nil, true, result.Error
			}
			continue
		}

		if result.ShouldStop && result.Content != nil {
			// Convert content to ModelOutput
			output := contentToModelOutput(result.Content)
			return output, true, nil
		}
	}
	return nil, false, nil
}

// ExecuteAfterModel executes after-model callbacks with short-circuit support.
// Can modify or replace the model output.
func (e *ShortCircuitExecutor) ExecuteAfterModel(
	ctx context.Context,
	callbacks []ShortCircuitAfterModelCallback,
	input *model.ModelInput,
	output *model.ModelOutput,
) (*model.ModelOutput, error) {
	currentOutput := output
	for _, callback := range callbacks {
		result := callback(ctx, input, currentOutput)
		if result == nil {
			continue
		}

		if result.Error != nil {
			if e.StopOnError {
				return nil, result.Error
			}
			continue
		}

		if result.ShouldStop && result.Content != nil {
			// Replace output
			currentOutput = contentToModelOutput(result.Content)
		}
	}
	return currentOutput, nil
}

// ExecuteBeforeAgent executes before-agent callbacks with short-circuit support.
// Returns (shortCircuitOutput, shouldShortCircuit, error)
func (e *ShortCircuitExecutor) ExecuteBeforeAgent(
	ctx context.Context,
	callbacks []ShortCircuitAgentCallback,
	ag agent.Agent,
	input *agent.AgentInput,
) (*agent.AgentOutput, bool, error) {
	for _, callback := range callbacks {
		result := callback(ctx, ag, input)
		if result == nil {
			continue
		}

		if result.Error != nil {
			if e.StopOnError {
				return nil, true, result.Error
			}
			continue
		}

		if result.ShouldStop && result.Content != nil {
			// Convert content to AgentOutput
			output := contentToAgentOutput(result.Content)
			return output, true, nil
		}
	}
	return nil, false, nil
}

// ExecuteAfterAgent executes after-agent callbacks with short-circuit support.
// Can modify or replace the agent output.
func (e *ShortCircuitExecutor) ExecuteAfterAgent(
	ctx context.Context,
	callbacks []ShortCircuitAfterAgentCallback,
	ag agent.Agent,
	input *agent.AgentInput,
	output *agent.AgentOutput,
) (*agent.AgentOutput, error) {
	currentOutput := output
	for _, callback := range callbacks {
		result := callback(ctx, ag, input, currentOutput)
		if result == nil {
			continue
		}

		if result.Error != nil {
			if e.StopOnError {
				return nil, result.Error
			}
			continue
		}

		if result.ShouldStop && result.Content != nil {
			// Replace output
			currentOutput = contentToAgentOutput(result.Content)
		}
	}
	return currentOutput, nil
}

// ExecuteBeforeTool executes before-tool callbacks with short-circuit support.
// Returns (shortCircuitResult, shouldShortCircuit, error)
func (e *ShortCircuitExecutor) ExecuteBeforeTool(
	ctx context.Context,
	callbacks []ShortCircuitToolCallback,
	toolName string,
	args map[string]interface{},
) (interface{}, bool, error) {
	for _, callback := range callbacks {
		result := callback(ctx, toolName, args)
		if result == nil {
			continue
		}

		if result.Error != nil {
			if e.StopOnError {
				return nil, true, result.Error
			}
			continue
		}

		if result.ShouldStop && result.Content != nil {
			return result.Content, true, nil
		}
	}
	return nil, false, nil
}

// ExecuteAfterTool executes after-tool callbacks with short-circuit support.
// Can modify or replace the tool result.
func (e *ShortCircuitExecutor) ExecuteAfterTool(
	ctx context.Context,
	callbacks []ShortCircuitAfterToolCallback,
	toolName string,
	args map[string]interface{},
	result interface{},
) (interface{}, error) {
	currentResult := result
	for _, callback := range callbacks {
		cbResult := callback(ctx, toolName, args, currentResult)
		if cbResult == nil {
			continue
		}

		if cbResult.Error != nil {
			if e.StopOnError {
				return nil, cbResult.Error
			}
			continue
		}

		if cbResult.ShouldStop && cbResult.Content != nil {
			currentResult = cbResult.Content
		}
	}
	return currentResult, nil
}

// ============================================================================
// Helper Functions
// ============================================================================

// contentToModelOutput converts arbitrary content to ModelOutput
func contentToModelOutput(content interface{}) *model.ModelOutput {
	switch v := content.(type) {
	case *model.ModelOutput:
		return v
	case string:
		return &model.ModelOutput{
			Text:         v,
			FinishReason: "callback_short_circuit",
		}
	case map[string]interface{}:
		output := &model.ModelOutput{
			FinishReason: "callback_short_circuit",
		}
		if text, ok := v["text"].(string); ok {
			output.Text = text
		}
		if tokens, ok := v["tokens_used"].(int); ok {
			output.TokensUsed = tokens
		}
		return output
	default:
		return &model.ModelOutput{
			Text:         "",
			FinishReason: "callback_short_circuit",
		}
	}
}

// contentToAgentOutput converts arbitrary content to AgentOutput
func contentToAgentOutput(content interface{}) *agent.AgentOutput {
	switch v := content.(type) {
	case *agent.AgentOutput:
		return v
	case string:
		return &agent.AgentOutput{
			Result: v,
			Metadata: map[string]interface{}{
				"source": "callback_short_circuit",
			},
		}
	case map[string]interface{}:
		output := &agent.AgentOutput{
			Metadata: map[string]interface{}{
				"source": "callback_short_circuit",
			},
		}
		if result, ok := v["result"]; ok {
			output.Result = result
		}
		if meta, ok := v["metadata"].(map[string]interface{}); ok {
			for k, val := range meta {
				output.Metadata[k] = val
			}
		}
		return output
	default:
		return &agent.AgentOutput{
			Result: content,
			Metadata: map[string]interface{}{
				"source": "callback_short_circuit",
			},
		}
	}
}

// ============================================================================
// Built-in Short-Circuit Callbacks
// ============================================================================

// CacheCallback creates a caching short-circuit callback
type CacheCallback struct {
	cache map[string]*model.ModelOutput
}

// NewCacheShortCircuitCallback creates a new cache callback
func NewCacheShortCircuitCallback() *CacheCallback {
	return &CacheCallback{
		cache: make(map[string]*model.ModelOutput),
	}
}

// BeforeModel returns cached response if available
func (c *CacheCallback) BeforeModel() ShortCircuitModelCallback {
	return func(ctx context.Context, input *model.ModelInput) *CallbackResult {
		if cached, ok := c.cache[input.Prompt]; ok {
			return ShortCircuit(cached)
		}
		return Continue()
	}
}

// AfterModel caches the response
func (c *CacheCallback) AfterModel() ShortCircuitAfterModelCallback {
	return func(ctx context.Context, input *model.ModelInput, output *model.ModelOutput) *CallbackResult {
		c.cache[input.Prompt] = output
		return Continue()
	}
}

// Get retrieves from cache
func (c *CacheCallback) Get(key string) (*model.ModelOutput, bool) {
	v, ok := c.cache[key]
	return v, ok
}

// Set stores in cache
func (c *CacheCallback) Set(key string, output *model.ModelOutput) {
	c.cache[key] = output
}

// Clear clears the cache
func (c *CacheCallback) Clear() {
	c.cache = make(map[string]*model.ModelOutput)
}

// ContentFilterCallback creates a content filtering short-circuit callback
func ContentFilterCallback(filter func(string) (bool, string)) ShortCircuitModelCallback {
	return func(ctx context.Context, input *model.ModelInput) *CallbackResult {
		allowed, reason := filter(input.Prompt)
		if !allowed {
			return ShortCircuitWithError(errors.New("content filtered: " + reason))
		}
		return Continue()
	}
}

// RateLimitCallback creates a rate limiting short-circuit callback
func RateLimitShortCircuitCallback(limiter func() bool, message string) ShortCircuitModelCallback {
	return func(ctx context.Context, input *model.ModelInput) *CallbackResult {
		if !limiter() {
			return ShortCircuitWithError(errors.New(message))
		}
		return Continue()
	}
}

// ConditionalShortCircuit creates a callback that short-circuits based on condition
func ConditionalShortCircuit(
	condition func(context.Context, *model.ModelInput) bool,
	content func(context.Context, *model.ModelInput) interface{},
) ShortCircuitModelCallback {
	return func(ctx context.Context, input *model.ModelInput) *CallbackResult {
		if condition(ctx, input) {
			return ShortCircuit(content(ctx, input))
		}
		return Continue()
	}
}

// ResponseModifierCallback creates a callback that modifies model responses
func ResponseModifierCallback(modifier func(*model.ModelOutput) *model.ModelOutput) ShortCircuitAfterModelCallback {
	return func(ctx context.Context, input *model.ModelInput, output *model.ModelOutput) *CallbackResult {
		modified := modifier(output)
		if modified != output {
			return ShortCircuit(modified)
		}
		return Continue()
	}
}
