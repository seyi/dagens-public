package scheduler

import (
	"fmt"
	"testing"

	"github.com/seyi/dagens/pkg/workflow"
)

func TestRuntimeJobStatusFromLifecycle_AwaitingHuman(t *testing.T) {
	got := runtimeJobStatusFromLifecycle(JobStateAwaitingHuman)
	if got != JobAwaitingHuman {
		t.Fatalf("runtimeJobStatusFromLifecycle(%q) = %q, want %q", JobStateAwaitingHuman, got, JobAwaitingHuman)
	}
}

func TestRuntimeJobStatusFromLifecycle_UnknownStateDefaultsPending(t *testing.T) {
	got := runtimeJobStatusFromLifecycle(JobLifecycleState("UNKNOWN_STATE"))
	if got != JobPending {
		t.Fatalf("runtimeJobStatusFromLifecycle(UNKNOWN_STATE) = %q, want %q", got, JobPending)
	}
}

func TestRuntimeTaskStatusFromLifecycle_Mappings(t *testing.T) {
	cases := []struct {
		name      string
		lifecycle TaskLifecycleState
		want      JobStatus
	}{
		{name: "pending", lifecycle: TaskStatePending, want: JobPending},
		{name: "dispatched", lifecycle: TaskStateDispatched, want: JobPending},
		{name: "running", lifecycle: TaskStateRunning, want: JobRunning},
		{name: "succeeded", lifecycle: TaskStateSucceeded, want: JobCompleted},
		{name: "failed", lifecycle: TaskStateFailed, want: JobFailed},
		{name: "unknown defaults pending", lifecycle: TaskLifecycleState("UNKNOWN"), want: JobPending},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runtimeTaskStatusFromLifecycle(tc.lifecycle)
			if got != tc.want {
				t.Fatalf("runtimeTaskStatusFromLifecycle(%q) = %q, want %q", tc.lifecycle, got, tc.want)
			}
		})
	}
}

func TestIsHumanPauseError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "hitl marker", err: errString("human interaction pending: checkpoint required"), want: true},
		{name: "paused marker", err: errString("graph execution paused"), want: true},
		{name: "wrapped hitl marker", err: fmt.Errorf("replay failed: %w", errString("human interaction pending: checkpoint required")), want: true},
		{name: "other", err: errString("boom"), want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := workflow.IsHumanPauseError(tc.err)
			if got != tc.want {
				t.Fatalf("IsHumanPauseError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestDeriveStageStatus_Aggregation(t *testing.T) {
	cases := []struct {
		name  string
		tasks []*Task
		want  JobStatus
	}{
		{
			name: "any failed returns failed",
			tasks: []*Task{
				{Status: JobCompleted},
				{Status: JobFailed},
				{Status: JobPending},
			},
			want: JobFailed,
		},
		{
			name: "running with no failed returns running",
			tasks: []*Task{
				{Status: JobCompleted},
				{Status: JobRunning},
			},
			want: JobRunning,
		},
		{
			name: "all completed returns completed",
			tasks: []*Task{
				{Status: JobCompleted},
				{Status: JobCompleted},
			},
			want: JobCompleted,
		},
		{
			name:  "empty returns pending",
			tasks: []*Task{},
			want:  JobPending,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveStageStatus(tc.tasks)
			if got != tc.want {
				t.Fatalf("deriveStageStatus(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

type errString string

func (e errString) Error() string { return string(e) }
