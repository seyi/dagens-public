package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/observability"
	"github.com/seyi/dagens/pkg/registry"
	"github.com/seyi/dagens/pkg/remote"
	"github.com/seyi/dagens/pkg/telemetry"
	"go.opentelemetry.io/otel/trace"
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

func TestSubmitJobWithContext_PropagatesContextToInitialTransitions(t *testing.T) {
	type ctxKey string
	const key ctxKey = "submit-request-id"

	store := &contextCaptureTransitionStore{
		inner: NewInMemoryTransitionStore(),
		key:   key,
	}
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}

	job := NewJob("job-ctx", "context")
	stage := &Stage{
		ID:    "stage-ctx",
		JobID: job.ID,
		Tasks: []*Task{{ID: "task-ctx", JobID: job.ID, StageID: "stage-ctx"}},
	}
	job.AddStage(stage)

	ctx := context.WithValue(context.Background(), key, "req-ctx-123")
	if err := s.SubmitJobWithContext(ctx, job); err != nil {
		t.Fatalf("SubmitJobWithContext unexpected error: %v", err)
	}

	if got := store.lastValue; got != "req-ctx-123" {
		t.Fatalf("captured context value = %v, want %q", got, "req-ctx-123")
	}
}

func TestSubmitJobWithContext_ReservesCapacityUntilInitialTransitionsPersist(t *testing.T) {
	store := &blockingAppendTransitionStore{
		InMemoryTransitionStore: NewInMemoryTransitionStore(),
		block:                   make(chan struct{}),
		entered:                 make(chan struct{}),
	}
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}

	job := NewJob("job-blocking-submit", "blocking-submit")
	done := make(chan error, 1)
	go func() {
		done <- s.SubmitJobWithContext(context.Background(), job)
	}()

	select {
	case <-store.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for transition append to block")
	}

	s.mu.RLock()
	queueDepth := len(s.jobQueue)
	pendingReservation := s.pendingEnqueue
	s.mu.RUnlock()
	if queueDepth != 0 {
		t.Fatalf("queue depth while transitions blocked = %d, want 0", queueDepth)
	}
	if pendingReservation != 1 {
		t.Fatalf("pending enqueue reservation = %d, want 1", pendingReservation)
	}

	close(store.block)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SubmitJobWithContext unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for submission completion")
	}

	s.mu.RLock()
	finalDepth := len(s.jobQueue)
	finalReservation := s.pendingEnqueue
	s.mu.RUnlock()
	if finalDepth != 1 {
		t.Fatalf("queue depth after transitions persisted = %d, want 1", finalDepth)
	}
	if finalReservation != 0 {
		t.Fatalf("pending enqueue reservation after submit = %d, want 0", finalReservation)
	}
}

func TestSetLeadershipProvider_AfterStartRejected(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	s.Start()
	defer s.Stop()

	err := s.SetLeadershipProvider(staticLeadershipProvider{
		authority: LeadershipAuthority{IsLeader: true, Epoch: "e1", LeaderID: "node-a"},
	})
	if !errors.Is(err, ErrLeadershipProviderSetAfterStart) {
		t.Fatalf("SetLeadershipProvider error = %v, want %v", err, ErrLeadershipProviderSetAfterStart)
	}
}

func TestRun_FollowerDefersDispatchUntilLeadershipAcquired(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		JobQueueSize:                      1,
		LeadershipRetryInterval:           5 * time.Millisecond,
		AffinityCleanupInterval:           10 * time.Millisecond,
		StageCapacityDeferralPollInterval: 5 * time.Millisecond,
	})
	provider := &toggleLeadershipProvider{}
	provider.setLeader(false)
	if err := s.SetLeadershipProvider(provider); err != nil {
		t.Fatalf("SetLeadershipProvider unexpected error: %v", err)
	}

	s.Start()
	defer s.Stop()

	job := NewJob("job-ha-follower", "ha-follower")
	if err := s.SubmitJob(job); err != nil {
		t.Fatalf("SubmitJob unexpected error: %v", err)
	}

	time.Sleep(40 * time.Millisecond)
	gotStatus, err := jobStatusForTest(s, job.ID)
	if err != nil {
		t.Fatalf("jobStatusForTest unexpected error: %v", err)
	}
	if gotStatus != JobPending {
		t.Fatalf("job status before leadership = %q, want %q", gotStatus, JobPending)
	}

	provider.setLeader(true)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		currentStatus, getErr := jobStatusForTest(s, job.ID)
		if getErr != nil {
			t.Fatalf("jobStatusForTest unexpected error: %v", getErr)
		}
		if currentStatus == JobCompleted {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	finalStatus, err := jobStatusForTest(s, job.ID)
	if err != nil {
		t.Fatalf("jobStatusForTest unexpected error: %v", err)
	}
	t.Fatalf("job status = %q, want %q after leadership acquisition", finalStatus, JobCompleted)
}

func TestStart_LeadershipProviderStartFailurePreventsSchedulerStart(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	lp := &lifecycleLeadershipProvider{
		authority: LeadershipAuthority{IsLeader: true, Epoch: "1", LeaderID: "leader"},
		startErr:  errors.New("leadership unavailable"),
	}
	if err := s.SetLeadershipProvider(lp); err != nil {
		t.Fatalf("SetLeadershipProvider unexpected error: %v", err)
	}

	s.Start()
	defer s.Stop()

	if s.started {
		t.Fatal("expected scheduler to remain stopped when leadership provider start fails")
	}
	if s.recovering {
		t.Fatal("expected recovering=false after failed start")
	}
	if lp.started.Load() {
		t.Fatal("expected lifecycle provider start marker to remain false on start error")
	}
}

func TestStop_LeadershipProviderStopCalled(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	lp := &lifecycleLeadershipProvider{
		authority: LeadershipAuthority{IsLeader: true, Epoch: "1", LeaderID: "leader"},
	}
	if err := s.SetLeadershipProvider(lp); err != nil {
		t.Fatalf("SetLeadershipProvider unexpected error: %v", err)
	}

	s.Start()
	s.Stop()

	if !lp.started.Load() {
		t.Fatal("expected lifecycle provider start to be called")
	}
	if !lp.stopped.Load() {
		t.Fatal("expected lifecycle provider stop to be called")
	}
}

func TestNewEtcdLeadershipProvider_RequiresIdentityAndEndpoints(t *testing.T) {
	_, err := NewEtcdLeadershipProvider(EtcdLeadershipProviderConfig{
		Identity: "cp-a",
	})
	if err == nil {
		t.Fatal("expected endpoints validation error")
	}

	_, err = NewEtcdLeadershipProvider(EtcdLeadershipProviderConfig{
		Endpoints: []string{"http://127.0.0.1:2379"},
	})
	if err == nil {
		t.Fatal("expected identity validation error")
	}
}

func TestExecuteJob_RecordsResumedTransitionFromAwaitingHuman(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	job := NewJob("job-resumed", "resumed")
	job.LifecycleState = JobStateAwaitingHuman

	s.executeJob(context.Background(), job)

	store, ok := s.TransitionStore().(*InMemoryTransitionStore)
	if !ok {
		t.Fatal("expected in-memory transition store")
	}
	records, err := store.ListTransitionsByJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("ListTransitionsByJob unexpected error: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("expected at least one transition record")
	}
	if records[0].Transition != TransitionJobResumed {
		t.Fatalf("first transition = %q, want %q", records[0].Transition, TransitionJobResumed)
	}
}

func TestExecuteJob_FromSubmittedRecordsQueuedBeforeRunning(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	job := NewJob("job-submitted", "submitted")

	s.executeJob(context.Background(), job)

	store, ok := s.TransitionStore().(*InMemoryTransitionStore)
	if !ok {
		t.Fatal("expected in-memory transition store")
	}
	records, err := store.ListTransitionsByJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("ListTransitionsByJob unexpected error: %v", err)
	}
	if len(records) < 3 {
		t.Fatalf("transition count = %d, want at least 3", len(records))
	}
	if records[0].Transition != TransitionJobQueued {
		t.Fatalf("first transition = %q, want %q", records[0].Transition, TransitionJobQueued)
	}
	if records[1].Transition != TransitionJobRunning {
		t.Fatalf("second transition = %q, want %q", records[1].Transition, TransitionJobRunning)
	}
	if records[2].Transition != TransitionJobSucceeded {
		t.Fatalf("third transition = %q, want %q", records[2].Transition, TransitionJobSucceeded)
	}
}

func TestExecuteJob_WithTasks_DefersRunningUntilDispatchClaim(t *testing.T) {
	executor := &sequenceTaskExecutor{}
	s := NewSchedulerWithConfig(capacityExhaustedRegistry{}, executor, SchedulerConfig{JobQueueSize: 1})
	job := NewJob("job-with-task", "with-task")
	stage := &Stage{
		ID:    "stage-1",
		JobID: job.ID,
		Tasks: []*Task{
			{
				ID:        "task-1",
				JobID:     job.ID,
				StageID:   "stage-1",
				AgentID:   "start",
				AgentName: "start",
				Input:     &agent.AgentInput{Instruction: "start"},
			},
		},
	}
	job.AddStage(stage)

	s.executeJob(context.Background(), job)

	store, ok := s.TransitionStore().(*InMemoryTransitionStore)
	if !ok {
		t.Fatal("expected in-memory transition store")
	}
	records, err := store.ListTransitionsByJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("ListTransitionsByJob unexpected error: %v", err)
	}

	var queuedIdx = -1
	var dispatchedIdx = -1
	var runningIdx = -1
	for i, record := range records {
		switch record.Transition {
		case TransitionJobQueued:
			if queuedIdx == -1 {
				queuedIdx = i
			}
		case TransitionTaskDispatched:
			if dispatchedIdx == -1 {
				dispatchedIdx = i
			}
		case TransitionJobRunning:
			if runningIdx == -1 {
				runningIdx = i
			}
		}
	}
	if queuedIdx == -1 || dispatchedIdx == -1 || runningIdx == -1 {
		t.Fatalf("missing expected transitions; queued=%d dispatched=%d running=%d", queuedIdx, dispatchedIdx, runningIdx)
	}
	if runningIdx <= queuedIdx {
		t.Fatalf("running transition index = %d, want > queued index %d", runningIdx, queuedIdx)
	}
	if runningIdx <= dispatchedIdx {
		t.Fatalf("running transition index = %d, want > dispatched index %d", runningIdx, dispatchedIdx)
	}
}

func TestCleanupTerminalJobsOnce_PrunesExpiredTerminalJobs(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		JobQueueSize:                8,
		EnableJobRetentionCleanup:   false,
		JobRetentionTTL:             time.Minute,
		JobRetentionCleanupInterval: 30 * time.Second,
	})

	now := time.Now()
	terminalOld := &Job{
		ID:             "job-terminal-old",
		Name:           "old",
		LifecycleState: JobStateSucceeded,
		Status:         JobCompleted,
		UpdatedAt:      now.Add(-2 * time.Minute),
	}
	terminalFresh := &Job{
		ID:             "job-terminal-fresh",
		Name:           "fresh",
		LifecycleState: JobStateFailed,
		Status:         JobFailed,
		UpdatedAt:      now.Add(-10 * time.Second),
	}
	running := &Job{
		ID:             "job-running",
		Name:           "running",
		LifecycleState: JobStateRunning,
		Status:         JobRunning,
		UpdatedAt:      now.Add(-10 * time.Minute),
	}

	s.mu.Lock()
	s.jobs[terminalOld.ID] = terminalOld
	s.jobs[terminalFresh.ID] = terminalFresh
	s.jobs[running.ID] = running
	s.jobSequences[terminalOld.ID] = 5
	s.jobSequences[terminalFresh.ID] = 4
	s.jobSequences[running.ID] = 3
	s.queuedJobs[terminalOld.ID] = struct{}{}
	s.mu.Unlock()

	pruned := s.cleanupTerminalJobsOnce(now)
	if pruned != 1 {
		t.Fatalf("cleanupTerminalJobsOnce pruned = %d, want 1", pruned)
	}

	if _, err := s.GetJob(terminalOld.ID); err == nil {
		t.Fatalf("expected %s to be pruned", terminalOld.ID)
	}
	if _, err := s.GetJob(terminalFresh.ID); err != nil {
		t.Fatalf("expected %s to remain, got error: %v", terminalFresh.ID, err)
	}
	if _, err := s.GetJob(running.ID); err != nil {
		t.Fatalf("expected %s to remain, got error: %v", running.ID, err)
	}
}

func TestExecuteJob_UsesProvidedContextForSpan(t *testing.T) {
	tracer := &captureTracer{}
	s := NewSchedulerWithConfigAndDeps(nil, nil, SchedulerConfig{JobQueueSize: 1}, SchedulerDependencies{
		Tracer: tracer,
	})

	ctx := context.WithValue(context.Background(), testContextKey("parent"), "ctx-marker")
	job := NewJob("job-context-span", "context-span")
	s.executeJob(ctx, job)

	if !tracer.lastStartCtxHasMarker {
		t.Fatal("expected executeJob to start span from provided parent context")
	}
}

func TestNewSchedulerWithBenchmarkQueueCapacityAllowsOverride(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.JobQueueSize = 20000

	s := NewSchedulerWithBenchmarkQueueCapacity(nil, nil, cfg, SchedulerDependencies{}, 20000)

	if got := cap(s.jobQueue); got != 20000 {
		t.Fatalf("jobQueue capacity = %d, want 20000", got)
	}
	if got := s.config.JobQueueSize; got != 20000 {
		t.Fatalf("config.JobQueueSize = %d, want 20000", got)
	}
}

func TestReconcileDurableQueuedJobsOnce_LeaderRequeuesQueuedJob(t *testing.T) {
	store := NewInMemoryTransitionStore()
	seedDurableQueuedJobForReconcileTest(t, store, "job-durable-queued")

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		JobQueueSize:                   4,
		EnableLeaderDurableRequeueLoop: false,
	})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}
	lp := &toggleLeadershipProvider{}
	lp.setLeader(true)
	if err := s.SetLeadershipProvider(lp); err != nil {
		t.Fatalf("SetLeadershipProvider unexpected error: %v", err)
	}
	seedHydratedQueuedRuntimeJobForReconcileTest(s, "job-durable-queued")

	if err := s.reconcileDurableQueuedJobsOnce(context.Background(), ReconcileModeLeader); err != nil {
		t.Fatalf("reconcileDurableQueuedJobsOnce unexpected error: %v", err)
	}
	if got := len(s.jobQueue); got != 1 {
		t.Fatalf("job queue depth after first reconcile = %d, want 1", got)
	}
	if _, err := s.GetJob("job-durable-queued"); err != nil {
		t.Fatalf("GetJob expected recovered queued job, got error: %v", err)
	}

	// Repeat reconcile should not duplicate the queued in-memory entry.
	if err := s.reconcileDurableQueuedJobsOnce(context.Background(), ReconcileModeFollower); err != nil {
		t.Fatalf("second reconcileDurableQueuedJobsOnce unexpected error: %v", err)
	}
	if got := len(s.jobQueue); got != 1 {
		t.Fatalf("job queue depth after second reconcile = %d, want 1", got)
	}
	reconciled, err := s.GetJob("job-durable-queued")
	if err != nil {
		t.Fatalf("expected queued job after second reconcile, got error: %v", err)
	}
	if reconciled.LifecycleState != JobStateQueued {
		t.Fatalf("queued job lifecycle after second reconcile = %q, want %q", reconciled.LifecycleState, JobStateQueued)
	}
}

func TestReconcileDurableQueuedJobsOnce_FollowerSkipsRequeue(t *testing.T) {
	store := NewInMemoryTransitionStore()
	seedDurableQueuedJobForReconcileTest(t, store, "job-follower-skip")

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		JobQueueSize:                   2,
		EnableLeaderDurableRequeueLoop: false,
	})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}
	lp := &toggleLeadershipProvider{}
	lp.setLeader(false)
	if err := s.SetLeadershipProvider(lp); err != nil {
		t.Fatalf("SetLeadershipProvider unexpected error: %v", err)
	}

	if err := s.reconcileDurableQueuedJobsOnce(context.Background(), ReconcileModeFollower); err != nil {
		t.Fatalf("reconcileDurableQueuedJobsOnce unexpected error: %v", err)
	}
	if got := len(s.jobQueue); got != 0 {
		t.Fatalf("job queue depth = %d, want 0 in follower mode", got)
	}
	job, err := s.GetJob("job-follower-skip")
	if err != nil {
		t.Fatalf("GetJob expected durable visibility fallback, got error: %v", err)
	}
	if job.LifecycleState != JobStateQueued {
		t.Fatalf("durable fallback lifecycle = %q, want %q", job.LifecycleState, JobStateQueued)
	}
	if got := len(s.jobQueue); got != 0 {
		t.Fatalf("job queue depth after follower GetJob fallback = %d, want 0", got)
	}
}

func TestLeaderTakeover_PreservesAwaitingHumanUntilExplicitResume(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now()
	jobID := "job-awaiting-human-takeover"
	taskID := jobID + "-task-1"

	requireNoErr := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	requireNoErr(store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:          jobID,
		Name:           jobID,
		CurrentState:   JobStateAwaitingHuman,
		LastSequenceID: 5,
		CreatedAt:      now,
		UpdatedAt:      now.Add(4 * time.Second),
	}))
	requireNoErr(store.UpsertTask(context.Background(), DurableTaskRecord{
		TaskID:       taskID,
		JobID:        jobID,
		StageID:      "stage-1",
		AgentID:      "human",
		AgentName:    "human",
		InputJSON:    `{"instruction":"approve"}`,
		CurrentState: TaskStatePending,
		UpdatedAt:    now.Add(3 * time.Second),
	}))
	requireNoErr(store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted,
		JobID: jobID, NewState: string(JobStateSubmitted), OccurredAt: now,
	}))
	requireNoErr(store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 2, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated,
		JobID: jobID, TaskID: taskID, NewState: string(TaskStatePending), OccurredAt: now.Add(time.Second),
	}))
	requireNoErr(store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 3, EntityType: TransitionEntityJob, Transition: TransitionJobQueued,
		JobID: jobID, PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(2 * time.Second),
	}))
	requireNoErr(store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 4, EntityType: TransitionEntityJob, Transition: TransitionJobRunning,
		JobID: jobID, PreviousState: string(JobStateQueued), NewState: string(JobStateRunning), OccurredAt: now.Add(3 * time.Second),
	}))
	requireNoErr(store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 5, EntityType: TransitionEntityJob, Transition: TransitionJobAwaitingHuman,
		JobID: jobID, PreviousState: string(JobStateRunning), NewState: string(JobStateAwaitingHuman), OccurredAt: now.Add(4 * time.Second),
	}))

	s := NewSchedulerWithConfig(capacityExhaustedRegistry{}, &sequenceTaskExecutor{}, SchedulerConfig{
		JobQueueSize:                   4,
		EnableLeaderDurableRequeueLoop: false,
	})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}
	lp := &toggleLeadershipProvider{}
	lp.setLeader(true)
	if err := s.SetLeadershipProvider(lp); err != nil {
		t.Fatalf("SetLeadershipProvider unexpected error: %v", err)
	}

	if err := s.reconcileDurableQueuedJobsOnce(context.Background(), ReconcileModeLeader); err != nil {
		t.Fatalf("reconcileDurableQueuedJobsOnce unexpected error: %v", err)
	}
	if got := len(s.jobQueue); got != 0 {
		t.Fatalf("job queue depth after takeover reconcile = %d, want 0", got)
	}

	job, err := s.GetJob(jobID)
	if err != nil {
		t.Fatalf("GetJob unexpected error: %v", err)
	}
	if job.LifecycleState != JobStateAwaitingHuman {
		t.Fatalf("hydrated lifecycle = %q, want %q", job.LifecycleState, JobStateAwaitingHuman)
	}
	if job.Status != JobAwaitingHuman {
		t.Fatalf("hydrated status = %q, want %q", job.Status, JobAwaitingHuman)
	}

	s.executeJob(context.Background(), job)

	records, err := store.ListTransitionsByJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("ListTransitionsByJob unexpected error: %v", err)
	}
	var resumedIdx = -1
	var succeededIdx = -1
	for i, record := range records {
		switch record.Transition {
		case TransitionJobResumed:
			resumedIdx = i
		case TransitionJobSucceeded:
			succeededIdx = i
		}
	}
	if resumedIdx == -1 {
		t.Fatalf("expected %q transition in %#v", TransitionJobResumed, records)
	}
	if succeededIdx == -1 {
		t.Fatalf("expected %q transition in %#v", TransitionJobSucceeded, records)
	}
	if succeededIdx <= resumedIdx {
		t.Fatalf("job succeeded index = %d, want > resumed index %d", succeededIdx, resumedIdx)
	}
}

func TestReconcileDurableQueuedJobsOnce_SkipsMalformedReplayAndContinues(t *testing.T) {
	store := NewInMemoryTransitionStore()
	seedDurableQueuedJobForReconcileTest(t, store, "job-reconcile-good")
	seedDurableMalformedQueuedJobForReconcileTest(t, store, "job-reconcile-bad")

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		JobQueueSize:                   4,
		EnableLeaderDurableRequeueLoop: false,
	})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}
	lp := &toggleLeadershipProvider{}
	lp.setLeader(true)
	if err := s.SetLeadershipProvider(lp); err != nil {
		t.Fatalf("SetLeadershipProvider unexpected error: %v", err)
	}
	seedHydratedQueuedRuntimeJobForReconcileTest(s, "job-reconcile-good")

	if err := s.reconcileDurableQueuedJobsOnce(context.Background(), ReconcileModeLeader); err != nil {
		t.Fatalf("reconcileDurableQueuedJobsOnce unexpected error: %v", err)
	}

	if got := len(s.jobQueue); got != 1 {
		t.Fatalf("job queue depth = %d, want 1 (good queued job only)", got)
	}
	if _, err := s.GetJob("job-reconcile-good"); err != nil {
		t.Fatalf("expected recovered good queued job, got error: %v", err)
	}
	bad, err := s.GetJob("job-reconcile-bad")
	if err != nil {
		t.Fatalf("expected malformed job to be quarantined to terminal state, got error: %v", err)
	}
	if bad.LifecycleState != JobStateFailed {
		t.Fatalf("malformed recovered job lifecycle = %q, want %q", bad.LifecycleState, JobStateFailed)
	}
}

func TestReconcileDurableQueuedJobsOnce_PromotesSubmittedToQueuedAndEnqueues(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now()
	jobID := "job-reconcile-submitted"
	taskID := jobID + "-task-1"

	requireNoErr := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	requireNoErr(store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:          jobID,
		Name:           jobID,
		CurrentState:   JobStateSubmitted,
		LastSequenceID: 2,
		CreatedAt:      now,
		UpdatedAt:      now.Add(time.Second),
	}))
	requireNoErr(store.UpsertTask(context.Background(), DurableTaskRecord{
		TaskID:       taskID,
		JobID:        jobID,
		StageID:      "stage-1",
		AgentID:      "start",
		AgentName:    "start",
		InputJSON:    `{"instruction":"start"}`,
		CurrentState: TaskStatePending,
		UpdatedAt:    now.Add(time.Second),
	}))
	requireNoErr(store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted,
		JobID: jobID, NewState: string(JobStateSubmitted), OccurredAt: now,
	}))
	requireNoErr(store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 2, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated,
		JobID: jobID, TaskID: taskID, NewState: string(TaskStatePending), OccurredAt: now.Add(time.Second),
	}))

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		JobQueueSize:                   4,
		EnableLeaderDurableRequeueLoop: false,
	})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}
	lp := &toggleLeadershipProvider{}
	lp.setLeader(true)
	if err := s.SetLeadershipProvider(lp); err != nil {
		t.Fatalf("SetLeadershipProvider unexpected error: %v", err)
	}

	if err := s.reconcileDurableQueuedJobsOnce(context.Background(), ReconcileModeLeader); err != nil {
		t.Fatalf("reconcileDurableQueuedJobsOnce unexpected error: %v", err)
	}
	if got := len(s.jobQueue); got != 1 {
		t.Fatalf("job queue depth = %d, want 1", got)
	}
	reconciled, err := s.GetJob(jobID)
	if err != nil {
		t.Fatalf("GetJob unexpected error: %v", err)
	}
	if reconciled.LifecycleState != JobStateQueued {
		t.Fatalf("reconciled lifecycle = %q, want %q", reconciled.LifecycleState, JobStateQueued)
	}

	records, err := store.ListTransitionsByJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("ListTransitionsByJob unexpected error: %v", err)
	}
	if len(records) < 3 {
		t.Fatalf("transition count = %d, want at least 3", len(records))
	}
	if got := records[len(records)-1].Transition; got != TransitionJobQueued {
		t.Fatalf("last transition = %q, want %q", got, TransitionJobQueued)
	}
}

func TestReconcileDurableQueuedJobsOnce_FailsOrphanRunningWithoutProgress(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now()
	jobID := "job-reconcile-orphan-running"
	taskID := jobID + "-task-1"

	requireNoErr := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	requireNoErr(store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:          jobID,
		Name:           jobID,
		CurrentState:   JobStateRunning,
		LastSequenceID: 4,
		CreatedAt:      now,
		UpdatedAt:      now.Add(3 * time.Second),
	}))
	requireNoErr(store.UpsertTask(context.Background(), DurableTaskRecord{
		TaskID:       taskID,
		JobID:        jobID,
		StageID:      "stage-1",
		AgentID:      "start",
		AgentName:    "start",
		InputJSON:    `{"instruction":"start"}`,
		CurrentState: TaskStatePending,
		UpdatedAt:    now.Add(2 * time.Second),
	}))
	requireNoErr(store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted,
		JobID: jobID, NewState: string(JobStateSubmitted), OccurredAt: now,
	}))
	requireNoErr(store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 2, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated,
		JobID: jobID, TaskID: taskID, NewState: string(TaskStatePending), OccurredAt: now.Add(time.Second),
	}))
	requireNoErr(store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 3, EntityType: TransitionEntityJob, Transition: TransitionJobQueued,
		JobID: jobID, PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(2 * time.Second),
	}))
	requireNoErr(store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 4, EntityType: TransitionEntityJob, Transition: TransitionJobRunning,
		JobID: jobID, PreviousState: string(JobStateQueued), NewState: string(JobStateRunning), OccurredAt: now.Add(3 * time.Second),
	}))

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		JobQueueSize:                   4,
		EnableLeaderDurableRequeueLoop: false,
	})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}
	lp := &toggleLeadershipProvider{}
	lp.setLeader(true)
	if err := s.SetLeadershipProvider(lp); err != nil {
		t.Fatalf("SetLeadershipProvider unexpected error: %v", err)
	}

	if err := s.reconcileDurableQueuedJobsOnce(context.Background(), ReconcileModeLeader); err != nil {
		t.Fatalf("reconcileDurableQueuedJobsOnce unexpected error: %v", err)
	}
	if got := len(s.jobQueue); got != 0 {
		t.Fatalf("job queue depth = %d, want 0", got)
	}
	reconciled, err := s.GetJob(jobID)
	if err != nil {
		t.Fatalf("GetJob unexpected error: %v", err)
	}
	if reconciled.LifecycleState != JobStateFailed {
		t.Fatalf("reconciled lifecycle = %q, want %q", reconciled.LifecycleState, JobStateFailed)
	}
}

func TestReconcileDurableQueuedJobsOnce_FailsStaleInFlightRunningJob(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().Add(-(staleInFlightReconcileTimeout + 10*time.Second))
	jobID := "job-reconcile-stale-running"
	runningTaskID := jobID + "-task-running"
	pendingTaskID := jobID + "-task-pending"

	requireNoErr := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	requireNoErr(store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:          jobID,
		Name:           jobID,
		CurrentState:   JobStateRunning,
		LastSequenceID: 7,
		CreatedAt:      now,
		UpdatedAt:      now.Add(3 * time.Second),
	}))
	requireNoErr(store.UpsertTask(context.Background(), DurableTaskRecord{
		TaskID:       runningTaskID,
		JobID:        jobID,
		StageID:      "stage-1",
		AgentID:      "start",
		AgentName:    "start",
		InputJSON:    `{"instruction":"start"}`,
		CurrentState: TaskStateRunning,
		LastAttempt:  1,
		UpdatedAt:    now.Add(3 * time.Second),
	}))
	requireNoErr(store.UpsertTask(context.Background(), DurableTaskRecord{
		TaskID:       pendingTaskID,
		JobID:        jobID,
		StageID:      "stage-1",
		AgentID:      "end",
		AgentName:    "end",
		InputJSON:    `{"instruction":"end"}`,
		CurrentState: TaskStatePending,
		UpdatedAt:    now.Add(2 * time.Second),
	}))
	requireNoErr(store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted,
		JobID: jobID, NewState: string(JobStateSubmitted), OccurredAt: now,
	}))
	requireNoErr(store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 2, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated,
		JobID: jobID, TaskID: runningTaskID, NewState: string(TaskStatePending), OccurredAt: now.Add(time.Second),
	}))
	requireNoErr(store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 3, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated,
		JobID: jobID, TaskID: pendingTaskID, NewState: string(TaskStatePending), OccurredAt: now.Add(1500 * time.Millisecond),
	}))
	requireNoErr(store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 4, EntityType: TransitionEntityJob, Transition: TransitionJobQueued,
		JobID: jobID, PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(2 * time.Second),
	}))
	requireNoErr(store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 5, EntityType: TransitionEntityTask, Transition: TransitionTaskDispatched,
		JobID: jobID, TaskID: runningTaskID, PreviousState: string(TaskStatePending), NewState: string(TaskStateDispatched), Attempt: 1, OccurredAt: now.Add(2500 * time.Millisecond),
	}))
	requireNoErr(store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 6, EntityType: TransitionEntityJob, Transition: TransitionJobRunning,
		JobID: jobID, PreviousState: string(JobStateQueued), NewState: string(JobStateRunning), OccurredAt: now.Add(3 * time.Second),
	}))
	requireNoErr(store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 7, EntityType: TransitionEntityTask, Transition: TransitionTaskRunning,
		JobID: jobID, TaskID: runningTaskID, PreviousState: string(TaskStateDispatched), NewState: string(TaskStateRunning), Attempt: 1, OccurredAt: now.Add(3 * time.Second),
	}))

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		JobQueueSize:                   4,
		EnableLeaderDurableRequeueLoop: false,
	})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}
	lp := &toggleLeadershipProvider{}
	lp.setLeader(true)
	if err := s.SetLeadershipProvider(lp); err != nil {
		t.Fatalf("SetLeadershipProvider unexpected error: %v", err)
	}

	if err := s.reconcileDurableQueuedJobsOnce(context.Background(), ReconcileModeLeader); err != nil {
		t.Fatalf("reconcileDurableQueuedJobsOnce unexpected error: %v", err)
	}
	reconciled, err := s.GetJob(jobID)
	if err != nil {
		t.Fatalf("GetJob unexpected error: %v", err)
	}
	if reconciled.LifecycleState != JobStateFailed {
		t.Fatalf("reconciled lifecycle = %q, want %q", reconciled.LifecycleState, JobStateFailed)
	}
}

func TestReconcileDurableQueuedJobsOnce_CompletesRecoveredRunningJobWithSucceededTasks(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()
	jobID := "job-reconcile-complete-running"
	firstTaskID := jobID + "-task-1"
	secondTaskID := jobID + "-task-2"

	requireNoErr := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	requireNoErr(store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:          jobID,
		Name:           jobID,
		CurrentState:   JobStateRunning,
		LastSequenceID: 11,
		CreatedAt:      now,
		UpdatedAt:      now.Add(11 * time.Second),
	}))
	requireNoErr(store.UpsertTask(context.Background(), DurableTaskRecord{
		TaskID:       firstTaskID,
		JobID:        jobID,
		StageID:      "stage-1",
		AgentID:      "start",
		AgentName:    "start",
		InputJSON:    `{"instruction":"start"}`,
		CurrentState: TaskStateSucceeded,
		LastAttempt:  1,
		UpdatedAt:    now.Add(8 * time.Second),
	}))
	requireNoErr(store.UpsertTask(context.Background(), DurableTaskRecord{
		TaskID:       secondTaskID,
		JobID:        jobID,
		StageID:      "stage-2",
		AgentID:      "end",
		AgentName:    "end",
		InputJSON:    `{"instruction":"end"}`,
		CurrentState: TaskStateSucceeded,
		LastAttempt:  1,
		UpdatedAt:    now.Add(11 * time.Second),
	}))

	records := []TransitionRecord{
		{SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted, JobID: jobID, NewState: string(JobStateSubmitted), OccurredAt: now},
		{SequenceID: 2, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated, JobID: jobID, TaskID: firstTaskID, NewState: string(TaskStatePending), OccurredAt: now.Add(time.Second)},
		{SequenceID: 3, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated, JobID: jobID, TaskID: secondTaskID, NewState: string(TaskStatePending), OccurredAt: now.Add(2 * time.Second)},
		{SequenceID: 4, EntityType: TransitionEntityJob, Transition: TransitionJobQueued, JobID: jobID, PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(3 * time.Second)},
		{SequenceID: 5, EntityType: TransitionEntityTask, Transition: TransitionTaskDispatched, JobID: jobID, TaskID: firstTaskID, PreviousState: string(TaskStatePending), NewState: string(TaskStateDispatched), Attempt: 1, OccurredAt: now.Add(4 * time.Second)},
		{SequenceID: 6, EntityType: TransitionEntityJob, Transition: TransitionJobRunning, JobID: jobID, PreviousState: string(JobStateQueued), NewState: string(JobStateRunning), OccurredAt: now.Add(5 * time.Second)},
		{SequenceID: 7, EntityType: TransitionEntityTask, Transition: TransitionTaskRunning, JobID: jobID, TaskID: firstTaskID, PreviousState: string(TaskStateDispatched), NewState: string(TaskStateRunning), Attempt: 1, OccurredAt: now.Add(6 * time.Second)},
		{SequenceID: 8, EntityType: TransitionEntityTask, Transition: TransitionTaskSucceeded, JobID: jobID, TaskID: firstTaskID, PreviousState: string(TaskStateRunning), NewState: string(TaskStateSucceeded), Attempt: 1, OccurredAt: now.Add(7 * time.Second)},
		{SequenceID: 9, EntityType: TransitionEntityTask, Transition: TransitionTaskDispatched, JobID: jobID, TaskID: secondTaskID, PreviousState: string(TaskStatePending), NewState: string(TaskStateDispatched), Attempt: 1, OccurredAt: now.Add(8 * time.Second)},
		{SequenceID: 10, EntityType: TransitionEntityTask, Transition: TransitionTaskRunning, JobID: jobID, TaskID: secondTaskID, PreviousState: string(TaskStateDispatched), NewState: string(TaskStateRunning), Attempt: 1, OccurredAt: now.Add(9 * time.Second)},
		{SequenceID: 11, EntityType: TransitionEntityTask, Transition: TransitionTaskSucceeded, JobID: jobID, TaskID: secondTaskID, PreviousState: string(TaskStateRunning), NewState: string(TaskStateSucceeded), Attempt: 1, OccurredAt: now.Add(10 * time.Second)},
	}
	for _, record := range records {
		requireNoErr(store.AppendTransition(context.Background(), record))
	}

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		JobQueueSize:                   4,
		EnableLeaderDurableRequeueLoop: false,
	})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}
	lp := &toggleLeadershipProvider{}
	lp.setLeader(true)
	if err := s.SetLeadershipProvider(lp); err != nil {
		t.Fatalf("SetLeadershipProvider unexpected error: %v", err)
	}

	if err := s.reconcileDurableQueuedJobsOnce(context.Background(), ReconcileModeLeader); err != nil {
		t.Fatalf("reconcileDurableQueuedJobsOnce unexpected error: %v", err)
	}
	reconciled, err := s.GetJob(jobID)
	if err != nil {
		t.Fatalf("GetJob unexpected error: %v", err)
	}
	if reconciled.LifecycleState != JobStateSucceeded {
		t.Fatalf("reconciled lifecycle = %q, want %q", reconciled.LifecycleState, JobStateSucceeded)
	}

	records, err = store.ListTransitionsByJob(context.Background(), jobID)
	if err != nil {
		t.Fatalf("ListTransitionsByJob unexpected error: %v", err)
	}
	if got := records[len(records)-1].Transition; got != TransitionJobSucceeded {
		t.Fatalf("last transition = %q, want %q", got, TransitionJobSucceeded)
	}
}

func TestRecordJobTransition_SkipsInvalidLifecycleTransition(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	job := NewJob("job-invalid-transition", "invalid")
	job.LifecycleState = JobStateSubmitted

	s.recordJobTransition(context.Background(), nil, job, TransitionJobSucceeded, JobStateSucceeded, "")

	store, ok := s.TransitionStore().(*InMemoryTransitionStore)
	if !ok {
		t.Fatal("expected in-memory transition store")
	}
	records, err := store.ListTransitionsByJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("ListTransitionsByJob unexpected error: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("transition count = %d, want 0 for invalid transition", len(records))
	}
	if job.LifecycleState != JobStateSubmitted {
		t.Fatalf("job lifecycle state = %q, want %q", job.LifecycleState, JobStateSubmitted)
	}
}

func TestRecordTaskTransition_SkipsInvalidLifecycleTransition(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	task := &Task{
		ID:             "task-invalid-transition",
		JobID:          "job-invalid-task-transition",
		StageID:        "stage-1",
		LifecycleState: TaskStatePending,
	}

	s.recordTaskTransition(context.Background(), nil, task, TransitionTaskSucceeded, TaskStateSucceeded, "worker-1", 1, "")

	store, ok := s.TransitionStore().(*InMemoryTransitionStore)
	if !ok {
		t.Fatal("expected in-memory transition store")
	}
	records, err := store.ListTransitionsByJob(context.Background(), task.JobID)
	if err != nil {
		t.Fatalf("ListTransitionsByJob unexpected error: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("transition count = %d, want 0 for invalid transition", len(records))
	}
	if task.LifecycleState != TaskStatePending {
		t.Fatalf("task lifecycle state = %q, want %q", task.LifecycleState, TaskStatePending)
	}
}

func TestRecordJobTransition_NormalizesAndTruncatesErrorSummary(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	job := NewJob("job-summary", "summary")
	job.LifecycleState = JobStateSubmitted

	raw := strings.Repeat("x", maxTransitionErrorSummaryRunes+25) + "\x00\n\t" + "tail"
	s.recordJobTransition(context.Background(), nil, job, TransitionJobQueued, JobStateQueued, raw)

	store, ok := s.TransitionStore().(*InMemoryTransitionStore)
	if !ok {
		t.Fatal("expected in-memory transition store")
	}
	records, err := store.ListTransitionsByJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("ListTransitionsByJob unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("transition count = %d, want 1", len(records))
	}
	summary := records[0].ErrorSummary
	if summary == "" {
		t.Fatal("expected non-empty normalized error summary")
	}
	if strings.ContainsRune(summary, '\x00') {
		t.Fatalf("summary contains raw control character: %q", summary)
	}
	if got := len([]rune(summary)); got != maxTransitionErrorSummaryRunes {
		t.Fatalf("summary rune length = %d, want %d", got, maxTransitionErrorSummaryRunes)
	}
}

func TestRecordTaskTransition_NormalizesErrorSummaryWhitespace(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	task := &Task{
		ID:             "task-summary",
		JobID:          "job-task-summary",
		StageID:        "stage-1",
		LifecycleState: TaskStatePending,
	}

	raw := fmt.Sprintf("   timeout\tafter\nretry\x00count=%d   ", 3)
	s.recordTaskTransition(context.Background(), nil, task, TransitionTaskDispatched, TaskStateDispatched, "worker-1", 1, raw)

	store, ok := s.TransitionStore().(*InMemoryTransitionStore)
	if !ok {
		t.Fatal("expected in-memory transition store")
	}
	records, err := store.ListTransitionsByJob(context.Background(), task.JobID)
	if err != nil {
		t.Fatalf("ListTransitionsByJob unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("transition count = %d, want 1", len(records))
	}
	if got, want := records[0].ErrorSummary, "timeout after retry count=3"; got != want {
		t.Fatalf("normalized summary = %q, want %q", got, want)
	}
}

func TestNormalizeTransitionErrorSummary(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		assert func(t *testing.T, got string)
	}{
		{
			name:  "empty",
			input: "",
			assert: func(t *testing.T, got string) {
				if got != "" {
					t.Fatalf("got %q, want empty", got)
				}
			},
		},
		{
			name:  "whitespace only collapses to empty",
			input: "  \t \n  ",
			assert: func(t *testing.T, got string) {
				if got != "" {
					t.Fatalf("got %q, want empty", got)
				}
			},
		},
		{
			name:  "control chars replaced and whitespace normalized",
			input: " error\x00with\x01control \n\t chars ",
			assert: func(t *testing.T, got string) {
				if got != "error with control chars" {
					t.Fatalf("got %q, want %q", got, "error with control chars")
				}
			},
		},
		{
			name:  "unicode truncation honors rune boundary",
			input: strings.Repeat("🎉", maxTransitionErrorSummaryRunes+10),
			assert: func(t *testing.T, got string) {
				if !utf8.ValidString(got) {
					t.Fatal("truncated summary is not valid UTF-8")
				}
				if gotRunes := len([]rune(got)); gotRunes != maxTransitionErrorSummaryRunes {
					t.Fatalf("rune length = %d, want %d", gotRunes, maxTransitionErrorSummaryRunes)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeTransitionErrorSummary(tt.input)
			tt.assert(t, got)
		})
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

func TestExecuteStage_ContextCanceledBeforeSelection(t *testing.T) {
	s := NewSchedulerWithConfig(capacityExhaustedRegistry{}, &sequenceTaskExecutor{}, SchedulerConfig{
		DefaultWorkerMaxConcurrency: 1,
	})
	stage := &Stage{
		ID:    "stage-canceled",
		JobID: "job-canceled",
		Tasks: []*Task{{
			ID:      "task-canceled",
			StageID: "stage-canceled",
			JobID:   "job-canceled",
		}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s.executeStage(ctx, stage)
	if err == nil {
		t.Fatal("expected canceled context error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("executeStage error = %v, want %v", err, context.Canceled)
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

func TestExecuteTaskRecordsStaleEpochDispatchRejectionReason(t *testing.T) {
	s := NewSchedulerWithConfig(nil, &failingTaskExecutor{err: remote.ErrStaleDispatchAuthority}, SchedulerConfig{})
	task := &Task{
		ID:        "task-stale-epoch",
		AgentID:   "agent-1",
		AgentName: "agent",
		Input:     &agent.AgentInput{},
	}
	node := registry.NodeInfo{ID: "worker-1"}

	before := counterVecValue(t, "dagens_scheduler_dispatch_rejections_total", "reason", "stale_epoch")
	err := s.executeTask(context.Background(), task, node)
	if err == nil {
		t.Fatal("expected executeTask to fail")
	}

	after := counterVecValue(t, "dagens_scheduler_dispatch_rejections_total", "reason", "stale_epoch")
	if after != before+1 {
		t.Fatalf("stale_epoch dispatch rejections = %v, want %v", after, before+1)
	}
}

func TestExecuteTaskRecordsMissingAuthorityDispatchRejectionReason(t *testing.T) {
	s := NewSchedulerWithConfig(nil, &failingTaskExecutor{err: remote.ErrMissingDispatchAuthority}, SchedulerConfig{})
	task := &Task{
		ID:        "task-missing-authority",
		AgentID:   "agent-1",
		AgentName: "agent",
		Input:     &agent.AgentInput{},
	}
	node := registry.NodeInfo{ID: "worker-1"}

	before := counterVecValue(t, "dagens_scheduler_dispatch_rejections_total", "reason", "missing_authority")
	err := s.executeTask(context.Background(), task, node)
	if err == nil {
		t.Fatal("expected executeTask to fail")
	}

	after := counterVecValue(t, "dagens_scheduler_dispatch_rejections_total", "reason", "missing_authority")
	if after != before+1 {
		t.Fatalf("missing_authority dispatch rejections = %v, want %v", after, before+1)
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

func TestClaimTaskDispatchTransitionRejectsDuplicateClaim(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	task := &Task{
		ID:             "task-claim-dup",
		JobID:          "job-claim-dup",
		StageID:        "stage-1",
		LifecycleState: TaskStatePending,
	}

	if err := s.claimTaskDispatchTransition(context.Background(), nil, task, "worker-1", 1); err != nil {
		t.Fatalf("first claimTaskDispatchTransition error = %v, want nil", err)
	}
	if err := s.claimTaskDispatchTransition(context.Background(), nil, task, "worker-2", 1); !errors.Is(err, ErrDispatchClaimRejected) {
		t.Fatalf("second claimTaskDispatchTransition error = %v, want %v", err, ErrDispatchClaimRejected)
	}

	store, ok := s.TransitionStore().(*InMemoryTransitionStore)
	if !ok {
		t.Fatal("expected in-memory transition store")
	}
	records, err := store.ListTransitionsByJob(context.Background(), task.JobID)
	if err != nil {
		t.Fatalf("ListTransitionsByJob unexpected error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("transition count = %d, want 1 after duplicate claim rejection", len(records))
	}
	if records[0].Transition != TransitionTaskDispatched {
		t.Fatalf("transition[0] = %q, want %q", records[0].Transition, TransitionTaskDispatched)
	}
}

func TestExecuteTaskWithRetryRejectsFencingConflictWithoutExecuting(t *testing.T) {
	executor := &sequenceTaskExecutor{}
	s := NewSchedulerWithConfig(nil, executor, SchedulerConfig{
		MaxDispatchAttempts:         1,
		DispatchRejectCooldown:      5 * time.Second,
		DefaultWorkerMaxConcurrency: 1,
	})

	store, ok := s.TransitionStore().(*InMemoryTransitionStore)
	if !ok {
		t.Fatal("expected in-memory transition store")
	}
	task := &Task{
		ID:             "task-fencing-conflict",
		JobID:          "job-fencing-conflict",
		StageID:        "stage-1",
		AgentID:        "agent-1",
		AgentName:      "agent",
		Input:          &agent.AgentInput{},
		LifecycleState: TaskStatePending,
	}
	if err := store.UpsertTask(context.Background(), DurableTaskRecord{
		TaskID:       task.ID,
		JobID:        task.JobID,
		StageID:      task.StageID,
		NodeID:       "worker-locked",
		CurrentState: TaskStateDispatched,
		LastAttempt:  1,
		UpdatedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("UpsertTask unexpected error: %v", err)
	}

	nodes := []registry.NodeInfo{{ID: "worker-1"}, {ID: "worker-2"}}
	err := s.executeTaskWithRetry(context.Background(), task, nodes[0], nodes)
	if !errors.Is(err, ErrDispatchClaimRejected) {
		t.Fatalf("executeTaskWithRetry error = %v, want %v", err, ErrDispatchClaimRejected)
	}
	if len(executor.callNodes) != 0 {
		t.Fatalf("executor call count = %d, want 0 when fencing claim rejected", len(executor.callNodes))
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

type contextCaptureTransitionStore struct {
	inner     *InMemoryTransitionStore
	key       interface{}
	lastValue interface{}
}

type blockingAppendTransitionStore struct {
	*InMemoryTransitionStore
	block   chan struct{}
	entered chan struct{}
	once    sync.Once
}

type testContextKey string

type toggleLeadershipProvider struct {
	isLeader atomic.Bool
}

func (p *toggleLeadershipProvider) setLeader(v bool) {
	p.isLeader.Store(v)
}

func (p *toggleLeadershipProvider) DispatchAuthority(context.Context) (LeadershipAuthority, error) {
	return LeadershipAuthority{
		IsLeader: p.isLeader.Load(),
		Epoch:    "test-epoch",
		LeaderID: "leader-test",
	}, nil
}

type lifecycleLeadershipProvider struct {
	authority LeadershipAuthority
	startErr  error
	started   atomic.Bool
	stopped   atomic.Bool
}

type captureTracer struct {
	lastStartCtxHasMarker bool
}

func (t *captureTracer) StartSpan(ctx context.Context, name string) (context.Context, telemetry.Span) {
	_, t.lastStartCtxHasMarker = ctx.Value(testContextKey("parent")).(string)
	return ctx, &captureSpan{}
}

func (t *captureTracer) GetSpan(ctx context.Context) telemetry.Span {
	return &captureSpan{}
}

type captureSpan struct{}

func (s *captureSpan) SetAttribute(string, interface{})        {}
func (s *captureSpan) SetStatus(telemetry.StatusCode, string)  {}
func (s *captureSpan) AddEvent(string, map[string]interface{}) {}
func (s *captureSpan) End()                                    {}
func (s *captureSpan) Context() context.Context                { return context.Background() }
func (s *captureSpan) TraceID() string                         { return "" }
func (s *captureSpan) SpanID() string                          { return "" }
func (s *captureSpan) SpanContext() trace.SpanContext          { return trace.SpanContext{} }

func (p *lifecycleLeadershipProvider) Start(context.Context) error {
	if p.startErr != nil {
		return p.startErr
	}
	p.started.Store(true)
	return nil
}

func (p *lifecycleLeadershipProvider) Stop() {
	p.stopped.Store(true)
}

func (p *lifecycleLeadershipProvider) DispatchAuthority(context.Context) (LeadershipAuthority, error) {
	return p.authority, nil
}

func (s *contextCaptureTransitionStore) capture(ctx context.Context) {
	if s.lastValue != nil {
		return
	}
	if v := ctx.Value(s.key); v != nil {
		s.lastValue = v
	}
}

func (s *contextCaptureTransitionStore) AppendTransition(ctx context.Context, record TransitionRecord) error {
	s.capture(ctx)
	return s.inner.AppendTransition(ctx, record)
}

func (s *contextCaptureTransitionStore) UpsertJob(ctx context.Context, job DurableJobRecord) error {
	s.capture(ctx)
	return s.inner.UpsertJob(ctx, job)
}

func (s *contextCaptureTransitionStore) UpsertTask(ctx context.Context, task DurableTaskRecord) error {
	s.capture(ctx)
	return s.inner.UpsertTask(ctx, task)
}

func (s *contextCaptureTransitionStore) ListUnfinishedJobs(ctx context.Context) ([]DurableJobRecord, error) {
	return s.inner.ListUnfinishedJobs(ctx)
}

func (s *contextCaptureTransitionStore) ListTransitionsByJob(ctx context.Context, jobID string) ([]TransitionRecord, error) {
	return s.inner.ListTransitionsByJob(ctx, jobID)
}

func (s *contextCaptureTransitionStore) WithTx(ctx context.Context, fn func(tx TransitionStoreTx) error) error {
	s.capture(ctx)
	return fn(s)
}

func (s *blockingAppendTransitionStore) AppendTransition(ctx context.Context, record TransitionRecord) error {
	s.once.Do(func() {
		close(s.entered)
		<-s.block
	})
	return s.InMemoryTransitionStore.AppendTransition(ctx, record)
}

func (s *blockingAppendTransitionStore) WithTx(ctx context.Context, fn func(tx TransitionStoreTx) error) error {
	return fn(s)
}

type panicTransitionStore struct{}

func (panicTransitionStore) AppendTransition(context.Context, TransitionRecord) error { return nil }
func (panicTransitionStore) UpsertJob(context.Context, DurableJobRecord) error        { return nil }
func (panicTransitionStore) UpsertTask(context.Context, DurableTaskRecord) error      { return nil }
func (panicTransitionStore) ListUnfinishedJobs(context.Context) ([]DurableJobRecord, error) {
	panic("boom in ListUnfinishedJobs")
}
func (panicTransitionStore) ListTransitionsByJob(context.Context, string) ([]TransitionRecord, error) {
	return nil, nil
}

func TestStart_RecoveryFlagsResetAfterPanic(t *testing.T) {
	s := NewSchedulerWithConfig(capacityExhaustedRegistry{}, &sequenceTaskExecutor{}, SchedulerConfig{})
	if err := s.SetTransitionStore(panicTransitionStore{}); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}

	var recovered interface{}
	func() {
		defer func() {
			recovered = recover()
		}()
		s.Start()
	}()

	if recovered == nil {
		t.Fatal("expected panic from transition store")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.recovering {
		t.Fatal("expected recovering=false after panic cleanup")
	}
	if s.recoveryCancel != nil {
		t.Fatal("expected recoveryCancel=nil after panic cleanup")
	}
	if s.started {
		t.Fatal("expected started=false when recovery panics")
	}
}

func TestDispatchEligibleNodes_FiltersControlPlaneNodes(t *testing.T) {
	s := NewSchedulerWithConfig(capacityExhaustedRegistry{}, nil, SchedulerConfig{})

	nodes := []registry.NodeInfo{
		{ID: "worker-1", Healthy: true, Port: 50051, Capabilities: []string{"generic-agent"}},
		{ID: "api-capability", Healthy: true, Port: 8080, Capabilities: []string{"control-plane"}},
		{ID: "api-metadata", Healthy: true, Port: 8080, Metadata: map[string]string{"role": "control-plane"}},
		{ID: "worker-2", Healthy: true, Port: 50051, Metadata: map[string]string{"role": "execution-plane"}},
	}

	eligible := s.dispatchEligibleNodes(nodes)
	if len(eligible) != 2 {
		t.Fatalf("eligible node count = %d, want 2", len(eligible))
	}

	ids := map[string]struct{}{}
	for _, node := range eligible {
		ids[node.ID] = struct{}{}
	}
	if _, ok := ids["worker-1"]; !ok {
		t.Fatal("expected worker-1 to be eligible")
	}
	if _, ok := ids["worker-2"]; !ok {
		t.Fatal("expected worker-2 to be eligible")
	}
	if _, ok := ids["api-capability"]; ok {
		t.Fatal("api-capability must be filtered from eligible nodes")
	}
	if _, ok := ids["api-metadata"]; ok {
		t.Fatal("api-metadata must be filtered from eligible nodes")
	}
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

func seedDurableQueuedJobForReconcileTest(t *testing.T, store *InMemoryTransitionStore, jobID string) {
	t.Helper()
	now := time.Now()
	job := DurableJobRecord{
		JobID:          jobID,
		Name:           jobID,
		CurrentState:   JobStateQueued,
		LastSequenceID: 3,
		CreatedAt:      now,
		UpdatedAt:      now.Add(2 * time.Second),
	}
	task := DurableTaskRecord{
		TaskID:       jobID + "-task-1",
		JobID:        jobID,
		StageID:      "stage-1",
		CurrentState: TaskStatePending,
		LastAttempt:  0,
		UpdatedAt:    now.Add(time.Second),
	}
	transitions := []TransitionRecord{
		{
			SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted,
			JobID: jobID, NewState: string(JobStateSubmitted), OccurredAt: now,
		},
		{
			SequenceID: 2, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated,
			JobID: jobID, TaskID: task.TaskID, NewState: string(TaskStatePending), OccurredAt: now.Add(time.Second),
		},
		{
			SequenceID: 3, EntityType: TransitionEntityJob, Transition: TransitionJobQueued,
			JobID: jobID, PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(2 * time.Second),
		},
	}
	if err := store.UpsertJob(context.Background(), job); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}
	if err := store.UpsertTask(context.Background(), task); err != nil {
		t.Fatalf("UpsertTask unexpected error: %v", err)
	}
	for _, tr := range transitions {
		if err := store.AppendTransition(context.Background(), tr); err != nil {
			t.Fatalf("AppendTransition(%d) unexpected error: %v", tr.SequenceID, err)
		}
	}
}

func seedDurableMalformedQueuedJobForReconcileTest(t *testing.T, store *InMemoryTransitionStore, jobID string) {
	t.Helper()
	now := time.Now()
	job := DurableJobRecord{
		JobID:          jobID,
		Name:           jobID,
		CurrentState:   JobStateQueued,
		LastSequenceID: 5,
		CreatedAt:      now,
		UpdatedAt:      now.Add(4 * time.Second),
	}
	task := DurableTaskRecord{
		TaskID:       jobID + "-task-1",
		JobID:        jobID,
		StageID:      "stage-1",
		CurrentState: TaskStatePending,
		LastAttempt:  1,
		UpdatedAt:    now.Add(3 * time.Second),
	}
	transitions := []TransitionRecord{
		{
			SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted,
			JobID: jobID, NewState: string(JobStateSubmitted), OccurredAt: now,
		},
		{
			SequenceID: 2, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated,
			JobID: jobID, TaskID: task.TaskID, NewState: string(TaskStatePending), OccurredAt: now.Add(time.Second),
		},
		{
			SequenceID: 3, EntityType: TransitionEntityTask, Transition: TransitionTaskDispatched,
			JobID: jobID, TaskID: task.TaskID, PreviousState: string(TaskStatePending), NewState: string(TaskStateDispatched), OccurredAt: now.Add(2 * time.Second),
		},
		{
			SequenceID: 4, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated,
			JobID: jobID, TaskID: task.TaskID, PreviousState: string(TaskStateDispatched), NewState: string(TaskStatePending), OccurredAt: now.Add(3 * time.Second),
		},
		{
			SequenceID: 5, EntityType: TransitionEntityJob, Transition: TransitionJobQueued,
			JobID: jobID, PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(4 * time.Second),
		},
	}
	if err := store.UpsertJob(context.Background(), job); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}
	if err := store.UpsertTask(context.Background(), task); err != nil {
		t.Fatalf("UpsertTask unexpected error: %v", err)
	}
	for _, tr := range transitions {
		if err := store.AppendTransition(context.Background(), tr); err != nil {
			t.Fatalf("AppendTransition(%d) unexpected error: %v", tr.SequenceID, err)
		}
	}
}

func seedHydratedQueuedRuntimeJobForReconcileTest(s *Scheduler, jobID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := NewJob(jobID, jobID)
	job.LifecycleState = JobStateQueued
	job.Status = JobPending
	stage := &Stage{ID: "stage-1", JobID: jobID, Status: JobPending}
	stage.Tasks = []*Task{
		{
			ID:             jobID + "-task-hydrated",
			StageID:        stage.ID,
			JobID:          jobID,
			AgentID:        "agent-1",
			AgentName:      "Start",
			Input:          &agent.AgentInput{Instruction: "reconcile test"},
			PartitionKey:   "pk-1",
			Status:         JobPending,
			LifecycleState: TaskStatePending,
		},
	}
	job.Stages = []*Stage{stage}
	s.jobs[jobID] = job
}

func jobStatusForTest(s *Scheduler, jobID string) (JobStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[jobID]
	if !ok {
		return "", fmt.Errorf("job %s not found", jobID)
	}
	return job.Status, nil
}
