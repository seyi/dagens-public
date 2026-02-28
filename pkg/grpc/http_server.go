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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/runtime"
)

// AgentHTTPServer provides HTTP API for agent execution (for Python integration)
type AgentHTTPServer struct {
	runtime      *runtime.AgentRuntime
	asyncRuntime *runtime.AsyncAgentRuntime
	agents       map[string]agent.Agent
	asyncAgents  map[string]runtime.AsyncAgent
	mu           sync.RWMutex
}

// ExecuteRequest represents an agent execution request
type ExecuteRequest struct {
	AgentID   string            `json:"agent_id"`
	Input     string            `json:"input"`
	Context   map[string]string `json:"context"`
	SessionID string            `json:"session_id"`
	UserID    string            `json:"user_id"`
}

// ExecuteResponse represents an agent execution response
type ExecuteResponse struct {
	Output     string            `json:"output"`
	Metadata   map[string]string `json:"metadata"`
	Success    bool              `json:"success"`
	Error      string            `json:"error,omitempty"`
	DurationMs int64             `json:"duration_ms"`
}

// BatchExecuteRequest represents a batch execution request
type BatchExecuteRequest struct {
	AgentID string            `json:"agent_id"`
	Inputs  []string          `json:"inputs"`
	Context map[string]string `json:"context"`
}

// BatchExecuteResponse represents a batch execution response
type BatchExecuteResponse struct {
	Results []ExecuteResponse `json:"results"`
}

// AgentInfo describes an available agent
type AgentInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
}

// ListAgentsResponse lists available agents
type ListAgentsResponse struct {
	Agents []AgentInfo `json:"agents"`
}

// StreamExecuteRequest represents a streaming execution request
type StreamExecuteRequest struct {
	AgentID   string            `json:"agent_id"`
	Input     string            `json:"input"`
	Context   map[string]string `json:"context"`
	SessionID string            `json:"session_id"`
	UserID    string            `json:"user_id"`
}

// StreamEvent represents a single event in the stream (SSE format)
type StreamEvent struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Content   interface{}            `json:"content"`
	Partial   bool                   `json:"partial"`
	Timestamp string                 `json:"timestamp"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Error     string                 `json:"error,omitempty"`
}

// NewAgentHTTPServer creates a new HTTP server for agents
func NewAgentHTTPServer(rt *runtime.AgentRuntime) *AgentHTTPServer {
	return &AgentHTTPServer{
		runtime:     rt,
		agents:      make(map[string]agent.Agent),
		asyncAgents: make(map[string]runtime.AsyncAgent),
	}
}

// WithAsyncRuntime sets the async runtime for streaming execution
func (s *AgentHTTPServer) WithAsyncRuntime(asyncRT *runtime.AsyncAgentRuntime) *AgentHTTPServer {
	s.asyncRuntime = asyncRT
	return s
}

// RegisterAgent registers an agent with the server
func (s *AgentHTTPServer) RegisterAgent(id string, ag agent.Agent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents[id] = ag
}

// GetAgent retrieves a registered agent
func (s *AgentHTTPServer) GetAgent(id string) (agent.Agent, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ag, ok := s.agents[id]
	return ag, ok
}

// RegisterAsyncAgent registers an async agent with the server
func (s *AgentHTTPServer) RegisterAsyncAgent(id string, ag runtime.AsyncAgent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asyncAgents[id] = ag
}

// GetAsyncAgent retrieves a registered async agent
func (s *AgentHTTPServer) GetAsyncAgent(id string) (runtime.AsyncAgent, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ag, ok := s.asyncAgents[id]
	return ag, ok
}

// HandleExecute handles single agent execution
func (s *AgentHTTPServer) HandleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	start := time.Now()

	// Get agent
	ag, ok := s.GetAgent(req.AgentID)
	if !ok {
		resp := ExecuteResponse{
			Success: false,
			Error:   fmt.Sprintf("agent not found: %s", req.AgentID),
		}
		s.writeJSON(w, http.StatusNotFound, resp)
		return
	}

	// Convert string context to interface{} map
	contextMap := make(map[string]interface{})
	for k, v := range req.Context {
		contextMap[k] = v
	}

	// Create input
	input := &agent.AgentInput{
		Instruction: req.Input,
		Context:     contextMap,
	}

	// Execute agent
	// Extract trace context from headers
	prop := otel.GetTextMapPropagator()
	ctx := prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

	// Start a new span for agent execution on the server side
	serverTracer := otel.Tracer("agent-server")
	ctx, span := serverTracer.Start(ctx, "agent.execute")
	defer span.End()

	output, err := ag.Execute(ctx, input)

	duration := time.Since(start).Milliseconds()

	if err != nil {
		resp := ExecuteResponse{
			Success:    false,
			Error:      err.Error(),
			DurationMs: duration,
		}
		s.writeJSON(w, http.StatusOK, resp)
		return
	}

	// Build response
	metadata := make(map[string]string)
	if output.Metadata != nil {
		for k, v := range output.Metadata {
			metadata[k] = fmt.Sprintf("%v", v)
		}
	}

	// Convert Result to string
	resultStr := fmt.Sprintf("%v", output.Result)

	resp := ExecuteResponse{
		Output:     resultStr,
		Metadata:   metadata,
		Success:    true,
		DurationMs: duration,
	}

	s.writeJSON(w, http.StatusOK, resp)
}

// HandleBatchExecute handles batch agent execution
func (s *AgentHTTPServer) HandleBatchExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req BatchExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Get agent
	ag, ok := s.GetAgent(req.AgentID)
	if !ok {
		http.Error(w, fmt.Sprintf("agent not found: %s", req.AgentID), http.StatusNotFound)
		return
	}

	// Convert string context to interface{} map
	contextMap := make(map[string]interface{})
	for k, v := range req.Context {
		contextMap[k] = v
	}

	// Execute all inputs
	results := make([]ExecuteResponse, len(req.Inputs))

	// Extract trace context once before the loop
	prop := otel.GetTextMapPropagator()
	ctx := prop.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

	for i, input := range req.Inputs {
		start := time.Now()

		agentInput := &agent.AgentInput{
			Instruction: input,
			Context:     contextMap,
		}

		// Start a new span for agent execution on the server side for each batch item
		serverTracer := otel.Tracer("agent-server")
		itemCtx, span := serverTracer.Start(ctx, "agent.batch_execute_item")
		defer span.End()

		output, err := ag.Execute(itemCtx, agentInput)

		duration := time.Since(start).Milliseconds()

		if err != nil {
			results[i] = ExecuteResponse{
				Success:    false,
				Error:      err.Error(),
				DurationMs: duration,
			}
			continue
		}

		// Build response
		metadata := make(map[string]string)
		if output.Metadata != nil {
			for k, v := range output.Metadata {
				metadata[k] = fmt.Sprintf("%v", v)
			}
		}

		// Convert Result to string
		resultStr := fmt.Sprintf("%v", output.Result)

		results[i] = ExecuteResponse{
			Output:     resultStr,
			Metadata:   metadata,
			Success:    true,
			DurationMs: duration,
		}
	}

	resp := BatchExecuteResponse{
		Results: results,
	}

	s.writeJSON(w, http.StatusOK, resp)
}

// HandleHealthCheck handles health check requests
func (s *AgentHTTPServer) HandleHealthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"healthy"}`))
}

// HandleListAgents handles listing available agents
func (s *AgentHTTPServer) HandleListAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	agents := make([]AgentInfo, 0, len(s.agents))
	for id, ag := range s.agents {
		agents = append(agents, AgentInfo{
			ID:          id,
			Name:        id,
			Description: fmt.Sprintf("Agent: %s", id),
			Type:        fmt.Sprintf("%T", ag),
		})
	}

	resp := ListAgentsResponse{
		Agents: agents,
	}

	s.writeJSON(w, http.StatusOK, resp)
}

// writeJSON writes a JSON response
func (s *AgentHTTPServer) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// HandleStreamExecute handles streaming agent execution using Server-Sent Events (SSE)
// Events are streamed as they are generated by the async agent
func (s *AgentHTTPServer) HandleStreamExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req StreamExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Check for async runtime
	if s.asyncRuntime == nil {
		http.Error(w, "Streaming not enabled: async runtime not configured", http.StatusServiceUnavailable)
		return
	}

	// Get async agent
	asyncAg, ok := s.GetAsyncAgent(req.AgentID)
	if !ok {
		http.Error(w, fmt.Sprintf("async agent not found: %s", req.AgentID), http.StatusNotFound)
		return
	}

	// Set up SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	// Ensure we can flush
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	// Convert string context to interface{} map
	contextMap := make(map[string]interface{})
	for k, v := range req.Context {
		contextMap[k] = v
	}

	// Create input
	input := &agent.AgentInput{
		Instruction: req.Input,
		Context:     contextMap,
	}

	// Create a context that can be cancelled if the client disconnects
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Execute async agent and stream events
	events := s.asyncRuntime.RunAsync(ctx, asyncAg, input)

	for event := range events {
		// Convert async event to stream event
		streamEvent := StreamEvent{
			ID:        event.ID,
			Type:      event.Type.String(),
			Content:   event.Content,
			Partial:   event.Partial,
			Timestamp: event.Timestamp.Format(time.RFC3339Nano),
			Metadata:  event.Metadata,
		}

		if event.Error != nil {
			streamEvent.Error = event.Error.Error()
		}

		// Serialize event
		data, err := json.Marshal(streamEvent)
		if err != nil {
			// Send error event
			fmt.Fprintf(w, "event: error\ndata: {\"error\": \"serialization error\"}\n\n")
			flusher.Flush()
			return
		}

		// Write SSE format
		fmt.Fprintf(w, "event: %s\n", event.Type.String())
		fmt.Fprintf(w, "data: %s\n\n", string(data))
		flusher.Flush()

		// Check for context cancellation (client disconnect)
		select {
		case <-ctx.Done():
			return
		default:
		}
	}

	// Send completion event
	fmt.Fprintf(w, "event: done\ndata: {\"status\": \"completed\"}\n\n")
	flusher.Flush()
}

// SetupRoutes sets up HTTP routes
func (s *AgentHTTPServer) SetupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/agents/execute", s.HandleExecute)
	mux.HandleFunc("/api/v1/agents/batch_execute", s.HandleBatchExecute)
	mux.HandleFunc("/api/v1/agents/stream", s.HandleStreamExecute)
	mux.HandleFunc("/api/v1/agents", s.HandleListAgents)
	mux.HandleFunc("/health", s.HandleHealthCheck)
}

// Start starts the HTTP server
func (s *AgentHTTPServer) Start(addr string) error {
	mux := http.NewServeMux()
	s.SetupRoutes(mux)

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	fmt.Printf("Starting agent HTTP server on %s\n", addr)
	return server.ListenAndServe()
}
