// Package protoactor provides an actor model implementation using the ProtoActor-Go library
package protoactor

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"
)

var (
	actorsSpawnedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "internal_actors_spawned_total",
		Help: "The total number of spawned actors.",
	})
	actorsStoppedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "internal_actors_stopped_total",
		Help: "The total number of stopped actors.",
	})
	messagesSentTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "internal_messages_sent_total",
		Help: "The total number of messages sent.",
	})
	messagesReceivedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "internal_messages_received_total",
		Help: "The total number of messages received.",
	})
	messageProcessingDurationSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "internal_message_processing_duration_seconds",
		Help: "The duration of message processing.",
	})
)

// TextMapCarrier is a carrier for injecting and extracting span context from a map.
type TextMapCarrier struct {
	m map[string]string
}

// NewTextMapCarrier creates a new TextMapCarrier.
func NewTextMapCarrier(m map[string]string) *TextMapCarrier {
	return &TextMapCarrier{m: m}
}

// Get returns the value for a given key.
func (c *TextMapCarrier) Get(key string) string {
	return c.m[key]
}

// Set sets the value for a given key.
func (c *TextMapCarrier) Set(key, value string) {
	c.m[key] = value
}

// Keys returns all the keys in the carrier.
func (c *TextMapCarrier) Keys() []string {
	keys := make([]string, 0, len(c.m))
	for k := range c.m {
		keys = append(keys, k)
	}
	return keys
}

func newTracerProvider(serviceName string) (*sdktrace.TracerProvider, error) {
	exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
	if err != nil {
		return nil, err
	}

	// Create resource without merging to avoid schema URL conflicts
	r := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String(serviceName),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(r),
	)
	return tp, nil
}

func initTracer() func() {
	tp, err := newTracerProvider("internal")
	if err != nil {
		panic(err)
	}
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	return func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			panic(err)
		}
	}
}

func getTracer() trace.Tracer {
	return otel.Tracer("internal/actor")
}
