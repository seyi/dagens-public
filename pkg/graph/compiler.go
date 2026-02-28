package graph

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/scheduler"
)

// DAGCompiler compiles a Graph into a scheduler.Job
type DAGCompiler struct {
	// Options can be added here
}

// CompileOptions holds optional parameters for compilation
type CompileOptions struct {
	// SessionID enables sticky scheduling. When provided, all tasks in the job
	// will use this as their PartitionKey, ensuring they route to the same worker.
	SessionID string
}

// NewDAGCompiler creates a new compiler
func NewDAGCompiler() *DAGCompiler {
	return &DAGCompiler{}
}

// Compile transforms a Graph into a Job (calls CompileWithOptions with default options)
func (c *DAGCompiler) Compile(g *Graph, input *agent.AgentInput) (*scheduler.Job, error) {
	return c.CompileWithOptions(g, input, CompileOptions{})
}

// CompileWithOptions transforms a Graph into a Job with the specified options.
// It uses Kahn's algorithm for topological sorting to correctly handle DAGs,
// including diamond patterns where multiple paths converge.
func (c *DAGCompiler) CompileWithOptions(g *Graph, input *agent.AgentInput, opts CompileOptions) (*scheduler.Job, error) {
	if err := g.Validate(); err != nil {
		return nil, fmt.Errorf("invalid graph: %w", err)
	}

	job := scheduler.NewJob(uuid.New().String(), g.Name())

	// Build the compilation context
	ctx := &compilationContext{
		graph:     g,
		job:       job,
		input:     input,
		sessionID: opts.SessionID,
		compiler:  c,
	}

	// Perform topological sort using Kahn's algorithm
	sortedLevels, err := ctx.topologicalSort()
	if err != nil {
		return nil, fmt.Errorf("topological sort failed: %w", err)
	}

	// Convert sorted levels into stages
	if err := ctx.buildStages(sortedLevels); err != nil {
		return nil, fmt.Errorf("stage building failed: %w", err)
	}

	// Capture logical edges for UI visualization
	for fromID, edgeList := range g.edges {
		for _, e := range edgeList {
			job.Edges = append(job.Edges, scheduler.Edge{
				From: fromID,
				To:   e.To(),
			})
		}
	}

	return job, nil
}

// compilationContext holds state during the compilation process
type compilationContext struct {
	graph     *Graph
	job       *scheduler.Job
	input     *agent.AgentInput
	sessionID string
	compiler  *DAGCompiler
}

// topologicalSort performs Kahn's algorithm to produce a level-ordered
// list of node IDs. Nodes at the same level have no dependencies on each other
// and can potentially execute in parallel.
// Returns: [][]string where each inner slice is a "level" of nodes
func (ctx *compilationContext) topologicalSort() ([][]string, error) {
	// Step 1: Build in-degree map and reverse adjacency (predecessors)
	inDegree := make(map[string]int)
	allNodes := ctx.graph.AllNodes()

	// Initialize in-degree for all nodes
	for _, node := range allNodes {
		inDegree[node.ID()] = 0
	}

	// Calculate in-degrees based on edges
	for _, node := range allNodes {
		edges := ctx.graph.GetEdges(node.ID())
		for _, edge := range edges {
			inDegree[edge.To()]++
		}
	}

	// Step 2: Initialize queue with nodes having in-degree 0
	// We process level by level to maintain dependency depth information
	var currentLevel []string
	for nodeID, degree := range inDegree {
		if degree == 0 {
			currentLevel = append(currentLevel, nodeID)
		}
	}

	// If no starting nodes found but graph has nodes, there's a cycle
	if len(currentLevel) == 0 && len(allNodes) > 0 {
		return nil, fmt.Errorf("cycle detected: no nodes with zero in-degree")
	}

	// Step 3: Process nodes level by level (BFS-style Kahn's)
	var sortedLevels [][]string
	processedCount := 0

	for len(currentLevel) > 0 {
		sortedLevels = append(sortedLevels, currentLevel)
		processedCount += len(currentLevel)

		var nextLevel []string

		// Process all nodes in current level
		for _, nodeID := range currentLevel {
			// Reduce in-degree of all successors
			edges := ctx.graph.GetEdges(nodeID)
			for _, edge := range edges {
				toID := edge.To()
				inDegree[toID]--
				if inDegree[toID] == 0 {
					nextLevel = append(nextLevel, toID)
				}
			}
		}

		currentLevel = nextLevel
	}

	// Step 4: Check for cycles (if not all nodes were processed)
	if processedCount != len(allNodes) {
		return nil, fmt.Errorf("cycle detected: processed %d nodes but graph has %d nodes",
			processedCount, len(allNodes))
	}

	return sortedLevels, nil
}

// buildStages converts topologically sorted levels into scheduler stages.
// Each level becomes a stage, with proper dependency linking.
func (ctx *compilationContext) buildStages(sortedLevels [][]string) error {
	if len(sortedLevels) == 0 {
		return nil
	}

	// Track which stage each node belongs to (for dependency resolution)
	nodeToStage := make(map[string]*scheduler.Stage)

	// Track the previous stage for simple linear dependency chaining
	var previousStage *scheduler.Stage

	for levelIdx, level := range sortedLevels {
		// Flatten the level: expand any ParallelNodes into their children
		flattenedNodes, err := ctx.flattenLevel(level)
		if err != nil {
			return fmt.Errorf("failed to flatten level %d: %w", levelIdx, err)
		}

		if len(flattenedNodes) == 0 {
			continue
		}

		// Create a new stage for this level
		stage := &scheduler.Stage{
			ID:     uuid.New().String(),
			JobID:  ctx.job.ID,
			Status: scheduler.JobPending,
			Tasks:  make([]*scheduler.Task, 0, len(flattenedNodes)),
		}

		// Determine stage dependencies
		stageDeps := ctx.computeStageDependencies(level, nodeToStage, previousStage)
		stage.Dependencies = stageDeps

		// Create tasks for all nodes in this level
		for _, node := range flattenedNodes {
			task := ctx.compiler.createTask(node, stage.ID, ctx.job.ID, ctx.input, ctx.sessionID)
			stage.Tasks = append(stage.Tasks, task)
		}

		// Register all original level nodes as belonging to this stage
		// (for dependency tracking of subsequent levels)
		for _, nodeID := range level {
			nodeToStage[nodeID] = stage
		}

		ctx.job.AddStage(stage)
		previousStage = stage
	}

	return nil
}

// flattenLevel expands a level of node IDs, handling ParallelNodes by
// extracting their children into the same level.
func (ctx *compilationContext) flattenLevel(nodeIDs []string) ([]Node, error) {
	var result []Node

	for _, nodeID := range nodeIDs {
		node, err := ctx.graph.GetNode(nodeID)
		if err != nil {
			return nil, fmt.Errorf("node %s not found: %w", nodeID, err)
		}

		// Check if this is a ParallelNode that needs flattening
		if parallelNode, ok := node.(*ParallelNode); ok {
			// Recursively flatten children
			flattened, err := ctx.flattenParallelNode(parallelNode)
			if err != nil {
				return nil, fmt.Errorf("failed to flatten parallel node %s: %w", nodeID, err)
			}
			result = append(result, flattened...)
		} else {
			result = append(result, node)
		}
	}

	return result, nil
}

// flattenParallelNode recursively extracts all executable nodes from a ParallelNode.
// If children are themselves ParallelNodes, they are recursively flattened.
// If children are complex subgraphs, they are treated as single units.
func (ctx *compilationContext) flattenParallelNode(pn *ParallelNode) ([]Node, error) {
	var result []Node

	for _, child := range pn.children {
		switch c := child.(type) {
		case *ParallelNode:
			// Recursively flatten nested parallel nodes
			nested, err := ctx.flattenParallelNode(c)
			if err != nil {
				return nil, err
			}
			result = append(result, nested...)

		default:
			// Simple node or complex subgraph - add as-is
			result = append(result, c)
		}
	}

	return result, nil
}

// computeStageDependencies determines which previous stages this stage depends on.
// It looks at the predecessors of nodes in the current level and finds their stages.
func (ctx *compilationContext) computeStageDependencies(
	levelNodeIDs []string,
	nodeToStage map[string]*scheduler.Stage,
	previousStage *scheduler.Stage,
) []string {
	// Collect unique stage dependencies
	depSet := make(map[string]struct{})

	// Find all predecessor nodes for nodes in this level
	for _, nodeID := range levelNodeIDs {
		predecessors := ctx.findPredecessors(nodeID)
		for _, predID := range predecessors {
			if stage, exists := nodeToStage[predID]; exists {
				depSet[stage.ID] = struct{}{}
			}
		}
	}

	// Convert to slice
	var deps []string
	for stageID := range depSet {
		deps = append(deps, stageID)
	}

	// If no explicit dependencies found but we have a previous stage,
	// use it as the dependency (maintains linear ordering for simple graphs)
	if len(deps) == 0 && previousStage != nil {
		// Only add if this isn't the first stage
		deps = append(deps, previousStage.ID)
	}

	return deps
}

// findPredecessors returns all node IDs that have edges pointing to the given node.
func (ctx *compilationContext) findPredecessors(nodeID string) []string {
	var predecessors []string

	for fromID, edges := range ctx.graph.edges {
		for _, edge := range edges {
			if edge.To() == nodeID {
				predecessors = append(predecessors, fromID)
			}
		}
	}

	return predecessors
}

// createTask creates a scheduler.Task from a graph Node.
func (c *DAGCompiler) createTask(node Node, stageID, jobID string, input *agent.AgentInput, sessionID string) *scheduler.Task {
	// Determine partition key:
	// 1. If sessionID is provided (sticky scheduling), use it
	// 2. Otherwise, check node metadata for partition
	// 3. Default to empty string (round-robin)
	partition := ""

	if sessionID != "" {
		// SessionID takes precedence for sticky scheduling
		partition = sessionID
	} else if meta := node.Metadata(); meta != nil {
		if p, ok := meta["partition"].(string); ok {
			partition = p
		}
	}

	return &scheduler.Task{
		ID:           uuid.New().String(),
		StageID:      stageID,
		JobID:        jobID,
		AgentID:      node.ID(),
		AgentName:    node.Name(),
		Input:        input,
		PartitionKey: partition,
		Status:       scheduler.JobPending,
	}
}