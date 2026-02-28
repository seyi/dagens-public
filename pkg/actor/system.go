// Package actor provides an actor model implementation for internal agent communication
// within the Dagens system. This allows for efficient, fault-tolerant communication
// between agents while maintaining A2A protocol for external interoperability.
package actor

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// System manages all actors within the system
type System struct {
	mu     sync.RWMutex
	actors map[Address]*Process
	ctx    context.Context
	cancel context.CancelFunc
}

// NewSystem creates a new actor system
func NewSystem() *System {
	ctx, cancel := context.WithCancel(context.Background())
	
	return &System{
		actors: make(map[Address]*Process),
		ctx:    ctx,
		cancel: cancel,
	}
}

// Spawn creates and starts a new actor
func (s *System) Spawn(actor Actor, id string) (Address, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	address := Address(id)

	// Check if actor already exists
	if _, exists := s.actors[address]; exists {
		return "", fmt.Errorf("actor with address %s already exists", address)
	}

	// Create new process
	process := NewProcess(actor, address, s)

	// Store in registry
	s.actors[address] = process

	// Start the process
	process.Start()

	return address, nil
}

// Send sends a message to an actor asynchronously
func (s *System) Send(to Address, msg Message) error {
	s.mu.RLock()
	process, exists := s.actors[to]
	s.mu.RUnlock()

	if !exists {
		return fmt.Errorf("actor with address %s not found", to)
	}

	if !process.Send(msg) {
		return fmt.Errorf("actor with address %s is stopped", to)
	}
	return nil
}

// Request sends a message and waits for a response synchronously
func (s *System) Request(to Address, msg Message, timeout time.Duration) (Message, error) {
	replyChan := make(chan Message, 1)
	
	// Set up the reply channel in the message
	msg.ReplyTo = replyChan
	
	// Send the message
	if err := s.Send(to, msg); err != nil {
		return Message{}, err
	}
	
	// Wait for response with timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	
	select {
	case response := <-replyChan:
		return response, nil
	case <-ctx.Done():
		return Message{}, fmt.Errorf("request timeout for actor %s", to)
	}
}

// Stop stops the actor system and all actors
func (s *System) Stop() {
	s.cancel()

	s.mu.Lock()
	// Copy processes to avoid holding lock during Stop
	processes := make([]*Process, 0, len(s.actors))
	for _, process := range s.actors {
		processes = append(processes, process)
	}
	// Clear the actors map
	s.actors = make(map[Address]*Process)
	s.mu.Unlock()

	// Stop all actors (each Process.Stop waits for its goroutine)
	for _, process := range processes {
		process.Stop()
	}
}

// handlePanic handles actor panics for fault tolerance
func (s *System) handlePanic(actorAddr Address, panic interface{}) {
	// In a real implementation, this would notify a supervisor
	// For now, just log the panic
	fmt.Printf("Actor %s panicked: %v\n", actorAddr, panic)
	
	// Potentially restart the actor or escalate to supervisor
	s.restartActor(actorAddr)
}

// restartActor attempts to restart a crashed actor
func (s *System) restartActor(actorAddr Address) {
	// This is a simplified restart - in a real implementation,
	// you'd need to preserve the actor's original configuration
	// and recreate it with the same behavior
	fmt.Printf("Attempting to restart actor: %s\n", actorAddr)
	
	// For now, just log - a full implementation would require
	// storing actor factory functions or configurations
}

// GetActor returns an actor process by address
func (s *System) GetActor(address Address) (*Process, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	process, exists := s.actors[address]
	return process, exists
}

// ActorCount returns the number of active actors
func (s *System) ActorCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	return len(s.actors)
}

// Addresses returns all active actor addresses
func (s *System) Addresses() []Address {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	addresses := make([]Address, 0, len(s.actors))
	for addr := range s.actors {
		addresses = append(addresses, addr)
	}
	
	return addresses
}