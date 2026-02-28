package graph

import (
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const maxStateWatchers = 10000

// stateObserverHub fans out events to watchers without blocking producers.
type stateObserverHub struct {
	in       chan StateEvent
	watchers map[string]chan StateEvent
	mu       sync.RWMutex
	closed   bool
	dropped  uint64 // accessed via atomic
	seq      int64
}

func newStateObserverHub() *stateObserverHub {
	h := &stateObserverHub{
		in:       make(chan StateEvent, 1024),
		watchers: make(map[string]chan StateEvent),
	}
	go h.run()
	return h
}

// enqueue tries to publish an event without blocking callers.
func (h *stateObserverHub) enqueue(evt StateEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.closed {
		return
	}
	select {
	case h.in <- evt:
	default:
		atomic.AddUint64(&h.dropped, 1)
	}
}

func (h *stateObserverHub) addWatcher() *StateObserver {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	if len(h.watchers) >= maxStateWatchers {
		// Guard against unbounded growth if callers leak watchers.
		// Note: not incrementing 'dropped' counter as that's for events, not watcher rejections
		return nil
	}
	id := h.nextID()
	ch := make(chan StateEvent, 256)
	h.watchers[id] = ch
	return &StateObserver{
		Events: ch,
		id:     id,
		stop: func() {
			h.removeWatcherByID(id)
		},
	}
}

func (h *stateObserverHub) removeWatcher(obs *StateObserver) {
	if obs == nil {
		return
	}
	h.removeWatcherByID(obs.id)
}

func (h *stateObserverHub) removeWatcherByID(id string) {
	h.mu.Lock()
	ch, ok := h.watchers[id]
	if ok {
		delete(h.watchers, id)
	}
	h.mu.Unlock()
	if ok {
		close(ch)
	}
}

func (h *stateObserverHub) run() {
	for evt := range h.in {
		h.mu.RLock()
		for _, ch := range h.watchers {
			select {
			case ch <- evt:
			default:
				atomic.AddUint64(&h.dropped, 1)
			}
		}
		h.mu.RUnlock()
	}
}

func (h *stateObserverHub) nextID() string {
	seq := atomic.AddInt64(&h.seq, 1)
	return "obs-" + time.Now().Format("20060102150405.000000000") + "-" + int64ToString(seq)
}

// Dropped returns the number of events that have been dropped due to backpressure.
func (h *stateObserverHub) Dropped() uint64 {
	return atomic.LoadUint64(&h.dropped)
}

// Stop cleanly shuts down the hub, closing all watchers and terminating the run loop.
func (h *stateObserverHub) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.closed {
		h.closed = true
		close(h.in)
		// Close all watcher channels
		for id, ch := range h.watchers {
			close(ch)
			delete(h.watchers, id)
		}
	}
}

func int64ToString(v int64) string {
	return strconv.FormatInt(v, 10)
}
