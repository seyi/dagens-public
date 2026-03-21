// Copyright 2025 Apache Spark AI Agents
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Example demonstrating production observability for AI agent systems.
// Includes structured JSON logging, metrics collection, and distributed tracing context.
//
// Run with: go run observability_example.go
package main

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/observability"
)

// Observability_Example1_StructuredLogging demonstrates structured JSON logging.
// Structured logs are essential for log aggregation systems like ELK, Datadog, and Splunk.
func Observability_Example1_StructuredLogging() {
	fmt.Println("=== Example 1: Structured JSON Logging ===")

	// Capture output for demonstration
	var buf bytes.Buffer

	// Create logger with production configuration
	config := observability.LoggerConfig{
		Output:        &buf,
		Level:         observability.LogLevelInfo,
		Service:       "spark-ai-agents",
		Version:       "1.0.0",
		IncludeCaller: false,
		PrettyPrint:   true, // Pretty print for readability
	}

	logger := observability.NewLogger(config)

	// Basic logging with fields
	logger.Info("agent_started",
		observability.Field("agent_name", "data-processor"),
		observability.Field("partition", 3),
		observability.Field("model", "gpt-4"),
	)

	fmt.Println("Structured log output:")
	fmt.Println(buf.String())

	fmt.Println("Key benefits of structured logging:")
	fmt.Println("  - Machine-parseable JSON format")
	fmt.Println("  - Queryable fields in log aggregators")
	fmt.Println("  - Consistent schema across services")
	fmt.Println()
}

// Observability_Example2_LogLevels demonstrates proper use of log levels.
// Different levels help filter logs in production vs debugging.
func Observability_Example2_LogLevels() {
	fmt.Println("=== Example 2: Log Levels ===")

	var buf bytes.Buffer
	config := observability.LoggerConfig{
		Output:      &buf,
		Level:       observability.LogLevelDebug, // Show all levels
		Service:     "agent-service",
		PrettyPrint: false, // Compact for production
	}

	logger := observability.NewLogger(config)

	// Different log levels for different scenarios
	logger.Debug("cache_lookup", observability.Field("key", "user:123"))
	logger.Info("request_received", observability.Field("endpoint", "/api/v1/process"))
	logger.Warn("rate_limit_approaching", observability.Field("current", 80), observability.Field("limit", 100))
	logger.Error("external_service_failed", observability.Field("service", "llm-api"), observability.Field("error", "timeout"))

	fmt.Println("Log output (compact JSON):")
	fmt.Println(buf.String())

	fmt.Println("\nLog level guidelines:")
	fmt.Println("  DEBUG - Detailed troubleshooting info (disabled in prod)")
	fmt.Println("  INFO  - Normal operation events")
	fmt.Println("  WARN  - Recoverable issues, degraded performance")
	fmt.Println("  ERROR - Failures requiring attention")
	fmt.Println("  FATAL - Critical failures, service shutdown")
	fmt.Println()
}

// Observability_Example3_AgentLogger demonstrates specialized agent logging.
// AgentLogger provides pre-defined methods for common agent operations.
func Observability_Example3_AgentLogger() {
	fmt.Println("=== Example 3: Agent-Specific Logging ===")

	var buf bytes.Buffer
	config := observability.LoggerConfig{
		Output:      &buf,
		Level:       observability.LogLevelDebug,
		Service:     "agent-executor",
		PrettyPrint: true,
	}

	baseLogger := observability.NewLogger(config)
	agentLogger := observability.NewAgentLogger(baseLogger, "research-agent", "agent-001")

	// Log agent lifecycle events
	agentLogger.ExecutionStarted("task-xyz", "Analyze quarterly sales data and identify trends")

	// Log tool calls
	agentLogger.ToolCalled("database_query", 150*time.Millisecond, nil)
	agentLogger.ToolCalled("calculator", 5*time.Millisecond, nil)

	// Log LLM calls
	agentLogger.LLMCalled("openai", "gpt-4", 1250, 2500*time.Millisecond, nil)

	// Log completion
	agentLogger.ExecutionCompleted("task-xyz", 3*time.Second)

	fmt.Println("Agent operation logs:")
	fmt.Println(buf.String())
}

// Observability_Example4_ContextualLogging demonstrates logging with trace context.
// Context propagation enables distributed tracing across services.
func Observability_Example4_ContextualLogging() {
	fmt.Println("=== Example 4: Contextual Logging with Trace IDs ===")

	var buf bytes.Buffer
	config := observability.LoggerConfig{
		Output:      &buf,
		Level:       observability.LogLevelInfo,
		Service:     "agent-orchestrator",
		PrettyPrint: true,
	}

	logger := observability.NewLogger(config)

	// Create context with trace information
	ctx := context.Background()
	ctx = context.WithValue(ctx, observability.TraceIDKey, "trace-abc123")
	ctx = context.WithValue(ctx, observability.SpanIDKey, "span-def456")
	ctx = context.WithValue(ctx, observability.RequestIDKey, "req-789")

	// Log with context - trace IDs automatically included
	logger.InfoCtx(ctx, "processing_request",
		observability.Field("user_id", "user-42"),
		observability.Field("action", "analyze"),
	)

	logger.InfoCtx(ctx, "delegating_to_agent",
		observability.Field("agent", "analyzer"),
	)

	fmt.Println("Logs with trace context:")
	fmt.Println(buf.String())

	fmt.Println("Trace context enables:")
	fmt.Println("  - Following requests across services")
	fmt.Println("  - Correlating logs in distributed systems")
	fmt.Println("  - Integration with Jaeger, Zipkin, Datadog APM")
	fmt.Println()
}

// Observability_Example5_LoggerWithFields demonstrates creating child loggers.
// Child loggers inherit and extend parent fields.
func Observability_Example5_LoggerWithFields() {
	fmt.Println("=== Example 5: Child Loggers with Default Fields ===")

	var buf bytes.Buffer
	config := observability.LoggerConfig{
		Output:      &buf,
		Level:       observability.LogLevelInfo,
		Service:     "spark-agent",
		PrettyPrint: true,
	}

	// Create base logger
	baseLogger := observability.NewLogger(config)

	// Create specialized loggers for different components
	executorLogger := baseLogger.WithFields(
		observability.Field("component", "executor"),
		observability.Field("partition_id", 5),
	)

	schedulerLogger := baseLogger.WithFields(
		observability.Field("component", "scheduler"),
		observability.Field("pool", "default"),
	)

	// Each logger includes its default fields
	executorLogger.Info("task_started", observability.Field("task_id", "t-001"))
	schedulerLogger.Info("job_queued", observability.Field("job_id", "j-001"))

	fmt.Println("Child logger output:")
	fmt.Println(buf.String())

	fmt.Println("Use cases for child loggers:")
	fmt.Println("  - Per-component logging")
	fmt.Println("  - Per-request loggers")
	fmt.Println("  - Adding context progressively")
	fmt.Println()
}

// Observability_Example6_HTTPRequestLogging demonstrates HTTP request logging.
// Standard format for API observability.
func Observability_Example6_HTTPRequestLogging() {
	fmt.Println("=== Example 6: HTTP Request Logging ===")

	var buf bytes.Buffer
	config := observability.LoggerConfig{
		Output:      &buf,
		Level:       observability.LogLevelInfo,
		Service:     "api-gateway",
		PrettyPrint: true,
	}

	logger := observability.NewLogger(config)
	requestLogger := observability.NewRequestLogger(logger)

	// Log various HTTP responses
	requestLogger.LogHTTPRequest("GET", "/api/v1/agents", 200, 45*time.Millisecond, nil)
	requestLogger.LogHTTPRequest("POST", "/api/v1/tasks", 201, 120*time.Millisecond, nil)
	requestLogger.LogHTTPRequest("GET", "/api/v1/invalid", 404, 5*time.Millisecond, nil)
	requestLogger.LogHTTPRequest("POST", "/api/v1/process", 500, 3*time.Second, fmt.Errorf("database connection failed"))

	fmt.Println("HTTP request logs:")
	fmt.Println(buf.String())
}

// Observability_Example7_MetricsCollection demonstrates metrics recording.
// Metrics enable dashboards, alerts, and capacity planning.
func Observability_Example7_MetricsCollection() {
	fmt.Println("=== Example 7: Metrics Collection ===")

	// Get metrics collector
	metrics := observability.GetMetrics()

	// Record agent execution metrics
	metrics.RecordAgentExecution("data-processor", "sequential", "success", 2500*time.Millisecond)
	metrics.RecordAgentExecution("analyzer", "parallel", "success", 1200*time.Millisecond)
	metrics.RecordAgentExecution("writer", "sequential", "error", 500*time.Millisecond)

	// Record LLM metrics
	metrics.RecordLLMRequest("openai", "gpt-4", "success", 850*time.Millisecond)
	metrics.RecordLLMRequest("openai", "gpt-4", "success", 1200*time.Millisecond)
	metrics.RecordLLMRequest("anthropic", "claude-3", "success", 950*time.Millisecond)
	metrics.RecordLLMTokens("openai", "gpt-4", 500, 1000)
	metrics.RecordLLMTokens("anthropic", "claude-3", 400, 800)

	// Record state operation metrics
	metrics.RecordStateOperation("get", "memory", 5*time.Millisecond)
	metrics.RecordStateOperation("set", "memory", 10*time.Millisecond)

	// Record resilience metrics
	metrics.RecordAgentRetry("flaky-agent")
	metrics.RecordCircuitBreakerState("external-api", 1) // Open

	fmt.Println("Metrics recorded (exposed on /metrics endpoint):")
	fmt.Println("  - spark_agent_execution_duration_seconds")
	fmt.Println("  - spark_agent_llm_request_duration_seconds")
	fmt.Println("  - spark_agent_llm_tokens_total")
	fmt.Println("  - spark_agent_state_operation_duration_seconds")
	fmt.Println("  - spark_agent_retries_total")
	fmt.Println("  - spark_agent_circuit_breaker_state")

	fmt.Println("\nMetrics enable:")
	fmt.Println("  - Real-time dashboards (Grafana)")
	fmt.Println("  - Alerting on SLOs")
	fmt.Println("  - Cost tracking (LLM tokens)")
	fmt.Println("  - Capacity planning")
	fmt.Println()
}

// Observability_Example8_ResilienceObservability demonstrates observing resilience patterns.
// Tracking retries, circuit breaker state, and rate limiting provides operational visibility.
func Observability_Example8_ResilienceObservability() {
	fmt.Println("=== Example 8: Resilience Observability ===")

	var buf bytes.Buffer
	config := observability.LoggerConfig{
		Output:      &buf,
		Level:       observability.LogLevelDebug,
		Service:     "resilient-agent",
		PrettyPrint: true,
	}

	baseLogger := observability.NewLogger(config)
	agentLogger := observability.NewAgentLogger(baseLogger, "external-api-agent", "agent-ext-001")

	// Log retry attempts
	agentLogger.RetryAttempt(1, fmt.Errorf("connection refused"), 100*time.Millisecond)
	agentLogger.RetryAttempt(2, fmt.Errorf("timeout"), 200*time.Millisecond)

	// Log circuit breaker state changes
	agentLogger.CircuitBreakerTripped("open")

	// Log rate limiting
	agentLogger.Logger.Warn("rate_limited",
		observability.Field("agent_name", "external-api-agent"),
		observability.Field("current_rate", 150),
		observability.Field("limit", 100),
	)

	fmt.Println("Resilience observability logs:")
	fmt.Println(buf.String())

	fmt.Println("Resilience metrics to track:")
	fmt.Println("  - Retry rate by agent")
	fmt.Println("  - Circuit breaker state transitions")
	fmt.Println("  - Rate limiter rejections")
	fmt.Println("  - Timeout occurrences")
	fmt.Println()
}

// Observability_Example9_ProductionConfig demonstrates production-ready configuration.
// Shows best practices for logging configuration in production environments.
func Observability_Example9_ProductionConfig() {
	fmt.Println("=== Example 9: Production Configuration ===")

	fmt.Println("Production logging configuration:")
	fmt.Print(`
// Environment variables:
//   LOG_LEVEL=info           # Filter debug logs in production
//   LOG_FORMAT=json          # JSON for log aggregators
//   SERVICE_NAME=my-service  # Service identifier
//   SERVICE_VERSION=1.2.3    # For deployment tracking

// In code:
config := observability.LoggerConfig{
    Output:        os.Stdout,           // stdout for container logs
    Level:         observability.LogLevelInfo,
    Service:       os.Getenv("SERVICE_NAME"),
    Version:       os.Getenv("SERVICE_VERSION"),
    IncludeCaller: false,               // Disabled for performance
    PrettyPrint:   false,               // Compact JSON for volume
    DefaultFields: map[string]interface{}{
        "environment": os.Getenv("ENVIRONMENT"),
        "region":      os.Getenv("AWS_REGION"),
    },
}
`)

	fmt.Println("Production best practices:")
	fmt.Println("  1. Use INFO level in production, DEBUG for troubleshooting")
	fmt.Println("  2. Include service name and version in every log")
	fmt.Println("  3. Use compact JSON format for log volume")
	fmt.Println("  4. Propagate trace IDs across service boundaries")
	fmt.Println("  5. Set up log sampling for high-volume endpoints")
	fmt.Println("  6. Establish alerting on ERROR rate increase")
	fmt.Println()
}

// RunObservabilityExamples runs all observability examples
func RunObservabilityExamples() {
	fmt.Println("========================================")
	fmt.Println("Observability Examples")
	fmt.Println("========================================")

	Observability_Example1_StructuredLogging()
	Observability_Example2_LogLevels()
	Observability_Example3_AgentLogger()
	Observability_Example4_ContextualLogging()
	Observability_Example5_LoggerWithFields()
	Observability_Example6_HTTPRequestLogging()
	Observability_Example7_MetricsCollection()
	Observability_Example8_ResilienceObservability()
	Observability_Example9_ProductionConfig()

	fmt.Println("========================================")
	fmt.Println("Key Takeaways:")
	fmt.Println("========================================")
	fmt.Println("1. Use structured JSON logging for production")
	fmt.Println("2. Include trace IDs for distributed tracing")
	fmt.Println("3. Create specialized loggers per component")
	fmt.Println("4. Record metrics for dashboards and alerting")
	fmt.Println("5. Track resilience patterns (retries, circuit breakers)")
	fmt.Println("6. Configure appropriately for production vs development")
}

func main() {
	RunObservabilityExamples()
}
