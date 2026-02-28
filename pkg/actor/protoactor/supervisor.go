// Package protoactor provides an actor model implementation using the ProtoActor-Go library
package protoactor

import (
	"fmt"
	"sync"
	"time"

	"github.com/asynkron/protoactor-go/actor"
)

// SupervisorStrategy defines how a supervisor handles child failures
type SupervisorStrategy string

const (
	// OneForOne restarts only the failed child
	OneForOne SupervisorStrategy = "one_for_one"
	
	// OneForAll restarts all children when one fails
	OneForAll SupervisorStrategy = "one_for_all"
	
	// RestForOne restarts the failed child and all children started after it
	RestForOne SupervisorStrategy = "rest_for_one"
)

// Supervisor manages a group of child actors using ProtoActor-Go's supervision
type Supervisor struct {
	mu       sync.RWMutex
	system   *System
	children map[Address]Actor
	strategy actor.SupervisorStrategy
	pid      Address
}

// ActorConfig holds configuration for creating an actor
type ActorConfig struct {
	ID       string
	Actor    Actor
	Strategy SupervisorStrategy
}

// NewSupervisor creates a new supervisor using ProtoActor-Go's supervision
func NewSupervisor(strategy SupervisorStrategy) *Supervisor {
	var protoStrategy actor.SupervisorStrategy

	switch strategy {
	case OneForOne:
		protoStrategy = actor.NewOneForOneStrategy(3, 5*time.Second, actor.DefaultDecider) // maxRestarts: 3, within: 5s
	case OneForAll:
		protoStrategy = actor.NewAllForOneStrategy(3, 5*time.Second, actor.DefaultDecider)
	// Note: ProtoActor-Go doesn't have a direct RestForOne strategy, using OneForOne as fallback
	default:
		protoStrategy = actor.NewOneForOneStrategy(3, 5*time.Second, actor.DefaultDecider)
	}

	return &Supervisor{
		children: make(map[Address]Actor),
		strategy: protoStrategy,
	}
}

// Receive implements the Actor interface for the supervisor
func (s *Supervisor) Receive(ctx Context) {
	switch ctx.Message.Type {
	case "supervise":
		if msg, ok := ctx.Message.Payload.(SuperviseMessage); ok {
			s.handleSuperviseMessage(ctx, msg)
		}
	case "failure":
		s.handleFailureMessage(ctx)
	default:
		// Handle other message types as needed
	}
}

// handleSuperviseMessage handles requests to supervise a child
func (s *Supervisor) handleSuperviseMessage(ctx Context, msg SuperviseMessage) {
	// In ProtoActor-Go, supervision is typically handled through Props
	// This is more of a conceptual supervisor that tracks children
	if ctx.Message.ReplyTo != nil {
		response := Message{
			Type:    "response",
			Payload: "child supervised",
		}
		// Non-blocking send to avoid deadlock
		select {
		case ctx.Message.ReplyTo <- response:
		default:
			// Channel full or closed, skip reply
		}
	}
}

// handleFailureMessage handles child failure notifications
func (s *Supervisor) handleFailureMessage(ctx Context) {
	// In ProtoActor-Go, failures are typically handled by the supervisor strategy
	// This is just a placeholder for custom failure handling if needed
}

// AddChild adds a child to be supervised
func (s *Supervisor) AddChild(system *System, config ActorConfig) (Address, error) {
	actorWrapper := NewActorWrapper(config.Actor)

	var protoStrategy actor.SupervisorStrategy
	switch config.Strategy {
	case OneForOne:
		protoStrategy = actor.NewOneForOneStrategy(3, 5*time.Second, actor.DefaultDecider)
	case OneForAll:
		protoStrategy = actor.NewAllForOneStrategy(3, 5*time.Second, actor.DefaultDecider)
	// Note: ProtoActor-Go doesn't have a direct RestForOne strategy, using OneForOne as fallback
	default:
		protoStrategy = actor.NewOneForOneStrategy(3, 5*time.Second, actor.DefaultDecider)
	}

	props := actor.PropsFromProducer(func() actor.Actor {
		return actorWrapper
	}, actor.WithSupervisor(protoStrategy))

	pid, err := system.rootContext.SpawnNamed(props, config.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to spawn supervised child %s: %w", config.ID, err)
	}

	s.mu.Lock()
	s.children[pid] = config.Actor
	s.mu.Unlock()

	return pid, nil
}

// RemoveChild removes a child from supervision
func (s *Supervisor) RemoveChild(pid Address) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.children, pid)
}

// GetChild returns a supervised child
func (s *Supervisor) GetChild(pid Address) (Actor, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	actor, exists := s.children[pid]
	return actor, exists
}

// ChildCount returns the number of supervised children
func (s *Supervisor) ChildCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.children)
}

// GetProtoStrategy returns the underlying ProtoActor-Go supervisor strategy
func (s *Supervisor) GetProtoStrategy() actor.SupervisorStrategy {
	return s.strategy
}