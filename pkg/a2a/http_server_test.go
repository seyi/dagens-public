package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/stretchr/testify/assert"
)

// MockAgent is a test agent implementation
type MockAgent struct {
	id          string
	name        string
	description string
	capabilities []string
	failOnExecute bool
}

func NewMockAgent(id string, failOnExecute bool) *MockAgent {
	return &MockAgent{
		id:            id,
		name:          id,
		description:   "Mock agent for testing",
		capabilities:  []string{"test"},
		failOnExecute: failOnExecute,
	}
}

func (m *MockAgent) ID() string {
	return m.id
}

func (m *MockAgent) Name() string {
	return m.name
}

func (m *MockAgent) Description() string {
	return m.description
}

func (m *MockAgent) Capabilities() []string {
	return m.capabilities
}

func (m *MockAgent) Dependencies() []agent.Agent {
	return []agent.Agent{}
}

func (m *MockAgent) Partition() string {
	return "default"
}

func (m *MockAgent) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	if m.failOnExecute {
		return nil, assert.AnError
	}
	
	return &agent.AgentOutput{
		Result: "Mock agent executed successfully",
		Metadata: map[string]interface{}{
			"agent_id": m.id,
			"input_instruction": input.Instruction,
		},
	}, nil
}

func TestHTTPServer_RegisterAgent(t *testing.T) {
	registry := NewDiscoveryRegistry()
	server := NewHTTPServer(registry)

	mockAgent := NewMockAgent("test-agent", false)
	card := &AgentCard{
		ID:          "test-agent",
		Name:        "Test Agent",
		Description: "A test agent",
		Endpoint:    "http://localhost:8080/a2a",
		Capabilities: []Capability{
			{
				Name:        "test_capability",
				Description: "Test capability",
			},
		},
		Modalities: []Modality{ModalityText},
		AuthScheme: AuthScheme{Type: AuthTypeNone},
	}

	err := server.RegisterAgent(mockAgent, card)
	assert.NoError(t, err)

	// Verify agent was registered
	agentInfo, err := registry.GetAgent("test-agent")
	assert.NoError(t, err)
	assert.Equal(t, "Test Agent", agentInfo.Name)
}

func TestHTTPServer_InvokeAgent(t *testing.T) {
	registry := NewDiscoveryRegistry()
	server := NewHTTPServer(registry)

	mockAgent := NewMockAgent("test-agent", false)
	card := &AgentCard{
		ID:          "test-agent",
		Name:        "Test Agent",
		Description: "A test agent",
		Endpoint:    "http://localhost:8080/a2a",
		Capabilities: []Capability{
			{
				Name:        "test_capability",
				Description: "Test capability",
			},
		},
		Modalities: []Modality{ModalityText},
		AuthScheme: AuthScheme{Type: AuthTypeNone},
	}

	err := server.RegisterAgent(mockAgent, card)
	assert.NoError(t, err)

	// Create invocation request
	req := &InvocationRequest{
		JSONRPC: "2.0",
		ID:      "test-id",
		Method:  "test-agent",
		Params: map[string]interface{}{
			"instruction": "Test instruction",
			"context":     map[string]interface{}{"key": "value"},
		},
	}

	resp, err := server.HandleInvocation(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "2.0", resp.JSONRPC)
	assert.Equal(t, "test-id", resp.ID)
	assert.NotNil(t, resp.Result)
}

func TestHTTPServer_HTTPInvokeEndpoint(t *testing.T) {
	registry := NewDiscoveryRegistry()
	server := NewHTTPServer(registry)

	mockAgent := NewMockAgent("test-agent", false)
	card := &AgentCard{
		ID:          "test-agent",
		Name:        "Test Agent",
		Description: "A test agent",
		Endpoint:    "http://localhost:8080/a2a",
		Capabilities: []Capability{
			{
				Name:        "test_capability",
				Description: "Test capability",
			},
		},
		Modalities: []Modality{ModalityText},
		AuthScheme: AuthScheme{Type: AuthTypeNone},
	}

	err := server.RegisterAgent(mockAgent, card)
	assert.NoError(t, err)

	// Create JSON-RPC request
	jsonReq := &InvocationRequest{
		JSONRPC: "2.0",
		ID:      "test-id",
		Method:  "test-agent",
		Params: map[string]interface{}{
			"instruction": "Test instruction",
			"context":     map[string]interface{}{"key": "value"},
		},
	}

	jsonData, err := json.Marshal(jsonReq)
	assert.NoError(t, err)

	// Create HTTP request
	httpReq := httptest.NewRequest("POST", "/a2a/invoke", bytes.NewBuffer(jsonData))
	httpReq.Header.Set("Content-Type", "application/json")

	// Create response recorder
	recorder := httptest.NewRecorder()

	// Call the handler
	server.handleInvoke(recorder, httpReq)

	// Check response
	assert.Equal(t, http.StatusOK, recorder.Code)

	var response InvocationResponse
	err = json.Unmarshal(recorder.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, "2.0", response.JSONRPC)
	assert.Equal(t, "test-id", response.ID)
	assert.NotNil(t, response.Result)
}

func TestHTTPServer_HTTPRegisterEndpoint(t *testing.T) {
	registry := NewDiscoveryRegistry()
	server := NewHTTPServer(registry)

	// Create registration request
	regReq := struct {
		AgentID string     `json:"agent_id"`
		Card    *AgentCard `json:"card"`
	}{
		AgentID: "test-agent",
		Card: &AgentCard{
			ID:          "test-agent",
			Name:        "Test Agent",
			Description: "A test agent",
			Endpoint:    "http://localhost:8080/a2a",
			Capabilities: []Capability{
				{
					Name:        "test_capability",
					Description: "Test capability",
				},
			},
			Modalities: []Modality{ModalityText},
			AuthScheme: AuthScheme{Type: AuthTypeNone},
		},
	}

	jsonData, err := json.Marshal(regReq)
	assert.NoError(t, err)

	// Create HTTP request
	httpReq := httptest.NewRequest("POST", "/a2a/register", bytes.NewBuffer(jsonData))
	httpReq.Header.Set("Content-Type", "application/json")

	// Create response recorder
	recorder := httptest.NewRecorder()

	// Call the handler (this will fail because the agent doesn't exist in the registry yet)
	// We need to first register an agent in the registry for this to work
	agentInfo := &AgentInfo{
		ID:           "test-agent",
		Name:         "Test Agent",
		Description:  "A test agent",
		Endpoint:     "http://localhost:8080/a2a",
		Capabilities: []string{"test_capability"},
		Modalities:   []Modality{ModalityText},
		Status:       StatusAvailable,
	}
	
	// Add agent to registry
	registry.agents["test-agent"] = agentInfo

	// Now call the handler
	server.handleRegister(recorder, httpReq)

	// Check response
	assert.Equal(t, http.StatusOK, recorder.Code)
}

func TestHTTPServer_UnregisterAgent(t *testing.T) {
	registry := NewDiscoveryRegistry()
	server := NewHTTPServer(registry)

	mockAgent := NewMockAgent("test-agent", false)
	card := &AgentCard{
		ID:          "test-agent",
		Name:        "Test Agent",
		Description: "A test agent",
		Endpoint:    "http://localhost:8080/a2a",
		Capabilities: []Capability{
			{
				Name:        "test_capability",
				Description: "Test capability",
			},
		},
		Modalities: []Modality{ModalityText},
		AuthScheme: AuthScheme{Type: AuthTypeNone},
	}

	err := server.RegisterAgent(mockAgent, card)
	assert.NoError(t, err)

	// Verify agent was registered
	_, err = registry.GetAgent("test-agent")
	assert.NoError(t, err)

	// Unregister the agent
	err = server.UnregisterAgent("test-agent")
	assert.NoError(t, err)

	// Verify agent was unregistered
	_, err = registry.GetAgent("test-agent")
	assert.Error(t, err)
}

func TestHTTPServer_GetAgentCard(t *testing.T) {
	registry := NewDiscoveryRegistry()
	server := NewHTTPServer(registry)

	mockAgent := NewMockAgent("test-agent", false)
	card := &AgentCard{
		ID:          "test-agent",
		Name:        "Test Agent",
		Description: "A test agent",
		Endpoint:    "http://localhost:8080/a2a",
		Capabilities: []Capability{
			{
				Name:        "test_capability",
				Description: "Test capability",
			},
		},
		Modalities: []Modality{ModalityText},
		AuthScheme: AuthScheme{Type: AuthTypeNone},
	}

	err := server.RegisterAgent(mockAgent, card)
	assert.NoError(t, err)

	// Create HTTP request to get card
	httpReq := httptest.NewRequest("GET", "/a2a/card?id=test-agent", nil)

	// Create response recorder
	recorder := httptest.NewRecorder()

	// Call the handler
	server.handleCard(recorder, httpReq)

	// Check response
	assert.Equal(t, http.StatusOK, recorder.Code)

	var response AgentCard
	err = json.Unmarshal(recorder.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, "test-agent", response.ID)
	assert.Equal(t, "Test Agent", response.Name)
}

// Tests for RemoteAgentWrapper

func TestNewRemoteAgentWrapper(t *testing.T) {
	info := &AgentInfo{
		ID:           "remote-agent-1",
		Name:         "Remote Agent",
		Description:  "A remote test agent",
		Endpoint:     "http://localhost:9999/a2a/invoke",
		Capabilities: []string{"capability1", "capability2"},
		Status:       StatusAvailable,
	}

	wrapper := NewRemoteAgentWrapper(info)

	assert.Equal(t, "remote-agent-1", wrapper.ID())
	assert.Equal(t, "Remote Agent", wrapper.Name())
	assert.Equal(t, "A remote test agent", wrapper.Description())
	assert.Equal(t, "a2a-remote", wrapper.Partition())
	assert.Equal(t, []string{"capability1", "capability2"}, wrapper.Capabilities())
	assert.Empty(t, wrapper.Dependencies())
}

func TestRemoteAgentWrapper_Execute_Success(t *testing.T) {
	// Create a mock remote agent server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		// Parse request
		var req InvocationRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		assert.NoError(t, err)
		assert.Equal(t, "2.0", req.JSONRPC)
		assert.Equal(t, "invoke", req.Method)

		// Send response
		resp := &InvocationResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{"message": "Hello from remote agent"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	// Create wrapper pointing to mock server
	info := &AgentInfo{
		ID:           "remote-agent",
		Name:         "Remote Agent",
		Endpoint:     mockServer.URL,
		Capabilities: []string{"test"},
	}
	wrapper := NewRemoteAgentWrapper(info)

	// Execute
	input := &agent.AgentInput{
		Instruction: "Test instruction",
		TaskID:      "task-123",
	}
	output, err := wrapper.Execute(context.Background(), input)

	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.Equal(t, "task-123", output.TaskID)

	// Check metadata
	assert.True(t, output.Metadata["a2a_remote"].(bool))
	assert.Equal(t, "remote-agent", output.Metadata["agent_id"])
}

func TestRemoteAgentWrapper_Execute_RPCError(t *testing.T) {
	// Create a mock server that returns an RPC error
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req InvocationRequest
		json.NewDecoder(r.Body).Decode(&req)

		resp := &InvocationResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    -32601,
				Message: "Method not found",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	info := &AgentInfo{
		ID:       "remote-agent",
		Name:     "Remote Agent",
		Endpoint: mockServer.URL,
	}
	wrapper := NewRemoteAgentWrapper(info)

	input := &agent.AgentInput{Instruction: "Test"}
	_, err := wrapper.Execute(context.Background(), input)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Method not found")
}

func TestRemoteAgentWrapper_Execute_NoEndpoint(t *testing.T) {
	info := &AgentInfo{
		ID:       "remote-agent",
		Name:     "Remote Agent",
		Endpoint: "", // No endpoint
	}
	wrapper := NewRemoteAgentWrapper(info)

	input := &agent.AgentInput{Instruction: "Test"}
	_, err := wrapper.Execute(context.Background(), input)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no endpoint configured")
}

func TestRemoteAgentWrapper_Execute_ServerError(t *testing.T) {
	// Create a mock server that returns 500
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mockServer.Close()

	info := &AgentInfo{
		ID:       "remote-agent",
		Name:     "Remote Agent",
		Endpoint: mockServer.URL,
	}
	wrapper := NewRemoteAgentWrapper(info)

	input := &agent.AgentInput{Instruction: "Test"}
	_, err := wrapper.Execute(context.Background(), input)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "returned status 500")
}

func TestHTTPServer_RegisterRemoteAgent(t *testing.T) {
	registry := NewDiscoveryRegistry()
	server := NewHTTPServer(registry)

	// Create registration request for a remote agent
	regReq := struct {
		AgentID string     `json:"agent_id"`
		Card    *AgentCard `json:"card"`
	}{
		Card: &AgentCard{
			ID:          "remote-agent",
			Name:        "Remote Agent",
			Description: "A remote test agent",
			Endpoint:    "http://remote-host:8080/a2a/invoke",
			Capabilities: []Capability{
				{Name: "remote_capability", Description: "Remote capability"},
			},
			Modalities: []Modality{ModalityText},
			AuthScheme: AuthScheme{Type: AuthTypeBearer},
		},
	}

	jsonData, err := json.Marshal(regReq)
	assert.NoError(t, err)

	httpReq := httptest.NewRequest("POST", "/a2a/register", bytes.NewBuffer(jsonData))
	httpReq.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	server.handleRegister(recorder, httpReq)

	assert.Equal(t, http.StatusOK, recorder.Code)

	// Verify response indicates remote agent
	var response map[string]interface{}
	err = json.Unmarshal(recorder.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.True(t, response["success"].(bool))
	assert.True(t, response["remote"].(bool))
	assert.Equal(t, "remote-agent", response["agent_id"])
}

func TestHTTPServer_RegisterLocalAgent(t *testing.T) {
	registry := NewDiscoveryRegistry()
	server := NewHTTPServer(registry)

	// Create registration request for a local agent (no endpoint)
	regReq := struct {
		AgentID string     `json:"agent_id"`
		Card    *AgentCard `json:"card"`
	}{
		Card: &AgentCard{
			ID:          "local-agent",
			Name:        "Local Agent",
			Description: "A local test agent",
			Endpoint:    "", // No endpoint = local
			Capabilities: []Capability{
				{Name: "local_capability", Description: "Local capability"},
			},
			Modalities: []Modality{ModalityText},
			AuthScheme: AuthScheme{Type: AuthTypeNone},
		},
	}

	jsonData, err := json.Marshal(regReq)
	assert.NoError(t, err)

	httpReq := httptest.NewRequest("POST", "/a2a/register", bytes.NewBuffer(jsonData))
	httpReq.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	server.handleRegister(recorder, httpReq)

	assert.Equal(t, http.StatusOK, recorder.Code)

	// Verify response indicates local agent
	var response map[string]interface{}
	err = json.Unmarshal(recorder.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.True(t, response["success"].(bool))
	assert.False(t, response["remote"].(bool))
	assert.Equal(t, "local-agent", response["agent_id"])
}

// Trace propagation tests

// setupOTELForTest configures OpenTelemetry for testing with W3C TraceContext
func setupOTELForTest() func() {
	// Set the global propagator to W3C TraceContext
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Create a simple tracer provider for testing
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)

	return func() {
		tp.Shutdown(context.Background())
	}
}

func TestHTTPServer_HandleInvoke_ExtractsTraceContext(t *testing.T) {
	cleanup := setupOTELForTest()
	defer cleanup()

	registry := NewDiscoveryRegistry()
	server := NewHTTPServer(registry)

	// Create a mock agent that captures the trace context
	var capturedTraceID trace.TraceID
	contextCapturingAgent := &contextCapturingMockAgent{
		id: "trace-test-agent",
		onExecute: func(ctx context.Context) {
			span := trace.SpanFromContext(ctx)
			if span.SpanContext().IsValid() {
				capturedTraceID = span.SpanContext().TraceID()
			}
		},
	}

	card := &AgentCard{
		ID:          "trace-test-agent",
		Name:        "Trace Test Agent",
		Description: "Tests trace context extraction",
		Endpoint:    "http://localhost:8080/a2a",
		Capabilities: []Capability{
			{Name: "test_capability", Description: "Test capability"},
		},
	}

	err := server.RegisterAgent(contextCapturingAgent, card)
	assert.NoError(t, err)

	// Create JSON-RPC request
	jsonReq := &InvocationRequest{
		JSONRPC: "2.0",
		ID:      "trace-test-id",
		Method:  "trace-test-agent",
		Params: map[string]interface{}{
			"instruction": "Test instruction",
		},
	}

	jsonData, err := json.Marshal(jsonReq)
	assert.NoError(t, err)

	// Create HTTP request with W3C TraceContext headers
	httpReq := httptest.NewRequest("POST", "/a2a/invoke", bytes.NewBuffer(jsonData))
	httpReq.Header.Set("Content-Type", "application/json")
	// W3C TraceContext format: version-traceid-spanid-flags
	httpReq.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")

	recorder := httptest.NewRecorder()
	server.handleInvoke(recorder, httpReq)

	assert.Equal(t, http.StatusOK, recorder.Code)

	// Verify the trace context was extracted and passed to the agent
	expectedTraceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	assert.Equal(t, expectedTraceID, capturedTraceID, "Trace ID should be extracted from incoming headers")
}

func TestRemoteAgentWrapper_Execute_InjectsTraceContext(t *testing.T) {
	cleanup := setupOTELForTest()
	defer cleanup()

	// Create a mock server that captures the trace headers
	var receivedTraceparent string
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedTraceparent = r.Header.Get("traceparent")

		// Send successful response
		resp := &InvocationResponse{
			JSONRPC: "2.0",
			ID:      "test-id",
			Result:  map[string]interface{}{"message": "Success"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	// Create wrapper pointing to mock server
	info := &AgentInfo{
		ID:           "remote-agent",
		Name:         "Remote Agent",
		Endpoint:     mockServer.URL,
		Capabilities: []string{"test"},
	}
	wrapper := NewRemoteAgentWrapper(info)

	// Create a context with a trace span
	tracer := otel.Tracer("test-tracer")
	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	// Execute the remote agent
	input := &agent.AgentInput{
		Instruction: "Test instruction",
		TaskID:      "task-123",
	}
	_, err := wrapper.Execute(ctx, input)
	assert.NoError(t, err)

	// Verify the trace context was injected into the outgoing request
	assert.NotEmpty(t, receivedTraceparent, "traceparent header should be injected into outgoing request")
	assert.Contains(t, receivedTraceparent, "00-", "traceparent should follow W3C format")
}

func TestHTTPServer_TracePropagation_EndToEnd(t *testing.T) {
	cleanup := setupOTELForTest()
	defer cleanup()

	// This test simulates end-to-end trace propagation:
	// 1. Client sends request with trace context
	// 2. Server extracts trace context
	// 3. Server calls remote agent, injecting trace context
	// 4. Remote agent receives the same trace context

	var remoteReceivedTraceparent string

	// Create a mock remote agent server
	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteReceivedTraceparent = r.Header.Get("traceparent")

		resp := &InvocationResponse{
			JSONRPC: "2.0",
			ID:      "remote-id",
			Result:  map[string]interface{}{"message": "Remote success"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer remoteServer.Close()

	// Setup local A2A server
	registry := NewDiscoveryRegistry()
	server := NewHTTPServer(registry)

	// Register a remote agent wrapper
	remoteInfo := &AgentInfo{
		ID:           "chained-remote-agent",
		Name:         "Chained Remote Agent",
		Endpoint:     remoteServer.URL,
		Capabilities: []string{"chain"},
	}
	remoteWrapper := NewRemoteAgentWrapper(remoteInfo)

	card := &AgentCard{
		ID:       "chained-remote-agent",
		Name:     "Chained Remote Agent",
		Endpoint: remoteServer.URL,
	}
	err := server.RegisterAgent(remoteWrapper, card)
	assert.NoError(t, err)

	// Create invoke request
	jsonReq := &InvocationRequest{
		JSONRPC: "2.0",
		ID:      "chain-test-id",
		Method:  "chained-remote-agent",
		Params:  map[string]interface{}{"agent_id": "chained-remote-agent"},
	}

	jsonData, _ := json.Marshal(jsonReq)

	// Send request with trace context
	httpReq := httptest.NewRequest("POST", "/a2a/invoke", bytes.NewBuffer(jsonData))
	httpReq.Header.Set("Content-Type", "application/json")
	// Original trace context from external caller
	originalTraceparent := "00-abcdef1234567890abcdef1234567890-1234567890abcdef-01"
	httpReq.Header.Set("traceparent", originalTraceparent)

	recorder := httptest.NewRecorder()
	server.handleInvoke(recorder, httpReq)

	assert.Equal(t, http.StatusOK, recorder.Code)

	// Verify the trace context was propagated to the remote agent
	assert.NotEmpty(t, remoteReceivedTraceparent, "Remote agent should receive trace context")
	// The trace ID should be preserved across the chain
	assert.Contains(t, remoteReceivedTraceparent, "abcdef1234567890abcdef1234567890",
		"Trace ID should be preserved in the chain")
}

// contextCapturingMockAgent is a mock agent that captures the context for testing
type contextCapturingMockAgent struct {
	id        string
	onExecute func(ctx context.Context)
}

func (m *contextCapturingMockAgent) ID() string          { return m.id }
func (m *contextCapturingMockAgent) Name() string        { return m.id }
func (m *contextCapturingMockAgent) Description() string { return "Context capturing mock agent" }
func (m *contextCapturingMockAgent) Capabilities() []string { return []string{"test"} }
func (m *contextCapturingMockAgent) Dependencies() []agent.Agent { return nil }
func (m *contextCapturingMockAgent) Partition() string { return "default" }

func (m *contextCapturingMockAgent) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	if m.onExecute != nil {
		m.onExecute(ctx)
	}
	return &agent.AgentOutput{
		Result: "Mock executed",
	}, nil
}