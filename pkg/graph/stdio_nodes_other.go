//go:build !linux

package graph

import (
	"context"
	"time"
)

// readFromStdinNonBlockingLinux is a stub for non-Linux platforms
// On non-Linux platforms, we fall back to the standard implementation
// which may have goroutine leak issues on timeout
func readFromStdinNonBlockingLinux(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	// This should not be called on non-Linux platforms
	// The caller should check useNonBlockingStdin() first
	panic("readFromStdinNonBlockingLinux called on non-Linux platform")
}

// useNonBlockingStdin returns false on non-Linux platforms
func useNonBlockingStdin() bool {
	return false
}
