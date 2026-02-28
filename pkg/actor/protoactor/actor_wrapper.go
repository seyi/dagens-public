// Package protoactor provides an actor model implementation using the ProtoActor-Go library
package protoactor

import (
	"context"
	"time"

	"github.com/asynkron/protoactor-go/actor"
	"go.opentelemetry.io/otel"
)

// ActorWrapper wraps our Actor interface to work with ProtoActor-Go
type ActorWrapper struct {
	Actor Actor
}

// NewActorWrapper creates a new actor wrapper
func NewActorWrapper(actor Actor) *ActorWrapper {
	return &ActorWrapper{
		Actor: actor,
	}
}

// Receive implements the ProtoActor-Go Actor interface
func (aw *ActorWrapper) Receive(ctx actor.Context) {
	messagesReceivedTotal.Inc()
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		messageProcessingDurationSeconds.Observe(duration.Seconds())
	}()

	// Extract span context from message
	var spanCtx context.Context
	if msg, ok := ctx.Message().(*Message); ok && msg.SpanContext != nil {
		carrier := NewTextMapCarrier(msg.SpanContext)
		spanCtx = otel.GetTextMapPropagator().Extract(context.Background(), carrier)
	} else {
		spanCtx = context.Background()
	}

	tracer := getTracer()
	spanCtx, span := tracer.Start(spanCtx, "actor.receive")
	defer span.End()

	switch msg := ctx.Message().(type) {
	case *Message:
		// Convert ProtoActor context to our context
		ourCtx := Context{
			Context: ctx,
			Message: *msg,
			Self:    ctx.Self(),
		}

		// Call the wrapped actor's Receive method
		aw.Actor.Receive(ourCtx)

	case *InvokeMessage:
		// Convert to our message format
		ourMsg := Message{
			Type:      "invoke",
			Payload:   msg, // Keep pointer for type assertions in actors
			Sender:    ctx.Sender(),
			Timestamp: time.Now(),
		}

		ourCtx := Context{
			Context: ctx,
			Message: ourMsg,
			Self:    ctx.Self(),
		}

		aw.Actor.Receive(ourCtx)

	case *PingMessage:
		// Convert to our message format
		ourMsg := Message{
			Type:      "ping",
			Payload:   msg, // Keep pointer for type assertions in actors
			Sender:    ctx.Sender(),
			Timestamp: time.Now(),
		}

		ourCtx := Context{
			Context: ctx,
			Message: ourMsg,
			Self:    ctx.Self(),
		}

		aw.Actor.Receive(ourCtx)

	default:
		// For other message types, we'll pass them through with a generic message
		ourMsg := Message{
			Type:      "generic",
			Payload:   msg,
			Sender:    ctx.Sender(),
			Timestamp: time.Now(),
		}

		ourCtx := Context{
			Context: ctx,
			Message: ourMsg,
			Self:    ctx.Self(),
		}

		aw.Actor.Receive(ourCtx)
	}
}