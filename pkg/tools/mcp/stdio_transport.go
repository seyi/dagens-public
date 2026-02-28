// Copyright 2025 Apache Spark AI Agents
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// stdioTransport implements Transport using subprocess stdio communication
type stdioTransport struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	stderr    io.ReadCloser
	params    StdioConnectionParams
	mu        sync.Mutex
	connected bool

	// Response handling
	responses     map[int]chan *JSONRPCResponse
	responseMu    sync.RWMutex
	readerStarted bool
	readerDone    chan struct{}
	stderrLines   []string
	stderrMu      sync.Mutex
}

// NewStdioTransport creates a new stdio transport
func NewStdioTransport(params StdioConnectionParams) (Transport, error) {
	if err := params.Validate(); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	// Set default timeout
	if params.Timeout == 0 {
		params.Timeout = 300
	}

	cmd := exec.Command(params.Command, params.Args...)

	// Set environment variables
	cmd.Env = os.Environ()
	for key, val := range params.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, val))
	}

	// Create pipes
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		stderr.Close()
		return nil, fmt.Errorf("failed to start MCP server: %w", err)
	}

	transport := &stdioTransport{
		cmd:        cmd,
		stdin:      stdin,
		stdout:     stdout,
		stderr:     stderr,
		params:     params,
		connected:  true,
		responses:  make(map[int]chan *JSONRPCResponse),
		readerDone: make(chan struct{}),
		stderrLines: make([]string, 0),
	}

	// Start background goroutines
	transport.startResponseReader()
	transport.startStderrReader()

	return transport, nil
}

// Send sends a JSON-RPC request and waits for the response
func (t *stdioTransport) Send(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	if !t.IsConnected() {
		return nil, fmt.Errorf("transport not connected")
	}

	// Create response channel
	responseChan := make(chan *JSONRPCResponse, 1)
	t.responseMu.Lock()
	t.responses[req.ID] = responseChan
	t.responseMu.Unlock()

	// Cleanup
	defer func() {
		t.responseMu.Lock()
		delete(t.responses, req.ID)
		t.responseMu.Unlock()
	}()

	// Send request
	t.mu.Lock()
	encoder := json.NewEncoder(t.stdin)
	if err := encoder.Encode(req); err != nil {
		t.mu.Unlock()
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	t.mu.Unlock()

	// Wait for response with timeout
	timeout := time.Duration(t.params.Timeout) * time.Second
	select {
	case resp := <-responseChan:
		return resp, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("request cancelled: %w", ctx.Err())
	case <-time.After(timeout):
		return nil, fmt.Errorf("request timeout after %v", timeout)
	case <-t.readerDone:
		return nil, fmt.Errorf("transport closed while waiting for response")
	}
}

// Close closes the transport and terminates the subprocess
func (t *stdioTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.connected {
		return nil
	}

	t.connected = false

	// Close stdin to signal subprocess to exit
	if t.stdin != nil {
		t.stdin.Close()
	}

	// Wait for process to exit (with timeout)
	done := make(chan error, 1)
	go func() {
		done <- t.cmd.Wait()
	}()

	select {
	case err := <-done:
		// Process exited
		if err != nil && err.Error() != "signal: killed" {
			return fmt.Errorf("process exit error: %w", err)
		}
		return nil
	case <-time.After(5 * time.Second):
		// Force kill if not exited
		if err := t.cmd.Process.Kill(); err != nil {
			return fmt.Errorf("failed to kill process: %w", err)
		}
		return nil
	}
}

// IsConnected returns true if the transport is connected
func (t *stdioTransport) IsConnected() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.connected
}

// startResponseReader starts a goroutine to read responses from stdout
func (t *stdioTransport) startResponseReader() {
	if t.readerStarted {
		return
	}
	t.readerStarted = true

	go func() {
		defer close(t.readerDone)
		scanner := bufio.NewScanner(t.stdout)

		// Increase buffer size for large responses
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024) // 1MB max

		for scanner.Scan() {
			line := scanner.Bytes()

			// Parse JSON-RPC response
			var resp JSONRPCResponse
			if err := json.Unmarshal(line, &resp); err != nil {
				// Log parse error but continue
				fmt.Fprintf(os.Stderr, "MCP: failed to parse response: %v\n", err)
				continue
			}

			// Deliver to waiting request
			t.responseMu.RLock()
			if ch, ok := t.responses[resp.ID]; ok {
				select {
				case ch <- &resp:
				default:
					// Channel full, drop response
				}
			}
			t.responseMu.RUnlock()
		}

		if err := scanner.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "MCP: stdout scanner error: %v\n", err)
		}
	}()
}

// startStderrReader starts a goroutine to capture stderr
func (t *stdioTransport) startStderrReader() {
	go func() {
		scanner := bufio.NewScanner(t.stderr)
		for scanner.Scan() {
			line := scanner.Text()
			t.stderrMu.Lock()
			t.stderrLines = append(t.stderrLines, line)
			// Keep only last 100 lines
			if len(t.stderrLines) > 100 {
				t.stderrLines = t.stderrLines[1:]
			}
			t.stderrMu.Unlock()

			// Also print to our stderr for debugging
			fmt.Fprintf(os.Stderr, "MCP stderr: %s\n", line)
		}
	}()
}

// GetStderr returns captured stderr lines
func (t *stdioTransport) GetStderr() []string {
	t.stderrMu.Lock()
	defer t.stderrMu.Unlock()

	lines := make([]string, len(t.stderrLines))
	copy(lines, t.stderrLines)
	return lines
}
