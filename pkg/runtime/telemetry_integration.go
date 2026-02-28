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

// Package runtime provides integrated telemetry for LLM flows.
//
// This file implements OpenTelemetry-compatible tracing similar to Google's ADK Go,
// with support for:
// - LLM call tracing with token usage
// - Tool call tracing with execution time
// - Agent execution tracing
// - Spark partition tracking
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ============================================================================
// Telemetry Types
// ============================================================================

// TelemetrySpan represents a trace span with timing and attributes
type TelemetrySpan struct {
	SpanID       string                 `json:"span_id"`
	TraceID      string                 `json:"trace_id"`
	ParentSpanID string                 `json:"parent_span_id,omitempty"`
	Name         string                 `json:"name"`
	StartTime    time.Time              `json:"start_time"`
	EndTime      time.Time              `json:"end_time,omitempty"`
	Duration     time.Duration          `json:"duration,omitempty"`
	Status       SpanStatus             `json:"status"`
	Attributes   map[string]interface{} `json:"attributes,omitempty"`
	Events       []SpanEvent            `json:"events,omitempty"`
	mu           sync.Mutex
}

// SpanStatus represents the status of a span
type SpanStatus string

const (
	SpanStatusUnset SpanStatus = ""
	SpanStatusOK    SpanStatus = "OK"
	SpanStatusError SpanStatus = "ERROR"
)

// SpanEvent represents an event within a span
type SpanEvent struct {
	Name       string                 `json:"name"`
	Timestamp  time.Time              `json:"timestamp"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
}

// End ends the span and calculates duration
func (s *TelemetrySpan) End() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.EndTime = time.Now()
	s.Duration = s.EndTime.Sub(s.StartTime)
}

// SetAttribute sets an attribute on the span
func (s *TelemetrySpan) SetAttribute(key string, value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Attributes == nil {
		s.Attributes = make(map[string]interface{})
	}
	s.Attributes[key] = value
}

// SetStatus sets the span status
func (s *TelemetrySpan) SetStatus(status SpanStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Status = status
}

// AddEvent adds an event to the span
func (s *TelemetrySpan) AddEvent(name string, attrs map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Events = append(s.Events, SpanEvent{
		Name:       name,
		Timestamp:  time.Now(),
		Attributes: attrs,
	})
}

// ============================================================================
// Telemetry Provider
// ============================================================================

// TelemetryProvider manages trace collection and export
type TelemetryProvider struct {
	serviceName string
	spans       []*TelemetrySpan
	exporters   []SpanExporter
	mu          sync.RWMutex
}

// SpanExporter exports spans to a backend
type SpanExporter interface {
	Export(spans []*TelemetrySpan) error
	Shutdown() error
}

// NewTelemetryProvider creates a new telemetry provider
func NewTelemetryProvider(serviceName string) *TelemetryProvider {
	return &TelemetryProvider{
		serviceName: serviceName,
		spans:       make([]*TelemetrySpan, 0),
		exporters:   make([]SpanExporter, 0),
	}
}

// AddExporter adds a span exporter
func (p *TelemetryProvider) AddExporter(exporter SpanExporter) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.exporters = append(p.exporters, exporter)
}

// StartSpan starts a new span
func (p *TelemetryProvider) StartSpan(ctx context.Context, name string) (context.Context, *TelemetrySpan) {
	span := &TelemetrySpan{
		SpanID:     generateID("span"),
		TraceID:    getTraceIDFromContext(ctx),
		Name:       name,
		StartTime:  time.Now(),
		Status:     SpanStatusUnset,
		Attributes: make(map[string]interface{}),
	}

	// Get parent span ID if exists
	if parentSpan := getSpanFromContext(ctx); parentSpan != nil {
		span.ParentSpanID = parentSpan.SpanID
	}

	// Create new trace ID if none exists
	if span.TraceID == "" {
		span.TraceID = generateID("trace")
	}

	// Add service name attribute
	span.SetAttribute("service.name", p.serviceName)

	// Store span for later export
	p.mu.Lock()
	p.spans = append(p.spans, span)
	p.mu.Unlock()

	// Add span to context
	ctx = context.WithValue(ctx, spanContextKey, span)
	ctx = context.WithValue(ctx, traceIDContextKey, span.TraceID)

	return ctx, span
}

// Flush exports all collected spans
func (p *TelemetryProvider) Flush() error {
	p.mu.Lock()
	spans := p.spans
	p.spans = make([]*TelemetrySpan, 0)
	exporters := p.exporters
	p.mu.Unlock()

	for _, exporter := range exporters {
		if err := exporter.Export(spans); err != nil {
			return fmt.Errorf("exporter error: %w", err)
		}
	}

	return nil
}

// Shutdown shuts down all exporters
func (p *TelemetryProvider) Shutdown() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, exporter := range p.exporters {
		if err := exporter.Shutdown(); err != nil {
			return err
		}
	}
	return nil
}

// Context keys
type contextKey string

const (
	spanContextKey    contextKey = "telemetry_span"
	traceIDContextKey contextKey = "telemetry_trace_id"
)

func getSpanFromContext(ctx context.Context) *TelemetrySpan {
	if span, ok := ctx.Value(spanContextKey).(*TelemetrySpan); ok {
		return span
	}
	return nil
}

func getTraceIDFromContext(ctx context.Context) string {
	if traceID, ok := ctx.Value(traceIDContextKey).(string); ok {
		return traceID
	}
	return ""
}

// ============================================================================
// Trace Functions (similar to Google ADK Go telemetry package)
// ============================================================================

// StartTrace starts a new trace span. If a provider is configured in context,
// it uses that; otherwise returns a no-op span.
func StartTrace(ctx context.Context, name string) (context.Context, *TelemetrySpan) {
	if provider := getTelemetryProvider(ctx); provider != nil {
		return provider.StartSpan(ctx, name)
	}
	// Return no-op span
	noopSpan := &TelemetrySpan{
		SpanID:     "noop",
		Name:       name,
		StartTime:  time.Now(),
		Attributes: make(map[string]interface{}),
	}
	return ctx, noopSpan
}

// TraceLLMCall adds LLM call details to a span
func TraceLLMCall(span *TelemetrySpan, invCtx *InvocationContext, req *LlmRequest, event *AsyncEvent) {
	if span == nil || span.SpanID == "noop" {
		return
	}

	span.SetAttribute("llm.agent_name", invCtx.AgentName)
	span.SetAttribute("llm.session_id", invCtx.SessionID)
	span.SetAttribute("llm.invocation_id", invCtx.InvocationID)

	// Request details
	if req != nil {
		span.SetAttribute("llm.request.contents_count", len(req.Contents))
		span.SetAttribute("llm.request.tools_count", len(req.Tools))
		if req.SystemInstruction != "" {
			span.SetAttribute("llm.request.has_system_instruction", true)
		}
		if req.Config != nil {
			span.SetAttribute("llm.request.temperature", req.Config.Temperature)
			span.SetAttribute("llm.request.max_tokens", req.Config.MaxOutputTokens)
		}
	}

	// Response details
	if event != nil {
		span.SetAttribute("llm.response.type", event.Type.String())
		span.SetAttribute("llm.response.partial", event.Partial)
		if event.Error != nil {
			span.SetStatus(SpanStatusError)
			span.SetAttribute("llm.error", event.Error.Error())
		} else {
			span.SetStatus(SpanStatusOK)
		}
	}

	// Spark partition info
	if invCtx.PartitionID != "" {
		span.SetAttribute("spark.partition_id", invCtx.PartitionID)
	}
}

// TraceToolCall adds tool call details to a span
func TraceToolCall(span *TelemetrySpan, toolName string, args map[string]interface{}, result interface{}, err error) {
	if span == nil || span.SpanID == "noop" {
		return
	}

	span.SetAttribute("tool.name", toolName)

	// Args (be careful about size)
	if args != nil {
		argsJSON, _ := json.Marshal(args)
		if len(argsJSON) < 1024 {
			span.SetAttribute("tool.args", string(argsJSON))
		} else {
			span.SetAttribute("tool.args_size", len(argsJSON))
		}
	}

	// Result
	if err != nil {
		span.SetStatus(SpanStatusError)
		span.SetAttribute("tool.error", err.Error())
	} else {
		span.SetStatus(SpanStatusOK)
		if result != nil {
			resultJSON, _ := json.Marshal(result)
			if len(resultJSON) < 1024 {
				span.SetAttribute("tool.result", string(resultJSON))
			} else {
				span.SetAttribute("tool.result_size", len(resultJSON))
			}
		}
	}
}

// TraceMergedToolCalls adds merged tool call details
func TraceMergedToolCalls(span *TelemetrySpan, event *AsyncEvent) {
	if span == nil || span.SpanID == "noop" || event == nil {
		return
	}

	span.SetAttribute("tool.merged", true)
	if event.Metadata != nil {
		if count, ok := event.Metadata["merged_count"].(int); ok {
			span.SetAttribute("tool.merged_count", count)
		}
	}
}

// TraceAgentExecution adds agent execution details
func TraceAgentExecution(span *TelemetrySpan, agentName string, stepCount int, eventCount int) {
	if span == nil || span.SpanID == "noop" {
		return
	}

	span.SetAttribute("agent.name", agentName)
	span.SetAttribute("agent.steps", stepCount)
	span.SetAttribute("agent.events", eventCount)
}

// TraceTokenUsage adds token usage details
func TraceTokenUsage(span *TelemetrySpan, promptTokens, completionTokens, totalTokens int) {
	if span == nil || span.SpanID == "noop" {
		return
	}

	span.SetAttribute("llm.tokens.prompt", promptTokens)
	span.SetAttribute("llm.tokens.completion", completionTokens)
	span.SetAttribute("llm.tokens.total", totalTokens)
}

// ============================================================================
// Console Exporter (for debugging)
// ============================================================================

// ConsoleExporter exports spans to the console
type ConsoleExporter struct {
	pretty bool
}

// NewConsoleExporter creates a new console exporter
func NewConsoleExporter(pretty bool) *ConsoleExporter {
	return &ConsoleExporter{pretty: pretty}
}

func (e *ConsoleExporter) Export(spans []*TelemetrySpan) error {
	for _, span := range spans {
		var data []byte
		var err error
		if e.pretty {
			data, err = json.MarshalIndent(span, "", "  ")
		} else {
			data, err = json.Marshal(span)
		}
		if err != nil {
			return err
		}
		fmt.Printf("[TRACE] %s\n", string(data))
	}
	return nil
}

func (e *ConsoleExporter) Shutdown() error {
	return nil
}

// ============================================================================
// Memory Exporter (for testing)
// ============================================================================

// MemoryExporter stores spans in memory for testing
type MemoryExporter struct {
	spans []*TelemetrySpan
	mu    sync.RWMutex
}

// NewMemoryExporter creates a new memory exporter
func NewMemoryExporter() *MemoryExporter {
	return &MemoryExporter{
		spans: make([]*TelemetrySpan, 0),
	}
}

func (e *MemoryExporter) Export(spans []*TelemetrySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = append(e.spans, spans...)
	return nil
}

func (e *MemoryExporter) Shutdown() error {
	return nil
}

// GetSpans returns all collected spans
func (e *MemoryExporter) GetSpans() []*TelemetrySpan {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]*TelemetrySpan, len(e.spans))
	copy(result, e.spans)
	return result
}

// Clear clears all collected spans
func (e *MemoryExporter) Clear() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = make([]*TelemetrySpan, 0)
}

// FindSpansByName finds spans with the given name
func (e *MemoryExporter) FindSpansByName(name string) []*TelemetrySpan {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var result []*TelemetrySpan
	for _, span := range e.spans {
		if span.Name == name {
			result = append(result, span)
		}
	}
	return result
}

// ============================================================================
// Context Helpers
// ============================================================================

type telemetryProviderKey struct{}

// WithTelemetryProvider adds a telemetry provider to the context
func WithTelemetryProvider(ctx context.Context, provider *TelemetryProvider) context.Context {
	return context.WithValue(ctx, telemetryProviderKey{}, provider)
}

func getTelemetryProvider(ctx context.Context) *TelemetryProvider {
	if provider, ok := ctx.Value(telemetryProviderKey{}).(*TelemetryProvider); ok {
		return provider
	}
	return nil
}

// ============================================================================
// Instrumented LLM Flow
// ============================================================================

// InstrumentedLlmFlow wraps an LlmFlow with telemetry
type InstrumentedLlmFlow struct {
	flow     *LlmFlow
	provider *TelemetryProvider
}

// NewInstrumentedLlmFlow creates an instrumented LLM flow
func NewInstrumentedLlmFlow(flow *LlmFlow, provider *TelemetryProvider) *InstrumentedLlmFlow {
	return &InstrumentedLlmFlow{
		flow:     flow,
		provider: provider,
	}
}

// RunAsync runs the flow with telemetry instrumentation
func (f *InstrumentedLlmFlow) RunAsync(ctx context.Context, invCtx *InvocationContext) <-chan *AsyncEvent {
	output := make(chan *AsyncEvent)

	go func() {
		defer close(output)

		// Add provider to context
		ctx = WithTelemetryProvider(ctx, f.provider)

		// Start flow span
		ctx, flowSpan := f.provider.StartSpan(ctx, "llm_flow.run")
		flowSpan.SetAttribute("agent.name", invCtx.AgentName)
		flowSpan.SetAttribute("session.id", invCtx.SessionID)
		defer func() {
			flowSpan.End()
			f.provider.Flush()
		}()

		stepCount := 0
		eventCount := 0

		// Run the underlying flow
		for event := range f.flow.RunAsync(ctx, invCtx) {
			eventCount++

			if event.Metadata != nil {
				if step, ok := event.Metadata["step"].(int); ok {
					stepCount = step
				}
			}

			output <- event
		}

		// Add execution stats
		TraceAgentExecution(flowSpan, invCtx.AgentName, stepCount, eventCount)
	}()

	return output
}
