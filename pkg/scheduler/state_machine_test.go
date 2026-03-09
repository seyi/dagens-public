package scheduler

import (
	"testing"
	"time"
)

func TestCanTransitionJobState(t *testing.T) {
	tests := []struct {
		name string
		from JobLifecycleState
		to   JobLifecycleState
		want bool
	}{
		{name: "submitted to queued", from: JobStateSubmitted, to: JobStateQueued, want: true},
		{name: "queued to running", from: JobStateQueued, to: JobStateRunning, want: true},
		{name: "running to awaiting human", from: JobStateRunning, to: JobStateAwaitingHuman, want: true},
		{name: "awaiting human to running", from: JobStateAwaitingHuman, to: JobStateRunning, want: true},
		{name: "running to succeeded", from: JobStateRunning, to: JobStateSucceeded, want: true},
		{name: "same state is no-op", from: JobStateCanceled, to: JobStateCanceled, want: true},
		{name: "submitted to succeeded invalid", from: JobStateSubmitted, to: JobStateSucceeded, want: false},
		{name: "terminal to running invalid", from: JobStateFailed, to: JobStateRunning, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CanTransitionJobState(tt.from, tt.to)
			if got != tt.want {
				t.Fatalf("CanTransitionJobState(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestCanTransitionTaskState(t *testing.T) {
	tests := []struct {
		name string
		from TaskLifecycleState
		to   TaskLifecycleState
		want bool
	}{
		{name: "pending to dispatched", from: TaskStatePending, to: TaskStateDispatched, want: true},
		{name: "dispatched to running", from: TaskStateDispatched, to: TaskStateRunning, want: true},
		{name: "running to failed", from: TaskStateRunning, to: TaskStateFailed, want: true},
		{name: "same state is no-op", from: TaskStateSucceeded, to: TaskStateSucceeded, want: true},
		{name: "pending to succeeded invalid", from: TaskStatePending, to: TaskStateSucceeded, want: false},
		{name: "terminal to running invalid", from: TaskStateFailed, to: TaskStateRunning, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CanTransitionTaskState(tt.from, tt.to)
			if got != tt.want {
				t.Fatalf("CanTransitionTaskState(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestTerminalStateHelpers(t *testing.T) {
	if !IsTerminalJobState(JobStateSucceeded) {
		t.Fatal("expected succeeded job state to be terminal")
	}
	if !IsTerminalJobState(JobStateCanceled) {
		t.Fatal("expected canceled job state to be terminal")
	}
	if IsTerminalJobState(JobStateRunning) {
		t.Fatal("expected running job state to be non-terminal")
	}
	if IsTerminalJobState(JobStateAwaitingHuman) {
		t.Fatal("expected awaiting-human job state to be non-terminal")
	}
	if !IsTerminalTaskState(TaskStateFailed) {
		t.Fatal("expected failed task state to be terminal")
	}
	if IsTerminalTaskState(TaskStateDispatched) {
		t.Fatal("expected dispatched task state to be non-terminal")
	}
}

func TestTransitionRecordValidate(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name    string
		record  TransitionRecord
		wantErr bool
	}{
		{
			name: "valid job transition",
			record: TransitionRecord{
				SequenceID:    1,
				EntityType:    TransitionEntityJob,
				Transition:    TransitionJobQueued,
				JobID:         "job-1",
				PreviousState: string(JobStateSubmitted),
				NewState:      string(JobStateQueued),
				OccurredAt:    now,
			},
		},
		{
			name: "valid task transition",
			record: TransitionRecord{
				SequenceID:    2,
				EntityType:    TransitionEntityTask,
				Transition:    TransitionTaskRunning,
				JobID:         "job-1",
				TaskID:        "task-1",
				PreviousState: string(TaskStateDispatched),
				NewState:      string(TaskStateRunning),
				OccurredAt:    now,
			},
		},
		{
			name: "valid resumed transition",
			record: TransitionRecord{
				SequenceID:    7,
				EntityType:    TransitionEntityJob,
				Transition:    TransitionJobResumed,
				JobID:         "job-resume",
				PreviousState: string(JobStateAwaitingHuman),
				NewState:      string(JobStateRunning),
				OccurredAt:    now,
			},
		},
		{
			name: "task transition with optional fields populated",
			record: TransitionRecord{
				SequenceID:    8,
				EntityType:    TransitionEntityTask,
				Transition:    TransitionTaskFailed,
				JobID:         "job-1",
				TaskID:        "task-1",
				PreviousState: string(TaskStateRunning),
				NewState:      string(TaskStateFailed),
				NodeID:        "node-42",
				Attempt:       3,
				ErrorSummary:  "timeout after 30s",
				OccurredAt:    now,
			},
		},
		{
			name: "valid initial job transition without previous state",
			record: TransitionRecord{
				SequenceID: 3,
				EntityType: TransitionEntityJob,
				Transition: TransitionJobSubmitted,
				JobID:      "job-init",
				NewState:   string(JobStateSubmitted),
				OccurredAt: now,
			},
		},
		{
			name: "valid initial task transition without previous state",
			record: TransitionRecord{
				SequenceID: 4,
				EntityType: TransitionEntityTask,
				Transition: TransitionTaskCreated,
				JobID:      "job-init",
				TaskID:     "task-init",
				NewState:   string(TaskStatePending),
				OccurredAt: now,
			},
		},
		{
			name: "missing sequence id",
			record: TransitionRecord{
				EntityType: TransitionEntityJob,
				Transition: TransitionJobSubmitted,
				JobID:      "job-1",
				NewState:   string(JobStateSubmitted),
				OccurredAt: now,
			},
			wantErr: true,
		},
		{
			name: "missing occurred_at",
			record: TransitionRecord{
				SequenceID: 9,
				EntityType: TransitionEntityJob,
				Transition: TransitionJobSubmitted,
				JobID:      "job-1",
				NewState:   string(JobStateSubmitted),
			},
			wantErr: true,
		},
		{
			name: "job transition with task id invalid",
			record: TransitionRecord{
				SequenceID: 1,
				EntityType: TransitionEntityJob,
				Transition: TransitionJobRunning,
				JobID:      "job-1",
				TaskID:     "task-1",
				NewState:   string(JobStateRunning),
				OccurredAt: now,
			},
			wantErr: true,
		},
		{
			name: "task transition missing task id invalid",
			record: TransitionRecord{
				SequenceID: 1,
				EntityType: TransitionEntityTask,
				Transition: TransitionTaskCreated,
				JobID:      "job-1",
				NewState:   string(TaskStatePending),
				OccurredAt: now,
			},
			wantErr: true,
		},
		{
			name: "invalid job state string",
			record: TransitionRecord{
				SequenceID: 1,
				EntityType: TransitionEntityJob,
				Transition: TransitionJobRunning,
				JobID:      "job-1",
				NewState:   "BROKEN",
				OccurredAt: now,
			},
			wantErr: true,
		},
		{
			name: "non-initial job transition missing previous state invalid",
			record: TransitionRecord{
				SequenceID: 5,
				EntityType: TransitionEntityJob,
				Transition: TransitionJobQueued,
				JobID:      "job-2",
				NewState:   string(JobStateQueued),
				OccurredAt: now,
			},
			wantErr: true,
		},
		{
			name: "non-initial task transition missing previous state invalid",
			record: TransitionRecord{
				SequenceID: 6,
				EntityType: TransitionEntityTask,
				Transition: TransitionTaskRunning,
				JobID:      "job-2",
				TaskID:     "task-2",
				NewState:   string(TaskStateRunning),
				OccurredAt: now,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.record.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
