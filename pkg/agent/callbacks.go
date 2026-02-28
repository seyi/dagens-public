package agent

import (
	"context"
	"fmt"
	"time"
)

// Lifecycle callbacks for agent execution
// Inspired by ADK's callback system but enhanced for distributed computing

// AgentCallback is called before or after agent execution
type AgentCallback func(ctx context.Context, agent Agent, input *AgentInput) error

// AgentOutputCallback is called after agent execution with output
type AgentOutputCallback func(ctx context.Context, agent Agent, input *AgentInput, output *AgentOutput) error

// ErrorCallback is called when an error occurs during execution
type ErrorCallback func(ctx context.Context, agent Agent, input *AgentInput, err error) error

// CallbackExecutor manages callback execution with error handling
type CallbackExecutor struct {
	stopOnError bool
}

// NewCallbackExecutor creates a new callback executor
func NewCallbackExecutor(stopOnError bool) *CallbackExecutor {
	return &CallbackExecutor{
		stopOnError: stopOnError,
	}
}

// ExecuteAgentCallbacks executes a list of agent callbacks
func (ce *CallbackExecutor) ExecuteAgentCallbacks(ctx context.Context, callbacks []AgentCallback, agent Agent, input *AgentInput) error {
	for i, callback := range callbacks {
		if err := callback(ctx, agent, input); err != nil {
			if ce.stopOnError {
				return fmt.Errorf("callback %d failed: %w", i, err)
			}
			// Log error but continue
			// Could publish event here
		}
	}
	return nil
}

// ExecuteOutputCallbacks executes callbacks that receive output
func (ce *CallbackExecutor) ExecuteOutputCallbacks(ctx context.Context, callbacks []AgentOutputCallback, agent Agent, input *AgentInput, output *AgentOutput) error {
	for i, callback := range callbacks {
		if err := callback(ctx, agent, input, output); err != nil {
			if ce.stopOnError {
				return fmt.Errorf("output callback %d failed: %w", i, err)
			}
		}
	}
	return nil
}

// ExecuteErrorCallbacks executes error callbacks
func (ce *CallbackExecutor) ExecuteErrorCallbacks(ctx context.Context, callbacks []ErrorCallback, agent Agent, input *AgentInput, err error) error {
	for _, callback := range callbacks {
		if cbErr := callback(ctx, agent, input, err); cbErr != nil {
			// Error in error callback - log but don't fail
			// Could publish event here
			_ = cbErr
		}
	}
	return nil
}

// Built-in utility callbacks

// LoggingCallback logs agent execution
func LoggingCallback(stage string) AgentCallback {
	return func(ctx context.Context, agent Agent, input *AgentInput) error {
		fmt.Printf("[%s] Agent: %s, Instruction: %s\n", stage, agent.Name(), input.Instruction)
		return nil
	}
}

// TimingCallback measures execution time
func TimingCallback() (AgentCallback, AgentOutputCallback) {
	startTimes := make(map[string]time.Time)

	before := func(ctx context.Context, agent Agent, input *AgentInput) error {
		startTimes[agent.ID()] = time.Now()
		return nil
	}

	after := func(ctx context.Context, agent Agent, input *AgentInput, output *AgentOutput) error {
		if startTime, ok := startTimes[agent.ID()]; ok {
			duration := time.Since(startTime)
			if output.Metadata == nil {
				output.Metadata = make(map[string]interface{})
			}
			output.Metadata["callback_timing"] = duration.Seconds()
			delete(startTimes, agent.ID())
		}
		return nil
	}

	return before, after
}

// ValidationCallback validates input
func ValidationCallback(validator func(*AgentInput) error) AgentCallback {
	return func(ctx context.Context, agent Agent, input *AgentInput) error {
		return validator(input)
	}
}

// TransformInputCallback transforms input before execution
func TransformInputCallback(transformer func(*AgentInput) error) AgentCallback {
	return func(ctx context.Context, agent Agent, input *AgentInput) error {
		return transformer(input)
	}
}

// TransformOutputCallback transforms output after execution
func TransformOutputCallback(transformer func(*AgentOutput) error) AgentOutputCallback {
	return func(ctx context.Context, agent Agent, input *AgentInput, output *AgentOutput) error {
		return transformer(output)
	}
}

// MetricsCallback collects execution metrics
func MetricsCallback(collector func(Agent, *AgentInput, *AgentOutput, time.Duration)) (AgentCallback, AgentOutputCallback) {
	startTimes := make(map[string]time.Time)

	before := func(ctx context.Context, agent Agent, input *AgentInput) error {
		startTimes[agent.ID()] = time.Now()
		return nil
	}

	after := func(ctx context.Context, agent Agent, input *AgentInput, output *AgentOutput) error {
		if startTime, ok := startTimes[agent.ID()]; ok {
			duration := time.Since(startTime)
			collector(agent, input, output, duration)
			delete(startTimes, agent.ID())
		}
		return nil
	}

	return before, after
}

// PartitionAwareCallback executes callback only on specific partitions
func PartitionAwareCallback(targetPartition string, callback AgentCallback) AgentCallback {
	return func(ctx context.Context, agent Agent, input *AgentInput) error {
		if agent.Partition() == targetPartition {
			return callback(ctx, agent, input)
		}
		return nil
	}
}

// ConditionalCallback executes callback based on condition
func ConditionalCallback(condition func(Agent, *AgentInput) bool, callback AgentCallback) AgentCallback {
	return func(ctx context.Context, agent Agent, input *AgentInput) error {
		if condition(agent, input) {
			return callback(ctx, agent, input)
		}
		return nil
	}
}

// ChainCallbacks combines multiple callbacks into one
func ChainCallbacks(callbacks ...AgentCallback) AgentCallback {
	return func(ctx context.Context, agent Agent, input *AgentInput) error {
		for i, cb := range callbacks {
			if err := cb(ctx, agent, input); err != nil {
				return fmt.Errorf("callback %d in chain failed: %w", i, err)
			}
		}
		return nil
	}
}

// RetryCallback wraps a callback with retry logic
func RetryCallback(callback AgentCallback, maxRetries int) AgentCallback {
	return func(ctx context.Context, agent Agent, input *AgentInput) error {
		var lastErr error
		for i := 0; i <= maxRetries; i++ {
			if err := callback(ctx, agent, input); err != nil {
				lastErr = err
				continue
			}
			return nil
		}
		return fmt.Errorf("callback failed after %d retries: %w", maxRetries, lastErr)
	}
}

// CacheCallback caches callback results
type CacheCallback struct {
	cache map[string]interface{}
}

func NewCacheCallback() *CacheCallback {
	return &CacheCallback{
		cache: make(map[string]interface{}),
	}
}

func (cc *CacheCallback) Get(key string) (interface{}, bool) {
	val, ok := cc.cache[key]
	return val, ok
}

func (cc *CacheCallback) Set(key string, value interface{}) {
	cc.cache[key] = value
}

func (cc *CacheCallback) Clear() {
	cc.cache = make(map[string]interface{})
}

// StateModificationCallback modifies agent state
func StateModificationCallback(modifier func(Agent, *AgentInput) error) AgentCallback {
	return func(ctx context.Context, agent Agent, input *AgentInput) error {
		return modifier(agent, input)
	}
}

// DistributedCallbackCoordinator coordinates callbacks across partitions
type DistributedCallbackCoordinator struct {
	localCallbacks  map[string][]AgentCallback
	remoteCallbacks map[string][]AgentCallback
}

func NewDistributedCallbackCoordinator() *DistributedCallbackCoordinator {
	return &DistributedCallbackCoordinator{
		localCallbacks:  make(map[string][]AgentCallback),
		remoteCallbacks: make(map[string][]AgentCallback),
	}
}

// RegisterLocal registers callback for local partition execution
func (dcc *DistributedCallbackCoordinator) RegisterLocal(partition string, callback AgentCallback) {
	dcc.localCallbacks[partition] = append(dcc.localCallbacks[partition], callback)
}

// RegisterRemote registers callback for remote partition execution
func (dcc *DistributedCallbackCoordinator) RegisterRemote(partition string, callback AgentCallback) {
	dcc.remoteCallbacks[partition] = append(dcc.remoteCallbacks[partition], callback)
}

// GetCallbacks retrieves appropriate callbacks based on partition locality
func (dcc *DistributedCallbackCoordinator) GetCallbacks(partition string, isLocal bool) []AgentCallback {
	if isLocal {
		return dcc.localCallbacks[partition]
	}
	return dcc.remoteCallbacks[partition]
}
