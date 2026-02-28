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

// Address uniquely identifies an actor within the system
type Address string

// Message represents a message sent between actors
type Message struct {
	Type        string
	Payload     interface{}
	Sender      Address
	ReplyTo     chan Message
	Timestamp   time.Time
	SpanContext map[string]string
}

// Actor defines the behavior of an actor
type Actor interface {
	Receive(ctx Context)
}

// Context provides the actor with access to the system and the current message
type Context struct {
	System  *System
	Message Message
	Self    Address
}

// Process represents a running actor instance
type Process struct {
	actor   Actor
	mailbox chan Message
	address Address
	system  *System
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.RWMutex // Protects stopped flag
	stopped bool         // Indicates if the process has been stopped
}

// NewProcess creates a new actor process
func NewProcess(actor Actor, address Address, system *System) *Process {
	ctx, cancel := context.WithCancel(context.Background())
	
	return &Process{
		actor:   actor,
		mailbox: make(chan Message, 100), // Buffered mailbox
		address: address,
		system:  system,
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Start starts the actor's execution loop
func (p *Process) Start() {
	p.wg.Add(1)
	go p.run()
}

// Stop stops the actor safely
func (p *Process) Stop() {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return // Already stopped
	}
	p.stopped = true
	p.mu.Unlock()

	p.cancel()
	close(p.mailbox)
	p.wg.Wait()
}

// IsStopped returns true if the process has been stopped
func (p *Process) IsStopped() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.stopped
}

// run is the main execution loop for the actor
func (p *Process) run() {
	defer p.wg.Done()
	
	for {
		select {
		case msg, ok := <-p.mailbox:
			if !ok {
				return // Channel closed
			}
			
			// Create context for this message
			ctx := Context{
				System:  p.system,
				Message: msg,
				Self:    p.address,
			}
			
			// Handle panic recovery for fault tolerance
			func() {
				defer func() {
					if r := recover(); r != nil {
						// Log the panic and potentially notify supervisor
						// Check if system is nil to prevent double panic
						if p.system != nil {
							p.system.handlePanic(p.address, r)
						} else {
							// Fallback logging when system is nil
							fmt.Printf("Actor %s panicked (no system): %v\n", p.address, r)
						}
					}
				}()

				p.actor.Receive(ctx)
			}()
			
		case <-p.ctx.Done():
			return
		}
	}
}

// Send sends a message to the actor's mailbox
// Returns true if message was sent, false if actor is stopped
func (p *Process) Send(msg Message) bool {
	// Check if stopped first to avoid sending to closed channel
	p.mu.RLock()
	if p.stopped {
		p.mu.RUnlock()
		return false
	}
	p.mu.RUnlock()

	select {
	case p.mailbox <- msg:
		return true
	case <-p.ctx.Done():
		return false
	}
}