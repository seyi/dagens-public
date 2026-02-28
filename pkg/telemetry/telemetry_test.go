package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOTELTracer(t *testing.T) {
	config := TracerConfig{
		ServiceName:    "test-service",
		ServiceVersion: "test",
		ExporterType:   ExporterNoOp, // Use NoOp for tests
		SampleRatio:    1.0, // Always sample for tests
	}

	tracer, err := NewOTELTracer(config)
	require.NoError(t, err)
	defer tracer.Shutdown(context.Background())

	t.Run("StartSpan creates valid span", func(t *testing.T) {
		_, span := tracer.StartSpan(context.Background(), "test-operation")
		require.NotNil(t, span)

		span.SetAttribute("test.key", "test-value")
		span.SetStatus(StatusOK, "success")
		span.AddEvent("test-event", map[string]interface{}{"event-key": "event-value"})
		span.End()

		// Verify span properties
		assert.NotEmpty(t, span.TraceID())
		assert.NotEmpty(t, span.SpanID())
	})

	t.Run("GetSpan retrieves span from context", func(t *testing.T) {
		ctx, span := tracer.StartSpan(context.Background(), "test-get-span")
		defer span.End()

		retrievedSpan := tracer.GetSpan(ctx)
		require.NotNil(t, retrievedSpan)
		assert.Equal(t, span.SpanID(), retrievedSpan.SpanID())
	})

	t.Run("Span attributes are set correctly", func(t *testing.T) {
		_, span := tracer.StartSpan(context.Background(), "test-attributes")
		defer span.End()

		span.SetAttribute("string_attr", "string_value")
		span.SetAttribute("int_attr", 42)
		span.SetAttribute("float_attr", 3.14)
		span.SetAttribute("bool_attr", true)
		span.SetAttribute("other_attr", "other_value")
	})
}

func TestTelemetryCollectorWithOTEL(t *testing.T) {
	config := CollectorConfig{
		TracerType: TracerOTEL,
		TracerConfig: TracerConfig{
			ServiceName:    "test-service",
			ServiceVersion: "test",
			ExporterType:   ExporterNoOp,
			SampleRatio:    1.0,
		},
		EnableMetrics: true,
		EnableLogging: true,
	}

	collector, err := NewTelemetryCollectorWithConfig(config)
	require.NoError(t, err)
	defer collector.Shutdown(context.Background())

	t.Run("Tracer is OTEL implementation", func(t *testing.T) {
		tracer := collector.GetTracer()
		_, ok := tracer.(*OTELTracer)
		assert.True(t, ok, "Tracer should be OTELTracer implementation")
	})

	t.Run("Can create and use spans", func(t *testing.T) {
		tracer := collector.GetTracer()
		_, span := tracer.StartSpan(context.Background(), "test-operation")
		defer span.End()

		span.SetAttribute("test", "value")
		span.SetStatus(StatusOK, "completed")

		// Verify span was created
		assert.NotEmpty(t, span.TraceID())
		assert.NotEmpty(t, span.SpanID())
	})
}

func TestTelemetryCollectorWithInMemory(t *testing.T) {
	config := CollectorConfig{
		TracerType:    TracerInMemory,
		EnableMetrics: true,
		EnableLogging: true,
	}

	collector, err := NewTelemetryCollectorWithConfig(config)
	require.NoError(t, err)

	t.Run("Tracer is in-memory implementation", func(t *testing.T) {
		tracer := collector.GetTracer()
		_, ok := tracer.(*InMemoryTracer)
		assert.True(t, ok, "Tracer should be InMemoryTracer implementation")
	})

	t.Run("Backward compatibility maintained", func(t *testing.T) {
		tracer := collector.GetTracer()
		_, span := tracer.StartSpan(context.Background(), "test-operation")
		defer span.End()

		span.SetAttribute("test", "value")
		span.SetStatus(StatusOK, "completed")

		// Verify span was created
		assert.NotEmpty(t, span.TraceID())
		assert.NotEmpty(t, span.SpanID())
	})
}

func TestGlobalTelemetry(t *testing.T) {
	// Reset global collector for test
	globalCollector = nil
	globalCollectorOnce = &Once{}

	t.Run("Global collector initialization", func(t *testing.T) {
		err := InitGlobalTelemetry("test-service", "test-version")
		require.NoError(t, err)

		collector := GetGlobalTelemetry()
		require.NotNil(t, collector)

		// Test that we can use the global collector
		tracer := collector.GetTracer()
		_, span := tracer.StartSpan(context.Background(), "global-test")
		span.End()
	})

	t.Run("Global collector shutdown", func(t *testing.T) {
		err := ShutdownGlobalTelemetry(context.Background())
		assert.NoError(t, err)
	})
}

func TestTelemetryCollectorShutdown(t *testing.T) {
	config := CollectorConfig{
		TracerType: TracerOTEL,
		TracerConfig: TracerConfig{
			ServiceName:    "test-service",
			ServiceVersion: "test",
			ExporterType:   ExporterNoOp,
		},
	}

	collector, err := NewTelemetryCollectorWithConfig(config)
	require.NoError(t, err)

	t.Run("Shutdown works without error", func(t *testing.T) {
		err := collector.Shutdown(context.Background())
		assert.NoError(t, err)
	})

	t.Run("ForceFlush works without error", func(t *testing.T) {
		err := collector.ForceFlush(context.Background())
		assert.NoError(t, err)
	})
}

func TestA2ATraceContextPropagation(t *testing.T) {
	// Setup a NoOp tracer for testing
	collectorConfig := CollectorConfig{
		TracerType: TracerOTEL,
		TracerConfig: TracerConfig{
			ServiceName:    "a2a-test-service",
			ServiceVersion: "1.0.0",
			ExporterType:   ExporterNoOp,
			SampleRatio:    1.0,
		},
	}
	collector, err := NewTelemetryCollectorWithConfig(collectorConfig)
	require.NoError(t, err)
	defer collector.Shutdown(context.Background())

	// Mock HTTP server to simulate an A2A agent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Extract trace context from incoming request
		prop := otel.GetTextMapPropagator()
		ctx := prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		// Start a new span for agent execution on the server side
		_, span := collector.GetTracer().StartSpan(ctx, "agent.execute")
		defer span.End()

		if !span.SpanContext().IsValid() {
			http.Error(w, "No trace context found", http.StatusBadRequest)
			return
		}

		// Respond with the extracted trace and span IDs
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fmt.Sprintf(`{"traceID": "%s", "spanID": "%s"}`, span.TraceID(), span.SpanID())))
	}))
	defer server.Close()

	// Simulate a client making an A2A call
	clientTracer := collector.GetTracer()
	reqCtx, clientSpan := clientTracer.StartSpan(context.Background(), "client-a2a-call")
	defer clientSpan.End()

	// Inject client trace context into outgoing request
	httpReq, err := http.NewRequestWithContext(reqCtx, "GET", server.URL, nil)
	require.NoError(t, err)
	otel.GetTextMapPropagator().Inject(reqCtx, propagation.HeaderCarrier(httpReq.Header))

	resp, err := http.DefaultClient.Do(httpReq)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := ioutil.ReadAll(resp.Body)
	require.NoError(t, err)

	var result map[string]string
	err = json.Unmarshal(body, &result)
	require.NoError(t, err)

	// Verify that the server received the correct trace context
	assert.Equal(t, clientSpan.TraceID(), result["traceID"])
	assert.NotEqual(t, clientSpan.SpanID(), result["spanID"]) // Span ID should be different as it's a new span on the server
	assert.NotEmpty(t, result["spanID"])
}
