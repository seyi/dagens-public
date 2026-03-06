// Package observability provides monitoring and metrics for Dagens.
// This includes Prometheus metrics export, structured logging, and tracing integration.
//
// Label cardinality guidance:
// - `agent_name`, `agent_type`, and `scheduler` should be stable low-cardinality identifiers.
// - Avoid dynamic values such as request IDs, payload hashes, or timestamps in labels.
package observability

import (
	"net/http"
	"os"
	"strings"
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
	DAGStagesTotal                       *prometheus.CounterVec
	DAGStagesDuration                    *prometheus.HistogramVec
	TaskQueueLength                      *prometheus.GaugeVec
	TaskQueueConfigMax                   *prometheus.GaugeVec
	TaskQueueObservedMax                 *prometheus.GaugeVec
	TaskExecutionTime                    *prometheus.HistogramVec
	TaskDispatchRetries                  prometheus.Counter
	TasksFailedMaxDispatchAttempts       prometheus.Counter
	SchedulerAllWorkersFull              prometheus.Counter
	SchedulerCapacityDeferrals           prometheus.Counter
	SchedulerCapacityDeferralPolls       prometheus.Counter
	SchedulerDegradedMode                prometheus.Counter
	SchedulerAffinityHits                prometheus.Counter
	SchedulerAffinityMisses              prometheus.Counter
	SchedulerAffinityStale               prometheus.Counter
	SchedulerDispatchCooldownActivations prometheus.Counter
	SchedulerDispatchRejections          *prometheus.CounterVec
	SchedulerRecoveryRuns                *prometheus.CounterVec
	SchedulerRecoveredJobs               prometheus.Counter
	SchedulerRecoveryResumedQueuedJobs   prometheus.Counter
	SchedulerRecoveryResumeSkippedQueued prometheus.Counter
	SchedulerRecoveryResumeSkippedUnsafe prometheus.Counter
	SchedulerRecoveryDuration            prometheus.Histogram
	WorkerHeartbeatsReceived             prometheus.Counter
	WorkerHeartbeatsSucceeded            prometheus.Counter
	WorkerHeartbeatAuthFailed            prometheus.Counter
	WorkerHeartbeatInvalidPayload        prometheus.Counter
	WorkerHeartbeatProcessingTime        prometheus.Histogram

	// Coordination metrics
	BarrierWaitSeconds *prometheus.HistogramVec
	GenerationCreated  *prometheus.CounterVec
	TripLatency        *prometheus.HistogramVec
	BarrierTripTotal   *prometheus.CounterVec

	// System metrics
	GoroutinesActive prometheus.Gauge
	MemoryUsageBytes prometheus.Gauge

	queueDepthMu     sync.Mutex
	queueObservedMax map[string]int
}

var (
	globalMetrics *Metrics
	once          sync.Once
)

const defaultMetricsNamespace = "dagens"

func resolveMetricsNamespace() string {
	namespace := strings.TrimSpace(os.Getenv("METRICS_NAMESPACE"))
	if namespace == "" {
		return defaultMetricsNamespace
	}
	return namespace
}

// GetMetrics returns the global metrics instance
func GetMetrics() *Metrics {
	once.Do(func() {
		globalMetrics = NewMetrics(resolveMetricsNamespace())
	})
	return globalMetrics
}

// NewMetrics creates a new Metrics instance with the given namespace
func NewMetrics(namespace string) *Metrics {
	return NewMetricsWithRegistry(namespace, prometheus.DefaultRegisterer)
}

// NewMetricsWithRegistry creates a new Metrics instance using the provided
// Prometheus registerer. This enables test-safe isolated registries.
func NewMetricsWithRegistry(namespace string, reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}
	auto := promauto.With(reg)
	m := &Metrics{
		queueObservedMax: make(map[string]int),
	}

	// Agent execution metrics
	m.AgentExecutions = auto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "executions_total",
			Help:      "Total number of agent executions",
		},
		[]string{"agent_name", "agent_type", "status"},
	)

	m.AgentExecutionTime = auto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "execution_duration_seconds",
			Help:      "Agent execution duration in seconds",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		},
		[]string{"agent_name", "agent_type"},
	)

	m.AgentExecutionErrors = auto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "execution_errors_total",
			Help:      "Total number of agent execution errors",
		},
		[]string{"agent_name", "agent_type", "error_type"},
	)

	m.AgentActiveRequests = auto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "active_requests",
			Help:      "Number of active agent requests",
		},
		[]string{"agent_name"},
	)

	m.AgentRetries = auto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "retries_total",
			Help:      "Total number of agent execution retries",
		},
		[]string{"agent_name"},
	)

	// LLM provider metrics
	m.LLMRequests = auto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "llm_requests_total",
			Help:      "Total number of LLM API requests",
		},
		[]string{"provider", "model", "status"},
	)

	m.LLMRequestDuration = auto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "llm_request_duration_seconds",
			Help:      "LLM API request duration in seconds",
			Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
		},
		[]string{"provider", "model"},
	)

	m.LLMTokensUsed = auto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "llm_tokens_total",
			Help:      "Total tokens used in LLM requests",
		},
		[]string{"provider", "model", "type"}, // type: input, output
	)

	m.LLMRateLimitHits = auto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "llm_rate_limit_hits_total",
			Help:      "Total number of LLM API rate limit hits",
		},
		[]string{"provider", "model"},
	)

	// Circuit breaker metrics
	m.CircuitBreakerState = auto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "circuit_breaker_state",
			Help:      "Circuit breaker state (0=closed, 1=open, 2=half-open)",
		},
		[]string{"name"},
	)

	m.CircuitBreakerFailures = auto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "circuit_breaker_failures_total",
			Help:      "Total number of circuit breaker failures",
		},
		[]string{"name"},
	)

	// Rate limiter metrics
	m.RateLimiterAllowed = auto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "rate_limiter_allowed_total",
			Help:      "Total number of requests allowed by rate limiter",
		},
		[]string{"name"},
	)

	m.RateLimiterRejected = auto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "rate_limiter_rejected_total",
			Help:      "Total number of requests rejected by rate limiter",
		},
		[]string{"name"},
	)

	// State management metrics
	m.CheckpointsSaved = auto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "checkpoints_saved_total",
			Help:      "Total number of checkpoints saved",
		},
		[]string{"agent_id"},
	)

	m.CheckpointsLoaded = auto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "checkpoints_loaded_total",
			Help:      "Total number of checkpoints loaded",
		},
		[]string{"agent_id"},
	)

	m.SessionsActive = auto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "sessions_active",
			Help:      "Number of active sessions",
		},
		[]string{"agent_id"},
	)

	m.StateOperationTime = auto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "state_operation_duration_seconds",
			Help:      "State operation duration in seconds",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		},
		[]string{"operation", "backend"},
	)

	// Scheduler metrics
	m.DAGStagesTotal = auto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "dag_stages_total",
			Help:      "Total number of DAG stages executed",
		},
		[]string{"status"},
	)

	m.DAGStagesDuration = auto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "dag_stage_duration_seconds",
			Help:      "DAG stage execution duration in seconds",
			Buckets:   []float64{0.1, 0.5, 1, 5, 10, 30, 60, 120},
		},
		[]string{"stage_id"},
	)

	m.TaskQueueLength = auto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "task_queue_length",
			Help:      "Current length of task queue",
		},
		[]string{"scheduler"},
	)

	m.TaskQueueConfigMax = auto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "task_queue_config_max",
			Help:      "Configured maximum task queue depth",
		},
		[]string{"scheduler"},
	)

	m.TaskQueueObservedMax = auto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "task_queue_observed_max",
			Help:      "Observed high-water mark for task queue depth over process lifetime",
		},
		[]string{"scheduler"},
	)

	m.TaskExecutionTime = auto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "task_execution_duration_seconds",
			Help:      "Task execution duration in seconds",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.5, 1, 5, 10, 30},
		},
		[]string{"locality"},
	)

	m.TaskDispatchRetries = auto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "task_dispatch_retries_total",
			Help:      "Total number of capacity-conflict task redispatch attempts",
		},
	)

	m.TasksFailedMaxDispatchAttempts = auto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "tasks_failed_max_dispatch_attempts_total",
			Help:      "Total number of tasks that failed after exhausting max dispatch attempts due to capacity conflicts",
		},
	)

	m.SchedulerAllWorkersFull = auto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "scheduler_all_workers_full_total",
			Help:      "Total number of times scheduling could not proceed because all healthy workers were at capacity",
		},
	)

	m.SchedulerCapacityDeferrals = auto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "scheduler_capacity_deferrals_total",
			Help:      "Total number of times stage scheduling entered deferred capacity wait before placement",
		},
	)

	m.SchedulerCapacityDeferralPolls = auto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "scheduler_capacity_deferral_polls_total",
			Help:      "Total number of polling iterations while waiting for deferred stage capacity",
		},
	)

	m.SchedulerDegradedMode = auto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "scheduler_degraded_mode_total",
			Help:      "Total number of times the scheduler fell back to stale capacity snapshots because no fresh-capacity worker was available",
		},
	)

	m.SchedulerAffinityHits = auto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "scheduler_affinity_hits_total",
			Help:      "Total number of sticky scheduling affinity hits",
		},
	)

	m.SchedulerAffinityMisses = auto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "scheduler_affinity_misses_total",
			Help:      "Total number of sticky scheduling affinity misses",
		},
	)

	m.SchedulerAffinityStale = auto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "scheduler_affinity_stale_total",
			Help:      "Total number of stale sticky scheduling affinity entries encountered",
		},
	)

	m.SchedulerDispatchCooldownActivations = auto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "scheduler_dispatch_cooldown_activations_total",
			Help:      "Total number of times a worker entered dispatch cooldown after a capacity-conflict style rejection",
		},
	)

	m.SchedulerDispatchRejections = auto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "scheduler_dispatch_rejections_total",
			Help:      "Total number of scheduler dispatch rejections by reason",
		},
		[]string{"reason"},
	)

	m.SchedulerRecoveryRuns = auto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "scheduler_recovery_runs_total",
			Help:      "Total number of scheduler startup recovery runs by status",
		},
		[]string{"status"},
	)

	m.SchedulerRecoveredJobs = auto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "scheduler_recovered_jobs_total",
			Help:      "Total number of jobs reconstructed into scheduler in-memory state during startup recovery",
		},
	)

	m.SchedulerRecoveryResumedQueuedJobs = auto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "scheduler_recovery_resumed_queued_jobs_total",
			Help:      "Total number of recovered QUEUED jobs re-enqueued during startup recovery",
		},
	)

	m.SchedulerRecoveryResumeSkippedQueued = auto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "scheduler_recovery_resume_skipped_queued_jobs_total",
			Help:      "Total number of recovered QUEUED jobs not resumed because the scheduler queue was full",
		},
	)

	m.SchedulerRecoveryResumeSkippedUnsafe = auto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "scheduler_recovery_resume_skipped_unsafe_queued_jobs_total",
			Help:      "Total number of recovered QUEUED jobs not resumed due to non-pending task lifecycle states",
		},
	)

	m.SchedulerRecoveryDuration = auto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "scheduler_recovery_duration_seconds",
			Help:      "Scheduler startup recovery duration in seconds",
			Buckets:   []float64{0.001, 0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		},
	)

	m.WorkerHeartbeatsReceived = auto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "worker_heartbeats_received_total",
			Help:      "Total number of worker capacity heartbeat requests received",
		},
	)

	m.WorkerHeartbeatsSucceeded = auto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "worker_heartbeats_succeeded_total",
			Help:      "Total number of worker capacity heartbeats processed successfully",
		},
	)

	m.WorkerHeartbeatAuthFailed = auto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "worker_heartbeat_auth_failed_total",
			Help:      "Total number of worker capacity heartbeats rejected due to authentication failures",
		},
	)

	m.WorkerHeartbeatInvalidPayload = auto.NewCounter(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "worker_heartbeat_invalid_payload_total",
			Help:      "Total number of worker capacity heartbeats rejected due to invalid payloads",
		},
	)

	m.WorkerHeartbeatProcessingTime = auto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "worker_heartbeat_processing_duration_seconds",
			Help:      "Worker capacity heartbeat processing duration in seconds",
			Buckets:   []float64{0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5},
		},
	)

	// Coordination metrics
	m.BarrierWaitSeconds = auto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "barrier_wait_seconds",
			Help:      "Time spent waiting at distributed barriers in seconds",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		},
		[]string{"barrier", "status"},
	)

	m.GenerationCreated = auto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "generation_created",
			Help:      "Total number of distributed barrier generations created",
		},
		[]string{"barrier"},
	)

	m.TripLatency = auto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "trip_latency",
			Help:      "Latency of distributed barrier trip transaction attempts in seconds",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		},
		[]string{"barrier"},
	)

	m.BarrierTripTotal = auto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "barrier_trip_total",
			Help:      "Total number of barrier trip transaction outcomes",
		},
		[]string{"barrier", "status"},
	)

	// System metrics
	m.GoroutinesActive = auto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "goroutines_active",
			Help:      "Number of active goroutines",
		},
	)

	m.MemoryUsageBytes = auto.NewGauge(
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
// Deprecated: prefer RecordAgentErrorTyped so dashboards preserve true agent_type.
func (m *Metrics) RecordAgentError(agentName, errorType string) {
	m.RecordAgentErrorTyped(agentName, "unknown", errorType)
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

// RecordTaskDispatchRetry records a capacity-conflict redispatch attempt.
func (m *Metrics) RecordTaskDispatchRetry() {
	m.TaskDispatchRetries.Inc()
}

// RecordTaskFailedMaxDispatchAttempts records a task failing after exhausting
// the configured dispatch retry budget.
func (m *Metrics) RecordTaskFailedMaxDispatchAttempts() {
	m.TasksFailedMaxDispatchAttempts.Inc()
}

// RecordSchedulerAllWorkersFull records a scheduling refusal due to no worker capacity.
func (m *Metrics) RecordSchedulerAllWorkersFull() {
	m.SchedulerAllWorkersFull.Inc()
}

// RecordSchedulerCapacityDeferral records entering stage-level deferred
// capacity waiting.
func (m *Metrics) RecordSchedulerCapacityDeferral() {
	m.SchedulerCapacityDeferrals.Inc()
}

// RecordSchedulerCapacityDeferralPoll records one polling iteration while
// waiting for deferred stage capacity.
func (m *Metrics) RecordSchedulerCapacityDeferralPoll() {
	m.SchedulerCapacityDeferralPolls.Inc()
}

// RecordSchedulerDegradedMode records a stale-capacity fallback selection.
func (m *Metrics) RecordSchedulerDegradedMode() {
	m.SchedulerDegradedMode.Inc()
}

// RecordSchedulerAffinityHit records a sticky scheduling affinity hit.
func (m *Metrics) RecordSchedulerAffinityHit() {
	m.SchedulerAffinityHits.Inc()
}

// RecordSchedulerAffinityMiss records a sticky scheduling affinity miss.
func (m *Metrics) RecordSchedulerAffinityMiss() {
	m.SchedulerAffinityMisses.Inc()
}

// RecordSchedulerAffinityStale records a stale sticky affinity reroute.
func (m *Metrics) RecordSchedulerAffinityStale() {
	m.SchedulerAffinityStale.Inc()
}

// RecordSchedulerDispatchCooldownActivation records a worker entering cooldown.
func (m *Metrics) RecordSchedulerDispatchCooldownActivation() {
	m.SchedulerDispatchCooldownActivations.Inc()
}

// RecordSchedulerDispatchRejection records a dispatch rejection by reason.
func (m *Metrics) RecordSchedulerDispatchRejection(reason string) {
	m.SchedulerDispatchRejections.WithLabelValues(reason).Inc()
}

// RecordSchedulerRecoveryRun records one scheduler startup recovery attempt by status.
func (m *Metrics) RecordSchedulerRecoveryRun(status string) {
	m.SchedulerRecoveryRuns.WithLabelValues(status).Inc()
}

// RecordSchedulerRecoveredJobs records how many jobs were reconstructed by startup recovery.
func (m *Metrics) RecordSchedulerRecoveredJobs(count int) {
	if count <= 0 {
		return
	}
	m.SchedulerRecoveredJobs.Add(float64(count))
}

// RecordSchedulerRecoveryResumedQueuedJobs records how many recovered QUEUED
// jobs were re-enqueued during startup recovery.
func (m *Metrics) RecordSchedulerRecoveryResumedQueuedJobs(count int) {
	if count <= 0 {
		return
	}
	m.SchedulerRecoveryResumedQueuedJobs.Add(float64(count))
}

// RecordSchedulerRecoveryResumeSkippedQueuedJobs records how many recovered
// QUEUED jobs were skipped due to startup queue saturation.
func (m *Metrics) RecordSchedulerRecoveryResumeSkippedQueuedJobs(count int) {
	if count <= 0 {
		return
	}
	m.SchedulerRecoveryResumeSkippedQueued.Add(float64(count))
}

// RecordSchedulerRecoveryResumeSkippedUnsafeQueuedJobs records how many
// recovered QUEUED jobs were skipped due to unsafe (non-pending) task states.
func (m *Metrics) RecordSchedulerRecoveryResumeSkippedUnsafeQueuedJobs(count int) {
	if count <= 0 {
		return
	}
	m.SchedulerRecoveryResumeSkippedUnsafe.Add(float64(count))
}

// RecordSchedulerRecoveryDuration records scheduler startup recovery duration.
func (m *Metrics) RecordSchedulerRecoveryDuration(duration time.Duration) {
	m.SchedulerRecoveryDuration.Observe(duration.Seconds())
}

// RecordWorkerHeartbeatReceived records that a worker heartbeat request was received.
func (m *Metrics) RecordWorkerHeartbeatReceived() {
	m.WorkerHeartbeatsReceived.Inc()
}

// RecordWorkerHeartbeatSucceeded records successful processing of a worker heartbeat.
func (m *Metrics) RecordWorkerHeartbeatSucceeded() {
	m.WorkerHeartbeatsSucceeded.Inc()
}

// RecordWorkerHeartbeatAuthFailed records a worker heartbeat auth failure.
func (m *Metrics) RecordWorkerHeartbeatAuthFailed() {
	m.WorkerHeartbeatAuthFailed.Inc()
}

// RecordWorkerHeartbeatInvalidPayload records a worker heartbeat payload validation failure.
func (m *Metrics) RecordWorkerHeartbeatInvalidPayload() {
	m.WorkerHeartbeatInvalidPayload.Inc()
}

// RecordWorkerHeartbeatProcessing records the duration of processing a worker heartbeat request.
func (m *Metrics) RecordWorkerHeartbeatProcessing(duration time.Duration) {
	m.WorkerHeartbeatProcessingTime.Observe(duration.Seconds())
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

// SetTaskQueueDepths sets queue depth gauges and tracks the process-lifetime
// observed high-water mark for a scheduler.
func (m *Metrics) SetTaskQueueDepths(scheduler string, current, configMax int) {
	m.TaskQueueLength.WithLabelValues(scheduler).Set(float64(current))
	m.TaskQueueConfigMax.WithLabelValues(scheduler).Set(float64(configMax))

	m.queueDepthMu.Lock()
	defer m.queueDepthMu.Unlock()

	if current > m.queueObservedMax[scheduler] {
		m.queueObservedMax[scheduler] = current
	}
	m.TaskQueueObservedMax.WithLabelValues(scheduler).Set(float64(m.queueObservedMax[scheduler]))
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
