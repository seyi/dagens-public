# Graph Observability Demo

This example demonstrates the new observability features added to the Dagens graph package.

## Overview

The demo shows how to:
- Create a graph with multiple nodes
- Execute the graph with full observability
- Track state changes during execution
- Monitor execution metrics and traces

## Features Demonstrated

1. **Graph-Level Tracing**: End-to-end tracing of graph execution
2. **Node-Level Tracing**: Individual node execution spans
3. **Edge Tracing**: Tracking of data flow between nodes
4. **State Change Tracking**: Monitoring of state modifications
5. **Execution Metrics**: Performance and error metrics

## Running the Demo

```bash
# Build and run the demo
go run examples/graph_observability_demo/main.go
```

## Expected Output

The demo will show:
- Console output of node execution
- Trace spans for graph and node execution
- State changes during execution
- Execution metrics

## Key Components

### GraphExecutionContext
- Manages the observability context for graph execution
- Creates root span for the entire graph
- Propagates context to all nodes

### State Store Observability
- Tracks state save/load operations
- Records file sizes and serialization metrics

### Edge Tracing
- Monitors data flow between nodes
- Records transfer metrics

## Integration with Existing Systems

The observability features integrate with:
- **OpenTelemetry**: Standard tracing and metrics
- **Jaeger**: Distributed tracing visualization
- **Prometheus**: Metrics collection
- **Existing Agentic Nodes**: Enhanced node-level observability

## Next Steps

For production use:
1. Configure proper exporters (Jaeger, Prometheus)
2. Implement sampling strategies
3. Add custom metrics for your use case
4. Set up alerting based on execution metrics