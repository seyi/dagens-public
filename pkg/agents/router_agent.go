package agents

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/observability"
	"github.com/seyi/dagens/pkg/resilience"
)

// RouterAgent dynamically routes inputs to different agents based on routing logic
// Enables intent-based routing, load balancing, and A/B testing
type RouterAgent struct {
	*agent.BaseAgent
	routes       []Route
	defaultAgent agent.Agent
	fallback     FallbackMode
	timeout      time.Duration
}

// Route defines a routing rule
type Route struct {
	Name      string
	Condition RouteCondition
	Agent     agent.Agent
	Priority  int // Higher priority routes checked first
}

// RouteCondition determines if a route should be taken
type RouteCondition func(input *agent.AgentInput) bool

// FallbackMode defines behavior when no route matches
type FallbackMode string

const (
	// FallbackError returns error if no route matches
	FallbackError FallbackMode = "error"

	// FallbackDefault uses default agent
	FallbackDefault FallbackMode = "default"

	// FallbackFirst uses first agent in routes
	FallbackFirst FallbackMode = "first"
)

// RouterAgentConfig configures a router agent
type RouterAgentConfig struct {
	Name         string
	Routes       []Route
	DefaultAgent agent.Agent
	Fallback     FallbackMode
	Timeout      time.Duration
	Dependencies []agent.Agent
}

// NewRouterAgent creates a new router agent
func NewRouterAgent(config RouterAgentConfig) *RouterAgent {
	if config.Fallback == "" {
		config.Fallback = FallbackError
	}
	if config.Timeout == 0 {
		config.Timeout = 5 * time.Minute
	}

	// Sort routes by priority (descending)
	routes := make([]Route, len(config.Routes))
	copy(routes, config.Routes)
	for i := 0; i < len(routes)-1; i++ {
		for j := i + 1; j < len(routes); j++ {
			if routes[j].Priority > routes[i].Priority {
				routes[i], routes[j] = routes[j], routes[i]
			}
		}
	}

	routerAgent := &RouterAgent{
		routes:       routes,
		defaultAgent: config.DefaultAgent,
		fallback:     config.Fallback,
		timeout:      config.Timeout,
	}

	executor := &routerExecutor{
		routerAgent: routerAgent,
	}

	baseAgent := agent.NewAgent(agent.AgentConfig{
		Name:         config.Name,
		Executor:     executor,
		Dependencies: config.Dependencies,
	})

	routerAgent.BaseAgent = baseAgent
	return routerAgent
}

// routerExecutor implements AgentExecutor for routing
type routerExecutor struct {
	routerAgent *RouterAgent
}

func (e *routerExecutor) Execute(ctx context.Context, _ agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	startTime := time.Now()

	// Create timeout context
	ctx, cancel := context.WithTimeout(ctx, e.routerAgent.timeout)
	defer cancel()

	// Find matching route
	var selectedRoute *Route
	for i := range e.routerAgent.routes {
		route := &e.routerAgent.routes[i]
		if route.Condition(input) {
			selectedRoute = route
			break
		}
	}

	// Handle no match
	if selectedRoute == nil {
		return e.handleNoMatch(ctx, input)
	}

	// Execute selected agent
	output, err := selectedRoute.Agent.Execute(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("route '%s' failed: %w", selectedRoute.Name, err)
	}

	// Add routing metadata
	if output.Metadata == nil {
		output.Metadata = make(map[string]interface{})
	}
	output.Metadata["pattern"] = "Router"
	output.Metadata["route_name"] = selectedRoute.Name
	output.Metadata["route_priority"] = selectedRoute.Priority
	output.Metadata["routing_time"] = time.Since(startTime).Seconds()

	return output, nil
}

// handleNoMatch handles case when no route matches
func (e *routerExecutor) handleNoMatch(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	switch e.routerAgent.fallback {
	case FallbackDefault:
		if e.routerAgent.defaultAgent == nil {
			return nil, fmt.Errorf("no route matched and no default agent configured")
		}
		return e.routerAgent.defaultAgent.Execute(ctx, input)

	case FallbackFirst:
		if len(e.routerAgent.routes) == 0 {
			return nil, fmt.Errorf("no routes configured")
		}
		return e.routerAgent.routes[0].Agent.Execute(ctx, input)

	case FallbackError:
		fallthrough
	default:
		return nil, fmt.Errorf("no route matched input")
	}
}

// RouterBuilder provides fluent API for building router agents
type RouterBuilder struct {
	name         string
	routes       []Route
	defaultAgent agent.Agent
	fallback     FallbackMode
	timeout      time.Duration
	dependencies []agent.Agent
}

// NewRouter creates a new router agent builder
func NewRouter() *RouterBuilder {
	return &RouterBuilder{
		routes:   []Route{},
		fallback: FallbackError,
		timeout:  5 * time.Minute,
	}
}

// WithName sets the agent name
func (b *RouterBuilder) WithName(name string) *RouterBuilder {
	b.name = name
	return b
}

// AddRoute adds a routing rule
func (b *RouterBuilder) AddRoute(name string, condition RouteCondition, agent agent.Agent) *RouterBuilder {
	b.routes = append(b.routes, Route{
		Name:      name,
		Condition: condition,
		Agent:     agent,
		Priority:  0,
	})
	return b
}

// AddRouteWithPriority adds a routing rule with priority
func (b *RouterBuilder) AddRouteWithPriority(name string, condition RouteCondition, agent agent.Agent, priority int) *RouterBuilder {
	b.routes = append(b.routes, Route{
		Name:      name,
		Condition: condition,
		Agent:     agent,
		Priority:  priority,
	})
	return b
}

// WithDefault sets the default agent
func (b *RouterBuilder) WithDefault(agent agent.Agent) *RouterBuilder {
	b.defaultAgent = agent
	b.fallback = FallbackDefault
	return b
}

// WithFallback sets the fallback mode
func (b *RouterBuilder) WithFallback(mode FallbackMode) *RouterBuilder {
	b.fallback = mode
	return b
}

// WithTimeout sets execution timeout
func (b *RouterBuilder) WithTimeout(timeout time.Duration) *RouterBuilder {
	b.timeout = timeout
	return b
}

// WithDependencies adds dependencies
func (b *RouterBuilder) WithDependencies(deps ...agent.Agent) *RouterBuilder {
	b.dependencies = append(b.dependencies, deps...)
	return b
}

// Build creates the router agent
func (b *RouterBuilder) Build() *RouterAgent {
	if b.name == "" {
		b.name = "router-agent"
	}

	return NewRouterAgent(RouterAgentConfig{
		Name:         b.name,
		Routes:       b.routes,
		DefaultAgent: b.defaultAgent,
		Fallback:     b.fallback,
		Timeout:      b.timeout,
		Dependencies: b.dependencies,
	})
}

// Common routing conditions

// KeywordCondition routes based on keywords in instruction
func KeywordCondition(keywords ...string) RouteCondition {
	return func(input *agent.AgentInput) bool {
		instruction := input.Instruction
		for _, keyword := range keywords {
			if contains(instruction, keyword) {
				return true
			}
		}
		return false
	}
}

// ContextKeyCondition routes based on context key value
func ContextKeyCondition(key string, value interface{}) RouteCondition {
	return func(input *agent.AgentInput) bool {
		if input.Context == nil {
			return false
		}
		val, exists := input.Context[key]
		return exists && val == value
	}
}

// ContextExistsCondition routes if context key exists
func ContextExistsCondition(key string) RouteCondition {
	return func(input *agent.AgentInput) bool {
		if input.Context == nil {
			return false
		}
		_, exists := input.Context[key]
		return exists
	}
}

// PrefixCondition routes based on instruction prefix
func PrefixCondition(prefix string) RouteCondition {
	return func(input *agent.AgentInput) bool {
		return len(input.Instruction) >= len(prefix) &&
			input.Instruction[:len(prefix)] == prefix
	}
}

// AlwaysCondition always matches
func AlwaysCondition() RouteCondition {
	return func(input *agent.AgentInput) bool {
		return true
	}
}

// NeverCondition never matches
func NeverCondition() RouteCondition {
	return func(input *agent.AgentInput) bool {
		return false
	}
}

// AndCondition combines conditions with AND logic
func AndCondition(conditions ...RouteCondition) RouteCondition {
	return func(input *agent.AgentInput) bool {
		for _, condition := range conditions {
			if !condition(input) {
				return false
			}
		}
		return true
	}
}

// OrCondition combines conditions with OR logic
func OrCondition(conditions ...RouteCondition) RouteCondition {
	return func(input *agent.AgentInput) bool {
		for _, condition := range conditions {
			if condition(input) {
				return true
			}
		}
		return false
	}
}

// NotCondition inverts a condition
func NotCondition(condition RouteCondition) RouteCondition {
	return func(input *agent.AgentInput) bool {
		return !condition(input)
	}
}

// LoadBalancerAgent distributes requests across agents using various strategies
// Enhanced with circuit breakers, health monitoring, metrics, and proper concurrency
type LoadBalancerAgent struct {
	*agent.BaseAgent
	agents         []AgentWrapper
	strategy       LoadBalanceStrategy
	counter        atomic.Int64            // Thread-safe counter for round-robin
	randSource     *rand.Rand              // Random source for random strategy
	randMu         sync.Mutex              // Separate mutex for random source only
	mu             sync.RWMutex            // Protects agents slice
	healthRegistry *observability.HealthRegistry
	metrics        *observability.Metrics
}

// AgentWrapper wraps an agent with resilience patterns and metrics
type AgentWrapper struct {
	Agent          agent.Agent
	CircuitBreaker *resilience.CircuitBreaker
	InFlight       atomic.Int64 // Current in-flight requests (for LeastLoaded)
	Healthy        atomic.Bool  // Health status
}

// LoadBalanceStrategy defines load balancing method
type LoadBalanceStrategy string

const (
	// RoundRobin distributes requests in round-robin fashion
	RoundRobin LoadBalanceStrategy = "round-robin"

	// Random selects agent randomly
	Random LoadBalanceStrategy = "random"

	// LeastLoaded selects agent with least load (requires metrics)
	LeastLoaded LoadBalanceStrategy = "least-loaded"
)

// LoadBalancerAgentConfig configures a load balancer agent
type LoadBalancerAgentConfig struct {
	Name                    string
	Agents                  []agent.Agent
	Strategy                LoadBalanceStrategy
	Dependencies            []agent.Agent
	CircuitBreakerConfig    *resilience.CircuitBreakerConfig // Optional custom config
	EnableHealthChecks      bool                             // Enable per-agent health checks
	HealthCheckInterval     time.Duration                    // Interval for health checks
}

// NewLoadBalancerAgent creates a load balancer agent with resilience and observability
func NewLoadBalancerAgent(config LoadBalancerAgentConfig) *LoadBalancerAgent {
	if config.Strategy == "" {
		config.Strategy = RoundRobin
	}
	if config.HealthCheckInterval == 0 {
		config.HealthCheckInterval = 30 * time.Second
	}

	lbAgent := &LoadBalancerAgent{
		agents:         make([]AgentWrapper, 0, len(config.Agents)),
		strategy:       config.Strategy,
		randSource:     rand.New(rand.NewSource(time.Now().UnixNano())),
		healthRegistry: observability.GetHealthRegistry(),
		metrics:        observability.GetMetrics(),
	}

	// Wrap each agent with circuit breaker and health tracking
	for _, a := range config.Agents {
		cbConfig := resilience.DefaultCircuitBreakerConfig(a.Name())
		if config.CircuitBreakerConfig != nil {
			cbConfig = *config.CircuitBreakerConfig
			cbConfig.Name = a.Name()
		}

		wrapper := AgentWrapper{
			Agent:          a,
			CircuitBreaker: resilience.NewCircuitBreaker(cbConfig),
		}
		wrapper.Healthy.Store(true) // Assume healthy initially

		lbAgent.agents = append(lbAgent.agents, wrapper)

		// Register health check for this agent
		if config.EnableHealthChecks {
			agentName := a.Name()
			observability.RegisterHealthCheck("lb-agent-"+agentName, func(ctx context.Context) observability.ComponentHealth {
				idx := lbAgent.findAgentIndex(agentName)
				if idx < 0 {
					return observability.ComponentHealth{
						Name:   agentName,
						Status: observability.HealthStatusUnhealthy,
						Message: "agent not found",
					}
				}
				lbAgent.mu.RLock()
				wrapper := lbAgent.agents[idx]
				lbAgent.mu.RUnlock()

				healthy := wrapper.Healthy.Load()
				cbState := wrapper.CircuitBreaker.State()

				status := observability.HealthStatusHealthy
				if !healthy || cbState != resilience.StateClosed {
					status = observability.HealthStatusDegraded
				}

				return observability.ComponentHealth{
					Name:      agentName,
					Status:    status,
					CheckedAt: time.Now(),
					Metadata: map[string]string{
						"circuit_breaker": cbState.String(),
						"in_flight":       fmt.Sprintf("%d", wrapper.InFlight.Load()),
					},
				}
			})
		}
	}

	executor := &loadBalancerExecutor{
		lbAgent: lbAgent,
	}

	baseAgent := agent.NewAgent(agent.AgentConfig{
		Name:         config.Name,
		Executor:     executor,
		Dependencies: config.Dependencies,
	})

	lbAgent.BaseAgent = baseAgent
	return lbAgent
}

// findAgentIndex returns the index of an agent by name, or -1 if not found
func (lb *LoadBalancerAgent) findAgentIndex(name string) int {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	for i, w := range lb.agents {
		if w.Agent.Name() == name {
			return i
		}
	}
	return -1
}

// AddAgent adds a new agent to the load balancer pool
func (lb *LoadBalancerAgent) AddAgent(a agent.Agent, cbConfig *resilience.CircuitBreakerConfig) error {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	// Check if agent already exists
	for _, w := range lb.agents {
		if w.Agent.Name() == a.Name() {
			return fmt.Errorf("agent '%s' already exists in pool", a.Name())
		}
	}

	// Create circuit breaker config
	config := resilience.DefaultCircuitBreakerConfig(a.Name())
	if cbConfig != nil {
		config = *cbConfig
		config.Name = a.Name()
	}

	wrapper := AgentWrapper{
		Agent:          a,
		CircuitBreaker: resilience.NewCircuitBreaker(config),
	}
	wrapper.Healthy.Store(true)

	lb.agents = append(lb.agents, wrapper)

	// Register health check
	agentName := a.Name()
	observability.RegisterHealthCheck("lb-agent-"+agentName, func(ctx context.Context) observability.ComponentHealth {
		idx := lb.findAgentIndex(agentName)
		if idx < 0 {
			return observability.ComponentHealth{
				Name:    agentName,
				Status:  observability.HealthStatusUnhealthy,
				Message: "agent not found",
			}
		}
		lb.mu.RLock()
		w := lb.agents[idx]
		lb.mu.RUnlock()

		healthy := w.Healthy.Load()
		cbState := w.CircuitBreaker.State()

		status := observability.HealthStatusHealthy
		if !healthy || cbState != resilience.StateClosed {
			status = observability.HealthStatusDegraded
		}

		return observability.ComponentHealth{
			Name:      agentName,
			Status:    status,
			CheckedAt: time.Now(),
			Metadata: map[string]string{
				"circuit_breaker": cbState.String(),
				"in_flight":       fmt.Sprintf("%d", w.InFlight.Load()),
			},
		}
	})

	return nil
}

// RemoveAgent removes an agent from the load balancer pool
func (lb *LoadBalancerAgent) RemoveAgent(name string) error {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	idx := -1
	for i, w := range lb.agents {
		if w.Agent.Name() == name {
			idx = i
			break
		}
	}

	if idx == -1 {
		return fmt.Errorf("agent '%s' not found in pool", name)
	}

	// Remove from slice (preserving order)
	lb.agents = append(lb.agents[:idx], lb.agents[idx+1:]...)

	// Unregister health check
	observability.UnregisterHealthCheck("lb-agent-" + name)

	return nil
}

// DrainAgent gracefully drains an agent by marking it unhealthy
// This allows in-flight requests to complete before removal
func (lb *LoadBalancerAgent) DrainAgent(ctx context.Context, name string, timeout time.Duration) error {
	lb.mu.RLock()
	idx := -1
	for i, w := range lb.agents {
		if w.Agent.Name() == name {
			idx = i
			break
		}
	}
	if idx == -1 {
		lb.mu.RUnlock()
		return fmt.Errorf("agent '%s' not found in pool", name)
	}
	wrapper := &lb.agents[idx]
	lb.mu.RUnlock()

	// Mark as unhealthy to stop new requests
	wrapper.Healthy.Store(false)

	// Wait for in-flight requests to complete
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if wrapper.InFlight.Load() == 0 {
				// All requests drained, safe to remove
				return lb.RemoveAgent(name)
			}
			if time.Now().After(deadline) {
				// Force remove after timeout
				return lb.RemoveAgent(name)
			}
		}
	}
}

// GetAgentCount returns the current number of agents in the pool
func (lb *LoadBalancerAgent) GetAgentCount() int {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	return len(lb.agents)
}

// GetHealthyAgentCount returns the number of healthy agents
func (lb *LoadBalancerAgent) GetHealthyAgentCount() int {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	count := 0
	for _, w := range lb.agents {
		if w.Healthy.Load() && w.CircuitBreaker.State() != resilience.StateOpen {
			count++
		}
	}
	return count
}

// GetAgentStats returns statistics about agents in the pool
func (lb *LoadBalancerAgent) GetAgentStats() []AgentStats {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	stats := make([]AgentStats, len(lb.agents))
	for i, w := range lb.agents {
		stats[i] = AgentStats{
			Name:               w.Agent.Name(),
			Healthy:            w.Healthy.Load(),
			InFlight:           w.InFlight.Load(),
			CircuitBreakerState: w.CircuitBreaker.State().String(),
		}
	}
	return stats
}

// AgentStats represents statistics for an agent in the pool
type AgentStats struct {
	Name                string
	Healthy             bool
	InFlight            int64
	CircuitBreakerState string
}

// loadBalancerExecutor implements AgentExecutor for load balancing
type loadBalancerExecutor struct {
	lbAgent *LoadBalancerAgent
}

func (e *loadBalancerExecutor) Execute(ctx context.Context, _ agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	e.lbAgent.mu.RLock()
	agentCount := len(e.lbAgent.agents)
	e.lbAgent.mu.RUnlock()

	if agentCount == 0 {
		return nil, fmt.Errorf("no agents configured")
	}

	startTime := time.Now()

	// Select agent wrapper based on strategy
	wrapper, idx, err := e.selectAgent()
	if err != nil {
		return nil, err
	}

	// Track in-flight requests for LeastLoaded strategy
	wrapper.InFlight.Add(1)
	defer wrapper.InFlight.Add(-1)

	// Execute through circuit breaker
	var output *agent.AgentOutput
	execErr := wrapper.CircuitBreaker.Execute(ctx, func(_ context.Context) error {
		var innerErr error
		output, innerErr = wrapper.Agent.Execute(ctx, input)
		return innerErr
	})

	duration := time.Since(startTime)

	// Update health status based on circuit breaker outcome
	if execErr != nil {
		// Mark unhealthy on consecutive failures (circuit breaker handles this internally)
		if wrapper.CircuitBreaker.State() == resilience.StateOpen {
			wrapper.Healthy.Store(false)
		}

		// Record failure metric
		if e.lbAgent.metrics != nil {
			e.lbAgent.metrics.RecordAgentExecution(wrapper.Agent.Name(), "load-balanced", "error", duration)
			e.lbAgent.metrics.RecordAgentError(wrapper.Agent.Name(), "circuit_breaker_failure")
		}

		return nil, fmt.Errorf("agent '%s' execution failed: %w", wrapper.Agent.Name(), execErr)
	}

	// Mark healthy on success
	wrapper.Healthy.Store(true)

	// Record success metrics
	if e.lbAgent.metrics != nil {
		e.lbAgent.metrics.RecordAgentExecution(wrapper.Agent.Name(), "load-balanced", "success", duration)
	}

	// Add load balancing metadata
	if output.Metadata == nil {
		output.Metadata = make(map[string]interface{})
	}
	output.Metadata["pattern"] = "LoadBalancer"
	output.Metadata["strategy"] = string(e.lbAgent.strategy)
	output.Metadata["selected_agent"] = wrapper.Agent.Name()
	output.Metadata["selected_index"] = idx
	output.Metadata["circuit_breaker_state"] = wrapper.CircuitBreaker.State().String()
	output.Metadata["execution_time_ms"] = duration.Milliseconds()

	return output, nil
}

// selectAgent selects an agent wrapper based on the configured strategy
func (e *loadBalancerExecutor) selectAgent() (*AgentWrapper, int, error) {
	e.lbAgent.mu.RLock()
	defer e.lbAgent.mu.RUnlock()

	agentCount := len(e.lbAgent.agents)
	if agentCount == 0 {
		return nil, -1, fmt.Errorf("no agents available")
	}

	// Find healthy agents for selection
	healthyIndices := make([]int, 0, agentCount)
	for i := range e.lbAgent.agents {
		if e.lbAgent.agents[i].Healthy.Load() &&
			e.lbAgent.agents[i].CircuitBreaker.State() != resilience.StateOpen {
			healthyIndices = append(healthyIndices, i)
		}
	}

	// Fall back to all agents if none are healthy
	if len(healthyIndices) == 0 {
		for i := range e.lbAgent.agents {
			healthyIndices = append(healthyIndices, i)
		}
	}

	var idx int
	switch e.lbAgent.strategy {
	case RoundRobin:
		// Atomic increment and modulo for thread-safe round-robin
		counter := e.lbAgent.counter.Add(1)
		healthyIdx := int(counter-1) % len(healthyIndices)
		idx = healthyIndices[healthyIdx]

	case Random:
		// Thread-safe random selection using separate mutex for random source
		e.lbAgent.randMu.Lock()
		healthyIdx := e.lbAgent.randSource.Intn(len(healthyIndices))
		e.lbAgent.randMu.Unlock()
		idx = healthyIndices[healthyIdx]

	case LeastLoaded:
		// Select agent with minimum in-flight requests
		minInFlight := int64(^uint64(0) >> 1) // Max int64
		idx = healthyIndices[0]
		for _, i := range healthyIndices {
			inFlight := e.lbAgent.agents[i].InFlight.Load()
			if inFlight < minInFlight {
				minInFlight = inFlight
				idx = i
			}
		}

	default:
		return nil, -1, fmt.Errorf("unknown load balance strategy: %s", e.lbAgent.strategy)
	}

	return &e.lbAgent.agents[idx], idx, nil
}

// IntentRouter routes based on detected intent
type IntentRouter struct {
	*RouterAgent
	classifier agent.Agent // Agent that classifies intent
}

// IntentRouterConfig configures intent-based routing
type IntentRouterConfig struct {
	Name            string
	Classifier      agent.Agent            // Classifies intent
	IntentToAgent   map[string]agent.Agent // Maps intent to agent
	DefaultAgent    agent.Agent
	ClassifyTimeout time.Duration
	Dependencies    []agent.Agent
}

// NewIntentRouter creates an intent-based router
func NewIntentRouter(config IntentRouterConfig) *IntentRouter {
	if config.ClassifyTimeout == 0 {
		config.ClassifyTimeout = 10 * time.Second
	}

	// Build routes from intent map
	var routes []Route
	for intent, intentAgent := range config.IntentToAgent {
		// Capture intent in closure
		intentCopy := intent
		routes = append(routes, Route{
			Name: fmt.Sprintf("intent-%s", intent),
			Condition: func(input *agent.AgentInput) bool {
				// This will be evaluated after classification
				if classifiedIntent, ok := input.Context["intent"].(string); ok {
					return classifiedIntent == intentCopy
				}
				return false
			},
			Agent:    intentAgent,
			Priority: 0,
		})
	}

	routerAgent := NewRouterAgent(RouterAgentConfig{
		Name:         config.Name,
		Routes:       routes,
		DefaultAgent: config.DefaultAgent,
		Fallback:     FallbackDefault,
		Dependencies: config.Dependencies,
	})

	return &IntentRouter{
		RouterAgent: routerAgent,
		classifier:  config.Classifier,
	}
}

// Execute classifies intent then routes
func (r *IntentRouter) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// First, classify intent
	classifyOutput, err := r.classifier.Execute(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("intent classification failed: %w", err)
	}

	// Extract intent from classification result
	intent, ok := classifyOutput.Result.(string)
	if !ok {
		return nil, fmt.Errorf("classifier did not return string intent")
	}

	// Add intent to input context
	if input.Context == nil {
		input.Context = make(map[string]interface{})
	}
	input.Context["intent"] = intent

	// Route based on intent
	return r.RouterAgent.Execute(ctx, input)
}
