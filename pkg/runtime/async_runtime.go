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

// Package runtime provides ADK-compatible async runtime for distributed agent execution.
//
// This file implements the core ADK pattern:
// - Agents yield events via channels (Go's equivalent to AsyncGenerator)
// - Runner consumes events and commits state per-event
// - Step-based execution with pause/resume semantics
package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/agent"
)

// EventActions represents actions to be performed when processing an event.
// This matches ADK's EventActions structure.
type EventActions struct {
	// StateDelta contains state changes to apply
	StateDelta map[string]interface{} `json:"state_delta,omitempty"`

	// ArtifactDelta contains artifact changes
	ArtifactDelta *ArtifactDelta `json:"artifact_delta,omitempty"`

	// TransferToAgent signals control transfer to another agent
	TransferToAgent string `json:"transfer_to_agent,omitempty"`

	// Escalate signals escalation to parent agent
	Escalate bool `json:"escalate,omitempty"`

	// SkipSummarization prevents event from being summarized
	SkipSummarization bool `json:"skip_summarization,omitempty"`
}

// AsyncEvent represents an event yielded during async agent execution.
// Enhanced version of Event with ADK-compatible Actions field.
type AsyncEvent struct {
	ID           string                 `json:"id"`
	InvocationID string                 `json:"invocation_id"`
	Author       string                 `json:"author"`
	Branch       string                 `json:"branch,omitempty"`
	Type         EventType              `json:"type"`
	Content      interface{}            `json:"content"`
	Partial      bool                   `json:"partial"`
	Timestamp    time.Time              `json:"timestamp"`

	// Actions to perform (ADK pattern)
	Actions *EventActions `json:"actions,omitempty"`

	// Spark-specific fields
	PartitionID string   `json:"partition_id,omitempty"`
	Lineage     []string `json:"lineage,omitempty"`

	// Metadata
	Metadata map[string]interface{} `json:"metadata,omitempty"`

	// Error if this is an error event
	Error error `json:"-"`
}

// NewAsyncEvent creates a new async event
func NewAsyncEvent(eventType EventType, content interface{}, author string) *AsyncEvent {
	return &AsyncEvent{
		ID:        generateID("event"),
		Type:      eventType,
		Content:   content,
		Author:    author,
		Timestamp: time.Now(),
		Actions:   &EventActions{},
	}
}

// WithStateDelta adds state changes to the event
func (e *AsyncEvent) WithStateDelta(delta map[string]interface{}) *AsyncEvent {
	if e.Actions == nil {
		e.Actions = &EventActions{}
	}
	e.Actions.StateDelta = delta
	return e
}

// WithPartial marks the event as partial (streaming)
func (e *AsyncEvent) WithPartial(partial bool) *AsyncEvent {
	e.Partial = partial
	return e
}

// IsFinalResponse checks if this event represents a final response
func (e *AsyncEvent) IsFinalResponse() bool {
	// Final if it's a message type and not partial
	return e.Type == EventTypeMessage && !e.Partial
}

// AsyncAgent interface for agents that yield events via channels.
// This is Go's equivalent to Python's AsyncGenerator[Event, None].
type AsyncAgent interface {
	// ID returns the unique identifier for this agent
	ID() string

	// Name returns the human-readable name
	Name() string

	// RunAsync executes the agent and yields events through the returned channel.
	// The channel is closed when execution completes.
	// This is equivalent to ADK's `async def run_async() -> AsyncGenerator[Event, None]`
	RunAsync(ctx context.Context, invCtx *InvocationContext) <-chan *AsyncEvent
}

// AsyncAgentFunc is a function type that can be used as an AsyncAgent
type AsyncAgentFunc func(ctx context.Context, invCtx *InvocationContext) <-chan *AsyncEvent

// StepExecutor executes a single step and yields events.
// This matches ADK's _run_one_step_async pattern.
type StepExecutor interface {
	// ExecuteStep runs one step (e.g., one LLM call) and yields events
	ExecuteStep(ctx context.Context, invCtx *InvocationContext) <-chan *AsyncEvent
}

// AsyncAgentRuntime orchestrates async agent execution with per-event commit.
// This implements ADK's Runner pattern with proper event streaming.
type AsyncAgentRuntime struct {
	services    *RuntimeServices
	partitionID string

	// Configuration
	maxEvents        int
	enableCheckpoint bool

	// Pause control
	shouldPauseFunc func(*AsyncEvent) bool

	// Event log
	eventLog []AsyncEvent
	mu       sync.RWMutex
}

// NewAsyncAgentRuntime creates a new async agent runtime
func NewAsyncAgentRuntime(services *RuntimeServices) *AsyncAgentRuntime {
	return &AsyncAgentRuntime{
		services:         services,
		maxEvents:        1000,
		enableCheckpoint: true,
		eventLog:         make([]AsyncEvent, 0),
	}
}

// WithPartitionID sets the Spark partition ID
func (r *AsyncAgentRuntime) WithPartitionID(id string) *AsyncAgentRuntime {
	r.partitionID = id
	return r
}

// WithPauseCondition sets a function to check if execution should pause
func (r *AsyncAgentRuntime) WithPauseCondition(fn func(*AsyncEvent) bool) *AsyncAgentRuntime {
	r.shouldPauseFunc = fn
	return r
}

// RunAsync executes the agent and streams events.
// This is the core ADK-compatible execution loop:
// 1. Agent yields events via channel
// 2. Runner processes each event
// 3. Non-partial events are committed to session
// 4. Events are forwarded to caller
// 5. Execution continues until final response or pause condition
func (r *AsyncAgentRuntime) RunAsync(ctx context.Context, ag AsyncAgent, input *agent.AgentInput) <-chan *AsyncEvent {
	output := make(chan *AsyncEvent)

	go func() {
		defer close(output)

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

		// Execute agent - this returns a channel of events
		agentEvents := ag.RunAsync(ctx, invCtx)

		// Process events as they arrive (ADK pattern)
		for event := range agentEvents {
			// Set invocation ID
			event.InvocationID = invCtx.InvocationID

			// Process and potentially commit the event
			if err := r.processEvent(ctx, invCtx, event); err != nil {
				// Yield error event
				errorEvent := NewAsyncEvent(EventTypeError, err.Error(), "runtime")
				errorEvent.Error = err
				output <- errorEvent
				return
			}

			// Forward event to caller
			output <- event

			// Check pause condition
			if r.shouldPauseFunc != nil && r.shouldPauseFunc(event) {
				return
			}

			// Check for final response
			if event.IsFinalResponse() {
				return
			}

			// Check context cancellation
			select {
			case <-ctx.Done():
				errorEvent := NewAsyncEvent(EventTypeError, ctx.Err().Error(), "runtime")
				errorEvent.Error = ctx.Err()
				output <- errorEvent
				return
			default:
			}
		}
	}()

	return output
}

// processEvent handles a single event (ADK's per-event processing)
func (r *AsyncAgentRuntime) processEvent(ctx context.Context, invCtx *InvocationContext, event *AsyncEvent) error {
	// Add to event log
	r.mu.Lock()
	r.eventLog = append(r.eventLog, *event)
	if len(r.eventLog) > r.maxEvents {
		r.eventLog = r.eventLog[len(r.eventLog)-r.maxEvents:]
	}
	r.mu.Unlock()

	// Process state delta from event actions
	if event.Actions != nil && event.Actions.StateDelta != nil {
		for key, value := range event.Actions.StateDelta {
			invCtx.Set(key, value)
		}
	}

	// Commit non-partial events (ADK pattern)
	if !event.Partial {
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
				return fmt.Errorf("failed to persist session: %w", err)
			}
		}

		// Handle artifact delta
		if event.Actions != nil && event.Actions.ArtifactDelta != nil {
			if err := r.processArtifactDelta(ctx, invCtx, event.Actions.ArtifactDelta); err != nil {
				return fmt.Errorf("failed to process artifact delta: %w", err)
			}
		}
	}

	return nil
}

// processArtifactDelta handles artifact changes
func (r *AsyncAgentRuntime) processArtifactDelta(ctx context.Context, invCtx *InvocationContext, delta *ArtifactDelta) error {
	if r.services == nil || r.services.ArtifactService == nil {
		return nil
	}

	// Note: In a full implementation, we'd process Created/Updated/Deleted artifacts
	// For now, we just log the delta
	return nil
}

// GetEventLog returns the event log
func (r *AsyncAgentRuntime) GetEventLog() []AsyncEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	events := make([]AsyncEvent, len(r.eventLog))
	copy(events, r.eventLog)
	return events
}

// BaseAsyncAgent provides a base implementation for async agents.
// Agents can embed this and implement the RunSteps method.
type BaseAsyncAgent struct {
	id          string
	name        string
	description string
}

// NewBaseAsyncAgent creates a new base async agent
func NewBaseAsyncAgent(id, name, description string) *BaseAsyncAgent {
	return &BaseAsyncAgent{
		id:          id,
		name:        name,
		description: description,
	}
}

func (a *BaseAsyncAgent) ID() string          { return a.id }
func (a *BaseAsyncAgent) Name() string        { return a.name }
func (a *BaseAsyncAgent) Description() string { return a.description }

// LLMAsyncAgent is an async agent that executes LLM calls in steps.
// This matches ADK's BaseLlmFlow pattern.
type LLMAsyncAgent struct {
	*BaseAsyncAgent
	executor StepExecutor
	maxSteps int

	// ADK-compatible LLM flow with processors and callbacks
	flow *LlmFlow

	// Callbacks (can be used without full flow)
	beforeModelCallbacks  []BeforeModelCallback
	afterModelCallbacks   []AfterModelCallback
	onModelErrorCallbacks []OnModelErrorCallback
}

// NewLLMAsyncAgent creates a new LLM async agent
func NewLLMAsyncAgent(id, name string, executor StepExecutor) *LLMAsyncAgent {
	return &LLMAsyncAgent{
		BaseAsyncAgent: NewBaseAsyncAgent(id, name, "LLM-based async agent"),
		executor:       executor,
		maxSteps:       10,
	}
}

// WithLlmFlow sets the LLM flow for ADK-compatible processing
func (a *LLMAsyncAgent) WithLlmFlow(flow *LlmFlow) *LLMAsyncAgent {
	a.flow = flow
	return a
}

// WithBeforeModelCallback adds a callback to run before model invocation
func (a *LLMAsyncAgent) WithBeforeModelCallback(cb BeforeModelCallback) *LLMAsyncAgent {
	a.beforeModelCallbacks = append(a.beforeModelCallbacks, cb)
	return a
}

// WithAfterModelCallback adds a callback to run after model response
func (a *LLMAsyncAgent) WithAfterModelCallback(cb AfterModelCallback) *LLMAsyncAgent {
	a.afterModelCallbacks = append(a.afterModelCallbacks, cb)
	return a
}

// WithOnModelErrorCallback adds a callback to run on model errors
func (a *LLMAsyncAgent) WithOnModelErrorCallback(cb OnModelErrorCallback) *LLMAsyncAgent {
	a.onModelErrorCallbacks = append(a.onModelErrorCallbacks, cb)
	return a
}

// WithMaxSteps sets the maximum number of steps
func (a *LLMAsyncAgent) WithMaxSteps(max int) *LLMAsyncAgent {
	a.maxSteps = max
	return a
}

// RunAsync implements the ADK step loop pattern:
// while True:
//
//	for event in _run_one_step_async():
//	    yield event
//	if last_event.is_final_response():
//	    break
func (a *LLMAsyncAgent) RunAsync(ctx context.Context, invCtx *InvocationContext) <-chan *AsyncEvent {
	output := make(chan *AsyncEvent)

	// Set agent name in context for billing/logging
	invCtx.AgentName = a.name

	go func() {
		defer close(output)

		// If we have a full LlmFlow, delegate to it
		if a.flow != nil {
			// Copy callbacks from agent to flow
			for _, cb := range a.beforeModelCallbacks {
				a.flow.BeforeModelCallbacks = append(a.flow.BeforeModelCallbacks, cb)
			}
			for _, cb := range a.afterModelCallbacks {
				a.flow.AfterModelCallbacks = append(a.flow.AfterModelCallbacks, cb)
			}
			for _, cb := range a.onModelErrorCallbacks {
				a.flow.OnModelErrorCallbacks = append(a.flow.OnModelErrorCallbacks, cb)
			}
			a.flow.MaxSteps = a.maxSteps

			// Run through flow
			for event := range a.flow.RunAsync(ctx, invCtx) {
				output <- event
			}
			return
		}

		// Otherwise, use the basic step executor pattern
		step := 0
		for step < a.maxSteps {
			step++

			var lastEvent *AsyncEvent

			// Execute one step
			stepEvents := a.executor.ExecuteStep(ctx, invCtx)

			// Yield all events from this step
			for event := range stepEvents {
				if event.Metadata == nil {
					event.Metadata = make(map[string]interface{})
				}
				event.Metadata["step"] = step
				lastEvent = event
				output <- event

				// Check for errors
				if event.Error != nil {
					return
				}
			}

			// Check if we should stop
			if lastEvent == nil {
				return
			}
			if lastEvent.IsFinalResponse() {
				return
			}
			// Note: Partial events do NOT terminate the loop - they are just streaming chunks.
			// Tool calls and other non-final events continue to the next step.

			// Check context cancellation
			select {
			case <-ctx.Done():
				errorEvent := NewAsyncEvent(EventTypeError, ctx.Err().Error(), a.name)
				errorEvent.Error = ctx.Err()
				output <- errorEvent
				return
			default:
			}
		}

		// Max steps reached
		maxStepsEvent := NewAsyncEvent(EventTypeMessage, "Maximum steps reached", a.name)
		maxStepsEvent.Metadata = map[string]interface{}{
			"max_steps_reached": true,
			"steps_executed":    step,
		}
		output <- maxStepsEvent
	}()

	return output
}

// SimpleAsyncAgent wraps a simple execution function as an async agent.
// Useful for converting existing synchronous agents.
type SimpleAsyncAgent struct {
	*BaseAsyncAgent
	executeFn func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error)
}

// NewSimpleAsyncAgent creates an async agent from a simple execute function
func NewSimpleAsyncAgent(id, name string, fn func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error)) *SimpleAsyncAgent {
	return &SimpleAsyncAgent{
		BaseAsyncAgent: NewBaseAsyncAgent(id, name, "Simple async agent"),
		executeFn:      fn,
	}
}

// RunAsync wraps the synchronous execution in an async pattern
func (a *SimpleAsyncAgent) RunAsync(ctx context.Context, invCtx *InvocationContext) <-chan *AsyncEvent {
	output := make(chan *AsyncEvent)

	go func() {
		defer close(output)

		// Execute the function
		result, err := a.executeFn(ctx, invCtx.Input)

		if err != nil {
			errorEvent := NewAsyncEvent(EventTypeError, err.Error(), a.name)
			errorEvent.Error = err
			output <- errorEvent
			return
		}

		// Yield result as final event
		event := NewAsyncEvent(EventTypeMessage, result.Result, a.name)
		event.Metadata = result.Metadata

		// Convert result metadata to state delta if present
		if result.Metadata != nil {
			event.Actions = &EventActions{
				StateDelta: result.Metadata,
			}
		}

		output <- event
	}()

	return output
}

// SequentialAsyncAgent executes multiple agents in sequence.
// Events from each agent are yielded as they arrive.
type SequentialAsyncAgent struct {
	*BaseAsyncAgent
	agents []AsyncAgent
}

// NewSequentialAsyncAgent creates a sequential async agent
func NewSequentialAsyncAgent(id, name string, agents []AsyncAgent) *SequentialAsyncAgent {
	return &SequentialAsyncAgent{
		BaseAsyncAgent: NewBaseAsyncAgent(id, name, "Sequential async agent"),
		agents:         agents,
	}
}

// RunAsync executes agents in sequence, yielding events from each
func (a *SequentialAsyncAgent) RunAsync(ctx context.Context, invCtx *InvocationContext) <-chan *AsyncEvent {
	output := make(chan *AsyncEvent)

	go func() {
		defer close(output)

		for i, ag := range a.agents {
			// Start event for this agent
			startEvent := NewAsyncEvent(EventTypeStateChange, fmt.Sprintf("Starting agent %d: %s", i+1, ag.Name()), a.name)
			startEvent.Partial = true
			output <- startEvent

			// Execute agent
			agentEvents := ag.RunAsync(ctx, invCtx)

			// Forward all events
			for event := range agentEvents {
				if event.Metadata == nil {
					event.Metadata = make(map[string]interface{})
				}
				event.Metadata["sequential_step"] = i + 1
				event.Metadata["sequential_agent"] = ag.Name()
				output <- event

				// Stop on error
				if event.Error != nil {
					return
				}
			}

			// Check cancellation between agents
			select {
			case <-ctx.Done():
				errorEvent := NewAsyncEvent(EventTypeError, ctx.Err().Error(), a.name)
				errorEvent.Error = ctx.Err()
				output <- errorEvent
				return
			default:
			}
		}
	}()

	return output
}

// ParallelAsyncAgent executes multiple agents in parallel.
// Events from all agents are merged into a single stream.
type ParallelAsyncAgent struct {
	*BaseAsyncAgent
	agents []AsyncAgent
}

// NewParallelAsyncAgent creates a parallel async agent
func NewParallelAsyncAgent(id, name string, agents []AsyncAgent) *ParallelAsyncAgent {
	return &ParallelAsyncAgent{
		BaseAsyncAgent: NewBaseAsyncAgent(id, name, "Parallel async agent"),
		agents:         agents,
	}
}

// RunAsync executes agents in parallel, merging event streams
func (a *ParallelAsyncAgent) RunAsync(ctx context.Context, invCtx *InvocationContext) <-chan *AsyncEvent {
	output := make(chan *AsyncEvent)

	go func() {
		defer close(output)

		// Create a merged channel
		var wg sync.WaitGroup
		merged := make(chan *AsyncEvent)

		// Start all agents
		for i, ag := range a.agents {
			wg.Add(1)
			go func(idx int, asyncAgent AsyncAgent) {
				defer wg.Done()

				agentEvents := asyncAgent.RunAsync(ctx, invCtx)
				for event := range agentEvents {
					if event.Metadata == nil {
						event.Metadata = make(map[string]interface{})
					}
					event.Metadata["parallel_branch"] = idx
					event.Metadata["parallel_agent"] = asyncAgent.Name()
					merged <- event
				}
			}(i, ag)
		}

		// Close merged channel when all agents complete
		go func() {
			wg.Wait()
			close(merged)
		}()

		// Forward merged events
		for event := range merged {
			output <- event
		}
	}()

	return output
}

// CollectEvents is a helper to collect all events from an async agent into a slice.
// Useful for testing or when you need all events at once.
func CollectEvents(events <-chan *AsyncEvent) []*AsyncEvent {
	var result []*AsyncEvent
	for event := range events {
		result = append(result, event)
	}
	return result
}

// GetFinalEvent returns the last non-partial event from a channel.
// Consumes all events and returns the final one.
func GetFinalEvent(events <-chan *AsyncEvent) *AsyncEvent {
	var last *AsyncEvent
	for event := range events {
		if !event.Partial {
			last = event
		}
	}
	return last
}

// ============================================================================
// ADK-Compatible Synchronous Event Loop
// ============================================================================
//
// The ADK pattern requires that agents PAUSE after yielding each event,
// waiting for the Runner to process and commit state changes before continuing.
// This ensures agents can see the effects of committed state when they resume.
//
// In Python:
//   yield event  # Pauses until Runner calls next()
//
// In Go, we achieve this with acknowledgment channels:
//   syncChan <- &SyncEvent{Event: event, Ack: ack}
//   <-ack  # Blocks until Runner signals completion

// SyncEvent wraps an AsyncEvent with an acknowledgment channel.
// The sender blocks on Ack until the receiver has processed the event.
type SyncEvent struct {
	Event *AsyncEvent
	Ack   chan struct{}
}

// NewSyncEvent creates a new synchronous event
func NewSyncEvent(event *AsyncEvent) *SyncEvent {
	return &SyncEvent{
		Event: event,
		Ack:   make(chan struct{}),
	}
}

// SyncEventChannel provides yield-wait semantics for event production.
// Agents send events and block until the Runner acknowledges processing.
type SyncEventChannel struct {
	events chan *SyncEvent
	closed bool
	mu     sync.Mutex
}

// NewSyncEventChannel creates a new sync event channel
func NewSyncEventChannel() *SyncEventChannel {
	return &SyncEventChannel{
		events: make(chan *SyncEvent),
	}
}

// Yield sends an event and blocks until it's processed.
// This is the Go equivalent to Python's `yield event` in an async generator.
func (c *SyncEventChannel) Yield(event *AsyncEvent) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	syncEvent := NewSyncEvent(event)
	c.events <- syncEvent
	<-syncEvent.Ack // Block until Runner acknowledges
}

// Receive returns the channel to receive sync events from
func (c *SyncEventChannel) Receive() <-chan *SyncEvent {
	return c.events
}

// Close closes the event channel
func (c *SyncEventChannel) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.events)
	}
}

// IsClosed returns whether the channel is closed
func (c *SyncEventChannel) IsClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// SyncAsyncAgent is an agent that uses synchronous yield-wait semantics.
// This provides true ADK-compatible event loop behavior.
type SyncAsyncAgent interface {
	// ID returns the unique identifier for this agent
	ID() string

	// Name returns the human-readable name
	Name() string

	// RunSync executes the agent using synchronous event yielding.
	// The agent yields events through the SyncEventChannel and blocks
	// until each event is processed by the Runner.
	RunSync(ctx context.Context, invCtx *InvocationContext, eventChan *SyncEventChannel)
}

// SyncAgentRuntime orchestrates sync agent execution with true ADK semantics.
// This implements the core ADK pattern where:
// 1. Agent yields an event and PAUSES
// 2. Runner processes the event and commits state
// 3. Runner signals completion
// 4. Agent resumes, seeing committed state changes
type SyncAgentRuntime struct {
	services    *RuntimeServices
	partitionID string

	// Configuration
	maxEvents        int
	enableCheckpoint bool

	// Pause control
	shouldPauseFunc func(*AsyncEvent) bool

	// Event log
	eventLog []AsyncEvent
	mu       sync.RWMutex
}

// NewSyncAgentRuntime creates a new sync agent runtime
func NewSyncAgentRuntime(services *RuntimeServices) *SyncAgentRuntime {
	return &SyncAgentRuntime{
		services:         services,
		maxEvents:        1000,
		enableCheckpoint: true,
		eventLog:         make([]AsyncEvent, 0),
	}
}

// WithPartitionID sets the Spark partition ID
func (r *SyncAgentRuntime) WithPartitionID(id string) *SyncAgentRuntime {
	r.partitionID = id
	return r
}

// WithPauseCondition sets a function to check if execution should pause
func (r *SyncAgentRuntime) WithPauseCondition(fn func(*AsyncEvent) bool) *SyncAgentRuntime {
	r.shouldPauseFunc = fn
	return r
}

// RunSync executes a sync agent with true ADK yield-wait semantics.
// This is the core ADK-compatible execution loop:
// 1. Agent yields event via SyncEventChannel (agent PAUSES)
// 2. Runner receives event
// 3. Runner processes and commits state
// 4. Runner acknowledges (agent RESUMES)
// 5. Runner forwards event to caller
// 6. Loop repeats until completion
func (r *SyncAgentRuntime) RunSync(ctx context.Context, ag SyncAsyncAgent, input *agent.AgentInput) <-chan *AsyncEvent {
	output := make(chan *AsyncEvent)

	go func() {
		defer close(output)

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

		// Create sync event channel
		eventChan := NewSyncEventChannel()

		// Start agent in goroutine - it will block on each Yield
		agentDone := make(chan struct{})
		go func() {
			defer close(agentDone)
			defer eventChan.Close()
			ag.RunSync(ctx, invCtx, eventChan)
		}()

		// Process events as they arrive (ADK pattern with true yield-wait)
		for syncEvent := range eventChan.Receive() {
			event := syncEvent.Event
			event.InvocationID = invCtx.InvocationID

			// Process and commit the event BEFORE acknowledging
			if err := r.processEvent(ctx, invCtx, event); err != nil {
				// Acknowledge to unblock agent, then yield error
				close(syncEvent.Ack)
				errorEvent := NewAsyncEvent(EventTypeError, err.Error(), "runtime")
				errorEvent.Error = err
				output <- errorEvent
				return
			}

			// CRITICAL: Acknowledge BEFORE forwarding
			// This allows the agent to resume and see committed state
			close(syncEvent.Ack)

			// Forward event to caller
			output <- event

			// Check pause condition
			if r.shouldPauseFunc != nil && r.shouldPauseFunc(event) {
				return
			}

			// Check for final response
			if event.IsFinalResponse() {
				return
			}

			// Check context cancellation
			select {
			case <-ctx.Done():
				errorEvent := NewAsyncEvent(EventTypeError, ctx.Err().Error(), "runtime")
				errorEvent.Error = ctx.Err()
				output <- errorEvent
				return
			default:
			}
		}

		// Wait for agent to complete
		<-agentDone
	}()

	return output
}

// processEvent handles a single event (same as AsyncAgentRuntime)
func (r *SyncAgentRuntime) processEvent(ctx context.Context, invCtx *InvocationContext, event *AsyncEvent) error {
	// Add to event log
	r.mu.Lock()
	r.eventLog = append(r.eventLog, *event)
	if len(r.eventLog) > r.maxEvents {
		r.eventLog = r.eventLog[len(r.eventLog)-r.maxEvents:]
	}
	r.mu.Unlock()

	// Process state delta from event actions
	if event.Actions != nil && event.Actions.StateDelta != nil {
		for key, value := range event.Actions.StateDelta {
			invCtx.Set(key, value)
		}
	}

	// Commit non-partial events (ADK pattern)
	if !event.Partial {
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
				return fmt.Errorf("failed to persist session: %w", err)
			}
		}

		// Handle artifact delta
		if event.Actions != nil && event.Actions.ArtifactDelta != nil {
			if err := r.processArtifactDelta(ctx, invCtx, event.Actions.ArtifactDelta); err != nil {
				return fmt.Errorf("failed to process artifact delta: %w", err)
			}
		}
	}

	return nil
}

// processArtifactDelta handles artifact changes
func (r *SyncAgentRuntime) processArtifactDelta(ctx context.Context, invCtx *InvocationContext, delta *ArtifactDelta) error {
	if r.services == nil || r.services.ArtifactService == nil {
		return nil
	}
	return nil
}

// GetEventLog returns the event log
func (r *SyncAgentRuntime) GetEventLog() []AsyncEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	events := make([]AsyncEvent, len(r.eventLog))
	copy(events, r.eventLog)
	return events
}

// BaseSyncAgent provides a base implementation for sync agents
type BaseSyncAgent struct {
	id          string
	name        string
	description string
}

// NewBaseSyncAgent creates a new base sync agent
func NewBaseSyncAgent(id, name, description string) *BaseSyncAgent {
	return &BaseSyncAgent{
		id:          id,
		name:        name,
		description: description,
	}
}

func (a *BaseSyncAgent) ID() string          { return a.id }
func (a *BaseSyncAgent) Name() string        { return a.name }
func (a *BaseSyncAgent) Description() string { return a.description }

// LLMSyncAgent is an LLM agent with true ADK yield-wait semantics
type LLMSyncAgent struct {
	*BaseSyncAgent
	flow     *LlmFlow
	maxSteps int
}

// NewLLMSyncAgent creates a new sync LLM agent
func NewLLMSyncAgent(id, name string, flow *LlmFlow) *LLMSyncAgent {
	return &LLMSyncAgent{
		BaseSyncAgent: NewBaseSyncAgent(id, name, "Sync LLM agent with ADK yield-wait semantics"),
		flow:          flow,
		maxSteps:      10,
	}
}

// WithMaxSteps sets the maximum number of steps
func (a *LLMSyncAgent) WithMaxSteps(max int) *LLMSyncAgent {
	a.maxSteps = max
	return a
}

// RunSync implements true ADK yield-wait semantics
func (a *LLMSyncAgent) RunSync(ctx context.Context, invCtx *InvocationContext, eventChan *SyncEventChannel) {
	invCtx.AgentName = a.name

	if a.flow == nil {
		errorEvent := NewAsyncEvent(EventTypeError, "no LLM flow configured", a.name)
		eventChan.Yield(errorEvent)
		return
	}

	step := 0
	for step < a.maxSteps {
		step++

		var lastEvent *AsyncEvent

		// Run one step - this returns a channel (async)
		// We convert to sync by yielding each event and waiting
		stepEvents := a.flow.runOneStepAsync(ctx, invCtx)

		for event := range stepEvents {
			if event.Metadata == nil {
				event.Metadata = make(map[string]interface{})
			}
			event.Metadata["step"] = step
			lastEvent = event

			// YIELD AND WAIT - this is the ADK pattern
			// Agent pauses here until Runner processes and acknowledges
			eventChan.Yield(event)

			// After resume, agent can see committed state changes
			// Check for errors
			if event.Error != nil {
				return
			}
		}

		// Check if we should stop
		if lastEvent == nil {
			return
		}
		if lastEvent.IsFinalResponse() {
			return
		}

		// Check context cancellation
		select {
		case <-ctx.Done():
			errorEvent := NewAsyncEvent(EventTypeError, ctx.Err().Error(), a.name)
			eventChan.Yield(errorEvent)
			return
		default:
		}
	}

	// Max steps reached
	maxStepsEvent := NewAsyncEvent(EventTypeMessage, "Maximum steps reached", a.name)
	maxStepsEvent.Metadata = map[string]interface{}{
		"max_steps_reached": true,
		"steps_executed":    step,
	}
	eventChan.Yield(maxStepsEvent)
}

// WrapAsyncAgentAsSync wraps an AsyncAgent to provide sync semantics.
// This adapter allows existing async agents to be used with SyncAgentRuntime.
// Note: The wrapped agent still uses async internally, but the wrapper
// provides yield-wait semantics at the runtime boundary.
type WrapAsyncAgentAsSync struct {
	asyncAgent AsyncAgent
}

// NewWrapAsyncAgentAsSync creates a wrapper
func NewWrapAsyncAgentAsSync(ag AsyncAgent) *WrapAsyncAgentAsSync {
	return &WrapAsyncAgentAsSync{asyncAgent: ag}
}

func (w *WrapAsyncAgentAsSync) ID() string   { return w.asyncAgent.ID() }
func (w *WrapAsyncAgentAsSync) Name() string { return w.asyncAgent.Name() }

// RunSync wraps the async agent with sync semantics
func (w *WrapAsyncAgentAsSync) RunSync(ctx context.Context, invCtx *InvocationContext, eventChan *SyncEventChannel) {
	// Run the async agent
	asyncEvents := w.asyncAgent.RunAsync(ctx, invCtx)

	// Convert each async event to sync yield-wait
	for event := range asyncEvents {
		eventChan.Yield(event)
	}
}
