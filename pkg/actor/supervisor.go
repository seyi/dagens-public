// Package actor provides an actor model implementation for internal agent communication
// within the Dagens system. This allows for efficient, fault-tolerant communication
// between agents while maintaining A2A protocol for external interoperability.
package actor

import (
	"fmt"
	"sync"
	"time"
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
	
	// SimpleOneForOne is used for dynamically created children
	SimpleOneForOne SupervisorStrategy = "simple_one_for_one"
)

// Supervisor manages a group of child actors
type Supervisor struct {
	children      map[Address]Actor
	childConfigs  map[Address]ActorConfig
	childProcs    map[Address]*Process // Track spawned child processes
	strategy      SupervisorStrategy
	restartLimit  int
	restartCount  int
	restartWindow time.Duration
	lastRestart   time.Time
	mu            sync.RWMutex
}

// ActorConfig holds configuration for creating an actor
type ActorConfig struct {
	ID       string
	Actor    Actor
	MaxRestarts int
	Timeout  time.Duration
}

// NewSupervisor creates a new supervisor actor
// Use System.Spawn() to spawn this supervisor in the actor system
func NewSupervisor(strategy SupervisorStrategy, maxRestarts int, window time.Duration) *Supervisor {
	return &Supervisor{
		children:      make(map[Address]Actor),
		childConfigs:  make(map[Address]ActorConfig),
		childProcs:    make(map[Address]*Process),
		strategy:      strategy,
		restartLimit:  maxRestarts,
		restartWindow: window,
		lastRestart:   time.Now(),
	}
}

// Receive handles messages for the supervisor
func (s *Supervisor) Receive(ctx Context) {
	switch ctx.Message.Type {
	case MessageTypeSupervise:
		s.handleSuperviseMessage(ctx)
	case MessageTypeFailure:
		s.handleFailureMessage(ctx)
	case MessageTypeStart:
		s.handleStartMessage(ctx)
	case MessageTypeStop:
		s.handleStopMessage(ctx)
	case MessageTypeRestart:
		s.handleRestartMessage(ctx)
	default:
		// Unknown message type
		if ctx.Message.ReplyTo != nil {
			errorMsg := Message{
				Type: MessageTypeError,
				Payload: ErrorMessage{
					Error:   "unknown message type",
					Details: ctx.Message.Type,
				},
			}
			select {
			case ctx.Message.ReplyTo <- errorMsg:
			default:
			}
		}
	}
}

// handleSuperviseMessage handles requests to supervise a child
func (s *Supervisor) handleSuperviseMessage(ctx Context) {
	if msg, ok := ctx.Message.Payload.(SuperviseMessage); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		
		// Add child to supervision
		if config, exists := s.childConfigs[msg.ChildAddress]; exists {
			s.children[msg.ChildAddress] = config.Actor
		}
	}
}

// handleFailureMessage handles child failure notifications
func (s *Supervisor) handleFailureMessage(ctx Context) {
	s.mu.Lock()

	// Check restart limits
	now := time.Now()
	if now.Sub(s.lastRestart) <= s.restartWindow {
		s.restartCount++
		if s.restartCount > s.restartLimit {
			// Too many restarts in the window, escalate or stop
			fmt.Printf("Supervisor: Too many restarts (%d) in window, stopping\n", s.restartCount)
			s.mu.Unlock()
			return
		}
	} else {
		// Reset counter if outside window
		s.restartCount = 1
		s.lastRestart = now
	}
	s.mu.Unlock()

	// Restart the failed child based on strategy
	if failedAddr, ok := ctx.Message.Payload.(Address); ok {
		s.restartChild(ctx.System, failedAddr)
	}
}

// handleStartMessage handles requests to start a child
func (s *Supervisor) handleStartMessage(ctx Context) {
	if config, ok := ctx.Message.Payload.(ActorConfig); ok {
		addr, err := s.SpawnChild(ctx.System, config)
		if err != nil {
			if ctx.Message.ReplyTo != nil {
				errorMsg := Message{
					Type: MessageTypeError,
					Payload: ErrorMessage{
						Error:   err.Error(),
						Details: config.ID,
					},
				}
				select {
				case ctx.Message.ReplyTo <- errorMsg:
				default:
					// ReplyTo channel full or closed, skip
				}
			}
			return
		}

		if ctx.Message.ReplyTo != nil {
			responseMsg := Message{
				Type:    MessageTypeResponse,
				Payload: addr,
			}
			select {
			case ctx.Message.ReplyTo <- responseMsg:
			default:
				// ReplyTo channel full or closed, skip
			}
		}
	}
}

// handleStopMessage handles requests to stop a child
func (s *Supervisor) handleStopMessage(ctx Context) {
	if childAddr, ok := ctx.Message.Payload.(Address); ok {
		s.StopChild(childAddr)

		if ctx.Message.ReplyTo != nil {
			responseMsg := Message{
				Type:    MessageTypeResponse,
				Payload: "stopped",
			}
			select {
			case ctx.Message.ReplyTo <- responseMsg:
			default:
			}
		}
	}
}

// handleRestartMessage handles requests to restart a child
func (s *Supervisor) handleRestartMessage(ctx Context) {
	if childAddr, ok := ctx.Message.Payload.(Address); ok {
		s.restartChild(ctx.System, childAddr)

		if ctx.Message.ReplyTo != nil {
			responseMsg := Message{
				Type:    MessageTypeResponse,
				Payload: "restarted",
			}
			select {
			case ctx.Message.ReplyTo <- responseMsg:
			default:
			}
		}
	}
}

// restartChild restarts a specific child based on the strategy
func (s *Supervisor) restartChild(system *System, childAddr Address) {
	s.mu.Lock()
	config, exists := s.childConfigs[childAddr]
	oldProc := s.childProcs[childAddr]
	s.mu.Unlock()

	if !exists {
		fmt.Printf("Supervisor: No config found for child %s, cannot restart\n", childAddr)
		return
	}

	// Stop the old process if it exists
	if oldProc != nil {
		oldProc.Stop()
	}

	// Spawn a new process through the system
	if system != nil {
		// Remove old actor from system first (if any)
		system.mu.Lock()
		delete(system.actors, childAddr)
		system.mu.Unlock()

		// Spawn new actor
		addr, err := system.Spawn(config.Actor, config.ID)
		if err != nil {
			fmt.Printf("Supervisor: Failed to restart child %s: %v\n", childAddr, err)
			return
		}

		// Update our tracking
		s.mu.Lock()
		if proc, ok := system.GetActor(addr); ok {
			s.childProcs[addr] = proc
		}
		s.mu.Unlock()

		fmt.Printf("Supervisor: Restarted child %s\n", childAddr)
	} else {
		fmt.Printf("Supervisor: Cannot restart child %s - no system available\n", childAddr)
	}
}

// SpawnChild spawns a child actor and adds it to supervision
func (s *Supervisor) SpawnChild(system *System, config ActorConfig) (Address, error) {
	if system == nil {
		return "", fmt.Errorf("cannot spawn child: system is nil")
	}

	// Spawn the actor through the system
	addr, err := system.Spawn(config.Actor, config.ID)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Store the configuration for potential restarts
	s.childConfigs[addr] = config
	s.children[addr] = config.Actor

	// Track the spawned process
	if proc, ok := system.GetActor(addr); ok {
		s.childProcs[addr] = proc
	}

	return addr, nil
}

// StopChild stops and removes a child from supervision
func (s *Supervisor) StopChild(addr Address) {
	s.mu.Lock()
	proc := s.childProcs[addr]
	delete(s.children, addr)
	delete(s.childConfigs, addr)
	delete(s.childProcs, addr)
	s.mu.Unlock()

	if proc != nil {
		proc.Stop()
	}
}

// AddChild adds a child config without spawning (for pre-registration)
// Use SpawnChild to actually spawn and supervise a child actor
func (s *Supervisor) AddChild(config ActorConfig) (Address, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	address := Address(config.ID)

	// Store the configuration for potential restarts
	s.childConfigs[address] = config

	// Add to children map
	s.children[address] = config.Actor

	return address, nil
}

// RemoveChild removes a child from supervision (does not stop the process)
// Use StopChild to stop and remove a child
func (s *Supervisor) RemoveChild(address Address) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.children, address)
	delete(s.childConfigs, address)
	delete(s.childProcs, address)
}

// GetChild returns a supervised child
func (s *Supervisor) GetChild(address Address) (Actor, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	actor, exists := s.children[address]
	return actor, exists
}

// ChildCount returns the number of supervised children
func (s *Supervisor) ChildCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	return len(s.children)
}