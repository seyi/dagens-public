package agents

import (
	"context"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/runtime"
)

// LoopType defines the type of loop
type LoopType string

const (
	// WhileLoop continues while condition is true
	WhileLoop LoopType = "while"

	// UntilLoop continues until condition is true
	UntilLoop LoopType = "until"

	// ForEachLoop iterates over collection
	ForEachLoop LoopType = "foreach"

	// CountLoop executes fixed number of times
	CountLoop LoopType = "count"
)

// ConditionFunc evaluates whether to continue looping
type ConditionFunc func(output *agent.AgentOutput, iteration int) bool

// LoopAgent executes an agent iteratively with conditions
// Critical missing feature identified in ADK comparison
type LoopAgent struct {
	*agent.BaseAgent
	innerAgent     agent.Agent
	loopType       LoopType
	condition      ConditionFunc
	maxIterations  int
	accumulateMode AccumulateMode
	timeout        time.Duration
}

// AccumulateMode defines how results are accumulated across iterations
type AccumulateMode string

const (
	// AccumulateAll keeps all iteration results
	AccumulateAll AccumulateMode = "all"

	// AccumulateLast keeps only the last result
	AccumulateLast AccumulateMode = "last"

	// AccumulateState maintains a state object across iterations
	AccumulateState AccumulateMode = "state"
)

// LoopAgentConfig configures a loop agent
type LoopAgentConfig struct {
	Name           string
	InnerAgent     agent.Agent   // For single agent loops (legacy)
	SubAgents      []agent.Agent // ADK-compatible: loop over multiple agents sequentially
	LoopType       LoopType
	Condition      ConditionFunc
	MaxIterations  int
	AccumulateMode AccumulateMode
	Timeout        time.Duration
	Dependencies   []agent.Agent
}

// NewLoopAgent creates a new loop agent
func NewLoopAgent(config LoopAgentConfig) *LoopAgent {
	if config.MaxIterations == 0 {
		config.MaxIterations = 10 // Safe default
	}
	if config.AccumulateMode == "" {
		config.AccumulateMode = AccumulateLast
	}
	if config.Timeout == 0 {
		config.Timeout = 10 * time.Minute
	}

	// Support both InnerAgent (legacy) and SubAgents (ADK-compatible) fields
	var innerAgent agent.Agent
	var subAgents []agent.Agent

	if len(config.SubAgents) > 0 {
		// ADK pattern: loop over multiple agents sequentially
		// Auto-wrap SubAgents in a SequentialAgent
		innerAgent = NewSequentialAgent(SequentialAgentConfig{
			Name:       config.Name + "_sequence",
			Agents:     config.SubAgents,
			PassOutput: true, // Pass output between agents in the sequence
		})
		subAgents = config.SubAgents
	} else {
		innerAgent = config.InnerAgent
		// If InnerAgent is set, treat it as the only sub-agent
		if innerAgent != nil {
			subAgents = []agent.Agent{innerAgent}
		}
	}

	loopAgent := &LoopAgent{
		innerAgent:     innerAgent,
		loopType:       config.LoopType,
		condition:      config.Condition,
		maxIterations:  config.MaxIterations,
		accumulateMode: config.AccumulateMode,
		timeout:        config.Timeout,
	}

	// Create executor
	executor := &loopExecutor{
		loopAgent: loopAgent,
	}

	baseAgent := agent.NewAgent(agent.AgentConfig{
		Name:         config.Name,
		Executor:     executor,
		Dependencies: config.Dependencies,
		SubAgents:    subAgents, // Register sub-agents in hierarchy
	})

	loopAgent.BaseAgent = baseAgent
	return loopAgent
}

// loopExecutor implements AgentExecutor for iterative execution
type loopExecutor struct {
	loopAgent *LoopAgent
}

func (e *loopExecutor) Execute(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	startTime := time.Now()

	// Runtime integration: Emit loop start event
	runtime.EmitMessage(ctx, fmt.Sprintf("Starting loop execution (max %d iterations)...", e.loopAgent.maxIterations), true)

	// Create timeout context
	ctx, cancel := context.WithTimeout(ctx, e.loopAgent.timeout)
	defer cancel()

	var results []*agent.AgentOutput
	var currentOutput *agent.AgentOutput
	iteration := 0
	currentInput := input

	for iteration < e.loopAgent.maxIterations {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("loop execution timed out after %d iterations", iteration)
		default:
		}

		iteration++

		// Runtime integration: Emit iteration start
		runtime.EmitMessage(ctx, fmt.Sprintf("Loop iteration %d/%d...", iteration, e.loopAgent.maxIterations), true)

		// Execute inner agent (context is propagated)
		output, err := e.loopAgent.innerAgent.Execute(ctx, currentInput)
		if err != nil {
			return nil, fmt.Errorf("loop iteration %d failed: %w", iteration, err)
		}

		currentOutput = output

		// Store based on accumulation mode
		switch e.loopAgent.accumulateMode {
		case AccumulateAll:
			results = append(results, output)
		case AccumulateLast:
			results = []*agent.AgentOutput{output}
		case AccumulateState:
			// State is passed in context
			results = []*agent.AgentOutput{output}
		}

		// Check loop condition
		shouldContinue := e.evaluateCondition(output, iteration)

		if !shouldContinue {
			break
		}

		// Prepare input for next iteration
		currentInput = e.prepareNextInput(input, output, iteration)
	}

	// Build final output
	finalResult := e.buildFinalResult(results, currentOutput)

	// Runtime integration: Emit loop completion event
	runtime.EmitEventWithDelta(ctx, runtime.EventTypeMessage, fmt.Sprintf("Loop completed after %d iterations", iteration), false, map[string]interface{}{
		"iterations":      iteration,
		"execution_time":  time.Since(startTime).Seconds(),
		"completed_early": iteration < e.loopAgent.maxIterations,
	})

	return &agent.AgentOutput{
		Result: finalResult,
		Metadata: map[string]interface{}{
			"pattern":         "Loop",
			"loop_type":       e.loopAgent.loopType,
			"iterations":      iteration,
			"max_iterations":  e.loopAgent.maxIterations,
			"accumulate_mode": e.loopAgent.accumulateMode,
			"execution_time":  time.Since(startTime).Seconds(),
			"all_results":     results,
			"completed_early": iteration < e.loopAgent.maxIterations,
		},
	}, nil
}

// evaluateCondition checks if loop should continue
func (e *loopExecutor) evaluateCondition(output *agent.AgentOutput, iteration int) bool {
	// ADK pattern: Check for escalate event in metadata
	// If escalate is true, terminate the loop immediately
	if escalate, ok := output.Metadata["escalate"].(bool); ok && escalate {
		return false // Stop loop due to escalate event
	}

	// If no condition provided, continue until max iterations
	if e.loopAgent.condition == nil {
		return true // Continue to max iterations
	}

	result := e.loopAgent.condition(output, iteration)

	// Invert for "until" loops
	if e.loopAgent.loopType == UntilLoop {
		return !result
	}

	return result
}

// prepareNextInput builds input for next iteration
func (e *loopExecutor) prepareNextInput(originalInput *agent.AgentInput, previousOutput *agent.AgentOutput, iteration int) *agent.AgentInput {
	return &agent.AgentInput{
		Instruction: originalInput.Instruction,
		Context: map[string]interface{}{
			"iteration":       iteration + 1,
			"previous_result": previousOutput.Result,
			"original_input":  originalInput,
			"state":           previousOutput.Metadata["state"],
		},
	}
}

// buildFinalResult creates the final aggregated result
func (e *loopExecutor) buildFinalResult(results []*agent.AgentOutput, lastOutput *agent.AgentOutput) interface{} {
	switch e.loopAgent.accumulateMode {
	case AccumulateLast:
		if lastOutput != nil {
			return lastOutput.Result
		}
		return nil

	case AccumulateAll:
		allResults := make([]interface{}, len(results))
		for i, result := range results {
			allResults[i] = result.Result
		}
		return allResults

	case AccumulateState:
		if lastOutput != nil {
			if state, ok := lastOutput.Metadata["state"]; ok {
				return state
			}
			return lastOutput.Result
		}
		return nil

	default:
		return lastOutput.Result
	}
}

// LoopBuilder provides a fluent API for building loop agents
type LoopBuilder struct {
	name           string
	innerAgent     agent.Agent
	loopType       LoopType
	condition      ConditionFunc
	maxIterations  int
	accumulateMode AccumulateMode
	timeout        time.Duration
	dependencies   []agent.Agent
}

// NewLoop creates a new loop agent builder
func NewLoop(innerAgent agent.Agent) *LoopBuilder {
	return &LoopBuilder{
		innerAgent:     innerAgent,
		loopType:       WhileLoop,
		maxIterations:  10,
		accumulateMode: AccumulateLast,
		timeout:        10 * time.Minute,
	}
}

// WithName sets the agent name
func (b *LoopBuilder) WithName(name string) *LoopBuilder {
	b.name = name
	return b
}

// While sets a while-loop condition
func (b *LoopBuilder) While(condition ConditionFunc) *LoopBuilder {
	b.loopType = WhileLoop
	b.condition = condition
	return b
}

// Until sets an until-loop condition
func (b *LoopBuilder) Until(condition ConditionFunc) *LoopBuilder {
	b.loopType = UntilLoop
	b.condition = condition
	return b
}

// MaxIterations sets the maximum number of iterations
func (b *LoopBuilder) MaxIterations(max int) *LoopBuilder {
	b.maxIterations = max
	return b
}

// AccumulateAll accumulates all iteration results
func (b *LoopBuilder) AccumulateAll() *LoopBuilder {
	b.accumulateMode = AccumulateAll
	return b
}

// AccumulateLast keeps only the last iteration result
func (b *LoopBuilder) AccumulateLast() *LoopBuilder {
	b.accumulateMode = AccumulateLast
	return b
}

// AccumulateState maintains state across iterations
func (b *LoopBuilder) AccumulateState() *LoopBuilder {
	b.accumulateMode = AccumulateState
	return b
}

// WithTimeout sets execution timeout
func (b *LoopBuilder) WithTimeout(timeout time.Duration) *LoopBuilder {
	b.timeout = timeout
	return b
}

// WithDependencies adds external dependencies
func (b *LoopBuilder) WithDependencies(deps ...agent.Agent) *LoopBuilder {
	b.dependencies = append(b.dependencies, deps...)
	return b
}

// Build creates the loop agent
func (b *LoopBuilder) Build() *LoopAgent {
	if b.name == "" {
		b.name = "loop-agent"
	}

	return NewLoopAgent(LoopAgentConfig{
		Name:           b.name,
		InnerAgent:     b.innerAgent,
		LoopType:       b.loopType,
		Condition:      b.condition,
		MaxIterations:  b.maxIterations,
		AccumulateMode: b.accumulateMode,
		Timeout:        b.timeout,
		Dependencies:   b.dependencies,
	})
}

// Condition helpers for common patterns

// Until creates an until-condition from a function
func Until(fn ConditionFunc) ConditionFunc {
	return fn
}

// While creates a while-condition from a function
func While(fn ConditionFunc) ConditionFunc {
	return fn
}

// MaxIterationsCondition stops after N iterations
func MaxIterationsCondition(max int) ConditionFunc {
	return func(output *agent.AgentOutput, iteration int) bool {
		return iteration < max
	}
}

// ScoreThresholdCondition continues until a score threshold is met
func ScoreThresholdCondition(scoreKey string, threshold float64) ConditionFunc {
	return func(output *agent.AgentOutput, iteration int) bool {
		if score, ok := output.Metadata[scoreKey].(float64); ok {
			return score < threshold
		}
		return true // Continue if score not found
	}
}

// ErrorThresholdCondition stops if error rate is too high
func ErrorThresholdCondition(threshold float64) ConditionFunc {
	errors := 0
	return func(output *agent.AgentOutput, iteration int) bool {
		if output.Error != nil {
			errors++
		}
		errorRate := float64(errors) / float64(iteration)
		return errorRate < threshold
	}
}

// ConvergenceCondition stops when results stop changing significantly
func ConvergenceCondition(tolerance float64) ConditionFunc {
	var previousValue float64
	firstIteration := true

	return func(output *agent.AgentOutput, iteration int) bool {
		if firstIteration {
			if val, ok := output.Result.(float64); ok {
				previousValue = val
			}
			firstIteration = false
			return true
		}

		if val, ok := output.Result.(float64); ok {
			diff := val - previousValue
			if diff < 0 {
				diff = -diff
			}

			previousValue = val
			return diff > tolerance
		}

		return true
	}
}
