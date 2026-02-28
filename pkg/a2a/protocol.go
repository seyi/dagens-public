// Package a2a implements Agent-to-Agent protocol for distributed agent communication
// Based on the A2A Protocol specification (JSON-RPC 2.0 over HTTP(S))
// Inspired by https://github.com/a2aproject and ADK patterns
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

	"github.com/google/uuid"
	"github.com/seyi/dagens/pkg/agent"
)

// A2AClient enables agent-to-agent communication
type A2AClient interface {
	// InvokeAgent calls a remote agent
	InvokeAgent(ctx context.Context, agentID string, input *agent.AgentInput) (*agent.AgentOutput, error)

	// DiscoverAgents finds agents with specific capabilities
	DiscoverAgents(ctx context.Context, capability string) ([]*AgentInfo, error)

	// GetAgentCard retrieves an agent's capability declaration
	GetAgentCard(ctx context.Context, agentID string) (*AgentCard, error)

	// StreamInvocation invokes agent with streaming response
	StreamInvocation(ctx context.Context, agentID string, input *agent.AgentInput) (<-chan *StreamChunk, error)
}

// A2AServer handles incoming agent-to-agent requests
type A2AServer interface {
	// RegisterAgent registers an agent for A2A communication
	RegisterAgent(agent agent.Agent, card *AgentCard) error

	// UnregisterAgent removes an agent
	UnregisterAgent(agentID string) error

	// HandleInvocation processes an invocation request
	HandleInvocation(ctx context.Context, req *InvocationRequest) (*InvocationResponse, error)

	// Start starts the A2A server
	Start(address string) error

	// Stop stops the A2A server
	Stop() error
}

// AgentCard represents an agent's capability declaration (A2A standard)
type AgentCard struct {
	ID                string                 `json:"id"`
	Name              string                 `json:"name"`
	Description       string                 `json:"description"`
	Version           string                 `json:"version"`
	Endpoint          string                 `json:"endpoint"`
	Capabilities      []Capability           `json:"capabilities"`
	Modalities        []Modality             `json:"modalities"`
	AuthScheme        AuthScheme             `json:"auth_scheme"`
	Metadata          map[string]interface{} `json:"metadata"`
	SupportedPatterns []CommunicationPattern `json:"supported_patterns"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

// Capability describes what an agent can do
type Capability struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema interface{}            `json:"input_schema"`
	OutputSchema interface{}           `json:"output_schema"`
	Metadata    map[string]interface{} `json:"metadata"`
}

// Modality defines interaction modes
type Modality string

const (
	ModalityText   Modality = "text"
	ModalityForm   Modality = "form"
	ModalityMedia  Modality = "media"
	ModalityStream Modality = "stream"
)

// CommunicationPattern defines supported patterns
type CommunicationPattern string

const (
	PatternRequestResponse CommunicationPattern = "request_response"
	PatternSSE             CommunicationPattern = "server_sent_events"
	PatternAsyncPush       CommunicationPattern = "async_push"
)

// AuthScheme defines authentication method
type AuthScheme struct {
	Type       AuthType               `json:"type"`
	Parameters map[string]interface{} `json:"parameters"`
}

// AuthType defines auth types
type AuthType string

const (
	AuthTypeNone   AuthType = "none"
	AuthTypeBasic  AuthType = "basic"
	AuthTypeBearer AuthType = "bearer"
	AuthTypeAPIKey AuthType = "api_key"
	AuthTypeMTLS   AuthType = "mtls"
)

// AgentInfo provides basic agent information for discovery
type AgentInfo struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	Endpoint     string       `json:"endpoint"`
	Capabilities []string     `json:"capabilities"`
	Modalities   []Modality   `json:"modalities"`
	Status       AgentStatus  `json:"status"`
}

// AgentStatus represents agent availability
type AgentStatus string

const (
	StatusAvailable   AgentStatus = "available"
	StatusBusy        AgentStatus = "busy"
	StatusMaintenance AgentStatus = "maintenance"
	StatusOffline     AgentStatus = "offline"
)

// InvocationRequest represents an agent invocation (JSON-RPC 2.0)
type InvocationRequest struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      string                 `json:"id"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params"`
}

// InvocationResponse represents the response (JSON-RPC 2.0)
type InvocationResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      string      `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

// RPCError represents a JSON-RPC error
type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// StreamChunk represents a chunk of streaming data
type StreamChunk struct {
	ID        string
	Sequence  int
	Data      interface{}
	Final     bool
	Timestamp time.Time
}

// HTTPA2AClient implements A2A client over HTTP
type HTTPClient struct {
	httpClient *http.Client
	registry   *DiscoveryRegistry
	authToken  string
}

// NewHTTPA2AClient creates a new HTTP-based A2A client
func NewHTTPA2AClient(registry *DiscoveryRegistry) *HTTPClient {
	return &HTTPClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		registry: registry,
	}
}

// InvokeAgent calls a remote agent via JSON-RPC 2.0
func (c *HTTPClient) InvokeAgent(ctx context.Context, agentID string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// Get agent endpoint from registry
	agentInfo, err := c.registry.GetAgent(agentID)
	if err != nil {
		return nil, fmt.Errorf("agent not found: %w", err)
	}

	// Build JSON-RPC request
	request := &InvocationRequest{
		JSONRPC: "2.0",
		ID:      uuid.New().String(),
		Method:  "invoke",
		Params: map[string]interface{}{
			"instruction": input.Instruction,
			"context":     input.Context,
			"tools":       input.Tools,
			"timeout":     input.Timeout,
		},
	}

	// Marshal request
	reqBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", agentInfo.Endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.authToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	// Inject trace context into headers
	prop := otel.GetTextMapPropagator()
	prop.Inject(ctx, propagation.HeaderCarrier(httpReq.Header))

	// Execute request
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke agent: %w", err)
	}
	defer resp.Body.Close()

	// Parse response
	var rpcResp InvocationResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if rpcResp.Error != nil {
		return nil, fmt.Errorf("RPC error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	// Convert result to AgentOutput
	output := &agent.AgentOutput{
		TaskID: input.TaskID,
		Result: rpcResp.Result,
		Metadata: map[string]interface{}{
			"rpc_id": rpcResp.ID,
		},
	}

	return output, nil
}

// DiscoverAgents finds agents with specific capabilities
func (c *HTTPClient) DiscoverAgents(ctx context.Context, capability string) ([]*AgentInfo, error) {
	return c.registry.FindByCapability(capability), nil
}

// GetAgentCard retrieves an agent's card
func (c *HTTPClient) GetAgentCard(ctx context.Context, agentID string) (*AgentCard, error) {
	return c.registry.GetAgentCard(agentID)
}

// StreamInvocation invokes with streaming (SSE pattern)
func (c *HTTPClient) StreamInvocation(ctx context.Context, agentID string, input *agent.AgentInput) (<-chan *StreamChunk, error) {
	// Get agent endpoint from registry
	agentInfo, err := c.registry.GetAgent(agentID)
	if err != nil {
		return nil, fmt.Errorf("agent not found: %w", err)
	}

	// Build streaming endpoint (append /stream to base endpoint)
	streamEndpoint := agentInfo.Endpoint
	if streamEndpoint != "" && streamEndpoint[len(streamEndpoint)-1] != '/' {
		streamEndpoint += "/stream"
	} else {
		streamEndpoint += "stream"
	}

	// Build JSON-RPC request
	request := &InvocationRequest{
		JSONRPC: "2.0",
		ID:      uuid.New().String(),
		Method:  "stream",
		Params: map[string]interface{}{
			"agent_id":    agentID,
			"instruction": input.Instruction,
			"context":     input.Context,
			"task_id":     input.TaskID,
		},
	}

	// Marshal request
	reqBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request for SSE
	httpReq, err := http.NewRequestWithContext(ctx, "POST", streamEndpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Cache-Control", "no-cache")
	httpReq.Header.Set("Connection", "keep-alive")

	if c.authToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	// Inject trace context into headers
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(httpReq.Header))

	// Execute request
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to stream: %w", err)
	}

	// Verify response is SSE
	contentType := resp.Header.Get("Content-Type")
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("stream request failed with status %d", resp.StatusCode)
	}

	chunks := make(chan *StreamChunk, 10)

	// Start SSE reader goroutine
	go func() {
		defer close(chunks)
		defer resp.Body.Close()

		reader := NewSSEReader(resp.Body)
		sequence := 0

		for {
			select {
			case <-ctx.Done():
				return
			default:
				event, err := reader.ReadEvent()
				if err != nil {
					// Check if it's EOF (normal end of stream)
					if err.Error() == "EOF" {
						return
					}
					// Send error as final chunk
					chunks <- &StreamChunk{
						ID:        request.ID,
						Sequence:  sequence,
						Data:      map[string]interface{}{"error": err.Error()},
						Final:     true,
						Timestamp: time.Now(),
					}
					return
				}

				if event == nil {
					continue
				}

				sequence++
				chunk := &StreamChunk{
					ID:        request.ID,
					Sequence:  sequence,
					Data:      event.Data,
					Final:     event.Event == "done" || event.Event == "complete",
					Timestamp: time.Now(),
				}

				// Parse content type for special handling
				if contentType == "text/event-stream" || contentType == "" {
					// Standard SSE
				}

				select {
				case chunks <- chunk:
				case <-ctx.Done():
					return
				}

				if chunk.Final {
					return
				}
			}
		}
	}()

	return chunks, nil
}

// DiscoveryRegistry manages agent discovery
type DiscoveryRegistry struct {
	agents map[string]*AgentInfo
	cards  map[string]*AgentCard
	mu     sync.RWMutex
}

// NewDiscoveryRegistry creates a new registry
func NewDiscoveryRegistry() *DiscoveryRegistry {
	return &DiscoveryRegistry{
		agents: make(map[string]*AgentInfo),
		cards:  make(map[string]*AgentCard),
	}
}

// Register adds an agent to the registry
func (r *DiscoveryRegistry) Register(card *AgentCard) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	info := &AgentInfo{
		ID:           card.ID,
		Name:         card.Name,
		Description:  card.Description,
		Endpoint:     card.Endpoint,
		Capabilities: extractCapabilityNames(card.Capabilities),
		Modalities:   card.Modalities,
		Status:       StatusAvailable,
	}

	r.agents[card.ID] = info
	r.cards[card.ID] = card

	return nil
}

// Unregister removes an agent
func (r *DiscoveryRegistry) Unregister(agentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.agents, agentID)
	delete(r.cards, agentID)

	return nil
}

// GetAgent retrieves agent info
func (r *DiscoveryRegistry) GetAgent(agentID string) (*AgentInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	info, exists := r.agents[agentID]
	if !exists {
		return nil, fmt.Errorf("agent %s not found", agentID)
	}

	return info, nil
}

// GetAgentCard retrieves agent card
func (r *DiscoveryRegistry) GetAgentCard(agentID string) (*AgentCard, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	card, exists := r.cards[agentID]
	if !exists {
		return nil, fmt.Errorf("agent card %s not found", agentID)
	}

	return card, nil
}

// FindByCapability finds agents with a capability
func (r *DiscoveryRegistry) FindByCapability(capability string) []*AgentInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	results := make([]*AgentInfo, 0)

	for _, info := range r.agents {
		for _, cap := range info.Capabilities {
			if cap == capability {
				results = append(results, info)
				break
			}
		}
	}

	return results
}

// ListAll returns all registered agents
func (r *DiscoveryRegistry) ListAll() []*AgentInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	results := make([]*AgentInfo, 0, len(r.agents))
	for _, info := range r.agents {
		results = append(results, info)
	}

	return results
}

// Helper functions

func extractCapabilityNames(capabilities []Capability) []string {
	names := make([]string, len(capabilities))
	for i, cap := range capabilities {
		names[i] = cap.Name
	}
	return names
}
