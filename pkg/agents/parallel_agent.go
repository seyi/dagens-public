package agents

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/runtime"
)

// AggregationType defines how parallel results are combined
type AggregationType string

const (
	// ConcatAggregation concatenates all results
	ConcatAggregation AggregationType = "concat"

	// FirstAggregation returns first completed result
	FirstAggregation AggregationType = "first"

	// AllAggregation returns all results as array
	AllAggregation AggregationType = "all"

	// ReduceAggregation applies custom reduce function
	ReduceAggregation AggregationType = "reduce"

	// VoteAggregation takes majority vote (for classification)
	VoteAggregation AggregationType = "vote"
)

// ParallelAgent executes multiple agents concurrently
// Leverages our DAG infrastructure for distributed parallel execution
type ParallelAgent struct {
	*agent.BaseAgent
	childAgents   []agent.Agent
	aggregation   AggregationType
	reduceFunc    ReduceFunc
	timeout       time.Duration
	failFast      bool // Fail immediately on first error
	minSuccessful int  // Minimum successful agents required
}

// ReduceFunc combines multiple results into one
type ReduceFunc func(results []*agent.AgentOutput) (interface{}, error)

// ParallelAgentConfig configures a parallel agent
type ParallelAgentConfig struct {
	Name          string
	Agents        []agent.Agent // Legacy field
	SubAgents     []agent.Agent // ADK-compatible field for sub-agents
	Aggregation   AggregationType
	ReduceFunc    ReduceFunc
	Timeout       time.Duration
	FailFast      bool
	MinSuccessful int // 0 means all must succeed
	Dependencies  []agent.Agent
}

// NewParallelAgent creates a new parallel agent
func NewParallelAgent(config ParallelAgentConfig) *ParallelAgent {
	if config.Aggregation == "" {
		config.Aggregation = AllAggregation
	}
	if config.Timeout == 0 {
		config.Timeout = 5 * time.Minute
	}

	// Support both Agents (legacy) and SubAgents (ADK-compatible) fields
	childAgents := config.Agents
	if len(config.SubAgents) > 0 {
		// SubAgents takes precedence for ADK compatibility
		childAgents = config.SubAgents
	}

	parallelAgent := &ParallelAgent{
		childAgents:   childAgents,
		aggregation:   config.Aggregation,
		reduceFunc:    config.ReduceFunc,
		timeout:       config.Timeout,
		failFast:      config.FailFast,
		minSuccessful: config.MinSuccessful,
	}

	// Create executor
	executor := &parallelExecutor{
		parallelAgent: parallelAgent,
	}

	// Note: Child agents have no dependencies between them (parallel)
	// They all depend on the parent's dependencies
	baseAgent := agent.NewAgent(agent.AgentConfig{
		Name:         config.Name,
		Executor:     executor,
		Dependencies: config.Dependencies,
		SubAgents:    childAgents, // Register as sub-agents for hierarchy support
	})

	parallelAgent.BaseAgent = baseAgent
	return parallelAgent
}

// parallelExecutor implements AgentExecutor for concurrent execution
type parallelExecutor struct {
	parallelAgent *ParallelAgent
}

func (e *parallelExecutor) Execute(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	startTime := time.Now()

	// Runtime integration: Emit parallel start event
	runtime.EmitMessage(ctx, fmt.Sprintf("Starting parallel execution of %d agents...", len(e.parallelAgent.childAgents)), true)

	// Create timeout context
	ctx, cancel := context.WithTimeout(ctx, e.parallelAgent.timeout)
	defer cancel()

	numAgents := len(e.parallelAgent.childAgents)
	results := make([]*agent.AgentOutput, numAgents)
	errors := make([]error, numAgents)

	// For "first" aggregation, use channel
	if e.parallelAgent.aggregation == FirstAggregation {
		return e.executeFirst(ctx, input, startTime)
	}

	// Execute all agents in parallel
	var wg sync.WaitGroup
	errChan := make(chan error, 1)

	for i, childAgent := range e.parallelAgent.childAgents {
		wg.Add(1)
		go func(idx int, agent agent.Agent) {
			defer wg.Done()

			output, err := agent.Execute(ctx, input)
			if err != nil {
				errors[idx] = err

				// Fail fast if configured
				if e.parallelAgent.failFast {
					select {
					case errChan <- fmt.Errorf("agent %s failed: %w", agent.Name(), err):
					default:
					}
					cancel() // Cancel all other executions
				}
				return
			}

			results[idx] = output
		}(i, childAgent)
	}

	// Wait for all to complete or fail-fast
	wg.Wait()

	// Check for fail-fast error
	select {
	case err := <-errChan:
		return nil, err
	default:
	}

	// Count successful executions
	successCount := 0
	for _, result := range results {
		if result != nil {
			successCount++
		}
	}

	// Check minimum successful threshold
	minRequired := e.parallelAgent.minSuccessful
	if minRequired == 0 {
		minRequired = numAgents // All must succeed by default
	}

	if successCount < minRequired {
		return nil, fmt.Errorf("parallel execution failed: only %d/%d agents succeeded (minimum: %d)",
			successCount, numAgents, minRequired)
	}

	// Aggregate results
	aggregatedResult, err := e.aggregateResults(results, errors)
	if err != nil {
		return nil, fmt.Errorf("result aggregation failed: %w", err)
	}

	// Runtime integration: Emit completion event
	runtime.EmitEventWithDelta(ctx, runtime.EventTypeMessage, "Parallel execution completed", false, map[string]interface{}{
		"successful":     successCount,
		"total_agents":   numAgents,
		"execution_time": time.Since(startTime).Seconds(),
	})

	return &agent.AgentOutput{
		Result: aggregatedResult,
		Metadata: map[string]interface{}{
			"pattern":            "Parallel",
			"aggregation":        e.parallelAgent.aggregation,
			"total_agents":       numAgents,
			"successful":         successCount,
			"execution_time":     time.Since(startTime).Seconds(),
			"individual_results": results,
			"errors":             errors,
		},
	}, nil
}

// executeFirst returns the first successful result
func (e *parallelExecutor) executeFirst(ctx context.Context, input *agent.AgentInput, startTime time.Time) (*agent.AgentOutput, error) {
	resultChan := make(chan *agent.AgentOutput, 1)
	errorChan := make(chan error, len(e.parallelAgent.childAgents))

	ctx, cancel := context.WithTimeout(ctx, e.parallelAgent.timeout)
	defer cancel()

	// Launch all agents
	for _, childAgent := range e.parallelAgent.childAgents {
		go func(agent agent.Agent) {
			output, err := agent.Execute(ctx, input)
			if err != nil {
				errorChan <- err
				return
			}

			select {
			case resultChan <- output:
				cancel() // Cancel other executions
			default:
			}
		}(childAgent)
	}

	// Wait for first success or all failures
	errorCount := 0
	for {
		select {
		case result := <-resultChan:
			result.Metadata["pattern"] = "Parallel-First"
			result.Metadata["execution_time"] = time.Since(startTime).Seconds()
			return result, nil

		case <-errorChan:
			errorCount++
			if errorCount >= len(e.parallelAgent.childAgents) {
				return nil, fmt.Errorf("all %d agents failed", len(e.parallelAgent.childAgents))
			}

		case <-ctx.Done():
			return nil, fmt.Errorf("parallel execution timed out")
		}
	}
}

// aggregateResults combines results based on aggregation type
func (e *parallelExecutor) aggregateResults(results []*agent.AgentOutput, errors []error) (interface{}, error) {
	// Filter out nil results
	validResults := make([]*agent.AgentOutput, 0)
	for _, result := range results {
		if result != nil {
			validResults = append(validResults, result)
		}
	}

	switch e.parallelAgent.aggregation {
	case AllAggregation:
		return e.aggregateAll(validResults), nil

	case ConcatAggregation:
		return e.aggregateConcat(validResults), nil

	case ReduceAggregation:
		if e.parallelAgent.reduceFunc == nil {
			return nil, fmt.Errorf("reduce function not provided")
		}
		return e.parallelAgent.reduceFunc(validResults)

	case VoteAggregation:
		return e.aggregateVote(validResults), nil

	default:
		return nil, fmt.Errorf("unknown aggregation type: %s", e.parallelAgent.aggregation)
	}
}

// aggregateAll returns all results as array
func (e *parallelExecutor) aggregateAll(results []*agent.AgentOutput) []interface{} {
	all := make([]interface{}, len(results))
	for i, result := range results {
		all[i] = result.Result
	}
	return all
}

// aggregateConcat concatenates string results
func (e *parallelExecutor) aggregateConcat(results []*agent.AgentOutput) string {
	var concat string
	for i, result := range results {
		if i > 0 {
			concat += "\n\n"
		}
		concat += fmt.Sprintf("%v", result.Result)
	}
	return concat
}

// aggregateVote takes majority vote from results
func (e *parallelExecutor) aggregateVote(results []*agent.AgentOutput) interface{} {
	votes := make(map[string]int)
	for _, result := range results {
		key := fmt.Sprintf("%v", result.Result)
		votes[key]++
	}

	// Find majority
	var majorityKey string
	maxVotes := 0
	for key, count := range votes {
		if count > maxVotes {
			maxVotes = count
			majorityKey = key
		}
	}

	return majorityKey
}

// ParallelBuilder provides a fluent API for building parallel agents
type ParallelBuilder struct {
	name          string
	agents        []agent.Agent
	aggregation   AggregationType
	reduceFunc    ReduceFunc
	timeout       time.Duration
	failFast      bool
	minSuccessful int
	dependencies  []agent.Agent
}

// NewParallel creates a new parallel agent builder
func NewParallel(aggregation AggregationType) *ParallelBuilder {
	return &ParallelBuilder{
		agents:      []agent.Agent{},
		aggregation: aggregation,
		timeout:     5 * time.Minute,
	}
}

// WithName sets the agent name
func (b *ParallelBuilder) WithName(name string) *ParallelBuilder {
	b.name = name
	return b
}

// Add adds an agent to parallel execution
func (b *ParallelBuilder) Add(agent agent.Agent) *ParallelBuilder {
	b.agents = append(b.agents, agent)
	return b
}

// AddMultiple adds multiple agents to parallel execution
func (b *ParallelBuilder) AddMultiple(agents ...agent.Agent) *ParallelBuilder {
	b.agents = append(b.agents, agents...)
	return b
}

// WithReduceFunc sets a custom reduce function
func (b *ParallelBuilder) WithReduceFunc(fn ReduceFunc) *ParallelBuilder {
	b.aggregation = ReduceAggregation
	b.reduceFunc = fn
	return b
}

// WithTimeout sets execution timeout
func (b *ParallelBuilder) WithTimeout(timeout time.Duration) *ParallelBuilder {
	b.timeout = timeout
	return b
}

// WithFailFast enables fail-fast behavior
func (b *ParallelBuilder) WithFailFast(failFast bool) *ParallelBuilder {
	b.failFast = failFast
	return b
}

// WithMinSuccessful sets minimum required successful agents
func (b *ParallelBuilder) WithMinSuccessful(min int) *ParallelBuilder {
	b.minSuccessful = min
	return b
}

// WithDependencies adds external dependencies
func (b *ParallelBuilder) WithDependencies(deps ...agent.Agent) *ParallelBuilder {
	b.dependencies = append(b.dependencies, deps...)
	return b
}

// Build creates the parallel agent
func (b *ParallelBuilder) Build() *ParallelAgent {
	if b.name == "" {
		b.name = "parallel-agent"
	}

	return NewParallelAgent(ParallelAgentConfig{
		Name:          b.name,
		Agents:        b.agents,
		Aggregation:   b.aggregation,
		ReduceFunc:    b.reduceFunc,
		Timeout:       b.timeout,
		FailFast:      b.failFast,
		MinSuccessful: b.minSuccessful,
		Dependencies:  b.dependencies,
	})
}
