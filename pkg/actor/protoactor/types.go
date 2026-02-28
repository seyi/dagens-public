// Package protoactor provides an actor model implementation using the ProtoActor-Go library
// for internal agent communication within the Dagens system. This allows for efficient, 
// fault-tolerant communication between agents while maintaining A2A protocol for external interoperability.
package protoactor

import (
	"time"

	"github.com/asynkron/protoactor-go/actor"
)

// Address represents an actor address (PID in ProtoActor terms)
type Address = *actor.PID

// Message represents a message sent between actors
type Message struct {
	Type        string
	Payload     interface{}
	Sender      Address
	ReplyTo     chan Message
	Timestamp   time.Time
	SpanContext map[string]string
}

// Actor wraps ProtoActor-Go's actor interface to maintain compatibility
type Actor interface {
	Receive(ctx Context)
}

// Context provides the actor with access to the system and the current message
type Context struct {
	Context actor.Context
	Message Message
	Self    Address
}

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