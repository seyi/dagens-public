# Migration Guide: From In-Memory Tracer to OpenTelemetry

This guide describes how to migrate from the in-memory tracer to the OpenTelemetry SDK.

## Configuration Changes

### Before (In-Memory Only)
```go
telemetryCollector := telemetry.NewDefaultTelemetryCollector()
```

### After (Configurable)
```go
// Option 1: Using environment variables (recommended for production)
telemetryCollector, err := telemetry.InitTelemetry("my-service", "1.0.0")
if err != nil {
    log.Fatal(err)
}

// Option 2: Explicit OTEL configuration
config := telemetry.TracerConfig{
    ServiceName:  "my-service",
    ServiceVersion: "1.0.0",
    Endpoint:     "localhost:4317",  // OTLP gRPC endpoint
    ExporterType: telemetry.ExporterOTLP,
    SampleRatio:  0.1,  // 10% sampling
}

telemetryCollector, err := telemetry.InitOTELTelemetry(config)
if err != nil {
    log.Fatal(err)
}

// Option 3: Console exporter for development
config.ExporterType = telemetry.ExporterConsole
telemetryCollector, err := telemetry.InitOTELTelemetry(config)
```

## Environment Variables

The system supports standard OpenTelemetry environment variables:

- `OTEL_EXPORTER_OTLP_ENDPOINT` - OTLP endpoint (e.g., "localhost:4317")
- `OTEL_SERVICE_NAME` - Service name override
- `OTEL_EXPORTER_TYPE` - Exporter type: "otlp", "console", "noop"
- `OTEL_TRACES_SAMPLER_ARG` - Sampling ratio (0.0 to 1.0)

## Shutdown Handling

### Before
```go
// No shutdown needed for in-memory
```

### After
```go
// Proper shutdown to flush spans
defer func() {
    if err := telemetryCollector.Shutdown(context.Background()); err != nil {
        log.Printf("Error shutting down telemetry: %v", err)
    }
}()

// Or for global collector:
defer func() {
    if err := telemetry.ShutdownGlobalTelemetry(context.Background()); err != nil {
        log.Printf("Error shutting down global telemetry: %v", err)
    }
}()
```

## Usage Examples

The usage remains the same due to interface compatibility:

```go
// Getting tracer
tracer := telemetryCollector.GetTracer()

// Starting spans
ctx, span := tracer.StartSpan(ctx, "operation-name")
defer span.End()

// Setting attributes
span.SetAttribute("user.id", userID)
span.SetAttribute("request.size", requestSize)

// Adding events
span.AddEvent("user-action", map[string]interface{}{
    "action": "click",
    "element": "button",
})

// Setting status
span.SetStatus(telemetry.StatusError, "operation failed")
```

## Performance Considerations

- OTEL uses batch processing by default (recommended)
- Sampling can be configured to reduce overhead
- Network calls are asynchronous and shouldn't block application logic
- Memory usage is generally lower than in-memory storage for long-running processes

## Testing

For unit tests, you can continue using the in-memory implementation:

```go
// For tests, explicitly use in-memory
config := telemetry.CollectorConfig{
    TracerType: telemetry.TracerInMemory,
}
telemetryCollector, _ := telemetry.NewTelemetryCollector(config)
```

Or use the global collector with in-memory:

```go
// In test setup
telemetry.InitGlobalTelemetry("test-service", "test")
```