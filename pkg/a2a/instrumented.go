// Package a2a provides instrumented A2A server and client with OpenTelemetry support
package a2a

import (
	"context"
	"net/http"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/telemetry"
)

// A2ATelemetry provides telemetry components for A2A operations
type A2ATelemetry struct {
	tracer  telemetry.Tracer
	meter   telemetry.Meter
	logger  telemetry.Logger

	// Metrics
	requestCounter    telemetry.Counter
	requestDuration   telemetry.Histogram
	activeConnections telemetry.Gauge
	errorCounter      telemetry.Counter
}

// NewA2ATelemetry creates telemetry for A2A operations
func NewA2ATelemetry(collector *telemetry.TelemetryCollector) *A2ATelemetry {
	meter := collector.GetMeter()

	return &A2ATelemetry{
		tracer:            collector.GetTracer(),
		meter:             meter,
		logger:            collector.GetLogger(),
		requestCounter:    meter.Counter("a2a.requests.total"),
		requestDuration:   meter.Histogram("a2a.request.duration_ms"),
		activeConnections: meter.Gauge("a2a.connections.active"),
		errorCounter:      meter.Counter("a2a.errors.total"),
	}
}

// InstrumentedHTTPServer wraps HTTPServer with telemetry
type InstrumentedHTTPServer struct {
	*HTTPServer
	telemetry *A2ATelemetry
}

// NewInstrumentedHTTPServer creates a new instrumented A2A HTTP server
func NewInstrumentedHTTPServer(registry *DiscoveryRegistry, tel *A2ATelemetry) *InstrumentedHTTPServer {
	server := NewHTTPServer(registry)
	return &InstrumentedHTTPServer{
		HTTPServer: server,
		telemetry:  tel,
	}
}

// RegisterAgent registers an agent with telemetry
func (s *InstrumentedHTTPServer) RegisterAgent(agent agent.Agent, card *AgentCard) error {
	_, span := s.telemetry.tracer.StartSpan(context.Background(), "a2a.server.register_agent")
	defer span.End()

	span.SetAttribute("agent.id", agent.ID())
	span.SetAttribute("agent.name", agent.Name())
	span.SetAttribute("agent.endpoint", card.Endpoint)

	s.telemetry.logger.Info("Registering agent", map[string]interface{}{
		"agent_id":   agent.ID(),
		"agent_name": agent.Name(),
		"endpoint":   card.Endpoint,
		"trace_id":   span.TraceID(),
	})

	err := s.HTTPServer.RegisterAgent(agent, card)
	if err != nil {
		span.SetStatus(telemetry.StatusError, err.Error())
		s.telemetry.errorCounter.Inc()
		s.telemetry.logger.Error("Failed to register agent", map[string]interface{}{
			"agent_id": agent.ID(),
			"error":    err.Error(),
		})
		return err
	}

	span.SetStatus(telemetry.StatusOK, "Agent registered")
	return nil
}

// HandleInvocation processes an invocation with telemetry
func (s *InstrumentedHTTPServer) HandleInvocation(ctx context.Context, req *InvocationRequest) (*InvocationResponse, error) {
	ctx, span := s.telemetry.tracer.StartSpan(ctx, "a2a.server.handle_invocation")
	defer span.End()

	startTime := time.Now()

	span.SetAttribute("rpc.id", req.ID)
	span.SetAttribute("rpc.method", req.Method)

	s.telemetry.requestCounter.Inc()
	s.telemetry.activeConnections.Inc()
	defer s.telemetry.activeConnections.Dec()

	span.AddEvent("invocation_started", map[string]interface{}{
		"method": req.Method,
		"params": req.Params,
	})

	resp, err := s.HTTPServer.HandleInvocation(ctx, req)

	duration := time.Since(startTime).Milliseconds()
	s.telemetry.requestDuration.Record(float64(duration))

	span.SetAttribute("duration_ms", duration)

	if err != nil {
		span.SetStatus(telemetry.StatusError, err.Error())
		s.telemetry.errorCounter.Inc()
		s.telemetry.logger.Error("Invocation failed", map[string]interface{}{
			"rpc_id":      req.ID,
			"method":      req.Method,
			"error":       err.Error(),
			"duration_ms": duration,
		})
		return resp, err
	}

	if resp.Error != nil {
		span.SetStatus(telemetry.StatusError, resp.Error.Message)
		span.SetAttribute("rpc.error.code", resp.Error.Code)
		s.telemetry.errorCounter.Inc()
	} else {
		span.SetStatus(telemetry.StatusOK, "Invocation completed")
	}

	s.telemetry.logger.Info("Invocation completed", map[string]interface{}{
		"rpc_id":      req.ID,
		"method":      req.Method,
		"duration_ms": duration,
		"success":     resp.Error == nil,
		"trace_id":    span.TraceID(),
	})

	return resp, nil
}

// InstrumentedHTTPClient wraps HTTPClient with telemetry
type InstrumentedHTTPClient struct {
	*HTTPClient
	telemetry *A2ATelemetry
}

// NewInstrumentedHTTPClient creates a new instrumented A2A HTTP client
func NewInstrumentedHTTPClient(registry *DiscoveryRegistry, tel *A2ATelemetry) *InstrumentedHTTPClient {
	client := NewHTTPA2AClient(registry)
	return &InstrumentedHTTPClient{
		HTTPClient: client,
		telemetry:  tel,
	}
}

// InvokeAgent calls a remote agent with telemetry
func (c *InstrumentedHTTPClient) InvokeAgent(ctx context.Context, agentID string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	ctx, span := c.telemetry.tracer.StartSpan(ctx, "a2a.client.invoke_agent")
	defer span.End()

	startTime := time.Now()

	span.SetAttribute("target.agent_id", agentID)
	span.SetAttribute("input.instruction", input.Instruction)
	span.SetAttribute("input.task_id", input.TaskID)

	c.telemetry.requestCounter.Inc()
	c.telemetry.activeConnections.Inc()
	defer c.telemetry.activeConnections.Dec()

	span.AddEvent("client_request_started", map[string]interface{}{
		"agent_id":    agentID,
		"instruction": input.Instruction,
	})

	output, err := c.HTTPClient.InvokeAgent(ctx, agentID, input)

	duration := time.Since(startTime).Milliseconds()
	c.telemetry.requestDuration.Record(float64(duration))

	span.SetAttribute("duration_ms", duration)

	if err != nil {
		span.SetStatus(telemetry.StatusError, err.Error())
		c.telemetry.errorCounter.Inc()
		c.telemetry.logger.Error("Agent invocation failed", map[string]interface{}{
			"agent_id":    agentID,
			"error":       err.Error(),
			"duration_ms": duration,
		})
		return nil, err
	}

	span.SetStatus(telemetry.StatusOK, "Agent invocation completed")
	span.AddEvent("client_request_completed", map[string]interface{}{
		"agent_id": agentID,
		"has_result": output.Result != nil,
	})

	c.telemetry.logger.Info("Agent invocation completed", map[string]interface{}{
		"agent_id":    agentID,
		"task_id":     output.TaskID,
		"duration_ms": duration,
		"trace_id":    span.TraceID(),
	})

	return output, nil
}

// DiscoverAgents finds agents with telemetry
func (c *InstrumentedHTTPClient) DiscoverAgents(ctx context.Context, capability string) ([]*AgentInfo, error) {
	ctx, span := c.telemetry.tracer.StartSpan(ctx, "a2a.client.discover_agents")
	defer span.End()

	span.SetAttribute("capability", capability)

	c.telemetry.logger.Debug("Discovering agents", map[string]interface{}{
		"capability": capability,
	})

	agents, err := c.HTTPClient.DiscoverAgents(ctx, capability)

	if err != nil {
		span.SetStatus(telemetry.StatusError, err.Error())
		c.telemetry.errorCounter.Inc()
		return nil, err
	}

	span.SetAttribute("agents.found", len(agents))
	span.SetStatus(telemetry.StatusOK, "Discovery completed")

	c.telemetry.logger.Info("Agents discovered", map[string]interface{}{
		"capability": capability,
		"count":      len(agents),
	})

	return agents, nil
}

// GetAgentCard retrieves an agent's card with telemetry
func (c *InstrumentedHTTPClient) GetAgentCard(ctx context.Context, agentID string) (*AgentCard, error) {
	ctx, span := c.telemetry.tracer.StartSpan(ctx, "a2a.client.get_agent_card")
	defer span.End()

	span.SetAttribute("agent_id", agentID)

	card, err := c.HTTPClient.GetAgentCard(ctx, agentID)

	if err != nil {
		span.SetStatus(telemetry.StatusError, err.Error())
		c.telemetry.errorCounter.Inc()
		return nil, err
	}

	span.SetStatus(telemetry.StatusOK, "Card retrieved")
	span.SetAttribute("agent.name", card.Name)
	span.SetAttribute("agent.capabilities_count", len(card.Capabilities))

	return card, nil
}

// InstrumentedRemoteAgentWrapper wraps RemoteAgentWrapper with telemetry
type InstrumentedRemoteAgentWrapper struct {
	*RemoteAgentWrapper
	telemetry *A2ATelemetry
}

// NewInstrumentedRemoteAgentWrapper creates a wrapper with telemetry
func NewInstrumentedRemoteAgentWrapper(info *AgentInfo, tel *A2ATelemetry) *InstrumentedRemoteAgentWrapper {
	return &InstrumentedRemoteAgentWrapper{
		RemoteAgentWrapper: NewRemoteAgentWrapper(info),
		telemetry:          tel,
	}
}

// Execute invokes the remote agent with telemetry
func (r *InstrumentedRemoteAgentWrapper) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	ctx, span := r.telemetry.tracer.StartSpan(ctx, "a2a.remote_agent.execute")
	defer span.End()

	startTime := time.Now()

	span.SetAttribute("agent.id", r.id)
	span.SetAttribute("agent.name", r.name)
	span.SetAttribute("agent.endpoint", r.endpoint)
	span.SetAttribute("input.task_id", input.TaskID)

	r.telemetry.requestCounter.Inc()
	r.telemetry.activeConnections.Inc()
	defer r.telemetry.activeConnections.Dec()

	output, err := r.RemoteAgentWrapper.Execute(ctx, input)

	duration := time.Since(startTime).Milliseconds()
	r.telemetry.requestDuration.Record(float64(duration))

	span.SetAttribute("duration_ms", duration)

	if err != nil {
		span.SetStatus(telemetry.StatusError, err.Error())
		r.telemetry.errorCounter.Inc()
		r.telemetry.logger.Error("Remote agent execution failed", map[string]interface{}{
			"agent_id":    r.id,
			"endpoint":    r.endpoint,
			"error":       err.Error(),
			"duration_ms": duration,
		})
		return nil, err
	}

	span.SetStatus(telemetry.StatusOK, "Execution completed")
	r.telemetry.logger.Info("Remote agent execution completed", map[string]interface{}{
		"agent_id":    r.id,
		"task_id":     output.TaskID,
		"duration_ms": duration,
		"trace_id":    span.TraceID(),
	})

	return output, nil
}

// InstrumentHTTPHandler wraps an http.Handler with request tracing
func InstrumentHTTPHandler(handler http.Handler, tel *A2ATelemetry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := tel.tracer.StartSpan(r.Context(), "a2a.http.request")
		defer span.End()

		startTime := time.Now()

		span.SetAttribute("http.method", r.Method)
		span.SetAttribute("http.url", r.URL.Path)
		span.SetAttribute("http.host", r.Host)

		tel.requestCounter.Inc()
		tel.activeConnections.Inc()
		defer tel.activeConnections.Dec()

		// Wrap response writer to capture status code
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		handler.ServeHTTP(wrapped, r.WithContext(ctx))

		duration := time.Since(startTime).Milliseconds()
		tel.requestDuration.RecordWithAttributes(float64(duration), map[string]interface{}{
			"method": r.Method,
			"path":   r.URL.Path,
			"status": wrapped.statusCode,
		})

		span.SetAttribute("http.status_code", wrapped.statusCode)
		span.SetAttribute("duration_ms", duration)

		if wrapped.statusCode >= 400 {
			span.SetStatus(telemetry.StatusError, "HTTP error")
			tel.errorCounter.Inc()
		} else {
			span.SetStatus(telemetry.StatusOK, "Request completed")
		}
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// A2AMetrics provides structured access to A2A metrics
type A2AMetrics struct {
	RequestsTotal     float64 `json:"requests_total"`
	ErrorsTotal       float64 `json:"errors_total"`
	ActiveConnections float64 `json:"active_connections"`
	RequestDuration   struct {
		Count int     `json:"count"`
		Sum   float64 `json:"sum_ms"`
		Min   float64 `json:"min_ms"`
		Max   float64 `json:"max_ms"`
		Mean  float64 `json:"mean_ms"`
	} `json:"request_duration"`
}

// GetMetrics returns current A2A metrics
func (t *A2ATelemetry) GetMetrics() *A2AMetrics {
	metrics := &A2AMetrics{}

	if counter, ok := t.requestCounter.(*telemetry.InMemoryCounter); ok {
		metrics.RequestsTotal = counter.Value()
	}
	if counter, ok := t.errorCounter.(*telemetry.InMemoryCounter); ok {
		metrics.ErrorsTotal = counter.Value()
	}
	if gauge, ok := t.activeConnections.(*telemetry.InMemoryGauge); ok {
		metrics.ActiveConnections = gauge.Value()
	}
	if histogram, ok := t.requestDuration.(*telemetry.InMemoryHistogram); ok {
		stats := histogram.Statistics()
		metrics.RequestDuration.Count = stats.Count
		metrics.RequestDuration.Sum = stats.Sum
		metrics.RequestDuration.Min = stats.Min
		metrics.RequestDuration.Max = stats.Max
		metrics.RequestDuration.Mean = stats.Mean
	}

	return metrics
}
