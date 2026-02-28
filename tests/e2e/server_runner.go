// Package e2e provides end-to-end testing utilities for the Dagens agent server.
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// ServerRunner manages the lifecycle of the agent-server binary for E2E tests.
type ServerRunner struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	addr       string
	port       int
	stdout     bytes.Buffer
	stderr     bytes.Buffer
	cancel     context.CancelFunc
	binaryPath string
}

// NewServerRunner creates a new server runner instance.
func NewServerRunner(t *testing.T) *ServerRunner {
	t.Helper()

	// Find or build the binary
	binaryPath := findOrBuildBinary(t)

	return &ServerRunner{
		binaryPath: binaryPath,
	}
}

// findOrBuildBinary locates the agent-server binary, building it if necessary.
func findOrBuildBinary(t *testing.T) string {
	t.Helper()

	// Check if binary exists in common locations
	possiblePaths := []string{
		"./agent-server",
		"../../cmd/agent-server/agent-server",
		"/tmp/agent-server-test",
	}

	for _, p := range possiblePaths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// Build the binary
	t.Log("Building agent-server binary...")
	buildPath := "/tmp/agent-server-test"

	// Find project root
	projectRoot := findProjectRoot(t)

	cmd := exec.Command("go", "build", "-o", buildPath, "./cmd/agent-server")
	cmd.Dir = projectRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to build agent-server: %v", err)
	}

	return buildPath
}

// findProjectRoot walks up directories to find go.mod
func findProjectRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("Could not find project root (no go.mod found)")
		}
		dir = parent
	}
}

// Start starts the server on an available port.
func (r *ServerRunner) Start(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()

	// Find a free port
	port, err := getFreePort()
	if err != nil {
		t.Fatalf("Failed to find free port: %v", err)
	}
	r.port = port
	r.addr = fmt.Sprintf("127.0.0.1:%d", port)

	// Create context with cancel
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel

	// Start the server
	r.cmd = exec.CommandContext(ctx, r.binaryPath, "-addr", r.addr)
	r.cmd.Stdout = &r.stdout
	r.cmd.Stderr = &r.stderr

	if err := r.cmd.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	// Register cleanup
	t.Cleanup(func() {
		r.Stop(t)
	})

	// Wait for server to be ready
	if err := r.waitForReady(t, 10*time.Second); err != nil {
		t.Fatalf("Server failed to become ready: %v\nStdout: %s\nStderr: %s",
			err, r.stdout.String(), r.stderr.String())
	}

	t.Logf("Server started on %s", r.addr)
}

// getFreePort finds an available TCP port.
func getFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// waitForReady polls the health endpoint until the server is ready.
func (r *ServerRunner) waitForReady(t *testing.T, timeout time.Duration) error {
	t.Helper()

	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 1 * time.Second}

	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("http://%s/health", r.addr))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("server did not become ready within %v", timeout)
}

// Stop stops the server gracefully.
func (r *ServerRunner) Stop(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}

	if r.cmd != nil && r.cmd.Process != nil {
		// Wait for process to exit
		done := make(chan error, 1)
		go func() {
			done <- r.cmd.Wait()
		}()

		select {
		case <-done:
			// Process exited
		case <-time.After(5 * time.Second):
			// Force kill
			r.cmd.Process.Kill()
		}
		r.cmd = nil
	}
}

// Addr returns the server address.
func (r *ServerRunner) Addr() string {
	return r.addr
}

// Port returns the server port.
func (r *ServerRunner) Port() int {
	return r.port
}

// BaseURL returns the base URL for the server.
func (r *ServerRunner) BaseURL() string {
	return fmt.Sprintf("http://%s", r.addr)
}

// Logs returns the captured stdout and stderr.
func (r *ServerRunner) Logs() (stdout, stderr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stdout.String(), r.stderr.String()
}
