package scheduler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReplayJobStateReconstructsCurrentState(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	records := []TransitionRecord{
		{
			SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted,
			JobID: "job-1", NewState: string(JobStateSubmitted), OccurredAt: now,
		},
		{
			SequenceID: 2, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated,
			JobID: "job-1", TaskID: "task-1", NewState: string(TaskStatePending), OccurredAt: now.Add(time.Second),
		},
		{
			SequenceID: 3, EntityType: TransitionEntityJob, Transition: TransitionJobQueued,
			JobID: "job-1", PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(2 * time.Second),
		},
		{
			SequenceID: 4, EntityType: TransitionEntityTask, Transition: TransitionTaskDispatched,
			JobID: "job-1", TaskID: "task-1", PreviousState: string(TaskStatePending), NewState: string(TaskStateDispatched), NodeID: "worker-1", Attempt: 1, OccurredAt: now.Add(3 * time.Second),
		},
		{
			SequenceID: 5, EntityType: TransitionEntityTask, Transition: TransitionTaskRunning,
			JobID: "job-1", TaskID: "task-1", PreviousState: string(TaskStateDispatched), NewState: string(TaskStateRunning), NodeID: "worker-1", Attempt: 1, OccurredAt: now.Add(4 * time.Second),
		},
		{
			SequenceID: 6, EntityType: TransitionEntityJob, Transition: TransitionJobRunning,
			JobID: "job-1", PreviousState: string(JobStateQueued), NewState: string(JobStateRunning), OccurredAt: now.Add(5 * time.Second),
		},
	}

	for _, record := range records {
		if err := store.AppendTransition(context.Background(), record); err != nil {
			t.Fatalf("AppendTransition unexpected error: %v", err)
		}
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID: "job-1", CurrentState: JobStateRunning, CreatedAt: now, UpdatedAt: now.Add(5 * time.Second),
	}); err != nil {
		t.Fatalf("UpsertJob unexpected error: %v", err)
	}

	replayed, err := ReplayJobState(context.Background(), store, "job-1")
	if err != nil {
		t.Fatalf("ReplayJobState unexpected error: %v", err)
	}

	if replayed.Job.CurrentState != JobStateRunning {
		t.Fatalf("job current state = %q, want %q", replayed.Job.CurrentState, JobStateRunning)
	}
	task, ok := replayed.Tasks["task-1"]
	if !ok {
		t.Fatal("expected task-1 to be reconstructed")
	}
	if task.CurrentState != TaskStateRunning {
		t.Fatalf("task current state = %q, want %q", task.CurrentState, TaskStateRunning)
	}
	if task.NodeID != "worker-1" {
		t.Fatalf("task node id = %q, want %q", task.NodeID, "worker-1")
	}
}

func TestReplayStateFromStoreReturnsOnlyUnfinishedJobs(t *testing.T) {
	// ReplayStateFromStore uses the unfinished-job index (UpsertJob/ListUnfinishedJobs)
	// to select which jobs to replay, then reconstructs each from transitions.
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	job1 := []TransitionRecord{
		{SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted, JobID: "job-1", NewState: string(JobStateSubmitted), OccurredAt: now},
		{SequenceID: 2, EntityType: TransitionEntityJob, Transition: TransitionJobQueued, JobID: "job-1", PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(time.Second)},
	}
	job2 := []TransitionRecord{
		{SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted, JobID: "job-2", NewState: string(JobStateSubmitted), OccurredAt: now},
		{SequenceID: 2, EntityType: TransitionEntityJob, Transition: TransitionJobSucceeded, JobID: "job-2", PreviousState: string(JobStateRunning), NewState: string(JobStateSucceeded), OccurredAt: now.Add(time.Second)},
	}

	for _, record := range append(job1, job2...) {
		if err := store.AppendTransition(context.Background(), record); err != nil {
			t.Fatalf("AppendTransition unexpected error: %v", err)
		}
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID: "job-1", CurrentState: JobStateQueued, CreatedAt: now, UpdatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("UpsertJob job-1 unexpected error: %v", err)
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID: "job-2", CurrentState: JobStateSucceeded, CreatedAt: now, UpdatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("UpsertJob job-2 unexpected error: %v", err)
	}

	replayed, err := ReplayStateFromStore(context.Background(), store)
	if err != nil {
		t.Fatalf("ReplayStateFromStore unexpected error: %v", err)
	}
	if len(replayed) != 1 {
		t.Fatalf("replayed job count = %d, want 1", len(replayed))
	}
	if replayed[0].Job.JobID != "job-1" {
		t.Fatalf("replayed job id = %q, want %q", replayed[0].Job.JobID, "job-1")
	}
}

func TestReplayJobStateEmptyHistory(t *testing.T) {
	store := NewInMemoryTransitionStore()

	replayed, err := ReplayJobState(context.Background(), store, "job-empty")
	if err != nil {
		t.Fatalf("ReplayJobState unexpected error: %v", err)
	}
	if replayed.Job.JobID != "job-empty" {
		t.Fatalf("replayed job id = %q, want %q", replayed.Job.JobID, "job-empty")
	}
	if replayed.Job.CurrentState != "" {
		t.Fatalf("replayed job state = %q, want empty", replayed.Job.CurrentState)
	}
	if len(replayed.Tasks) != 0 {
		t.Fatalf("replayed task count = %d, want 0", len(replayed.Tasks))
	}
	if len(replayed.Transitions) != 0 {
		t.Fatalf("replayed transition count = %d, want 0", len(replayed.Transitions))
	}
}

func TestReplayJobStateReconstructsMultipleTasks(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	records := []TransitionRecord{
		{SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted, JobID: "job-1", NewState: string(JobStateSubmitted), OccurredAt: now},
		{SequenceID: 2, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated, JobID: "job-1", TaskID: "task-1", NewState: string(TaskStatePending), OccurredAt: now.Add(time.Second)},
		{SequenceID: 3, EntityType: TransitionEntityTask, Transition: TransitionTaskCreated, JobID: "job-1", TaskID: "task-2", NewState: string(TaskStatePending), OccurredAt: now.Add(2 * time.Second)},
		{SequenceID: 4, EntityType: TransitionEntityTask, Transition: TransitionTaskDispatched, JobID: "job-1", TaskID: "task-1", PreviousState: string(TaskStatePending), NewState: string(TaskStateDispatched), NodeID: "worker-1", Attempt: 1, OccurredAt: now.Add(3 * time.Second)},
		{SequenceID: 5, EntityType: TransitionEntityTask, Transition: TransitionTaskRunning, JobID: "job-1", TaskID: "task-1", PreviousState: string(TaskStateDispatched), NewState: string(TaskStateRunning), NodeID: "worker-1", Attempt: 1, OccurredAt: now.Add(4 * time.Second)},
		{SequenceID: 6, EntityType: TransitionEntityTask, Transition: TransitionTaskDispatched, JobID: "job-1", TaskID: "task-2", PreviousState: string(TaskStatePending), NewState: string(TaskStateDispatched), NodeID: "worker-2", Attempt: 1, OccurredAt: now.Add(5 * time.Second)},
		{SequenceID: 7, EntityType: TransitionEntityTask, Transition: TransitionTaskFailed, JobID: "job-1", TaskID: "task-2", PreviousState: string(TaskStateDispatched), NewState: string(TaskStateFailed), NodeID: "worker-2", Attempt: 1, OccurredAt: now.Add(6 * time.Second)},
	}
	for _, record := range records {
		if err := store.AppendTransition(context.Background(), record); err != nil {
			t.Fatalf("AppendTransition unexpected error: %v", err)
		}
	}

	replayed, err := ReplayJobState(context.Background(), store, "job-1")
	if err != nil {
		t.Fatalf("ReplayJobState unexpected error: %v", err)
	}
	if len(replayed.Tasks) != 2 {
		t.Fatalf("replayed task count = %d, want 2", len(replayed.Tasks))
	}
	if got := replayed.Tasks["task-1"].CurrentState; got != TaskStateRunning {
		t.Fatalf("task-1 state = %q, want %q", got, TaskStateRunning)
	}
	if got := replayed.Tasks["task-2"].CurrentState; got != TaskStateFailed {
		t.Fatalf("task-2 state = %q, want %q", got, TaskStateFailed)
	}
}

func TestReplayStateFromStoreStableSortByCreatedAtThenJobID(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	records := []TransitionRecord{
		{SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted, JobID: "job-b", NewState: string(JobStateSubmitted), OccurredAt: now},
		{SequenceID: 2, EntityType: TransitionEntityJob, Transition: TransitionJobQueued, JobID: "job-b", PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(time.Second)},
		{SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted, JobID: "job-a", NewState: string(JobStateSubmitted), OccurredAt: now},
		{SequenceID: 2, EntityType: TransitionEntityJob, Transition: TransitionJobQueued, JobID: "job-a", PreviousState: string(JobStateSubmitted), NewState: string(JobStateQueued), OccurredAt: now.Add(time.Second)},
	}
	for _, record := range records {
		if err := store.AppendTransition(context.Background(), record); err != nil {
			t.Fatalf("AppendTransition unexpected error: %v", err)
		}
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID: "job-b", CurrentState: JobStateQueued, CreatedAt: now, UpdatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("UpsertJob job-b unexpected error: %v", err)
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID: "job-a", CurrentState: JobStateQueued, CreatedAt: now, UpdatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("UpsertJob job-a unexpected error: %v", err)
	}

	replayed, err := ReplayStateFromStore(context.Background(), store)
	if err != nil {
		t.Fatalf("ReplayStateFromStore unexpected error: %v", err)
	}
	if len(replayed) != 2 {
		t.Fatalf("replayed job count = %d, want 2", len(replayed))
	}
	if replayed[0].Job.JobID != "job-a" || replayed[1].Job.JobID != "job-b" {
		t.Fatalf("unexpected replay order = [%s, %s], want [job-a, job-b]", replayed[0].Job.JobID, replayed[1].Job.JobID)
	}
}

func TestReplayJobStateRejectsIllegalTransitions(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	if err := store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted,
		JobID: "job-1", NewState: string(JobStateSubmitted), OccurredAt: now,
	}); err != nil {
		t.Fatalf("AppendTransition unexpected error: %v", err)
	}
	if err := store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 2, EntityType: TransitionEntityJob, Transition: TransitionJobSucceeded,
		JobID: "job-1", PreviousState: string(JobStateSubmitted), NewState: string(JobStateSucceeded), OccurredAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("AppendTransition unexpected error: %v", err)
	}

	_, err := ReplayJobState(context.Background(), store, "job-1")
	if err == nil {
		t.Fatal("expected replay to fail for illegal job transition sequence")
	}
	if !strings.Contains(err.Error(), "illegal job transition") {
		t.Fatalf("expected illegal transition error, got: %v", err)
	}
}

func TestReplayJobStateRejectsInvalidTransitionRecord(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	store.mu.Lock()
	store.transitions["job-1"] = []TransitionRecord{
		{
			SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted,
			JobID: "job-1", NewState: "BROKEN", OccurredAt: now,
		},
	}
	store.mu.Unlock()

	_, err := ReplayJobState(context.Background(), store, "job-1")
	if err == nil {
		t.Fatal("expected replay to fail for invalid transition record")
	}
	if !strings.Contains(err.Error(), "invalid transition during replay") {
		t.Fatalf("expected validation error, got: %v", err)
	}
}

func TestReplayJobStateContextCanceled(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	if err := store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID: 1, EntityType: TransitionEntityJob, Transition: TransitionJobSubmitted,
		JobID: "job-1", NewState: string(JobStateSubmitted), OccurredAt: now,
	}); err != nil {
		t.Fatalf("AppendTransition unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ReplayJobState(ctx, store, "job-1")
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}
