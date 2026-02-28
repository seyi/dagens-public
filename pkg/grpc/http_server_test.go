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

package grpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/runtime"
)

// Mock agent for testing
type mockAgent struct {
	id       string
	response string
	err      error
}

func (m *mockAgent) ID() string {
	return m.id
}

func (m *mockAgent) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &agent.AgentOutput{
		Result: m.response,
		Metadata: map[string]interface{}{
			"test": "data",
		},
	}, nil
}

// Implement full Agent interface for mock
func (m *mockAgent) Name() string              { return m.id }
func (m *mockAgent) Description() string       { return "mock agent" }
func (m *mockAgent) Capabilities() []string    { return []string{} }
func (m *mockAgent) Dependencies() []agent.Agent { return []agent.Agent{} }
func (m *mockAgent) Partition() string         { return "" }

func TestHandleExecute(t *testing.T) {
	// Setup
	server := NewAgentHTTPServer(nil)
	mockAg := &mockAgent{
		id:       "test-agent",
		response: "test response",
	}
	server.RegisterAgent("test-agent", mockAg)

	tests := []struct {
		name           string
		method         string
		body           interface{}
		wantStatus     int
		wantSuccess    bool
		wantOutputText string
	}{
		{
			name:   "successful execution",
			method: http.MethodPost,
			body: ExecuteRequest{
				AgentID: "test-agent",
				Input:   "test input",
				Context: map[string]string{"key": "value"},
			},
			wantStatus:     http.StatusOK,
			wantSuccess:    true,
			wantOutputText: "test response",
		},
		{
			name:   "agent not found",
			method: http.MethodPost,
			body: ExecuteRequest{
				AgentID: "nonexistent",
				Input:   "test input",
			},
			wantStatus:  http.StatusNotFound,
			wantSuccess: false,
		},
		{
			name:       "method not allowed",
			method:     http.MethodGet,
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "invalid request body",
			method:     http.MethodPost,
			body:       "invalid json",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body []byte
			var err error

			if tt.body != nil {
				if str, ok := tt.body.(string); ok {
					body = []byte(str)
				} else {
					body, err = json.Marshal(tt.body)
					if err != nil {
						t.Fatalf("Failed to marshal body: %v", err)
					}
				}
			}

			req := httptest.NewRequest(tt.method, "/api/v1/agents/execute", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.HandleExecute(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Status = %d, want %d", w.Code, tt.wantStatus)
			}

			// Check response body for successful requests
			if tt.wantStatus == http.StatusOK {
				var resp ExecuteResponse
				if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
					t.Fatalf("Failed to decode response: %v", err)
				}

				if resp.Success != tt.wantSuccess {
					t.Errorf("Success = %v, want %v", resp.Success, tt.wantSuccess)
				}

				if tt.wantOutputText != "" && resp.Output != tt.wantOutputText {
					t.Errorf("Output = %q, want %q", resp.Output, tt.wantOutputText)
				}

				if resp.DurationMs < 0 {
					t.Errorf("DurationMs should be non-negative, got %d", resp.DurationMs)
				}

				if len(resp.Metadata) == 0 {
					t.Error("Expected metadata to be populated")
				}
			}

			// Check response body for not found
			if tt.wantStatus == http.StatusNotFound {
				var resp ExecuteResponse
				if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
					t.Fatalf("Failed to decode response: %v", err)
				}

				if resp.Success {
					t.Error("Expected Success = false for not found")
				}

				if resp.Error == "" {
					t.Error("Expected error message for not found")
				}
			}
		})
	}
}

func TestHandleBatchExecute(t *testing.T) {
	// Setup
	server := NewAgentHTTPServer(nil)
	mockAg := &mockAgent{
		id:       "test-agent",
		response: "batch response",
	}
	server.RegisterAgent("test-agent", mockAg)

	tests := []struct {
		name        string
		method      string
		body        interface{}
		wantStatus  int
		wantResults int
	}{
		{
			name:   "successful batch execution",
			method: http.MethodPost,
			body: BatchExecuteRequest{
				AgentID: "test-agent",
				Inputs:  []string{"input1", "input2", "input3"},
				Context: map[string]string{"key": "value"},
			},
			wantStatus:  http.StatusOK,
			wantResults: 3,
		},
		{
			name:   "empty batch",
			method: http.MethodPost,
			body: BatchExecuteRequest{
				AgentID: "test-agent",
				Inputs:  []string{},
			},
			wantStatus:  http.StatusOK,
			wantResults: 0,
		},
		{
			name:   "agent not found",
			method: http.MethodPost,
			body: BatchExecuteRequest{
				AgentID: "nonexistent",
				Inputs:  []string{"input1"},
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "method not allowed",
			method:     http.MethodGet,
			wantStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body []byte
			var err error

			if tt.body != nil {
				body, err = json.Marshal(tt.body)
				if err != nil {
					t.Fatalf("Failed to marshal body: %v", err)
				}
			}

			req := httptest.NewRequest(tt.method, "/api/v1/agents/batch_execute", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.HandleBatchExecute(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Status = %d, want %d", w.Code, tt.wantStatus)
			}

			if tt.wantStatus == http.StatusOK {
				var resp BatchExecuteResponse
				if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
					t.Fatalf("Failed to decode response: %v", err)
				}

				if len(resp.Results) != tt.wantResults {
					t.Errorf("Got %d results, want %d", len(resp.Results), tt.wantResults)
				}

				// Check all results
				for i, result := range resp.Results {
					if !result.Success {
						t.Errorf("Result[%d] failed: %s", i, result.Error)
					}
					if result.Output == "" {
						t.Errorf("Result[%d] has empty output", i)
					}
					if result.DurationMs < 0 {
						t.Errorf("Result[%d] has invalid duration: %d", i, result.DurationMs)
					}
				}
			}
		})
	}
}

func TestHandleHealthCheck(t *testing.T) {
	server := NewAgentHTTPServer(nil)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	server.HandleHealthCheck(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusOK)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}

	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["status"] != "healthy" {
		t.Errorf("Status = %q, want healthy", resp["status"])
	}
}

func TestHandleListAgents(t *testing.T) {
	server := NewAgentHTTPServer(nil)

	// Register some agents
	server.RegisterAgent("agent1", &mockAgent{id: "agent1"})
	server.RegisterAgent("agent2", &mockAgent{id: "agent2"})

	tests := []struct {
		name       string
		method     string
		wantStatus int
		wantAgents int
	}{
		{
			name:       "successful list",
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			wantAgents: 2,
		},
		{
			name:       "method not allowed",
			method:     http.MethodPost,
			wantStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/api/v1/agents", nil)
			w := httptest.NewRecorder()

			server.HandleListAgents(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Status = %d, want %d", w.Code, tt.wantStatus)
			}

			if tt.wantStatus == http.StatusOK {
				var resp ListAgentsResponse
				if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
					t.Fatalf("Failed to decode response: %v", err)
				}

				if len(resp.Agents) != tt.wantAgents {
					t.Errorf("Got %d agents, want %d", len(resp.Agents), tt.wantAgents)
				}

				// Check agent info
				for _, agentInfo := range resp.Agents {
					if agentInfo.ID == "" {
						t.Error("Agent ID is empty")
					}
					if agentInfo.Name == "" {
						t.Error("Agent Name is empty")
					}
				}
			}
		})
	}
}

func TestRegisterAndGetAgent(t *testing.T) {
	server := NewAgentHTTPServer(nil)

	mockAg := &mockAgent{id: "test"}
	server.RegisterAgent("test", mockAg)

	// Test successful get
	ag, ok := server.GetAgent("test")
	if !ok {
		t.Error("Expected agent to be found")
	}
	if ag != mockAg {
		t.Error("Got different agent instance")
	}

	// Test not found
	_, ok = server.GetAgent("nonexistent")
	if ok {
		t.Error("Expected agent not to be found")
	}
}

func TestSetupRoutes(t *testing.T) {
	server := NewAgentHTTPServer(nil)

	mux := http.NewServeMux()
	server.SetupRoutes(mux)

	// Test that routes are registered
	routes := []string{
		"/api/v1/agents/execute",
		"/api/v1/agents/batch_execute",
		"/api/v1/agents",
		"/health",
	}

	for _, route := range routes {
		req := httptest.NewRequest(http.MethodPost, route, nil)
		w := httptest.NewRecorder()

		mux.ServeHTTP(w, req)

		// Should not get 404 for registered routes
		// (may get 405 Method Not Allowed for GET on POST-only routes, which is fine)
		if w.Code == http.StatusNotFound {
			t.Errorf("Route %s not registered", route)
		}
	}
}

func TestConcurrentExecute(t *testing.T) {
	server := NewAgentHTTPServer(nil)
	mockAg := &mockAgent{
		id:       "concurrent-agent",
		response: "concurrent response",
	}
	server.RegisterAgent("concurrent-agent", mockAg)

	// Test concurrent requests
	const numRequests = 10
	done := make(chan bool, numRequests)

	for i := 0; i < numRequests; i++ {
		go func(n int) {
			body, _ := json.Marshal(ExecuteRequest{
				AgentID: "concurrent-agent",
				Input:   "concurrent test",
			})

			req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/execute", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.HandleExecute(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("Request %d failed with status %d", n, w.Code)
			}

			done <- true
		}(i)
	}

	// Wait for all requests
	for i := 0; i < numRequests; i++ {
		<-done
	}
}

func TestExecuteWithError(t *testing.T) {
	server := NewAgentHTTPServer(nil)

	// Create agent that returns error
	mockAg := &mockAgent{
		id:  "error-agent",
		err: fmt.Errorf("invalid input"),
	}
	server.RegisterAgent("error-agent", mockAg)

	body, _ := json.Marshal(ExecuteRequest{
		AgentID: "error-agent",
		Input:   "test",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/execute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.HandleExecute(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Status = %d, want %d (errors return 200 with success=false)", w.Code, http.StatusOK)
	}

	var resp ExecuteResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp.Success {
		t.Error("Expected success=false for agent error")
	}

	if resp.Error == "" {
		t.Error("Expected error message")
	}
}

// Mock async agent for streaming tests
type mockAsyncAgent struct {
	id       string
	name     string
	events   []*runtime.AsyncEvent
	delay    time.Duration
}

func (m *mockAsyncAgent) ID() string   { return m.id }
func (m *mockAsyncAgent) Name() string { return m.name }

func (m *mockAsyncAgent) RunAsync(ctx context.Context, invCtx *runtime.InvocationContext) <-chan *runtime.AsyncEvent {
	output := make(chan *runtime.AsyncEvent)

	go func() {
		defer close(output)

		for _, event := range m.events {
			if m.delay > 0 {
				time.Sleep(m.delay)
			}

			select {
			case <-ctx.Done():
				return
			case output <- event:
			}
		}
	}()

	return output
}

func TestHandleStreamExecute(t *testing.T) {
	// Create async runtime
	asyncRuntime := runtime.NewAsyncAgentRuntime(nil)

	// Create HTTP server with async runtime
	server := NewAgentHTTPServer(nil).WithAsyncRuntime(asyncRuntime)

	// Create mock async agent with test events
	mockEvents := []*runtime.AsyncEvent{
		{
			ID:        "event-1",
			Type:      runtime.EventTypeStateChange,
			Content:   "Processing...",
			Partial:   true,
			Timestamp: time.Now(),
		},
		{
			ID:        "event-2",
			Type:      runtime.EventTypeToolCall,
			Content:   "Calling tool: search",
			Partial:   false,
			Timestamp: time.Now(),
		},
		{
			ID:        "event-3",
			Type:      runtime.EventTypeMessage,
			Content:   "Final result",
			Partial:   false,
			Timestamp: time.Now(),
		},
	}

	mockAsync := &mockAsyncAgent{
		id:     "streaming-agent",
		name:   "Streaming Agent",
		events: mockEvents,
	}
	server.RegisterAsyncAgent("streaming-agent", mockAsync)

	tests := []struct {
		name           string
		method         string
		body           interface{}
		wantStatus     int
		wantEventCount int
	}{
		{
			name:   "successful streaming",
			method: http.MethodPost,
			body: StreamExecuteRequest{
				AgentID: "streaming-agent",
				Input:   "stream test",
			},
			wantStatus:     http.StatusOK,
			wantEventCount: 3, // 3 events + done event
		},
		{
			name:   "agent not found",
			method: http.MethodPost,
			body: StreamExecuteRequest{
				AgentID: "nonexistent",
				Input:   "test",
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "method not allowed",
			method:     http.MethodGet,
			wantStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body []byte
			var err error

			if tt.body != nil {
				body, err = json.Marshal(tt.body)
				if err != nil {
					t.Fatalf("Failed to marshal body: %v", err)
				}
			}

			req := httptest.NewRequest(tt.method, "/api/v1/agents/stream", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			server.HandleStreamExecute(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("Status = %d, want %d", w.Code, tt.wantStatus)
			}

			if tt.wantStatus == http.StatusOK {
				// Verify SSE format
				contentType := w.Header().Get("Content-Type")
				if contentType != "text/event-stream" {
					t.Errorf("Content-Type = %q, want text/event-stream", contentType)
				}

				// Parse SSE events
				body := w.Body.String()
				eventCount := 0
				scanner := bufio.NewScanner(strings.NewReader(body))
				for scanner.Scan() {
					line := scanner.Text()
					if strings.HasPrefix(line, "event:") {
						eventCount++
					}
				}

				// Should have at least the expected events (plus done event)
				if eventCount < tt.wantEventCount {
					t.Errorf("Got %d events, want at least %d", eventCount, tt.wantEventCount)
				}

				// Verify done event is present
				if !strings.Contains(body, "event: done") {
					t.Error("Expected done event in response")
				}
			}
		})
	}
}

func TestHandleStreamExecute_NoAsyncRuntime(t *testing.T) {
	// Create server without async runtime
	server := NewAgentHTTPServer(nil)

	body, _ := json.Marshal(StreamExecuteRequest{
		AgentID: "test",
		Input:   "test",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/stream", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	server.HandleStreamExecute(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestRegisterAndGetAsyncAgent(t *testing.T) {
	server := NewAgentHTTPServer(nil)

	mockAsync := &mockAsyncAgent{id: "async-test", name: "Async Test"}
	server.RegisterAsyncAgent("async-test", mockAsync)

	// Test successful get
	ag, ok := server.GetAsyncAgent("async-test")
	if !ok {
		t.Error("Expected async agent to be found")
	}
	if ag != mockAsync {
		t.Error("Got different async agent instance")
	}

	// Test not found
	_, ok = server.GetAsyncAgent("nonexistent")
	if ok {
		t.Error("Expected async agent not to be found")
	}
}

func TestStreamingRouteRegistered(t *testing.T) {
	asyncRuntime := runtime.NewAsyncAgentRuntime(nil)
	server := NewAgentHTTPServer(nil).WithAsyncRuntime(asyncRuntime)

	mux := http.NewServeMux()
	server.SetupRoutes(mux)

	// Test streaming route is registered
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/stream", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	// Should not get 404 (will get 400 for invalid body)
	if w.Code == http.StatusNotFound {
		t.Error("Stream route not registered")
	}
}
