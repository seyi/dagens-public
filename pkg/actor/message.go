// Package actor provides an actor model implementation for internal agent communication
// within the Dagens system. This allows for efficient, fault-tolerant communication
// between agents while maintaining A2A protocol for external interoperability.
package actor

import (
	"time"
)

// Common message types for the actor system
const (
	MessageTypeInvoke     = "invoke"
	MessageTypeResponse   = "response"
	MessageTypeError      = "error"
	MessageTypePing       = "ping"
	MessageTypePong       = "pong"
	MessageTypeStart      = "start"
	MessageTypeStop       = "stop"
	MessageTypeSupervise  = "supervise"
	MessageTypeFailure    = "failure"
	MessageTypeRestart    = "restart"
)

// InvokeMessage represents an agent invocation request
type InvokeMessage struct {
	AgentID     string
	Instruction string
	Context     map[string]interface{}
	Timeout     time.Duration
	TaskID      string
}

// ResponseMessage represents an agent invocation response
type ResponseMessage struct {
	TaskID string
	Result interface{}
	Error  error
}

// ErrorMessage represents an error response
type ErrorMessage struct {
	Error   string
	Details interface{}
}

// PingMessage is used for health checks
type PingMessage struct {
	ID string
}

// PongMessage is the response to a ping
type PongMessage struct {
	ID        string
	Timestamp time.Time
}

// SuperviseMessage is used by supervisors
type SuperviseMessage struct {
	ChildAddress Address
	Action       string // "start", "stop", "restart"
}