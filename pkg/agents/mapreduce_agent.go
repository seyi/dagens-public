package agents

import (
	"context"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/agent"
)

// MapReduceAgent implements the map-reduce pattern for distributed data processing
// Maps data across agents, then reduces results
type MapReduceAgent struct {
	*agent.BaseAgent
	mapperAgent   agent.Agent
	reducerAgent  agent.Agent
	splitter      SplitFunc
	parallelism   int
	timeout       time.Duration
	collectErrors bool
}

// SplitFunc splits input into multiple parts for mapping
type SplitFunc func(input *agent.AgentInput) ([]*agent.AgentInput, error)

// MapReduceAgentConfig configures a map-reduce agent
type MapReduceAgentConfig struct {
	Name          string
	MapperAgent   agent.Agent  // Agent to process each split
	ReducerAgent  agent.Agent  // Agent to combine results
	Splitter      SplitFunc    // Function to split input
	Parallelism   int          // Max parallel mappers
	Timeout       time.Duration
	CollectErrors bool // Continue on mapper errors
	Dependencies  []agent.Agent
}

// NewMapReduceAgent creates a new map-reduce agent
func NewMapReduceAgent(config MapReduceAgentConfig) *MapReduceAgent {
	if config.Parallelism == 0 {
		config.Parallelism = 10
	}
	if config.Timeout == 0 {
		config.Timeout = 10 * time.Minute
	}

	mrAgent := &MapReduceAgent{
		mapperAgent:   config.MapperAgent,
		reducerAgent:  config.ReducerAgent,
		splitter:      config.Splitter,
		parallelism:   config.Parallelism,
		timeout:       config.Timeout,
		collectErrors: config.CollectErrors,
	}

	// Create executor
	executor := &mapReduceExecutor{
		mrAgent: mrAgent,
	}

	baseAgent := agent.NewAgent(agent.AgentConfig{
		Name:         config.Name,
		Executor:     executor,
		Dependencies: config.Dependencies,
	})

	mrAgent.BaseAgent = baseAgent
	return mrAgent
}

// mapReduceExecutor implements AgentExecutor for map-reduce pattern
type mapReduceExecutor struct {
	mrAgent *MapReduceAgent
}

func (e *mapReduceExecutor) Execute(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	startTime := time.Now()

	// Create timeout context
	ctx, cancel := context.WithTimeout(ctx, e.mrAgent.timeout)
	defer cancel()

	// Step 1: Split input
	splits, err := e.mrAgent.splitter(input)
	if err != nil {
		return nil, fmt.Errorf("input splitting failed: %w", err)
	}

	if len(splits) == 0 {
		return nil, fmt.Errorf("splitter returned no splits")
	}

	// Step 2: Map phase - process splits in parallel
	mapResults, mapErrors := e.mapPhase(ctx, splits)

	if !e.mrAgent.collectErrors && len(mapErrors) > 0 {
		return nil, fmt.Errorf("map phase failed: %d errors, first: %w",
			len(mapErrors), mapErrors[0])
	}

	// Step 3: Reduce phase - combine results
	reduceResult, err := e.reducePhase(ctx, mapResults)
	if err != nil {
		return nil, fmt.Errorf("reduce phase failed: %w", err)
	}

	return &agent.AgentOutput{
		Result: reduceResult.Result,
		Metadata: map[string]interface{}{
			"pattern":         "MapReduce",
			"splits":          len(splits),
			"successful_maps": len(mapResults),
			"failed_maps":     len(mapErrors),
			"execution_time":  time.Since(startTime).Seconds(),
			"map_results":     mapResults,
			"map_errors":      mapErrors,
		},
	}, nil
}

// mapPhase executes mappers in parallel with controlled parallelism
func (e *mapReduceExecutor) mapPhase(ctx context.Context, splits []*agent.AgentInput) ([]*agent.AgentOutput, []error) {
	results := make([]*agent.AgentOutput, 0)
	errors := make([]error, 0)

	// Use semaphore to control parallelism
	sem := make(chan struct{}, e.mrAgent.parallelism)
	resultsChan := make(chan *agent.AgentOutput, len(splits))
	errorsChan := make(chan error, len(splits))

	// Launch mappers
	for i, split := range splits {
		sem <- struct{}{} // Acquire

		go func(idx int, input *agent.AgentInput) {
			defer func() { <-sem }() // Release

			// Add split metadata
			if input.Context == nil {
				input.Context = make(map[string]interface{})
			}
			input.Context["split_index"] = idx
			input.Context["total_splits"] = len(splits)

			// Execute mapper
			output, err := e.mrAgent.mapperAgent.Execute(ctx, input)
			if err != nil {
				errorsChan <- fmt.Errorf("mapper %d failed: %w", idx, err)
				return
			}

			// Add split metadata to output
			if output.Metadata == nil {
				output.Metadata = make(map[string]interface{})
			}
			output.Metadata["split_index"] = idx

			resultsChan <- output
		}(i, split)
	}

	// Wait for all mappers to complete or context to cancel
	completed := 0
	for completed < len(splits) {
		select {
		case result := <-resultsChan:
			results = append(results, result)
			completed++

		case err := <-errorsChan:
			errors = append(errors, err)
			completed++

		case <-ctx.Done():
			errors = append(errors, fmt.Errorf("map phase cancelled: %w", ctx.Err()))
			return results, errors
		}
	}

	return results, errors
}

// reducePhase combines mapper results
func (e *mapReduceExecutor) reducePhase(ctx context.Context, mapResults []*agent.AgentOutput) (*agent.AgentOutput, error) {
	if len(mapResults) == 0 {
		return nil, fmt.Errorf("no map results to reduce")
	}

	// If only one result, return it
	if len(mapResults) == 1 {
		return mapResults[0], nil
	}

	// Create input for reducer with all map results
	reduceInput := &agent.AgentInput{
		Instruction: "Combine the following results",
		Context: map[string]interface{}{
			"map_results": mapResults,
			"result_count": len(mapResults),
		},
	}

	// Execute reducer
	return e.mrAgent.reducerAgent.Execute(ctx, reduceInput)
}

// MapReduceBuilder provides fluent API for building map-reduce agents
type MapReduceBuilder struct {
	name          string
	mapperAgent   agent.Agent
	reducerAgent  agent.Agent
	splitter      SplitFunc
	parallelism   int
	timeout       time.Duration
	collectErrors bool
	dependencies  []agent.Agent
}

// NewMapReduce creates a new map-reduce agent builder
func NewMapReduce(mapper, reducer agent.Agent, splitter SplitFunc) *MapReduceBuilder {
	return &MapReduceBuilder{
		mapperAgent:   mapper,
		reducerAgent:  reducer,
		splitter:      splitter,
		parallelism:   10,
		timeout:       10 * time.Minute,
		collectErrors: false,
	}
}

// WithName sets the agent name
func (b *MapReduceBuilder) WithName(name string) *MapReduceBuilder {
	b.name = name
	return b
}

// WithParallelism sets max parallel mappers
func (b *MapReduceBuilder) WithParallelism(parallelism int) *MapReduceBuilder {
	b.parallelism = parallelism
	return b
}

// WithTimeout sets execution timeout
func (b *MapReduceBuilder) WithTimeout(timeout time.Duration) *MapReduceBuilder {
	b.timeout = timeout
	return b
}

// WithCollectErrors continues on mapper errors
func (b *MapReduceBuilder) WithCollectErrors(collect bool) *MapReduceBuilder {
	b.collectErrors = collect
	return b
}

// WithDependencies adds dependencies
func (b *MapReduceBuilder) WithDependencies(deps ...agent.Agent) *MapReduceBuilder {
	b.dependencies = append(b.dependencies, deps...)
	return b
}

// Build creates the map-reduce agent
func (b *MapReduceBuilder) Build() *MapReduceAgent {
	if b.name == "" {
		b.name = "mapreduce-agent"
	}

	return NewMapReduceAgent(MapReduceAgentConfig{
		Name:          b.name,
		MapperAgent:   b.mapperAgent,
		ReducerAgent:  b.reducerAgent,
		Splitter:      b.splitter,
		Parallelism:   b.parallelism,
		Timeout:       b.timeout,
		CollectErrors: b.collectErrors,
		Dependencies:  b.dependencies,
	})
}

// Common splitter functions

// LineSplitter splits text input by lines
func LineSplitter(chunkSize int) SplitFunc {
	return func(input *agent.AgentInput) ([]*agent.AgentInput, error) {
		text, ok := input.Context["text"].(string)
		if !ok {
			return nil, fmt.Errorf("text not found in context")
		}

		lines := []rune(text)
		var splits []*agent.AgentInput

		for i := 0; i < len(lines); i += chunkSize {
			end := i + chunkSize
			if end > len(lines) {
				end = len(lines)
			}

			chunk := string(lines[i:end])
			splits = append(splits, &agent.AgentInput{
				Instruction: input.Instruction,
				Context: map[string]interface{}{
					"text":  chunk,
					"chunk": i / chunkSize,
				},
			})
		}

		return splits, nil
	}
}

// ListSplitter splits a list into chunks
func ListSplitter(itemsKey string, chunkSize int) SplitFunc {
	return func(input *agent.AgentInput) ([]*agent.AgentInput, error) {
		items, ok := input.Context[itemsKey].([]interface{})
		if !ok {
			return nil, fmt.Errorf("items not found at key %s", itemsKey)
		}

		var splits []*agent.AgentInput

		for i := 0; i < len(items); i += chunkSize {
			end := i + chunkSize
			if end > len(items) {
				end = len(items)
			}

			chunk := items[i:end]
			splits = append(splits, &agent.AgentInput{
				Instruction: input.Instruction,
				Context: map[string]interface{}{
					itemsKey: chunk,
					"chunk":  i / chunkSize,
				},
			})
		}

		return splits, nil
	}
}

// FixedCountSplitter splits into N equal parts
func FixedCountSplitter(count int, dataKey string) SplitFunc {
	return func(input *agent.AgentInput) ([]*agent.AgentInput, error) {
		data, ok := input.Context[dataKey].([]interface{})
		if !ok {
			return nil, fmt.Errorf("data not found at key %s", dataKey)
		}

		chunkSize := len(data) / count
		if chunkSize == 0 {
			chunkSize = 1
		}

		return ListSplitter(dataKey, chunkSize)(input)
	}
}

// DocumentSplitter splits documents
func DocumentSplitter(docsKey string) SplitFunc {
	return func(input *agent.AgentInput) ([]*agent.AgentInput, error) {
		docs, ok := input.Context[docsKey].([]interface{})
		if !ok {
			return nil, fmt.Errorf("documents not found at key %s", docsKey)
		}

		var splits []*agent.AgentInput

		for i, doc := range docs {
			splits = append(splits, &agent.AgentInput{
				Instruction: input.Instruction,
				Context: map[string]interface{}{
					"document": doc,
					"doc_index": i,
					"total_docs": len(docs),
				},
			})
		}

		return splits, nil
	}
}

// FanOutAgent distributes work to multiple agents
// Similar to map-reduce but without reduce phase
type FanOutAgent struct {
	*agent.BaseAgent
	workerAgents []agent.Agent
	splitter     SplitFunc
	parallelism  int
	timeout      time.Duration
}

// FanOutAgentConfig configures a fan-out agent
type FanOutAgentConfig struct {
	Name         string
	Workers      []agent.Agent
	Splitter     SplitFunc
	Parallelism  int
	Timeout      time.Duration
	Dependencies []agent.Agent
}

// NewFanOutAgent creates a fan-out agent
func NewFanOutAgent(config FanOutAgentConfig) *FanOutAgent {
	if config.Parallelism == 0 {
		config.Parallelism = len(config.Workers)
	}
	if config.Timeout == 0 {
		config.Timeout = 5 * time.Minute
	}

	fanOutAgent := &FanOutAgent{
		workerAgents: config.Workers,
		splitter:     config.Splitter,
		parallelism:  config.Parallelism,
		timeout:      config.Timeout,
	}

	executor := &fanOutExecutor{
		fanOutAgent: fanOutAgent,
	}

	baseAgent := agent.NewAgent(agent.AgentConfig{
		Name:         config.Name,
		Executor:     executor,
		Dependencies: config.Dependencies,
	})

	fanOutAgent.BaseAgent = baseAgent
	return fanOutAgent
}

// fanOutExecutor implements AgentExecutor for fan-out
type fanOutExecutor struct {
	fanOutAgent *FanOutAgent
}

func (e *fanOutExecutor) Execute(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	startTime := time.Now()

	// Split input
	splits, err := e.fanOutAgent.splitter(input)
	if err != nil {
		return nil, fmt.Errorf("input splitting failed: %w", err)
	}

	if len(splits) > len(e.fanOutAgent.workerAgents) {
		return nil, fmt.Errorf("more splits (%d) than workers (%d)",
			len(splits), len(e.fanOutAgent.workerAgents))
	}

	// Create timeout context
	ctx, cancel := context.WithTimeout(ctx, e.fanOutAgent.timeout)
	defer cancel()

	// Distribute work to agents
	results := make([]*agent.AgentOutput, len(splits))
	errors := make([]error, len(splits))

	sem := make(chan struct{}, e.fanOutAgent.parallelism)
	done := make(chan int, len(splits))

	for i, split := range splits {
		sem <- struct{}{}

		go func(idx int, input *agent.AgentInput, worker agent.Agent) {
			defer func() {
				<-sem
				done <- idx
			}()

			output, err := worker.Execute(ctx, input)
			if err != nil {
				errors[idx] = err
				return
			}

			results[idx] = output
		}(i, split, e.fanOutAgent.workerAgents[i])
	}

	// Wait for completion
	for i := 0; i < len(splits); i++ {
		select {
		case <-done:
		case <-ctx.Done():
			return nil, fmt.Errorf("fan-out timed out: %w", ctx.Err())
		}
	}

	// Collect results
	var successfulResults []*agent.AgentOutput
	var collectedErrors []error

	for i := range splits {
		if errors[i] != nil {
			collectedErrors = append(collectedErrors, errors[i])
		} else if results[i] != nil {
			successfulResults = append(successfulResults, results[i])
		}
	}

	if len(collectedErrors) > 0 {
		return nil, fmt.Errorf("fan-out had %d errors, first: %w",
			len(collectedErrors), collectedErrors[0])
	}

	return &agent.AgentOutput{
		Result: successfulResults,
		Metadata: map[string]interface{}{
			"pattern":        "FanOut",
			"workers":        len(e.fanOutAgent.workerAgents),
			"splits":         len(splits),
			"execution_time": time.Since(startTime).Seconds(),
		},
	}, nil
}
