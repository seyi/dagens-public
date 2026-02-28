package agent

import (
	"github.com/seyi/dagens/pkg/events"
)

// Context helper functions for state management with event bus integration
// Preserves distributed Spark architecture while adding ADK observability

// SetContextValue sets a value in the context and optionally publishes a state change event
// This enables observability without breaking distributed execution
func SetContextValue(input *AgentInput, key string, value interface{}, agentName string, publishEvent bool) {
	if input.Context == nil {
		input.Context = make(map[string]interface{})
	}

	oldValue := input.Context[key]

	// Set the value
	input.Context[key] = value

	// Publish state change event if requested
	if publishEvent {
		_ = events.PublishStateChange(key, oldValue, value, agentName, "")
	}
}

// GetContextValue retrieves a value from the context
func GetContextValue(input *AgentInput, key string) (interface{}, bool) {
	if input.Context == nil {
		return nil, false
	}

	value, ok := input.Context[key]
	return value, ok
}

// UpdateContext updates multiple context values at once with event publishing
func UpdateContext(input *AgentInput, updates map[string]interface{}, agentName string, publishEvents bool) {
	if input.Context == nil {
		input.Context = make(map[string]interface{})
	}

	for key, newValue := range updates {
		oldValue := input.Context[key]
		input.Context[key] = newValue

		if publishEvents {
			_ = events.PublishStateChange(key, oldValue, newValue, agentName, "")
		}
	}
}

// MergeContext merges context from one input to another (for distributed scenarios)
// Preserves both contexts and publishes merge events if requested
func MergeContext(target *AgentInput, source *AgentInput, agentName string, publishEvents bool) {
	if target.Context == nil {
		target.Context = make(map[string]interface{})
	}

	if source.Context == nil {
		return
	}

	for key, value := range source.Context {
		oldValue := target.Context[key]
		target.Context[key] = value

		if publishEvents {
			_ = events.PublishStateChange(key, oldValue, value, agentName, "context_merge")
		}
	}
}

// EmitStateChange manually emits a state change event
// Useful when state is modified outside of SetContextValue
func EmitStateChange(key string, oldValue, newValue interface{}, agentName, partition string) {
	_ = events.PublishStateChange(key, oldValue, newValue, agentName, partition)
}
