package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRecoverFromTransitionsRebuildsVisibilityState(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	records := []TransitionRecord{
		{SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted, JobID: "job-1", NewState: string(JobStateSubmitted), OccurredAt: now},
		{SequenceID: 2, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated, JobID: "job-1", TaskID: "task-1", NewState: string(TaskStatePending), OccurredAt: now.Add(time.Second)},
		{SequenceID: 3, EntityType: TransitionEntityJob, Transition: TransitionJobQueued, JobID: "job-1", PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(2 * time.Second)},
		{SequenceID: 4, EntityType: TransitionEntityTask, Transition: TransitionTaskDispatched, JobID: "job-1", TaskID: "task-1", PreviousState: string(TaskStatePending), NewState: string(TaskStateDispatched), NodeID: "worker-1", Attempt: 1, OccurredAt: now.Add(3 * time.Second)},
		{SequenceID: 5, EntityType: TransitionEntityTask, Transition: TransitionTaskRunning, JobID: "job-1", TaskID: "task-1", PreviousState: string(TaskStateDispatched), NewState: string(TaskStateRunning), NodeID: "worker-1", Attempt: 1, OccurredAt: now.Add(4 * time.Second)},
		{SequenceID: 6, EntityType: TransitionEntityJob, Transition: TransitionJobRunning, JobID: "job-1", PreviousState: string(JobStateQueued), NewState: string(JobStateRunning), OccurredAt: now.Add(5 * time.Second)},
	}
	for _, record := range records {
		if err := store.AppendTransition(context.Background(), record); err != nil {
			t.Fatalf("AppendTransition unexpected error: %v", err)
		}
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:        "job-1",
		Name:         "recovered-job",
		CurrentState: JobStateRunning,
		CreatedAt:    now,
		UpdatedAt:    now.Add(5 * time.Second),
	}); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		JobQueueSize: 1,
	})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}

	if err := s.RecoverFromTransitions(context.Background()); err != nil {
		t.Fatalf("RecoverFromTransitions unexpected error: %v", err)
	}

	job, err := s.GetJob("job-1")
	if err != nil {
		t.Fatalf("GetJob unexpected error: %v", err)
	}
	if job.Status != JobRunning {
		t.Fatalf("job runtime status = %q, want %q", job.Status, JobRunning)
	}
	if job.LifecycleState != JobStateRunning {
		t.Fatalf("job lifecycle state = %q, want %q", job.LifecycleState, JobStateRunning)
	}
	if recovered, _ := job.Metadata["recovered"].(bool); !recovered {
		t.Fatalf("expected recovered metadata marker")
	}
	if len(job.Stages) != 1 {
		t.Fatalf("stage count = %d, want 1", len(job.Stages))
	}
	if len(job.Stages[0].Tasks) != 1 {
		t.Fatalf("task count = %d, want 1", len(job.Stages[0].Tasks))
	}
	task := job.Stages[0].Tasks[0]
	if task.LifecycleState != TaskStateRunning {
		t.Fatalf("task lifecycle state = %q, want %q", task.LifecycleState, TaskStateRunning)
	}
	if task.Status != JobRunning {
		t.Fatalf("task runtime status = %q, want %q", task.Status, JobRunning)
	}
}

func TestRecoverFromTransitionsSkipsExistingJobs(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	if err := store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted,
		JobID: "job-1", NewState: string(JobStateSubmitted), OccurredAt: now,
	}); err != nil {
		t.Fatalf("AppendTransition unexpected error: %v", err)
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID: "job-1", CurrentState: JobStateSubmitted, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}

	existing := NewJob("job-1", "existing")
	if err := s.SubmitJob(existing); err != nil {
		t.Fatalf("SubmitJob unexpected error: %v", err)
	}

	if err := s.RecoverFromTransitions(context.Background()); err != nil {
		t.Fatalf("RecoverFromTransitions unexpected error: %v", err)
	}

	job, err := s.GetJob("job-1")
	if err != nil {
		t.Fatalf("GetJob unexpected error: %v", err)
	}
	if job.Name != "existing" {
		t.Fatalf("expected existing job to be preserved, got name %q", job.Name)
	}
}

func TestRecoverFromTransitionsReconstructsTaskRetryAttempts(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	records := []TransitionRecord{
		{SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted, JobID: "job-retry", NewState: string(JobStateSubmitted), OccurredAt: now},
		{SequenceID: 2, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated, JobID: "job-retry", TaskID: "task-retry", NewState: string(TaskStatePending), OccurredAt: now.Add(time.Second)},
		{SequenceID: 3, EntityType: TransitionEntityTask, Transition: TransitionTaskDispatched, JobID: "job-retry", TaskID: "task-retry", PreviousState: string(TaskStatePending), NewState: string(TaskStateDispatched), Attempt: 1, OccurredAt: now.Add(2 * time.Second)},
		{SequenceID: 4, EntityType: TransitionEntityTask, Transition: TransitionTaskDispatched, JobID: "job-retry", TaskID: "task-retry", PreviousState: string(TaskStateDispatched), NewState: string(TaskStateDispatched), Attempt: 2, OccurredAt: now.Add(3 * time.Second)},
		{SequenceID: 5, EntityType: TransitionEntityTask, Transition: TransitionTaskRunning, JobID: "job-retry", TaskID: "task-retry", PreviousState: string(TaskStateDispatched), NewState: string(TaskStateRunning), Attempt: 2, OccurredAt: now.Add(4 * time.Second)},
	}
	for _, record := range records {
		if err := store.AppendTransition(context.Background(), record); err != nil {
			t.Fatalf("AppendTransition unexpected error: %v", err)
		}
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID: "job-retry", CurrentState: JobStateRunning, CreatedAt: now, UpdatedAt: now.Add(4 * time.Second),
	}); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}
	if err := s.RecoverFromTransitions(context.Background()); err != nil {
		t.Fatalf("RecoverFromTransitions unexpected error: %v", err)
	}

	job, err := s.GetJob("job-retry")
	if err != nil {
		t.Fatalf("GetJob unexpected error: %v", err)
	}
	if len(job.Stages) != 1 || len(job.Stages[0].Tasks) != 1 {
		t.Fatalf("expected one recovered task, got stages=%d tasks=%d", len(job.Stages), len(job.Stages[0].Tasks))
	}
	task := job.Stages[0].Tasks[0]
	if task.Attempts != 2 {
		t.Fatalf("task attempts = %d, want 2", task.Attempts)
	}
	if task.LifecycleState != TaskStateRunning {
		t.Fatalf("task lifecycle state = %q, want %q", task.LifecycleState, TaskStateRunning)
	}
}

func TestStartRejectsSubmitWhileRecovering(t *testing.T) {
	base := NewInMemoryTransitionStore()
	now := time.Now().UTC()
	if err := base.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted,
		JobID: "job-existing", NewState: string(JobStateSubmitted), OccurredAt: now,
	}); err != nil {
		t.Fatalf("AppendTransition unexpected error: %v", err)
	}
	if err := base.UpsertJob(context.Background(), DurableJobRecord{
		JobID: "job-existing", CurrentState: JobStateSubmitted, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}

	blockStore := &blockingTransitionStore{
		TransitionStore: base,
		started:         make(chan struct{}, 1),
		release:         make(chan struct{}),
	}

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 2})
	if err := s.SetTransitionStore(blockStore); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}

	startDone := make(chan struct{})
	go func() {
		s.Start()
		close(startDone)
	}()

	select {
	case <-blockStore.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for recovery to start")
	}

	err := s.SubmitJob(NewJob("job-during-recovery", "during-recovery"))
	if !errors.Is(err, ErrSchedulerRecovering) {
		t.Fatalf("SubmitJob error = %v, want %v", err, ErrSchedulerRecovering)
	}

	close(blockStore.release)
	select {
	case <-startDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for scheduler start to complete")
	}
	s.Stop()
}

func TestRecoverFromTransitionsRecordsSuccessMetricsAndLog(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	if err := store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted,
		JobID: "job-metrics-success", NewState: string(JobStateSubmitted), OccurredAt: now,
	}); err != nil {
		t.Fatalf("AppendTransition unexpected error: %v", err)
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID: "job-metrics-success", CurrentState: JobStateSubmitted, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}

	beforeRuns := counterVecValue(t, "spark_agent_scheduler_recovery_runs_total", "status", "succeeded")
	beforeRecovered := metricCounterValue(t, "spark_agent_scheduler_recovered_jobs_total")
	logger := schedulerLogger()
	beforeLogs := len(logger.GetLogs())

	if err := s.RecoverFromTransitions(context.Background()); err != nil {
		t.Fatalf("RecoverFromTransitions unexpected error: %v", err)
	}

	afterRuns := counterVecValue(t, "spark_agent_scheduler_recovery_runs_total", "status", "succeeded")
	if afterRuns != beforeRuns+1 {
		t.Fatalf("scheduler recovery succeeded runs = %v, want %v", afterRuns, beforeRuns+1)
	}
	afterRecovered := metricCounterValue(t, "spark_agent_scheduler_recovered_jobs_total")
	if afterRecovered != beforeRecovered+1 {
		t.Fatalf("scheduler recovered jobs total = %v, want %v", afterRecovered, beforeRecovered+1)
	}

	logs := logger.GetLogs()
	if len(logs) != beforeLogs+1 {
		t.Fatalf("log count = %d, want %d", len(logs), beforeLogs+1)
	}
	last := logs[len(logs)-1]
	if last.Message != "scheduler startup recovery completed" {
		t.Fatalf("log message = %q, want %q", last.Message, "scheduler startup recovery completed")
	}
	if last.Attributes["recovered_jobs"] != 1 {
		t.Fatalf("recovered_jobs log attr = %v, want %d", last.Attributes["recovered_jobs"], 1)
	}
}

func TestRecoverFromTransitionsRecordsFailureMetricsAndLog(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	// Insert an unfinished job index entry.
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID: "job-metrics-fail", CurrentState: JobStateSubmitted, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}

	// Inject an invalid transition directly to force replay failure.
	store.mu.Lock()
	store.transitions["job-metrics-fail"] = []TransitionRecord{
		{
			SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted,
			JobID: "job-metrics-fail", NewState: "BROKEN", OccurredAt: now,
		},
	}
	store.mu.Unlock()

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}

	beforeRuns := counterVecValue(t, "spark_agent_scheduler_recovery_runs_total", "status", "failed")
	logger := schedulerLogger()
	beforeLogs := len(logger.GetLogs())

	err := s.RecoverFromTransitions(context.Background())
	if err == nil {
		t.Fatal("expected RecoverFromTransitions to fail")
	}

	afterRuns := counterVecValue(t, "spark_agent_scheduler_recovery_runs_total", "status", "failed")
	if afterRuns != beforeRuns+1 {
		t.Fatalf("scheduler recovery failed runs = %v, want %v", afterRuns, beforeRuns+1)
	}

	logs := logger.GetLogs()
	if len(logs) != beforeLogs+1 {
		t.Fatalf("log count = %d, want %d", len(logs), beforeLogs+1)
	}
	last := logs[len(logs)-1]
	if last.Message != "scheduler startup recovery failed" {
		t.Fatalf("log message = %q, want %q", last.Message, "scheduler startup recovery failed")
	}
}

type blockingTransitionStore struct {
	TransitionStore
	started chan struct{}
	release chan struct{}
}

func (b *blockingTransitionStore) ListUnfinishedJobs(ctx context.Context) ([]DurableJobRecord, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	select {
	case <-b.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return b.TransitionStore.ListUnfinishedJobs(ctx)
}
