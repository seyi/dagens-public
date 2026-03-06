package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
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
		SequenceID: 1,
		EntityType: TransitionEntityJob,
		Transition: TransitionJobSubmitted,
		JobID:      "job-1",
		NewState:   string(JobStateSubmitted),
		OccurredAt: now,
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

func TestInMemoryTransitionStoreWithTxRollsBackOnError(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	errExpected := errors.New("fail and rollback")
	err := store.WithTx(context.Background(), func(tx TransitionStoreTx) error {
		if err := tx.AppendTransition(context.Background(), TransitionRecord{
			SequenceID: 1,
			EntityType: TransitionEntityJob,
			Transition: TransitionJobSubmitted,
			JobID:      "job-rollback",
			NewState:   string(JobStateSubmitted),
			OccurredAt: now,
		}); err != nil {
			return err
		}
		if err := tx.UpsertJob(context.Background(), DurableJobRecord{
			JobID:        "job-rollback",
			Name:         "rollback",
			CurrentState: JobStateSubmitted,
			CreatedAt:    now,
			UpdatedAt:    now,
		}); err != nil {
			return err
		}
		return errExpected
	})
	if !errors.Is(err, errExpected) {
		t.Fatalf("WithTx error = %v, want %v", err, errExpected)
	}

	records, err := store.ListTransitionsByJob(context.Background(), "job-rollback")
	if err != nil {
		t.Fatalf("ListTransitionsByJob unexpected error: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("transition count after rollback = %d, want 0", len(records))
	}

	jobs, err := store.ListUnfinishedJobs(context.Background())
	if err != nil {
		t.Fatalf("ListUnfinishedJobs unexpected error: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("unfinished jobs after rollback = %d, want 0", len(jobs))
	}
}

func TestInMemoryTransitionStoreWithTxConcurrentCommits(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	const workers = 16
	var wg sync.WaitGroup
	errCh := make(chan error, workers)

	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()

			jobID := fmt.Sprintf("job-%d", i)
			err := store.WithTx(context.Background(), func(tx TransitionStoreTx) error {
				if err := tx.AppendTransition(context.Background(), TransitionRecord{
					SequenceID: 1,
					EntityType: TransitionEntityJob,
					Transition: TransitionJobSubmitted,
					JobID:      jobID,
					NewState:   string(JobStateSubmitted),
					OccurredAt: now.Add(time.Duration(i) * time.Millisecond),
				}); err != nil {
					return err
				}
				return tx.UpsertJob(context.Background(), DurableJobRecord{
					JobID:        jobID,
					Name:         jobID,
					CurrentState: JobStateSubmitted,
					CreatedAt:    now.Add(time.Duration(i) * time.Millisecond),
					UpdatedAt:    now.Add(time.Duration(i) * time.Millisecond),
				})
			})
			if err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent WithTx returned error: %v", err)
		}
	}

	for i := 0; i < workers; i++ {
		jobID := fmt.Sprintf("job-%d", i)
		records, err := store.ListTransitionsByJob(context.Background(), jobID)
		if err != nil {
			t.Fatalf("ListTransitionsByJob(%q) unexpected error: %v", jobID, err)
		}
		if len(records) != 1 {
			t.Fatalf("job %q transition count = %d, want 1", jobID, len(records))
		}
		if records[0].SequenceID != 1 {
			t.Fatalf("job %q sequence_id = %d, want 1", jobID, records[0].SequenceID)
		}
	}

	unfinished, err := store.ListUnfinishedJobs(context.Background())
	if err != nil {
		t.Fatalf("ListUnfinishedJobs unexpected error: %v", err)
	}
	if len(unfinished) != workers {
		t.Fatalf("unfinished job count = %d, want %d", len(unfinished), workers)
	}
}

func TestInMemoryTransitionStoreWithTxConcurrentSameKey(t *testing.T) {
	store := NewInMemoryTransitionStore()
	now := time.Now().UTC()

	const workers = 16
	var wg sync.WaitGroup
	errCh := make(chan error, workers)

	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()

			err := store.WithTx(context.Background(), func(tx TransitionStoreTx) error {
				seq := uint64(i + 1)
				if err := tx.AppendTransition(context.Background(), TransitionRecord{
					SequenceID: seq,
					EntityType: TransitionEntityJob,
					Transition: TransitionJobSubmitted,
					JobID:      "job-same-key",
					NewState:   string(JobStateSubmitted),
					OccurredAt: now.Add(time.Duration(i) * time.Millisecond),
				}); err != nil {
					return err
				}
				return tx.UpsertJob(context.Background(), DurableJobRecord{
					JobID:        "job-same-key",
					Name:         "same-key",
					CurrentState: JobStateSubmitted,
					CreatedAt:    now,
					UpdatedAt:    now.Add(time.Duration(i) * time.Millisecond),
				})
			})
			if err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent same-key WithTx returned error: %v", err)
		}
	}

	records, err := store.ListTransitionsByJob(context.Background(), "job-same-key")
	if err != nil {
		t.Fatalf("ListTransitionsByJob unexpected error: %v", err)
	}
	if len(records) != workers {
		t.Fatalf("same-key transition count = %d, want %d", len(records), workers)
	}

	for i, record := range records {
		want := uint64(i + 1)
		if record.SequenceID != want {
			t.Fatalf("same-key record[%d] sequence_id = %d, want %d", i, record.SequenceID, want)
		}
	}
}
