package scheduler

import (
	"context"
	"fmt"
	"sort"
)

// ReplayJobError attaches the failed job identifier to a replay error.
type ReplayJobError struct {
	JobID string
	Err   error
}

func (e *ReplayJobError) Error() string {
	if e == nil {
		return "replay job error"
	}
	return fmt.Sprintf("replay failed for job %s: %v", e.JobID, e.Err)
}

func (e *ReplayJobError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ReplayedJobState is the reconstructed control-plane view for a job after
// replaying its durable transition history.
type ReplayedJobState struct {
	Job         DurableJobRecord
	Tasks       map[string]DurableTaskRecord
	Transitions []TransitionRecord
}

// ReplayStateFromStore rebuilds the current control-plane view for every
// unfinished job returned by the transition store.
//
// This is a visibility-first recovery helper. It does not redispatch work or
// attempt to reconcile ambiguous in-flight execution.
//
// Replay currently follows fail-fast semantics: if any unfinished job has an
// invalid or illegal transition sequence, the replay operation returns an
// error.
func ReplayStateFromStore(ctx context.Context, store TransitionStore) ([]ReplayedJobState, error) {
	if store == nil {
		return nil, fmt.Errorf("transition store is required")
	}

	jobs, err := store.ListUnfinishedJobs(ctx)
	if err != nil {
		return nil, err
	}

	var (
		batchedTransitions map[string][]TransitionRecord
		batchedTasks       map[string][]DurableTaskRecord
	)
	if batchStore, ok := store.(ReplayBatchLookupStore); ok && len(jobs) > 0 {
		jobIDs := make([]string, 0, len(jobs))
		for _, job := range jobs {
			jobIDs = append(jobIDs, job.JobID)
		}
		batchedTransitions, err = batchStore.ListTransitionsByJobs(ctx, jobIDs)
		if err != nil {
			return nil, err
		}
		batchedTasks, err = batchStore.ListTasksByJobs(ctx, jobIDs)
		if err != nil {
			return nil, err
		}
		for _, jobID := range jobIDs {
			if _, ok := batchedTransitions[jobID]; !ok {
				return nil, fmt.Errorf("batched replay transitions missing job %s", jobID)
			}
			if _, ok := batchedTasks[jobID]; !ok {
				return nil, fmt.Errorf("batched replay tasks missing job %s", jobID)
			}
		}
	}

	result := make([]ReplayedJobState, 0, len(jobs))
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var replayed ReplayedJobState
		if batchedTransitions != nil {
			replayed, err = replayJobStateFromData(ctx, job.JobID, batchedTransitions[job.JobID], batchedTasks[job.JobID])
		} else {
			replayed, err = ReplayJobState(ctx, store, job.JobID)
		}
		if err != nil {
			return nil, &ReplayJobError{JobID: job.JobID, Err: err}
		}
		replayed.Job.Name = job.Name
		replayed.Job.LastSequenceID = job.LastSequenceID
		if replayed.Job.CreatedAt.IsZero() {
			replayed.Job.CreatedAt = job.CreatedAt
		}
		if replayed.Job.UpdatedAt.IsZero() {
			replayed.Job.UpdatedAt = job.UpdatedAt
		}
		if len(replayed.Transitions) > 0 {
			last := replayed.Transitions[len(replayed.Transitions)-1].SequenceID
			if last > replayed.Job.LastSequenceID {
				replayed.Job.LastSequenceID = last
			}
		}
		result = append(result, replayed)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Job.CreatedAt.Equal(result[j].Job.CreatedAt) {
			return result[i].Job.JobID < result[j].Job.JobID
		}
		return result[i].Job.CreatedAt.Before(result[j].Job.CreatedAt)
	})
	return result, nil
}

// ReplayJobState rebuilds the current control-plane view for a single job from
// its ordered transition history.
//
// Ordering assumption:
// TransitionStore.ListTransitionsByJob must return transitions ordered by
// SequenceID ascending.
func ReplayJobState(ctx context.Context, store TransitionStore, jobID string) (ReplayedJobState, error) {
	if store == nil {
		return ReplayedJobState{}, fmt.Errorf("transition store is required")
	}
	if jobID == "" {
		return ReplayedJobState{}, fmt.Errorf("job_id is required")
	}

	transitions, err := store.ListTransitionsByJob(ctx, jobID)
	if err != nil {
		return ReplayedJobState{}, err
	}

	var durableTasks []DurableTaskRecord
	if taskLookup, ok := store.(DurableTaskLookupStore); ok {
		durableTasks, err = taskLookup.ListTasksByJob(ctx, jobID)
		if err != nil {
			return ReplayedJobState{}, fmt.Errorf("list durable tasks by job: %w", err)
		}
	}

	return replayJobStateFromData(ctx, jobID, transitions, durableTasks)
}

func replayJobStateFromData(ctx context.Context, jobID string, transitions []TransitionRecord, durableTasks []DurableTaskRecord) (ReplayedJobState, error) {
	state := ReplayedJobState{
		Job: DurableJobRecord{
			JobID: jobID,
		},
		Tasks:       make(map[string]DurableTaskRecord),
		Transitions: transitions,
	}
	for _, task := range durableTasks {
		// Seed static execution payload fields from durable task snapshots,
		// but keep lifecycle fields empty so transition history remains the
		// sole source of ordering/state truth during replay.
		state.Tasks[task.TaskID] = DurableTaskRecord{
			TaskID:       task.TaskID,
			JobID:        task.JobID,
			StageID:      task.StageID,
			NodeID:       task.NodeID,
			AgentID:      task.AgentID,
			AgentName:    task.AgentName,
			InputJSON:    task.InputJSON,
			PartitionKey: task.PartitionKey,
		}
	}

	for _, record := range transitions {
		if err := ctx.Err(); err != nil {
			return ReplayedJobState{}, err
		}
		if err := record.Validate(); err != nil {
			return ReplayedJobState{}, fmt.Errorf("invalid transition during replay for job %s: %w", jobID, err)
		}
		if record.JobID != jobID {
			return ReplayedJobState{}, fmt.Errorf("transition references wrong job during replay: expected %s, got %s", jobID, record.JobID)
		}

		switch record.EntityType {
		case TransitionEntityJob:
			newState := JobLifecycleState(record.NewState)
			if state.Job.CurrentState != "" && !CanTransitionJobState(state.Job.CurrentState, newState) {
				return ReplayedJobState{}, fmt.Errorf("illegal job transition during replay for job %s: %s -> %s", jobID, state.Job.CurrentState, newState)
			}
			state.Job.CurrentState = newState
			if state.Job.CreatedAt.IsZero() {
				state.Job.CreatedAt = record.OccurredAt
			}
			state.Job.UpdatedAt = record.OccurredAt
		case TransitionEntityTask:
			current := state.Tasks[record.TaskID]
			newState := TaskLifecycleState(record.NewState)
			if current.CurrentState != "" && !CanTransitionTaskState(current.CurrentState, newState) {
				return ReplayedJobState{}, fmt.Errorf("illegal task transition during replay for task %s: %s -> %s", record.TaskID, current.CurrentState, newState)
			}

			current.TaskID = record.TaskID
			current.JobID = record.JobID
			current.NodeID = record.NodeID
			current.CurrentState = newState
			if record.Attempt > current.LastAttempt {
				current.LastAttempt = record.Attempt
			}
			current.UpdatedAt = record.OccurredAt
			state.Tasks[record.TaskID] = current
		default:
			return ReplayedJobState{}, fmt.Errorf("unknown transition entity type %q", record.EntityType)
		}
	}

	return state, nil
}
