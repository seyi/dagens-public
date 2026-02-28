# Stdio IPC for Agent Communication

This package provides an implementation of interprocess communication (IPC) for agents using stdin/stdout as the communication channel, similar to Erlang's message passing model.

## Overview

The `ipc` package enables agents to communicate across process boundaries using JSON messages sent via stdin/stdout. This approach provides:

- **Erlang-like message passing**: Agents send structured messages to each other
- **Process isolation**: Each agent can run in its own process
- **Language agnostic**: Communication happens via JSON messages
- **Fault tolerance**: Processes can fail independently

## Key Components

### 1. ProcessAgent
Represents an agent running in a separate process. It manages the lifecycle of the external process and handles communication via stdin/stdout.

### 2. StdioAgentNode
A graph node that communicates with an external agent process. It converts graph state to JSON messages and updates state based on responses.

### 3. StdioAgentServer
A server that listens on stdin for incoming messages and responds on stdout. It can host multiple agents and route messages to the appropriate agent.

### 4. Message Structure
Messages follow a standard format:
```json
{
  "id": "unique-message-id",
  "from": "sender-id",
  "to": "receiver-id", 
  "type": "request|response|error|ping|pong",
  "payload": {...},
  "sent_at": "timestamp",
  "metadata": {...}
}
```

## Comparison to Erlang Model

| Feature | Erlang | Dagens Stdio IPC |
|---------|--------|------------------|
| Message Passing | Native process communication | JSON messages via stdin/stdout |
| Process Isolation | Lightweight processes | Separate OS processes |
| Fault Tolerance | Process supervision trees | Process restart and error handling |
| Distribution | Built-in across nodes | Could be extended with network transport |
| Pattern Matching | Native support | Could be implemented in message handlers |

## Usage Example

### Server Process (stdio_agent_process.go)
```go
server := ipc.NewStdioAgentServer()
server.RegisterAgent("echo", &SimpleEchoAgent{id: "echo"})
server.Start() // Listens on stdin/stdout
```

### Client Process
```go
// Create a process agent that connects to the server process
agent := ipc.NewProcessAgent("my-agent", "./stdio_agent_process")
agent.Start()

// Send a message
msg := ipc.Message{
    Type: ipc.MessageTypeRequest,
    Payload: map[string]interface{}{"input": "hello"},
}
response, err := agent.SendRequest(msg.Payload)
```

### Integration with Graph
```go
config := ipc.StdioAgentNodeConfig{
    ID: "stdio-agent-node",
    Agent: processAgent, // From above
}
node := ipc.NewStdioAgentNode(config)

// Add to graph and execute
graph.AddNode(node)
```

## Benefits

1. **Process Isolation**: Agents run in separate processes, preventing one agent's failure from affecting others
2. **Resource Management**: Each process can be managed independently for memory/CPU usage
3. **Security**: Process boundaries provide natural security isolation
4. **Scalability**: Processes can be distributed across machines
5. **Polyglot**: Different agents can be written in different languages

## Drawbacks

1. **Performance**: Message serialization/deserialization overhead
2. **Complexity**: More complex than in-process communication
3. **Latency**: Inter-process communication is slower than in-memory
4. **Process Management**: Need to manage process lifecycle

## Architecture Considerations

This approach bridges the gap between Erlang's lightweight process model and traditional Unix process model. While not as efficient as Erlang's native message passing, it provides:

- Similar architectural benefits (isolation, fault tolerance)
- Compatibility with existing process-based systems
- Potential for distribution across machines
- Language interoperability

The implementation demonstrates how to achieve Erlang-inspired communication patterns in a Go-based agent system using standard Unix IPC mechanisms.