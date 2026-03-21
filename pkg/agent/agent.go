// Package agent provides core abstractions for distributed AI agents
// inspired by Apache Spark's distributed computing model.
package agent

import (
	"context"
	"errors" // Added
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/seyi/dagens/pkg/telemetry"
	"github.com/seyi/dagens/pkg/types"
)

// ErrAgentNotFound is returned when a requested agent cannot be found
var ErrAgentNotFound = errors.New("agent not found")

// Agent represents a distributed AI agent with specific capabilities.
// Inspired by Spark's RDD abstraction, agents are composable units of computation.
type Agent interface {
	// ID returns the unique identifier for this agent.
	ID() string

	// Name returns the human-readable name of the agent.
	Name() string

	// Description returns a brief explanation of what this agent does.
	Description() string

	// Capabilities returns a list of tools or other capabilities this agent possesses.
	Capabilities() []string

	// Execute runs the agent with a given input and context. This is the primary
	// method for invoking an agent's logic.
	Execute(ctx context.Context, input *AgentInput) (*AgentOutput, error)

	// Dependencies returns a slice of Agents that must be successfully executed
	// before this agent can run.
	Dependencies() []Agent

	// Partition returns the partition key for locality-aware scheduling. Agents
	// with the same partition key may be scheduled on the same node to improve
	// data locality and reduce network overhead.
	Partition() string
}

// BaseAgent provides a concrete implementation of the Agent interface, handling
// common functionalities such as lifecycle callbacks and agent hierarchy.
type BaseAgent struct {
	id           string
	name         string
	description  string
	capabilities []string
	dependencies []Agent
	partition    string
	executor     AgentExecutor
	// outputKey is an ADK-compatible feature that automatically stores the agent's
	// result in the context with the specified key.
	outputKey string

	// Lifecycle callbacks (ADK-compatible)
	beforeExecute []AgentCallback
	afterExecute  []AgentOutputCallback
	onError       []ErrorCallback

	// Agent hierarchy support (ADK-compatible)
	parent    Agent   // Parent agent reference
	subAgents []Agent // Child agents
	mu        sync.RWMutex // Thread-safe access to hierarchy
}

// AgentInput represents the input for an agent execution.
type AgentInput struct {
	// TaskID is a unique identifier for the specific task being executed.
	TaskID string
	// Instruction is the primary directive or question for the agent.
	Instruction string
	// Context provides a key-value store for arbitrary data that can be passed
	// between agents and tasks.
	Context map[string]interface{}
	// Tools is a list of tools available to the agent for this specific task.
	Tools []types.ToolDefinition
	// Model is the identifier for the language model to be used.
	Model string
	// MaxRetries is the maximum number of times to retry a failed execution.
	MaxRetries int
	// Timeout is the maximum duration for the execution.
	Timeout time.Duration
}

// AgentOutput represents the result of an agent execution.
type AgentOutput struct {
	// TaskID is the unique identifier for the task that produced this output.
	TaskID string
	// Result is the primary output of the agent's execution.
	Result interface{}
	// Metadata provides a key-value store for any additional information about
	// the execution, such as performance metrics or finish reasons.
	Metadata map[string]interface{}
	// Error holds any error that occurred during execution.
	Error error
	// ExecutionLog provides a step-by-step lineage of the execution, tracking
	// actions, inputs, outputs, and durations.
	ExecutionLog []ExecutionStep
	// Metrics contains performance metrics for the execution.
	Metrics *ExecutionMetrics
}

// ExecutionStep tracks a single step within an agent's execution for lineage
// and debugging purposes.
type ExecutionStep struct {
	// StepID is a unique identifier for this step.
	StepID string
	// Timestamp is the time the step was executed.
	Timestamp time.Time
	// Action is a description of the action being performed (e.g., "call_tool", "llm_generate").
	Action string
	// Input is the input to the action.
	Input interface{}
	// Output is the output of the action.
	Output interface{}
	// Duration is the time taken to execute the step.
	Duration time.Duration
}

// ExecutionMetrics tracks performance and operational metrics for an agent execution.
type ExecutionMetrics struct {
	// StartTime is the time the execution started.
	StartTime time.Time
	// EndTime is the time the execution finished.
	EndTime time.Time
	// Duration is the total execution time.
	Duration time.Duration
	// ToolCallCount is the number of times a tool was called.
	ToolCallCount int
	// TokensUsed is the number of language model tokens used.
	TokensUsed int
	// RetryCount is the number of times the execution was retried.
	RetryCount int
	// PartitionHits is the number of times the execution was able to leverage
	// data locality.
	PartitionHits int
}

// AgentExecutor is responsible for the concrete implementation of an agent's
// execution logic. This allows for different execution strategies (e.g., LLM-based,
// simple function calls) to be plugged into the agent.
type AgentExecutor interface {
	// Execute performs the agent's logic with the given input and context.
	Execute(ctx context.Context, agent Agent, input *AgentInput) (*AgentOutput, error)
}

// ExecutorFunc is a function adapter that allows a simple function to be used
// as an AgentExecutor.
type ExecutorFunc func(ctx context.Context, agent Agent, input *AgentInput) (*AgentOutput, error)

// Execute implements the AgentExecutor interface for ExecutorFunc.
func (f ExecutorFunc) Execute(ctx context.Context, agent Agent, input *AgentInput) (*AgentOutput, error) {
	return f(ctx, agent, input)
}

func unwrapBaseAgent(agent Agent) (*BaseAgent, bool) {
	if agent == nil {
		return nil, false
	}
	if base, ok := agent.(*BaseAgent); ok {
		return base, true
	}
	value := reflect.ValueOf(agent)
	if !value.IsValid() || value.Kind() != reflect.Ptr || value.IsNil() {
		return nil, false
	}
	elem := value.Elem()
	if !elem.IsValid() || elem.Kind() != reflect.Struct {
		return nil, false
	}
	baseField := elem.FieldByName("BaseAgent")
	if !baseField.IsValid() || baseField.Kind() != reflect.Ptr || baseField.IsNil() {
		return nil, false
	}
	base, ok := baseField.Interface().(*BaseAgent)
	return base, ok && base != nil
}



// NewAgent creates a new BaseAgent with the specified configuration. It
// initializes the agent's properties, sets up its executor, and configures its
// lifecycle callbacks and sub-agents.
func NewAgent(config AgentConfig) *BaseAgent {
	agent := &BaseAgent{
		id:           uuid.New().String(),
		name:         config.Name,
		description:  config.Description,
		capabilities: config.Capabilities,
		dependencies: config.Dependencies,
		partition:    config.Partition,
		executor:     config.Executor,
		outputKey:    config.OutputKey,
		subAgents:    make([]Agent, 0),

		// Initialize callbacks (ADK-compatible)
		beforeExecute: config.BeforeExecute,
		afterExecute:  config.AfterExecute,
		onError:       config.OnError,
	}

	// Set up sub-agent parent references (ADK-compatible)
	if len(config.SubAgents) > 0 {
		for _, subAgent := range config.SubAgents {
			if err := agent.AddSubAgent(subAgent); err != nil {
				// Log error but don't fail agent creation
				fmt.Printf("Warning: failed to add sub-agent %s: %v\n", subAgent.Name(), err)
			}
		}
	}

	return agent
}

// AgentConfig holds the configuration for creating a new agent.
type AgentConfig struct {
	// Name is the human-readable name of the agent.
	Name string
	// Description is a brief explanation of what the agent does.
	Description string
	// Capabilities is a list of tools or other capabilities the agent possesses.
	Capabilities []string
	// Dependencies is a slice of Agents that must be successfully executed
	// before this agent can run.
	Dependencies []Agent
	// Partition is the partition key for locality-aware scheduling.
	Partition string
	// Executor is the concrete implementation of the agent's execution logic.
	Executor AgentExecutor
	// SubAgents is a list of child agents, enabling hierarchical agent structures.
	SubAgents []Agent
	// OutputKey is an ADK-compatible feature that automatically stores the agent's
	// result in the context with the specified key.
	OutputKey string

	// Lifecycle callbacks (ADK-compatible)
	// BeforeExecute is a slice of callbacks to be executed before the agent's
	// main logic.
	BeforeExecute []AgentCallback
	// AfterExecute is a slice of callbacks to be executed after the agent's
	// main logic has completed successfully.
	AfterExecute []AgentOutputCallback
	// OnError is a slice of callbacks to be executed if an error occurs during
	// the agent's execution.
	OnError []ErrorCallback
}

// ID returns the unique identifier for this agent.
func (a *BaseAgent) ID() string { return a.id }

// Name returns the human-readable name of the agent.
func (a *BaseAgent) Name() string { return a.name }

// Description returns a brief explanation of what this agent does.
func (a *BaseAgent) Description() string { return a.description }

// Capabilities returns a list of tools or other capabilities this agent possesses.
func (a *BaseAgent) Capabilities() []string { return a.capabilities }

// Dependencies returns a slice of Agents that must be successfully executed
// before this agent can run.
func (a *BaseAgent) Dependencies() []Agent { return a.dependencies }

// Partition returns the partition key for locality-aware scheduling.
func (a *BaseAgent) Partition() string { return a.partition }

// Execute runs the agent's logic. It orchestrates the execution, including
// running callbacks, handling errors, and storing the output.
func (a *BaseAgent) Execute(ctx context.Context, input *AgentInput) (*AgentOutput, error) {
	// Start a new span for the agent execution
	tracer := telemetry.GetGlobalTelemetry().GetTracer()
	ctx, span := tracer.StartSpan(ctx, "agent.execute")
	defer span.End()

	span.SetAttribute("agent.id", a.id)
	span.SetAttribute("agent.name", a.name)
	span.SetAttribute("agent.partition", a.partition)

	if a.executor == nil {
		err := fmt.Errorf("no executor configured for agent %s", a.name)
		span.SetStatus(telemetry.StatusError, err.Error())
		return nil, err
	}

	// ADK-compatible: Execute BeforeExecute callbacks
	if len(a.beforeExecute) > 0 {
		executor := NewCallbackExecutor(false) // Don't stop on callback errors
		if err := executor.ExecuteAgentCallbacks(ctx, a.beforeExecute, a, input); err != nil {
			// Log warning but continue (ADK-compatible behavior)
			fmt.Printf("Warning: before_execute callback for agent %s: %v\n", a.name, err)
		}
	}

	// ADK-compatible: Interpolate templates in instruction before execution
	if input.Instruction != "" && HasTemplates(input.Instruction) {
		interpolated, err := InterpolateInstruction(input.Instruction, input.Context)
		if err != nil {
			// Log warning but continue with interpolated result
			// This matches ADK's lenient behavior
			fmt.Printf("Warning: template interpolation for agent %s: %v\n", a.name, err)
		}
		input.Instruction = interpolated
	}

	// Execute the agent
	output, err := a.executor.Execute(ctx, a, input)
	if err != nil {
		// ADK-compatible: Execute OnError callbacks
		if len(a.onError) > 0 {
			executor := NewCallbackExecutor(false)
			if cbErr := executor.ExecuteErrorCallbacks(ctx, a.onError, a, input, err); cbErr != nil {
				fmt.Printf("Warning: error callback for agent %s: %v\n", a.name, cbErr)
			}
		}
		return nil, err
	}

	// Ensure metadata map is initialized
	if output != nil && output.Metadata == nil {
		output.Metadata = make(map[string]interface{})
	}

	// ADK-compatible: Auto-store result to OutputKey if specified
	if a.outputKey != "" && output != nil {
		// Ensure Context map exists
		if input.Context == nil {
			input.Context = make(map[string]interface{})
		}

		// Store result to the specified key
		// This enables subsequent agents to access the result by key
		input.Context[a.outputKey] = output.Result

		// Also store in output metadata for visibility
		output.Metadata["output_key"] = a.outputKey
		output.Metadata["stored_to_context"] = true
	}

	// ADK-compatible: Execute AfterExecute callbacks
	if len(a.afterExecute) > 0 {
		executor := NewCallbackExecutor(false)
		if err := executor.ExecuteOutputCallbacks(ctx, a.afterExecute, a, input, output); err != nil {
			fmt.Printf("Warning: after_execute callback for agent %s: %v\n", a.name, err)
		}
	}

	return output, nil
}

// Agent Hierarchy Methods (ADK-compatible)

// Parent returns the parent agent, or nil if this is a root agent.
func (a *BaseAgent) Parent() Agent {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.parent
}

// SubAgents returns a slice of the agent's direct children.
func (a *BaseAgent) SubAgents() []Agent {
	a.mu.RLock()
	defer a.mu.RUnlock()
	// Return a copy to prevent external modification
	agents := make([]Agent, len(a.subAgents))
	copy(agents, a.subAgents)
	return agents
}

// AddSubAgent adds a child agent to this agent's list of sub-agents and sets
// this agent as the child's parent. It returns an error if the child agent
// already has a parent, enforcing a single-parent rule.
func (a *BaseAgent) AddSubAgent(child Agent) error {
	if child == nil {
		return fmt.Errorf("cannot add nil sub-agent")
	}

	// Check if child is a BaseAgent (so we can set parent)
	baseChild, ok := unwrapBaseAgent(child)
	if !ok {
		return fmt.Errorf("sub-agent must be *BaseAgent to support hierarchy")
	}

	// Check single parent rule
	baseChild.mu.Lock()
	defer baseChild.mu.Unlock()

	if baseChild.parent != nil {
		return fmt.Errorf("agent %s already has a parent %s (single parent rule)",
			child.Name(), baseChild.parent.Name())
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Set parent reference
	baseChild.parent = a

	// Add to sub-agents list
	a.subAgents = append(a.subAgents, child)

	return nil
}

// RemoveSubAgent removes a child agent from this agent's list of sub-agents
// and clears the child's parent reference.
func (a *BaseAgent) RemoveSubAgent(child Agent) error {
	if child == nil {
		return fmt.Errorf("cannot remove nil sub-agent")
	}

	baseChild, ok := unwrapBaseAgent(child)
	if !ok {
		return fmt.Errorf("sub-agent must be *BaseAgent")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Find and remove from sub-agents list
	found := false
	for i, sub := range a.subAgents {
		if sub.ID() == child.ID() {
			a.subAgents = append(a.subAgents[:i], a.subAgents[i+1:]...)
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("agent %s is not a sub-agent of %s", child.Name(), a.name)
	}

	// Clear parent reference
	baseChild.mu.Lock()
	baseChild.parent = nil
	baseChild.mu.Unlock()

	return nil
}

// FindAgent searches the agent hierarchy for a descendant agent with the given
// name. It returns the agent if found, or an error if not found.
func (a *BaseAgent) FindAgent(name string) (Agent, error) {
	return a.findAgent(name, map[string]bool{})
}

func (a *BaseAgent) findAgent(name string, visited map[string]bool) (Agent, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if visited[a.id] {
		return nil, fmt.Errorf("agent %s not found in hierarchy", name)
	}
	visited[a.id] = true

	// Check direct children
	for _, child := range a.subAgents {
		if child.Name() == name {
			return child, nil
		}
	}

	// Recursively search descendants
	for _, child := range a.subAgents {
		if baseChild, ok := unwrapBaseAgent(child); ok {
			if found, err := baseChild.findAgent(name, visited); err == nil {
				return found, nil
			}
		}
	}

	return nil, fmt.Errorf("agent %s not found in hierarchy", name)
}

// FindAgentByID searches the agent hierarchy for a descendant agent with the
// given ID. It returns the agent if found, or an error if not found.
func (a *BaseAgent) FindAgentByID(id string) (Agent, error) {
	return a.findAgentByID(id, map[string]bool{})
}

func (a *BaseAgent) findAgentByID(id string, visited map[string]bool) (Agent, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if visited[a.id] {
		return nil, fmt.Errorf("agent with ID %s not found in hierarchy", id)
	}
	visited[a.id] = true

	// Check direct children
	for _, child := range a.subAgents {
		if child.ID() == id {
			return child, nil
		}
	}

	// Recursively search descendants
	for _, child := range a.subAgents {
		if baseChild, ok := unwrapBaseAgent(child); ok {
			if found, err := baseChild.findAgentByID(id, visited); err == nil {
				return found, nil
			}
		}
	}

	return nil, fmt.Errorf("agent with ID %s not found in hierarchy", id)
}

// GetRoot returns the root agent in the hierarchy.
func (a *BaseAgent) GetRoot() Agent {
	a.mu.RLock()
	current := Agent(a)
	a.mu.RUnlock()
	visited := map[string]bool{}

	for {
		if baseAgent, ok := unwrapBaseAgent(current); ok {
			if visited[baseAgent.id] {
				return current
			}
			visited[baseAgent.id] = true
			parent := baseAgent.Parent()
			if parent == nil {
				return current
			}
			current = parent
		} else {
			return current
		}
	}
}

// GetPath returns the path from the root of the hierarchy to this agent as a
// slice of agents.
func (a *BaseAgent) GetPath() []Agent {
	path := []Agent{}
	current := Agent(a)
	visited := map[string]bool{}

	for current != nil {
		path = append([]Agent{current}, path...)
		if baseAgent, ok := unwrapBaseAgent(current); ok {
			if visited[baseAgent.id] {
				break
			}
			visited[baseAgent.id] = true
			current = baseAgent.Parent()
		} else {
			break
		}
	}

	return path
}

// GetDepth returns the depth of this agent in the hierarchy, with the root
// agent being at depth 0.
func (a *BaseAgent) GetDepth() int {
	depth := 0
	current := Agent(a)
	visited := map[string]bool{}

	for current != nil {
		if baseAgent, ok := unwrapBaseAgent(current); ok {
			if visited[baseAgent.id] {
				break
			}
			visited[baseAgent.id] = true
			parent := baseAgent.Parent()
			if parent == nil {
				break
			}
			depth++
			current = parent
		} else {
			break
		}
	}

	return depth
}

// AddDependency adds a dependency agent to this agent.
func (a *BaseAgent) AddDependency(dep Agent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dependencies = append(a.dependencies, dep)
}

// AgentTask represents a unit of work to be executed by an agent
// Similar to Spark's Task abstraction
type AgentTask struct {
	ID           string
	AgentID      string
	Input        *AgentInput
	Stage        int
	Attempt      int
	PreferredLoc string // Locality preference
	State        TaskState
	CreatedAt    time.Time
	StartedAt    time.Time
	CompletedAt  time.Time
	Output       *AgentOutput
}

// TaskState represents the execution state of a task
type TaskState int

const (
	TaskPending TaskState = iota
	TaskScheduled
	TaskRunning
	TaskCompleted
	TaskFailed
	TaskRetrying
	TaskPolicyBlocked // Added
)

func (s TaskState) String() string {
	return [...]string{
		"PENDING",
		"SCHEDULED",
		"RUNNING",
		"COMPLETED",
		"FAILED",
		"RETRYING",
		"POLICY_BLOCKED",
	}[s]
}

// AgentDAG represents a directed acyclic graph of agent tasks
// Inspired by Spark's DAG scheduling
type AgentDAG struct {
	ID     string
	Name   string
	Stages []*AgentStage
	Root   Agent
}

// AgentStage groups tasks that can be executed in parallel
// Similar to Spark's Stage abstraction
type AgentStage struct {
	ID           int
	Tasks        []*AgentTask
	Dependencies []*AgentStage
	IsShuffleMap bool // Indicates if stage produces intermediate results
}

// NewAgentDAG creates a DAG from a root agent
func NewAgentDAG(root Agent, input *AgentInput) (*AgentDAG, error) {
	dag := &AgentDAG{
		ID:     uuid.New().String(),
		Name:   fmt.Sprintf("DAG-%s", root.Name()),
		Root:   root,
		Stages: make([]*AgentStage, 0),
	}

	// Build stages from agent dependencies
	if err := dag.buildStages(root, input); err != nil {
		return nil, err
	}

	return dag, nil
}

// buildStages recursively builds stages from agent dependencies
func (d *AgentDAG) buildStages(agent Agent, input *AgentInput) error {
	visited := make(map[string]bool)
	stageMap := make(map[int][]*AgentTask)

	var buildRecursive func(Agent, int) error
	buildRecursive = func(a Agent, depth int) error {
		if visited[a.ID()] {
			return nil
		}
		visited[a.ID()] = true

		// Process dependencies first (deeper stages)
		for _, dep := range a.Dependencies() {
			if err := buildRecursive(dep, depth+1); err != nil {
				return err
			}
		}

		// Create task for this agent
		task := &AgentTask{
			ID:           uuid.New().String(),
			AgentID:      a.ID(),
			Input:        input,
			Stage:        depth,
			Attempt:      0,
			PreferredLoc: a.Partition(),
			State:        TaskPending,
			CreatedAt:    time.Now(),
		}

		stageMap[depth] = append(stageMap[depth], task)
		return nil
	}

	if err := buildRecursive(agent, 0); err != nil {
		return err
	}

	// Convert stageMap to ordered stages
	maxDepth := 0
	for depth := range stageMap {
		if depth > maxDepth {
			maxDepth = depth
		}
	}

	for i := maxDepth; i >= 0; i-- {
		if tasks, ok := stageMap[i]; ok {
			stage := &AgentStage{
				ID:           i,
				Tasks:        tasks,
				Dependencies: make([]*AgentStage, 0),
				IsShuffleMap: i > 0, // Non-final stages are shuffle map stages
			}
			d.Stages = append(d.Stages, stage)
		}
	}

	return nil
}
