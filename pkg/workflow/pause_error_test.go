package workflow

import (
	"fmt"
	"testing"
)

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
			got := IsHumanPauseError(tc.err)
			if got != tc.want {
				t.Fatalf("IsHumanPauseError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

type errString string

func (e errString) Error() string { return string(e) }
