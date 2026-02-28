package agents

import (
	"context"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/runtime"
)

// SequentialAgent executes agents in predetermined order
// Provides ADK-like convenience while compiling to our DAG infrastructure
type SequentialAgent struct {
	*agent.BaseAgent
	childAgents []agent.Agent
	passOutput  bool
	stopOnError bool
}

// SequentialAgentConfig configures a sequential agent
type SequentialAgentConfig struct {
	Name         string
	Agents       []agent.Agent // Legacy field
	SubAgents    []agent.Agent // ADK-compatible field for sub-agents
	PassOutput   bool          // Pass output from one agent to next
	StopOnError  bool          // Stop on first error vs continue
	Dependencies []agent.Agent
}

// NewSequentialAgent creates a new sequential agent
func NewSequentialAgent(config SequentialAgentConfig) *SequentialAgent {
	if config.StopOnError {
		config.StopOnError = true // Default to stop on error
	}

	// Support both Agents (legacy) and SubAgents (ADK-compatible) fields
	childAgents := config.Agents
	if len(config.SubAgents) > 0 {
		// SubAgents takes precedence for ADK compatibility
		childAgents = config.SubAgents
	}

	seqAgent := &SequentialAgent{
		childAgents: childAgents,
		passOutput:  config.PassOutput,
		stopOnError: config.StopOnError,
	}

	// Create executor
	executor := &sequentialExecutor{
		seqAgent: seqAgent,
	}

	// Wire up dependencies between child agents
	wireDependencies(childAgents)

	// Create base agent with the last agent as dependency
	deps := config.Dependencies
	if len(childAgents) > 0 {
		deps = append(deps, childAgents[len(childAgents)-1])
	}

	baseAgent := agent.NewAgent(agent.AgentConfig{
		Name:         config.Name,
		Executor:     executor,
		Dependencies: deps,
		SubAgents:    childAgents, // Register as sub-agents for hierarchy support
	})

	seqAgent.BaseAgent = baseAgent
	return seqAgent
}

// wireDependencies sets up sequential dependencies between agents
func wireDependencies(agents []agent.Agent) {
	for i := 1; i < len(agents); i++ {
		// Each agent depends on the previous one
		if ba, ok := agents[i].(*agent.BaseAgent); ok {
			ba.AddDependency(agents[i-1])
		}
	}
}

// sequentialExecutor implements AgentExecutor for sequential execution
type sequentialExecutor struct {
	seqAgent *SequentialAgent
}

func (e *sequentialExecutor) Execute(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	startTime := time.Now()

	// Runtime integration: Emit sequence start event
	runtime.EmitMessage(ctx, fmt.Sprintf("Starting sequential execution of %d agents...", len(e.seqAgent.childAgents)), true)

	results := make([]*agent.AgentOutput, 0, len(e.seqAgent.childAgents))
	currentInput := input

	for i, childAgent := range e.seqAgent.childAgents {
		// Runtime integration: Emit step start event
		runtime.EmitMessage(ctx, fmt.Sprintf("Step %d/%d: Executing %s...", i+1, len(e.seqAgent.childAgents), childAgent.Name()), true)

		// Execute child agent (context is propagated)
		output, err := childAgent.Execute(ctx, currentInput)

		if err != nil {
			// Runtime integration: Emit error event
			runtime.EmitEvent(ctx, runtime.EventTypeError, map[string]interface{}{
				"agent": childAgent.Name(),
				"step":  i,
				"error": err.Error(),
			}, false)

			if e.seqAgent.stopOnError {
				return nil, fmt.Errorf("sequential agent failed at step %d (%s): %w",
					i, childAgent.Name(), err)
			}

			// Continue with error recorded
			results = append(results, &agent.AgentOutput{
				Result:    nil,
				Error:     err,

				Metadata: map[string]interface{}{
					"agent": childAgent.Name(),
					"step":  i,
				},
			})

			// If not passing output, keep using original input
			if !e.seqAgent.passOutput {
				continue
			}

			// If passing output, use error context
			currentInput = &agent.AgentInput{
				Instruction: currentInput.Instruction,
				Context: map[string]interface{}{
					"previous_error":  err.Error(),
					"previous_agent":  childAgent.Name(),
					"previous_result": nil,
				},
			}
			continue
		}

		results = append(results, output)

		// If passing output, prepare input for next agent
		if e.seqAgent.passOutput && i < len(e.seqAgent.childAgents)-1 {
			currentInput = &agent.AgentInput{
				Instruction: fmt.Sprintf("Process the following result: %v", output.Result),
				Context: map[string]interface{}{
					"previous_result": output.Result,
					"previous_agent":  childAgent.Name(),
					"original_input":  input,
				},
			}
		}
	}

	// Aggregate results
	finalResult := e.aggregateResults(results)

	// Runtime integration: Emit completion event
	runtime.EmitEventWithDelta(ctx, runtime.EventTypeMessage, "Sequential execution completed", false, map[string]interface{}{
		"steps_completed": len(results),
		"execution_time":  time.Since(startTime).Seconds(),
	})

	return &agent.AgentOutput{
		Result: finalResult,
		Metadata: map[string]interface{}{
			"pattern":         "Sequential",
			"steps_completed": len(results),
			"total_steps":     len(e.seqAgent.childAgents),
			"execution_time":  time.Since(startTime).Seconds(),
			"step_results":    results,
		},
	}, nil
}

// aggregateResults combines results from all steps
func (e *sequentialExecutor) aggregateResults(results []*agent.AgentOutput) interface{} {
	if len(results) == 0 {
		return nil
	}

	// If only one result, return it directly
	if len(results) == 1 {
		return results[0].Result
	}

	// If passing output is enabled, return the last result
	if e.seqAgent.passOutput && len(results) > 0 {
		return results[len(results)-1].Result
	}

	// Otherwise return all results
	allResults := make([]interface{}, len(results))
	for i, result := range results {
		allResults[i] = result.Result
	}
	return allResults
}

// SequentialBuilder provides a fluent API for building sequential agents
type SequentialBuilder struct {
	name         string
	agents       []agent.Agent
	passOutput   bool
	stopOnError  bool
	dependencies []agent.Agent
}

// NewSequential creates a new sequential agent builder
func NewSequential() *SequentialBuilder {
	return &SequentialBuilder{
		agents:      []agent.Agent{},
		stopOnError: true,
	}
}

// WithName sets the agent name
func (b *SequentialBuilder) WithName(name string) *SequentialBuilder {
	b.name = name
	return b
}

// Add adds an agent to the sequence
func (b *SequentialBuilder) Add(agent agent.Agent) *SequentialBuilder {
	b.agents = append(b.agents, agent)
	return b
}

// AddMultiple adds multiple agents to the sequence
func (b *SequentialBuilder) AddMultiple(agents ...agent.Agent) *SequentialBuilder {
	b.agents = append(b.agents, agents...)
	return b
}

// WithPassOutput enables passing output from one agent to the next
func (b *SequentialBuilder) WithPassOutput(pass bool) *SequentialBuilder {
	b.passOutput = pass
	return b
}

// WithStopOnError sets whether to stop on first error
func (b *SequentialBuilder) WithStopOnError(stop bool) *SequentialBuilder {
	b.stopOnError = stop
	return b
}

// WithDependencies adds external dependencies
func (b *SequentialBuilder) WithDependencies(deps ...agent.Agent) *SequentialBuilder {
	b.dependencies = append(b.dependencies, deps...)
	return b
}

// Build creates the sequential agent
func (b *SequentialBuilder) Build() *SequentialAgent {
	if b.name == "" {
		b.name = "sequential-agent"
	}

	return NewSequentialAgent(SequentialAgentConfig{
		Name:         b.name,
		Agents:       b.agents,
		PassOutput:   b.passOutput,
		StopOnError:  b.stopOnError,
		Dependencies: b.dependencies,
	})
}

// ConditionalBuilder allows conditional branching in sequential workflows
type ConditionalBuilder struct {
	condition func(*agent.AgentOutput) bool
	thenAgent agent.Agent
	elseAgent agent.Agent
}

// NewConditional creates a conditional branch in workflow
func NewConditional(condition func(*agent.AgentOutput) bool) *ConditionalBuilder {
	return &ConditionalBuilder{
		condition: condition,
	}
}

// Then sets the agent to execute if condition is true
func (b *ConditionalBuilder) Then(agent agent.Agent) *ConditionalBuilder {
	b.thenAgent = agent
	return b
}

// Else sets the agent to execute if condition is false
func (b *ConditionalBuilder) Else(agent agent.Agent) *ConditionalBuilder {
	b.elseAgent = agent
	return b
}

// Build creates a conditional executor agent
func (b *ConditionalBuilder) Build() agent.Agent {
	executor := &conditionalExecutor{
		condition: b.condition,
		thenAgent: b.thenAgent,
		elseAgent: b.elseAgent,
	}

	return agent.NewAgent(agent.AgentConfig{
		Name:     "conditional-agent",
		Executor: executor,
	})
}

// conditionalExecutor executes one of two agents based on condition
type conditionalExecutor struct {
	condition func(*agent.AgentOutput) bool
	thenAgent agent.Agent
	elseAgent agent.Agent
}

func (e *conditionalExecutor) Execute(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// Get previous result from context
	var previousOutput *agent.AgentOutput
	if prevResult, ok := input.Context["previous_result"]; ok {
		if output, ok := prevResult.(*agent.AgentOutput); ok {
			previousOutput = output
		}
	}

	// Evaluate condition
	var targetAgent agent.Agent
	if previousOutput != nil && e.condition(previousOutput) {
		targetAgent = e.thenAgent
	} else {
		targetAgent = e.elseAgent
	}

	// Execute selected agent
	if targetAgent != nil {
		return targetAgent.Execute(ctx, input)
	}

	// No agent selected, pass through
	return &agent.AgentOutput{
		Result:    input,
		
		Metadata: map[string]interface{}{
			"pattern": "Conditional",
			"skipped": true,
		},
	}, nil
}
