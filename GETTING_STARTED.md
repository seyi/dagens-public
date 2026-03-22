# Getting Started with Spark AI Agents

This guide walks you through building production-ready AI agents using the Spark AI Agents framework in Go.

## Prerequisites

- Go 1.21 or later
- Git

## Installation

```bash
# Clone the repository
git clone https://github.com/seyi/dagens
cd dagens

# Download dependencies
go mod download

# Verify installation
go test ./pkg/agent/... -v
```

## Quick Start

### 1. Create Your First Agent

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/seyi/dagens/pkg/agent"
)

func main() {
    ctx := context.Background()

    // Create a simple agent
    myAgent := agent.NewAgent(agent.AgentConfig{
        Name:        "greeting-agent",
        Description: "A simple agent that greets users",
    })

    // Execute the agent
    output, err := myAgent.Execute(ctx, &agent.AgentInput{
        TaskID:      "task-001",
        Instruction: "Say hello to the world",
    })
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Result: %v\n", output.Result)
}
```

### 2. Add Production Resilience

Wrap your agent with production-grade resilience patterns:

```go
package main

import (
    "context"
    "time"

    "github.com/seyi/dagens/pkg/agent"
    "github.com/seyi/dagens/pkg/resilience"
)

func main() {
    ctx := context.Background()

    // Create base agent
    baseAgent := agent.NewAgent(agent.AgentConfig{
        Name: "data-processor",
    })

    // Configure resilience
    config := agent.ResilientAgentConfig{
        BackoffConfig: resilience.BackoffConfig{
            InitialDelay: 100 * time.Millisecond,
            MaxDelay:     5 * time.Second,
            Multiplier:   2.0,
            JitterFactor: 0.1,
            MaxRetries:   3,
        },
        CircuitBreakerConfig: resilience.CircuitBreakerConfig{
            Name:             "data-processor",
            FailureThreshold: 5,
            SuccessThreshold: 2,
            Timeout:          30 * time.Second,
        },
        RateLimitEnabled:   true,
        RateLimitPerSecond: 100,
        RateLimitBurst:     200,
        ExecutionTimeout:   60 * time.Second,
    }

    // Wrap with resilience
    resilientAgent := agent.WrapWithResilienceConfig(baseAgent, config)

    // Execute - now with automatic retry, circuit breaker, and rate limiting
    output, err := resilientAgent.Execute(ctx, &agent.AgentInput{
        TaskID:      "task-001",
        Instruction: "Process data",
    })
    // ... handle output
}
```

### 3. Add State Management

Enable checkpointing for fault-tolerant long-running operations:

```go
package main

import (
    "context"

    "github.com/seyi/dagens/pkg/state"
)

func main() {
    ctx := context.Background()

    // Create state store (memory for development, swap for Redis/DynamoDB in production)
    store := state.NewMemoryStateStore()
    defer store.Close()

    // Save checkpoint during processing
    checkpoint := &state.Checkpoint{
        ID:    "cp-step-1",
        JobID: "job-001",
        State: map[string]interface{}{
            "progress":         50,
            "last_record_id":   "rec-500",
            "processed_count":  500,
        },
    }
    store.Checkpoint.Save(ctx, checkpoint)

    // Later, recover from checkpoint
    latest, err := store.Checkpoint.GetLatest(ctx, "job-001")
    if err == nil {
        // Resume from checkpoint
        progress := latest.State["progress"].(int)
        // ... continue from progress
    }
}
```

### 4. Add Observability

Enable structured logging and metrics:

```go
package main

import (
    "os"
    "time"

    "github.com/seyi/dagens/pkg/observability"
)

func main() {
    // Configure production logger
    config := observability.LoggerConfig{
        Output:      os.Stdout,
        Level:       observability.LogLevelInfo,
        Service:     "my-agent-service",
        Version:     "1.0.0",
        PrettyPrint: false, // Compact JSON for production
    }

    logger := observability.NewLogger(config)

    // Log structured events
    logger.Info("agent_started",
        observability.Field("agent_name", "data-processor"),
        observability.Field("partition", 3),
    )

    // Record metrics
    metrics := observability.GetMetrics()
    metrics.RecordAgentExecution("data-processor", "sequential", "success", 2*time.Second)
    metrics.RecordLLMRequest("openai", "gpt-4", "success", 850*time.Millisecond)
}
```

## Core Concepts

### Agents

Agents are the basic unit of computation:

```go
type Agent interface {
    ID() string
    Name() string
    Description() string
    Capabilities() []string
    Execute(ctx context.Context, input *AgentInput) (*AgentOutput, error)
    Dependencies() []Agent
    Partition() string
}
```

### Agent Input/Output

```go
// Input to an agent
type AgentInput struct {
    TaskID      string
    Instruction string
    Context     map[string]interface{}
    Tools       []Tool
    Model       string
    MaxRetries  int
    Timeout     time.Duration
}

// Output from an agent
type AgentOutput struct {
    TaskID       string
    Result       interface{}
    Metadata     map[string]interface{}
    Error        error
    ExecutionLog []ExecutionStep
    Metrics      *ExecutionMetrics
}
```

### Resilience Patterns

The framework provides three core resilience patterns:

| Pattern | Purpose | Configuration |
|---------|---------|---------------|
| **Retry** | Handle transient failures | `BackoffConfig` |
| **Circuit Breaker** | Prevent cascade failures | `CircuitBreakerConfig` |
| **Rate Limiting** | Protect downstream services | `RateLimitPerSecond`, `RateLimitBurst` |

### State Management

Four types of state backends:

| Backend | Purpose | Interface |
|---------|---------|-----------|
| **State** | Key-value storage with TTL | `StateBackend` |
| **Checkpoint** | Fault-tolerant snapshots | `CheckpointBackend` |
| **Session** | User context with optimistic locking | `SessionBackend` |
| **History** | Execution audit trail | `HistoryBackend` |

## Running the Examples

The framework includes comprehensive examples:

```bash
cd spark-ai-agents

# Resilience patterns (retry, circuit breaker, rate limiting)
go run examples/resilience_patterns_example.go

# State management (checkpointing, sessions, history)
go run examples/state_management_example.go

# Observability (logging, metrics, tracing)
go run examples/observability_example.go

# Full production stack (all patterns combined)
go run examples/production_hardening_example.go
```

## Production Deployment Checklist

### Resilience
- [ ] Configure retry with exponential backoff
- [ ] Set up circuit breakers per external service
- [ ] Enable rate limiting to protect downstream
- [ ] Set appropriate timeouts on all operations

### State Management
- [ ] Enable checkpointing for long-running operations
- [ ] Use session state for user context
- [ ] Track execution history for debugging
- [ ] Configure TTL for temporary state cleanup

### Observability
- [ ] Use structured JSON logging
- [ ] Propagate trace IDs across services
- [ ] Export Prometheus metrics
- [ ] Set up alerting on error rates

### Operations
- [ ] Implement graceful shutdown
- [ ] Add health check endpoints
- [ ] Configure resource limits
- [ ] Set up monitoring dashboards

## Configuration Reference

### Resilient Agent Configuration

```go
type ResilientAgentConfig struct {
    // Retry configuration
    BackoffConfig resilience.BackoffConfig

    // Circuit breaker configuration
    CircuitBreakerConfig resilience.CircuitBreakerConfig

    // Rate limiting
    RateLimitEnabled   bool
    RateLimitPerSecond float64
    RateLimitBurst     int

    // Timeout per execution
    ExecutionTimeout time.Duration

    // Observability
    MetricsEnabled bool
    LoggingEnabled bool
}
```

### Logger Configuration

```go
type LoggerConfig struct {
    Output        io.Writer      // Output destination
    Level         LogLevel       // Minimum log level
    Service       string         // Service name
    Version       string         // Service version
    IncludeCaller bool           // Include file:line
    PrettyPrint   bool           // Pretty JSON (dev only)
    DefaultFields map[string]interface{}
}
```

### State Backend Configuration

```go
// For production, implement these interfaces with Redis, DynamoDB, etc.
type StateBackend interface {
    Get(ctx context.Context, key string) ([]byte, error)
    Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
    Delete(ctx context.Context, key string) error
    List(ctx context.Context, pattern string) ([]string, error)
    Close() error
}
```

## Best Practices

### 1. Use Context for Cancellation

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

output, err := agent.Execute(ctx, input)
```

### 2. Handle Errors Gracefully

```go
output, err := agent.Execute(ctx, input)
if err != nil {
    switch {
    case errors.Is(err, resilience.ErrCircuitOpen):
        // Service is unhealthy, use fallback
    case errors.Is(err, resilience.ErrRateLimited):
        // Too many requests, back off
    case errors.Is(err, context.DeadlineExceeded):
        // Timeout, may retry
    default:
        // Handle other errors
    }
}
```

### 3. Use Structured Logging

```go
// Good - structured and queryable
logger.Info("agent_execution_completed",
    observability.Field("agent_name", "processor"),
    observability.Field("duration_ms", 150),
    observability.Field("task_id", taskID),
)

// Avoid - unstructured
log.Printf("Agent processor completed in 150ms for task %s", taskID)
```

### 4. Checkpoint Long Operations

```go
for i, item := range items {
    // Process item
    processItem(item)

    // Checkpoint every 100 items
    if i % 100 == 0 {
        store.Checkpoint.Save(ctx, &state.Checkpoint{
            ID:    fmt.Sprintf("batch-%d", i/100),
            JobID: jobID,
            State: map[string]interface{}{
                "last_index": i,
                "timestamp":  time.Now().Unix(),
            },
        })
    }
}
```

## Troubleshooting

### Circuit Breaker Keeps Opening

1. Check the failure threshold - it may be too low
2. Verify the downstream service health
3. Review error categorization (are transient errors counted?)

### Rate Limiting Too Aggressive

1. Increase `RateLimitPerSecond`
2. Increase `RateLimitBurst` for traffic spikes
3. Consider per-client rate limiting

### Checkpoints Not Persisting

1. Verify backend connection
2. Check for serialization errors in state
3. Ensure checkpoint IDs are unique

## Next Steps

- Review [ARCHITECTURE.md](ARCHITECTURE.md) for system design
- See [PRODUCTION_READINESS_ASSESSMENT.md](PRODUCTION_READINESS_ASSESSMENT.md) for deployment guide
- Explore [examples/](../examples/) for more patterns

## Support

- **Issues**: GitHub Issues
- **Discussions**: GitHub Discussions
- **Documentation**: [docs/](.)
