// Package planner provides task decomposition and planning for AI agents
// Inspired by hierarchical task planning and ADK's planning patterns
package planner

import (
	"context"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/google/uuid"
)

// Planner decomposes complex tasks into executable DAGs
type Planner interface {
	// CreatePlan generates an execution plan for an objective
	CreatePlan(ctx context.Context, objective string, constraints PlanConstraints) (*Plan, error)

	// ValidatePlan checks if a plan is valid
	ValidatePlan(plan *Plan) error

	// OptimizePlan optimizes the plan for better execution
	OptimizePlan(plan *Plan) (*Plan, error)
}

// Plan represents an execution plan
type Plan struct {
	ID           string
	Objective    string
	Steps        []*PlanStep
	DAG          *agent.AgentDAG
	Estimated    time.Duration
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Status       PlanStatus
	Metadata     map[string]interface{}
}

// PlanStep represents a single step in the plan
type PlanStep struct {
	ID           string
	Description  string
	AgentID      string
	AgentType    string
	Dependencies []string // IDs of steps that must complete first
	Input        *agent.AgentInput
	ExpectedOutput interface{}
	Timeout      time.Duration
	Retries      int
	Priority     int
	Metadata     map[string]interface{}
}

// PlanConstraints defines constraints for planning
type PlanConstraints struct {
	MaxSteps         int
	MaxParallelism   int
	Timeout          time.Duration
	RequiredAgents   []string
	ForbiddenAgents  []string
	PreferredPattern PlanPattern
	Budget           *ResourceBudget
}

// ResourceBudget defines resource constraints
type ResourceBudget struct {
	MaxTokens    int
	MaxCost      float64
	MaxDuration  time.Duration
	MaxExecutors int
}

// PlanPattern defines execution patterns
type PlanPattern string

const (
	PatternSequential   PlanPattern = "sequential"
	PatternParallel     PlanPattern = "parallel"
	PatternPipeline     PlanPattern = "pipeline"
	PatternMapReduce    PlanPattern = "map_reduce"
	PatternHierarchical PlanPattern = "hierarchical"
)

// PlanStatus represents the plan state
type PlanStatus string

const (
	PlanStatusDraft     PlanStatus = "draft"
	PlanStatusValidated PlanStatus = "validated"
	PlanStatusExecuting PlanStatus = "executing"
	PlanStatusCompleted PlanStatus = "completed"
	PlanStatusFailed    PlanStatus = "failed"
)

// DefaultPlanner implements basic planning logic
type DefaultPlanner struct {
	agentRegistry map[string]agent.Agent
	modelProvider ModelProvider
}

// ModelProvider provides AI model access for planning
type ModelProvider interface {
	GeneratePlan(ctx context.Context, objective string, constraints PlanConstraints) (*Plan, error)
}

// NewDefaultPlanner creates a new planner
func NewDefaultPlanner(agentRegistry map[string]agent.Agent, modelProvider ModelProvider) *DefaultPlanner {
	return &DefaultPlanner{
		agentRegistry: agentRegistry,
		modelProvider: modelProvider,
	}
}

// CreatePlan generates an execution plan
func (p *DefaultPlanner) CreatePlan(ctx context.Context, objective string, constraints PlanConstraints) (*Plan, error) {
	// Use AI model to generate plan if available
	if p.modelProvider != nil {
		return p.modelProvider.GeneratePlan(ctx, objective, constraints)
	}

	// Fallback to simple planning
	return p.createSimplePlan(objective, constraints)
}

// createSimplePlan creates a basic sequential plan
func (p *DefaultPlanner) createSimplePlan(objective string, constraints PlanConstraints) (*Plan, error) {
	plan := &Plan{
		ID:        uuid.New().String(),
		Objective: objective,
		Steps:     make([]*PlanStep, 0),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Status:    PlanStatusDraft,
		Metadata:  make(map[string]interface{}),
	}

	// Create a simple single-step plan
	step := &PlanStep{
		ID:           uuid.New().String(),
		Description:  objective,
		Dependencies: make([]string, 0),
		Input: &agent.AgentInput{
			Instruction: objective,
			Context:     make(map[string]interface{}),
			Timeout:     constraints.Timeout,
		},
		Timeout:  constraints.Timeout,
		Retries:  3,
		Priority: 1,
		Metadata: make(map[string]interface{}),
	}

	plan.Steps = append(plan.Steps, step)
	plan.Estimated = constraints.Timeout

	return plan, nil
}

// ValidatePlan checks if a plan is valid
func (p *DefaultPlanner) ValidatePlan(plan *Plan) error {
	if plan == nil {
		return fmt.Errorf("plan is nil")
	}

	if plan.Objective == "" {
		return fmt.Errorf("plan objective is empty")
	}

	if len(plan.Steps) == 0 {
		return fmt.Errorf("plan has no steps")
	}

	// Check for circular dependencies
	if err := p.checkCircularDependencies(plan); err != nil {
		return err
	}

	// Validate each step
	for _, step := range plan.Steps {
		if err := p.validateStep(step, plan); err != nil {
			return fmt.Errorf("invalid step %s: %w", step.ID, err)
		}
	}

	return nil
}

// validateStep validates a single step
func (p *DefaultPlanner) validateStep(step *PlanStep, plan *Plan) error {
	if step.ID == "" {
		return fmt.Errorf("step ID is empty")
	}

	if step.Description == "" {
		return fmt.Errorf("step description is empty")
	}

	// Validate dependencies exist
	for _, depID := range step.Dependencies {
		found := false
		for _, s := range plan.Steps {
			if s.ID == depID {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("dependency %s not found", depID)
		}
	}

	return nil
}

// checkCircularDependencies detects circular dependencies in the plan
func (p *DefaultPlanner) checkCircularDependencies(plan *Plan) error {
	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	for _, step := range plan.Steps {
		if err := p.detectCycle(step.ID, plan, visited, recStack); err != nil {
			return err
		}
	}

	return nil
}

// detectCycle uses DFS to detect cycles
func (p *DefaultPlanner) detectCycle(stepID string, plan *Plan, visited, recStack map[string]bool) error {
	visited[stepID] = true
	recStack[stepID] = true

	// Find the step
	var currentStep *PlanStep
	for _, s := range plan.Steps {
		if s.ID == stepID {
			currentStep = s
			break
		}
	}

	if currentStep == nil {
		return nil
	}

	// Check all dependencies
	for _, depID := range currentStep.Dependencies {
		if !visited[depID] {
			if err := p.detectCycle(depID, plan, visited, recStack); err != nil {
				return err
			}
		} else if recStack[depID] {
			return fmt.Errorf("circular dependency detected: %s -> %s", stepID, depID)
		}
	}

	recStack[stepID] = false
	return nil
}

// OptimizePlan optimizes the plan for better execution
func (p *DefaultPlanner) OptimizePlan(plan *Plan) (*Plan, error) {
	if err := p.ValidatePlan(plan); err != nil {
		return nil, fmt.Errorf("cannot optimize invalid plan: %w", err)
	}

	optimized := &Plan{
		ID:        plan.ID,
		Objective: plan.Objective,
		Steps:     make([]*PlanStep, len(plan.Steps)),
		CreatedAt: plan.CreatedAt,
		UpdatedAt: time.Now(),
		Status:    plan.Status,
		Metadata:  plan.Metadata,
	}

	copy(optimized.Steps, plan.Steps)

	// Optimize: Reorder steps to maximize parallelism
	optimized.Steps = p.reorderForParallelism(optimized.Steps)

	// Optimize: Assign priorities based on critical path
	p.assignPriorities(optimized.Steps)

	// Recalculate estimated duration
	optimized.Estimated = p.estimateDuration(optimized.Steps)

	return optimized, nil
}

// reorderForParallelism reorders steps to maximize parallel execution
func (p *DefaultPlanner) reorderForParallelism(steps []*PlanStep) []*PlanStep {
	// Build dependency graph
	levels := make(map[int][]*PlanStep)
	stepLevels := make(map[string]int)

	// Calculate levels (topological sort)
	for _, step := range steps {
		level := 0
		for _, depID := range step.Dependencies {
			if depLevel, exists := stepLevels[depID]; exists {
				if depLevel+1 > level {
					level = depLevel + 1
				}
			}
		}
		stepLevels[step.ID] = level
		levels[level] = append(levels[level], step)
	}

	// Flatten back to array
	reordered := make([]*PlanStep, 0, len(steps))
	for i := 0; i < len(levels); i++ {
		if levelSteps, exists := levels[i]; exists {
			reordered = append(reordered, levelSteps...)
		}
	}

	return reordered
}

// assignPriorities assigns priorities based on critical path
func (p *DefaultPlanner) assignPriorities(steps []*PlanStep) {
	for i, step := range steps {
		// Higher priority for earlier steps
		step.Priority = len(steps) - i
	}
}

// estimateDuration estimates the total duration of the plan
func (p *DefaultPlanner) estimateDuration(steps []*PlanStep) time.Duration {
	// Build dependency levels
	stepLevels := make(map[string]int)

	for _, step := range steps {
		level := 0
		for _, depID := range step.Dependencies {
			if depLevel, exists := stepLevels[depID]; exists {
				if depLevel+1 > level {
					level = depLevel + 1
				}
			}
		}
		stepLevels[step.ID] = level
	}

	// Calculate duration per level
	levelDurations := make(map[int]time.Duration)
	for _, step := range steps {
		level := stepLevels[step.ID]
		if step.Timeout > levelDurations[level] {
			levelDurations[level] = step.Timeout
		}
	}

	// Sum up level durations
	var total time.Duration
	for _, duration := range levelDurations {
		total += duration
	}

	return total
}

// PlanToDAG converts a plan to an AgentDAG
func PlanToDAG(plan *Plan, agentRegistry map[string]agent.Agent) (*agent.AgentDAG, error) {
	// Build agent map from steps
	agentMap := make(map[string]agent.Agent)

	for _, step := range plan.Steps {
		if step.AgentID != "" {
			if ag, exists := agentRegistry[step.AgentID]; exists {
				agentMap[step.ID] = ag
			} else {
				// Create a synthetic agent for this step
				agentMap[step.ID] = &agent.BaseAgent{}
			}
		}
	}

	// Build dependency graph
	// This is a simplified version - in production, you'd build a proper DAG
	dag := &agent.AgentDAG{
		ID:     plan.ID,
		Stages: make([]*agent.AgentStage, 0),
	}

	return dag, nil
}
