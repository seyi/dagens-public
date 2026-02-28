// Package telemetry provides enhanced observability and monitoring
// using OpenTelemetry SDK for distributed tracing with Jaeger support via OTLP
package telemetry

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
)

// NoOpExporter is a no-op implementation of SpanExporter
type NoOpExporter struct{}

// ExportSpans implements the SpanExporter interface
func (e *NoOpExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	return nil
}

// Shutdown implements the SpanExporter interface
func (e *NoOpExporter) Shutdown(ctx context.Context) error {
	return nil
}

// OTELTracer implements the Tracer interface using OpenTelemetry SDK
type OTELTracer struct {
	tracer trace.Tracer
	tp     *sdktrace.TracerProvider
	config TracerConfig
}

// TracerConfig holds configuration for the OTEL tracer
type TracerConfig struct {
	ServiceName        string
	ServiceVersion     string
	Endpoint           string
	ExporterType       ExporterType
	SampleRatio        float64
	Insecure           bool
	Headers            map[string]string
	Timeout            time.Duration
}

// ExporterType defines the type of exporter to use
type ExporterType string

const (
	ExporterOTLP     ExporterType = "otlp"
	ExporterConsole  ExporterType = "console"
	ExporterNoOp     ExporterType = "noop"
)

// NewOTELTracer creates a new OTEL tracer with the given configuration
func NewOTELTracer(config TracerConfig) (*OTELTracer, error) {
	// Set up resource with service information
	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String(config.ServiceName),
		semconv.ServiceVersionKey.String(config.ServiceVersion),
	)

	var exporter sdktrace.SpanExporter
	var err error

	switch config.ExporterType {
	case ExporterOTLP:
		// Strip http:// or https:// prefix if present - OTLP gRPC expects just host:port
		endpoint := config.Endpoint
		if len(endpoint) > 7 && endpoint[:7] == "http://" {
			endpoint = endpoint[7:]
		} else if len(endpoint) > 8 && endpoint[:8] == "https://" {
			endpoint = endpoint[8:]
		}
		fmt.Printf("[OTEL] Initializing OTLP gRPC exporter with endpoint: %s\n", endpoint)
		exporter, err = otlptracegrpc.New(
			context.Background(),
			otlptracegrpc.WithEndpoint(endpoint),
			otlptracegrpc.WithInsecure(), // In production, use WithTLSCredentials for secure connections
		)
	case ExporterConsole:
		exporter, err = stdouttrace.New(stdouttrace.WithWriter(os.Stdout))
	case ExporterNoOp:
		exporter = &NoOpExporter{}
	default:
		exporter, err = stdouttrace.New(stdouttrace.WithWriter(os.Stdout))
	}

	if err != nil {
		return nil, err
	}

	// Create span processor
	spanProcessor := sdktrace.NewBatchSpanProcessor(exporter)

	// Create tracer provider with sampling
	// Default to AlwaysSample if SampleRatio is not explicitly set (0 means "not set")
	var sampler sdktrace.Sampler
	if config.SampleRatio >= 1.0 || config.SampleRatio == 0 {
		// Default to always sample - production systems should set an explicit ratio
		sampler = sdktrace.AlwaysSample()
	} else if config.SampleRatio < 0 {
		sampler = sdktrace.NeverSample()
	} else {
		sampler = sdktrace.ParentBased(sdktrace.TraceIDRatioBased(config.SampleRatio))
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sampler),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(spanProcessor),
	)

	// Set the global trace provider
	otel.SetTracerProvider(tp)

	// Set the global propagator
	otel.SetTextMapPropagator(propagation.TraceContext{})

	otelTracer := tp.Tracer(config.ServiceName)

	return &OTELTracer{
		tracer: otelTracer,
		tp:     tp,
		config: config,
	}, nil
}

// StartSpan starts a new span and returns the updated context
func (t *OTELTracer) StartSpan(ctx context.Context, name string) (context.Context, Span) {
	ctx, span := t.tracer.Start(ctx, name)
	return ctx, &OTELSpan{span: span}
}

// GetSpan retrieves the current span from context
func (t *OTELTracer) GetSpan(ctx context.Context) Span {
	span := trace.SpanFromContext(ctx)
	if span == nil || !span.SpanContext().IsValid() {
		return nil
	}
	return &OTELSpan{span: span}
}

// Shutdown gracefully shuts down the tracer provider
func (t *OTELTracer) Shutdown(ctx context.Context) error {
	return t.tp.Shutdown(ctx)
}

// ForceFlush flushes all remaining spans
func (t *OTELTracer) ForceFlush(ctx context.Context) error {
	fmt.Println("[OTEL] Force flushing spans...")
	return t.tp.ForceFlush(ctx)
}

// OTELSpan wraps the OTEL span to implement our Span interface
type OTELSpan struct {
	span trace.Span
	mu   sync.RWMutex // Not strictly needed for OTEL spans, but for interface compatibility
}

// SetAttribute sets an attribute on the span
func (s *OTELSpan) SetAttribute(key string, value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	switch v := value.(type) {
	case string:
		s.span.SetAttributes(attribute.String(key, v))
	case int:
		s.span.SetAttributes(attribute.Int(key, v))
	case int64:
		s.span.SetAttributes(attribute.Int64(key, v))
	case float64:
		s.span.SetAttributes(attribute.Float64(key, v))
	case bool:
		s.span.SetAttributes(attribute.Bool(key, v))
	default:
		s.span.SetAttributes(attribute.String(key, toString(value)))
	}
}

// SetStatus sets the status of the span
func (s *OTELSpan) SetStatus(code StatusCode, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var otelCode codes.Code
	switch code {
	case StatusOK:
		otelCode = codes.Ok
	case StatusError:
		otelCode = codes.Error
	default:
		otelCode = codes.Unset
	}
	s.span.SetStatus(otelCode, message)
}

// AddEvent adds an event to the span
func (s *OTELSpan) AddEvent(name string, attributes map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	attrs := make([]attribute.KeyValue, 0, len(attributes))
	for k, v := range attributes {
		attrs = append(attrs, attribute.KeyValue{
			Key:   attribute.Key(k),
			Value: attributeValue(v),
		})
	}
	s.span.AddEvent(name, trace.WithAttributes(attrs...))
}

// End ends the span
func (s *OTELSpan) End() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.span.End()
}

// Context returns the span's context
func (s *OTELSpan) Context() context.Context {
	// This method is part of the interface but doesn't have a direct OTEL equivalent
	// We return a background context with the span attached for compatibility
	ctx := context.Background()
	return trace.ContextWithSpan(ctx, s.span)
}

// TraceID returns the trace ID
func (s *OTELSpan) TraceID() string {
	return s.span.SpanContext().TraceID().String()
}

// SpanID returns the span ID
func (s *OTELSpan) SpanID() string {
	return s.span.SpanContext().SpanID().String()
}

// SpanContext returns the underlying OpenTelemetry SpanContext
func (s *OTELSpan) SpanContext() trace.SpanContext {
	return s.span.SpanContext()
}

// attributeValue converts an interface{} value to an attribute.Value
func attributeValue(v interface{}) attribute.Value {
	switch val := v.(type) {
	case string:
		return attribute.StringValue(val)
	case int:
		return attribute.IntValue(val)
	case int64:
		return attribute.Int64Value(val)
	case float64:
		return attribute.Float64Value(val)
	case bool:
		return attribute.BoolValue(val)
	default:
		return attribute.StringValue(toString(val))
	}
}

// toString converts an interface{} to string
func toString(v interface{}) string {
	if v == nil {
		return ""
	}
	return string(v.(string)) // This is a simplified version - in practice, you'd want more robust conversion
}