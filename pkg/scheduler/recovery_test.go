package scheduler

import (
	"context"
	"errors"
	"strings"
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

func TestRecoverFromTransitionsResumesRecoveredQueuedJobsWhenEnabled(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	records := []TransitionRecord{
		{SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted, JobID: "job-resume", NewState: string(JobStateSubmitted), OccurredAt: now},
		{SequenceID: 2, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated, JobID: "job-resume", TaskID: "task-1", NewState: string(TaskStatePending), OccurredAt: now.Add(time.Second)},
		{SequenceID: 3, EntityType: TransitionEntityJob, Transition: TransitionJobQueued, JobID: "job-resume", PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(2 * time.Second)},
	}
	for _, record := range records {
		if err := store.AppendTransition(context.Background(), record); err != nil {
			t.Fatalf("AppendTransition unexpected error: %v", err)
		}
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:          "job-resume",
		Name:           "job-resume",
		CurrentState:   JobStateQueued,
		LastSequenceID: 3,
		CreatedAt:      now,
		UpdatedAt:      now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}

	beforeResumed := metricCounterValue(t, "dagens_scheduler_recovery_resumed_queued_jobs_total")
	beforeSkipped := metricCounterValue(t, "dagens_scheduler_recovery_resume_skipped_queued_jobs_total")

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		JobQueueSize:                    2,
		EnableResumeRecoveredQueuedJobs: true,
	})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}

	if err := s.RecoverFromTransitions(context.Background()); err != nil {
		t.Fatalf("RecoverFromTransitions unexpected error: %v", err)
	}

	if got := len(s.jobQueue); got != 1 {
		t.Fatalf("recovered queue depth = %d, want 1", got)
	}
	resumed := metricCounterValue(t, "dagens_scheduler_recovery_resumed_queued_jobs_total")
	if resumed != beforeResumed+1 {
		t.Fatalf("resumed queued jobs metric = %v, want %v", resumed, beforeResumed+1)
	}
	skipped := metricCounterValue(t, "dagens_scheduler_recovery_resume_skipped_queued_jobs_total")
	if skipped != beforeSkipped {
		t.Fatalf("skipped queued jobs metric = %v, want %v", skipped, beforeSkipped)
	}
}

func TestRecoverFromTransitionsDoesNotResumeQueuedJobsWhenDisabled(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	records := []TransitionRecord{
		{SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted, JobID: "job-visibility-only", NewState: string(JobStateSubmitted), OccurredAt: now},
		{SequenceID: 2, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated, JobID: "job-visibility-only", TaskID: "task-1", NewState: string(TaskStatePending), OccurredAt: now.Add(time.Second)},
		{SequenceID: 3, EntityType: TransitionEntityJob, Transition: TransitionJobQueued, JobID: "job-visibility-only", PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(2 * time.Second)},
	}
	for _, record := range records {
		if err := store.AppendTransition(context.Background(), record); err != nil {
			t.Fatalf("AppendTransition unexpected error: %v", err)
		}
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:          "job-visibility-only",
		Name:           "job-visibility-only",
		CurrentState:   JobStateQueued,
		LastSequenceID: 3,
		CreatedAt:      now,
		UpdatedAt:      now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 2})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}

	if err := s.RecoverFromTransitions(context.Background()); err != nil {
		t.Fatalf("RecoverFromTransitions unexpected error: %v", err)
	}
	if got := len(s.jobQueue); got != 0 {
		t.Fatalf("recovered queue depth with resume disabled = %d, want 0", got)
	}
}

func TestRecoverFromTransitionsDoesNotResumeRunningJobsWhenEnabled(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	records := []TransitionRecord{
		{SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted, JobID: "job-running", NewState: string(JobStateSubmitted), OccurredAt: now},
		{SequenceID: 2, EntityType: TransitionEntityJob, Transition: TransitionJobQueued, JobID: "job-running", PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(time.Second)},
		{SequenceID: 3, EntityType: TransitionEntityJob, Transition: TransitionJobRunning, JobID: "job-running", PreviousState: string(JobStateQueued), NewState: string(JobStateRunning), OccurredAt: now.Add(2 * time.Second)},
	}
	for _, record := range records {
		if err := store.AppendTransition(context.Background(), record); err != nil {
			t.Fatalf("AppendTransition unexpected error: %v", err)
		}
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:          "job-running",
		Name:           "job-running",
		CurrentState:   JobStateRunning,
		LastSequenceID: 3,
		CreatedAt:      now,
		UpdatedAt:      now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		JobQueueSize:                    2,
		EnableResumeRecoveredQueuedJobs: true,
	})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}
	if err := s.RecoverFromTransitions(context.Background()); err != nil {
		t.Fatalf("RecoverFromTransitions unexpected error: %v", err)
	}

	if got := len(s.jobQueue); got != 0 {
		t.Fatalf("recovered queue depth for RUNNING job = %d, want 0", got)
	}
}

func TestRecoverFromTransitionsRebuildsAwaitingHumanJob(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	records := []TransitionRecord{
		{SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted, JobID: "job-awaiting-human", NewState: string(JobStateSubmitted), OccurredAt: now},
		{SequenceID: 2, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated, JobID: "job-awaiting-human", TaskID: "task-1", NewState: string(TaskStatePending), OccurredAt: now.Add(time.Second)},
		{SequenceID: 3, EntityType: TransitionEntityJob, Transition: TransitionJobQueued, JobID: "job-awaiting-human", PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(2 * time.Second)},
		{SequenceID: 4, EntityType: TransitionEntityJob, Transition: TransitionJobRunning, JobID: "job-awaiting-human", PreviousState: string(JobStateQueued), NewState: string(JobStateRunning), OccurredAt: now.Add(3 * time.Second)},
		{SequenceID: 5, EntityType: TransitionEntityJob, Transition: TransitionJobAwaitingHuman, JobID: "job-awaiting-human", PreviousState: string(JobStateRunning), NewState: string(JobStateAwaitingHuman), OccurredAt: now.Add(4 * time.Second)},
	}
	for _, record := range records {
		if err := store.AppendTransition(context.Background(), record); err != nil {
			t.Fatalf("AppendTransition unexpected error: %v", err)
		}
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:          "job-awaiting-human",
		Name:           "job-awaiting-human",
		CurrentState:   JobStateAwaitingHuman,
		LastSequenceID: 5,
		CreatedAt:      now,
		UpdatedAt:      now.Add(4 * time.Second),
	}); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		JobQueueSize:                    2,
		EnableResumeRecoveredQueuedJobs: true,
	})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}

	if err := s.RecoverFromTransitions(context.Background()); err != nil {
		t.Fatalf("RecoverFromTransitions unexpected error: %v", err)
	}

	job, err := s.GetJob("job-awaiting-human")
	if err != nil {
		t.Fatalf("GetJob unexpected error: %v", err)
	}
	if job.LifecycleState != JobStateAwaitingHuman {
		t.Fatalf("job lifecycle state = %q, want %q", job.LifecycleState, JobStateAwaitingHuman)
	}
	if job.Status != JobAwaitingHuman {
		t.Fatalf("job runtime status = %q, want %q", job.Status, JobAwaitingHuman)
	}
	if got := len(s.jobQueue); got != 0 {
		t.Fatalf("recovered queue depth for awaiting-human job = %d, want 0", got)
	}
}

func TestRecoverFromTransitionsFailsForMalformedNonInitialTransitionMissingPreviousState(t *testing.T) {
	now := time.Now().UTC()
	store := NewInMemoryTransitionStore()

	// Seed unfinished job visibility row.
	store.jobs["job-malformed-prev"] = DurableJobRecord{
		JobID:          "job-malformed-prev",
		Name:           "job-malformed-prev",
		CurrentState:   JobStateQueued,
		LastSequenceID: 2,
		CreatedAt:      now,
		UpdatedAt:      now.Add(time.Second),
	}
	// Inject malformed history directly to exercise replay fail-fast behavior.
	// Sequence 2 is a non-initial QUEUED transition but omits PreviousState.
	store.transitions["job-malformed-prev"] = []TransitionRecord{
		{
			SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted,
			JobID: "job-malformed-prev", NewState: string(JobStateSubmitted), OccurredAt: now,
		},
		{
			SequenceID: 2, EntityType: TransitionEntityJob, Transition: TransitionJobQueued,
			JobID: "job-malformed-prev", PreviousState: "", NewState: string(JobStateQueued), OccurredAt: now.Add(time.Second),
		},
	}

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}

	err := s.RecoverFromTransitions(context.Background())
	if err == nil {
		t.Fatal("expected recovery error for malformed transition history")
	}
	if !strings.Contains(err.Error(), "previous_state required") {
		t.Fatalf("recovery error = %v, want previous_state validation failure", err)
	}
}

func TestRecoverFromTransitionsSkipsQueuedJobsWithDispatchedTasks(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	records := []TransitionRecord{
		{SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted, JobID: "job-dispatched", NewState: string(JobStateSubmitted), OccurredAt: now},
		{SequenceID: 2, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated, JobID: "job-dispatched", TaskID: "task-1", NewState: string(TaskStatePending), OccurredAt: now.Add(time.Second)},
		{SequenceID: 3, EntityType: TransitionEntityJob, Transition: TransitionJobQueued, JobID: "job-dispatched", PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(2 * time.Second)},
		{SequenceID: 4, EntityType: TransitionEntityTask, Transition: TransitionTaskDispatched, JobID: "job-dispatched", TaskID: "task-1", PreviousState: string(TaskStatePending), NewState: string(TaskStateDispatched), NodeID: "worker-1", Attempt: 1, OccurredAt: now.Add(3 * time.Second)},
	}
	for _, record := range records {
		if err := store.AppendTransition(context.Background(), record); err != nil {
			t.Fatalf("AppendTransition unexpected error: %v", err)
		}
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:          "job-dispatched",
		Name:           "job-dispatched",
		CurrentState:   JobStateQueued,
		LastSequenceID: 4,
		CreatedAt:      now,
		UpdatedAt:      now.Add(3 * time.Second),
	}); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		JobQueueSize:                    2,
		EnableResumeRecoveredQueuedJobs: true,
	})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}
	if err := s.RecoverFromTransitions(context.Background()); err != nil {
		t.Fatalf("RecoverFromTransitions unexpected error: %v", err)
	}

	if got := len(s.jobQueue); got != 0 {
		t.Fatalf("recovered queue depth for QUEUED job with DISPATCHED task = %d, want 0", got)
	}
}

func TestRecoverFromTransitionsSkipsQueuedJobsWhenQueueFull(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:          "job-q1",
		Name:           "job-q1",
		CurrentState:   JobStateQueued,
		LastSequenceID: 1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}
	if err := store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobQueued,
		JobID: "job-q1", PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now,
	}); err != nil {
		t.Fatalf("AppendTransition unexpected error: %v", err)
	}

	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:          "job-q2",
		Name:           "job-q2",
		CurrentState:   JobStateQueued,
		LastSequenceID: 1,
		CreatedAt:      now.Add(time.Second),
		UpdatedAt:      now.Add(time.Second),
	}); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}
	if err := store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobQueued,
		JobID: "job-q2", PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("AppendTransition unexpected error: %v", err)
	}

	beforeResumed := metricCounterValue(t, "dagens_scheduler_recovery_resumed_queued_jobs_total")
	beforeSkipped := metricCounterValue(t, "dagens_scheduler_recovery_resume_skipped_queued_jobs_total")

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		JobQueueSize:                    1,
		EnableResumeRecoveredQueuedJobs: true,
	})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}
	if err := s.RecoverFromTransitions(context.Background()); err != nil {
		t.Fatalf("RecoverFromTransitions unexpected error: %v", err)
	}

	if got := len(s.jobQueue); got != 1 {
		t.Fatalf("recovered queue depth with queue-full scenario = %d, want 1", got)
	}
	resumed := metricCounterValue(t, "dagens_scheduler_recovery_resumed_queued_jobs_total")
	if resumed != beforeResumed+1 {
		t.Fatalf("resumed queued jobs metric = %v, want %v", resumed, beforeResumed+1)
	}
	skipped := metricCounterValue(t, "dagens_scheduler_recovery_resume_skipped_queued_jobs_total")
	if skipped != beforeSkipped+1 {
		t.Fatalf("skipped queued jobs metric = %v, want %v", skipped, beforeSkipped+1)
	}
}

func TestRecoverFromTransitionsSkipsQueuedJobsWithMixedTaskStates(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	records := []TransitionRecord{
		{SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted, JobID: "job-mixed", NewState: string(JobStateSubmitted), OccurredAt: now},
		{SequenceID: 2, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated, JobID: "job-mixed", TaskID: "task-pending", NewState: string(TaskStatePending), OccurredAt: now.Add(time.Second)},
		{SequenceID: 3, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated, JobID: "job-mixed", TaskID: "task-dispatched", NewState: string(TaskStatePending), OccurredAt: now.Add(2 * time.Second)},
		{SequenceID: 4, EntityType: TransitionEntityJob, Transition: TransitionJobQueued, JobID: "job-mixed", PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(3 * time.Second)},
		{SequenceID: 5, EntityType: TransitionEntityTask, Transition: TransitionTaskDispatched, JobID: "job-mixed", TaskID: "task-dispatched", PreviousState: string(TaskStatePending), NewState: string(TaskStateDispatched), NodeID: "worker-1", Attempt: 1, OccurredAt: now.Add(4 * time.Second)},
	}
	for _, record := range records {
		if err := store.AppendTransition(context.Background(), record); err != nil {
			t.Fatalf("AppendTransition unexpected error: %v", err)
		}
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:          "job-mixed",
		Name:           "job-mixed",
		CurrentState:   JobStateQueued,
		LastSequenceID: 5,
		CreatedAt:      now,
		UpdatedAt:      now.Add(4 * time.Second),
	}); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}

	beforeUnsafe := metricCounterValue(t, "dagens_scheduler_recovery_resume_skipped_unsafe_queued_jobs_total")

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		JobQueueSize:                    2,
		EnableResumeRecoveredQueuedJobs: true,
	})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}
	if err := s.RecoverFromTransitions(context.Background()); err != nil {
		t.Fatalf("RecoverFromTransitions unexpected error: %v", err)
	}

	if got := len(s.jobQueue); got != 0 {
		t.Fatalf("recovered queue depth for QUEUED job with mixed task states = %d, want 0", got)
	}
	afterUnsafe := metricCounterValue(t, "dagens_scheduler_recovery_resume_skipped_unsafe_queued_jobs_total")
	if afterUnsafe != beforeUnsafe+1 {
		t.Fatalf("unsafe skipped queued jobs metric = %v, want %v", afterUnsafe, beforeUnsafe+1)
	}
}

func TestRecoverFromTransitionsSkipsQueuedJobsWithTerminalTaskState(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	records := []TransitionRecord{
		{SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted, JobID: "job-terminal", NewState: string(JobStateSubmitted), OccurredAt: now},
		{SequenceID: 2, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated, JobID: "job-terminal", TaskID: "task-1", NewState: string(TaskStatePending), OccurredAt: now.Add(time.Second)},
		{SequenceID: 3, EntityType: TransitionEntityJob, Transition: TransitionJobQueued, JobID: "job-terminal", PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(2 * time.Second)},
		{SequenceID: 4, EntityType: TransitionEntityTask, Transition: TransitionTaskFailed, JobID: "job-terminal", TaskID: "task-1", PreviousState: string(TaskStatePending), NewState: string(TaskStateFailed), ErrorSummary: "terminal failure", OccurredAt: now.Add(3 * time.Second)},
	}
	for _, record := range records {
		if err := store.AppendTransition(context.Background(), record); err != nil {
			t.Fatalf("AppendTransition unexpected error: %v", err)
		}
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:          "job-terminal",
		Name:           "job-terminal",
		CurrentState:   JobStateQueued,
		LastSequenceID: 4,
		CreatedAt:      now,
		UpdatedAt:      now.Add(3 * time.Second),
	}); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}

	beforeUnsafe := metricCounterValue(t, "dagens_scheduler_recovery_resume_skipped_unsafe_queued_jobs_total")

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		JobQueueSize:                    2,
		EnableResumeRecoveredQueuedJobs: true,
	})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}
	if err := s.RecoverFromTransitions(context.Background()); err != nil {
		t.Fatalf("RecoverFromTransitions unexpected error: %v", err)
	}

	if got := len(s.jobQueue); got != 0 {
		t.Fatalf("recovered queue depth for QUEUED job with terminal task state = %d, want 0", got)
	}
	afterUnsafe := metricCounterValue(t, "dagens_scheduler_recovery_resume_skipped_unsafe_queued_jobs_total")
	if afterUnsafe != beforeUnsafe+1 {
		t.Fatalf("unsafe skipped queued jobs metric = %v, want %v", afterUnsafe, beforeUnsafe+1)
	}
}

func TestIsSafeForQueuedResume_AllowsEmptyLifecycleState(t *testing.T) {
	job := &Job{
		LifecycleState: JobStateQueued,
		Stages: []*Stage{
			{
				Tasks: []*Task{
					{LifecycleState: ""},
				},
			},
		},
	}

	if !isSafeForQueuedResume(job) {
		t.Fatal("expected empty task lifecycle state to be safe for queued resume")
	}
}

func TestIsSafeForQueuedResume_HandlesNilComponents(t *testing.T) {
	if isSafeForQueuedResume(nil) {
		t.Fatal("nil job should be unsafe for queued resume")
	}

	jobWithNilStage := &Job{
		LifecycleState: JobStateQueued,
		Stages:         []*Stage{nil},
	}
	if !isSafeForQueuedResume(jobWithNilStage) {
		t.Fatal("job with nil stage should be treated as safe when no unsafe task states are present")
	}

	jobWithNilTask := &Job{
		LifecycleState: JobStateQueued,
		Stages: []*Stage{
			{
				Tasks: []*Task{nil},
			},
		},
	}
	if !isSafeForQueuedResume(jobWithNilTask) {
		t.Fatal("job with nil task should be treated as safe when no unsafe task states are present")
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

func TestRecoverFromTransitionsSeedsJobSequenceFromReplayedTransitions(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	records := []TransitionRecord{
		{SequenceID: 4, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted, JobID: "job-seq", NewState: string(JobStateSubmitted), OccurredAt: now},
		{SequenceID: 5, EntityType: TransitionEntityJob, Transition: TransitionJobQueued, JobID: "job-seq", PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(time.Second)},
	}
	for _, record := range records {
		if err := store.AppendTransition(context.Background(), record); err != nil {
			t.Fatalf("AppendTransition unexpected error: %v", err)
		}
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:        "job-seq",
		Name:         "job-seq",
		CurrentState: JobStateQueued,
		CreatedAt:    now,
		UpdatedAt:    now.Add(time.Second),
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

	if got := s.nextSequenceID("job-seq"); got != 6 {
		t.Fatalf("nextSequenceID after recovery = %d, want %d", got, 6)
	}
}

func TestRecoverFromTransitionsSeedsSequenceForExistingJob(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	records := []TransitionRecord{
		{SequenceID: 9, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted, JobID: "job-existing-seq", NewState: string(JobStateSubmitted), OccurredAt: now},
		{SequenceID: 10, EntityType: TransitionEntityJob, Transition: TransitionJobQueued, JobID: "job-existing-seq", PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(time.Second)},
	}
	for _, record := range records {
		if err := store.AppendTransition(context.Background(), record); err != nil {
			t.Fatalf("AppendTransition unexpected error: %v", err)
		}
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:          "job-existing-seq",
		Name:           "existing-seq",
		CurrentState:   JobStateQueued,
		LastSequenceID: 10,
		CreatedAt:      now,
		UpdatedAt:      now.Add(time.Second),
	}); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 2})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}
	s.mu.Lock()
	s.jobs["job-existing-seq"] = &Job{
		ID:             "job-existing-seq",
		Name:           "existing",
		Status:         JobPending,
		LifecycleState: JobStateSubmitted,
		CreatedAt:      now,
		UpdatedAt:      now,
		Stages:         []*Stage{},
		Metadata:       map[string]interface{}{},
	}
	s.mu.Unlock()
	if err := s.RecoverFromTransitions(context.Background()); err != nil {
		t.Fatalf("RecoverFromTransitions unexpected error: %v", err)
	}

	if got := s.nextSequenceID("job-existing-seq"); got != 11 {
		t.Fatalf("nextSequenceID for existing recovered job = %d, want %d", got, 11)
	}
}

func TestSeedJobSequenceLockedUsesDurableBaselineWhenHigher(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	transitions := []TransitionRecord{
		{SequenceID: 5},
	}

	seeded, maxSeq := s.seedJobSequenceLocked("job-seed-baseline", 10, transitions)
	if !seeded {
		t.Fatal("expected seeded=true")
	}
	if maxSeq != 10 {
		t.Fatalf("maxSeq = %d, want %d", maxSeq, 10)
	}
	if got := s.jobSequences["job-seed-baseline"]; got != 10 {
		t.Fatalf("job sequence = %d, want %d", got, 10)
	}
}

func TestSeedJobSequenceLockedUsesTransitionMaxWhenHigher(t *testing.T) {
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	transitions := []TransitionRecord{
		{SequenceID: 7},
	}

	seeded, maxSeq := s.seedJobSequenceLocked("job-seed-transition", 3, transitions)
	if !seeded {
		t.Fatal("expected seeded=true")
	}
	if maxSeq != 7 {
		t.Fatalf("maxSeq = %d, want %d", maxSeq, 7)
	}
	if got := s.jobSequences["job-seed-transition"]; got != 7 {
		t.Fatalf("job sequence = %d, want %d", got, 7)
	}
}

func TestDeriveStageStatusPrecedenceFailedOverCompleted(t *testing.T) {
	stageTasks := []*Task{
		{Status: JobCompleted},
		{Status: JobFailed},
	}

	if got := deriveStageStatus(stageTasks); got != JobFailed {
		t.Fatalf("deriveStageStatus = %q, want %q", got, JobFailed)
	}
}

func TestDeriveStageStatusTable(t *testing.T) {
	tests := []struct {
		name  string
		tasks []*Task
		want  JobStatus
	}{
		{
			name:  "empty",
			tasks: []*Task{},
			want:  JobPending,
		},
		{
			name: "all completed",
			tasks: []*Task{
				{Status: JobCompleted},
				{Status: JobCompleted},
			},
			want: JobCompleted,
		},
		{
			name: "running plus pending",
			tasks: []*Task{
				{Status: JobRunning},
				{Status: JobPending},
			},
			want: JobRunning,
		},
		{
			name: "failed plus running",
			tasks: []*Task{
				{Status: JobFailed},
				{Status: JobRunning},
			},
			want: JobFailed,
		},
		{
			name: "blocked treated as failed",
			tasks: []*Task{
				{Status: JobBlocked},
				{Status: JobCompleted},
			},
			want: JobFailed,
		},
		{
			name: "all pending",
			tasks: []*Task{
				{Status: JobPending},
				{Status: JobPending},
			},
			want: JobPending,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveStageStatus(tt.tasks); got != tt.want {
				t.Fatalf("deriveStageStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRecoverFromTransitionsGroupsMissingStageIDUnderRecovered(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	records := []TransitionRecord{
		{SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted, JobID: "job-stage-fallback", NewState: string(JobStateSubmitted), OccurredAt: now},
		{SequenceID: 2, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated, JobID: "job-stage-fallback", TaskID: "task-1", NewState: string(TaskStatePending), OccurredAt: now.Add(time.Second)},
		{SequenceID: 3, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated, JobID: "job-stage-fallback", TaskID: "task-2", NewState: string(TaskStatePending), OccurredAt: now.Add(2 * time.Second)},
	}
	for _, record := range records {
		if err := store.AppendTransition(context.Background(), record); err != nil {
			t.Fatalf("AppendTransition unexpected error: %v", err)
		}
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:        "job-stage-fallback",
		Name:         "job-stage-fallback",
		CurrentState: JobStateSubmitted,
		CreatedAt:    now,
		UpdatedAt:    now.Add(2 * time.Second),
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

	job, err := s.GetJob("job-stage-fallback")
	if err != nil {
		t.Fatalf("GetJob unexpected error: %v", err)
	}
	if len(job.Stages) != 1 {
		t.Fatalf("stage count = %d, want 1", len(job.Stages))
	}
	if job.Stages[0].ID != "recovered" {
		t.Fatalf("fallback stage id = %q, want %q", job.Stages[0].ID, "recovered")
	}
	if len(job.Stages[0].Tasks) != 2 {
		t.Fatalf("task count in fallback stage = %d, want 2", len(job.Stages[0].Tasks))
	}
}

func TestRecoverFromTransitionsSetsRecoveredMetadataOnly(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	if err := store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted,
		JobID: "job-metadata-reset", NewState: string(JobStateSubmitted), OccurredAt: now,
	}); err != nil {
		t.Fatalf("AppendTransition unexpected error: %v", err)
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:        "job-metadata-reset",
		Name:         "job-metadata-reset",
		CurrentState: JobStateSubmitted,
		CreatedAt:    now,
		UpdatedAt:    now,
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

	job, err := s.GetJob("job-metadata-reset")
	if err != nil {
		t.Fatalf("GetJob unexpected error: %v", err)
	}
	if len(job.Metadata) != 1 {
		t.Fatalf("metadata size = %d, want 1", len(job.Metadata))
	}
	if recovered, ok := job.Metadata["recovered"].(bool); !ok || !recovered {
		t.Fatalf("metadata recovered marker = %v, want true", job.Metadata["recovered"])
	}
}

func TestRecoverFromTransitionsEmptyStoreRecordsSuccessMetrics(t *testing.T) {
	store := NewInMemoryTransitionStore()
	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	if err := s.SetTransitionStore(store); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}

	beforeRuns := counterVecValue(t, "dagens_scheduler_recovery_runs_total", "status", "succeeded")
	beforeRecovered := metricCounterValue(t, "dagens_scheduler_recovered_jobs_total")
	logger := schedulerLogger(t)
	beforeLogs := len(logger.GetLogs())

	if err := s.RecoverFromTransitions(context.Background()); err != nil {
		t.Fatalf("RecoverFromTransitions unexpected error: %v", err)
	}

	afterRuns := counterVecValue(t, "dagens_scheduler_recovery_runs_total", "status", "succeeded")
	if afterRuns != beforeRuns+1 {
		t.Fatalf("scheduler recovery succeeded runs = %v, want %v", afterRuns, beforeRuns+1)
	}
	afterRecovered := metricCounterValue(t, "dagens_scheduler_recovered_jobs_total")
	if afterRecovered != beforeRecovered {
		t.Fatalf("scheduler recovered jobs total = %v, want %v", afterRecovered, beforeRecovered)
	}

	logs := logger.GetLogs()
	if len(logs) != beforeLogs+1 {
		t.Fatalf("log count = %d, want %d", len(logs), beforeLogs+1)
	}
	last := logs[len(logs)-1]
	if last.Message != "scheduler startup recovery completed" {
		t.Fatalf("log message = %q, want %q", last.Message, "scheduler startup recovery completed")
	}
	if last.Attributes["recovered_jobs"] != 0 {
		t.Fatalf("recovered_jobs log attr = %v, want %d", last.Attributes["recovered_jobs"], 0)
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

func TestStopCancelsRecoveryAndPreventsRunLoopStart(t *testing.T) {
	base := NewInMemoryTransitionStore()
	now := time.Now().UTC()
	if err := base.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted,
		JobID: "job-stop-cancel", NewState: string(JobStateSubmitted), OccurredAt: now,
	}); err != nil {
		t.Fatalf("AppendTransition unexpected error: %v", err)
	}
	if err := base.UpsertJob(context.Background(), DurableJobRecord{
		JobID: "job-stop-cancel", CurrentState: JobStateSubmitted, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}

	blockStore := &blockingTransitionStore{
		TransitionStore: base,
		started:         make(chan struct{}, 1),
		release:         make(chan struct{}),
	}

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
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

	// Stop should cancel recovery context and allow Start() to return without
	// launching the run loop.
	s.Stop()

	select {
	case <-startDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Start() to return after Stop() cancellation")
	}
}

func TestStartRespectsRecoveryTimeout(t *testing.T) {
	base := NewInMemoryTransitionStore()
	now := time.Now().UTC()
	if err := base.UpsertJob(context.Background(), DurableJobRecord{
		JobID: "job-timeout", CurrentState: JobStateSubmitted, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}

	blockStore := &blockingTransitionStore{
		TransitionStore: base,
		started:         make(chan struct{}, 1),
		release:         make(chan struct{}), // never released; rely on context timeout
	}

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{
		JobQueueSize:     1,
		RecoveryTimeout:  50 * time.Millisecond,
		EnableStickiness: false,
	})
	if err := s.SetTransitionStore(blockStore); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}

	beforeRuns := counterVecValue(t, "dagens_scheduler_recovery_runs_total", "status", "canceled")
	startDone := make(chan struct{})
	go func() {
		s.Start()
		close(startDone)
	}()

	select {
	case <-blockStore.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for recovery start")
	}

	select {
	case <-startDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return within timeout window")
	}

	afterRuns := counterVecValue(t, "dagens_scheduler_recovery_runs_total", "status", "canceled")
	if afterRuns != beforeRuns+1 {
		t.Fatalf("scheduler recovery canceled runs = %v, want %v", afterRuns, beforeRuns+1)
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

	beforeRuns := counterVecValue(t, "dagens_scheduler_recovery_runs_total", "status", "succeeded")
	beforeRecovered := metricCounterValue(t, "dagens_scheduler_recovered_jobs_total")
	logger := schedulerLogger(t)
	beforeLogs := len(logger.GetLogs())

	if err := s.RecoverFromTransitions(context.Background()); err != nil {
		t.Fatalf("RecoverFromTransitions unexpected error: %v", err)
	}

	afterRuns := counterVecValue(t, "dagens_scheduler_recovery_runs_total", "status", "succeeded")
	if afterRuns != beforeRuns+1 {
		t.Fatalf("scheduler recovery succeeded runs = %v, want %v", afterRuns, beforeRuns+1)
	}
	afterRecovered := metricCounterValue(t, "dagens_scheduler_recovered_jobs_total")
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

	beforeRuns := counterVecValue(t, "dagens_scheduler_recovery_runs_total", "status", "failed")
	logger := schedulerLogger(t)
	beforeLogs := len(logger.GetLogs())

	err := s.RecoverFromTransitions(context.Background())
	if err == nil {
		t.Fatal("expected RecoverFromTransitions to fail")
	}

	afterRuns := counterVecValue(t, "dagens_scheduler_recovery_runs_total", "status", "failed")
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
	if last.Attributes["job_id"] != "job-metrics-fail" {
		t.Fatalf("job_id log attr = %v, want %q", last.Attributes["job_id"], "job-metrics-fail")
	}
	if replayErr, ok := last.Attributes["replay_error"].(string); !ok || replayErr == "" {
		t.Fatalf("replay_error log attr = %v, want non-empty string", last.Attributes["replay_error"])
	}
}

func TestRecoverFromTransitionsRecordsCanceledMetricsAndLog(t *testing.T) {
	base := NewInMemoryTransitionStore()
	now := time.Now().UTC()
	if err := base.UpsertJob(context.Background(), DurableJobRecord{
		JobID: "job-metrics-canceled", CurrentState: JobStateSubmitted, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}

	blockStore := &blockingTransitionStore{
		TransitionStore: base,
		started:         make(chan struct{}, 1),
		release:         make(chan struct{}),
	}

	s := NewSchedulerWithConfig(nil, nil, SchedulerConfig{JobQueueSize: 1})
	if err := s.SetTransitionStore(blockStore); err != nil {
		t.Fatalf("SetTransitionStore unexpected error: %v", err)
	}

	beforeRuns := counterVecValue(t, "dagens_scheduler_recovery_runs_total", "status", "canceled")
	logger := schedulerLogger(t)
	beforeLogs := len(logger.GetLogs())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		select {
		case <-blockStore.started:
			cancel()
		case <-done:
		}
	}()

	err := s.RecoverFromTransitions(ctx)
	close(done)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RecoverFromTransitions error = %v, want %v", err, context.Canceled)
	}

	afterRuns := counterVecValue(t, "dagens_scheduler_recovery_runs_total", "status", "canceled")
	if afterRuns != beforeRuns+1 {
		t.Fatalf("scheduler recovery canceled runs = %v, want %v", afterRuns, beforeRuns+1)
	}

	logs := logger.GetLogs()
	if len(logs) != beforeLogs+1 {
		t.Fatalf("log count = %d, want %d", len(logs), beforeLogs+1)
	}
	last := logs[len(logs)-1]
	if last.Message != "scheduler startup recovery failed" {
		t.Fatalf("log message = %q, want %q", last.Message, "scheduler startup recovery failed")
	}
	if last.Attributes["status"] != "canceled" {
		t.Fatalf("status log attr = %v, want %q", last.Attributes["status"], "canceled")
	}
	if replayErr, ok := last.Attributes["replay_error"].(string); !ok || replayErr == "" {
		t.Fatalf("replay_error log attr = %v, want non-empty string", last.Attributes["replay_error"])
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
