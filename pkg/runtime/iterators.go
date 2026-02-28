// Copyright 2025 Apache Spark AI Agents
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package runtime provides Go 1.23 iterator pattern support for ADK compatibility.
//
// This file implements iter.Seq2 patterns that match Google's ADK Go SDK,
// while maintaining compatibility with our existing channel-based implementation.
package runtime

import (
	"context"
	"iter"
	"time"

	"github.com/seyi/dagens/pkg/agent"
)

// ============================================================================
// Go 1.23 Iterator Pattern Support
// ============================================================================
//
// Google's ADK Go uses iter.Seq2[*Event, error] for event iteration.
// This provides a cleaner, more idiomatic Go API compared to channels.
// We support both patterns for compatibility.

// EventIterator is a Go 1.23 iterator that yields events.
// This matches Google ADK Go's pattern: iter.Seq2[*session.Event, error]
type EventIterator = iter.Seq2[*AsyncEvent, error]

// IteratorAgent is an agent that uses Go 1.23 iterators.
// This matches Google ADK Go's Agent.Run signature.
type IteratorAgent interface {
	// Name returns the agent name
	Name() string
	// Description returns the agent description
	Description() string
	// Run executes the agent and returns an iterator of events
	Run(ctx context.Context, invCtx *InvocationContext) EventIterator
	// SubAgents returns child agents
	SubAgents() []IteratorAgent
}

// IteratorRunner orchestrates iterator-based agent execution.
// This matches Google ADK Go's Runner pattern.
type IteratorRunner struct {
	services    *RuntimeServices
	partitionID string
	rootAgent   IteratorAgent
	parents     map[string]IteratorAgent // parent tracking for agent tree
}

// NewIteratorRunner creates a new iterator-based runner
func NewIteratorRunner(rootAgent IteratorAgent, services *RuntimeServices) *IteratorRunner {
	runner := &IteratorRunner{
		services:  services,
		rootAgent: rootAgent,
		parents:   make(map[string]IteratorAgent),
	}
	// Build parent map for agent tree navigation
	runner.buildParentMap(rootAgent, nil)
	return runner
}

// buildParentMap recursively builds the parent-child relationships
func (r *IteratorRunner) buildParentMap(ag IteratorAgent, parent IteratorAgent) {
	if parent != nil {
		r.parents[ag.Name()] = parent
	}
	for _, subAgent := range ag.SubAgents() {
		r.buildParentMap(subAgent, ag)
	}
}

// WithPartitionID sets the Spark partition ID
func (r *IteratorRunner) WithPartitionID(id string) *IteratorRunner {
	r.partitionID = id
	return r
}

// Run executes the agent using Go 1.23 iterator pattern.
// This is the ADK-compatible execution loop using iterators.
func (r *IteratorRunner) Run(ctx context.Context, input *agent.AgentInput) EventIterator {
	return func(yield func(*AsyncEvent, error) bool) {
		// Create invocation context
		invCtx := NewInvocationContext(ctx, input)
		invCtx.PartitionID = r.partitionID
		invCtx.Services = r.services

		// Load session if available
		if r.services != nil && r.services.SessionService != nil {
			session, err := r.services.SessionService.GetSession(ctx, invCtx.SessionID)
			if err == nil && session != nil {
				invCtx.State = session.State
			}
		}

		// Find the agent to run (based on session history)
		agentToRun := r.findAgentToRun(invCtx)

		// Execute agent using iterator
		for event, err := range agentToRun.Run(ctx, invCtx) {
			if err != nil {
				if !yield(nil, err) {
					return
				}
				continue
			}

			// Set invocation ID
			event.InvocationID = invCtx.InvocationID

			// Commit non-partial events
			if !event.Partial {
				if commitErr := r.commitEvent(ctx, invCtx, event); commitErr != nil {
					if !yield(nil, commitErr) {
						return
					}
					continue
				}
			}

			// Yield event to caller
			if !yield(event, nil) {
				return
			}

			// Check for final response
			if event.IsFinalResponse() {
				return
			}
		}
	}
}

// findAgentToRun determines which agent should handle the request
func (r *IteratorRunner) findAgentToRun(invCtx *InvocationContext) IteratorAgent {
	// TODO: Implement session history-based agent selection
	// For now, return root agent
	return r.rootAgent
}

// commitEvent persists the event to the session
func (r *IteratorRunner) commitEvent(ctx context.Context, invCtx *InvocationContext, event *AsyncEvent) error {
	// Apply state delta
	if event.Actions != nil && event.Actions.StateDelta != nil {
		for key, value := range event.Actions.StateDelta {
			invCtx.Set(key, value)
		}
	}

	// Commit state changes
	invCtx.CommitStateDeltas()

	// Persist to session service
	if r.services != nil && r.services.SessionService != nil {
		session := &Session{
			ID:        invCtx.SessionID,
			UserID:    invCtx.UserID,
			State:     invCtx.State,
			UpdatedAt: time.Now(),
		}
		if err := r.services.SessionService.UpdateSession(ctx, session); err != nil {
			return err
		}
	}

	return nil
}

// GetParent returns the parent of the given agent
func (r *IteratorRunner) GetParent(agentName string) IteratorAgent {
	return r.parents[agentName]
}

// ============================================================================
// Iterator-based LLM Agent
// ============================================================================

// LLMIteratorAgent is an LLM agent using Go 1.23 iterators.
// This matches Google ADK Go's llmagent pattern.
type LLMIteratorAgent struct {
	name        string
	description string
	flow        *LlmFlow
	subAgents   []IteratorAgent
	maxSteps    int

	// Callbacks
	beforeAgentCallbacks []BeforeAgentCallback
	afterAgentCallbacks  []AfterAgentCallback
}

// BeforeAgentCallback is called before agent execution
type BeforeAgentCallback func(ctx *CallbackContext) (*AsyncEvent, error)

// AfterAgentCallback is called after agent execution
type AfterAgentCallback func(ctx *CallbackContext) (*AsyncEvent, error)

// NewLLMIteratorAgent creates a new iterator-based LLM agent
func NewLLMIteratorAgent(name, description string, flow *LlmFlow) *LLMIteratorAgent {
	return &LLMIteratorAgent{
		name:        name,
		description: description,
		flow:        flow,
		maxSteps:    10,
	}
}

func (a *LLMIteratorAgent) Name() string        { return a.name }
func (a *LLMIteratorAgent) Description() string { return a.description }
func (a *LLMIteratorAgent) SubAgents() []IteratorAgent {
	return a.subAgents
}

// WithSubAgents adds sub-agents
func (a *LLMIteratorAgent) WithSubAgents(agents ...IteratorAgent) *LLMIteratorAgent {
	a.subAgents = append(a.subAgents, agents...)
	return a
}

// WithMaxSteps sets maximum execution steps
func (a *LLMIteratorAgent) WithMaxSteps(max int) *LLMIteratorAgent {
	a.maxSteps = max
	return a
}

// WithBeforeAgentCallback adds a before-agent callback
func (a *LLMIteratorAgent) WithBeforeAgentCallback(cb BeforeAgentCallback) *LLMIteratorAgent {
	a.beforeAgentCallbacks = append(a.beforeAgentCallbacks, cb)
	return a
}

// WithAfterAgentCallback adds an after-agent callback
func (a *LLMIteratorAgent) WithAfterAgentCallback(cb AfterAgentCallback) *LLMIteratorAgent {
	a.afterAgentCallbacks = append(a.afterAgentCallbacks, cb)
	return a
}

// Run implements IteratorAgent using Go 1.23 iterators
func (a *LLMIteratorAgent) Run(ctx context.Context, invCtx *InvocationContext) EventIterator {
	return func(yield func(*AsyncEvent, error) bool) {
		invCtx.AgentName = a.name

		// Run before-agent callbacks
		callbackCtx := NewCallbackContext(invCtx, &EventActions{
			StateDelta: make(map[string]interface{}),
		})
		for _, cb := range a.beforeAgentCallbacks {
			event, err := cb(callbackCtx)
			if err != nil {
				if !yield(nil, err) {
					return
				}
				return
			}
			if event != nil {
				if !yield(event, nil) {
					return
				}
				// Short-circuit if callback returns event
				return
			}
		}

		// Run the LLM flow using step loop
		step := 0
		for step < a.maxSteps {
			step++

			var lastEvent *AsyncEvent

			// Run one step - convert channel to iterator internally
			stepChan := a.flow.runOneStepAsync(ctx, invCtx)
			for event := range stepChan {
				if event.Metadata == nil {
					event.Metadata = make(map[string]interface{})
				}
				event.Metadata["step"] = step
				event.Author = a.name
				lastEvent = event

				if !yield(event, nil) {
					return
				}

				if event.Error != nil {
					return
				}
			}

			// Check termination conditions
			if lastEvent == nil {
				break
			}
			if lastEvent.IsFinalResponse() {
				break
			}

			// Check context cancellation
			select {
			case <-ctx.Done():
				errorEvent := NewAsyncEvent(EventTypeError, ctx.Err().Error(), a.name)
				errorEvent.Error = ctx.Err()
				yield(errorEvent, nil)
				return
			default:
			}
		}

		// Run after-agent callbacks
		for _, cb := range a.afterAgentCallbacks {
			event, err := cb(callbackCtx)
			if err != nil {
				yield(nil, err)
				return
			}
			if event != nil {
				yield(event, nil)
				return
			}
		}

		// Max steps reached warning
		if step >= a.maxSteps {
			maxStepsEvent := NewAsyncEvent(EventTypeMessage, "Maximum steps reached", a.name)
			maxStepsEvent.Metadata = map[string]interface{}{
				"max_steps_reached": true,
				"steps_executed":    step,
			}
			yield(maxStepsEvent, nil)
		}
	}
}

// ============================================================================
// Conversion Utilities
// ============================================================================

// IteratorToChannel converts an iterator to a channel.
// Useful for bridging between iterator and channel-based code.
func IteratorToChannel(it EventIterator) <-chan *AsyncEvent {
	ch := make(chan *AsyncEvent)
	go func() {
		defer close(ch)
		for event, err := range it {
			if err != nil {
				errorEvent := NewAsyncEvent(EventTypeError, err.Error(), "iterator")
				errorEvent.Error = err
				ch <- errorEvent
				return
			}
			ch <- event
		}
	}()
	return ch
}

// ChannelToIterator converts a channel to an iterator.
// Useful for adapting existing channel-based agents.
func ChannelToIterator(ch <-chan *AsyncEvent) EventIterator {
	return func(yield func(*AsyncEvent, error) bool) {
		for event := range ch {
			if event.Error != nil {
				if !yield(event, event.Error) {
					return
				}
				continue
			}
			if !yield(event, nil) {
				return
			}
		}
	}
}

// WrapAsyncAgentAsIterator adapts an AsyncAgent to use iterators
func WrapAsyncAgentAsIterator(ag AsyncAgent) IteratorAgent {
	return &asyncAgentIteratorWrapper{
		asyncAgent: ag,
	}
}

type asyncAgentIteratorWrapper struct {
	asyncAgent AsyncAgent
}

func (w *asyncAgentIteratorWrapper) Name() string        { return w.asyncAgent.Name() }
func (w *asyncAgentIteratorWrapper) Description() string { return "" }
func (w *asyncAgentIteratorWrapper) SubAgents() []IteratorAgent {
	return nil
}

func (w *asyncAgentIteratorWrapper) Run(ctx context.Context, invCtx *InvocationContext) EventIterator {
	ch := w.asyncAgent.RunAsync(ctx, invCtx)
	return ChannelToIterator(ch)
}

// ============================================================================
// Sequential and Parallel Iterator Agents
// ============================================================================

// SequentialIteratorAgent runs sub-agents in sequence
type SequentialIteratorAgent struct {
	name        string
	description string
	subAgents   []IteratorAgent
}

// NewSequentialIteratorAgent creates a new sequential agent
func NewSequentialIteratorAgent(name, description string, agents ...IteratorAgent) *SequentialIteratorAgent {
	return &SequentialIteratorAgent{
		name:        name,
		description: description,
		subAgents:   agents,
	}
}

func (a *SequentialIteratorAgent) Name() string                 { return a.name }
func (a *SequentialIteratorAgent) Description() string          { return a.description }
func (a *SequentialIteratorAgent) SubAgents() []IteratorAgent { return a.subAgents }

func (a *SequentialIteratorAgent) Run(ctx context.Context, invCtx *InvocationContext) EventIterator {
	return func(yield func(*AsyncEvent, error) bool) {
		for i, subAgent := range a.subAgents {
			// Start event
			startEvent := NewAsyncEvent(EventTypeStateChange,
				"Starting "+subAgent.Name(), a.name)
			startEvent.Partial = true
			startEvent.Metadata = map[string]interface{}{
				"sequential_step": i + 1,
				"agent":           subAgent.Name(),
			}
			if !yield(startEvent, nil) {
				return
			}

			// Run sub-agent
			for event, err := range subAgent.Run(ctx, invCtx) {
				if err != nil {
					if !yield(nil, err) {
						return
					}
					return
				}
				if event.Metadata == nil {
					event.Metadata = make(map[string]interface{})
				}
				event.Metadata["sequential_step"] = i + 1
				event.Metadata["sequential_agent"] = subAgent.Name()

				if !yield(event, nil) {
					return
				}

				if event.Error != nil {
					return
				}
			}

			// Check cancellation
			select {
			case <-ctx.Done():
				return
			default:
			}
		}
	}
}

// ParallelIteratorAgent runs sub-agents in parallel and merges results
type ParallelIteratorAgent struct {
	name        string
	description string
	subAgents   []IteratorAgent
}

// NewParallelIteratorAgent creates a new parallel agent
func NewParallelIteratorAgent(name, description string, agents ...IteratorAgent) *ParallelIteratorAgent {
	return &ParallelIteratorAgent{
		name:        name,
		description: description,
		subAgents:   agents,
	}
}

func (a *ParallelIteratorAgent) Name() string                 { return a.name }
func (a *ParallelIteratorAgent) Description() string          { return a.description }
func (a *ParallelIteratorAgent) SubAgents() []IteratorAgent { return a.subAgents }

func (a *ParallelIteratorAgent) Run(ctx context.Context, invCtx *InvocationContext) EventIterator {
	return func(yield func(*AsyncEvent, error) bool) {
		// Use channels internally for parallel execution
		merged := make(chan struct {
			event *AsyncEvent
			err   error
			idx   int
		})
		done := make(chan struct{})

		// Start all agents in parallel
		for i, subAgent := range a.subAgents {
			go func(idx int, ag IteratorAgent) {
				for event, err := range ag.Run(ctx, invCtx) {
					select {
					case merged <- struct {
						event *AsyncEvent
						err   error
						idx   int
					}{event, err, idx}:
					case <-done:
						return
					}
				}
			}(i, subAgent)
		}

		// Count completed agents
		completed := 0
		expectedCompletions := len(a.subAgents)

		for completed < expectedCompletions {
			select {
			case result := <-merged:
				if result.err != nil {
					if !yield(nil, result.err) {
						close(done)
						return
					}
					continue
				}
				if result.event == nil {
					completed++
					continue
				}
				if result.event.Metadata == nil {
					result.event.Metadata = make(map[string]interface{})
				}
				result.event.Metadata["parallel_branch"] = result.idx
				result.event.Metadata["parallel_agent"] = a.subAgents[result.idx].Name()

				if !yield(result.event, nil) {
					close(done)
					return
				}
			case <-ctx.Done():
				close(done)
				return
			}
		}
	}
}

// LoopIteratorAgent runs sub-agents in a loop until condition is met
type LoopIteratorAgent struct {
	name          string
	description   string
	subAgents     []IteratorAgent
	maxIterations int
	shouldStop    func(event *AsyncEvent) bool
}

// NewLoopIteratorAgent creates a new loop agent
func NewLoopIteratorAgent(name, description string, maxIterations int, agents ...IteratorAgent) *LoopIteratorAgent {
	return &LoopIteratorAgent{
		name:          name,
		description:   description,
		subAgents:     agents,
		maxIterations: maxIterations,
	}
}

func (a *LoopIteratorAgent) Name() string                 { return a.name }
func (a *LoopIteratorAgent) Description() string          { return a.description }
func (a *LoopIteratorAgent) SubAgents() []IteratorAgent { return a.subAgents }

// WithStopCondition sets a function to determine when to stop looping
func (a *LoopIteratorAgent) WithStopCondition(fn func(*AsyncEvent) bool) *LoopIteratorAgent {
	a.shouldStop = fn
	return a
}

func (a *LoopIteratorAgent) Run(ctx context.Context, invCtx *InvocationContext) EventIterator {
	return func(yield func(*AsyncEvent, error) bool) {
		for iteration := 0; iteration < a.maxIterations; iteration++ {
			// Run all sub-agents in sequence for this iteration
			for _, subAgent := range a.subAgents {
				for event, err := range subAgent.Run(ctx, invCtx) {
					if err != nil {
						if !yield(nil, err) {
							return
						}
						return
					}
					if event.Metadata == nil {
						event.Metadata = make(map[string]interface{})
					}
					event.Metadata["loop_iteration"] = iteration + 1
					event.Metadata["loop_agent"] = subAgent.Name()

					if !yield(event, nil) {
						return
					}

					// Check stop condition
					if a.shouldStop != nil && a.shouldStop(event) {
						return
					}

					if event.Error != nil {
						return
					}
				}
			}

			// Check cancellation
			select {
			case <-ctx.Done():
				return
			default:
			}
		}

		// Max iterations reached
		maxIterEvent := NewAsyncEvent(EventTypeMessage, "Maximum iterations reached", a.name)
		maxIterEvent.Metadata = map[string]interface{}{
			"max_iterations_reached": true,
			"iterations_executed":    a.maxIterations,
		}
		yield(maxIterEvent, nil)
	}
}

// ============================================================================
// Collect Utilities for Iterators
// ============================================================================

// CollectIterator collects all events from an iterator into a slice
func CollectIterator(it EventIterator) ([]*AsyncEvent, error) {
	var events []*AsyncEvent
	var lastErr error
	for event, err := range it {
		if err != nil {
			lastErr = err
			continue
		}
		events = append(events, event)
	}
	return events, lastErr
}

// GetFinalEventFromIterator returns the last non-partial event from an iterator
func GetFinalEventFromIterator(it EventIterator) (*AsyncEvent, error) {
	var last *AsyncEvent
	var lastErr error
	for event, err := range it {
		if err != nil {
			lastErr = err
			continue
		}
		if !event.Partial {
			last = event
		}
	}
	return last, lastErr
}
