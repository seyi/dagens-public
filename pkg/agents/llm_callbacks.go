package agents

import (
	"context"
	"fmt"

	"github.com/seyi/dagens/pkg/model"
)

// LLM-specific lifecycle callbacks
// Inspired by ADK's before_model/after_model and before_tool/after_tool callbacks

// ModelCallback is called before or after LLM model invocation
type ModelCallback func(ctx context.Context, input *model.ModelInput) error

// ModelOutputCallback is called after LLM model completes
type ModelOutputCallback func(ctx context.Context, input *model.ModelInput, output *model.ModelOutput) error

// ToolCallback is called before or after tool execution
type ToolCallback func(ctx context.Context, toolName string, params map[string]interface{}) error

// ToolResultCallback is called after tool execution completes
type ToolResultCallback func(ctx context.Context, toolName string, params map[string]interface{}, result interface{}) error

// ModelCallbackExecutor manages LLM model callback execution
type ModelCallbackExecutor struct {
	stopOnError bool
}

// NewModelCallbackExecutor creates a new model callback executor
func NewModelCallbackExecutor(stopOnError bool) *ModelCallbackExecutor {
	return &ModelCallbackExecutor{
		stopOnError: stopOnError,
	}
}

// ExecuteBefore executes before-model callbacks
func (mce *ModelCallbackExecutor) ExecuteBefore(ctx context.Context, callbacks []ModelCallback, input *model.ModelInput) error {
	for i, callback := range callbacks {
		if err := callback(ctx, input); err != nil {
			if mce.stopOnError {
				return fmt.Errorf("before-model callback %d failed: %w", i, err)
			}
		}
	}
	return nil
}

// ExecuteAfter executes after-model callbacks
func (mce *ModelCallbackExecutor) ExecuteAfter(ctx context.Context, callbacks []ModelOutputCallback, input *model.ModelInput, output *model.ModelOutput) error {
	for i, callback := range callbacks {
		if err := callback(ctx, input, output); err != nil {
			if mce.stopOnError {
				return fmt.Errorf("after-model callback %d failed: %w", i, err)
			}
		}
	}
	return nil
}

// ToolCallbackExecutor manages tool callback execution
type ToolCallbackExecutor struct {
	stopOnError bool
}

// NewToolCallbackExecutor creates a new tool callback executor
func NewToolCallbackExecutor(stopOnError bool) *ToolCallbackExecutor {
	return &ToolCallbackExecutor{
		stopOnError: stopOnError,
	}
}

// ExecuteBefore executes before-tool callbacks
func (tce *ToolCallbackExecutor) ExecuteBefore(ctx context.Context, callbacks []ToolCallback, toolName string, params map[string]interface{}) error {
	for i, callback := range callbacks {
		if err := callback(ctx, toolName, params); err != nil {
			if tce.stopOnError {
				return fmt.Errorf("before-tool callback %d failed: %w", i, err)
			}
		}
	}
	return nil
}

// ExecuteAfter executes after-tool callbacks
func (tce *ToolCallbackExecutor) ExecuteAfter(ctx context.Context, callbacks []ToolResultCallback, toolName string, params map[string]interface{}, result interface{}) error {
	for i, callback := range callbacks {
		if err := callback(ctx, toolName, params, result); err != nil {
			if tce.stopOnError {
				return fmt.Errorf("after-tool callback %d failed: %w", i, err)
			}
		}
	}
	return nil
}

// Built-in LLM callbacks

// LogModelCallback logs model invocations
func LogModelCallback(stage string) ModelCallback {
	return func(ctx context.Context, input *model.ModelInput) error {
		fmt.Printf("[%s] Model call - Prompt length: %d chars\n", stage, len(input.Prompt))
		return nil
	}
}

// LogModelOutputCallback logs model responses
func LogModelOutputCallback() ModelOutputCallback {
	return func(ctx context.Context, input *model.ModelInput, output *model.ModelOutput) error {
		fmt.Printf("[after_model] Response length: %d chars, Tokens: %d\n",
			len(output.Text), output.TokensUsed)
		return nil
	}
}

// LogToolCallback logs tool invocations
func LogToolCallback(stage string) ToolCallback {
	return func(ctx context.Context, toolName string, params map[string]interface{}) error {
		fmt.Printf("[%s] Tool: %s, Params: %v\n", stage, toolName, params)
		return nil
	}
}

// LogToolResultCallback logs tool results
func LogToolResultCallback() ToolResultCallback {
	return func(ctx context.Context, toolName string, params map[string]interface{}, result interface{}) error {
		fmt.Printf("[after_tool] Tool %s completed, Result type: %T\n", toolName, result)
		return nil
	}
}

// TokenCounterCallback tracks token usage
type TokenCounterCallback struct {
	totalTokens int
}

func NewTokenCounterCallback() *TokenCounterCallback {
	return &TokenCounterCallback{}
}

func (tcc *TokenCounterCallback) AfterModel() ModelOutputCallback {
	return func(ctx context.Context, input *model.ModelInput, output *model.ModelOutput) error {
		tcc.totalTokens += output.TokensUsed
		return nil
	}
}

func (tcc *TokenCounterCallback) GetTotal() int {
	return tcc.totalTokens
}

func (tcc *TokenCounterCallback) Reset() {
	tcc.totalTokens = 0
}

// PromptModifierCallback modifies prompts before sending to model
func PromptModifierCallback(modifier func(string) string) ModelCallback {
	return func(ctx context.Context, input *model.ModelInput) error {
		input.Prompt = modifier(input.Prompt)
		return nil
	}
}

// ResponseFilterCallback filters model responses
func ResponseFilterCallback(filter func(string) (string, error)) ModelOutputCallback {
	return func(ctx context.Context, input *model.ModelInput, output *model.ModelOutput) error {
		filtered, err := filter(output.Text)
		if err != nil {
			return err
		}
		output.Text = filtered
		return nil
	}
}

// ToolAuthorizationCallback checks if tool is authorized
func ToolAuthorizationCallback(authorizer func(string) bool) ToolCallback {
	return func(ctx context.Context, toolName string, params map[string]interface{}) error {
		if !authorizer(toolName) {
			return fmt.Errorf("tool %s not authorized", toolName)
		}
		return nil
	}
}

// ToolParameterValidationCallback validates tool parameters
func ToolParameterValidationCallback(validator func(string, map[string]interface{}) error) ToolCallback {
	return func(ctx context.Context, toolName string, params map[string]interface{}) error {
		return validator(toolName, params)
	}
}

// CachedModelCallback caches model responses
type CachedModelCallback struct {
	cache map[string]*model.ModelOutput
}

func NewCachedModelCallback() *CachedModelCallback {
	return &CachedModelCallback{
		cache: make(map[string]*model.ModelOutput),
	}
}

func (cmc *CachedModelCallback) Before() ModelCallback {
	return func(ctx context.Context, input *model.ModelInput) error {
		// Store input for potential caching
		// In a real implementation, would check cache here
		return nil
	}
}

func (cmc *CachedModelCallback) After() ModelOutputCallback {
	return func(ctx context.Context, input *model.ModelInput, output *model.ModelOutput) error {
		// Cache the response
		cacheKey := input.Prompt // Simplified cache key
		cmc.cache[cacheKey] = output
		return nil
	}
}

func (cmc *CachedModelCallback) Get(prompt string) (*model.ModelOutput, bool) {
	output, ok := cmc.cache[prompt]
	return output, ok
}

func (cmc *CachedModelCallback) Clear() {
	cmc.cache = make(map[string]*model.ModelOutput)
}

// RateLimiterCallback implements rate limiting for model calls
type RateLimiterCallback struct {
	maxCallsPerMinute int
	calls             []int64
}

func NewRateLimiterCallback(maxCallsPerMinute int) *RateLimiterCallback {
	return &RateLimiterCallback{
		maxCallsPerMinute: maxCallsPerMinute,
		calls:             make([]int64, 0),
	}
}

func (rlc *RateLimiterCallback) Before() ModelCallback {
	return func(ctx context.Context, input *model.ModelInput) error {
		// Simple rate limiting implementation
		// In production, would use time.Now().Unix() and sliding window
		if len(rlc.calls) >= rlc.maxCallsPerMinute {
			return fmt.Errorf("rate limit exceeded: %d calls per minute", rlc.maxCallsPerMinute)
		}
		rlc.calls = append(rlc.calls, 1) // Simplified
		return nil
	}
}

// MetricsCollectorCallback collects detailed metrics
type MetricsCollectorCallback struct {
	modelCalls    int
	toolCalls     int
	totalTokens   int
	errors        int
	averageLatency float64
}

func NewMetricsCollectorCallback() *MetricsCollectorCallback {
	return &MetricsCollectorCallback{}
}

func (mcc *MetricsCollectorCallback) BeforeModel() ModelCallback {
	return func(ctx context.Context, input *model.ModelInput) error {
		mcc.modelCalls++
		return nil
	}
}

func (mcc *MetricsCollectorCallback) AfterModel() ModelOutputCallback {
	return func(ctx context.Context, input *model.ModelInput, output *model.ModelOutput) error {
		mcc.totalTokens += output.TokensUsed
		return nil
	}
}

func (mcc *MetricsCollectorCallback) BeforeTool() ToolCallback {
	return func(ctx context.Context, toolName string, params map[string]interface{}) error {
		mcc.toolCalls++
		return nil
	}
}

func (mcc *MetricsCollectorCallback) GetMetrics() map[string]interface{} {
	return map[string]interface{}{
		"model_calls":  mcc.modelCalls,
		"tool_calls":   mcc.toolCalls,
		"total_tokens": mcc.totalTokens,
		"errors":       mcc.errors,
	}
}

func (mcc *MetricsCollectorCallback) Reset() {
	mcc.modelCalls = 0
	mcc.toolCalls = 0
	mcc.totalTokens = 0
	mcc.errors = 0
}
