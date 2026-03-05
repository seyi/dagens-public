package scheduler

import (
	"context"
	"testing"
	"time"
)

func TestInMemoryTransitionStoreOrdersTransitionsBySequence(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	if err := store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID:    2,
		EntityType:    TransitionEntityJob,
		Transition:    TransitionJobQueued,
		JobID:         "job-1",
		PreviousState: string(JobStateSubmitted),
		NewState:      string(JobStateQueued),
		OccurredAt:    now,
	}); err != nil {
		t.Fatalf("AppendTransition(2) unexpected error: %v", err)
	}
	if err := store.AppendTransition(context.Background(), TransitionRecord{
		SequenceID:    1,
		EntityType:    TransitionEntityJob,
		Transition:    TransitionJobSubmitted,
		JobID:         "job-1",
		NewState:      string(JobStateSubmitted),
		OccurredAt:    now,
	}); err != nil {
		t.Fatalf("AppendTransition(1) unexpected error: %v", err)
	}

	records, err := store.ListTransitionsByJob(context.Background(), "job-1")
	if err != nil {
		t.Fatalf("ListTransitionsByJob unexpected error: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("transition count = %d, want 2", len(records))
	}
	if records[0].SequenceID != 1 || records[1].SequenceID != 2 {
		t.Fatalf("unexpected sequence order: got [%d, %d], want [1, 2]", records[0].SequenceID, records[1].SequenceID)
	}
}

func TestInMemoryTransitionStoreListsOnlyUnfinishedJobs(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:        "job-running",
		Name:         "running",
		CurrentState: JobStateRunning,
		CreatedAt:    now,
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("UpsertJob running unexpected error: %v", err)
	}
	if err := store.UpsertJob(context.Background(), DurableJobRecord{
		JobID:        "job-succeeded",
		Name:         "done",
		CurrentState: JobStateSucceeded,
		CreatedAt:    now.Add(time.Second),
		UpdatedAt:    now.Add(time.Second),
	}); err != nil {
		t.Fatalf("UpsertJob succeeded unexpected error: %v", err)
	}

	unfinished, err := store.ListUnfinishedJobs(context.Background())
	if err != nil {
		t.Fatalf("ListUnfinishedJobs unexpected error: %v", err)
	}
	if len(unfinished) != 1 {
		t.Fatalf("unfinished job count = %d, want 1", len(unfinished))
	}
	if unfinished[0].JobID != "job-running" {
		t.Fatalf("unfinished job id = %q, want %q", unfinished[0].JobID, "job-running")
	}
}
