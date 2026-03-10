package scheduler

import (
	"context"
	"sort"
	"sync"
)

// InMemoryTransitionStore is the first simple TransitionStore implementation.
// It is process-local and non-durable, but it provides the correct lifecycle
// contracts before a real durable backend is introduced.
type InMemoryTransitionStore struct {
	mu          sync.RWMutex
	transitions map[string][]TransitionRecord
	jobs        map[string]DurableJobRecord
	tasks       map[string]DurableTaskRecord
}

func NewInMemoryTransitionStore() *InMemoryTransitionStore {
	return &InMemoryTransitionStore{
		transitions: make(map[string][]TransitionRecord),
		jobs:        make(map[string]DurableJobRecord),
		tasks:       make(map[string]DurableTaskRecord),
	}
}

func (s *InMemoryTransitionStore) AppendTransition(_ context.Context, record TransitionRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.transitions[record.JobID] = append(s.transitions[record.JobID], record)
	sort.Slice(s.transitions[record.JobID], func(i, j int) bool {
		return s.transitions[record.JobID][i].SequenceID < s.transitions[record.JobID][j].SequenceID
	})
	if job, ok := s.jobs[record.JobID]; ok && record.SequenceID > job.LastSequenceID {
		job.LastSequenceID = record.SequenceID
		s.jobs[record.JobID] = job
	}
	return nil
}

func (s *InMemoryTransitionStore) UpsertJob(_ context.Context, job DurableJobRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.jobs[job.JobID]; ok && existing.LastSequenceID > job.LastSequenceID {
		job.LastSequenceID = existing.LastSequenceID
	}
	s.jobs[job.JobID] = job
	return nil
}

func (s *InMemoryTransitionStore) UpsertTask(_ context.Context, task DurableTaskRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[task.TaskID] = task
	return nil
}

func (s *InMemoryTransitionStore) ListUnfinishedJobs(_ context.Context) ([]DurableJobRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]DurableJobRecord, 0, len(s.jobs))
	for _, job := range s.jobs {
		if IsTerminalJobState(job.CurrentState) {
			continue
		}
		result = append(result, job)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result, nil
}

func (s *InMemoryTransitionStore) ListTransitionsByJob(_ context.Context, jobID string) ([]TransitionRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	records := s.transitions[jobID]
	out := make([]TransitionRecord, len(records))
	copy(out, records)
	return out, nil
}

// WithTx executes transition writes atomically against the in-memory store by
// applying changes to a snapshot and committing only on successful callback
// return.
//
// Locking semantics: this method holds the store mutex for the full callback
// duration. This keeps snapshot creation + commit linearizable and is intended
// for short, in-memory transition write callbacks only.
func (s *InMemoryTransitionStore) WithTx(ctx context.Context, fn func(tx TransitionStoreTx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tx := &inMemoryTransitionStoreTx{
		transitions: cloneTransitionMap(s.transitions),
		jobs:        cloneJobsMap(s.jobs),
		tasks:       cloneTasksMap(s.tasks),
	}

	if err := fn(tx); err != nil {
		return err
	}

	s.transitions = tx.transitions
	s.jobs = tx.jobs
	s.tasks = tx.tasks
	return nil
}

type inMemoryTransitionStoreTx struct {
	transitions map[string][]TransitionRecord
	jobs        map[string]DurableJobRecord
	tasks       map[string]DurableTaskRecord
}

func (tx *inMemoryTransitionStoreTx) AppendTransition(_ context.Context, record TransitionRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	tx.transitions[record.JobID] = append(tx.transitions[record.JobID], record)
	sort.Slice(tx.transitions[record.JobID], func(i, j int) bool {
		return tx.transitions[record.JobID][i].SequenceID < tx.transitions[record.JobID][j].SequenceID
	})
	if job, ok := tx.jobs[record.JobID]; ok && record.SequenceID > job.LastSequenceID {
		job.LastSequenceID = record.SequenceID
		tx.jobs[record.JobID] = job
	}
	return nil
}

func (tx *inMemoryTransitionStoreTx) UpsertJob(_ context.Context, job DurableJobRecord) error {
	if existing, ok := tx.jobs[job.JobID]; ok && existing.LastSequenceID > job.LastSequenceID {
		job.LastSequenceID = existing.LastSequenceID
	}
	tx.jobs[job.JobID] = job
	return nil
}

func (tx *inMemoryTransitionStoreTx) UpsertTask(_ context.Context, task DurableTaskRecord) error {
	tx.tasks[task.TaskID] = task
	return nil
}

func (tx *inMemoryTransitionStoreTx) ClaimTaskDispatch(_ context.Context, claim TaskDispatchClaim) (bool, error) {
	if claim.TaskID == "" {
		return false, nil
	}
	existing, ok := tx.tasks[claim.TaskID]
	if !ok {
		tx.tasks[claim.TaskID] = DurableTaskRecord{
			TaskID:       claim.TaskID,
			JobID:        claim.JobID,
			StageID:      claim.StageID,
			NodeID:       claim.NodeID,
			CurrentState: TaskStateDispatched,
			LastAttempt:  claim.Attempt,
			UpdatedAt:    claim.UpdatedAt,
		}
		return true, nil
	}

	if existing.LastAttempt < claim.Attempt &&
		existing.CurrentState != TaskStateRunning &&
		existing.CurrentState != TaskStateSucceeded {
		existing.JobID = claim.JobID
		existing.StageID = claim.StageID
		existing.NodeID = claim.NodeID
		existing.CurrentState = TaskStateDispatched
		existing.LastAttempt = claim.Attempt
		existing.UpdatedAt = claim.UpdatedAt
		tx.tasks[claim.TaskID] = existing
		return true, nil
	}

	return false, nil
}

func cloneTransitionMap(in map[string][]TransitionRecord) map[string][]TransitionRecord {
	out := make(map[string][]TransitionRecord, len(in))
	for jobID, records := range in {
		// TransitionRecord is currently a value-type struct (no pointers/maps/
		// slices), so slice copy here is safe and produces independent snapshots.
		cp := make([]TransitionRecord, len(records))
		copy(cp, records)
		out[jobID] = cp
	}
	return out
}

func cloneJobsMap(in map[string]DurableJobRecord) map[string]DurableJobRecord {
	out := make(map[string]DurableJobRecord, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneTasksMap(in map[string]DurableTaskRecord) map[string]DurableTaskRecord {
	out := make(map[string]DurableTaskRecord, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
