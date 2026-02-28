package a2a

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/telemetry"
	"github.com/stretchr/testify/assert"
)

func TestNewA2ATelemetry(t *testing.T) {
	collector := telemetry.NewTelemetryCollector()
	tel := NewA2ATelemetry(collector)

	assert.NotNil(t, tel)
	assert.NotNil(t, tel.tracer)
	assert.NotNil(t, tel.meter)
	assert.NotNil(t, tel.logger)
	assert.NotNil(t, tel.requestCounter)
	assert.NotNil(t, tel.requestDuration)
	assert.NotNil(t, tel.activeConnections)
	assert.NotNil(t, tel.errorCounter)
}

func TestInstrumentedHTTPServer_RegisterAgent(t *testing.T) {
	collector := telemetry.NewTelemetryCollector()
	tel := NewA2ATelemetry(collector)
	registry := NewDiscoveryRegistry()
	server := NewInstrumentedHTTPServer(registry, tel)

	mockAgent := NewMockAgent("test-agent", false)
	card := &AgentCard{
		ID:          "test-agent",
		Name:        "Test Agent",
		Description: "A test agent",
		Endpoint:    "http://localhost:8080/a2a",
		Capabilities: []Capability{
			{Name: "test_capability", Description: "Test capability"},
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

func TestInstrumentedHTTPServer_HandleInvocation(t *testing.T) {
	collector := telemetry.NewTelemetryCollector()
	tel := NewA2ATelemetry(collector)
	registry := NewDiscoveryRegistry()
	server := NewInstrumentedHTTPServer(registry, tel)

	mockAgent := NewMockAgent("test-agent", false)
	card := &AgentCard{
		ID:          "test-agent",
		Name:        "Test Agent",
		Description: "A test agent",
		Endpoint:    "http://localhost:8080/a2a",
		Capabilities: []Capability{
			{Name: "test_capability", Description: "Test capability"},
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
		},
	}

	resp, err := server.HandleInvocation(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "2.0", resp.JSONRPC)
	assert.Nil(t, resp.Error)

	// Verify metrics were recorded
	metrics := tel.GetMetrics()
	assert.Greater(t, metrics.RequestsTotal, float64(0))
	assert.Greater(t, metrics.RequestDuration.Count, 0)
}

func TestInstrumentedHTTPClient_InvokeAgent(t *testing.T) {
	// Create a mock remote agent server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req InvocationRequest
		json.NewDecoder(r.Body).Decode(&req)

		resp := &InvocationResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{"message": "Hello from remote agent"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	collector := telemetry.NewTelemetryCollector()
	tel := NewA2ATelemetry(collector)
	registry := NewDiscoveryRegistry()

	// Register agent in registry
	card := &AgentCard{
		ID:          "remote-agent",
		Name:        "Remote Agent",
		Endpoint:    mockServer.URL,
		Capabilities: []Capability{
			{Name: "test", Description: "Test capability"},
		},
	}
	registry.Register(card)

	client := NewInstrumentedHTTPClient(registry, tel)

	input := &agent.AgentInput{
		Instruction: "Test instruction",
		TaskID:      "task-123",
	}

	output, err := client.InvokeAgent(context.Background(), "remote-agent", input)
	assert.NoError(t, err)
	assert.NotNil(t, output)

	// Verify metrics
	metrics := tel.GetMetrics()
	assert.Greater(t, metrics.RequestsTotal, float64(0))
}

func TestInstrumentedHTTPClient_DiscoverAgents(t *testing.T) {
	collector := telemetry.NewTelemetryCollector()
	tel := NewA2ATelemetry(collector)
	registry := NewDiscoveryRegistry()

	// Register some agents
	cards := []*AgentCard{
		{ID: "agent-1", Name: "Agent 1", Capabilities: []Capability{{Name: "search"}}},
		{ID: "agent-2", Name: "Agent 2", Capabilities: []Capability{{Name: "search"}}},
		{ID: "agent-3", Name: "Agent 3", Capabilities: []Capability{{Name: "compute"}}},
	}
	for _, card := range cards {
		registry.Register(card)
	}

	client := NewInstrumentedHTTPClient(registry, tel)

	agents, err := client.DiscoverAgents(context.Background(), "search")
	assert.NoError(t, err)
	assert.Len(t, agents, 2)
}

func TestInstrumentedRemoteAgentWrapper(t *testing.T) {
	// Create a mock remote agent server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req InvocationRequest
		json.NewDecoder(r.Body).Decode(&req)

		resp := &InvocationResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]interface{}{"message": "Executed"},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer mockServer.Close()

	collector := telemetry.NewTelemetryCollector()
	tel := NewA2ATelemetry(collector)

	info := &AgentInfo{
		ID:       "remote-agent",
		Name:     "Remote Agent",
		Endpoint: mockServer.URL,
	}

	wrapper := NewInstrumentedRemoteAgentWrapper(info, tel)

	input := &agent.AgentInput{
		Instruction: "Test instruction",
		TaskID:      "task-456",
	}

	output, err := wrapper.Execute(context.Background(), input)
	assert.NoError(t, err)
	assert.NotNil(t, output)
	assert.Equal(t, "task-456", output.TaskID)

	// Verify metrics
	metrics := tel.GetMetrics()
	assert.Greater(t, metrics.RequestsTotal, float64(0))
	assert.Greater(t, metrics.RequestDuration.Count, 0)
}

func TestInstrumentHTTPHandler(t *testing.T) {
	collector := telemetry.NewTelemetryCollector()
	tel := NewA2ATelemetry(collector)

	// Create a simple handler
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Wrap with instrumentation
	instrumentedHandler := InstrumentHTTPHandler(handler, tel)

	// Create test request
	req := httptest.NewRequest("GET", "/test", nil)
	recorder := httptest.NewRecorder()

	instrumentedHandler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "OK", recorder.Body.String())

	// Verify metrics were recorded
	metrics := tel.GetMetrics()
	assert.Greater(t, metrics.RequestsTotal, float64(0))
	assert.Greater(t, metrics.RequestDuration.Count, 0)
}

func TestInstrumentHTTPHandler_Error(t *testing.T) {
	collector := telemetry.NewTelemetryCollector()
	tel := NewA2ATelemetry(collector)

	// Create a handler that returns an error
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	})

	instrumentedHandler := InstrumentHTTPHandler(handler, tel)

	req := httptest.NewRequest("GET", "/test", nil)
	recorder := httptest.NewRecorder()

	instrumentedHandler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusInternalServerError, recorder.Code)

	// Verify error counter incremented
	metrics := tel.GetMetrics()
	assert.Greater(t, metrics.ErrorsTotal, float64(0))
}

func TestA2AMetrics(t *testing.T) {
	collector := telemetry.NewTelemetryCollector()
	tel := NewA2ATelemetry(collector)

	// Initial metrics should be zero
	metrics := tel.GetMetrics()
	assert.Equal(t, float64(0), metrics.RequestsTotal)
	assert.Equal(t, float64(0), metrics.ErrorsTotal)
	assert.Equal(t, float64(0), metrics.ActiveConnections)
	assert.Equal(t, 0, metrics.RequestDuration.Count)
}
