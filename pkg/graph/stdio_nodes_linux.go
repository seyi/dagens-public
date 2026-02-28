//go:build linux

package graph

import (
	"context"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// readFromStdinNonBlockingLinux reads from stdin with proper timeout support on Linux
// using unix.Poll to avoid goroutine leaks. This is the Linux-specific implementation
// that truly eliminates goroutine leaks by using non-blocking I/O.
func readFromStdinNonBlockingLinux(ctx context.Context, prompt string, timeout time.Duration) (string, error) {
	// Display prompt if provided
	if prompt != "" {
		fmt.Print(prompt)
	}

	fd := int(os.Stdin.Fd())

	// Get current flags to restore later
	oldFlags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		return "", fmt.Errorf("failed to get stdin flags: %w", err)
	}

	// Set stdin to non-blocking mode
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_SETFL, oldFlags|unix.O_NONBLOCK); err != nil {
		return "", fmt.Errorf("failed to set stdin non-blocking: %w", err)
	}

	// Ensure we restore the original blocking mode
	defer func() {
		_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETFL, oldFlags)
	}()

	// Create a ticker for periodic context checks during long polls
	pollInterval := 100 * time.Millisecond
	if timeout > 0 && timeout < pollInterval {
		pollInterval = timeout
	}
	pollIntervalMs := int(pollInterval / time.Millisecond)
	if pollIntervalMs < 1 {
		pollIntervalMs = 1
	}

	startTime := time.Now()

	// Poll loop with periodic context checks
	for {
		// Check context before polling
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		// Check if we've exceeded timeout
		if timeout > 0 && time.Since(startTime) >= timeout {
			return "", fmt.Errorf("timeout waiting for stdin input")
		}

		// Calculate remaining time for this poll iteration
		remainingMs := pollIntervalMs
		if timeout > 0 {
			remaining := timeout - time.Since(startTime)
			if remaining < pollInterval {
				remainingMs = int(remaining / time.Millisecond)
				if remainingMs < 1 {
					remainingMs = 1
				}
			}
		}

		// Prepare pollfd structure
		pollFds := []unix.PollFd{
			{
				Fd:     int32(fd),
				Events: unix.POLLIN,
			},
		}

		// Poll for input
		n, err := unix.Poll(pollFds, remainingMs)
		if err != nil {
			if err == unix.EINTR {
				// Interrupted by signal, continue polling
				continue
			}
			return "", fmt.Errorf("poll failed: %w", err)
		}

		if n == 0 {
			// Timeout on this poll iteration, continue to check context and overall timeout
			continue
		}

		// Check for error events
		if pollFds[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return "", fmt.Errorf("stdin poll error event occurred")
		}

		if pollFds[0].Revents&unix.POLLIN != 0 {
			// Data is available, read it
			break
		}
	}

	// Read available data
	buf := make([]byte, 4096)
	result := make([]byte, 0, 256)

	for {
		// Check context before reading
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		n, err := unix.Read(fd, buf)
		if err != nil {
			// Handle EAGAIN/EWOULDBLOCK which indicate no more data available
			if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
				break
			}
			return "", fmt.Errorf("failed to read from stdin: %w", err)
		}

		if n == 0 {
			// EOF reached
			break
		}

		result = append(result, buf[:n]...)

		// Check for newline - stop reading at end of line
		if len(result) > 0 && result[len(result)-1] == '\n' {
			break
		}

		// If we read less than buffer size, likely no more data available
		if n < len(buf) {
			break
		}

		// Safety check to prevent excessive memory usage
		if len(result) > 1024*1024 { // 1MB limit
			return "", fmt.Errorf("exceeded maximum read limit")
		}
	}

	// Convert bytes to string and trim trailing newline/carriage return
	str := string(result)
	for len(str) > 0 && (str[len(str)-1] == '\n' || str[len(str)-1] == '\r') {
		str = str[:len(str)-1]
	}

	return str, nil
}

// useNonBlockingStdin returns true on Linux where we have proper non-blocking support
func useNonBlockingStdin() bool {
	return true
}
