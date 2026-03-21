package agents

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/seyi/dagens/pkg/a2a"
	"github.com/seyi/dagens/pkg/agent"
)

// RemoteAgent wraps a remote A2A agent for easy invocation
// Provides ADK-style remote agent access over our A2A protocol
type RemoteAgent struct {
	*agent.BaseAgent
	agentCard   *a2a.AgentCard
	client      a2a.A2AClient
	endpoint    string
	agentID     string
	timeout     time.Duration
	retryCount  int
	cacheCard   bool
	cardRefresh time.Duration
}

// RemoteAgentConfig configures a remote agent
type RemoteAgentConfig struct {
	// Agent identification
	Name     string
	AgentID  string   // Remote agent ID
	Endpoint string   // Agent endpoint URL
	Card     *a2a.AgentCard // Optional pre-fetched agent card

	// Connection settings
	Timeout     time.Duration
	RetryCount  int
	HTTPClient  *http.Client

	// Caching
	CacheCard   bool          // Cache agent card
	CardRefresh time.Duration // How often to refresh card

	// Dependencies
	Dependencies []agent.Agent
}

// NewRemoteAgent creates a new remote agent wrapper
func NewRemoteAgent(config RemoteAgentConfig) (*RemoteAgent, error) {
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.RetryCount == 0 {
		config.RetryCount = 3
	}
	if config.CardRefresh == 0 {
		config.CardRefresh = 5 * time.Minute
	}

	// Create A2A client using discovery registry
	var client a2a.A2AClient
	if config.Endpoint != "" {
		// Create a registry and register the remote agent
		registry := a2a.NewDiscoveryRegistry()
		if config.Card != nil {
			registry.Register(config.Card)
		}
		client = a2a.NewHTTPA2AClient(registry)
	}

	remoteAgent := &RemoteAgent{
		agentCard:   config.Card,
		client:      client,
		endpoint:    config.Endpoint,
		agentID:     config.AgentID,
		timeout:     config.Timeout,
		retryCount:  config.RetryCount,
		cacheCard:   config.CacheCard,
		cardRefresh: config.CardRefresh,
	}

	// Create executor
	executor := &remoteExecutor{
		remoteAgent: remoteAgent,
	}

	// Create base agent
	baseAgent := agent.NewAgent(agent.AgentConfig{
		Name:         config.Name,
		Executor:     executor,
		Dependencies: config.Dependencies,
	})

	remoteAgent.BaseAgent = baseAgent
	return remoteAgent, nil
}

// remoteExecutor implements AgentExecutor for remote invocation
type remoteExecutor struct {
	remoteAgent *RemoteAgent
}

func (e *remoteExecutor) Execute(ctx context.Context, _ agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	startTime := time.Now()

	// Ensure we have agent card
	if err := e.ensureAgentCard(ctx); err != nil {
		return nil, fmt.Errorf("failed to get agent card: %w", err)
	}

	// Invoke remote agent with retry
	var output *agent.AgentOutput
	var err error

	for attempt := 0; attempt <= e.remoteAgent.retryCount; attempt++ {
		if attempt > 0 {
			// Exponential backoff
			backoff := time.Duration(attempt*attempt) * time.Second
			time.Sleep(backoff)
		}

		output, err = e.invokeRemoteAgent(ctx, input)
		if err == nil {
			break
		}

		// Check if error is retryable
		if !isRetryableError(err) {
			break
		}
	}

	if err != nil {
		return nil, fmt.Errorf("remote agent invocation failed after %d attempts: %w",
			e.remoteAgent.retryCount+1, err)
	}

	// Add remote agent metadata
	if output.Metadata == nil {
		output.Metadata = make(map[string]interface{})
	}
	output.Metadata["remote_agent_id"] = e.remoteAgent.agentID
	output.Metadata["remote_endpoint"] = e.remoteAgent.endpoint
	output.Metadata["total_time"] = time.Since(startTime).Seconds()

	return output, nil
}

// ensureAgentCard fetches agent card if not cached
func (e *remoteExecutor) ensureAgentCard(ctx context.Context) error {
	if e.remoteAgent.agentCard != nil && e.remoteAgent.cacheCard {
		return nil
	}

	if e.remoteAgent.client == nil {
		return fmt.Errorf("no A2A client configured")
	}

	// Fetch agent card
	card, err := e.remoteAgent.client.GetAgentCard(ctx, e.remoteAgent.agentID)
	if err != nil {
		return fmt.Errorf("failed to fetch agent card: %w", err)
	}

	e.remoteAgent.agentCard = card
	return nil
}

// invokeRemoteAgent performs the actual remote invocation
func (e *remoteExecutor) invokeRemoteAgent(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// Create timeout context
	ctx, cancel := context.WithTimeout(ctx, e.remoteAgent.timeout)
	defer cancel()

	// Invoke remote agent
	output, err := e.remoteAgent.client.InvokeAgent(ctx, e.remoteAgent.agentID, input)
	if err != nil {
		return nil, fmt.Errorf("remote invocation failed: %w", err)
	}

	return output, nil
}

// isRetryableError determines if error should be retried
func isRetryableError(err error) bool {
	// Network errors, timeouts, and 5xx status codes are retryable
	// 4xx client errors are not retryable
	errStr := err.Error()

	// Timeout errors
	if err == context.DeadlineExceeded || err == context.Canceled {
		return true
	}

	// Network errors
	if contains(errStr, "connection refused") ||
		contains(errStr, "connection reset") ||
		contains(errStr, "temporary failure") ||
		contains(errStr, "timeout") {
		return true
	}

	// Server errors (5xx)
	if contains(errStr, "500") ||
		contains(errStr, "502") ||
		contains(errStr, "503") ||
		contains(errStr, "504") {
		return true
	}

	return false
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// GetAgentCard returns the cached or fetched agent card
func (r *RemoteAgent) GetAgentCard(ctx context.Context) (*a2a.AgentCard, error) {
	if r.agentCard == nil {
		if r.client == nil {
			return nil, fmt.Errorf("no A2A client configured")
		}

		card, err := r.client.GetAgentCard(ctx, r.agentID)
		if err != nil {
			return nil, err
		}

		r.agentCard = card
	}

	return r.agentCard, nil
}

// GetCapabilities returns remote agent capabilities
func (r *RemoteAgent) GetCapabilities(ctx context.Context) ([]a2a.Capability, error) {
	card, err := r.GetAgentCard(ctx)
	if err != nil {
		return nil, err
	}

	return card.Capabilities, nil
}

// SupportsCapability checks if remote agent supports a capability
func (r *RemoteAgent) SupportsCapability(ctx context.Context, capability string) (bool, error) {
	capabilities, err := r.GetCapabilities(ctx)
	if err != nil {
		return false, err
	}

	for _, cap := range capabilities {
		if cap.Name == capability {
			return true, nil
		}
	}

	return false, nil
}

// RemoteAgentBuilder provides fluent API for building remote agents
type RemoteAgentBuilder struct {
	name         string
	agentID      string
	endpoint     string
	card         *a2a.AgentCard
	timeout      time.Duration
	retryCount   int
	httpClient   *http.Client
	cacheCard    bool
	cardRefresh  time.Duration
	dependencies []agent.Agent
}

// NewRemote creates a new remote agent builder
func NewRemote(agentID, endpoint string) *RemoteAgentBuilder {
	return &RemoteAgentBuilder{
		agentID:     agentID,
		endpoint:    endpoint,
		timeout:     30 * time.Second,
		retryCount:  3,
		cacheCard:   true,
		cardRefresh: 5 * time.Minute,
	}
}

// WithName sets the agent name
func (b *RemoteAgentBuilder) WithName(name string) *RemoteAgentBuilder {
	b.name = name
	return b
}

// WithAgentCard sets a pre-fetched agent card
func (b *RemoteAgentBuilder) WithAgentCard(card *a2a.AgentCard) *RemoteAgentBuilder {
	b.card = card
	return b
}

// WithTimeout sets the invocation timeout
func (b *RemoteAgentBuilder) WithTimeout(timeout time.Duration) *RemoteAgentBuilder {
	b.timeout = timeout
	return b
}

// WithRetryCount sets the number of retry attempts
func (b *RemoteAgentBuilder) WithRetryCount(count int) *RemoteAgentBuilder {
	b.retryCount = count
	return b
}

// WithHTTPClient sets a custom HTTP client
func (b *RemoteAgentBuilder) WithHTTPClient(client *http.Client) *RemoteAgentBuilder {
	b.httpClient = client
	return b
}

// WithCaching configures agent card caching
func (b *RemoteAgentBuilder) WithCaching(cache bool, refresh time.Duration) *RemoteAgentBuilder {
	b.cacheCard = cache
	b.cardRefresh = refresh
	return b
}

// WithDependencies adds dependencies
func (b *RemoteAgentBuilder) WithDependencies(deps ...agent.Agent) *RemoteAgentBuilder {
	b.dependencies = append(b.dependencies, deps...)
	return b
}

// Build creates the remote agent
func (b *RemoteAgentBuilder) Build() (*RemoteAgent, error) {
	if b.name == "" {
		b.name = fmt.Sprintf("remote-%s", b.agentID)
	}

	return NewRemoteAgent(RemoteAgentConfig{
		Name:         b.name,
		AgentID:      b.agentID,
		Endpoint:     b.endpoint,
		Card:         b.card,
		Timeout:      b.timeout,
		RetryCount:   b.retryCount,
		HTTPClient:   b.httpClient,
		CacheCard:    b.cacheCard,
		CardRefresh:  b.cardRefresh,
		Dependencies: b.dependencies,
	})
}

// StreamingRemoteAgent extends RemoteAgent with streaming capabilities
type StreamingRemoteAgent struct {
	*RemoteAgent
	bufferSize int
}

// StreamingRemoteAgentConfig extends RemoteAgentConfig
type StreamingRemoteAgentConfig struct {
	RemoteAgentConfig
	BufferSize int // Channel buffer size for streaming
}

// NewStreamingRemoteAgent creates a streaming remote agent
func NewStreamingRemoteAgent(config StreamingRemoteAgentConfig) (*StreamingRemoteAgent, error) {
	if config.BufferSize == 0 {
		config.BufferSize = 10
	}

	baseAgent, err := NewRemoteAgent(config.RemoteAgentConfig)
	if err != nil {
		return nil, err
	}

	return &StreamingRemoteAgent{
		RemoteAgent: baseAgent,
		bufferSize:  config.BufferSize,
	}, nil
}

// StreamExecute executes with streaming results
func (s *StreamingRemoteAgent) StreamExecute(ctx context.Context, input *agent.AgentInput) (<-chan *a2a.StreamChunk, error) {
	if s.client == nil {
		return nil, fmt.Errorf("no A2A client configured")
	}

	// Ensure we have agent card
	card, err := s.GetAgentCard(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get agent card: %w", err)
	}

	// Check if agent supports streaming
	supportsStreaming := false
	for _, pattern := range card.SupportedPatterns {
		if pattern == a2a.PatternSSE {
			supportsStreaming = true
			break
		}
	}

	if !supportsStreaming {
		return nil, fmt.Errorf("remote agent does not support streaming")
	}

	// Start streaming invocation
	return s.client.StreamInvocation(ctx, s.agentID, input)
}

// ConsumeStream consumes a stream and returns final result
func (s *StreamingRemoteAgent) ConsumeStream(ctx context.Context, stream <-chan *a2a.StreamChunk) (*agent.AgentOutput, error) {
	var lastResult string
	var chunks []string
	startTime := time.Now()

	for {
		select {
		case chunk, ok := <-stream:
			if !ok {
				// Stream closed
				return &agent.AgentOutput{
					Result: lastResult,
					Metadata: map[string]interface{}{
						"streaming":       true,
						"chunk_count":     len(chunks),
						"execution_time":  time.Since(startTime).Seconds(),
						"remote_agent_id": s.agentID,
						"completed_at":    time.Now(),
					},
				}, nil
			}

			// Process chunk data
			if chunk.Data != nil {
				if content, ok := chunk.Data.(string); ok {
					chunks = append(chunks, content)
					lastResult += content
				}
			}

		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// StreamingCallback is called for each streaming chunk
type StreamingCallback func(chunk *a2a.StreamChunk) error

// StreamWithCallback streams results and calls callback for each chunk
func (s *StreamingRemoteAgent) StreamWithCallback(ctx context.Context, input *agent.AgentInput, callback StreamingCallback) error {
	stream, err := s.StreamExecute(ctx, input)
	if err != nil {
		return err
	}

	for {
		select {
		case chunk, ok := <-stream:
			if !ok {
				return nil
			}

			// Check for final chunk
			if chunk.Final {
				if err := callback(chunk); err != nil {
					return fmt.Errorf("callback error: %w", err)
				}
				return nil
			}

			if err := callback(chunk); err != nil {
				return fmt.Errorf("callback error: %w", err)
			}

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// RemoteAgentPool manages a pool of remote agents for load balancing
type RemoteAgentPool struct {
	agents  []*RemoteAgent
	current int
}

// NewRemoteAgentPool creates a pool of remote agents
func NewRemoteAgentPool(agents ...*RemoteAgent) *RemoteAgentPool {
	return &RemoteAgentPool{
		agents: agents,
	}
}

// Execute round-robins across agents
func (p *RemoteAgentPool) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	if len(p.agents) == 0 {
		return nil, fmt.Errorf("no agents in pool")
	}

	// Simple round-robin
	agentIdx := p.current % len(p.agents)
	p.current++

	return p.agents[agentIdx].Execute(ctx, input)
}

// ExecuteWithFallback tries agents in order until success
func (p *RemoteAgentPool) ExecuteWithFallback(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	if len(p.agents) == 0 {
		return nil, fmt.Errorf("no agents in pool")
	}

	var lastErr error
	for _, agent := range p.agents {
		output, err := agent.Execute(ctx, input)
		if err == nil {
			return output, nil
		}
		lastErr = err
	}

	return nil, fmt.Errorf("all agents failed, last error: %w", lastErr)
}

// RemoteAgentDiscovery discovers and creates remote agents
type RemoteAgentDiscovery struct {
	registry a2a.DiscoveryRegistry
}

// NewRemoteAgentDiscovery creates a discovery client
func NewRemoteAgentDiscovery(registry a2a.DiscoveryRegistry) *RemoteAgentDiscovery {
	return &RemoteAgentDiscovery{
		registry: registry,
	}
}

// DiscoverByCapability finds remote agents with specific capability
func (d *RemoteAgentDiscovery) DiscoverByCapability(capability string) ([]*RemoteAgent, error) {
	agentInfos := d.registry.FindByCapability(capability)

	agents := make([]*RemoteAgent, 0, len(agentInfos))
	for _, info := range agentInfos {
		agent, err := NewRemoteAgent(RemoteAgentConfig{
			Name:     info.Name,
			AgentID:  info.ID,
			Endpoint: info.Endpoint,
		})
		if err != nil {
			continue
		}
		agents = append(agents, agent)
	}

	return agents, nil
}

// DiscoverAll returns all registered remote agents
func (d *RemoteAgentDiscovery) DiscoverAll() ([]*RemoteAgent, error) {
	agentInfos := d.registry.ListAll()

	agents := make([]*RemoteAgent, 0, len(agentInfos))
	for _, info := range agentInfos {
		agent, err := NewRemoteAgent(RemoteAgentConfig{
			Name:     info.Name,
			AgentID:  info.ID,
			Endpoint: info.Endpoint,
		})
		if err != nil {
			continue
		}
		agents = append(agents, agent)
	}

	return agents, nil
}

// Helper to check if writer is a flusher
type flusher interface {
	io.Writer
	Flush() error
}

// StreamToWriter writes streaming chunks to an io.Writer
func StreamToWriter(ctx context.Context, stream <-chan *a2a.StreamChunk, w io.Writer) error {
	fl, canFlush := w.(flusher)

	for {
		select {
		case chunk, ok := <-stream:
			if !ok {
				return nil
			}

			// Process chunk data as content
			if chunk.Data != nil {
				if content, ok := chunk.Data.(string); ok && content != "" {
					_, err := w.Write([]byte(content))
					if err != nil {
						return fmt.Errorf("write error: %w", err)
					}

					if canFlush {
						if err := fl.Flush(); err != nil {
							return fmt.Errorf("flush error: %w", err)
						}
					}
				}
			}

			// Check if final chunk
			if chunk.Final {
				return nil
			}

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
