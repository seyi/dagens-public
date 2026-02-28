// Package observability provides monitoring and metrics for the AI agents framework.
// This includes Prometheus metrics export, structured logging, and tracing integration.
package observability

import (
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds all Prometheus metrics for the framework
type Metrics struct {
	// Agent execution metrics
	AgentExecutions      *prometheus.CounterVec
	AgentExecutionTime   *prometheus.HistogramVec
	AgentExecutionErrors *prometheus.CounterVec
	AgentActiveRequests  *prometheus.GaugeVec
	AgentRetries         *prometheus.CounterVec

	// LLM provider metrics
	LLMRequests        *prometheus.CounterVec
	LLMRequestDuration *prometheus.HistogramVec
	LLMTokensUsed      *prometheus.CounterVec
	LLMRateLimitHits   *prometheus.CounterVec

	// Circuit breaker metrics
	CircuitBreakerState    *prometheus.GaugeVec
	CircuitBreakerFailures *prometheus.CounterVec

	// Rate limiter metrics
	RateLimiterAllowed  *prometheus.CounterVec
	RateLimiterRejected *prometheus.CounterVec

	// State management metrics
	CheckpointsSaved   *prometheus.CounterVec
	CheckpointsLoaded  *prometheus.CounterVec
	SessionsActive     *prometheus.GaugeVec
	StateOperationTime *prometheus.HistogramVec

	// Scheduler metrics
	DAGStagesTotal    *prometheus.CounterVec
	DAGStagesDuration *prometheus.HistogramVec
	TaskQueueLength   *prometheus.GaugeVec
	TaskExecutionTime *prometheus.HistogramVec

	// Coordination metrics
	BarrierWaitSeconds *prometheus.HistogramVec
	GenerationCreated  *prometheus.CounterVec
	TripLatency        *prometheus.HistogramVec
	BarrierTripTotal   *prometheus.CounterVec

	// System metrics
	GoroutinesActive prometheus.Gauge
	MemoryUsageBytes prometheus.Gauge
}

var (
	globalMetrics *Metrics
	once          sync.Once
)

// GetMetrics returns the global metrics instance
func GetMetrics() *Metrics {
	once.Do(func() {
		globalMetrics = NewMetrics("spark_agent")
	})
	return globalMetrics
}

// NewMetrics creates a new Metrics instance with the given namespace
func NewMetrics(namespace string) *Metrics {
	m := &Metrics{}

	// Agent execution metrics
	m.AgentExecutions = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "executions_total",
			Help:      "Total number of agent executions",
		},
		[]string{"agent_name", "agent_type", "status"},
	)

	m.AgentExecutionTime = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "execution_duration_seconds",
			Help:      "Agent execution duration in seconds",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		},
		[]string{"agent_name", "agent_type"},
	)

	m.AgentExecutionErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "execution_errors_total",
			Help:      "Total number of agent execution errors",
		},
		[]string{"agent_name", "agent_type", "error_type"},
	)

	m.AgentActiveRequests = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "active_requests",
			Help:      "Number of active agent requests",
		},
		[]string{"agent_name"},
	)

	m.AgentRetries = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "retries_total",
			Help:      "Total number of agent execution retries",
		},
		[]string{"agent_name"},
	)

	// LLM provider metrics
	m.LLMRequests = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "llm_requests_total",
			Help:      "Total number of LLM API requests",
		},
		[]string{"provider", "model", "status"},
	)

	m.LLMRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "llm_request_duration_seconds",
			Help:      "LLM API request duration in seconds",
			Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
		},
		[]string{"provider", "model"},
	)

	m.LLMTokensUsed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "llm_tokens_total",
			Help:      "Total tokens used in LLM requests",
		},
		[]string{"provider", "model", "type"}, // type: input, output
	)

	m.LLMRateLimitHits = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "llm_rate_limit_hits_total",
			Help:      "Total number of LLM API rate limit hits",
		},
		[]string{"provider", "model"},
	)

	// Circuit breaker metrics
	m.CircuitBreakerState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "circuit_breaker_state",
			Help:      "Circuit breaker state (0=closed, 1=open, 2=half-open)",
		},
		[]string{"name"},
	)

	m.CircuitBreakerFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "circuit_breaker_failures_total",
			Help:      "Total number of circuit breaker failures",
		},
		[]string{"name"},
	)

	// Rate limiter metrics
	m.RateLimiterAllowed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "rate_limiter_allowed_total",
			Help:      "Total number of requests allowed by rate limiter",
		},
		[]string{"name"},
	)

	m.RateLimiterRejected = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "rate_limiter_rejected_total",
			Help:      "Total number of requests rejected by rate limiter",
		},
		[]string{"name"},
	)

	// State management metrics
	m.CheckpointsSaved = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "checkpoints_saved_total",
			Help:      "Total number of checkpoints saved",
		},
		[]string{"agent_id"},
	)

	m.CheckpointsLoaded = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "checkpoints_loaded_total",
			Help:      "Total number of checkpoints loaded",
		},
		[]string{"agent_id"},
	)

	m.SessionsActive = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "sessions_active",
			Help:      "Number of active sessions",
		},
		[]string{"agent_id"},
	)

	m.StateOperationTime = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "state_operation_duration_seconds",
			Help:      "State operation duration in seconds",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		},
		[]string{"operation", "backend"},
	)

	// Scheduler metrics
	m.DAGStagesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "dag_stages_total",
			Help:      "Total number of DAG stages executed",
		},
		[]string{"status"},
	)

	m.DAGStagesDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "dag_stage_duration_seconds",
			Help:      "DAG stage execution duration in seconds",
			Buckets:   []float64{0.1, 0.5, 1, 5, 10, 30, 60, 120},
		},
		[]string{"stage_id"},
	)

	m.TaskQueueLength = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "task_queue_length",
			Help:      "Current length of task queue",
		},
		[]string{"scheduler"},
	)

	m.TaskExecutionTime = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "task_execution_duration_seconds",
			Help:      "Task execution duration in seconds",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30},
		},
		[]string{"locality"},
	)

	// Coordination metrics
	m.BarrierWaitSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "barrier_wait_seconds",
			Help:      "Time spent waiting at distributed barriers in seconds",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		},
		[]string{"barrier", "status"},
	)

	m.GenerationCreated = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "generation_created",
			Help:      "Total number of distributed barrier generations created",
		},
		[]string{"barrier"},
	)

	m.TripLatency = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "trip_latency",
			Help:      "Latency of distributed barrier trip transaction attempts in seconds",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		},
		[]string{"barrier"},
	)

	m.BarrierTripTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "barrier_trip_total",
			Help:      "Total number of barrier trip transaction outcomes",
		},
		[]string{"barrier", "status"},
	)

	// System metrics
	m.GoroutinesActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "goroutines_active",
			Help:      "Number of active goroutines",
		},
	)

	m.MemoryUsageBytes = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "memory_usage_bytes",
			Help:      "Current memory usage in bytes",
		},
	)

	return m
}

// RecordAgentExecution records an agent execution
func (m *Metrics) RecordAgentExecution(agentName, agentType, status string, duration time.Duration) {
	m.AgentExecutions.WithLabelValues(agentName, agentType, status).Inc()
	m.AgentExecutionTime.WithLabelValues(agentName, agentType).Observe(duration.Seconds())
}

// RecordAgentError records an agent execution error (simple version)
func (m *Metrics) RecordAgentError(agentName, errorType string) {
	m.AgentExecutionErrors.WithLabelValues(agentName, "resilient", errorType).Inc()
}

// RecordAgentErrorTyped records an agent execution error with type
func (m *Metrics) RecordAgentErrorTyped(agentName, agentType, errorType string) {
	m.AgentExecutionErrors.WithLabelValues(agentName, agentType, errorType).Inc()
}

// RecordAgentRetry records an agent retry attempt
func (m *Metrics) RecordAgentRetry(agentName string) {
	m.AgentRetries.WithLabelValues(agentName).Inc()
}

// RecordLLMRequest records an LLM API request
func (m *Metrics) RecordLLMRequest(provider, model, status string, duration time.Duration) {
	m.LLMRequests.WithLabelValues(provider, model, status).Inc()
	m.LLMRequestDuration.WithLabelValues(provider, model).Observe(duration.Seconds())
}

// RecordLLMTokens records token usage
func (m *Metrics) RecordLLMTokens(provider, model string, inputTokens, outputTokens int) {
	m.LLMTokensUsed.WithLabelValues(provider, model, "input").Add(float64(inputTokens))
	m.LLMTokensUsed.WithLabelValues(provider, model, "output").Add(float64(outputTokens))
}

// RecordCircuitBreakerState records circuit breaker state
func (m *Metrics) RecordCircuitBreakerState(name string, state int) {
	m.CircuitBreakerState.WithLabelValues(name).Set(float64(state))
}

// RecordCircuitBreakerFailure records a circuit breaker failure
func (m *Metrics) RecordCircuitBreakerFailure(name string) {
	m.CircuitBreakerFailures.WithLabelValues(name).Inc()
}

// RecordRateLimiterAllowed records an allowed request
func (m *Metrics) RecordRateLimiterAllowed(name string) {
	m.RateLimiterAllowed.WithLabelValues(name).Inc()
}

// RecordRateLimiterRejected records a rejected request
func (m *Metrics) RecordRateLimiterRejected(name string) {
	m.RateLimiterRejected.WithLabelValues(name).Inc()
}

// RecordCheckpointSaved records a checkpoint save
func (m *Metrics) RecordCheckpointSaved(agentID string) {
	m.CheckpointsSaved.WithLabelValues(agentID).Inc()
}

// RecordCheckpointLoaded records a checkpoint load
func (m *Metrics) RecordCheckpointLoaded(agentID string) {
	m.CheckpointsLoaded.WithLabelValues(agentID).Inc()
}

// RecordStateOperation records a state operation
func (m *Metrics) RecordStateOperation(operation, backend string, duration time.Duration) {
	m.StateOperationTime.WithLabelValues(operation, backend).Observe(duration.Seconds())
}

// RecordDAGStage records a DAG stage execution
func (m *Metrics) RecordDAGStage(stageID, status string, duration time.Duration) {
	m.DAGStagesTotal.WithLabelValues(status).Inc()
	m.DAGStagesDuration.WithLabelValues(stageID).Observe(duration.Seconds())
}

// RecordTaskExecution records a task execution
func (m *Metrics) RecordTaskExecution(locality string, duration time.Duration) {
	m.TaskExecutionTime.WithLabelValues(locality).Observe(duration.Seconds())
}

// RecordBarrierWait records time spent waiting at a barrier.
func (m *Metrics) RecordBarrierWait(barrierKey, status string, duration time.Duration) {
	m.BarrierWaitSeconds.WithLabelValues(barrierKey, status).Observe(duration.Seconds())
}

// RecordBarrierGenerationCreated records creation of a new barrier generation.
func (m *Metrics) RecordBarrierGenerationCreated(barrierKey string) {
	m.GenerationCreated.WithLabelValues(barrierKey).Inc()
}

// RecordBarrierTripLatency records trip transaction latency.
func (m *Metrics) RecordBarrierTripLatency(barrierKey string, duration time.Duration) {
	m.TripLatency.WithLabelValues(barrierKey).Observe(duration.Seconds())
}

// RecordBarrierTrip records barrier trip transaction outcome.
func (m *Metrics) RecordBarrierTrip(barrierKey, status string) {
	m.BarrierTripTotal.WithLabelValues(barrierKey, status).Inc()
}

// SetTaskQueueLength sets the task queue length
func (m *Metrics) SetTaskQueueLength(scheduler string, length int) {
	m.TaskQueueLength.WithLabelValues(scheduler).Set(float64(length))
}

// SetActiveRequests sets the number of active requests for an agent
func (m *Metrics) SetActiveRequests(agentName string, count int) {
	m.AgentActiveRequests.WithLabelValues(agentName).Set(float64(count))
}

// SetActiveSessions sets the number of active sessions
func (m *Metrics) SetActiveSessions(agentID string, count int) {
	m.SessionsActive.WithLabelValues(agentID).Set(float64(count))
}

// Handler returns an HTTP handler for Prometheus metrics
func Handler() http.Handler {
	return promhttp.Handler()
}

// ExecutionTimer helps measure execution time
type ExecutionTimer struct {
	start     time.Time
	metrics   *Metrics
	agentName string
	agentType string
}

// NewExecutionTimer starts a new execution timer
func NewExecutionTimer(metrics *Metrics, agentName, agentType string) *ExecutionTimer {
	metrics.AgentActiveRequests.WithLabelValues(agentName).Inc()
	return &ExecutionTimer{
		start:     time.Now(),
		metrics:   metrics,
		agentName: agentName,
		agentType: agentType,
	}
}

// Finish completes the timer and records the metric
func (t *ExecutionTimer) Finish(status string) time.Duration {
	duration := time.Since(t.start)
	t.metrics.RecordAgentExecution(t.agentName, t.agentType, status, duration)
	t.metrics.AgentActiveRequests.WithLabelValues(t.agentName).Dec()
	return duration
}

// FinishWithError completes the timer with an error
func (t *ExecutionTimer) FinishWithError(errorType string) time.Duration {
	duration := time.Since(t.start)
	t.metrics.RecordAgentExecution(t.agentName, t.agentType, "error", duration)
	t.metrics.RecordAgentErrorTyped(t.agentName, t.agentType, errorType)
	t.metrics.AgentActiveRequests.WithLabelValues(t.agentName).Dec()
	return duration
}
