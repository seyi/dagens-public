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
	return nil
}

func (s *InMemoryTransitionStore) UpsertJob(_ context.Context, job DurableJobRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
