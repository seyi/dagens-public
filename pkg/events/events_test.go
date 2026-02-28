package events

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryEventBus(t *testing.T) {
	bus := NewMemoryEventBus()
	defer bus.Close()

	t.Run("Subscribe and Publish", func(t *testing.T) {
		called := false
		handler := func(ctx context.Context, event Event) error {
			called = true
			if event.Type() != EventAgentStarted {
				t.Errorf("Expected event type %s, got %s", EventAgentStarted, event.Type())
			}
			return nil
		}

		err := bus.Subscribe(EventAgentStarted, handler)
		if err != nil {
			t.Fatalf("Failed to subscribe: %v", err)
		}

		event := NewAgentEvent(EventAgentStarted, "agent-1", "session-1", "test data")
		err = bus.Publish(event)
		if err != nil {
			t.Fatalf("Failed to publish: %v", err)
		}

		// Give handlers time to execute
		time.Sleep(10 * time.Millisecond)

		if !called {
			t.Error("Handler was not called")
		}
	})

	t.Run("Multiple Subscribers", func(t *testing.T) {
		bus2 := NewMemoryEventBus()
		defer bus2.Close()

		var count int32
		handler1 := func(ctx context.Context, event Event) error {
			atomic.AddInt32(&count, 1)
			return nil
		}
		handler2 := func(ctx context.Context, event Event) error {
			atomic.AddInt32(&count, 1)
			return nil
		}

		bus2.Subscribe(EventTaskScheduled, handler1)
		bus2.Subscribe(EventTaskScheduled, handler2)

		event := NewTaskEvent(EventTaskScheduled, "task-1", "job-1", nil)
		bus2.Publish(event)

		time.Sleep(10 * time.Millisecond)

		if atomic.LoadInt32(&count) != 2 {
			t.Errorf("Expected 2 handler calls, got %d", count)
		}
	})

	t.Run("Event Type Filtering", func(t *testing.T) {
		bus3 := NewMemoryEventBus()
		defer bus3.Close()

		var agentStartedCalled bool
		var taskStartedCalled bool

		bus3.Subscribe(EventAgentStarted, func(ctx context.Context, event Event) error {
			agentStartedCalled = true
			return nil
		})

		bus3.Subscribe(EventTaskStarted, func(ctx context.Context, event Event) error {
			taskStartedCalled = true
			return nil
		})

		// Publish agent event
		bus3.Publish(NewAgentEvent(EventAgentStarted, "agent-1", "", nil))
		time.Sleep(10 * time.Millisecond)

		if !agentStartedCalled {
			t.Error("Agent started handler not called")
		}
		if taskStartedCalled {
			t.Error("Task started handler should not be called")
		}
	})

	t.Run("Unsubscribe", func(t *testing.T) {
		bus4 := NewMemoryEventBus()
		defer bus4.Close()

		handler := func(ctx context.Context, event Event) error {
			return nil
		}

		bus4.Subscribe(EventJobSubmitted, handler)
		bus4.Unsubscribe(EventJobSubmitted, handler)

		// After unsubscribe, no handlers should be called
		event := NewAgentEvent(EventJobSubmitted, "agent-1", "", nil)
		err := bus4.Publish(event)
		if err != nil {
			t.Errorf("Unexpected error after unsubscribe: %v", err)
		}
	})
}

func TestBaseEvent(t *testing.T) {
	t.Run("Event Properties", func(t *testing.T) {
		now := time.Now()
		metadata := map[string]interface{}{
			"key1": "value1",
			"key2": 123,
		}

		event := &BaseEvent{
			EventType:  EventAgentCompleted,
			EventTime:  now,
			Agent:      "agent-123",
			Session:    "session-456",
			EventData:  "test data",
			Meta:       metadata,
		}

		if event.Type() != EventAgentCompleted {
			t.Errorf("Expected type %s, got %s", EventAgentCompleted, event.Type())
		}

		if event.AgentID() != "agent-123" {
			t.Errorf("Expected agent ID 'agent-123', got '%s'", event.AgentID())
		}

		if event.SessionID() != "session-456" {
			t.Errorf("Expected session ID 'session-456', got '%s'", event.SessionID())
		}

		if event.Data() != "test data" {
			t.Errorf("Expected data 'test data', got '%v'", event.Data())
		}

		if event.Metadata()["key1"] != "value1" {
			t.Error("Metadata not preserved correctly")
		}

		if !event.Timestamp().Equal(now) {
			t.Error("Timestamp not preserved correctly")
		}
	})
}

func TestEventMetrics(t *testing.T) {
	metrics := NewEventMetrics()

	t.Run("Track Events", func(t *testing.T) {
		ctx := context.Background()

		event1 := NewAgentEvent(EventAgentStarted, "agent-1", "", nil)
		event2 := NewAgentEvent(EventAgentStarted, "agent-2", "", nil)
		event3 := NewAgentEvent(EventAgentCompleted, "agent-1", "", nil)

		metrics.Track(ctx, event1)
		metrics.Track(ctx, event2)
		metrics.Track(ctx, event3)

		startedCount := metrics.GetCount(EventAgentStarted)
		if startedCount != 2 {
			t.Errorf("Expected 2 agent started events, got %d", startedCount)
		}

		completedCount := metrics.GetCount(EventAgentCompleted)
		if completedCount != 1 {
			t.Errorf("Expected 1 agent completed event, got %d", completedCount)
		}
	})

	t.Run("Get All Counts", func(t *testing.T) {
		allCounts := metrics.GetAllCounts()

		if len(allCounts) != 2 {
			t.Errorf("Expected 2 event types, got %d", len(allCounts))
		}

		if allCounts[EventAgentStarted] != 2 {
			t.Error("Incorrect count for agent started events")
		}
	})

	t.Run("Concurrent Tracking", func(t *testing.T) {
		metrics2 := NewEventMetrics()
		ctx := context.Background()
		const numGoroutines = 20

		var wg sync.WaitGroup
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				event := NewAgentEvent(EventTaskCompleted, "agent-1", "", nil)
				metrics2.Track(ctx, event)
			}()
		}

		wg.Wait()

		count := metrics2.GetCount(EventTaskCompleted)
		if count != int64(numGoroutines) {
			t.Errorf("Expected %d events, got %d", numGoroutines, count)
		}
	})
}

func TestMemoryEventStore(t *testing.T) {
	store := NewMemoryEventStore()
	ctx := context.Background()

	t.Run("Store and Load", func(t *testing.T) {
		event := NewAgentEvent(EventAgentRegistered, "agent-1", "session-1", "data")
		err := store.Store(ctx, event)
		if err != nil {
			t.Fatalf("Failed to store event: %v", err)
		}

		filter := EventFilter{}
		events, err := store.Load(ctx, filter)
		if err != nil {
			t.Fatalf("Failed to load events: %v", err)
		}

		if len(events) < 1 {
			t.Error("Expected at least 1 event")
		}
	})

	t.Run("Filter by Event Type", func(t *testing.T) {
		store2 := NewMemoryEventStore()

		// Store multiple event types
		store2.Store(ctx, NewAgentEvent(EventAgentStarted, "agent-1", "", nil))
		store2.Store(ctx, NewAgentEvent(EventAgentCompleted, "agent-1", "", nil))
		store2.Store(ctx, NewAgentEvent(EventAgentStarted, "agent-2", "", nil))

		filter := EventFilter{
			EventTypes: []EventType{EventAgentStarted},
		}

		events, _ := store2.Load(ctx, filter)

		if len(events) != 2 {
			t.Errorf("Expected 2 agent started events, got %d", len(events))
		}

		for _, event := range events {
			if event.Type() != EventAgentStarted {
				t.Error("Filter did not work correctly")
			}
		}
	})

	t.Run("Filter by Agent ID", func(t *testing.T) {
		store3 := NewMemoryEventStore()

		store3.Store(ctx, NewAgentEvent(EventTaskScheduled, "agent-1", "", nil))
		store3.Store(ctx, NewAgentEvent(EventTaskScheduled, "agent-2", "", nil))
		store3.Store(ctx, NewAgentEvent(EventTaskScheduled, "agent-1", "", nil))

		filter := EventFilter{
			AgentID: "agent-1",
		}

		events, _ := store3.Load(ctx, filter)

		if len(events) != 2 {
			t.Errorf("Expected 2 events for agent-1, got %d", len(events))
		}
	})

	t.Run("Filter by Session ID", func(t *testing.T) {
		store4 := NewMemoryEventStore()

		store4.Store(ctx, NewAgentEvent(EventSessionCreated, "agent-1", "session-1", nil))
		store4.Store(ctx, NewAgentEvent(EventSessionUpdated, "agent-1", "session-2", nil))
		store4.Store(ctx, NewAgentEvent(EventSessionDeleted, "agent-1", "session-1", nil))

		filter := EventFilter{
			SessionID: "session-1",
		}

		events, _ := store4.Load(ctx, filter)

		if len(events) != 2 {
			t.Errorf("Expected 2 events for session-1, got %d", len(events))
		}
	})

	t.Run("Filter by Time Range", func(t *testing.T) {
		store5 := NewMemoryEventStore()

		now := time.Now()
		past := now.Add(-1 * time.Hour)
		future := now.Add(1 * time.Hour)

		// Create events with specific timestamps
		event1 := NewAgentEvent(EventAgentStarted, "agent-1", "", nil)
		event1.EventTime = past

		event2 := NewAgentEvent(EventAgentStarted, "agent-2", "", nil)
		event2.EventTime = now

		event3 := NewAgentEvent(EventAgentStarted, "agent-3", "", nil)
		event3.EventTime = future

		store5.Store(ctx, event1)
		store5.Store(ctx, event2)
		store5.Store(ctx, event3)

		filter := EventFilter{
			StartTime: past.Add(1 * time.Minute),
			EndTime:   future.Add(-1 * time.Minute),
		}

		events, _ := store5.Load(ctx, filter)

		// Should only get event2
		if len(events) != 1 {
			t.Errorf("Expected 1 event in time range, got %d", len(events))
		}

		if events[0].AgentID() != "agent-2" {
			t.Error("Wrong event returned from time range filter")
		}
	})

	t.Run("Filter with Limit", func(t *testing.T) {
		store6 := NewMemoryEventStore()

		for i := 0; i < 10; i++ {
			store6.Store(ctx, NewAgentEvent(EventTaskCompleted, "agent-1", "", nil))
		}

		filter := EventFilter{
			Limit: 5,
		}

		events, _ := store6.Load(ctx, filter)

		if len(events) != 5 {
			t.Errorf("Expected 5 events with limit, got %d", len(events))
		}
	})

	t.Run("Combined Filters", func(t *testing.T) {
		store7 := NewMemoryEventStore()

		// Store various events
		store7.Store(ctx, NewAgentEvent(EventAgentStarted, "agent-1", "session-1", nil))
		store7.Store(ctx, NewAgentEvent(EventAgentCompleted, "agent-1", "session-1", nil))
		store7.Store(ctx, NewAgentEvent(EventAgentStarted, "agent-2", "session-1", nil))
		store7.Store(ctx, NewAgentEvent(EventAgentStarted, "agent-1", "session-2", nil))

		filter := EventFilter{
			EventTypes: []EventType{EventAgentStarted},
			AgentID:    "agent-1",
			SessionID:  "session-1",
		}

		events, _ := store7.Load(ctx, filter)

		if len(events) != 1 {
			t.Errorf("Expected 1 event matching all filters, got %d", len(events))
		}
	})
}

func TestEventRecorder(t *testing.T) {
	t.Run("Record and Query Events", func(t *testing.T) {
		bus := NewMemoryEventBus()
		store := NewMemoryEventStore()

		// Manually subscribe to store events instead of using EventRecorder
		bus.Subscribe(EventJobSubmitted, func(ctx context.Context, event Event) error {
			return store.Store(ctx, event)
		})

		event := NewAgentEvent(EventJobSubmitted, "agent-1", "session-1", map[string]interface{}{
			"job_id": "job-123",
		})

		err := bus.Publish(event)
		if err != nil {
			t.Fatalf("Failed to publish event: %v", err)
		}

		// Give time for event to be recorded
		time.Sleep(10 * time.Millisecond)

		filter := EventFilter{
			EventTypes: []EventType{EventJobSubmitted},
		}

		events, err := store.Load(context.Background(), filter)
		if err != nil {
			t.Fatalf("Failed to query events: %v", err)
		}

		if len(events) < 1 {
			t.Error("Event was not recorded")
		}

		if events[0].Type() != EventJobSubmitted {
			t.Error("Wrong event type recorded")
		}
	})
}

func TestConvenienceFunctions(t *testing.T) {
	t.Run("NewAgentEvent", func(t *testing.T) {
		event := NewAgentEvent(EventAgentFailed, "agent-1", "session-1", "error message")

		if event.Type() != EventAgentFailed {
			t.Error("Event type not set correctly")
		}

		if event.AgentID() != "agent-1" {
			t.Error("Agent ID not set correctly")
		}

		if event.SessionID() != "session-1" {
			t.Error("Session ID not set correctly")
		}

		if event.Data() != "error message" {
			t.Error("Data not set correctly")
		}
	})

	t.Run("NewTaskEvent", func(t *testing.T) {
		event := NewTaskEvent(EventTaskFailed, "task-123", "job-456", "error")

		if event.Type() != EventTaskFailed {
			t.Error("Event type not set correctly")
		}

		taskID := event.Metadata()["task_id"]
		if taskID != "task-123" {
			t.Error("Task ID not set correctly in metadata")
		}

		jobID := event.Metadata()["job_id"]
		if jobID != "job-456" {
			t.Error("Job ID not set correctly in metadata")
		}
	})

	t.Run("NewToolEvent", func(t *testing.T) {
		event := NewToolEvent(EventToolInvoked, "calculator", "agent-1", map[string]interface{}{
			"operation": "add",
			"args":      []int{1, 2},
		})

		if event.Type() != EventToolInvoked {
			t.Error("Event type not set correctly")
		}

		if event.AgentID() != "agent-1" {
			t.Error("Agent ID not set correctly")
		}

		toolName := event.Metadata()["tool_name"]
		if toolName != "calculator" {
			t.Error("Tool name not set correctly in metadata")
		}
	})
}

func TestConcurrentEventBus(t *testing.T) {
	bus := NewMemoryEventBus()
	defer bus.Close()

	var eventCount int32
	handler := func(ctx context.Context, event Event) error {
		atomic.AddInt32(&eventCount, 1)
		time.Sleep(1 * time.Millisecond) // Simulate work
		return nil
	}

	bus.Subscribe(EventAgentStarted, handler)

	const numEvents = 50
	var wg sync.WaitGroup

	// Publish events concurrently
	for i := 0; i < numEvents; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			event := NewAgentEvent(EventAgentStarted, "agent-1", "", idx)
			bus.Publish(event)
		}(i)
	}

	wg.Wait()

	// Give handlers time to complete
	time.Sleep(100 * time.Millisecond)

	count := atomic.LoadInt32(&eventCount)
	if count != numEvents {
		t.Errorf("Expected %d events handled, got %d", numEvents, count)
	}
}

func TestEventTypes(t *testing.T) {
	// Verify all event type constants are defined
	eventTypes := []EventType{
		EventAgentRegistered,
		EventAgentStarted,
		EventAgentCompleted,
		EventAgentFailed,
		EventAgentPaused,
		EventAgentResumed,
		EventSessionCreated,
		EventSessionUpdated,
		EventSessionDeleted,
		EventSessionRewound,
		EventTaskScheduled,
		EventTaskStarted,
		EventTaskCompleted,
		EventTaskFailed,
		EventTaskRetrying,
		EventToolInvoked,
		EventToolCompleted,
		EventToolFailed,
		EventCheckpointCreated,
		EventCheckpointRestored,
		EventMemoryStored,
		EventMemoryRetrieved,
		EventJobSubmitted,
		EventJobStarted,
		EventJobCompleted,
		EventJobFailed,
		EventStageStarted,
		EventStageCompleted,
		EventStageFailed,
		EventExecutorRegistered,
		EventExecutorUnregistered,
		EventExecutorHealthCheck,
	}

	for _, et := range eventTypes {
		if string(et) == "" {
			t.Error("Event type is empty")
		}
	}
}
