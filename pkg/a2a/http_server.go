// Package a2a provides HTTP server implementation for the Agent-to-Agent protocol
package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/seyi/dagens/pkg/agent"
)

// HTTPServer implements the A2AServer interface using HTTP/JSON-RPC 2.0
type HTTPServer struct {
	httpServer   *http.Server
	registry     *DiscoveryRegistry
	agents       map[string]agent.Agent
	agentCards   map[string]*AgentCard
	mu           sync.RWMutex
	initialized  bool
}

// NewHTTPServer creates a new A2A HTTP server
func NewHTTPServer(registry *DiscoveryRegistry) *HTTPServer {
	server := &HTTPServer{
		registry:   registry,
		agents:     make(map[string]agent.Agent),
		agentCards: make(map[string]*AgentCard),
	}

	// Set up HTTP routes
	mux := http.NewServeMux()
	mux.HandleFunc("/a2a/invoke", server.handleInvoke)
	mux.HandleFunc("/a2a/register", server.handleRegister)
	mux.HandleFunc("/a2a/unregister", server.handleUnregister)
	mux.HandleFunc("/a2a/card", server.handleCard)
	mux.HandleFunc("/a2a/discover", server.handleDiscover)

	server.httpServer = &http.Server{
		Handler: mux,
	}

	return server
}

// RegisterAgent registers an agent for A2A communication
func (s *HTTPServer) RegisterAgent(agent agent.Agent, card *AgentCard) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.agents[agent.ID()] = agent
	s.agentCards[agent.ID()] = card

	// Also register in the discovery registry
	if err := s.registry.Register(card); err != nil {
		return fmt.Errorf("failed to register in discovery registry: %w", err)
	}

	return nil
}

// UnregisterAgent removes an agent
func (s *HTTPServer) UnregisterAgent(agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.agents, agentID)
	delete(s.agentCards, agentID)

	// Also unregister from the discovery registry
	return s.registry.Unregister(agentID)
}

// HandleInvocation processes an invocation request
func (s *HTTPServer) HandleInvocation(ctx context.Context, req *InvocationRequest) (*InvocationResponse, error) {
	// Extract agent ID from method (assuming format "agent_id.method")
	agentID := req.Method
	if req.Params != nil {
		if idParam, ok := req.Params["agent_id"]; ok {
			if idStr, ok := idParam.(string); ok {
				agentID = idStr
			}
		}
	}

	s.mu.RLock()
	agt, exists := s.agents[agentID]
	s.mu.RUnlock()

	if !exists {
		return &InvocationResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    -32601,
				Message: fmt.Sprintf("agent %s not found", agentID),
			},
		}, nil
	}

	// Convert params to AgentInput
	input := &agent.AgentInput{
		Instruction: "Remote invocation",
		Context:     req.Params,
		TaskID:      req.ID,
	}

	// Execute the agent
	output, err := agt.Execute(ctx, input)
	if err != nil {
		return &InvocationResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    -32603,
				Message: err.Error(),
			},
		}, nil
	}

	return &InvocationResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  output.Result,
	}, nil
}

// Start starts the A2A server
func (s *HTTPServer) Start(address string) error {
	s.mu.Lock()
	if s.initialized {
		s.mu.Unlock()
		return fmt.Errorf("server already initialized")
	}
	s.initialized = true
	s.mu.Unlock()

	s.httpServer.Addr = address
	return s.httpServer.ListenAndServe()
}

// Stop stops the A2A server
func (s *HTTPServer) Stop() error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(context.Background())
	}
	return nil
}

// handleInvoke handles agent invocation requests (JSON-RPC 2.0)
func (s *HTTPServer) handleInvoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req InvocationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Extract trace context from incoming headers
	ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))

	// Process the invocation with extracted trace context
	resp, err := s.HandleInvocation(ctx, &req)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleRegister handles agent registration requests
func (s *HTTPServer) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var regReq struct {
		AgentID string     `json:"agent_id"`
		Card    *AgentCard `json:"card"`
	}

	if err := json.NewDecoder(r.Body).Decode(&regReq); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if regReq.Card == nil {
		http.Error(w, "card is required", http.StatusBadRequest)
		return
	}

	// Use card ID if agent_id not provided
	agentID := regReq.AgentID
	if agentID == "" {
		agentID = regReq.Card.ID
	}

	// Build AgentInfo from the card
	agentInfo := &AgentInfo{
		ID:           agentID,
		Name:         regReq.Card.Name,
		Description:  regReq.Card.Description,
		Endpoint:     regReq.Card.Endpoint,
		Capabilities: extractCapabilityNames(regReq.Card.Capabilities),
		Modalities:   regReq.Card.Modalities,
		Status:       StatusAvailable,
	}

	// Create wrapper based on whether agent has a remote endpoint
	var wrapperAgent agent.Agent
	if agentInfo.Endpoint != "" {
		// Remote agent - use RemoteAgentWrapper for HTTP delegation
		wrapperAgent = NewRemoteAgentWrapper(agentInfo)
	} else {
		// Local agent - use SimpleAgentWrapper
		wrapperAgent = &SimpleAgentWrapper{
			id:   agentInfo.ID,
			name: agentInfo.Name,
		}
	}

	// Register the agent
	if err := s.RegisterAgent(wrapperAgent, regReq.Card); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success":  true,
		"message":  "agent registered successfully",
		"agent_id": agentID,
		"remote":   agentInfo.Endpoint != "",
	})
}

// handleUnregister handles agent unregistration requests
func (s *HTTPServer) handleUnregister(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var unregReq struct {
		AgentID string `json:"agent_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&unregReq); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if err := s.UnregisterAgent(unregReq.AgentID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "agent unregistered successfully",
	})
}

// handleCard handles agent card requests
func (s *HTTPServer) handleCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	agentID := r.URL.Query().Get("id")
	if agentID == "" {
		http.Error(w, "agent id is required", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	card, exists := s.agentCards[agentID]
	s.mu.RUnlock()

	if !exists {
		http.Error(w, "agent card not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(card)
}

// handleDiscover handles agent discovery requests
func (s *HTTPServer) handleDiscover(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	capability := r.URL.Query().Get("capability")
	if capability != "" {
		agents := s.registry.FindByCapability(capability)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(agents)
		return
	}

	// Return all agents if no capability filter
	allAgents := s.registry.ListAll()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(allAgents)
}

// RemoteAgentWrapper wraps a remote agent for A2A communication
// It implements the agent.Agent interface and delegates execution to the remote endpoint
type RemoteAgentWrapper struct {
	id           string
	name         string
	description  string
	endpoint     string
	capabilities []string
	httpClient   *http.Client
}

// NewRemoteAgentWrapper creates a wrapper for a remote agent
func NewRemoteAgentWrapper(info *AgentInfo) *RemoteAgentWrapper {
	return &RemoteAgentWrapper{
		id:           info.ID,
		name:         info.Name,
		description:  info.Description,
		endpoint:     info.Endpoint,
		capabilities: info.Capabilities,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (r *RemoteAgentWrapper) ID() string {
	return r.id
}

func (r *RemoteAgentWrapper) Name() string {
	return r.name
}

func (r *RemoteAgentWrapper) Description() string {
	if r.description != "" {
		return r.description
	}
	return "A2A remote agent: " + r.name
}

func (r *RemoteAgentWrapper) Capabilities() []string {
	return r.capabilities
}

func (r *RemoteAgentWrapper) Dependencies() []agent.Agent {
	return []agent.Agent{}
}

func (r *RemoteAgentWrapper) Partition() string {
	return "a2a-remote"
}

// Execute invokes the remote agent via HTTP JSON-RPC 2.0
func (r *RemoteAgentWrapper) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	if r.endpoint == "" {
		return nil, fmt.Errorf("remote agent %s has no endpoint configured", r.id)
	}

	// Build JSON-RPC 2.0 request
	requestID := fmt.Sprintf("%s-%d", r.id, time.Now().UnixNano())
	rpcRequest := &InvocationRequest{
		JSONRPC: "2.0",
		ID:      requestID,
		Method:  "invoke",
		Params: map[string]interface{}{
			"agent_id":    r.id,
			"instruction": input.Instruction,
			"context":     input.Context,
			"task_id":     input.TaskID,
		},
	}

	// Marshal request body
	reqBody, err := json.Marshal(rpcRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal RPC request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", r.endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Inject trace context into headers
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(httpReq.Header))

	// Execute HTTP request
	resp, err := r.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke remote agent %s: %w", r.id, err)
	}
	defer resp.Body.Close()

	// Check HTTP status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remote agent %s returned status %d", r.id, resp.StatusCode)
	}

	// Parse JSON-RPC response
	var rpcResp InvocationResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("failed to decode RPC response: %w", err)
	}

	// Check for RPC error
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("remote agent %s error [%d]: %s", r.id, rpcResp.Error.Code, rpcResp.Error.Message)
	}

	// Build output
	return &agent.AgentOutput{
		TaskID: input.TaskID,
		Result: rpcResp.Result,
		Metadata: map[string]interface{}{
			"a2a_remote":  true,
			"rpc_id":      rpcResp.ID,
			"agent_id":    r.id,
			"endpoint":    r.endpoint,
		},
	}, nil
}

// SimpleAgentWrapper is kept for backward compatibility with local agents
// Use RemoteAgentWrapper for remote A2A agents
type SimpleAgentWrapper struct {
	id   string
	name string
}

func (s *SimpleAgentWrapper) ID() string {
	return s.id
}

func (s *SimpleAgentWrapper) Name() string {
	return s.name
}

func (s *SimpleAgentWrapper) Description() string {
	return "A2A local agent wrapper"
}

func (s *SimpleAgentWrapper) Capabilities() []string {
	return []string{}
}

func (s *SimpleAgentWrapper) Dependencies() []agent.Agent {
	return []agent.Agent{}
}

func (s *SimpleAgentWrapper) Partition() string {
	return "a2a-local"
}

func (s *SimpleAgentWrapper) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	return &agent.AgentOutput{
		Result:   "Local A2A wrapper - no remote endpoint",
		Metadata: map[string]interface{}{"a2a_wrapper": true, "local": true},
	}, nil
}