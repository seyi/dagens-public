package scheduler

import (
	"context"
	"fmt"
	"time"
)

// JobLifecycleState is the durable control-plane state for a job.
// This is separate from the existing runtime scheduler JobStatus so the
// durability model can evolve without forcing an immediate execution-path refactor.
type JobLifecycleState string

const (
	JobStateSubmitted     JobLifecycleState = "SUBMITTED"
	JobStateQueued        JobLifecycleState = "QUEUED"
	JobStateRunning       JobLifecycleState = "RUNNING"
	JobStateAwaitingHuman JobLifecycleState = "AWAITING_HUMAN"
	JobStateSucceeded     JobLifecycleState = "SUCCEEDED"
	JobStateFailed        JobLifecycleState = "FAILED"
	JobStateCanceled      JobLifecycleState = "CANCELED"
)

func (s JobLifecycleState) String() string {
	return string(s)
}

// TaskLifecycleState is the durable control-plane state for a task.
type TaskLifecycleState string

const (
	TaskStatePending    TaskLifecycleState = "PENDING"
	TaskStateDispatched TaskLifecycleState = "DISPATCHED"
	TaskStateRunning    TaskLifecycleState = "RUNNING"
	TaskStateSucceeded  TaskLifecycleState = "SUCCEEDED"
	TaskStateFailed     TaskLifecycleState = "FAILED"
)

func (s TaskLifecycleState) String() string {
	return string(s)
}

// TransitionEntityType identifies whether a transition applies to a job or task.
type TransitionEntityType string

const (
	TransitionEntityJob  TransitionEntityType = "JOB"
	TransitionEntityTask TransitionEntityType = "TASK"
)

func (t TransitionEntityType) String() string {
	return string(t)
}

// TransitionType describes the durable lifecycle event being appended.
type TransitionType string

const (
	TransitionJobSubmitted     TransitionType = "JOB_SUBMITTED"
	TransitionJobQueued        TransitionType = "JOB_QUEUED"
	TransitionJobRunning       TransitionType = "JOB_RUNNING"
	TransitionJobAwaitingHuman TransitionType = "JOB_AWAITING_HUMAN"
	TransitionJobResumed       TransitionType = "JOB_RESUMED"
	TransitionJobSucceeded     TransitionType = "JOB_SUCCEEDED"
	TransitionJobFailed        TransitionType = "JOB_FAILED"
	TransitionJobCanceled      TransitionType = "JOB_CANCELED"
	TransitionTaskCreated      TransitionType = "TASK_CREATED"
	TransitionTaskDispatched   TransitionType = "TASK_DISPATCHED"
	TransitionTaskRunning      TransitionType = "TASK_RUNNING"
	TransitionTaskSucceeded    TransitionType = "TASK_SUCCEEDED"
	TransitionTaskFailed       TransitionType = "TASK_FAILED"
)

func (t TransitionType) String() string {
	return string(t)
}

// DurableJobRecord is the materialized current-state view used for fast lookup.
// The transition log remains authoritative; this record is a rebuildable index.
type DurableJobRecord struct {
	JobID          string
	Name           string
	CurrentState   JobLifecycleState
	LastSequenceID uint64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// DurableTaskRecord is the materialized current-state view for a task.
type DurableTaskRecord struct {
	TaskID       string
	JobID        string
	StageID      string
	NodeID       string
	AgentID      string
	AgentName    string
	InputJSON    string
	PartitionKey string
	CurrentState TaskLifecycleState
	LastAttempt  int
	UpdatedAt    time.Time
}

// TransitionRecord is the append-only durable event used to reconstruct state.
//
// SequenceID is the replay ordering source of truth and must be monotonic
// within the chosen replay scope. For v0.2, per-job monotonic ordering is the
// recommended default because replay correctness does not require a global
// cross-job order.
//
// PreviousState must be present for non-initial transitions. Initial
// transitions may leave PreviousState empty:
//   - JOB transitions with NewState=SUBMITTED
//   - TASK transitions with NewState=PENDING
type TransitionRecord struct {
	SequenceID    uint64
	EntityType    TransitionEntityType
	Transition    TransitionType
	JobID         string
	TaskID        string
	PreviousState string
	NewState      string
	NodeID        string
	Attempt       int
	ErrorSummary  string
	OccurredAt    time.Time
}

// TransitionStore is the first durability interface for append-only lifecycle
// transitions plus rebuildable current-state indexes.
//
// Implementations must treat the transition log as authoritative. If a durable
// materialized current-state view is also persisted, the transition append and
// corresponding Upsert* operation must be atomic (same transaction or write
// batch) so replay and fast-path lookups cannot diverge.
type TransitionStore interface {
	AppendTransition(ctx context.Context, record TransitionRecord) error
	UpsertJob(ctx context.Context, job DurableJobRecord) error
	UpsertTask(ctx context.Context, task DurableTaskRecord) error
	ListUnfinishedJobs(ctx context.Context) ([]DurableJobRecord, error)
	// ListTransitionsByJob returns transitions for the given job ordered by
	// SequenceID ascending.
	ListTransitionsByJob(ctx context.Context, jobID string) ([]TransitionRecord, error)
}

// TransitionStoreTx is the write surface used inside an atomic transition-store
// transaction.
type TransitionStoreTx interface {
	AppendTransition(ctx context.Context, record TransitionRecord) error
	UpsertJob(ctx context.Context, job DurableJobRecord) error
	UpsertTask(ctx context.Context, task DurableTaskRecord) error
}

// TaskDispatchClaim is the atomic claim payload used to guard dispatch
// ownership transitions against stale or duplicate dispatch attempts.
type TaskDispatchClaim struct {
	TaskID    string
	JobID     string
	StageID   string
	NodeID    string
	Attempt   int
	UpdatedAt time.Time
}

// DispatchClaimTransitionStoreTx is an optional TransitionStoreTx extension for
// state-guarded dispatch claims (CAS semantics).
type DispatchClaimTransitionStoreTx interface {
	TransitionStoreTx
	// ClaimTaskDispatch attempts to claim dispatch ownership for the provided
	// task attempt. It returns true only when ownership was newly claimed.
	ClaimTaskDispatch(ctx context.Context, claim TaskDispatchClaim) (bool, error)
}

// AtomicTransitionStore extends TransitionStore with transactional writes for
// append+upsert atomicity.
type AtomicTransitionStore interface {
	TransitionStore
	WithTx(ctx context.Context, fn func(tx TransitionStoreTx) error) error
}

// SequenceIDStore optionally provides durable sequence allocation for transition
// writes. Implementations should guarantee monotonic sequence IDs per job.
// Scheduler writers call NextSequenceID when available; otherwise they fall
// back to in-memory per-job counters.
type SequenceIDStore interface {
	NextSequenceID(ctx context.Context, jobID string) (uint64, error)
}

// DurableTaskLookupStore is an optional replay extension for reading the
// materialized durable task view by job.
type DurableTaskLookupStore interface {
	ListTasksByJob(ctx context.Context, jobID string) ([]DurableTaskRecord, error)
}

// ReplayBatchLookupStore is an optional replay optimization surface for
// loading transitions and durable task snapshots for multiple jobs in bulk.
//
// Implementations must return records grouped by job ID with the same ordering
// guarantees as the single-job methods:
//   - transitions ordered by sequence_id ascending
//   - tasks ordered by task_id ascending
//   - every requested job ID present in both returned maps, even when the
//     corresponding slice is empty
type ReplayBatchLookupStore interface {
	ListTransitionsByJobs(ctx context.Context, jobIDs []string) (map[string][]TransitionRecord, error)
	ListTasksByJobs(ctx context.Context, jobIDs []string) (map[string][]DurableTaskRecord, error)
}

var validJobTransitions = map[JobLifecycleState]map[JobLifecycleState]struct{}{
	JobStateSubmitted: {
		JobStateQueued: {},
	},
	JobStateQueued: {
		JobStateRunning:  {},
		JobStateFailed:   {},
		JobStateCanceled: {},
	},
	JobStateRunning: {
		JobStateAwaitingHuman: {},
		JobStateSucceeded:     {},
		JobStateFailed:        {},
		JobStateCanceled:      {},
	},
	JobStateAwaitingHuman: {
		JobStateRunning:  {},
		JobStateFailed:   {},
		JobStateCanceled: {},
	},
	JobStateSucceeded: {},
	JobStateFailed:    {},
	JobStateCanceled:  {},
}

var validTaskTransitions = map[TaskLifecycleState]map[TaskLifecycleState]struct{}{
	TaskStatePending: {
		TaskStateDispatched: {},
		TaskStateFailed:     {},
	},
	TaskStateDispatched: {
		TaskStateRunning: {},
		TaskStateFailed:  {},
	},
	TaskStateRunning: {
		TaskStateSucceeded: {},
		TaskStateFailed:    {},
	},
	TaskStateSucceeded: {},
	TaskStateFailed:    {},
}

// CanTransitionJobState returns true when the transition is legal for the
// durable job lifecycle model. Re-applying the same state is treated as a no-op.
func CanTransitionJobState(from, to JobLifecycleState) bool {
	if from == to {
		return true
	}
	next, ok := validJobTransitions[from]
	if !ok {
		return false
	}
	_, ok = next[to]
	return ok
}

// CanTransitionTaskState returns true when the transition is legal for the
// durable task lifecycle model. Re-applying the same state is treated as a no-op.
func CanTransitionTaskState(from, to TaskLifecycleState) bool {
	if from == to {
		return true
	}
	next, ok := validTaskTransitions[from]
	if !ok {
		return false
	}
	_, ok = next[to]
	return ok
}

// IsTerminalJobState returns true when the job state is terminal.
func IsTerminalJobState(state JobLifecycleState) bool {
	switch state {
	case JobStateSucceeded, JobStateFailed, JobStateCanceled:
		return true
	default:
		return false
	}
}

// IsTerminalTaskState returns true when the task state is terminal.
func IsTerminalTaskState(state TaskLifecycleState) bool {
	switch state {
	case TaskStateSucceeded, TaskStateFailed:
		return true
	default:
		return false
	}
}

// Validate checks that the transition record is structurally consistent before
// it is appended to durable storage.
//
// Lifecycle legality (for example SUBMITTED->QUEUED) is intentionally enforced
// by scheduler transition writers where full in-memory context is available.
func (r TransitionRecord) Validate() error {
	if r.SequenceID == 0 {
		return fmt.Errorf("sequence_id must be greater than zero")
	}
	if r.JobID == "" {
		return fmt.Errorf("job_id must be set")
	}
	if r.EntityType != TransitionEntityJob && r.EntityType != TransitionEntityTask {
		return fmt.Errorf("invalid entity_type %q", r.EntityType)
	}
	if r.Transition == "" {
		return fmt.Errorf("transition must be set")
	}
	if r.NewState == "" {
		return fmt.Errorf("new_state must be set")
	}
	if r.OccurredAt.IsZero() {
		return fmt.Errorf("occurred_at must be set")
	}

	switch r.EntityType {
	case TransitionEntityJob:
		if r.TaskID != "" {
			return fmt.Errorf("task_id must be empty for job transitions")
		}
		if !isValidJobLifecycleState(r.NewState) {
			return fmt.Errorf("invalid job new_state %q", r.NewState)
		}
		if r.PreviousState != "" && !isValidJobLifecycleState(r.PreviousState) {
			return fmt.Errorf("invalid job previous_state %q", r.PreviousState)
		}
		if r.PreviousState == "" && JobLifecycleState(r.NewState) != JobStateSubmitted {
			return fmt.Errorf("previous_state required for non-initial job transition to %q", r.NewState)
		}
	case TransitionEntityTask:
		if r.TaskID == "" {
			return fmt.Errorf("task_id must be set for task transitions")
		}
		if !isValidTaskLifecycleState(r.NewState) {
			return fmt.Errorf("invalid task new_state %q", r.NewState)
		}
		if r.PreviousState != "" && !isValidTaskLifecycleState(r.PreviousState) {
			return fmt.Errorf("invalid task previous_state %q", r.PreviousState)
		}
		if r.PreviousState == "" && TaskLifecycleState(r.NewState) != TaskStatePending {
			return fmt.Errorf("previous_state required for non-initial task transition to %q", r.NewState)
		}
	}

	return nil
}

func isValidJobLifecycleState(state string) bool {
	switch JobLifecycleState(state) {
	case JobStateSubmitted, JobStateQueued, JobStateRunning, JobStateAwaitingHuman, JobStateSucceeded, JobStateFailed, JobStateCanceled:
		return true
	default:
		return false
	}
}

func isValidTaskLifecycleState(state string) bool {
	switch TaskLifecycleState(state) {
	case TaskStatePending, TaskStateDispatched, TaskStateRunning, TaskStateSucceeded, TaskStateFailed:
		return true
	default:
		return false
	}
}
