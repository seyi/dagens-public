# Jaeger Integration for Dagens

This document explains how to configure Dagens to export traces to Jaeger for distributed tracing visualization and analysis.

## Overview

Dagens uses OpenTelemetry (OTEL) for distributed tracing. Jaeger can receive traces from OTEL using the OTLP (OpenTelemetry Protocol) format. This is the recommended approach as the dedicated Jaeger exporter is deprecated.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                     Dagens Framework                      │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────┐  │
│  │   Agents    │  │ LoadBalancer│  │  A2A Protocol   │  │
│  └──────┬──────┘  └──────┬──────┘  └────────┬────────┘  │
│         │                │                   │           │
│         └────────────────┼───────────────────┘           │
│                          │                               │
│              ┌───────────▼───────────┐                   │
│              │   pkg/telemetry       │                   │
│              │   (OTEL SDK)          │                   │
│              └───────────┬───────────┘                   │
└──────────────────────────┼──────────────────────────────┘
                           │ OTLP
                           ▼
              ┌────────────────────────┐
              │   Jaeger / OTLP       │
              │   Collector           │
              └────────────────────────┘
```

## Jaeger Setup

### 1. Start Jaeger with OTLP Support

```bash
docker run -d --name jaeger \
  -e COLLECTOR_OTLP_ENABLED=true \
  -p 16686:16686 \
  -p 4317:4317 \
  -p 4318:4318 \
  jaegertracing/all-in-one:latest
```

### 2. Configure Dagens for Jaeger

#### Option A: Using Environment Variables

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317
export OTEL_EXPORTER_TYPE=otlp
export OTEL_SERVICE_NAME=dagens-agents
export OTEL_SERVICE_VERSION=1.0.0
export OTEL_TRACES_SAMPLER_ARG=0.1  # 10% sampling for production
```

#### Option B: Programmatic Configuration

```go
import "github.com/seyi/dagens/pkg/telemetry"

config := telemetry.TracerConfig{
    ServiceName:    "dagens-agents",
    ServiceVersion: "1.0.0",
    Endpoint:       "localhost:4317",  // Jaeger OTLP endpoint
    ExporterType:   telemetry.ExporterOTLP,
    SampleRatio:    0.1,  // 10% sampling
}

collector, err := telemetry.NewTelemetryCollectorWithConfig(telemetry.CollectorConfig{
    TracerType:     telemetry.TracerOTEL,
    TracerConfig:   config,
    EnableMetrics:  true,
    EnableLogging:  true,
})
if err != nil {
    log.Fatal(err)
}
```

## Jaeger Features in Dagens

### 1. Distributed Trace Context Propagation

Dagens supports W3C TraceContext propagation across agent-to-agent calls:

```go
// In A2A communication, context is automatically propagated
ctx, span := tracer.StartSpan(ctx, "agent-operation")
defer span.End()

// Attributes are automatically added
span.SetAttribute("agent.name", agent.Name())
span.SetAttribute("operation.type", "execute")
```

### 2. Service Mesh Integration

Jaeger can visualize the communication between different agents in Dagens:

- Agent execution flows
- Load balancer decision points
- Circuit breaker state changes
- Health check results

### 3. Performance Analysis

Jaeger provides detailed performance analysis:

- Latency breakdowns
- Service dependency graphs
- Error rate analysis
- Sampling configuration

## Configuration Options

### Exporter Types

| Type | Description | Use Case |
|------|-------------|----------|
| `otlp` | OTLP gRPC exporter | Production (Jaeger, Tempo, etc.) |
| `console` | Console output | Development/debugging |
| `noop` | No export | Testing |

### Sampling Strategies

```go
// 100% sampling (development)
SampleRatio: 1.0

// 1% sampling (production high-traffic)
SampleRatio: 0.01

// No sampling (testing)
SampleRatio: 0.0
```

## Testing Jaeger Integration

### 1. Verify Jaeger is Running

```bash
# Check if Jaeger is accepting OTLP
curl -s http://localhost:16686/api/services | jq
```

### 2. Run a Simple Test

```go
// Create a simple trace to verify integration
tracer := collector.GetTracer()
ctx, span := tracer.StartSpan(context.Background(), "test-operation")
defer span.End()

span.SetAttribute("test", "value")
span.SetStatus(telemetry.StatusOK, "completed")

// Force flush to ensure trace is sent
collector.ForceFlush(context.Background())
```

### 3. View Traces

Open Jaeger UI at `http://localhost:16686` and search for traces from your service.

## Production Considerations

### 1. Resource Configuration

```go
// Proper resource configuration for Jaeger
res := resource.NewWithAttributes(
    semconv.SchemaURL,
    semconv.ServiceNameKey.String("dagens-agents"),
    semconv.ServiceVersionKey.String("1.0.0"),
    semconv.DeploymentEnvironmentKey.String("production"),
)
```

### 2. Batch Processing

The OTEL SDK uses batch processing by default, which is efficient for Jaeger:

```go
// Batch span processor (default)
spanProcessor := sdktrace.NewBatchSpanProcessor(exporter)
```

### 3. Security

For production deployments, use secure connections:

```go
// Secure OTLP connection
exporter, err := otlptracegrpc.New(
    context.Background(),
    otlptracegrpc.WithEndpoint("jaeger.example.com:4317"),
    otlptracegrpc.WithTLSCredentials(credentials.NewClientTLSFromCert(nil, "")),
)
```

## Troubleshooting

### 1. No Traces Appearing in Jaeger

- Verify Jaeger is running and accepting OTLP connections
- Check network connectivity to Jaeger endpoint
- Verify OTEL environment variables are set correctly
- Check application logs for export errors

### 2. High Memory Usage

- Adjust sampling ratio to reduce trace volume
- Tune batch processor settings
- Monitor application resource usage

### 3. Connection Issues

```bash
# Test connectivity to Jaeger
telnet localhost 4317

# Check Jaeger logs
docker logs jaeger
```

## Best Practices

### 1. Sampling Configuration

- Development: 100% sampling for debugging
- Staging: 10-50% sampling for testing
- Production: 1-10% sampling for high-traffic services

### 2. Trace Context Propagation

- Always pass context between agent calls
- Use semantic attributes for consistency
- Limit attribute cardinality to avoid high cardinality issues

### 3. Service Naming

- Use consistent service names across deployments
- Include environment in service names if needed
- Follow semantic conventions where possible

## Example: Complete Jaeger Configuration

```go
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
        ServiceName:    "dagens-production",
        ServiceVersion: "v1.2.3",
        Endpoint:       "jaeger-collector.example.com:4317",
        ExporterType:   telemetry.ExporterOTLP,
        SampleRatio:    0.05, // 5% sampling
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
    defer collector.Shutdown(context.Background())

    // Use the tracer in your application
    tracer := collector.GetTracer()
    
    ctx, span := tracer.StartSpan(context.Background(), "application-startup")
    defer span.End()
    
    span.SetAttribute("startup.time", time.Now().Unix())
    span.SetStatus(telemetry.StatusOK, "Application started successfully")
    
    // Your application logic here...
    
    // Force flush before shutdown
    collector.ForceFlush(context.Background())
}
```

## Integration with Load Testing

The Jaeger integration works seamlessly with the load testing framework:

```bash
# Run load test with Jaeger tracing
./loadtest --concurrency=100 --duration=60s --scenario=throughput

# View traces in Jaeger UI to analyze performance
```

This integration allows you to visualize the performance characteristics of your Dagens deployment under various load conditions.