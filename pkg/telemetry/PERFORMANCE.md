# Performance Considerations for OpenTelemetry Integration

This document outlines the performance implications and optimizations for the OpenTelemetry integration.

## Key Performance Improvements

### 1. Batch Processing
- Uses `sdktrace.NewBatchSpanProcessor` by default
- Spans are buffered and sent in batches rather than individually
- Reduces network overhead and improves throughput

### 2. Asynchronous Operations
- Span export happens on background goroutines
- Application execution is not blocked by network calls
- Default batch processor handles retries and failures gracefully

### 3. Configurable Sampling
- Supports various sampling strategies:
  - `AlwaysSample()` - for development/debugging
  - `TraceIDRatioBased(ratio)` - for production (e.g., 1% sampling)
  - `ParentBased()` - respects upstream sampling decisions
- Significantly reduces overhead at scale

## Configuration for Performance

### Sampling Configuration
```go
config := telemetry.TracerConfig{
    // ... other config
    SampleRatio: 0.01, // 1% sampling for high-traffic services
}
```

### Batch Processor Tuning
The OTEL SDK provides default values that work well for most cases:
- Batch size: 512 spans
- Export timeout: 30 seconds
- Max queue size: 2048 spans

For high-throughput services, consider:
```go
// Custom batch processor (if needed)
bsp := sdktrace.NewBatchSpanProcessor(exporter,
    sdktrace.WithBatchTimeout(5*time.Second),     // Faster export
    sdktrace.WithMaxExportBatchSize(1024),        // Larger batches
    sdktrace.WithMaxQueueSize(4096),              // Larger queue
)
```

## Memory Usage

### Before (In-Memory Implementation)
- All spans stored in memory indefinitely
- Memory usage grows linearly with request volume
- Risk of OOM in high-traffic scenarios

### After (OTEL Implementation)
- Spans are processed and exported asynchronously
- Memory usage is bounded by batch processor queue size
- Automatic cleanup of completed spans

## Network Considerations

### Exporter Selection
- **OTLP gRPC (Recommended)**: Efficient binary protocol, lower overhead
- **Console (Development)**: No network overhead, for debugging
- **NoOp (Testing)**: Zero overhead

### Connection Management
- Reuses HTTP/gRPC connections
- Handles connection failures and retries
- Configurable timeouts to prevent hanging requests

## Context Propagation Performance

- Uses W3C TraceContext standard
- Minimal overhead for context extraction/injection
- Critical for distributed tracing across services

## Best Practices

### 1. Proper Span Lifecycle
```go
// Good: Always end spans
ctx, span := tracer.StartSpan(ctx, "operation")
defer span.End() // Ensures span is ended even on panic/error

// Do work...
```

### 2. Attribute Limitations
- Limit number of attributes per span (recommended < 30)
- Use semantic attributes when possible
- Avoid high-cardinality attributes

### 3. Resource Configuration
- Set service name and version for proper identification
- Use resource detection for automatic host/process attributes

## Monitoring Performance

### Metrics to Watch
- Span export success/failure rates
- Batch processor queue length
- Memory usage patterns
- Network latency for exports

### Health Checks
The implementation includes proper shutdown and flush methods:
```go
// During graceful shutdown
if err := collector.Shutdown(ctx); err != nil {
    log.Printf("Error shutting down telemetry: %v", err)
}
```

## Performance Testing Recommendations

### Load Testing
- Test with expected production traffic volumes
- Monitor memory usage under sustained load
- Verify span export performance doesn't impact application

### Sampling Validation
- Validate sampling rates achieve desired coverage
- Monitor for trace fragmentation in distributed systems

## Comparison Summary

| Aspect | In-Memory | OTEL SDK |
|--------|-----------|----------|
| Memory Usage | Grows indefinitely | Bounded by queue size |
| Network Overhead | None | Configurable, async |
| Sampling | None | Configurable |
| Export Reliability | None (lost on restart) | Resilient with retries |
| Distributed Tracing | Not supported | Full support |
| Performance Impact | Low initially, OOM risk | Configurable, bounded |

The OTEL implementation provides significantly better performance characteristics for production use while maintaining low overhead through proper configuration.