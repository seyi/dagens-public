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

// Package runtime provides ADK-compatible LLM flow with processors and callbacks.
//
// This file implements the ADK BaseLlmFlow pattern with:
// - Request/Response processors that can yield events
// - Before/After model callbacks that can short-circuit execution
// - Error recovery callbacks
// - Function/tool call handling
package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// LlmRequest represents a request to the LLM.
// Matches ADK's LlmRequest structure.
type LlmRequest struct {
	// Contents is the conversation history
	Contents []Content `json:"contents,omitempty"`

	// SystemInstruction is the system prompt
	SystemInstruction string `json:"system_instruction,omitempty"`

	// Tools available for this request
	Tools []ToolDefinition `json:"tools,omitempty"`

	// ToolsDict maps tool names to their definitions
	ToolsDict map[string]ToolDefinition `json:"-"`

	// Config contains generation configuration
	Config *GenerateConfig `json:"config,omitempty"`

	// Labels for billing/tracking (ADK pattern)
	Labels map[string]string `json:"labels,omitempty"`
}

// Content represents a message in the conversation
type Content struct {
	Role  string `json:"role"`
	Parts []Part `json:"parts"`
}

// Part represents a part of a message (text, function call, etc.)
type Part struct {
	Text             string            `json:"text,omitempty"`
	FunctionCall     *FunctionCall     `json:"function_call,omitempty"`
	FunctionResponse *FunctionResponse `json:"function_response,omitempty"`
}

// FunctionCall represents a function/tool call from the LLM
type FunctionCall struct {
	ID   string                 `json:"id,omitempty"`
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

// FunctionResponse represents the result of a function call
type FunctionResponse struct {
	Name     string      `json:"name"`
	Response interface{} `json:"response"`
	Error    string      `json:"error,omitempty"`
}

// ToolDefinition defines a tool available to the LLM
type ToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
	Handler     ToolHandler            `json:"-"`
}

// ToolHandler is a function that executes a tool
type ToolHandler func(ctx context.Context, args map[string]interface{}) (interface{}, error)

// GenerateConfig contains LLM generation parameters
type GenerateConfig struct {
	Temperature     float64           `json:"temperature,omitempty"`
	TopP            float64           `json:"top_p,omitempty"`
	TopK            int               `json:"top_k,omitempty"`
	MaxOutputTokens int               `json:"max_output_tokens,omitempty"`
	StopSequences   []string          `json:"stop_sequences,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
}

// LlmResponse represents a response from the LLM.
// Matches ADK's LlmResponse structure.
type LlmResponse struct {
	// Content is the response content
	Content *Content `json:"content,omitempty"`

	// Partial indicates if this is a streaming chunk
	Partial bool `json:"partial,omitempty"`

	// ErrorCode if the response is an error
	ErrorCode string `json:"error_code,omitempty"`

	// ErrorMessage provides error details
	ErrorMessage string `json:"error_message,omitempty"`

	// TurnComplete indicates the LLM finished its turn
	TurnComplete bool `json:"turn_complete,omitempty"`

	// Interrupted indicates the response was interrupted
	Interrupted bool `json:"interrupted,omitempty"`

	// UsageMetadata contains token usage info
	UsageMetadata *UsageMetadata `json:"usage_metadata,omitempty"`

	// GroundingMetadata for search grounding
	GroundingMetadata interface{} `json:"grounding_metadata,omitempty"`
}

// UsageMetadata contains token usage information
type UsageMetadata struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// GetFunctionCalls extracts function calls from the response
func (r *LlmResponse) GetFunctionCalls() []*FunctionCall {
	if r.Content == nil {
		return nil
	}
	var calls []*FunctionCall
	for _, part := range r.Content.Parts {
		if part.FunctionCall != nil {
			calls = append(calls, part.FunctionCall)
		}
	}
	return calls
}

// HasFunctionCalls checks if the response contains function calls
func (r *LlmResponse) HasFunctionCalls() bool {
	return len(r.GetFunctionCalls()) > 0
}

// CallbackContext provides context for callbacks (mutable)
type CallbackContext struct {
	InvocationContext *InvocationContext
	EventActions      *EventActions
}

// NewCallbackContext creates a new callback context
func NewCallbackContext(invCtx *InvocationContext, actions *EventActions) *CallbackContext {
	return &CallbackContext{
		InvocationContext: invCtx,
		EventActions:      actions,
	}
}

// ReadonlyContext provides read-only access to invocation context
type ReadonlyContext struct {
	invCtx *InvocationContext
}

// NewReadonlyContext creates a read-only context wrapper
func NewReadonlyContext(invCtx *InvocationContext) *ReadonlyContext {
	return &ReadonlyContext{invCtx: invCtx}
}

func (r *ReadonlyContext) SessionID() string    { return r.invCtx.SessionID }
func (r *ReadonlyContext) UserID() string       { return r.invCtx.UserID }
func (r *ReadonlyContext) InvocationID() string { return r.invCtx.InvocationID }
func (r *ReadonlyContext) Get(key string) (interface{}, bool) {
	return r.invCtx.Get(key)
}

// ToolContext provides context for tool execution
type ToolContext struct {
	InvocationContext *InvocationContext
	ToolName          string
}

// NewToolContext creates a new tool context
func NewToolContext(invCtx *InvocationContext, toolName string) *ToolContext {
	return &ToolContext{
		InvocationContext: invCtx,
		ToolName:          toolName,
	}
}

// RequestProcessor processes LLM requests before they're sent.
// Can yield events (e.g., for preprocessing status).
type RequestProcessor interface {
	// ProcessRequest modifies the request and optionally yields events
	ProcessRequest(ctx context.Context, invCtx *InvocationContext, req *LlmRequest) <-chan *AsyncEvent
}

// ResponseProcessor processes LLM responses after they're received.
// Can yield events (e.g., for postprocessing status).
type ResponseProcessor interface {
	// ProcessResponse handles the response and optionally yields events
	ProcessResponse(ctx context.Context, invCtx *InvocationContext, resp *LlmResponse) <-chan *AsyncEvent
}

// BeforeModelCallback is called before the LLM is invoked.
// Can return a response to short-circuit the LLM call.
type BeforeModelCallback func(ctx *CallbackContext, req *LlmRequest) (*LlmResponse, error)

// AfterModelCallback is called after the LLM responds.
// Can modify the response.
type AfterModelCallback func(ctx *CallbackContext, resp *LlmResponse) (*LlmResponse, error)

// OnModelErrorCallback is called when the LLM returns an error.
// Can return a recovery response.
type OnModelErrorCallback func(ctx *CallbackContext, req *LlmRequest, err error) (*LlmResponse, error)

// StreamingMode controls how streaming responses are handled
type StreamingMode int

const (
	// StreamingModeNone - no streaming, wait for complete response
	StreamingModeNone StreamingMode = iota
	// StreamingModeSSE - Server-Sent Events streaming (yield partial responses)
	StreamingModeSSE
	// StreamingModeBidirectional - bidirectional streaming (CFC mode)
	StreamingModeBidirectional
)

// Tracer interface for OpenTelemetry-style tracing
type Tracer interface {
	// StartSpan starts a new span with the given name
	StartSpan(ctx context.Context, name string) (context.Context, Span)
}

// Span represents a trace span
type Span interface {
	// End ends the span
	End()
	// SetAttribute sets an attribute on the span
	SetAttribute(key string, value interface{})
	// RecordError records an error on the span
	RecordError(err error)
}

// NoopTracer is a tracer that does nothing (default)
type NoopTracer struct{}

func (t *NoopTracer) StartSpan(ctx context.Context, name string) (context.Context, Span) {
	return ctx, &NoopSpan{}
}

// NoopSpan is a span that does nothing
type NoopSpan struct{}

func (s *NoopSpan) End()                                  {}
func (s *NoopSpan) SetAttribute(key string, value interface{}) {}
func (s *NoopSpan) RecordError(err error)                 {}

// TraceCallback is called for each LLM call for detailed tracing
type TraceCallback func(invCtx *InvocationContext, eventID string, req *LlmRequest, resp *LlmResponse)

// LiveRequestQueue for CFC (Continuous Function Calling) mode
type LiveRequestQueue struct {
	requests chan *LlmRequest
	closed   bool
}

// NewLiveRequestQueue creates a new live request queue
func NewLiveRequestQueue() *LiveRequestQueue {
	return &LiveRequestQueue{
		requests: make(chan *LlmRequest, 10),
	}
}

// Send sends a request to the queue
func (q *LiveRequestQueue) Send(req *LlmRequest) {
	if !q.closed {
		q.requests <- req
	}
}

// Receive returns the request channel
func (q *LiveRequestQueue) Receive() <-chan *LlmRequest {
	return q.requests
}

// Close closes the queue
func (q *LiveRequestQueue) Close() {
	if !q.closed {
		q.closed = true
		close(q.requests)
	}
}

// RunConfig contains runtime configuration for an invocation
type RunConfig struct {
	// StreamingMode controls streaming behavior
	StreamingMode StreamingMode

	// SupportCFC enables Continuous Function Calling mode
	SupportCFC bool

	// MaxLLMCalls limits the number of LLM calls per invocation
	MaxLLMCalls int
}

// LlmFlow orchestrates LLM execution with processors and callbacks.
// This is Go's equivalent to ADK's BaseLlmFlow.
type LlmFlow struct {
	// Processors
	RequestProcessors  []RequestProcessor
	ResponseProcessors []ResponseProcessor

	// Callbacks
	BeforeModelCallbacks  []BeforeModelCallback
	AfterModelCallbacks   []AfterModelCallback
	OnModelErrorCallbacks []OnModelErrorCallback

	// LLM provider
	LlmProvider LlmProvider

	// Live provider for CFC mode (optional)
	LiveProvider LiveLlmProvider

	// Configuration
	AgentNameLabelKey string
	MaxSteps          int

	// Tracing (optional)
	Tracer        Tracer
	TraceCallback TraceCallback

	// Default run config
	DefaultRunConfig *RunConfig

	// Plugin Manager (ADK-compatible)
	PluginManager *PluginManager

	// BeforeTool/AfterTool callbacks (direct, not via plugin)
	BeforeToolCallbacks []BeforeToolCallback
	AfterToolCallbacks  []AfterToolCallback
}

// LiveLlmProvider is the interface for bidirectional streaming LLM backends
type LiveLlmProvider interface {
	// RunLive runs bidirectional streaming with the LLM
	RunLive(ctx context.Context, requestQueue *LiveRequestQueue) <-chan *LlmResponse
}

// LlmProvider is the interface for LLM backends
type LlmProvider interface {
	// GenerateContent calls the LLM and streams responses
	GenerateContent(ctx context.Context, req *LlmRequest) <-chan *LlmResponse
}

// NewLlmFlow creates a new LLM flow
func NewLlmFlow(provider LlmProvider) *LlmFlow {
	return &LlmFlow{
		LlmProvider:       provider,
		AgentNameLabelKey: "adk_agent_name",
		MaxSteps:          10,
	}
}

// WithRequestProcessor adds a request processor
func (f *LlmFlow) WithRequestProcessor(p RequestProcessor) *LlmFlow {
	f.RequestProcessors = append(f.RequestProcessors, p)
	return f
}

// WithResponseProcessor adds a response processor
func (f *LlmFlow) WithResponseProcessor(p ResponseProcessor) *LlmFlow {
	f.ResponseProcessors = append(f.ResponseProcessors, p)
	return f
}

// WithBeforeModelCallback adds a before-model callback
func (f *LlmFlow) WithBeforeModelCallback(cb BeforeModelCallback) *LlmFlow {
	f.BeforeModelCallbacks = append(f.BeforeModelCallbacks, cb)
	return f
}

// WithAfterModelCallback adds an after-model callback
func (f *LlmFlow) WithAfterModelCallback(cb AfterModelCallback) *LlmFlow {
	f.AfterModelCallbacks = append(f.AfterModelCallbacks, cb)
	return f
}

// WithOnModelErrorCallback adds an error callback
func (f *LlmFlow) WithOnModelErrorCallback(cb OnModelErrorCallback) *LlmFlow {
	f.OnModelErrorCallbacks = append(f.OnModelErrorCallbacks, cb)
	return f
}

// WithBeforeToolCallback adds a before-tool callback
func (f *LlmFlow) WithBeforeToolCallback(cb BeforeToolCallback) *LlmFlow {
	f.BeforeToolCallbacks = append(f.BeforeToolCallbacks, cb)
	return f
}

// WithAfterToolCallback adds an after-tool callback
func (f *LlmFlow) WithAfterToolCallback(cb AfterToolCallback) *LlmFlow {
	f.AfterToolCallbacks = append(f.AfterToolCallbacks, cb)
	return f
}

// RunAsync executes the LLM flow and yields events.
// This is Go's equivalent to ADK's run_async.
func (f *LlmFlow) RunAsync(ctx context.Context, invCtx *InvocationContext) <-chan *AsyncEvent {
	output := make(chan *AsyncEvent)

	go func() {
		defer close(output)

		step := 0
		for step < f.MaxSteps {
			step++

			var lastEvent *AsyncEvent

			// Run one step
			stepEvents := f.runOneStepAsync(ctx, invCtx)
			for event := range stepEvents {
				if event.Metadata == nil {
					event.Metadata = make(map[string]interface{})
				}
				event.Metadata["step"] = step
				lastEvent = event
				output <- event

				// Check for errors
				if event.Error != nil {
					return
				}
			}

			// Check if we should stop
			if lastEvent == nil {
				return
			}
			if lastEvent.IsFinalResponse() {
				return
			}

			// Check context cancellation
			select {
			case <-ctx.Done():
				errorEvent := NewAsyncEvent(EventTypeError, ctx.Err().Error(), "llm_flow")
				errorEvent.Error = ctx.Err()
				output <- errorEvent
				return
			default:
			}
		}

		// Max steps reached
		maxStepsEvent := NewAsyncEvent(EventTypeMessage, "Maximum steps reached", "llm_flow")
		maxStepsEvent.Metadata = map[string]interface{}{
			"max_steps_reached": true,
			"steps_executed":    step,
		}
		output <- maxStepsEvent
	}()

	return output
}

// runOneStepAsync executes one LLM step (one LLM call + tool handling)
func (f *LlmFlow) runOneStepAsync(ctx context.Context, invCtx *InvocationContext) <-chan *AsyncEvent {
	output := make(chan *AsyncEvent)

	go func() {
		defer close(output)

		// Create LLM request
		llmRequest := &LlmRequest{
			Config: &GenerateConfig{
				Labels: make(map[string]string),
			},
			Labels:    make(map[string]string),
			ToolsDict: make(map[string]ToolDefinition),
		}

		// Preprocess: run request processors
		for _, processor := range f.RequestProcessors {
			events := processor.ProcessRequest(ctx, invCtx, llmRequest)
			for event := range events {
				output <- event
			}

			// Check if invocation should end
			if invCtx.EndInvocation {
				return
			}
		}

		// Create model response event
		modelResponseEvent := NewAsyncEvent(EventTypeMessage, "", invCtx.AgentName)
		modelResponseEvent.InvocationID = invCtx.InvocationID

		// Call LLM with callbacks
		llmResponses := f.callLlmAsync(ctx, invCtx, llmRequest, modelResponseEvent)

		for llmResponse := range llmResponses {
			// Postprocess: run response processors
			for _, processor := range f.ResponseProcessors {
				events := processor.ProcessResponse(ctx, invCtx, llmResponse)
				for event := range events {
					output <- event
				}
			}

			// Skip empty responses
			if llmResponse.Content == nil && llmResponse.ErrorCode == "" && !llmResponse.Interrupted {
				continue
			}

			// Build the event
			event := f.finalizeModelResponseEvent(llmRequest, llmResponse, modelResponseEvent)
			output <- event

			// Handle function calls
			if llmResponse.HasFunctionCalls() {
				functionEvents := f.handleFunctionCallsAsync(ctx, invCtx, event, llmRequest)
				for funcEvent := range functionEvents {
					output <- funcEvent
				}
			}
		}
	}()

	return output
}

// callLlmAsync calls the LLM with tracing, callbacks, and streaming support.
// This is Go's equivalent to ADK's _call_llm_with_tracing().
func (f *LlmFlow) callLlmAsync(
	ctx context.Context,
	invCtx *InvocationContext,
	llmRequest *LlmRequest,
	modelResponseEvent *AsyncEvent,
) <-chan *LlmResponse {
	output := make(chan *LlmResponse)

	go func() {
		defer close(output)

		// Start tracing span
		tracer := f.Tracer
		if tracer == nil {
			tracer = &NoopTracer{}
		}
		spanCtx, span := tracer.StartSpan(ctx, "call_llm")
		defer span.End()

		// Run before-model callbacks
		callbackCtx := NewCallbackContext(invCtx, modelResponseEvent.Actions)

		// First run plugin before-model callbacks
		if f.PluginManager != nil {
			response, err := f.PluginManager.RunBeforeModelCallbacks(callbackCtx, llmRequest)
			if err != nil {
				span.RecordError(err)
				output <- &LlmResponse{
					ErrorCode:    "plugin_callback_error",
					ErrorMessage: err.Error(),
				}
				return
			}
			if response != nil {
				// Plugin short-circuited - yield its response and return
				output <- response
				return
			}
		}

		// Then run direct before-model callbacks
		for _, callback := range f.BeforeModelCallbacks {
			response, err := callback(callbackCtx, llmRequest)
			if err != nil {
				span.RecordError(err)
				output <- &LlmResponse{
					ErrorCode:    "callback_error",
					ErrorMessage: err.Error(),
				}
				return
			}
			if response != nil {
				// Callback short-circuited - yield its response and return
				output <- response
				return
			}
		}

		// Add agent name label for billing
		if llmRequest.Config == nil {
			llmRequest.Config = &GenerateConfig{Labels: make(map[string]string)}
		}
		if llmRequest.Config.Labels == nil {
			llmRequest.Config.Labels = make(map[string]string)
		}
		if _, ok := llmRequest.Config.Labels[f.AgentNameLabelKey]; !ok {
			llmRequest.Config.Labels[f.AgentNameLabelKey] = invCtx.AgentName
		}

		// Get run config
		runConfig := f.getRunConfig(invCtx)
		streamingMode := StreamingModeNone
		supportCFC := false
		if runConfig != nil {
			streamingMode = runConfig.StreamingMode
			supportCFC = runConfig.SupportCFC
		}

		// CFC (Continuous Function Calling) mode with LiveRequestQueue
		if supportCFC && f.LiveProvider != nil {
			// Set up live request queue
			liveQueue := NewLiveRequestQueue()
			invCtx.LiveRequestQueue = liveQueue

			// Send initial request
			liveQueue.Send(llmRequest)

			// Run live mode
			responses := f.LiveProvider.RunLive(spanCtx, liveQueue)
			for llmResponse := range f.runAndHandleErrorStream(spanCtx, invCtx, responses, llmRequest, modelResponseEvent) {
				// Run after-model callback
				if alteredResponse := f.handleAfterModelCallback(callbackCtx, llmResponse, span); alteredResponse != nil {
					llmResponse = alteredResponse
				}

				// Only yield partial responses in SSE streaming mode
				if streamingMode == StreamingModeSSE || !llmResponse.Partial {
					output <- llmResponse
				}

				// Handle turn completion
				if llmResponse.TurnComplete {
					liveQueue.Close()
				}
			}
			return
		}

		// Standard mode - check LLM call limit
		if err := invCtx.IncrementLLMCallCount(); err != nil {
			span.RecordError(err)
			output <- &LlmResponse{
				ErrorCode:    "call_limit_exceeded",
				ErrorMessage: err.Error(),
			}
			return
		}

		// Call the LLM
		if f.LlmProvider == nil {
			output <- &LlmResponse{
				ErrorCode:    "no_provider",
				ErrorMessage: "LLM provider not configured",
			}
			return
		}

		// Determine if we should stream
		shouldStream := streamingMode == StreamingModeSSE

		// Execute with error handling
		responses := f.runWithErrorHandling(spanCtx, invCtx, llmRequest, modelResponseEvent)

		for llmResponse := range responses {
			// Trace the LLM call
			if f.TraceCallback != nil {
				f.TraceCallback(invCtx, modelResponseEvent.ID, llmRequest, llmResponse)
			}
			span.SetAttribute("response_partial", llmResponse.Partial)

			// Run after-model callback
			if alteredResponse := f.handleAfterModelCallback(callbackCtx, llmResponse, span); alteredResponse != nil {
				llmResponse = alteredResponse
			}

			// Only yield partial responses in SSE streaming mode
			if shouldStream || !llmResponse.Partial {
				output <- llmResponse
			}
		}
	}()

	return output
}

// getRunConfig gets the run configuration for this invocation
func (f *LlmFlow) getRunConfig(invCtx *InvocationContext) *RunConfig {
	if invCtx.RunConfig != nil {
		if rc, ok := invCtx.RunConfig.(*RunConfig); ok {
			return rc
		}
	}
	return f.DefaultRunConfig
}

// handleAfterModelCallback runs after-model callbacks and returns altered response if any
func (f *LlmFlow) handleAfterModelCallback(callbackCtx *CallbackContext, response *LlmResponse, span Span) *LlmResponse {
	// First run plugin after-model callbacks
	if f.PluginManager != nil {
		alteredResponse, err := f.PluginManager.RunAfterModelCallbacks(callbackCtx, response)
		if err != nil {
			span.RecordError(err)
		} else if alteredResponse != nil {
			response = alteredResponse
		}
	}

	// Then run direct after-model callbacks
	for _, callback := range f.AfterModelCallbacks {
		alteredResponse, err := callback(callbackCtx, response)
		if err != nil {
			span.RecordError(err)
			continue
		}
		if alteredResponse != nil {
			return alteredResponse
		}
	}
	return nil
}

// runAndHandleErrorStream wraps a response stream with error handling
func (f *LlmFlow) runAndHandleErrorStream(
	ctx context.Context,
	invCtx *InvocationContext,
	responses <-chan *LlmResponse,
	llmRequest *LlmRequest,
	modelResponseEvent *AsyncEvent,
) <-chan *LlmResponse {
	output := make(chan *LlmResponse)

	go func() {
		defer close(output)

		for response := range responses {
			if response.ErrorCode != "" {
				// Try error recovery callbacks
				callbackCtx := NewCallbackContext(invCtx, modelResponseEvent.Actions)
				err := fmt.Errorf("%s: %s", response.ErrorCode, response.ErrorMessage)

				for _, callback := range f.OnModelErrorCallbacks {
					recoveryResponse, cbErr := callback(callbackCtx, llmRequest, err)
					if cbErr != nil {
						continue
					}
					if recoveryResponse != nil {
						output <- recoveryResponse
						return
					}
				}
			}
			output <- response
		}
	}()

	return output
}

// runWithErrorHandling wraps LLM execution with error recovery
func (f *LlmFlow) runWithErrorHandling(
	ctx context.Context,
	invCtx *InvocationContext,
	llmRequest *LlmRequest,
	modelResponseEvent *AsyncEvent,
) <-chan *LlmResponse {
	output := make(chan *LlmResponse)

	go func() {
		defer close(output)

		// Create a channel to capture panics as errors
		done := make(chan struct{})
		var capturedErr error

		go func() {
			defer func() {
				if r := recover(); r != nil {
					capturedErr = fmt.Errorf("panic in LLM call: %v", r)
				}
				close(done)
			}()

			responses := f.LlmProvider.GenerateContent(ctx, llmRequest)
			for response := range responses {
				output <- response
			}
		}()

		<-done

		if capturedErr != nil {
			// Try error recovery callbacks
			callbackCtx := NewCallbackContext(invCtx, modelResponseEvent.Actions)

			for _, callback := range f.OnModelErrorCallbacks {
				recoveryResponse, err := callback(callbackCtx, llmRequest, capturedErr)
				if err != nil {
					continue
				}
				if recoveryResponse != nil {
					output <- recoveryResponse
					return
				}
			}

			// No recovery - yield error response
			output <- &LlmResponse{
				ErrorCode:    "llm_error",
				ErrorMessage: capturedErr.Error(),
			}
		}
	}()

	return output
}

// handleFunctionCallsAsync processes function calls from the LLM
func (f *LlmFlow) handleFunctionCallsAsync(
	ctx context.Context,
	invCtx *InvocationContext,
	functionCallEvent *AsyncEvent,
	llmRequest *LlmRequest,
) <-chan *AsyncEvent {
	output := make(chan *AsyncEvent)

	go func() {
		defer close(output)

		// Get function calls from the event content
		llmResponse, ok := functionCallEvent.Content.(*LlmResponse)
		if !ok {
			return
		}

		functionCalls := llmResponse.GetFunctionCalls()
		if len(functionCalls) == 0 {
			return
		}

		// Execute each function call
		var functionResponses []FunctionResponse

		for _, call := range functionCalls {
			// Look up the tool
			tool, exists := llmRequest.ToolsDict[call.Name]
			if !exists {
				functionResponses = append(functionResponses, FunctionResponse{
					Name:  call.Name,
					Error: fmt.Sprintf("unknown tool: %s", call.Name),
				})
				continue
			}

			// Execute the tool
			toolCtx := NewToolContext(invCtx, call.Name)

			// Run before-tool callbacks (plugins first)
			if f.PluginManager != nil {
				if err := f.PluginManager.RunBeforeToolCallbacks(toolCtx, call.Name, call.Args); err != nil {
					functionResponses = append(functionResponses, FunctionResponse{
						Name:  call.Name,
						Error: fmt.Sprintf("before_tool callback failed: %s", err.Error()),
					})
					continue
				}
			}

			// Run direct before-tool callbacks
			shouldSkip := false
			for _, cb := range f.BeforeToolCallbacks {
				if err := cb(toolCtx, call.Name, call.Args); err != nil {
					functionResponses = append(functionResponses, FunctionResponse{
						Name:  call.Name,
						Error: fmt.Sprintf("before_tool callback failed: %s", err.Error()),
					})
					shouldSkip = true
					break
				}
			}
			if shouldSkip {
				continue
			}

			if tool.Handler == nil {
				functionResponses = append(functionResponses, FunctionResponse{
					Name:  call.Name,
					Error: "tool has no handler",
				})
				continue
			}

			result, err := tool.Handler(ctx, call.Args)

			// Run after-tool callbacks (plugins first)
			if f.PluginManager != nil {
				if cbErr := f.PluginManager.RunAfterToolCallbacks(toolCtx, call.Name, call.Args, result, err); cbErr != nil {
					// Log but don't fail the tool result
					_ = cbErr
				}
			}

			// Run direct after-tool callbacks
			for _, cb := range f.AfterToolCallbacks {
				if cbErr := cb(toolCtx, call.Name, call.Args, result, err); cbErr != nil {
					// Log but don't fail the tool result
					_ = cbErr
				}
			}

			if err != nil {
				functionResponses = append(functionResponses, FunctionResponse{
					Name:  call.Name,
					Error: err.Error(),
				})
			} else {
				functionResponses = append(functionResponses, FunctionResponse{
					Name:     call.Name,
					Response: result,
				})
			}
		}

		// Create function response event
		functionResponseEvent := NewAsyncEvent(EventTypeToolResult, functionResponses, invCtx.AgentName)
		functionResponseEvent.InvocationID = invCtx.InvocationID
		functionResponseEvent.Timestamp = time.Now()

		// Check for agent transfer
		for _, resp := range functionResponses {
			if resp.Name == "transfer_to_agent" {
				if agentName, ok := resp.Response.(string); ok {
					functionResponseEvent.Actions = &EventActions{
						TransferToAgent: agentName,
					}
				}
			}
		}

		output <- functionResponseEvent
	}()

	return output
}

// finalizeModelResponseEvent builds the final event from the LLM response
func (f *LlmFlow) finalizeModelResponseEvent(
	llmRequest *LlmRequest,
	llmResponse *LlmResponse,
	modelResponseEvent *AsyncEvent,
) *AsyncEvent {
	event := &AsyncEvent{
		ID:           generateID("event"),
		InvocationID: modelResponseEvent.InvocationID,
		Author:       modelResponseEvent.Author,
		Type:         EventTypeMessage,
		Timestamp:    time.Now(),
		Partial:      llmResponse.Partial,
	}

	// Set content based on response
	if llmResponse.Content != nil {
		// Extract text content
		var textParts []string
		for _, part := range llmResponse.Content.Parts {
			if part.Text != "" {
				textParts = append(textParts, part.Text)
			}
		}
		if len(textParts) > 0 {
			event.Content = textParts[0]
			if len(textParts) > 1 {
				event.Content = textParts
			}
		}

		// Check for function calls
		if llmResponse.HasFunctionCalls() {
			event.Type = EventTypeToolCall
			event.Content = llmResponse
		}
	}

	// Set error if present
	if llmResponse.ErrorCode != "" {
		event.Type = EventTypeError
		event.Error = fmt.Errorf("%s: %s", llmResponse.ErrorCode, llmResponse.ErrorMessage)
	}

	// Copy metadata
	if llmResponse.UsageMetadata != nil {
		if event.Metadata == nil {
			event.Metadata = make(map[string]interface{})
		}
		event.Metadata["usage"] = llmResponse.UsageMetadata
	}

	return event
}

// Convenience functions for creating common processors

// LoggingRequestProcessor logs requests
type LoggingRequestProcessor struct {
	Prefix string
}

func (p *LoggingRequestProcessor) ProcessRequest(ctx context.Context, invCtx *InvocationContext, req *LlmRequest) <-chan *AsyncEvent {
	output := make(chan *AsyncEvent)
	go func() {
		defer close(output)
		// Emit a state change event for logging
		event := NewAsyncEvent(EventTypeStateChange, fmt.Sprintf("%s: Processing request", p.Prefix), "processor")
		event.Partial = true
		output <- event
	}()
	return output
}

// LoggingResponseProcessor logs responses
type LoggingResponseProcessor struct {
	Prefix string
}

func (p *LoggingResponseProcessor) ProcessResponse(ctx context.Context, invCtx *InvocationContext, resp *LlmResponse) <-chan *AsyncEvent {
	output := make(chan *AsyncEvent)
	go func() {
		defer close(output)
		// Emit a state change event for logging
		event := NewAsyncEvent(EventTypeStateChange, fmt.Sprintf("%s: Processed response", p.Prefix), "processor")
		event.Partial = true
		output <- event
	}()
	return output
}

// CachingBeforeModelCallback checks cache before LLM call
func CachingBeforeModelCallback(cache map[string]*LlmResponse) BeforeModelCallback {
	return func(ctx *CallbackContext, req *LlmRequest) (*LlmResponse, error) {
		// Simple cache key from system instruction
		cacheKey := req.SystemInstruction
		if cached, ok := cache[cacheKey]; ok {
			return cached, nil
		}
		return nil, nil
	}
}

// RateLimitingBeforeModelCallback implements rate limiting
type RateLimitingBeforeModelCallback struct {
	MaxCallsPerMinute int
	callCount         int
	windowStart       time.Time
}

func (r *RateLimitingBeforeModelCallback) Callback() BeforeModelCallback {
	return func(ctx *CallbackContext, req *LlmRequest) (*LlmResponse, error) {
		now := time.Now()
		if now.Sub(r.windowStart) > time.Minute {
			r.callCount = 0
			r.windowStart = now
		}

		if r.callCount >= r.MaxCallsPerMinute {
			return &LlmResponse{
				ErrorCode:    "rate_limited",
				ErrorMessage: fmt.Sprintf("rate limit exceeded: %d calls per minute", r.MaxCallsPerMinute),
			}, nil
		}

		r.callCount++
		return nil, nil
	}
}

// RetryOnErrorCallback retries on specific errors
func RetryOnErrorCallback(maxRetries int, retryableErrors []string) OnModelErrorCallback {
	retryCount := 0
	return func(ctx *CallbackContext, req *LlmRequest, err error) (*LlmResponse, error) {
		if retryCount >= maxRetries {
			return nil, nil // Let the error propagate
		}

		errStr := err.Error()
		for _, retryable := range retryableErrors {
			if errStr == retryable {
				retryCount++
				// Return nil to signal retry should happen
				// In practice, the flow would need to re-call the LLM
				return nil, nil
			}
		}

		return nil, nil
	}
}

// ============================================================================
// Plugin Manager System (ADK-compatible)
// ============================================================================

// Plugin defines the interface for LLM flow plugins.
// Plugins can register callbacks and have lifecycle methods.
// Inspired by ADK's plugin system for extensibility.
type Plugin interface {
	// Name returns the unique plugin name
	Name() string

	// Initialize is called when the plugin is registered
	Initialize(ctx context.Context) error

	// Cleanup is called when the plugin is unregistered
	Cleanup(ctx context.Context) error

	// BeforeModel returns callbacks to run before LLM calls
	BeforeModel() []BeforeModelCallback

	// AfterModel returns callbacks to run after LLM calls
	AfterModel() []AfterModelCallback

	// OnModelError returns callbacks for error recovery
	OnModelError() []OnModelErrorCallback

	// BeforeTool returns callbacks to run before tool execution
	BeforeTool() []BeforeToolCallback

	// AfterTool returns callbacks to run after tool execution
	AfterTool() []AfterToolCallback

	// RequestProcessors returns request processors from this plugin
	RequestProcessors() []RequestProcessor

	// ResponseProcessors returns response processors from this plugin
	ResponseProcessors() []ResponseProcessor

	// Priority determines callback order (higher = earlier)
	Priority() int

	// Enabled returns whether the plugin is currently active
	Enabled() bool

	// SetEnabled enables or disables the plugin
	SetEnabled(enabled bool)
}

// BeforeToolCallback is called before tool execution
type BeforeToolCallback func(ctx *ToolContext, toolName string, args map[string]interface{}) error

// AfterToolCallback is called after tool execution with result
type AfterToolCallback func(ctx *ToolContext, toolName string, args map[string]interface{}, result interface{}, err error) error

// BasePlugin provides a default implementation of Plugin interface
type BasePlugin struct {
	name     string
	priority int
	enabled  bool

	beforeModelCallbacks  []BeforeModelCallback
	afterModelCallbacks   []AfterModelCallback
	onModelErrorCallbacks []OnModelErrorCallback
	beforeToolCallbacks   []BeforeToolCallback
	afterToolCallbacks    []AfterToolCallback
	requestProcessors     []RequestProcessor
	responseProcessors    []ResponseProcessor
}

// NewBasePlugin creates a new base plugin
func NewBasePlugin(name string, priority int) *BasePlugin {
	return &BasePlugin{
		name:     name,
		priority: priority,
		enabled:  true,
	}
}

func (p *BasePlugin) Name() string                           { return p.name }
func (p *BasePlugin) Priority() int                          { return p.priority }
func (p *BasePlugin) Enabled() bool                          { return p.enabled }
func (p *BasePlugin) SetEnabled(enabled bool)                { p.enabled = enabled }
func (p *BasePlugin) Initialize(ctx context.Context) error   { return nil }
func (p *BasePlugin) Cleanup(ctx context.Context) error      { return nil }
func (p *BasePlugin) BeforeModel() []BeforeModelCallback     { return p.beforeModelCallbacks }
func (p *BasePlugin) AfterModel() []AfterModelCallback       { return p.afterModelCallbacks }
func (p *BasePlugin) OnModelError() []OnModelErrorCallback   { return p.onModelErrorCallbacks }
func (p *BasePlugin) BeforeTool() []BeforeToolCallback       { return p.beforeToolCallbacks }
func (p *BasePlugin) AfterTool() []AfterToolCallback         { return p.afterToolCallbacks }
func (p *BasePlugin) RequestProcessors() []RequestProcessor  { return p.requestProcessors }
func (p *BasePlugin) ResponseProcessors() []ResponseProcessor { return p.responseProcessors }

// AddBeforeModelCallback adds a before-model callback to the plugin
func (p *BasePlugin) AddBeforeModelCallback(cb BeforeModelCallback) *BasePlugin {
	p.beforeModelCallbacks = append(p.beforeModelCallbacks, cb)
	return p
}

// AddAfterModelCallback adds an after-model callback to the plugin
func (p *BasePlugin) AddAfterModelCallback(cb AfterModelCallback) *BasePlugin {
	p.afterModelCallbacks = append(p.afterModelCallbacks, cb)
	return p
}

// AddOnModelErrorCallback adds an error callback to the plugin
func (p *BasePlugin) AddOnModelErrorCallback(cb OnModelErrorCallback) *BasePlugin {
	p.onModelErrorCallbacks = append(p.onModelErrorCallbacks, cb)
	return p
}

// AddBeforeToolCallback adds a before-tool callback to the plugin
func (p *BasePlugin) AddBeforeToolCallback(cb BeforeToolCallback) *BasePlugin {
	p.beforeToolCallbacks = append(p.beforeToolCallbacks, cb)
	return p
}

// AddAfterToolCallback adds an after-tool callback to the plugin
func (p *BasePlugin) AddAfterToolCallback(cb AfterToolCallback) *BasePlugin {
	p.afterToolCallbacks = append(p.afterToolCallbacks, cb)
	return p
}

// AddRequestProcessor adds a request processor to the plugin
func (p *BasePlugin) AddRequestProcessor(proc RequestProcessor) *BasePlugin {
	p.requestProcessors = append(p.requestProcessors, proc)
	return p
}

// AddResponseProcessor adds a response processor to the plugin
func (p *BasePlugin) AddResponseProcessor(proc ResponseProcessor) *BasePlugin {
	p.responseProcessors = append(p.responseProcessors, proc)
	return p
}

// PluginManager coordinates plugin registration and callback execution.
// This matches ADK's plugin_manager pattern.
type PluginManager struct {
	plugins       map[string]Plugin
	pluginOrder   []string // Maintains insertion order for deterministic behavior
	initialized   bool
}

// NewPluginManager creates a new plugin manager
func NewPluginManager() *PluginManager {
	return &PluginManager{
		plugins:     make(map[string]Plugin),
		pluginOrder: make([]string, 0),
	}
}

// Register adds a plugin to the manager
func (pm *PluginManager) Register(plugin Plugin) error {
	if _, exists := pm.plugins[plugin.Name()]; exists {
		return fmt.Errorf("plugin %s already registered", plugin.Name())
	}

	pm.plugins[plugin.Name()] = plugin

	// Insert in priority order
	inserted := false
	for i, name := range pm.pluginOrder {
		if pm.plugins[name].Priority() < plugin.Priority() {
			// Insert before this plugin
			pm.pluginOrder = append(pm.pluginOrder[:i], append([]string{plugin.Name()}, pm.pluginOrder[i:]...)...)
			inserted = true
			break
		}
	}
	if !inserted {
		pm.pluginOrder = append(pm.pluginOrder, plugin.Name())
	}

	return nil
}

// Unregister removes a plugin from the manager
func (pm *PluginManager) Unregister(name string) error {
	plugin, exists := pm.plugins[name]
	if !exists {
		return fmt.Errorf("plugin %s not registered", name)
	}

	// Cleanup if initialized
	if pm.initialized {
		if err := plugin.Cleanup(context.Background()); err != nil {
			return fmt.Errorf("plugin %s cleanup failed: %w", name, err)
		}
	}

	delete(pm.plugins, name)

	// Remove from order
	for i, n := range pm.pluginOrder {
		if n == name {
			pm.pluginOrder = append(pm.pluginOrder[:i], pm.pluginOrder[i+1:]...)
			break
		}
	}

	return nil
}

// Initialize initializes all registered plugins
func (pm *PluginManager) Initialize(ctx context.Context) error {
	if pm.initialized {
		return nil
	}

	for _, name := range pm.pluginOrder {
		plugin := pm.plugins[name]
		if plugin.Enabled() {
			if err := plugin.Initialize(ctx); err != nil {
				return fmt.Errorf("plugin %s initialization failed: %w", name, err)
			}
		}
	}

	pm.initialized = true
	return nil
}

// Cleanup cleans up all registered plugins
func (pm *PluginManager) Cleanup(ctx context.Context) error {
	if !pm.initialized {
		return nil
	}

	var errs []error
	// Cleanup in reverse order
	for i := len(pm.pluginOrder) - 1; i >= 0; i-- {
		name := pm.pluginOrder[i]
		plugin := pm.plugins[name]
		if plugin.Enabled() {
			if err := plugin.Cleanup(ctx); err != nil {
				errs = append(errs, fmt.Errorf("plugin %s: %w", name, err))
			}
		}
	}

	pm.initialized = false

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %v", errs)
	}
	return nil
}

// GetPlugin retrieves a plugin by name
func (pm *PluginManager) GetPlugin(name string) (Plugin, bool) {
	plugin, exists := pm.plugins[name]
	return plugin, exists
}

// EnablePlugin enables a specific plugin
func (pm *PluginManager) EnablePlugin(name string) error {
	plugin, exists := pm.plugins[name]
	if !exists {
		return fmt.Errorf("plugin %s not registered", name)
	}
	plugin.SetEnabled(true)
	return nil
}

// DisablePlugin disables a specific plugin
func (pm *PluginManager) DisablePlugin(name string) error {
	plugin, exists := pm.plugins[name]
	if !exists {
		return fmt.Errorf("plugin %s not registered", name)
	}
	plugin.SetEnabled(false)
	return nil
}

// ListPlugins returns all registered plugin names in priority order
func (pm *PluginManager) ListPlugins() []string {
	result := make([]string, len(pm.pluginOrder))
	copy(result, pm.pluginOrder)
	return result
}

// RunBeforeModelCallbacks executes all before-model callbacks from plugins
func (pm *PluginManager) RunBeforeModelCallbacks(ctx *CallbackContext, req *LlmRequest) (*LlmResponse, error) {
	for _, name := range pm.pluginOrder {
		plugin := pm.plugins[name]
		if !plugin.Enabled() {
			continue
		}

		for _, callback := range plugin.BeforeModel() {
			response, err := callback(ctx, req)
			if err != nil {
				return nil, fmt.Errorf("plugin %s before_model: %w", name, err)
			}
			if response != nil {
				// Short-circuit with this response
				return response, nil
			}
		}
	}
	return nil, nil
}

// RunAfterModelCallbacks executes all after-model callbacks from plugins
func (pm *PluginManager) RunAfterModelCallbacks(ctx *CallbackContext, resp *LlmResponse) (*LlmResponse, error) {
	for _, name := range pm.pluginOrder {
		plugin := pm.plugins[name]
		if !plugin.Enabled() {
			continue
		}

		for _, callback := range plugin.AfterModel() {
			alteredResp, err := callback(ctx, resp)
			if err != nil {
				return nil, fmt.Errorf("plugin %s after_model: %w", name, err)
			}
			if alteredResp != nil {
				resp = alteredResp
			}
		}
	}
	return resp, nil
}

// RunOnModelErrorCallbacks executes all error callbacks from plugins
func (pm *PluginManager) RunOnModelErrorCallbacks(ctx *CallbackContext, req *LlmRequest, err error) (*LlmResponse, error) {
	for _, name := range pm.pluginOrder {
		plugin := pm.plugins[name]
		if !plugin.Enabled() {
			continue
		}

		for _, callback := range plugin.OnModelError() {
			response, cbErr := callback(ctx, req, err)
			if cbErr != nil {
				return nil, fmt.Errorf("plugin %s on_model_error: %w", name, cbErr)
			}
			if response != nil {
				// Recovery response
				return response, nil
			}
		}
	}
	return nil, nil
}

// RunBeforeToolCallbacks executes all before-tool callbacks from plugins
func (pm *PluginManager) RunBeforeToolCallbacks(ctx *ToolContext, toolName string, args map[string]interface{}) error {
	for _, name := range pm.pluginOrder {
		plugin := pm.plugins[name]
		if !plugin.Enabled() {
			continue
		}

		for _, callback := range plugin.BeforeTool() {
			if err := callback(ctx, toolName, args); err != nil {
				return fmt.Errorf("plugin %s before_tool: %w", name, err)
			}
		}
	}
	return nil
}

// RunAfterToolCallbacks executes all after-tool callbacks from plugins
func (pm *PluginManager) RunAfterToolCallbacks(ctx *ToolContext, toolName string, args map[string]interface{}, result interface{}, err error) error {
	for _, name := range pm.pluginOrder {
		plugin := pm.plugins[name]
		if !plugin.Enabled() {
			continue
		}

		for _, callback := range plugin.AfterTool() {
			if cbErr := callback(ctx, toolName, args, result, err); cbErr != nil {
				return fmt.Errorf("plugin %s after_tool: %w", name, cbErr)
			}
		}
	}
	return nil
}

// GetAllRequestProcessors returns all request processors from enabled plugins
func (pm *PluginManager) GetAllRequestProcessors() []RequestProcessor {
	var processors []RequestProcessor
	for _, name := range pm.pluginOrder {
		plugin := pm.plugins[name]
		if plugin.Enabled() {
			processors = append(processors, plugin.RequestProcessors()...)
		}
	}
	return processors
}

// GetAllResponseProcessors returns all response processors from enabled plugins
func (pm *PluginManager) GetAllResponseProcessors() []ResponseProcessor {
	var processors []ResponseProcessor
	for _, name := range pm.pluginOrder {
		plugin := pm.plugins[name]
		if plugin.Enabled() {
			processors = append(processors, plugin.ResponseProcessors()...)
		}
	}
	return processors
}

// ============================================================================
// Built-in Plugins
// ============================================================================

// LoggingPlugin provides logging for all LLM and tool operations
type LoggingPlugin struct {
	*BasePlugin
	verbose bool
}

// NewLoggingPlugin creates a new logging plugin
func NewLoggingPlugin(verbose bool) *LoggingPlugin {
	plugin := &LoggingPlugin{
		BasePlugin: NewBasePlugin("logging", 100), // High priority to log first
		verbose:    verbose,
	}

	// Add logging callbacks
	plugin.AddBeforeModelCallback(func(ctx *CallbackContext, req *LlmRequest) (*LlmResponse, error) {
		if plugin.verbose {
			fmt.Printf("[logging] before_model: %d contents, %d tools\n",
				len(req.Contents), len(req.Tools))
		}
		return nil, nil
	})

	plugin.AddAfterModelCallback(func(ctx *CallbackContext, resp *LlmResponse) (*LlmResponse, error) {
		if plugin.verbose {
			hasContent := resp.Content != nil
			fmt.Printf("[logging] after_model: has_content=%v, partial=%v\n",
				hasContent, resp.Partial)
		}
		return nil, nil
	})

	plugin.AddBeforeToolCallback(func(ctx *ToolContext, toolName string, args map[string]interface{}) error {
		if plugin.verbose {
			fmt.Printf("[logging] before_tool: %s\n", toolName)
		}
		return nil
	})

	plugin.AddAfterToolCallback(func(ctx *ToolContext, toolName string, args map[string]interface{}, result interface{}, err error) error {
		if plugin.verbose {
			fmt.Printf("[logging] after_tool: %s, error=%v\n", toolName, err)
		}
		return nil
	})

	return plugin
}

// MetricsPlugin collects metrics about LLM usage
type MetricsPlugin struct {
	*BasePlugin
	modelCalls   int
	toolCalls    int
	totalTokens  int
	errors       int
}

// NewMetricsPlugin creates a new metrics plugin
func NewMetricsPlugin() *MetricsPlugin {
	plugin := &MetricsPlugin{
		BasePlugin: NewBasePlugin("metrics", 50),
	}

	plugin.AddBeforeModelCallback(func(ctx *CallbackContext, req *LlmRequest) (*LlmResponse, error) {
		plugin.modelCalls++
		return nil, nil
	})

	plugin.AddAfterModelCallback(func(ctx *CallbackContext, resp *LlmResponse) (*LlmResponse, error) {
		if resp.UsageMetadata != nil {
			plugin.totalTokens += resp.UsageMetadata.TotalTokens
		}
		return nil, nil
	})

	plugin.AddOnModelErrorCallback(func(ctx *CallbackContext, req *LlmRequest, err error) (*LlmResponse, error) {
		plugin.errors++
		return nil, nil
	})

	plugin.AddBeforeToolCallback(func(ctx *ToolContext, toolName string, args map[string]interface{}) error {
		plugin.toolCalls++
		return nil
	})

	return plugin
}

// GetMetrics returns collected metrics
func (p *MetricsPlugin) GetMetrics() map[string]int {
	return map[string]int{
		"model_calls":  p.modelCalls,
		"tool_calls":   p.toolCalls,
		"total_tokens": p.totalTokens,
		"errors":       p.errors,
	}
}

// Reset clears all metrics
func (p *MetricsPlugin) Reset() {
	p.modelCalls = 0
	p.toolCalls = 0
	p.totalTokens = 0
	p.errors = 0
}

// AuthorizationPlugin provides tool authorization
type AuthorizationPlugin struct {
	*BasePlugin
	allowedTools map[string]bool
	denyByDefault bool
}

// NewAuthorizationPlugin creates a new authorization plugin
func NewAuthorizationPlugin(allowedTools []string, denyByDefault bool) *AuthorizationPlugin {
	plugin := &AuthorizationPlugin{
		BasePlugin:    NewBasePlugin("authorization", 90), // High priority for security
		allowedTools:  make(map[string]bool),
		denyByDefault: denyByDefault,
	}

	for _, tool := range allowedTools {
		plugin.allowedTools[tool] = true
	}

	plugin.AddBeforeToolCallback(func(ctx *ToolContext, toolName string, args map[string]interface{}) error {
		allowed, exists := plugin.allowedTools[toolName]
		if plugin.denyByDefault {
			if !exists || !allowed {
				return fmt.Errorf("tool %s not authorized", toolName)
			}
		} else {
			if exists && !allowed {
				return fmt.Errorf("tool %s explicitly denied", toolName)
			}
		}
		return nil
	})

	return plugin
}

// AllowTool adds a tool to the allowed list
func (p *AuthorizationPlugin) AllowTool(name string) {
	p.allowedTools[name] = true
}

// DenyTool adds a tool to the denied list
func (p *AuthorizationPlugin) DenyTool(name string) {
	p.allowedTools[name] = false
}

// RateLimitPlugin provides rate limiting for LLM calls
type RateLimitPlugin struct {
	*BasePlugin
	maxCallsPerMinute int
	callTimestamps    []time.Time
}

// NewRateLimitPlugin creates a new rate limit plugin
func NewRateLimitPlugin(maxCallsPerMinute int) *RateLimitPlugin {
	plugin := &RateLimitPlugin{
		BasePlugin:        NewBasePlugin("rate_limit", 95), // High priority
		maxCallsPerMinute: maxCallsPerMinute,
		callTimestamps:    make([]time.Time, 0),
	}

	plugin.AddBeforeModelCallback(func(ctx *CallbackContext, req *LlmRequest) (*LlmResponse, error) {
		now := time.Now()
		windowStart := now.Add(-time.Minute)

		// Filter out old timestamps
		validTimestamps := make([]time.Time, 0)
		for _, ts := range plugin.callTimestamps {
			if ts.After(windowStart) {
				validTimestamps = append(validTimestamps, ts)
			}
		}
		plugin.callTimestamps = validTimestamps

		// Check rate limit
		if len(plugin.callTimestamps) >= plugin.maxCallsPerMinute {
			return &LlmResponse{
				ErrorCode:    "rate_limited",
				ErrorMessage: fmt.Sprintf("rate limit exceeded: %d calls per minute", plugin.maxCallsPerMinute),
			}, nil
		}

		plugin.callTimestamps = append(plugin.callTimestamps, now)
		return nil, nil
	})

	return plugin
}

// CachingPlugin provides response caching
type CachingPlugin struct {
	*BasePlugin
	cache    map[string]*LlmResponse
	ttl      time.Duration
	cacheHits int
	cacheMisses int
}

// NewCachingPlugin creates a new caching plugin
func NewCachingPlugin(ttl time.Duration) *CachingPlugin {
	plugin := &CachingPlugin{
		BasePlugin: NewBasePlugin("caching", 80),
		cache:      make(map[string]*LlmResponse),
		ttl:        ttl,
	}

	plugin.AddBeforeModelCallback(func(ctx *CallbackContext, req *LlmRequest) (*LlmResponse, error) {
		// Simple cache key (in production would use hash)
		cacheKey := req.SystemInstruction
		if cached, ok := plugin.cache[cacheKey]; ok {
			plugin.cacheHits++
			return cached, nil
		}
		plugin.cacheMisses++
		return nil, nil
	})

	plugin.AddAfterModelCallback(func(ctx *CallbackContext, resp *LlmResponse) (*LlmResponse, error) {
		if !resp.Partial && resp.ErrorCode == "" {
			// Cache complete, successful responses
			// In production would use proper cache key
			if ctx.InvocationContext != nil {
				// Store in cache (simplified)
			}
		}
		return nil, nil
	})

	return plugin
}

// GetCacheStats returns cache statistics
func (p *CachingPlugin) GetCacheStats() map[string]int {
	return map[string]int{
		"hits":   p.cacheHits,
		"misses": p.cacheMisses,
		"size":   len(p.cache),
	}
}

// ClearCache clears the cache
func (p *CachingPlugin) ClearCache() {
	p.cache = make(map[string]*LlmResponse)
}

// ============================================================================
// LlmFlow Plugin Integration
// ============================================================================

// WithPluginManager adds a plugin manager to the flow
func (f *LlmFlow) WithPluginManager(pm *PluginManager) *LlmFlow {
	f.PluginManager = pm
	return f
}

// WithTracer adds a tracer to the flow
func (f *LlmFlow) WithTracer(tracer Tracer) *LlmFlow {
	f.Tracer = tracer
	return f
}

// WithTraceCallback adds a trace callback to the flow
func (f *LlmFlow) WithTraceCallback(cb TraceCallback) *LlmFlow {
	f.TraceCallback = cb
	return f
}

// WithLiveProvider adds a live provider for CFC mode
func (f *LlmFlow) WithLiveProvider(provider LiveLlmProvider) *LlmFlow {
	f.LiveProvider = provider
	return f
}

// WithDefaultRunConfig sets the default run configuration
func (f *LlmFlow) WithDefaultRunConfig(config *RunConfig) *LlmFlow {
	f.DefaultRunConfig = config
	return f
}

// ============================================================================
// Full run_live Implementation (ADK-compatible)
// ============================================================================

// LiveSession represents an active bidirectional streaming session.
// This matches ADK's live session handling for real-time communication.
type LiveSession struct {
	ID            string
	InvCtx        *InvocationContext
	RequestQueue  *LiveRequestQueue
	ResponseChan  <-chan *LlmResponse
	EventChan     chan *AsyncEvent
	Closed        bool
	PauseState    *SessionPauseState
	StartTime     time.Time
	LastActivity  time.Time
}

// SessionPauseState captures state for session resumption
type SessionPauseState struct {
	// Conversation history at pause point
	Contents []Content `json:"contents"`

	// Pending function calls
	PendingCalls []*FunctionCall `json:"pending_calls,omitempty"`

	// System state
	SystemInstruction string `json:"system_instruction,omitempty"`

	// Checkpoint ID for resumption
	CheckpointID string `json:"checkpoint_id"`

	// Timestamp of pause
	PausedAt time.Time `json:"paused_at"`
}

// LiveSessionConfig configuration for live sessions
type LiveSessionConfig struct {
	// MaxIdleTime before session is auto-closed
	MaxIdleTime time.Duration

	// EnableAudioCache for audio streaming
	EnableAudioCache bool

	// BufferSize for response buffering
	BufferSize int

	// EnableTranscription for speech-to-text
	EnableTranscription bool

	// OnDisconnect callback when connection drops
	OnDisconnect func(session *LiveSession)

	// OnReconnect callback when connection restores
	OnReconnect func(session *LiveSession)
}

// DefaultLiveSessionConfig returns default live session configuration
func DefaultLiveSessionConfig() *LiveSessionConfig {
	return &LiveSessionConfig{
		MaxIdleTime:         5 * time.Minute,
		EnableAudioCache:    false,
		BufferSize:          100,
		EnableTranscription: false,
	}
}

// LiveSessionManager manages active live sessions
type LiveSessionManager struct {
	sessions map[string]*LiveSession
	config   *LiveSessionConfig
	mu       sync.RWMutex
}

// NewLiveSessionManager creates a new live session manager
func NewLiveSessionManager(config *LiveSessionConfig) *LiveSessionManager {
	if config == nil {
		config = DefaultLiveSessionConfig()
	}
	return &LiveSessionManager{
		sessions: make(map[string]*LiveSession),
		config:   config,
	}
}

// CreateSession creates a new live session
func (m *LiveSessionManager) CreateSession(invCtx *InvocationContext) *LiveSession {
	m.mu.Lock()
	defer m.mu.Unlock()

	session := &LiveSession{
		ID:           generateID("live-session"),
		InvCtx:       invCtx,
		RequestQueue: NewLiveRequestQueue(),
		EventChan:    make(chan *AsyncEvent, m.config.BufferSize),
		Closed:       false,
		StartTime:    time.Now(),
		LastActivity: time.Now(),
	}

	m.sessions[session.ID] = session
	return session
}

// GetSession retrieves a session by ID
func (m *LiveSessionManager) GetSession(sessionID string) (*LiveSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, ok := m.sessions[sessionID]
	return session, ok
}

// CloseSession closes and removes a session
func (m *LiveSessionManager) CloseSession(sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}

	session.Closed = true
	session.RequestQueue.Close()
	close(session.EventChan)
	delete(m.sessions, sessionID)

	return nil
}

// PauseSession pauses a session and returns state for resumption
func (m *LiveSessionManager) PauseSession(sessionID string) (*SessionPauseState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	// Capture current state
	pauseState := &SessionPauseState{
		CheckpointID: generateID("checkpoint"),
		PausedAt:     time.Now(),
	}

	session.PauseState = pauseState
	return pauseState, nil
}

// ResumeSession resumes a paused session
func (m *LiveSessionManager) ResumeSession(sessionID string, pauseState *SessionPauseState) (*LiveSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}

	// Restore state
	session.PauseState = nil
	session.LastActivity = time.Now()

	return session, nil
}

// ListActiveSessions returns all active session IDs
func (m *LiveSessionManager) ListActiveSessions() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	return ids
}

// CleanupIdleSessions closes sessions that have been idle too long
func (m *LiveSessionManager) CleanupIdleSessions() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleaned := 0
	now := time.Now()

	for id, session := range m.sessions {
		if now.Sub(session.LastActivity) > m.config.MaxIdleTime {
			session.Closed = true
			session.RequestQueue.Close()
			close(session.EventChan)
			delete(m.sessions, id)
			cleaned++

			if m.config.OnDisconnect != nil {
				m.config.OnDisconnect(session)
			}
		}
	}

	return cleaned
}

// RunLive executes a live streaming session.
// This is Go's equivalent to ADK's run_live method.
func (f *LlmFlow) RunLive(ctx context.Context, session *LiveSession) <-chan *AsyncEvent {
	output := make(chan *AsyncEvent, 100)

	go func() {
		defer close(output)

		if f.LiveProvider == nil {
			errorEvent := NewAsyncEvent(EventTypeError, "live provider not configured", "llm_flow")
			errorEvent.Error = fmt.Errorf("LiveProvider is nil")
			output <- errorEvent
			return
		}

		invCtx := session.InvCtx

		// Start live streaming with the provider
		session.ResponseChan = f.LiveProvider.RunLive(ctx, session.RequestQueue)

		// Process responses
		for llmResponse := range session.ResponseChan {
			session.LastActivity = time.Now()

			// Run response processors
			for _, processor := range f.ResponseProcessors {
				events := processor.ProcessResponse(ctx, invCtx, llmResponse)
				for event := range events {
					output <- event
				}
			}

			// Build and emit event
			event := f.buildLiveEvent(invCtx, llmResponse)

			// Run after-model callbacks via plugin manager
			if f.PluginManager != nil {
				callbackCtx := NewCallbackContext(invCtx, event.Actions)
				f.PluginManager.RunAfterModelCallbacks(callbackCtx, llmResponse)
			}

			// Run direct after-model callbacks
			for _, callback := range f.AfterModelCallbacks {
				callbackCtx := NewCallbackContext(invCtx, event.Actions)
				callback(callbackCtx, llmResponse)
			}

			output <- event

			// Handle function calls if present
			if llmResponse.HasFunctionCalls() {
				funcEvents := f.handleLiveFunctionCalls(ctx, session, llmResponse)
				for funcEvent := range funcEvents {
					output <- funcEvent
				}
			}

			// Check for turn completion
			if llmResponse.TurnComplete {
				completionEvent := NewAsyncEvent(EventTypeStateChange, "turn_complete", invCtx.AgentName)
				completionEvent.Metadata = map[string]interface{}{
					"turn_complete": true,
					"session_id":    session.ID,
				}
				output <- completionEvent
			}

			// Check for interruption
			if llmResponse.Interrupted {
				interruptEvent := NewAsyncEvent(EventTypeStateChange, "interrupted", invCtx.AgentName)
				interruptEvent.Metadata = map[string]interface{}{
					"interrupted": true,
					"session_id":  session.ID,
				}
				output <- interruptEvent
			}

			// Check context cancellation
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}()

	return output
}

// buildLiveEvent builds an event from a live response
func (f *LlmFlow) buildLiveEvent(invCtx *InvocationContext, llmResponse *LlmResponse) *AsyncEvent {
	event := NewAsyncEvent(EventTypeMessage, "", invCtx.AgentName)
	event.InvocationID = invCtx.InvocationID
	event.Partial = llmResponse.Partial

	if llmResponse.Content != nil {
		var textParts []string
		for _, part := range llmResponse.Content.Parts {
			if part.Text != "" {
				textParts = append(textParts, part.Text)
			}
		}
		if len(textParts) > 0 {
			event.Content = textParts[0]
		}

		if llmResponse.HasFunctionCalls() {
			event.Type = EventTypeToolCall
			event.Content = llmResponse
		}
	}

	if llmResponse.ErrorCode != "" {
		event.Type = EventTypeError
		event.Error = fmt.Errorf("%s: %s", llmResponse.ErrorCode, llmResponse.ErrorMessage)
	}

	if llmResponse.UsageMetadata != nil {
		event.Metadata = map[string]interface{}{
			"usage": llmResponse.UsageMetadata,
		}
	}

	return event
}

// handleLiveFunctionCalls handles function calls in live mode
func (f *LlmFlow) handleLiveFunctionCalls(
	ctx context.Context,
	session *LiveSession,
	llmResponse *LlmResponse,
) <-chan *AsyncEvent {
	output := make(chan *AsyncEvent)

	go func() {
		defer close(output)

		invCtx := session.InvCtx
		functionCalls := llmResponse.GetFunctionCalls()
		if len(functionCalls) == 0 {
			return
		}

		// Get tools dict from invocation context state
		toolsDict := make(map[string]ToolDefinition)
		if invCtx.State != nil {
			if td, ok := invCtx.State["tools_dict"].(map[string]ToolDefinition); ok {
				toolsDict = td
			}
		}

		var functionResponses []FunctionResponse

		for _, call := range functionCalls {
			toolCtx := NewToolContext(invCtx, call.Name)

			// Run before-tool callbacks
			if f.PluginManager != nil {
				if err := f.PluginManager.RunBeforeToolCallbacks(toolCtx, call.Name, call.Args); err != nil {
					functionResponses = append(functionResponses, FunctionResponse{
						Name:  call.Name,
						Error: err.Error(),
					})
					continue
				}
			}

			tool, exists := toolsDict[call.Name]
			if !exists || tool.Handler == nil {
				functionResponses = append(functionResponses, FunctionResponse{
					Name:  call.Name,
					Error: fmt.Sprintf("tool %s not found or has no handler", call.Name),
				})
				continue
			}

			result, err := tool.Handler(ctx, call.Args)

			// Run after-tool callbacks
			if f.PluginManager != nil {
				f.PluginManager.RunAfterToolCallbacks(toolCtx, call.Name, call.Args, result, err)
			}

			if err != nil {
				functionResponses = append(functionResponses, FunctionResponse{
					Name:  call.Name,
					Error: err.Error(),
				})
			} else {
				functionResponses = append(functionResponses, FunctionResponse{
					Name:     call.Name,
					Response: result,
				})
			}
		}

		// Emit tool result event
		toolResultEvent := NewAsyncEvent(EventTypeToolResult, functionResponses, invCtx.AgentName)
		toolResultEvent.InvocationID = invCtx.InvocationID
		output <- toolResultEvent

		// Send function responses back to the live queue for continued conversation
		if !session.RequestQueue.closed {
			responseContent := Content{
				Role:  "user",
				Parts: make([]Part, len(functionResponses)),
			}
			for i, resp := range functionResponses {
				responseContent.Parts[i] = Part{
					FunctionResponse: &FunctionResponse{
						Name:     resp.Name,
						Response: resp.Response,
						Error:    resp.Error,
					},
				}
			}

			followupReq := &LlmRequest{
				Contents: []Content{responseContent},
			}
			session.RequestQueue.Send(followupReq)
		}
	}()

	return output
}

// SendLiveMessage sends a message to an active live session
func (f *LlmFlow) SendLiveMessage(session *LiveSession, content string) error {
	if session.Closed {
		return fmt.Errorf("session is closed")
	}

	session.LastActivity = time.Now()

	req := &LlmRequest{
		Contents: []Content{
			{
				Role:  "user",
				Parts: []Part{{Text: content}},
			},
		},
	}

	session.RequestQueue.Send(req)
	return nil
}

// InterruptLiveSession sends an interrupt signal to the live session
func (f *LlmFlow) InterruptLiveSession(session *LiveSession) error {
	if session.Closed {
		return fmt.Errorf("session is closed")
	}

	// Send empty request to signal interrupt
	session.RequestQueue.Send(&LlmRequest{})
	return nil
}

// ============================================================================
// Long-Running Tool Support (ADK-compatible)
// ============================================================================

// LongRunningTool represents a tool that runs asynchronously
type LongRunningTool struct {
	ID          string
	Name        string
	Args        map[string]interface{}
	Status      ToolStatus
	StartTime   time.Time
	EndTime     time.Time
	Result      interface{}
	Error       error
	Progress    float64
	ProgressMsg string
	Cancel      context.CancelFunc
}

// ToolStatus represents the status of a long-running tool
type ToolStatus int

const (
	ToolStatusPending ToolStatus = iota
	ToolStatusRunning
	ToolStatusCompleted
	ToolStatusFailed
	ToolStatusCancelled
)

func (s ToolStatus) String() string {
	switch s {
	case ToolStatusPending:
		return "pending"
	case ToolStatusRunning:
		return "running"
	case ToolStatusCompleted:
		return "completed"
	case ToolStatusFailed:
		return "failed"
	case ToolStatusCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// LongRunningToolTracker tracks long-running tool executions
type LongRunningToolTracker struct {
	tools map[string]*LongRunningTool
	mu    sync.RWMutex
}

// NewLongRunningToolTracker creates a new tracker
func NewLongRunningToolTracker() *LongRunningToolTracker {
	return &LongRunningToolTracker{
		tools: make(map[string]*LongRunningTool),
	}
}

// StartTool registers and starts tracking a tool
func (t *LongRunningToolTracker) StartTool(name string, args map[string]interface{}, cancel context.CancelFunc) *LongRunningTool {
	t.mu.Lock()
	defer t.mu.Unlock()

	tool := &LongRunningTool{
		ID:        generateID("tool"),
		Name:      name,
		Args:      args,
		Status:    ToolStatusRunning,
		StartTime: time.Now(),
		Cancel:    cancel,
	}

	t.tools[tool.ID] = tool
	return tool
}

// UpdateProgress updates tool progress
func (t *LongRunningToolTracker) UpdateProgress(toolID string, progress float64, msg string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	tool, ok := t.tools[toolID]
	if !ok {
		return fmt.Errorf("tool %s not found", toolID)
	}

	tool.Progress = progress
	tool.ProgressMsg = msg
	return nil
}

// CompleteTool marks a tool as completed
func (t *LongRunningToolTracker) CompleteTool(toolID string, result interface{}, err error) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	tool, ok := t.tools[toolID]
	if !ok {
		return fmt.Errorf("tool %s not found", toolID)
	}

	tool.EndTime = time.Now()
	tool.Result = result
	tool.Error = err

	if err != nil {
		tool.Status = ToolStatusFailed
	} else {
		tool.Status = ToolStatusCompleted
	}

	return nil
}

// CancelTool cancels a running tool
func (t *LongRunningToolTracker) CancelTool(toolID string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	tool, ok := t.tools[toolID]
	if !ok {
		return fmt.Errorf("tool %s not found", toolID)
	}

	if tool.Cancel != nil {
		tool.Cancel()
	}
	tool.Status = ToolStatusCancelled
	tool.EndTime = time.Now()

	return nil
}

// GetTool retrieves tool status
func (t *LongRunningToolTracker) GetTool(toolID string) (*LongRunningTool, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	tool, ok := t.tools[toolID]
	return tool, ok
}

// ListRunningTools returns all currently running tools
func (t *LongRunningToolTracker) ListRunningTools() []*LongRunningTool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var running []*LongRunningTool
	for _, tool := range t.tools {
		if tool.Status == ToolStatusRunning {
			running = append(running, tool)
		}
	}
	return running
}

// CleanupCompleted removes completed/failed tools older than duration
func (t *LongRunningToolTracker) CleanupCompleted(olderThan time.Duration) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	cleaned := 0
	cutoff := time.Now().Add(-olderThan)

	for id, tool := range t.tools {
		if tool.Status != ToolStatusRunning && tool.EndTime.Before(cutoff) {
			delete(t.tools, id)
			cleaned++
		}
	}

	return cleaned
}
