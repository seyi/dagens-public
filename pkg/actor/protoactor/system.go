// Package protoactor provides an actor model implementation using the ProtoActor-Go library
package protoactor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/asynkron/protoactor-go/actor"
	"go.opentelemetry.io/otel"
)

// System manages all actors within the ProtoActor-Go system
type System struct {
	actorSystem    *actor.ActorSystem
	rootContext    *actor.RootContext
	mu             sync.RWMutex
	actors         map[string]Address // Registry of spawned actors
	shutdownTracer func()
}

// NewSystem creates a new ProtoActor-Go system
func NewSystem() *System {
	system := actor.NewActorSystem()
	shutdown := initTracer()

	return &System{
		actorSystem:    system,
		rootContext:    system.Root,
		actors:         make(map[string]Address),
		shutdownTracer: shutdown,
	}
}

// Spawn creates and starts a new actor using ProtoActor-Go
func (s *System) Spawn(actorImpl Actor, id string) (Address, error) {
	actorWrapper := NewActorWrapper(actorImpl)
	props := actor.PropsFromProducer(func() actor.Actor {
		return actorWrapper
	})

	pid, err := s.rootContext.SpawnNamed(props, id)
	if err != nil {
		return nil, fmt.Errorf("failed to spawn actor %s: %w", id, err)
	}

	// Register the actor
	s.mu.Lock()
	s.actors[id] = pid
	s.mu.Unlock()

	actorsSpawnedTotal.Inc()

	return pid, nil
}

// SpawnWithSupervisor creates and starts a new actor with supervision
func (s *System) SpawnWithSupervisor(actorImpl Actor, id string, strategy actor.SupervisorStrategy) (Address, error) {
	actorWrapper := NewActorWrapper(actorImpl)
	props := actor.PropsFromProducer(func() actor.Actor {
		return actorWrapper
	}, actor.WithSupervisor(strategy))

	pid, err := s.rootContext.SpawnNamed(props, id)
	if err != nil {
		return nil, fmt.Errorf("failed to spawn supervised actor %s: %w", id, err)
	}

	// Register the actor
	s.mu.Lock()
	s.actors[id] = pid
	s.mu.Unlock()

	actorsSpawnedTotal.Inc()

	return pid, nil
}

// Send sends a message to an actor asynchronously
func (s *System) Send(to Address, msg Message) error {
	tracer := getTracer()
	ctx := context.Background()
	ctx, span := tracer.Start(ctx, "actor.send")
	defer span.End()

	if msg.SpanContext == nil {
		msg.SpanContext = make(map[string]string)
	}
	carrier := NewTextMapCarrier(msg.SpanContext)
	otel.GetTextMapPropagator().Inject(ctx, carrier)

	s.rootContext.Send(to, &msg)
	messagesSentTotal.Inc()
	return nil
}

// SendRaw sends a raw message to an actor asynchronously
func (s *System) SendRaw(to Address, msg interface{}) error {
	s.rootContext.Send(to, msg)
	messagesSentTotal.Inc()
	return nil
}

// Request sends a message and waits for a response synchronously
func (s *System) Request(to Address, msg Message, timeout time.Duration) (Message, error) {
	future := s.rootContext.RequestFuture(to, &msg, timeout)
	
	result, err := future.Result()
	if err != nil {
		return Message{}, fmt.Errorf("request failed: %w", err)
	}
	
	// Convert the result back to our Message type
	if message, ok := result.(*Message); ok {
		return *message, nil
	}
	
	// If it's not our Message type, wrap it in a generic message
	return Message{
		Type:      "response",
		Payload:   result,
		Timestamp: time.Now(),
	}, nil
}

// RequestRaw sends a raw message and waits for a response
func (s *System) RequestRaw(to Address, msg interface{}, timeout time.Duration) (interface{}, error) {
	future := s.rootContext.RequestFuture(to, msg, timeout)
	
	result, err := future.Result()
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	
	return result, nil
}

// Stop stops the actor system and all actors
func (s *System) Stop() {
	// Stop all registered actors first
	s.mu.Lock()
	for id, pid := range s.actors {
		s.rootContext.Stop(pid)
		delete(s.actors, id)
	}
	s.mu.Unlock()

	// Shutdown the actor system
	s.actorSystem.Shutdown()

	// Shutdown the tracer provider
	if s.shutdownTracer != nil {
		s.shutdownTracer()
	}
}

// StopActor stops a specific actor and removes it from the registry
func (s *System) StopActor(pid Address) {
	s.rootContext.Stop(pid)
	actorsStoppedTotal.Inc()

	// Remove from registry
	s.mu.Lock()
	for id, registeredPid := range s.actors {
		if registeredPid.Id == pid.Id {
			delete(s.actors, id)
			break
		}
	}
	s.mu.Unlock()
}

// PoisonPill sends a poison pill message to stop an actor gracefully
func (s *System) PoisonPill(pid Address) {
	s.rootContext.Send(pid, &actor.PoisonPill{})
}

// GetActor checks if an actor exists in the registry
func (s *System) GetActor(address Address) bool {
	if address == nil {
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, pid := range s.actors {
		if pid.Id == address.Id {
			return true
		}
	}
	return false
}

// GetActorByID returns an actor's PID by its ID
func (s *System) GetActorByID(id string) (Address, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pid, exists := s.actors[id]
	return pid, exists
}

// Addresses returns all active actor addresses from the registry
func (s *System) Addresses() []Address {
	s.mu.RLock()
	defer s.mu.RUnlock()

	addresses := make([]Address, 0, len(s.actors))
	for _, pid := range s.actors {
		addresses = append(addresses, pid)
	}
	return addresses
}

// ActorCount returns the number of registered actors
func (s *System) ActorCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.actors)
}
