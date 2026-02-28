// Example demonstrating Jaeger integration with Dagens
package main

import (
	"context"
	"log"
	"time"

	"github.com/seyi/dagens/pkg/telemetry"
)

func main() {
	// Initialize telemetry with Jaeger configuration
	config := telemetry.TracerConfig{
		ServiceName:    "jaeger-demo-service",
		ServiceVersion: "1.0.0",
		Endpoint:       "localhost:4317", // Jaeger collector endpoint
		ExporterType:   telemetry.ExporterOTLP,
		SampleRatio:    1.0, // 100% sampling for demo
	}

	collector, err := telemetry.NewTelemetryCollectorWithConfig(telemetry.CollectorConfig{
		TracerType:     telemetry.TracerOTEL,
		TracerConfig:   config,
		EnableMetrics:  true,
		EnableLogging:  true,
	})
	if err != nil {
		log.Fatal("Failed to initialize telemetry: ", err)
	}
	defer func() {
		if err := collector.Shutdown(context.Background()); err != nil {
			log.Printf("Error shutting down telemetry: %v", err)
		}
	}()

	// Get tracer and create a trace
	tracer := collector.GetTracer()
	
	// Create a root span
	ctx, rootSpan := tracer.StartSpan(context.Background(), "jaeger-integration-demo")
	rootSpan.SetAttribute("demo.version", "1.0.0")
	rootSpan.SetAttribute("service.name", config.ServiceName)
	defer rootSpan.End()

	// Simulate some work with child spans
	for i := 0; i < 3; i++ {
		childCtx, childSpan := tracer.StartSpan(ctx, "child-operation")
		childSpan.SetAttribute("operation.id", i)
		childSpan.SetAttribute("operation.type", "demo")
		
		// Simulate work
		time.Sleep(10 * time.Millisecond)
		
		// Add an event
		childSpan.AddEvent("work-completed", map[string]interface{}{
			"items-processed": 100,
			"worker-id":      i,
		})
		
		childSpan.SetStatus(telemetry.StatusOK, "completed successfully")
		childSpan.End()
		
		// Pass context to next operation
		ctx = childCtx
	}

	// Add an event to root span
	rootSpan.AddEvent("demo-completed", map[string]interface{}{
		"total-operations": 3,
		"timestamp":        time.Now().Unix(),
	})

	rootSpan.SetStatus(telemetry.StatusOK, "demo completed successfully")

	// Force flush to ensure traces are sent to Jaeger
	if err := collector.ForceFlush(context.Background()); err != nil {
		log.Printf("Error flushing telemetry: %v", err)
	}

	log.Println("Jaeger integration demo completed. Check Jaeger UI for traces!")
	log.Println("Jaeger UI: http://localhost:16686")
	log.Println("Search for service name: jaeger-demo-service")
}