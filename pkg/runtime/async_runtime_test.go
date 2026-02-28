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

package runtime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/agent"
)

// Mock async agent for testing
type mockAsyncAgent struct {
	id       string
	name     string
	events   []*AsyncEvent
	delay    time.Duration
	failAt   int // Fail after this many events (-1 = no fail)
}

func newMockAsyncAgent(id string, events []*AsyncEvent) *mockAsyncAgent {
	return &mockAsyncAgent{
		id:     id,
		name:   id,
		events: events,
		failAt: -1,
	}
}

func (a *mockAsyncAgent) ID() string   { return a.id }
func (a *mockAsyncAgent) Name() string { return a.name }

func (a *mockAsyncAgent) RunAsync(ctx context.Context, invCtx *InvocationContext) <-chan *AsyncEvent {
	output := make(chan *AsyncEvent)

	go func() {
		defer close(output)

		for i, event := range a.events {
			// Check for failure
			if a.failAt >= 0 && i >= a.failAt {
				errorEvent := NewAsyncEvent(EventTypeError, "intentional failure", a.name)
				errorEvent.Error = errors.New("intentional failure")
				output <- errorEvent
				return
			}

			// Apply delay
			if a.delay > 0 {
				time.Sleep(a.delay)
			}

			// Check context
			select {
			case <-ctx.Done():
				return
			default:
			}

			output <- event
		}
	}()

	return output
}

// Mock session service for testing
type mockSessionService struct {
	sessions      map[string]*Session
	appendedCount int32
}

func newMockSessionService() *mockSessionService {
	return &mockSessionService{
		sessions: make(map[string]*Session),
	}
}

func (s *mockSessionService) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	if session, ok := s.sessions[sessionID]; ok {
		return session, nil
	}
	return nil, nil
}

func (s *mockSessionService) UpdateSession(ctx context.Context, session *Session) error {
	s.sessions[session.ID] = session
	atomic.AddInt32(&s.appendedCount, 1)
	return nil
}

func (s *mockSessionService) ListSessions(ctx context.Context, userID string) ([]*Session, error) {
	var sessions []*Session
	for _, session := range s.sessions {
		if session.UserID == userID {
			sessions = append(sessions, session)
		}
	}
	return sessions, nil
}

func TestAsyncEvent(t *testing.T) {
	t.Run("NewAsyncEvent", func(t *testing.T) {
		event := NewAsyncEvent(EventTypeMessage, "test content", "test-agent")

		if event.Type != EventTypeMessage {
			t.Errorf("Type = %v, want %v", event.Type, EventTypeMessage)
		}
		if event.Content != "test content" {
			t.Errorf("Content = %v, want 'test content'", event.Content)
		}
		if event.Author != "test-agent" {
			t.Errorf("Author = %v, want 'test-agent'", event.Author)
		}
		if event.ID == "" {
			t.Error("ID should not be empty")
		}
		if event.Actions == nil {
			t.Error("Actions should not be nil")
		}
	})

	t.Run("WithStateDelta", func(t *testing.T) {
		event := NewAsyncEvent(EventTypeMessage, "test", "agent").
			WithStateDelta(map[string]interface{}{"key": "value"})

		if event.Actions.StateDelta["key"] != "value" {
			t.Errorf("StateDelta[key] = %v, want 'value'", event.Actions.StateDelta["key"])
		}
	})

	t.Run("IsFinalResponse", func(t *testing.T) {
		final := NewAsyncEvent(EventTypeMessage, "final", "agent")
		if !final.IsFinalResponse() {
			t.Error("Non-partial message should be final")
		}

		partial := NewAsyncEvent(EventTypeMessage, "partial", "agent").WithPartial(true)
		if partial.IsFinalResponse() {
			t.Error("Partial message should not be final")
		}

		toolCall := NewAsyncEvent(EventTypeToolCall, "tool", "agent")
		if toolCall.IsFinalResponse() {
			t.Error("Tool call should not be final")
		}
	})
}

func TestAsyncAgentRuntime(t *testing.T) {
	t.Run("BasicExecution", func(t *testing.T) {
		events := []*AsyncEvent{
			NewAsyncEvent(EventTypeMessage, "Hello", "test").WithPartial(true),
			NewAsyncEvent(EventTypeMessage, "World", "test"),
		}
		ag := newMockAsyncAgent("test", events)

		runtime := NewAsyncAgentRuntime(nil)
		input := &agent.AgentInput{Instruction: "test"}

		resultEvents := CollectEvents(runtime.RunAsync(context.Background(), ag, input))

		if len(resultEvents) != 2 {
			t.Errorf("Got %d events, want 2", len(resultEvents))
		}
	})

	t.Run("StopsOnFinalResponse", func(t *testing.T) {
		events := []*AsyncEvent{
			NewAsyncEvent(EventTypeMessage, "partial1", "test").WithPartial(true),
			NewAsyncEvent(EventTypeMessage, "final", "test"), // This is final
			NewAsyncEvent(EventTypeMessage, "should not see", "test"),
		}
		ag := newMockAsyncAgent("test", events)

		runtime := NewAsyncAgentRuntime(nil)
		input := &agent.AgentInput{Instruction: "test"}

		resultEvents := CollectEvents(runtime.RunAsync(context.Background(), ag, input))

		// Should stop after final response
		if len(resultEvents) != 2 {
			t.Errorf("Got %d events, want 2 (should stop on final)", len(resultEvents))
		}
	})

	t.Run("CommitsNonPartialEvents", func(t *testing.T) {
		sessionService := newMockSessionService()
		services := &RuntimeServices{SessionService: sessionService}

		events := []*AsyncEvent{
			NewAsyncEvent(EventTypeMessage, "partial1", "test").WithPartial(true),
			NewAsyncEvent(EventTypeMessage, "partial2", "test").WithPartial(true),
			NewAsyncEvent(EventTypeMessage, "final", "test"), // Non-partial
		}
		ag := newMockAsyncAgent("test", events)

		runtime := NewAsyncAgentRuntime(services)
		input := &agent.AgentInput{Instruction: "test"}

		CollectEvents(runtime.RunAsync(context.Background(), ag, input))

		// Only non-partial events should trigger session update
		if sessionService.appendedCount != 1 {
			t.Errorf("Session updated %d times, want 1 (only non-partial)", sessionService.appendedCount)
		}
	})

	t.Run("AppliesStateDelta", func(t *testing.T) {
		events := []*AsyncEvent{
			NewAsyncEvent(EventTypeMessage, "with state", "test").
				WithStateDelta(map[string]interface{}{"key": "value"}),
		}
		ag := newMockAsyncAgent("test", events)

		sessionService := newMockSessionService()
		services := &RuntimeServices{SessionService: sessionService}

		runtime := NewAsyncAgentRuntime(services)
		input := &agent.AgentInput{Instruction: "test"}

		CollectEvents(runtime.RunAsync(context.Background(), ag, input))

		// Verify session was updated
		if sessionService.appendedCount != 1 {
			t.Errorf("Session updated %d times, want 1", sessionService.appendedCount)
		}
	})

	t.Run("PauseCondition", func(t *testing.T) {
		events := []*AsyncEvent{
			NewAsyncEvent(EventTypeToolCall, "tool call", "test"),
			NewAsyncEvent(EventTypeMessage, "should not see", "test"),
		}
		ag := newMockAsyncAgent("test", events)

		runtime := NewAsyncAgentRuntime(nil).
			WithPauseCondition(func(e *AsyncEvent) bool {
				return e.Type == EventTypeToolCall
			})

		input := &agent.AgentInput{Instruction: "test"}

		resultEvents := CollectEvents(runtime.RunAsync(context.Background(), ag, input))

		// Should pause on tool call
		if len(resultEvents) != 1 {
			t.Errorf("Got %d events, want 1 (should pause on tool call)", len(resultEvents))
		}
	})

	t.Run("ContextCancellation", func(t *testing.T) {
		// Create many events to ensure we have time to cancel
		events := make([]*AsyncEvent, 10)
		for i := range events {
			events[i] = NewAsyncEvent(EventTypeMessage, "event", "test").WithPartial(true)
		}
		ag := newMockAsyncAgent("test", events)
		ag.delay = 50 * time.Millisecond // Each event takes 50ms

		runtime := NewAsyncAgentRuntime(nil)
		input := &agent.AgentInput{Instruction: "test"}

		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
		defer cancel()

		resultEvents := CollectEvents(runtime.RunAsync(ctx, ag, input))

		// Should not have received all 10 events due to cancellation
		// We expect 1-2 events before timeout
		if len(resultEvents) >= 10 {
			t.Errorf("Got %d events, expected fewer due to cancellation", len(resultEvents))
		}
	})
}

func TestSimpleAsyncAgent(t *testing.T) {
	t.Run("SuccessfulExecution", func(t *testing.T) {
		simpleAgent := NewSimpleAsyncAgent("test", "Test Agent", func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
			return &agent.AgentOutput{
				Result: "Hello, " + input.Instruction,
				Metadata: map[string]interface{}{
					"source": "test",
				},
			}, nil
		})

		invCtx := NewInvocationContext(context.Background(), &agent.AgentInput{
			Instruction: "World",
		})

		events := CollectEvents(simpleAgent.RunAsync(context.Background(), invCtx))

		if len(events) != 1 {
			t.Fatalf("Got %d events, want 1", len(events))
		}

		if events[0].Content != "Hello, World" {
			t.Errorf("Content = %v, want 'Hello, World'", events[0].Content)
		}
	})

	t.Run("ErrorExecution", func(t *testing.T) {
		errorAgent := NewSimpleAsyncAgent("test", "Test Agent", func(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
			return nil, errors.New("test error")
		})

		invCtx := NewInvocationContext(context.Background(), &agent.AgentInput{})

		events := CollectEvents(errorAgent.RunAsync(context.Background(), invCtx))

		if len(events) != 1 {
			t.Fatalf("Got %d events, want 1", len(events))
		}

		if events[0].Error == nil {
			t.Error("Expected error event")
		}
	})
}

func TestSequentialAsyncAgent(t *testing.T) {
	t.Run("ExecutesInOrder", func(t *testing.T) {
		agent1 := newMockAsyncAgent("agent1", []*AsyncEvent{
			NewAsyncEvent(EventTypeMessage, "first", "agent1"),
		})
		agent2 := newMockAsyncAgent("agent2", []*AsyncEvent{
			NewAsyncEvent(EventTypeMessage, "second", "agent2"),
		})

		sequential := NewSequentialAsyncAgent("seq", "Sequential", []AsyncAgent{agent1, agent2})

		invCtx := NewInvocationContext(context.Background(), &agent.AgentInput{})
		events := CollectEvents(sequential.RunAsync(context.Background(), invCtx))

		// Should have start events + agent events
		if len(events) < 2 {
			t.Errorf("Got %d events, want at least 2", len(events))
		}
	})

	t.Run("StopsOnError", func(t *testing.T) {
		agent1 := newMockAsyncAgent("agent1", []*AsyncEvent{
			NewAsyncEvent(EventTypeMessage, "first", "agent1"),
		})
		agent1.failAt = 0 // Fail immediately

		agent2 := newMockAsyncAgent("agent2", []*AsyncEvent{
			NewAsyncEvent(EventTypeMessage, "should not see", "agent2"),
		})

		sequential := NewSequentialAsyncAgent("seq", "Sequential", []AsyncAgent{agent1, agent2})

		invCtx := NewInvocationContext(context.Background(), &agent.AgentInput{})
		events := CollectEvents(sequential.RunAsync(context.Background(), invCtx))

		// Should have stopped after first agent error
		hasError := false
		for _, e := range events {
			if e.Error != nil {
				hasError = true
			}
			if e.Author == "agent2" {
				t.Error("Should not have reached agent2")
			}
		}
		if !hasError {
			t.Error("Expected error from agent1")
		}
	})
}

func TestParallelAsyncAgent(t *testing.T) {
	t.Run("ExecutesInParallel", func(t *testing.T) {
		agent1 := newMockAsyncAgent("agent1", []*AsyncEvent{
			NewAsyncEvent(EventTypeMessage, "from1", "agent1"),
		})
		agent2 := newMockAsyncAgent("agent2", []*AsyncEvent{
			NewAsyncEvent(EventTypeMessage, "from2", "agent2"),
		})

		parallel := NewParallelAsyncAgent("par", "Parallel", []AsyncAgent{agent1, agent2})

		invCtx := NewInvocationContext(context.Background(), &agent.AgentInput{})
		events := CollectEvents(parallel.RunAsync(context.Background(), invCtx))

		// Should have events from both agents
		if len(events) != 2 {
			t.Errorf("Got %d events, want 2", len(events))
		}

		// Verify we got events from both
		authors := make(map[string]bool)
		for _, e := range events {
			authors[e.Author] = true
		}
		if !authors["agent1"] || !authors["agent2"] {
			t.Errorf("Expected events from both agents, got authors: %v", authors)
		}
	})
}

func TestLLMAsyncAgent(t *testing.T) {
	t.Run("StepExecution", func(t *testing.T) {
		stepCount := 0
		executor := &mockStepExecutor{
			steps: []func() []*AsyncEvent{
				func() []*AsyncEvent {
					stepCount++
					return []*AsyncEvent{
						NewAsyncEvent(EventTypeToolCall, "call tool", "llm").WithPartial(true),
					}
				},
				func() []*AsyncEvent {
					stepCount++
					return []*AsyncEvent{
						NewAsyncEvent(EventTypeMessage, "final response", "llm"),
					}
				},
			},
		}

		llmAgent := NewLLMAsyncAgent("llm", "LLM Agent", executor)

		invCtx := NewInvocationContext(context.Background(), &agent.AgentInput{})
		events := CollectEvents(llmAgent.RunAsync(context.Background(), invCtx))

		if stepCount != 2 {
			t.Errorf("Executed %d steps, want 2", stepCount)
		}

		if len(events) != 2 {
			t.Errorf("Got %d events, want 2", len(events))
		}
	})

	t.Run("MaxStepsLimit", func(t *testing.T) {
		executor := &mockStepExecutor{
			steps: []func() []*AsyncEvent{
				// Each step returns partial, so loop continues
				func() []*AsyncEvent {
					return []*AsyncEvent{
						NewAsyncEvent(EventTypeToolCall, "step", "llm").WithPartial(true),
					}
				},
			},
			repeat: true, // Repeat the step
		}

		llmAgent := NewLLMAsyncAgent("llm", "LLM Agent", executor).WithMaxSteps(3)

		invCtx := NewInvocationContext(context.Background(), &agent.AgentInput{})
		events := CollectEvents(llmAgent.RunAsync(context.Background(), invCtx))

		// Should have hit max steps
		lastEvent := events[len(events)-1]
		if lastEvent.Metadata["max_steps_reached"] != true {
			t.Error("Expected max_steps_reached in last event")
		}
	})
}

// Mock step executor
type mockStepExecutor struct {
	steps       []func() []*AsyncEvent
	currentStep int
	repeat      bool
}

func (e *mockStepExecutor) ExecuteStep(ctx context.Context, invCtx *InvocationContext) <-chan *AsyncEvent {
	output := make(chan *AsyncEvent)

	go func() {
		defer close(output)

		if e.currentStep >= len(e.steps) {
			if e.repeat {
				e.currentStep = 0
			} else {
				return
			}
		}

		events := e.steps[e.currentStep]()
		e.currentStep++

		for _, event := range events {
			output <- event
		}
	}()

	return output
}

func TestCollectEvents(t *testing.T) {
	ch := make(chan *AsyncEvent, 3)
	ch <- NewAsyncEvent(EventTypeMessage, "1", "test")
	ch <- NewAsyncEvent(EventTypeMessage, "2", "test")
	ch <- NewAsyncEvent(EventTypeMessage, "3", "test")
	close(ch)

	events := CollectEvents(ch)

	if len(events) != 3 {
		t.Errorf("Got %d events, want 3", len(events))
	}
}

func TestGetFinalEvent(t *testing.T) {
	ch := make(chan *AsyncEvent, 3)
	ch <- NewAsyncEvent(EventTypeMessage, "partial", "test").WithPartial(true)
	ch <- NewAsyncEvent(EventTypeMessage, "final1", "test")
	ch <- NewAsyncEvent(EventTypeMessage, "partial2", "test").WithPartial(true)
	close(ch)

	final := GetFinalEvent(ch)

	if final == nil {
		t.Fatal("Expected final event")
	}
	if final.Content != "final1" {
		t.Errorf("Content = %v, want 'final1'", final.Content)
	}
}

// ============================================================================
// Tests for ADK-Compatible Synchronous Event Loop
// ============================================================================

func TestSyncEvent(t *testing.T) {
	t.Run("NewSyncEvent", func(t *testing.T) {
		event := NewAsyncEvent(EventTypeMessage, "test", "agent")
		syncEvent := NewSyncEvent(event)

		if syncEvent.Event != event {
			t.Error("SyncEvent should wrap the original event")
		}
		if syncEvent.Ack == nil {
			t.Error("SyncEvent should have an Ack channel")
		}
	})

	t.Run("AckChannelBlocks", func(t *testing.T) {
		event := NewAsyncEvent(EventTypeMessage, "test", "agent")
		syncEvent := NewSyncEvent(event)

		// Verify the Ack channel blocks until closed
		select {
		case <-syncEvent.Ack:
			t.Error("Ack channel should block until closed")
		default:
			// Expected - channel is blocking
		}

		// Now close and verify it unblocks
		close(syncEvent.Ack)
		select {
		case <-syncEvent.Ack:
			// Expected - channel is now closed
		default:
			t.Error("Ack channel should unblock after close")
		}
	})
}

func TestSyncEventChannel(t *testing.T) {
	t.Run("YieldAndReceive", func(t *testing.T) {
		eventChan := NewSyncEventChannel()
		event := NewAsyncEvent(EventTypeMessage, "test", "agent")

		// Start a goroutine to receive the event
		received := make(chan *AsyncEvent)
		go func() {
			for syncEvent := range eventChan.Receive() {
				received <- syncEvent.Event
				close(syncEvent.Ack) // Acknowledge
			}
		}()

		// Yield event in a separate goroutine (since it blocks)
		done := make(chan struct{})
		go func() {
			eventChan.Yield(event)
			close(done)
		}()

		// Wait for the event to be received
		select {
		case rcvd := <-received:
			if rcvd.Content != "test" {
				t.Errorf("Content = %v, want 'test'", rcvd.Content)
			}
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for event")
		}

		// Yield should complete after acknowledgment
		select {
		case <-done:
			// Expected
		case <-time.After(time.Second):
			t.Fatal("Yield should complete after acknowledgment")
		}

		eventChan.Close()
	})

	t.Run("YieldBlocksUntilAcknowledged", func(t *testing.T) {
		eventChan := NewSyncEventChannel()
		event := NewAsyncEvent(EventTypeMessage, "test", "agent")

		yieldCompleted := make(chan struct{})

		// Yield in background
		go func() {
			eventChan.Yield(event)
			close(yieldCompleted)
		}()

		// Receive without acknowledging yet
		var syncEvent *SyncEvent
		select {
		case syncEvent = <-eventChan.Receive():
			// Got the event
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for event")
		}

		// Yield should still be blocked
		select {
		case <-yieldCompleted:
			t.Fatal("Yield should block until acknowledged")
		case <-time.After(100 * time.Millisecond):
			// Expected - still blocked
		}

		// Now acknowledge
		close(syncEvent.Ack)

		// Yield should complete
		select {
		case <-yieldCompleted:
			// Expected
		case <-time.After(time.Second):
			t.Fatal("Yield should complete after acknowledgment")
		}

		eventChan.Close()
	})

	t.Run("CloseStopsYield", func(t *testing.T) {
		eventChan := NewSyncEventChannel()
		eventChan.Close()

		if !eventChan.IsClosed() {
			t.Error("Channel should be closed")
		}

		// Yield on closed channel should return immediately
		done := make(chan struct{})
		go func() {
			eventChan.Yield(NewAsyncEvent(EventTypeMessage, "test", "agent"))
			close(done)
		}()

		select {
		case <-done:
			// Expected - returns immediately on closed channel
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Yield on closed channel should return immediately")
		}
	})

	t.Run("MultipleYields", func(t *testing.T) {
		eventChan := NewSyncEventChannel()
		events := []string{"first", "second", "third"}

		// Receiver goroutine
		receivedOrder := make([]string, 0, len(events))
		receiveComplete := make(chan struct{})
		go func() {
			for syncEvent := range eventChan.Receive() {
				receivedOrder = append(receivedOrder, syncEvent.Event.Content.(string))
				close(syncEvent.Ack)
			}
			close(receiveComplete)
		}()

		// Yield all events
		for _, content := range events {
			eventChan.Yield(NewAsyncEvent(EventTypeMessage, content, "agent"))
		}

		eventChan.Close()

		// Wait for receiver to complete
		select {
		case <-receiveComplete:
		case <-time.After(time.Second):
			t.Fatal("Timeout waiting for receiver")
		}

		// Verify order
		if len(receivedOrder) != len(events) {
			t.Fatalf("Got %d events, want %d", len(receivedOrder), len(events))
		}
		for i, content := range events {
			if receivedOrder[i] != content {
				t.Errorf("Event %d = %v, want %v", i, receivedOrder[i], content)
			}
		}
	})
}

// mockSyncAgent is a test agent with sync semantics
type mockSyncAgent struct {
	id     string
	name   string
	events []*AsyncEvent
	// Track when each yield returned (for testing timing)
	yieldReturns []time.Time
	mu           sync.Mutex
}

func newMockSyncAgent(id string, events []*AsyncEvent) *mockSyncAgent {
	return &mockSyncAgent{
		id:           id,
		name:         id,
		events:       events,
		yieldReturns: make([]time.Time, 0),
	}
}

func (a *mockSyncAgent) ID() string   { return a.id }
func (a *mockSyncAgent) Name() string { return a.name }

func (a *mockSyncAgent) RunSync(ctx context.Context, invCtx *InvocationContext, eventChan *SyncEventChannel) {
	for _, event := range a.events {
		select {
		case <-ctx.Done():
			return
		default:
		}
		eventChan.Yield(event)
		// Record when yield returned (after acknowledgment)
		a.mu.Lock()
		a.yieldReturns = append(a.yieldReturns, time.Now())
		a.mu.Unlock()
	}
}

func (a *mockSyncAgent) GetYieldReturns() []time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	returns := make([]time.Time, len(a.yieldReturns))
	copy(returns, a.yieldReturns)
	return returns
}

// slowMockSyncAgent is like mockSyncAgent but adds delay between yields
type slowMockSyncAgent struct {
	*mockSyncAgent
	delay time.Duration
}

func newSlowMockSyncAgent(id string, events []*AsyncEvent, delay time.Duration) *slowMockSyncAgent {
	return &slowMockSyncAgent{
		mockSyncAgent: newMockSyncAgent(id, events),
		delay:         delay,
	}
}

func (a *slowMockSyncAgent) RunSync(ctx context.Context, invCtx *InvocationContext, eventChan *SyncEventChannel) {
	for _, event := range a.events {
		select {
		case <-ctx.Done():
			return
		default:
		}
		// Add delay before yielding
		time.Sleep(a.delay)
		eventChan.Yield(event)
		a.mu.Lock()
		a.yieldReturns = append(a.yieldReturns, time.Now())
		a.mu.Unlock()
	}
}

func TestSyncAgentRuntime(t *testing.T) {
	t.Run("BasicExecution", func(t *testing.T) {
		events := []*AsyncEvent{
			NewAsyncEvent(EventTypeMessage, "Hello", "test").WithPartial(true),
			NewAsyncEvent(EventTypeMessage, "World", "test"),
		}
		ag := newMockSyncAgent("test", events)

		runtime := NewSyncAgentRuntime(nil)
		input := &agent.AgentInput{Instruction: "test"}

		resultEvents := CollectEvents(runtime.RunSync(context.Background(), ag, input))

		if len(resultEvents) != 2 {
			t.Errorf("Got %d events, want 2", len(resultEvents))
		}
	})

	t.Run("YieldWaitSemantics", func(t *testing.T) {
		// This test verifies the agent truly pauses until Runner acknowledges
		// Use only partial events so the loop continues for all events

		events := []*AsyncEvent{
			NewAsyncEvent(EventTypeMessage, "event1", "test").WithPartial(true),
			NewAsyncEvent(EventTypeMessage, "event2", "test").WithPartial(true),
			NewAsyncEvent(EventTypeMessage, "event3", "test").WithPartial(true),
		}
		ag := newMockSyncAgent("test", events)

		// Use a session service to add processing delay
		sessionService := newMockSessionService()
		services := &RuntimeServices{SessionService: sessionService}

		runtime := NewSyncAgentRuntime(services)
		input := &agent.AgentInput{Instruction: "test"}

		start := time.Now()
		resultEvents := CollectEvents(runtime.RunSync(context.Background(), ag, input))
		_ = time.Since(start)

		// Verify all events received (partial events don't stop the loop,
		// loop only stops when channel closes or final response)
		if len(resultEvents) != 3 {
			t.Errorf("Got %d events, want 3", len(resultEvents))
		}

		// Verify yields returned in order (showing sequential execution)
		returns := ag.GetYieldReturns()
		if len(returns) != 3 {
			t.Errorf("Got %d yield returns, want 3", len(returns))
		}
		for i := 1; i < len(returns); i++ {
			if returns[i].Before(returns[i-1]) {
				t.Error("Yields should return in sequential order")
			}
		}
	})

	t.Run("StateCommittedBeforeResume", func(t *testing.T) {
		// This test verifies state is committed BEFORE agent resumes

		// Agent that checks state after each yield
		ag := &stateCheckingSyncAgent{
			id:            "state-checker",
			name:          "State Checker",
			expectedState: make(map[string]interface{}),
		}

		sessionService := newMockSessionService()
		services := &RuntimeServices{SessionService: sessionService}

		runtime := NewSyncAgentRuntime(services)
		input := &agent.AgentInput{Instruction: "test"}

		CollectEvents(runtime.RunSync(context.Background(), ag, input))

		// If state wasn't committed before resume, stateVerified would be false
		if !ag.stateVerified {
			t.Error("State should be committed before agent resumes")
		}
	})

	t.Run("StopsOnFinalResponse", func(t *testing.T) {
		events := []*AsyncEvent{
			NewAsyncEvent(EventTypeMessage, "partial", "test").WithPartial(true),
			NewAsyncEvent(EventTypeMessage, "final", "test"),
			NewAsyncEvent(EventTypeMessage, "should not see", "test"),
		}
		ag := newMockSyncAgent("test", events)

		runtime := NewSyncAgentRuntime(nil)
		input := &agent.AgentInput{Instruction: "test"}

		resultEvents := CollectEvents(runtime.RunSync(context.Background(), ag, input))

		if len(resultEvents) != 2 {
			t.Errorf("Got %d events, want 2", len(resultEvents))
		}
	})

	t.Run("PauseCondition", func(t *testing.T) {
		events := []*AsyncEvent{
			NewAsyncEvent(EventTypeToolCall, "tool call", "test"),
			NewAsyncEvent(EventTypeMessage, "should not see", "test"),
		}
		ag := newMockSyncAgent("test", events)

		runtime := NewSyncAgentRuntime(nil).
			WithPauseCondition(func(e *AsyncEvent) bool {
				return e.Type == EventTypeToolCall
			})

		input := &agent.AgentInput{Instruction: "test"}

		resultEvents := CollectEvents(runtime.RunSync(context.Background(), ag, input))

		if len(resultEvents) != 1 {
			t.Errorf("Got %d events, want 1 (should pause on tool call)", len(resultEvents))
		}
	})

	t.Run("ContextCancellation", func(t *testing.T) {
		// Create many events with a slow agent
		events := make([]*AsyncEvent, 10)
		for i := range events {
			events[i] = NewAsyncEvent(EventTypeMessage, "event", "test").WithPartial(true)
		}
		ag := newSlowMockSyncAgent("test", events, 20*time.Millisecond)

		runtime := NewSyncAgentRuntime(nil)
		input := &agent.AgentInput{Instruction: "test"}

		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Millisecond)
		defer cancel()

		resultEvents := CollectEvents(runtime.RunSync(ctx, ag, input))

		// Should not have received all 10 events due to cancellation
		// With 20ms delay per event and 75ms timeout, expect 3-4 events
		if len(resultEvents) >= 10 {
			t.Errorf("Got %d events, expected fewer due to cancellation", len(resultEvents))
		}
	})

	t.Run("EventLog", func(t *testing.T) {
		events := []*AsyncEvent{
			NewAsyncEvent(EventTypeMessage, "event1", "test").WithPartial(true),
			NewAsyncEvent(EventTypeMessage, "event2", "test"),
		}
		ag := newMockSyncAgent("test", events)

		runtime := NewSyncAgentRuntime(nil)
		input := &agent.AgentInput{Instruction: "test"}

		CollectEvents(runtime.RunSync(context.Background(), ag, input))

		eventLog := runtime.GetEventLog()
		if len(eventLog) != 2 {
			t.Errorf("Event log has %d events, want 2", len(eventLog))
		}
	})
}

// stateCheckingSyncAgent verifies state is committed before resume
type stateCheckingSyncAgent struct {
	id            string
	name          string
	expectedState map[string]interface{}
	stateVerified bool
	step          int
}

func (a *stateCheckingSyncAgent) ID() string   { return a.id }
func (a *stateCheckingSyncAgent) Name() string { return a.name }

func (a *stateCheckingSyncAgent) RunSync(ctx context.Context, invCtx *InvocationContext, eventChan *SyncEventChannel) {
	// Step 1: Yield event with state delta
	a.step = 1
	event1 := NewAsyncEvent(EventTypeMessage, "step1", a.name).WithPartial(true)
	event1.Actions = &EventActions{
		StateDelta: map[string]interface{}{"step1_key": "step1_value"},
	}
	eventChan.Yield(event1)

	// After resume, check if state was committed
	// Note: The runtime commits state delta before acknowledging
	// so invCtx should have the committed state
	if val, exists := invCtx.Get("step1_key"); exists && val == "step1_value" {
		a.stateVerified = true
	}

	// Step 2: Final event
	a.step = 2
	event2 := NewAsyncEvent(EventTypeMessage, "final", a.name)
	eventChan.Yield(event2)
}

func TestWrapAsyncAgentAsSync(t *testing.T) {
	t.Run("WrapsCorrectly", func(t *testing.T) {
		events := []*AsyncEvent{
			NewAsyncEvent(EventTypeMessage, "event1", "async").WithPartial(true),
			NewAsyncEvent(EventTypeMessage, "final", "async"),
		}
		asyncAg := newMockAsyncAgent("async", events)

		wrapper := NewWrapAsyncAgentAsSync(asyncAg)

		if wrapper.ID() != "async" {
			t.Errorf("ID = %v, want 'async'", wrapper.ID())
		}
		if wrapper.Name() != "async" {
			t.Errorf("Name = %v, want 'async'", wrapper.Name())
		}
	})

	t.Run("ExecutesWithSyncSemantics", func(t *testing.T) {
		events := []*AsyncEvent{
			NewAsyncEvent(EventTypeMessage, "event1", "async").WithPartial(true),
			NewAsyncEvent(EventTypeMessage, "final", "async"),
		}
		asyncAg := newMockAsyncAgent("async", events)

		wrapper := NewWrapAsyncAgentAsSync(asyncAg)

		runtime := NewSyncAgentRuntime(nil)
		input := &agent.AgentInput{Instruction: "test"}

		resultEvents := CollectEvents(runtime.RunSync(context.Background(), wrapper, input))

		if len(resultEvents) != 2 {
			t.Errorf("Got %d events, want 2", len(resultEvents))
		}
	})
}

func TestAsyncVsSyncComparison(t *testing.T) {
	// This test demonstrates the difference between async and sync runtimes

	t.Run("BothProduceSameResults", func(t *testing.T) {
		events := []*AsyncEvent{
			NewAsyncEvent(EventTypeMessage, "event1", "test").WithPartial(true),
			NewAsyncEvent(EventTypeMessage, "event2", "test").WithPartial(true),
			NewAsyncEvent(EventTypeMessage, "final", "test"),
		}

		// Create both agent types
		asyncAg := newMockAsyncAgent("test", events)
		syncAg := newMockSyncAgent("test", events)

		asyncRuntime := NewAsyncAgentRuntime(nil)
		syncRuntime := NewSyncAgentRuntime(nil)

		input := &agent.AgentInput{Instruction: "test"}

		asyncResults := CollectEvents(asyncRuntime.RunAsync(context.Background(), asyncAg, input))
		syncResults := CollectEvents(syncRuntime.RunSync(context.Background(), syncAg, input))

		// Same number of events
		if len(asyncResults) != len(syncResults) {
			t.Errorf("Async got %d events, sync got %d", len(asyncResults), len(syncResults))
		}

		// Same content
		for i := range asyncResults {
			if asyncResults[i].Content != syncResults[i].Content {
				t.Errorf("Event %d: async=%v, sync=%v",
					i, asyncResults[i].Content, syncResults[i].Content)
			}
		}
	})
}

// Benchmark to compare async vs sync performance
func BenchmarkAsyncVsSync(b *testing.B) {
	events := make([]*AsyncEvent, 100)
	for i := range events {
		if i == len(events)-1 {
			events[i] = NewAsyncEvent(EventTypeMessage, "final", "test")
		} else {
			events[i] = NewAsyncEvent(EventTypeMessage, "partial", "test").WithPartial(true)
		}
	}

	b.Run("AsyncRuntime", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ag := newMockAsyncAgent("test", events)
			runtime := NewAsyncAgentRuntime(nil)
			input := &agent.AgentInput{Instruction: "test"}
			CollectEvents(runtime.RunAsync(context.Background(), ag, input))
		}
	})

	b.Run("SyncRuntime", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			ag := newMockSyncAgent("test", events)
			runtime := NewSyncAgentRuntime(nil)
			input := &agent.AgentInput{Instruction: "test"}
			CollectEvents(runtime.RunSync(context.Background(), ag, input))
		}
	})
}
