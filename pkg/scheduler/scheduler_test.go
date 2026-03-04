package scheduler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/observability"
	"github.com/seyi/dagens/pkg/registry"
	"github.com/seyi/dagens/pkg/telemetry"
)

func TestSubmitJobReturnsQueueFullWithoutRetainingRejectedJob(t *testing.T) {
	_ = schedulerMetrics()

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		JobQueueSize: 1,
	})

	first := NewJob("job-1", "first")
	if err := s.SubmitJob(first); err != nil {
		t.Fatalf("SubmitJob(first) unexpected error: %v", err)
	}

	second := NewJob("job-2", "second")
	err := s.SubmitJob(second)
	if !errors.Is(err, ErrJobQueueFull) {
		t.Fatalf("SubmitJob(second) error = %v, want %v", err, ErrJobQueueFull)
	}

	if _, err := s.GetJob(second.ID); err == nil {
		t.Fatalf("rejected job %q should not be retained in scheduler state", second.ID)
	}

	if got := len(s.GetAllJobs()); got != 1 {
		t.Fatalf("GetAllJobs length = %d, want 1", got)
	}

	if got := gaugeValue(t, "spark_agent_task_queue_config_max", "scheduler", schedulerMetricsID); got != 1 {
		t.Fatalf("task queue config max = %v, want %v", got, 1.0)
	}
	if got := gaugeValue(t, "spark_agent_task_queue_observed_max", "scheduler", schedulerMetricsID); got < 1 {
		t.Fatalf("task queue observed max = %v, want at least %v", got, 1.0)
	}
}

func TestSelectNodeByCapacityPrefersLeastBusyNode(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		DefaultWorkerMaxConcurrency: 2,
	})

	s.nodeCapacity["worker-1"] = &nodeCapacity{ReservedInFlight: 2, MaxConcurrency: 2}
	s.nodeCapacity["worker-2"] = &nodeCapacity{ReservedInFlight: 0, MaxConcurrency: 2}

	selected, ok := s.selectNodeByCapacity([]registry.NodeInfo{
		{ID: "worker-1"},
		{ID: "worker-2"},
	})
	if !ok {
		t.Fatal("expected node selection to succeed")
	}

	if selected.ID != "worker-2" {
		t.Fatalf("selected node = %q, want %q", selected.ID, "worker-2")
	}
}

func TestSelectNodeByCapacityFailsWhenAllWorkersAreFull(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		DefaultWorkerMaxConcurrency: 1,
	})

	s.nodeCapacity["worker-1"] = &nodeCapacity{ReservedInFlight: 1, MaxConcurrency: 1}
	s.nodeCapacity["worker-2"] = &nodeCapacity{ReservedInFlight: 2, MaxConcurrency: 2}

	_, ok := s.selectNodeByCapacity([]registry.NodeInfo{
		{ID: "worker-1"},
		{ID: "worker-2"},
	})
	if ok {
		t.Fatal("expected selection to fail when all workers are full")
	}
}

func TestReserveAndReleaseNodeSlotTracksInFlight(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		DefaultWorkerMaxConcurrency: 1,
	})

	s.reserveNodeSlot("worker-1")
	capacity := s.nodeCapacity["worker-1"]
	if capacity == nil {
		t.Fatal("expected node capacity to be created")
	}
	if capacity.ReservedInFlight != 1 {
		t.Fatalf("reserved in-flight after reserve = %d, want 1", capacity.ReservedInFlight)
	}

	s.releaseNodeSlot("worker-1")
	if capacity.ReservedInFlight != 0 {
		t.Fatalf("reserved in-flight after release = %d, want 0", capacity.ReservedInFlight)
	}

	s.releaseNodeSlot("worker-1")
	if capacity.ReservedInFlight != 0 {
		t.Fatalf("reserved in-flight after extra release = %d, want 0", capacity.ReservedInFlight)
	}
}

func TestUpdateNodeCapacityOverridesSnapshot(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		DefaultWorkerMaxConcurrency: 1,
		MaxWorkerConcurrencyCap:     4,
	})

	s.UpdateNodeCapacity("worker-1", 3, 5)
	capacity := s.nodeCapacity["worker-1"]
	if capacity == nil {
		t.Fatal("expected node capacity to be created")
	}
	if capacity.ReportedInFlight != 3 {
		t.Fatalf("reported in-flight = %d, want 3", capacity.ReportedInFlight)
	}
	if capacity.MaxConcurrency != 4 {
		t.Fatalf("max concurrency = %d, want 4", capacity.MaxConcurrency)
	}

	s.UpdateNodeCapacity("worker-1", -1, 0)
	if capacity.ReportedInFlight != 0 {
		t.Fatalf("reported in-flight after sanitize = %d, want 0", capacity.ReportedInFlight)
	}
	if capacity.MaxConcurrency != 1 {
		t.Fatalf("max concurrency after sanitize = %d, want 1", capacity.MaxConcurrency)
	}
}

func TestAvailableCapacityUsesHigherOfReservedAndReported(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		DefaultWorkerMaxConcurrency: 4,
	})

	capacity := &nodeCapacity{
		ReservedInFlight: 1,
		ReportedInFlight: 3,
		MaxConcurrency:   5,
	}

	if got := s.availableCapacityLocked(capacity); got != 2 {
		t.Fatalf("available capacity = %d, want 2", got)
	}

	capacity.ReportedInFlight = 0
	if got := s.availableCapacityLocked(capacity); got != 4 {
		t.Fatalf("available capacity with local reservations = %d, want 4", got)
	}
}

func TestSelectNodeByCapacityPrefersFreshSnapshotsOverStaleOnes(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		DefaultWorkerMaxConcurrency: 1,
		CapacityTTL:                 5 * time.Second,
	})

	s.nodeCapacity["worker-stale"] = &nodeCapacity{
		ReportedInFlight: 0,
		MaxConcurrency:   5,
		LastUpdated:      time.Now().Add(-10 * time.Second),
	}
	s.nodeCapacity["worker-fresh"] = &nodeCapacity{
		ReportedInFlight: 0,
		MaxConcurrency:   2,
		LastUpdated:      time.Now(),
	}

	selected, ok := s.selectNodeByCapacity([]registry.NodeInfo{
		{ID: "worker-stale"},
		{ID: "worker-fresh"},
	})
	if !ok {
		t.Fatal("expected selection to succeed")
	}
	if selected.ID != "worker-fresh" {
		t.Fatalf("selected node = %q, want %q", selected.ID, "worker-fresh")
	}
}

func TestSelectNodeByCapacityFallsBackToStaleWhenNoFreshCapacityExists(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		DefaultWorkerMaxConcurrency: 1,
		CapacityTTL:                 5 * time.Second,
	})
	_ = schedulerMetrics()
	before := metricCounterValue(t, "spark_agent_scheduler_degraded_mode_total")

	s.nodeCapacity["worker-stale"] = &nodeCapacity{
		ReportedInFlight: 0,
		MaxConcurrency:   2,
		LastUpdated:      time.Now().Add(-10 * time.Second),
	}
	s.nodeCapacity["worker-stale-2"] = &nodeCapacity{
		ReportedInFlight: 0,
		MaxConcurrency:   1,
		LastUpdated:      time.Now().Add(-10 * time.Second),
	}

	selected, ok := s.selectNodeByCapacity([]registry.NodeInfo{
		{ID: "worker-stale"},
		{ID: "worker-stale-2"},
	})
	if !ok {
		t.Fatal("expected stale fallback selection to succeed")
	}
	if selected.ID != "worker-stale" {
		t.Fatalf("selected node = %q, want %q", selected.ID, "worker-stale")
	}

	after := metricCounterValue(t, "spark_agent_scheduler_degraded_mode_total")
	if after != before+1 {
		t.Fatalf("SchedulerDegradedMode = %v, want %v", after, before+1)
	}
}

func TestSelectNodeByCapacitySkipsWorkersInDispatchCooldown(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		DefaultWorkerMaxConcurrency: 1,
		CapacityTTL:                 5 * time.Second,
		DispatchRejectCooldown:      5 * time.Second,
	})

	now := time.Now()
	s.nodeCapacity["worker-cooldown"] = &nodeCapacity{
		ReportedInFlight: 0,
		MaxConcurrency:   2,
		LastUpdated:      now,
	}
	s.nodeCapacity["worker-available"] = &nodeCapacity{
		ReportedInFlight: 0,
		MaxConcurrency:   1,
		LastUpdated:      now,
	}
	s.dispatchCooldowns["worker-cooldown"] = now.Add(5 * time.Second)

	selected, ok := s.selectNodeByCapacity([]registry.NodeInfo{
		{ID: "worker-cooldown"},
		{ID: "worker-available"},
	})
	if !ok {
		t.Fatal("expected node selection to succeed")
	}
	if selected.ID != "worker-available" {
		t.Fatalf("selected node = %q, want %q", selected.ID, "worker-available")
	}
}

func TestExecuteStageRecordsCapacityExhaustionMetric(t *testing.T) {
	s := newCapacityExhaustedScheduler()
	stage := &Stage{
		ID:    "stage-1",
		JobID: "job-1",
		Tasks: []*Task{{ID: "task-1", StageID: "stage-1", JobID: "job-1"}},
	}

	_ = schedulerMetrics()
	metrics := schedulerAllWorkersFullValue(t)
	err := s.executeStage(context.Background(), stage)
	if !errors.Is(err, ErrNoWorkerCapacity) {
		t.Fatalf("executeStage error = %v, want %v", err, ErrNoWorkerCapacity)
	}

	updated := schedulerAllWorkersFullValue(t)
	if updated != metrics+1 {
		t.Fatalf("SchedulerAllWorkersFull = %v, want %v", updated, metrics+1)
	}
}

func TestExecuteStageLogsCapacityExhaustionWarning(t *testing.T) {
	s := newCapacityExhaustedScheduler()
	stage := &Stage{
		ID:    "stage-logs",
		JobID: "job-logs",
		Tasks: []*Task{{ID: "task-1", StageID: "stage-logs", JobID: "job-logs"}},
	}

	logger := schedulerLogger()
	before := len(logger.GetLogs())

	err := s.executeStage(context.Background(), stage)
	if !errors.Is(err, ErrNoWorkerCapacity) {
		t.Fatalf("executeStage error = %v, want %v", err, ErrNoWorkerCapacity)
	}

	logs := logger.GetLogs()
	if len(logs) != before+1 {
		t.Fatalf("log count = %d, want %d", len(logs), before+1)
	}

	entry := logs[len(logs)-1]
	if entry.Message != "no worker capacity available" {
		t.Fatalf("log message = %q, want %q", entry.Message, "no worker capacity available")
	}
	if entry.Attributes["stage_id"] != stage.ID {
		t.Fatalf("stage_id = %v, want %v", entry.Attributes["stage_id"], stage.ID)
	}
	if entry.Attributes["healthy_workers"] != 1 {
		t.Fatalf("healthy_workers = %v, want %d", entry.Attributes["healthy_workers"], 1)
	}
	if entry.Attributes["started_tasks"] != 0 {
		t.Fatalf("started_tasks = %v, want %d", entry.Attributes["started_tasks"], 0)
	}
	if entry.Attributes["pending_tasks"] != len(stage.Tasks) {
		t.Fatalf("pending_tasks = %v, want %d", entry.Attributes["pending_tasks"], len(stage.Tasks))
	}
}

func TestExecuteTaskRecordsDispatchRejectionReason(t *testing.T) {
	s := NewSchedulerWithConfig(nil, &failingTaskExecutor{err: errors.New("connection refused")}, SchedulerConfig{})
	task := &Task{
		ID:        "task-dispatch-reject",
		AgentID:   "agent-1",
		AgentName: "agent",
		Input:     &agent.AgentInput{},
	}
	node := registry.NodeInfo{ID: "worker-1"}

	before := counterVecValue(t, "spark_agent_scheduler_dispatch_rejections_total", "reason", "transport_error")
	err := s.executeTask(context.Background(), task, node)
	if err == nil {
		t.Fatal("expected executeTask to fail")
	}

	after := counterVecValue(t, "spark_agent_scheduler_dispatch_rejections_total", "reason", "transport_error")
	if after != before+1 {
		t.Fatalf("transport_error dispatch rejections = %v, want %v", after, before+1)
	}
}

func TestExecuteTaskWithRetryRetriesCapacityConflictAndSucceeds(t *testing.T) {
	s := NewSchedulerWithConfig(nil, &sequenceTaskExecutor{
		errs: []error{errors.New("worker at capacity"), nil},
	}, SchedulerConfig{
		MaxDispatchAttempts:        2,
		DispatchRejectCooldown:     5 * time.Second,
		DefaultWorkerMaxConcurrency: 1,
	})

	nodes := []registry.NodeInfo{{ID: "worker-1"}, {ID: "worker-2"}}
	task := &Task{
		ID:        "task-retry-success",
		AgentID:   "agent-1",
		AgentName: "agent",
		Input:     &agent.AgentInput{},
	}

	beforeRetries := metricCounterValue(t, "spark_agent_task_dispatch_retries_total")
	err := s.executeTaskWithRetry(context.Background(), task, nodes[0], nodes)
	if err != nil {
		t.Fatalf("executeTaskWithRetry error = %v, want nil", err)
	}
	if task.Attempts != 2 {
		t.Fatalf("task.Attempts = %d, want %d", task.Attempts, 2)
	}

	afterRetries := metricCounterValue(t, "spark_agent_task_dispatch_retries_total")
	if afterRetries != beforeRetries+1 {
		t.Fatalf("task dispatch retries = %v, want %v", afterRetries, beforeRetries+1)
	}
}

func TestExecuteTaskWithRetryFailsAtMaxDispatchAttempts(t *testing.T) {
	s := NewSchedulerWithConfig(nil, &sequenceTaskExecutor{
		errs: []error{errors.New("worker at capacity"), errors.New("worker at capacity")},
	}, SchedulerConfig{
		MaxDispatchAttempts:        2,
		DispatchRejectCooldown:     5 * time.Second,
		DefaultWorkerMaxConcurrency: 1,
	})

	nodes := []registry.NodeInfo{{ID: "worker-1"}, {ID: "worker-2"}}
	task := &Task{
		ID:        "task-retry-fail",
		AgentID:   "agent-1",
		AgentName: "agent",
		Input:     &agent.AgentInput{},
	}

	beforeFailures := metricCounterValue(t, "spark_agent_tasks_failed_max_dispatch_attempts_total")
	err := s.executeTaskWithRetry(context.Background(), task, nodes[0], nodes)
	if err == nil {
		t.Fatal("expected executeTaskWithRetry to fail")
	}
	if task.Attempts != 2 {
		t.Fatalf("task.Attempts = %d, want %d", task.Attempts, 2)
	}

	afterFailures := metricCounterValue(t, "spark_agent_tasks_failed_max_dispatch_attempts_total")
	if afterFailures != beforeFailures+1 {
		t.Fatalf("tasks failed max dispatch attempts = %v, want %v", afterFailures, beforeFailures+1)
	}
}

func newCapacityExhaustedScheduler() *Scheduler {
	s := NewSchedulerWithConfig(capacityExhaustedRegistry{}, nil, SchedulerConfig{
		DefaultWorkerMaxConcurrency: 1,
	})
	s.nodeCapacity["worker-1"] = &nodeCapacity{ReservedInFlight: 1, MaxConcurrency: 1}
	return s
}

func schedulerMetrics() *observability.Metrics {
	return observability.GetMetrics()
}

func schedulerAllWorkersFullValue(t *testing.T) float64 {
	return metricCounterValue(t, "spark_agent_scheduler_all_workers_full_total")
}

func metricCounterValue(t *testing.T, metricName string) float64 {
	t.Helper()
	return counterVecValue(t, metricName)
}

func counterVecValue(t *testing.T, metricName string, labels ...string) float64 {
	t.Helper()

	metricFamilies, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	for _, family := range metricFamilies {
		if family.GetName() != metricName && family.GetName() != strings.TrimSuffix(metricName, "_total") {
			continue
		}
		for _, metric := range family.GetMetric() {
			if metricMatchesLabels(metric, labels...) {
				if metric.GetCounter() == nil {
					t.Fatalf("metric %q missing counter value", family.GetName())
				}
				return metric.GetCounter().GetValue()
			}
		}
		return 0
	}

	return 0
}

func gaugeValue(t *testing.T, metricName string, labels ...string) float64 {
	t.Helper()

	metricFamilies, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}

	for _, family := range metricFamilies {
		if family.GetName() != metricName {
			continue
		}
		for _, metric := range family.GetMetric() {
			if metricMatchesLabels(metric, labels...) {
				if metric.GetGauge() == nil {
					t.Fatalf("metric %q missing gauge value", family.GetName())
				}
				return metric.GetGauge().GetValue()
			}
		}
		return 0
	}

	return 0
}

func metricMatchesLabels(metric *dto.Metric, labels ...string) bool {
	if len(labels) == 0 {
		return true
	}
	if len(labels)%2 != 0 {
		return false
	}

	labelMap := make(map[string]string, len(metric.GetLabel()))
	for _, label := range metric.GetLabel() {
		labelMap[label.GetName()] = label.GetValue()
	}
	for i := 0; i < len(labels); i += 2 {
		if labelMap[labels[i]] != labels[i+1] {
			return false
		}
	}
	return true
}

func schedulerLogger() *telemetry.InMemoryLogger {
	logger, ok := telemetry.GetGlobalTelemetry().GetLogger().(*telemetry.InMemoryLogger)
	if !ok {
		panic("expected in-memory logger")
	}
	return logger
}

type capacityExhaustedRegistry struct{}

func (capacityExhaustedRegistry) GetHealthyNodes() []registry.NodeInfo {
	return []registry.NodeInfo{{ID: "worker-1", Healthy: true}}
}

func (capacityExhaustedRegistry) GetNode(nodeID string) (registry.NodeInfo, bool) {
	if nodeID == "worker-1" {
		return registry.NodeInfo{ID: "worker-1", Healthy: true}, true
	}
	return registry.NodeInfo{}, false
}

func (capacityExhaustedRegistry) GetNodes() []registry.NodeInfo {
	return []registry.NodeInfo{{ID: "worker-1", Healthy: true}}
}

func (capacityExhaustedRegistry) GetNodeID() string { return "scheduler-test" }

func (capacityExhaustedRegistry) GetNodesByCapability(string) []registry.NodeInfo {
	return []registry.NodeInfo{{ID: "worker-1", Healthy: true}}
}

func (capacityExhaustedRegistry) GetNodeCount() int { return 1 }

func (capacityExhaustedRegistry) GetHealthyNodeCount() int { return 1 }

func (capacityExhaustedRegistry) Start(context.Context) error { return nil }

func (capacityExhaustedRegistry) Stop() error { return nil }

type failingTaskExecutor struct {
	err error
}

func (f *failingTaskExecutor) ExecuteOnNode(ctx context.Context, nodeID string, agentName string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	return nil, f.err
}

type sequenceTaskExecutor struct {
	errs  []error
	calls int
}

func (s *sequenceTaskExecutor) ExecuteOnNode(ctx context.Context, nodeID string, agentName string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	if s.calls >= len(s.errs) {
		return &agent.AgentOutput{}, nil
	}
	err := s.errs[s.calls]
	s.calls++
	if err != nil {
		return nil, err
	}
	return &agent.AgentOutput{}, nil
}
