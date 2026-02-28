package backend

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/seyi/dagens/pkg/graph"
)

// LocalBackend executes graphs in the current process.
// This is the simplest backend implementation with no distribution.
type LocalBackend struct {
	name           string
	results        map[string]*ExecutionResult
	resultsMu      sync.RWMutex
	maxConcurrency int
}

// LocalBackendConfig holds configuration for LocalBackend.
type LocalBackendConfig struct {
	Name           string
	MaxConcurrency int // Maximum concurrent graph executions (0 = unlimited)
}

// NewLocalBackend creates a new local backend.
func NewLocalBackend() *LocalBackend {
	return NewLocalBackendWithConfig(LocalBackendConfig{
		Name:           "local",
		MaxConcurrency: 0, // Unlimited by default
	})
}

// NewLocalBackendWithConfig creates a new local backend with configuration.
func NewLocalBackendWithConfig(cfg LocalBackendConfig) *LocalBackend {
	name := cfg.Name
	if name == "" {
		name = "local"
	}

	return &LocalBackend{
		name:           name,
		results:        make(map[string]*ExecutionResult),
		maxConcurrency: cfg.MaxConcurrency,
	}
}

// Name returns the backend name.
func (b *LocalBackend) Name() string {
	return b.name
}

// Execute runs the graph synchronously and returns the result.
func (b *LocalBackend) Execute(ctx context.Context, g *graph.Graph, state graph.State) (*ExecutionResult, error) {
	// Validate graph before execution
	if err := g.Validate(); err != nil {
		return nil, fmt.Errorf("graph validation failed: %w", err)
	}

	// Create execution result
	executionID := uuid.New().String()
	result := NewExecutionResult(executionID, g.ID())
	result.Status = StatusRunning
	result.State = state

	// Store result
	b.storeResult(result)

	// Execute the graph
	err := b.executeGraph(ctx, g, state, result)

	// Update result status
	if err != nil {
		result.Fail(err)
	} else {
		result.Complete()
	}

	return result, err
}

// ExecuteAsync runs the graph asynchronously and returns an execution ID.
func (b *LocalBackend) ExecuteAsync(ctx context.Context, g *graph.Graph, state graph.State) (string, error) {
	// Validate graph before execution
	if err := g.Validate(); err != nil {
		return "", fmt.Errorf("graph validation failed: %w", err)
	}

	// Create execution result
	executionID := uuid.New().String()
	result := NewExecutionResult(executionID, g.ID())
	result.State = state.Clone() // Clone state for async execution

	// Store result
	b.storeResult(result)

	// Launch goroutine for execution
	go func() {
		result.Status = StatusRunning

		err := b.executeGraph(ctx, g, result.State, result)

		// Update result status
		if err != nil {
			result.Fail(err)
		} else {
			result.Complete()
		}
	}()

	return executionID, nil
}

// GetResult retrieves the result of an async execution.
func (b *LocalBackend) GetResult(executionID string) (*ExecutionResult, error) {
	b.resultsMu.RLock()
	defer b.resultsMu.RUnlock()

	result, exists := b.results[executionID]
	if !exists {
		return nil, fmt.Errorf("execution %s not found", executionID)
	}

	return result, nil
}

// Close closes the backend and releases resources.
func (b *LocalBackend) Close() error {
	b.resultsMu.Lock()
	defer b.resultsMu.Unlock()

	// Clear results
	b.results = make(map[string]*ExecutionResult)

	return nil
}

// storeResult stores an execution result.
func (b *LocalBackend) storeResult(result *ExecutionResult) {
	b.resultsMu.Lock()
	defer b.resultsMu.Unlock()
	b.results[result.ExecutionID] = result
}

// executeGraph executes the graph by traversing nodes from entry to finish.
func (b *LocalBackend) executeGraph(ctx context.Context, g *graph.Graph, state graph.State, result *ExecutionResult) error {
	// Start from entry node
	currentNodeID := g.Entry()
	finishNodes := make(map[string]bool)
	for _, fn := range g.FinishNodes() {
		finishNodes[fn] = true
	}

	visited := make(map[string]bool)
	maxSteps := 1000 // Prevent infinite loops

	for step := 0; step < maxSteps; step++ {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check if we've reached a finish node
		if finishNodes[currentNodeID] {
			// Execute the finish node
			node, err := g.GetNode(currentNodeID)
			if err != nil {
				return fmt.Errorf("failed to get finish node %s: %w", currentNodeID, err)
			}

			if err := node.Execute(ctx, state); err != nil {
				return fmt.Errorf("finish node %s execution failed: %w", currentNodeID, err)
			}

			result.AddNode(currentNodeID)
			return nil // Successfully reached finish
		}

		// Check for cycles
		if visited[currentNodeID] {
			// Allow revisiting nodes, but track it
			// This enables loop patterns
		}
		visited[currentNodeID] = true

		// Get and execute current node
		node, err := g.GetNode(currentNodeID)
		if err != nil {
			return fmt.Errorf("failed to get node %s: %w", currentNodeID, err)
		}

		if err := node.Execute(ctx, state); err != nil {
			return fmt.Errorf("node %s execution failed: %w", currentNodeID, err)
		}

		result.AddNode(currentNodeID)

		// Find next node via edges
		edges := g.GetEdges(currentNodeID)
		if len(edges) == 0 {
			// No outgoing edges and not a finish node
			return fmt.Errorf("node %s has no outgoing edges and is not a finish node", currentNodeID)
		}

		// Find the first edge that should be traversed
		var nextNodeID string
		for _, edge := range edges {
			if edge.ShouldTraverse(state) {
				// Handle dynamic edges
				if dynEdge, ok := edge.(*graph.DynamicEdge); ok {
					nextNodeID = dynEdge.ToWithState(state)
				} else {
					nextNodeID = edge.To()
				}
				break
			}
		}

		if nextNodeID == "" {
			return fmt.Errorf("no traversable edge found from node %s", currentNodeID)
		}

		// Move to next node
		currentNodeID = nextNodeID
	}

	return fmt.Errorf("execution exceeded maximum steps (%d), possible infinite loop", maxSteps)
}

// ClearResults removes all stored execution results.
func (b *LocalBackend) ClearResults() {
	b.resultsMu.Lock()
	defer b.resultsMu.Unlock()
	b.results = make(map[string]*ExecutionResult)
}

// AllResults returns all stored execution results.
func (b *LocalBackend) AllResults() []*ExecutionResult {
	b.resultsMu.RLock()
	defer b.resultsMu.RUnlock()

	results := make([]*ExecutionResult, 0, len(b.results))
	for _, result := range b.results {
		results = append(results, result)
	}
	return results
}
