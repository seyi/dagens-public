package graph

import (
	"context"
)

// Node represents a single node in the execution graph.
// A node can be an agent, a tool, or any other executable unit.
type Node interface {
	// ID returns the unique identifier for this node.
	ID() string

	// Name returns a human-readable name for this node.
	Name() string

	// Type returns the type of node (e.g., "agent", "tool", "function").
	Type() string

	// Metadata returns the node's metadata.
	Metadata() map[string]interface{}

	// Execute runs the node's logic with the given state.
	Execute(ctx context.Context, state State) error
}
// BaseNode provides common functionality for all node types.
type BaseNode struct {
	id       string
	name     string
	nodeType string
	metadata map[string]interface{}
}

// NewBaseNode creates a new base node.
func NewBaseNode(id, nodeType string) *BaseNode {
	return &BaseNode{
		id:       id,
		name:     id, // Default name to ID
		nodeType: nodeType,
		metadata: make(map[string]interface{}),
	}
}

// ID returns the node's unique identifier.
func (n *BaseNode) ID() string {
	return n.id
}

// Name returns the human-readable name.
func (n *BaseNode) Name() string {
	return n.name
}

// SetName sets the human-readable name.
func (n *BaseNode) SetName(name string) {
	n.name = name
}

// Type returns the node type.
func (n *BaseNode) Type() string {
	return n.nodeType
}

// Metadata returns the node's metadata.
func (n *BaseNode) Metadata() map[string]interface{} {
	return n.metadata
}

// SetMetadata sets a metadata value.
func (n *BaseNode) SetMetadata(key string, value interface{}) {
	n.metadata[key] = value
}

// GetMetadata retrieves a metadata value.
func (n *BaseNode) GetMetadata(key string) (interface{}, bool) {
	val, exists := n.metadata[key]
	return val, exists
}

// FunctionNode represents a simple function-based node.
type FunctionNode struct {
	*BaseNode
	fn func(ctx context.Context, state State) error
}

// NewFunctionNode creates a new function node.
func NewFunctionNode(id string, fn func(ctx context.Context, state State) error) *FunctionNode {
	return &FunctionNode{
		BaseNode: NewBaseNode(id, "function"),
		fn:       fn,
	}
}

// Execute runs the function.
func (n *FunctionNode) Execute(ctx context.Context, state State) error {
	if n.fn == nil {
		return nil
	}
	return n.fn(ctx, state)
}

// PassthroughNode is a no-op node that simply passes state through.
type PassthroughNode struct {
	*BaseNode
}

// NewPassthroughNode creates a new passthrough node.
func NewPassthroughNode(id string) *PassthroughNode {
	return &PassthroughNode{
		BaseNode: NewBaseNode(id, "passthrough"),
	}
}

// Execute is a no-op for passthrough nodes.
func (n *PassthroughNode) Execute(ctx context.Context, state State) error {
	// No-op: state passes through unchanged
	return nil
}

// ParallelNode executes multiple child nodes in parallel.
type ParallelNode struct {
	*BaseNode
	children []Node
}

// NewParallelNode creates a new parallel execution node.
func NewParallelNode(id string, children []Node) *ParallelNode {
	return &ParallelNode{
		BaseNode: NewBaseNode(id, "parallel"),
		children: children,
	}
}

// Execute runs all child nodes in parallel.
func (n *ParallelNode) Execute(ctx context.Context, state State) error {
	if len(n.children) == 0 {
		return nil
	}

	// Create error channel
	errChan := make(chan error, len(n.children))

	// Launch goroutines for each child
	for _, child := range n.children {
		go func(c Node) {
			errChan <- c.Execute(ctx, state)
		}(child)
	}

	// Wait for all to complete and collect errors
	var firstErr error
	for i := 0; i < len(n.children); i++ {
		if err := <-errChan; err != nil && firstErr == nil {
			firstErr = err
		}
	}

	return firstErr
}

// LoopNode executes a child node repeatedly until a condition is met.
type LoopNode struct {
	*BaseNode
	child     Node
	condition func(state State) bool
	maxIters  int
}

// LoopConfig holds configuration for loop nodes.
type LoopConfig struct {
	ID        string
	Child     Node
	Condition func(state State) bool // Returns true to continue looping
	MaxIters  int                    // Maximum iterations (0 = unlimited)
}

// NewLoopNode creates a new loop node.
func NewLoopNode(cfg LoopConfig) *LoopNode {
	maxIters := cfg.MaxIters
	if maxIters == 0 {
		maxIters = 1000 // Default max to prevent infinite loops
	}

	return &LoopNode{
		BaseNode:  NewBaseNode(cfg.ID, "loop"),
		child:     cfg.Child,
		condition: cfg.Condition,
		maxIters:  maxIters,
	}
}

// Execute runs the child node repeatedly until the condition returns false.
func (n *LoopNode) Execute(ctx context.Context, state State) error {
	if n.child == nil {
		return nil
	}

	iter := 0
	for {
		// Check max iterations
		if iter >= n.maxIters {
			break
		}

		// Check condition
		if n.condition != nil && !n.condition(state) {
			break
		}

		// Execute child
		if err := n.child.Execute(ctx, state); err != nil {
			return err
		}

		iter++
	}

	return nil
}

// ConditionalNode executes different branches based on a condition.
type ConditionalNode struct {
	*BaseNode
	condition func(state State) bool
	trueNode  Node
	falseNode Node
}

// ConditionalConfig holds configuration for conditional nodes.
type ConditionalConfig struct {
	ID        string
	Condition func(state State) bool
	TrueNode  Node
	FalseNode Node
}

// NewConditionalNode creates a new conditional node.
func NewConditionalNode(cfg ConditionalConfig) *ConditionalNode {
	return &ConditionalNode{
		BaseNode:  NewBaseNode(cfg.ID, "conditional"),
		condition: cfg.Condition,
		trueNode:  cfg.TrueNode,
		falseNode: cfg.FalseNode,
	}
}

// Execute runs either the true or false branch based on the condition.
func (n *ConditionalNode) Execute(ctx context.Context, state State) error {
	if n.condition == nil {
		return nil
	}

	if n.condition(state) {
		if n.trueNode != nil {
			return n.trueNode.Execute(ctx, state)
		}
	} else {
		if n.falseNode != nil {
			return n.falseNode.Execute(ctx, state)
		}
	}

	return nil
}
