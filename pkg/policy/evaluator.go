package policy

import "context"

// RuleEvaluator is the interface all rule types must implement
type RuleEvaluator interface {
	// Type returns the rule type identifier (e.g., "pii", "content")
	Type() string

	// Evaluate checks the content against the rule
	Evaluate(ctx context.Context, content string, config map[string]interface{}) (*EvaluationResult, error)
}

// Registry holds all registered rule evaluators
type Registry struct {
	evaluators map[string]RuleEvaluator
}

// NewRegistry creates an empty registry
func NewRegistry() *Registry {
	return &Registry{
		evaluators: make(map[string]RuleEvaluator),
	}
}

// Register adds an evaluator to the registry
func (r *Registry) Register(e RuleEvaluator) {
	r.evaluators[e.Type()] = e
}

// Get retrieves an evaluator by type
func (r *Registry) Get(ruleType string) (RuleEvaluator, bool) {
	e, ok := r.evaluators[ruleType]
	return e, ok
}

// Types returns all registered rule types
func (r *Registry) Types() []string {
	types := make([]string, 0, len(r.evaluators))
	for t := range r.evaluators {
		types = append(types, t)
	}
	return types
}
