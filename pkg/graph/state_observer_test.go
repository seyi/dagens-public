package graph

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMemoryState_WatchFanOut(t *testing.T) {
	s := NewMemoryState()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch1, stop1 := s.Watch(ctx)
	ch2, stop2 := s.Watch(ctx)
	defer stop1()
	defer stop2()

	s.Set("k", "v")

	wait := func(ch <-chan StateEvent) StateEvent {
		select {
		case evt := <-ch:
			return evt
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for state event")
			return StateEvent{}
		}
	}

	e1 := wait(ch1)
	e2 := wait(ch2)

	if e1.Key != "k" || e2.Key != "k" {
		t.Fatalf("unexpected event key(s): %q, %q", e1.Key, e2.Key)
	}
	if e1.Type != StateEventSet || e2.Type != StateEventSet {
		t.Fatalf("unexpected event type(s): %v, %v", e1.Type, e2.Type)
	}
}

func TestMemoryState_WatchUnsubscribeIsIdempotent(t *testing.T) {
	s := NewMemoryState()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, stop := s.Watch(ctx)
	stop()
	stop()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected observer channel to be closed after unsubscribe")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for observer channel close")
	}
}

// Run with -race to exercise cancel/stop synchronization.
func TestMemoryState_WatchContextCancelAndManualStopRace(t *testing.T) {
	s := NewMemoryState()
	ctx, cancel := context.WithCancel(context.Background())

	_, stop := s.Watch(ctx)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		stop()
	}()
	go func() {
		defer wg.Done()
		cancel()
	}()
	wg.Wait()
}

func TestMemoryState_Watch_BufferFullDropsEvents(t *testing.T) {
	s := NewMemoryState()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, stop := s.Watch(ctx)

	// Publish without draining. Watch uses a 100-slot non-blocking buffer.
	for i := 0; i < 200; i++ {
		s.Set("k", i)
	}

	// Close observer and then drain what survived buffering.
	stop()

	received := 0
	for range ch {
		received++
	}
	if received > 100 {
		t.Fatalf("received %d events, expected watch buffer/drop policy to cap at <=100", received)
	}
	if received == 0 {
		t.Fatal("expected at least one event to be delivered")
	}
}

func TestMemoryState_Watch_IndependentUnsubscribe(t *testing.T) {
	s := NewMemoryState()
	ctx := context.Background()

	ch1, stop1 := s.Watch(ctx)
	ch2, stop2 := s.Watch(ctx)
	defer stop2()

	stop1()

	select {
	case _, ok := <-ch1:
		if ok {
			t.Fatal("expected first observer channel to be closed after unsubscribe")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for first observer channel close")
	}

	s.Set("k", "v")
	select {
	case evt := <-ch2:
		if evt.Key != "k" || evt.Value != "v" {
			t.Fatalf("unexpected event on second observer: %#v", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event on second observer")
	}
}

func TestMemoryState_Close_ClosesObserverChannels(t *testing.T) {
	s := NewMemoryState()
	ctx := context.Background()

	ch1, stop1 := s.Watch(ctx)
	ch2, stop2 := s.Watch(ctx)
	defer stop1()
	defer stop2()

	if err := s.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	for i, ch := range []<-chan StateEvent{ch1, ch2} {
		select {
		case _, ok := <-ch:
			if ok {
				t.Fatalf("observer channel %d should be closed after Close()", i+1)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for observer channel %d close", i+1)
		}
	}

	if !s.IsClosed() {
		t.Fatal("expected state to report closed")
	}
}

func TestMemoryState_Watch_EventOrdering(t *testing.T) {
	s := NewMemoryState()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, stop := s.Watch(ctx)
	defer stop()

	expected := []string{"a", "b", "c", "d", "e"}
	for _, v := range expected {
		s.Set("k", v)
	}

	for i, want := range expected {
		select {
		case evt := <-ch:
			if evt.Key != "k" || evt.Value != want {
				t.Fatalf("event %d mismatch: got key=%q value=%v want key=%q value=%q", i, evt.Key, evt.Value, "k", want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for event %d", i)
		}
	}
}
