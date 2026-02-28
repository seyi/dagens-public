# Actor System for Dagens

This package implements an actor model for internal agent communication within the Dagens system. It provides a hybrid approach that maintains the A2A protocol for external interoperability while using efficient actor-based communication for internal operations.

## Overview

The actor system provides:

- **Lightweight concurrency**: Each agent runs as a goroutine with its own mailbox
- **Message passing**: Asynchronous communication via channels
- **Fault tolerance**: Supervision patterns for automatic recovery
- **Location transparency**: Same interface for local and remote communication
- **Integration**: Adapters for the existing A2A protocol

## Architecture

### Core Components

1. **Actor**: Defines the behavior interface with a `Receive` method
2. **Process**: Represents a running actor instance with its own mailbox
3. **System**: Manages all actors, provides spawning and message routing
4. **Message**: Universal message envelope for actor communication
5. **Supervisor**: Provides fault tolerance through supervision strategies

### Message Types

Common message types include:
- `MessageTypeInvoke`: Agent invocation requests
- `MessageTypeResponse`: Successful responses
- `MessageTypeError`: Error responses
- `MessageTypePing/Pong`: Health checks
- `MessageTypeSupervise`: Supervision commands
- `MessageTypeFailure/Restart`: Fault tolerance messages

## Usage

### Creating an Actor

```go
type MyActor struct {
    // Actor state
}

func (a *MyActor) Receive(ctx actor.Context) {
    switch ctx.Message.Type {
    case actor.MessageTypeInvoke:
        // Handle invocation
        if msg, ok := ctx.Message.Payload.(actor.InvokeMessage); ok {
            // Process the message
            result := processMessage(msg)
            
            // Send response if needed
            if ctx.Message.ReplyTo != nil {
                response := actor.Message{
                    Type: actor.MessageTypeResponse,
                    Payload: actor.ResponseMessage{
                        TaskID: msg.TaskID,
                        Result: result,
                    },
                }
                ctx.Message.ReplyTo <- response
            }
        }
    }
}
```

### Using the Actor System

```go
// Create a system
system := actor.NewSystem()
defer system.Stop()

// Create and spawn an actor
myActor := &MyActor{}
address, err := system.Spawn(myActor, "my-actor-1")
if err != nil {
    log.Fatal(err)
}

// Send a message
msg := actor.Message{
    Type: actor.MessageTypeInvoke,
    Payload: actor.InvokeMessage{
        Instruction: "Hello",
        TaskID: "task-1",
    },
}

response, err := system.Request(address, msg, 5*time.Second)
if err != nil {
    log.Printf("Error: %v", err)
} else {
    log.Printf("Response: %v", response.Payload)
}
```

## Integration with A2A Protocol

The actor system integrates with the existing A2A protocol through adapters:

- `A2AActorAdapter`: Allows A2A clients to invoke internal actors
- `A2AServerActorAdapter`: Allows A2A servers to route requests to internal actors
- `ActorAgentAdapter`: Wraps existing agents to make them compatible with actors

## Supervision Strategies

The supervisor supports multiple restart strategies:
- `OneForOne`: Restart only the failed child
- `OneForAll`: Restart all children when one fails
- `RestForOne`: Restart failed child and all children started after it
- `SimpleOneForOne`: For dynamically created children

## Benefits

1. **Performance**: Internal communication is much faster than HTTP/JSON-RPC
2. **Fault Tolerance**: Automatic recovery from actor failures
3. **Scalability**: Thousands of lightweight actors can run efficiently
4. **Isolation**: Each actor has its own state and execution context
5. **Compatibility**: Maintains A2A protocol for external systems

## Migration Strategy

The hybrid approach allows for incremental migration:

1. **Phase 1**: Implement actor system foundation
2. **Phase 2**: Adapt existing agents to work with actors
3. **Phase 3**: Update A2A server to use actor system internally
4. **Phase 4**: Add supervision for critical agents
5. **Phase 5**: Optimize internal agent-to-agent communication

## Performance Considerations

- Actor communication is significantly faster than network-based A2A calls
- Each actor processes messages sequentially, eliminating the need for mutexes
- Mailbox channels provide natural backpressure
- Supervisors add minimal overhead but provide robust fault tolerance