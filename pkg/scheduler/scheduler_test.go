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

	if got := gaugeValue(t, "dagens_task_queue_config_max", "scheduler", schedulerMetricsID); got != 1 {
		t.Fatalf("task queue config max = %v, want %v", got, 1.0)
	}
	if got := gaugeValue(t, "dagens_task_queue_observed_max", "scheduler", schedulerMetricsID); got < 1 {
		t.Fatalf("task queue observed max = %v, want at least %v", got, 1.0)
	}
}

func TestSubmitJobRecordsInitialDurableTransitions(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		JobQueueSize: 1,
	})

	job := NewJob("job-init", "initial")
	stage := &Stage{
		ID:    "stage-1",
		JobID: job.ID,
		Tasks: []*Task{
			{ID: "task-1", JobID: job.ID, StageID: "stage-1"},
		},
	}
	job.AddStage(stage)

	if err := s.SubmitJob(job); err != nil {
		t.Fatalf("SubmitJob unexpected error: %v", err)
	}

	store, ok := s.TransitionStore().(*InMemoryTransitionStore)
	if !ok {
		t.Fatal("expected in-memory transition store")
	}

	records, err := store.ListTransitionsByJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("ListTransitionsByJob unexpected error: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("transition count = %d, want 3", len(records))
	}
	if records[0].Transition != TransitionJobSubmitted {
		t.Fatalf("first transition = %q, want %q", records[0].Transition, TransitionJobSubmitted)
	}
	if records[1].Transition != TransitionTaskCreated {
		t.Fatalf("second transition = %q, want %q", records[1].Transition, TransitionTaskCreated)
	}
	if records[2].Transition != TransitionJobQueued {
		t.Fatalf("third transition = %q, want %q", records[2].Transition, TransitionJobQueued)
	}
	if job.LifecycleState != JobStateQueued {
		t.Fatalf("job lifecycle state = %q, want %q", job.LifecycleState, JobStateQueued)
	}
	if stage.Tasks[0].LifecycleState != TaskStatePending {
		t.Fatalf("task lifecycle state = %q, want %q", stage.Tasks[0].LifecycleState, TaskStatePending)
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
	before := metricCounterValue(t, "dagens_scheduler_degraded_mode_total")

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

	after := metricCounterValue(t, "dagens_scheduler_degraded_mode_total")
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

	logger := schedulerLogger(t)
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

func TestExecuteStageDefersUntilCapacityAvailable(t *testing.T) {
	s := NewSchedulerWithConfig(capacityExhaustedRegistry{}, &sequenceTaskExecutor{}, SchedulerConfig{
		DefaultWorkerMaxConcurrency:       1,
		EnableStageCapacityDeferral:       true,
		StageCapacityDeferralTimeout:      300 * time.Millisecond,
		StageCapacityDeferralPollInterval: 20 * time.Millisecond,
	})
	s.nodeCapacity["worker-1"] = &nodeCapacity{
		ReservedInFlight: 1,
		MaxConcurrency:   1,
		LastUpdated:      time.Now(),
	}

	stage := &Stage{
		ID:    "stage-defer-success",
		JobID: "job-defer-success",
		Tasks: []*Task{{
			ID:        "task-1",
			StageID:   "stage-defer-success",
			JobID:     "job-defer-success",
			AgentID:   "agent-1",
			AgentName: "agent",
			Input:     &agent.AgentInput{},
		}},
	}

	beforeDeferrals := metricCounterValue(t, "dagens_scheduler_capacity_deferrals_total")
	beforePolls := metricCounterValue(t, "dagens_scheduler_capacity_deferral_polls_total")
	go func() {
		time.Sleep(80 * time.Millisecond)
		s.releaseNodeSlot("worker-1")
	}()

	if err := s.executeStage(context.Background(), stage); err != nil {
		t.Fatalf("executeStage error = %v, want nil", err)
	}
	if stage.Status != JobCompleted {
		t.Fatalf("stage status = %q, want %q", stage.Status, JobCompleted)
	}

	afterDeferrals := metricCounterValue(t, "dagens_scheduler_capacity_deferrals_total")
	if afterDeferrals != beforeDeferrals+1 {
		t.Fatalf("scheduler capacity deferrals = %v, want %v", afterDeferrals, beforeDeferrals+1)
	}
	afterPolls := metricCounterValue(t, "dagens_scheduler_capacity_deferral_polls_total")
	if afterPolls <= beforePolls {
		t.Fatalf("scheduler capacity deferral polls = %v, want > %v", afterPolls, beforePolls)
	}
}

func TestExecuteStageDeferralTimeoutReturnsNoWorkerCapacity(t *testing.T) {
	s := NewSchedulerWithConfig(capacityExhaustedRegistry{}, &sequenceTaskExecutor{}, SchedulerConfig{
		DefaultWorkerMaxConcurrency:       1,
		EnableStageCapacityDeferral:       true,
		StageCapacityDeferralTimeout:      60 * time.Millisecond,
		StageCapacityDeferralPollInterval: 10 * time.Millisecond,
	})
	s.nodeCapacity["worker-1"] = &nodeCapacity{
		ReservedInFlight: 1,
		MaxConcurrency:   1,
		LastUpdated:      time.Now(),
	}

	stage := &Stage{
		ID:    "stage-defer-timeout",
		JobID: "job-defer-timeout",
		Tasks: []*Task{{
			ID:      "task-1",
			StageID: "stage-defer-timeout",
			JobID:   "job-defer-timeout",
		}},
	}

	err := s.executeStage(context.Background(), stage)
	if !errors.Is(err, ErrNoWorkerCapacity) {
		t.Fatalf("executeStage error = %v, want %v", err, ErrNoWorkerCapacity)
	}
	if stage.Status != JobFailed {
		t.Fatalf("stage status = %q, want %q", stage.Status, JobFailed)
	}
}

func TestSelectNodeForTaskWithDeferralRespectsContextCancellation(t *testing.T) {
	s := NewSchedulerWithConfig(capacityExhaustedRegistry{}, &sequenceTaskExecutor{}, SchedulerConfig{
		DefaultWorkerMaxConcurrency:       1,
		EnableStageCapacityDeferral:       true,
		StageCapacityDeferralTimeout:      2 * time.Second,
		StageCapacityDeferralPollInterval: 50 * time.Millisecond,
	})
	s.nodeCapacity["worker-1"] = &nodeCapacity{
		ReservedInFlight: 1,
		MaxConcurrency:   1,
		LastUpdated:      time.Now(),
	}

	task := &Task{ID: "task-cancel", PartitionKey: "pk-cancel"}
	nodes := []registry.NodeInfo{{ID: "worker-1", Healthy: true}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, ok, err := s.selectNodeForTaskWithDeferral(ctx, task, nodes)
	if ok {
		t.Fatal("expected no node selection when context is canceled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("selectNodeForTaskWithDeferral error = %v, want %v", err, context.Canceled)
	}
}

func TestSelectNodeForTaskAffinityHit(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		EnableStickiness:            true,
		DefaultWorkerMaxConcurrency: 2,
	})
	beforeHits := metricCounterValue(t, "dagens_scheduler_affinity_hits_total")
	s.affinityMap.Set("pk-hit", "worker-1")
	s.nodeCapacity["worker-1"] = &nodeCapacity{MaxConcurrency: 2, LastUpdated: time.Now()}
	s.nodeCapacity["worker-2"] = &nodeCapacity{MaxConcurrency: 2, LastUpdated: time.Now()}

	task := &Task{ID: "task-aff-hit", PartitionKey: "pk-hit"}
	node, result, ok := s.selectNodeForTask(task, []registry.NodeInfo{
		{ID: "worker-1", Healthy: true},
		{ID: "worker-2", Healthy: true},
	})
	if !ok {
		t.Fatal("expected affinity hit selection to succeed")
	}
	if node.ID != "worker-1" {
		t.Fatalf("selected node = %q, want %q", node.ID, "worker-1")
	}
	if !result.IsHit {
		t.Fatal("expected IsHit=true for affinity hit")
	}
	afterHits := metricCounterValue(t, "dagens_scheduler_affinity_hits_total")
	if afterHits != beforeHits+1 {
		t.Fatalf("scheduler affinity hits = %v, want %v", afterHits, beforeHits+1)
	}
}

func TestSelectNodeForTaskAffinityMissCreatesEntry(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		EnableStickiness:            true,
		DefaultWorkerMaxConcurrency: 1,
	})
	beforeMisses := metricCounterValue(t, "dagens_scheduler_affinity_misses_total")
	s.nodeCapacity["worker-1"] = &nodeCapacity{MaxConcurrency: 1, LastUpdated: time.Now()}

	task := &Task{ID: "task-aff-miss", PartitionKey: "pk-miss"}
	node, result, ok := s.selectNodeForTask(task, []registry.NodeInfo{
		{ID: "worker-1", Healthy: true},
	})
	if !ok {
		t.Fatal("expected affinity miss selection to succeed")
	}
	if result.IsHit {
		t.Fatal("expected IsHit=false for affinity miss")
	}
	if result.IsStale {
		t.Fatal("expected IsStale=false for affinity miss")
	}
	entry := s.affinityMap.Get("pk-miss")
	if entry == nil {
		t.Fatal("expected affinity entry to be created on miss")
	}
	if entry.NodeID != node.ID {
		t.Fatalf("affinity node = %q, want %q", entry.NodeID, node.ID)
	}
	afterMisses := metricCounterValue(t, "dagens_scheduler_affinity_misses_total")
	if afterMisses != beforeMisses+1 {
		t.Fatalf("scheduler affinity misses = %v, want %v", afterMisses, beforeMisses+1)
	}
}

func TestSelectNodeForTaskAffinityStaleReroutes(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		EnableStickiness:            true,
		DefaultWorkerMaxConcurrency: 1,
	})
	beforeStale := metricCounterValue(t, "dagens_scheduler_affinity_stale_total")
	beforeMisses := metricCounterValue(t, "dagens_scheduler_affinity_misses_total")
	s.affinityMap.Set("pk-stale", "worker-stale")
	s.nodeCapacity["worker-new"] = &nodeCapacity{MaxConcurrency: 1, LastUpdated: time.Now()}

	task := &Task{ID: "task-aff-stale", PartitionKey: "pk-stale"}
	node, result, ok := s.selectNodeForTask(task, []registry.NodeInfo{
		{ID: "worker-new", Healthy: true},
	})
	if !ok {
		t.Fatal("expected stale affinity reroute to succeed")
	}
	if node.ID != "worker-new" {
		t.Fatalf("selected node = %q, want %q", node.ID, "worker-new")
	}
	if !result.IsStale {
		t.Fatal("expected IsStale=true for stale affinity")
	}
	entry := s.affinityMap.Get("pk-stale")
	if entry == nil || entry.NodeID != "worker-new" {
		t.Fatalf("expected stale affinity to be replaced with %q", "worker-new")
	}
	afterStale := metricCounterValue(t, "dagens_scheduler_affinity_stale_total")
	if afterStale != beforeStale+1 {
		t.Fatalf("scheduler affinity stale = %v, want %v", afterStale, beforeStale+1)
	}
	afterMisses := metricCounterValue(t, "dagens_scheduler_affinity_misses_total")
	if afterMisses != beforeMisses+1 {
		t.Fatalf("scheduler affinity misses = %v, want %v", afterMisses, beforeMisses+1)
	}
}

func TestSelectNodeForTaskAffinityCapacityBypass(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		EnableStickiness:            true,
		DefaultWorkerMaxConcurrency: 1,
	})
	s.affinityMap.Set("pk-cap-bypass", "worker-1")
	now := time.Now()
	s.nodeCapacity["worker-1"] = &nodeCapacity{
		ReservedInFlight: 1,
		MaxConcurrency:   1,
		LastUpdated:      now,
	}
	s.nodeCapacity["worker-2"] = &nodeCapacity{
		ReservedInFlight: 0,
		MaxConcurrency:   1,
		LastUpdated:      now,
	}

	task := &Task{ID: "task-aff-bypass", PartitionKey: "pk-cap-bypass"}
	node, result, ok := s.selectNodeForTask(task, []registry.NodeInfo{
		{ID: "worker-1", Healthy: true},
		{ID: "worker-2", Healthy: true},
	})
	if !ok {
		t.Fatal("expected capacity bypass selection to succeed")
	}
	if node.ID != "worker-2" {
		t.Fatalf("selected node = %q, want %q", node.ID, "worker-2")
	}
	if result.IsHit {
		t.Fatal("expected IsHit=false for capacity bypass")
	}
	entry := s.affinityMap.Get("pk-cap-bypass")
	if entry == nil || entry.NodeID != "worker-2" {
		t.Fatalf("expected affinity to update to bypass node %q", "worker-2")
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

	before := counterVecValue(t, "dagens_scheduler_dispatch_rejections_total", "reason", "transport_error")
	err := s.executeTask(context.Background(), task, node)
	if err == nil {
		t.Fatal("expected executeTask to fail")
	}

	after := counterVecValue(t, "dagens_scheduler_dispatch_rejections_total", "reason", "transport_error")
	if after != before+1 {
		t.Fatalf("transport_error dispatch rejections = %v, want %v", after, before+1)
	}
}

func TestExecuteTaskWithRetryRetriesCapacityConflictAndSucceeds(t *testing.T) {
	executor := &sequenceTaskExecutor{
		errs: []error{errors.New("worker at capacity"), nil},
	}
	s := NewSchedulerWithConfig(nil, executor, SchedulerConfig{
		MaxDispatchAttempts:         2,
		DispatchRejectCooldown:      5 * time.Second,
		DefaultWorkerMaxConcurrency: 1,
	})

	nodes := []registry.NodeInfo{{ID: "worker-1"}, {ID: "worker-2"}}
	task := &Task{
		ID:        "task-retry-success",
		AgentID:   "agent-1",
		AgentName: "agent",
		Input:     &agent.AgentInput{},
	}

	beforeRetries := metricCounterValue(t, "dagens_task_dispatch_retries_total")
	err := s.executeTaskWithRetry(context.Background(), task, nodes[0], nodes)
	if err != nil {
		t.Fatalf("executeTaskWithRetry error = %v, want nil", err)
	}
	if task.Attempts != 2 {
		t.Fatalf("task.Attempts = %d, want %d", task.Attempts, 2)
	}

	afterRetries := metricCounterValue(t, "dagens_task_dispatch_retries_total")
	if afterRetries != beforeRetries+1 {
		t.Fatalf("task dispatch retries = %v, want %v", afterRetries, beforeRetries+1)
	}
	if len(executor.callNodes) < 2 {
		t.Fatalf("executor callNodes length = %d, want at least 2", len(executor.callNodes))
	}
	if executor.callNodes[0] == executor.callNodes[1] {
		t.Fatalf("expected retry to pick a different node, got %q twice", executor.callNodes[0])
	}
}

func TestExecuteTaskWithRetryFailsAtMaxDispatchAttempts(t *testing.T) {
	s := NewSchedulerWithConfig(nil, &sequenceTaskExecutor{
		errs: []error{errors.New("worker at capacity"), errors.New("worker at capacity")},
	}, SchedulerConfig{
		MaxDispatchAttempts:         2,
		DispatchRejectCooldown:      5 * time.Second,
		DefaultWorkerMaxConcurrency: 1,
	})

	nodes := []registry.NodeInfo{{ID: "worker-1"}, {ID: "worker-2"}}
	task := &Task{
		ID:        "task-retry-fail",
		AgentID:   "agent-1",
		AgentName: "agent",
		Input:     &agent.AgentInput{},
	}

	beforeFailures := metricCounterValue(t, "dagens_tasks_failed_max_dispatch_attempts_total")
	err := s.executeTaskWithRetry(context.Background(), task, nodes[0], nodes)
	if err == nil {
		t.Fatal("expected executeTaskWithRetry to fail")
	}
	if task.Attempts != 2 {
		t.Fatalf("task.Attempts = %d, want %d", task.Attempts, 2)
	}

	afterFailures := metricCounterValue(t, "dagens_tasks_failed_max_dispatch_attempts_total")
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
	return metricCounterValue(t, "dagens_scheduler_all_workers_full_total")
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
		if len(labels) == 0 {
			t.Fatalf("metric %q found but no counter samples available", metricName)
		}
		return 0 // Labeled counters may not have a sample yet; treat as zero baseline.
	}

	if len(labels) == 0 {
		t.Fatalf("metric family %q not found", metricName)
	}
	return 0 // Labeled metric vec may not exist in gather output until first label sample.
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
		if len(labels) == 0 {
			t.Fatalf("metric %q found but no gauge samples match labels", metricName)
		}
		return 0
	}

	if len(labels) == 0 {
		t.Fatalf("metric family %q not found", metricName)
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

func schedulerLogger(t *testing.T) *telemetry.InMemoryLogger {
	t.Helper()
	logger, ok := telemetry.GetGlobalTelemetry().GetLogger().(*telemetry.InMemoryLogger)
	if !ok {
		t.Fatal("expected in-memory logger")
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
	errs      []error
	calls     int
	callNodes []string
}

func (s *sequenceTaskExecutor) ExecuteOnNode(ctx context.Context, nodeID string, agentName string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	s.callNodes = append(s.callNodes, nodeID)
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
