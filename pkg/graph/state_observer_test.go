package graph

import (
	"context"
	"testing"
	"time"
)

func TestStateObserverFanOut(t *testing.T) {
	s := &MemoryState{}
	obs1 := s.Watch()
	if obs1 == nil {
		t.Fatal("expected observer 1, got nil")
	}
	obs2 := s.Watch()
	if obs2 == nil {
		t.Fatal("expected observer 2, got nil")
	}
	defer obs1.Stop()
	defer obs2.Stop()

	evt := StateEvent{
		Type:    StateEventSet,
		Key:     "k",
		Value:   "v",
		Version: 1,
		Time:    time.Now(),
	}
	s.publishEvent(evt)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	recv := func(ch <-chan StateEvent) StateEvent {
		select {
		case e := <-ch:
			return e
		case <-ctx.Done():
			t.Fatalf("timeout waiting for event")
		}
		return StateEvent{}
	}

	e1 := recv(obs1.Events)
	e2 := recv(obs2.Events)

	if e1.Key != "k" || e2.Key != "k" {
		t.Fatalf("unexpected keys: e1=%q e2=%q", e1.Key, e2.Key)
	}
	if e1.Type != StateEventSet || e2.Type != StateEventSet {
		t.Fatalf("unexpected event types: e1=%v e2=%v", e1.Type, e2.Type)
	}
}

func TestStateObserverDropOnSlowConsumer(t *testing.T) {
	h := newStateObserverHub()

	defer func() {
		h.mu.Lock()
		if !h.closed {
			h.closed = true
			close(h.in)
		}
		h.mu.Unlock()
	}()

	fast := h.addWatcher()
	if fast == nil {
		t.Fatal("expected fast watcher")
	}
	slow := h.addWatcher()
	if slow == nil {
		t.Fatal("expected slow watcher")
	}
	defer fast.Stop()
	defer slow.Stop()

	const total = 2000
	for i := 0; i < total; i++ {
		h.enqueue(StateEvent{
			Type:    StateEventSet,
			Key:     "k",
			Value:   i,
			Version: int64(i),
			Time:    time.Now(),
		})
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sentinel := StateEvent{
		Type:    StateEventSet,
		Key:     "sentinel",
		Version: -1,
		Time:    time.Now(),
	}
	h.enqueue(sentinel)

	// Drain fast observer until we see the sentinel
	for {
		select {
		case e, ok := <-fast.Events:
			if !ok {
				t.Fatalf("fast observer channel closed unexpectedly")
			}
			if e.Key == "sentinel" && e.Version == -1 {
				goto Done
			}
		case <-waitCtx.Done():
			t.Fatalf("timeout waiting for sentinel, possible deadlock")
		}
	}
Done:

	dropped := h.Dropped()
	if dropped == 0 {
		t.Fatalf("expected some dropped events due to slow consumer, got %d", dropped)
	}

	h.mu.Lock()
	if !h.closed {
		h.closed = true
		close(h.in)
	}
	h.mu.Unlock()
}

func TestStateObserverLifecycleWatchStopUnwatch(t *testing.T) {
	s := &MemoryState{}
	obs := s.Watch()
	if obs == nil {
		t.Fatal("expected observer")
	}

	s.Unwatch(obs)

	select {
	case _, ok := <-obs.Events:
		if ok {
			t.Fatal("expected observer channel to be closed after Unwatch")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for observer channel close after Unwatch")
	}

	obs2 := s.Watch()
	if obs2 == nil {
		t.Fatal("expected second observer")
	}

	obs2.Stop()

	select {
	case _, ok := <-obs2.Events:
		if ok {
			t.Fatal("expected observer channel to be closed after Stop")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for observer channel close after Stop")
	}

	obs2.Stop()
	s.Unwatch(obs2)

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.publishEvent(StateEvent{
			Type:    StateEventSet,
			Key:     "k",
			Value:   "v",
			Version: 1,
			Time:    time.Now(),
		})
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timeout publishing event after all watchers removed")
	}
}
