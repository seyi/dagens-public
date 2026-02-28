# Dagens Examples

This directory contains examples demonstrating the Dagens AI agent framework.

## Path C: LangGraph-Inspired Go Runtime

Dagens implements a lightweight, stateful, graph-based agent runtime inspired by LangGraph but optimized for Go.

### Architecture Highlights

- **Stateful Agents**: Persistent conversation memory (vs. Spark's stateless tasks)
- **Real-Time Execution**: <1ms graph overhead (vs. 100ms+ batch processing)
- **Go-Native**: Pure Go with goroutine-based actors (vs. JVM/CGO complexity)
- **Optional Distribution**: Start local, scale when needed (vs. required distribution)

## Examples

### 1. Simple Graph (`simple_graph/`)

**Purpose**: Demonstrates basic graph construction and manual execution.

**Features**:
- Fluent builder API
- Function nodes
- Direct edges
- Manual state management

**Run**:
```bash
cd examples/simple_graph
go run main.go
```

**Expected Output**:
```
Graph 'greeting-bot' created successfully!
- Nodes: 2
- Entry: greet
- Finish nodes: [respond]

[greet] Generated: Hello, Alice! Welcome to the graph execution system.
[respond] Generated: Hello, Alice! Welcome to the graph execution system. How can I help you today?
```

### 2. AI Agent Chatbot (`agent_chatbot/`)

**Purpose**: Complete example showing AI agent integration with OpenAI.

**Features**:
- OpenAI provider integration
- AgentNode with system prompts
- LocalBackend automatic execution
- Multi-turn conversations
- Token usage tracking
- Cost estimation

**Setup**:
```bash
export OPENAI_API_KEY="sk-..."
```

**Run**:
```bash
cd examples/agent_chatbot
go run main.go
```

**Expected Output**:
```
Provider created: gpt-4o-mini (openai)
- Supports tools: true
- Supports vision: true
- Context window: 128000 tokens

Graph 'chatbot' built successfully!

User: Hello! Can you explain what you are in one sentence?

=== Execution Results ===
Execution ID: <uuid>
Status: completed
Duration: 847ms
Nodes executed: 1

=== Agent Response ===
Assistant: I'm an AI assistant designed to help answer questions and assist with tasks.

Token usage:
  Prompt tokens: 35
  Completion tokens: 15
  Total tokens: 50
  Estimated cost: $0.000019
```

## Core Concepts

### Graph Builder API

```go
graph, err := graph.NewBuilder("my-graph").
    AddAgent("assistant", graph.AgentConfig{
        Provider:     provider,
        SystemPrompt: "You are helpful.",
    }).
    AddFunction("process", processingFunc).
    AddEdge("assistant", "process").
    SetEntry("assistant").
    AddFinish("process").
    Build()
```

### Backend Execution

```go
backend := backend.NewLocalBackend()
state := graph.NewMemoryState()
state.Set("user_message", "Hello!")

result, err := backend.Execute(ctx, graph, state)
```

### State Management

```go
// Set values
state.Set("key", value)

// Get values
val, exists := state.Get("key")

// Snapshots for debugging
snapshot := state.Snapshot()
state.Restore(snapshot)
```

## Key Differences from Spark

| Aspect | Spark (Path B - Rejected) | Dagens (Path C) |
|--------|---------------------------|-----------------|
| **State** | Stateless tasks | Stateful agents |
| **Latency** | 100ms+ batch overhead | <1ms graph overhead |
| **Runtime** | JVM + CGO | Pure Go |
| **Distribution** | Required | Optional |
| **Workflow** | DAG tasks | Graph nodes |
| **Actor Model** | Thread pools | Goroutines |

## Why Path C?

Based on unanimous consensus (3-0 vote) from three frontier AI models:
- **DeepSeek R1**: Rejected Spark, confidence 9/10
- **GPT-5.1**: Rejected Spark, confidence 8/10
- **Grok-4**: Rejected Spark (even when assigned to argue FOR it), confidence 8/10

See: `docs/ARCHITECTURAL_CONSENSUS.md`

## Next Steps

1. **Run the examples** to understand the API
2. **Read the design doc**: `docs/PATH_C_DESIGN.md`
3. **Check the consensus**: `docs/ARCHITECTURAL_CONSENSUS.md`
4. **Review OpenAI provider**: `pkg/models/openai/`

## Future Examples (Coming Soon)

- **Tool Calling**: Agents with function calling
- **RAG Pipeline**: Retrieval-augmented generation
- **Multi-Agent**: Collaborative agents
- **Conditional Routing**: Dynamic workflow branching
- **Distributed Execution**: NATS-based distribution

## Requirements

- Go 1.21+
- OpenAI API key (for agent examples)
- Environment variables:
  - `OPENAI_API_KEY` - Required for agent_chatbot example

## Architecture Docs

- **Design**: `../docs/PATH_C_DESIGN.md` - Complete technical specification
- **Consensus**: `../docs/ARCHITECTURAL_CONSENSUS.md` - Why we chose Path C
- **Critical Assessment**: `../docs/GEMINI3_CRITICAL_ASSESSMENT.md` - Production fixes applied

---

**Generated**: December 5, 2025
**Framework**: Dagens (Path C: LangGraph-Inspired Go Runtime)
**Based On**: Unanimous architectural consensus rejecting Spark
