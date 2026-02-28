# Jaeger Integration Demo

This example demonstrates how to integrate Dagens with Jaeger for distributed tracing.

## Overview

This demo shows how to configure Dagens to export traces to Jaeger using the OTLP protocol. Jaeger is the recommended distributed tracing backend for Dagens.

## Prerequisites

- Docker
- Go 1.19+

## Running the Demo

### 1. Start Jaeger

```bash
# Start Jaeger with OTLP support
docker run -d --name jaeger \
  -e COLLECTOR_OTLP_ENABLED=true \
  -p 16686:16686 \
  -p 4317:4317 \
  -p 4318:4318 \
  jaegertracing/all-in-one:latest
```

### 2. Run the Demo

```bash
# Build the demo
go build -o jaeger-demo ./examples/jaeger_demo/main.go

# Run the demo
./jaeger-demo
```

### 3. View Traces in Jaeger

Open your browser and navigate to: `http://localhost:16686`

Search for traces from the service: `jaeger-demo-service`

## Configuration

The demo is configured to export traces to Jaeger using these settings:

- **Endpoint**: `localhost:4317` (Jaeger OTLP gRPC endpoint)
- **Exporter**: OTLP (recommended)
- **Sampling**: 100% (for demo purposes)
- **Service Name**: `jaeger-demo-service`

## Production Configuration

For production use, adjust the configuration:

```go
config := telemetry.TracerConfig{
    ServiceName:    "your-production-service",
    ServiceVersion: "v1.2.3",
    Endpoint:       "jaeger-collector.your-domain.com:4317",
    ExporterType:   telemetry.ExporterOTLP,
    SampleRatio:    0.05, // 5% sampling for high-traffic services
}
```

## Architecture

The demo shows:

1. **Trace Creation**: Creating root and child spans
2. **Attribute Setting**: Adding metadata to spans
3. **Event Recording**: Adding events within spans
4. **Status Setting**: Marking span completion status
5. **Context Propagation**: Passing trace context between operations

## Cleanup

Stop Jaeger when done:

```bash
docker stop jaeger
docker rm jaeger
```

## Troubleshooting

### Jaeger Not Receiving Traces

- Verify Jaeger is running and listening on port 4317
- Check network connectivity
- Look for export errors in the application logs

### No Traces Visible

- Ensure the service name matches what you're searching for in Jaeger UI
- Check that sampling is enabled (100% for demo)
- Verify the endpoint configuration

## Integration with Dagens

This demo shows the same tracing configuration that can be used throughout the Dagens framework:

- Agent execution traces
- Load balancer decision traces
- Circuit breaker state changes
- Health check results
- A2A communication flows

The integration works seamlessly with the existing Dagens architecture and load testing framework.