package workflow

import "strings"

const (
	humanPauseMarker     = "human interaction pending"
	graphExecutionPaused = "execution paused"
)

// IsHumanPauseError returns true when err indicates graph execution paused for HITL.
func IsHumanPauseError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, humanPauseMarker) ||
		strings.Contains(msg, graphExecutionPaused)
}
