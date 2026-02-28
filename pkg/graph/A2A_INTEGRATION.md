# A2A Integration Guide for Graph Package

## Overview

The A2A (Agent-to-Agent) integration in the graph package enables distributed graph execution across multiple agent instances. This allows you to create graphs that span local and remote agents, combining the power of local computation with distributed, specialized remote agents.

## Key Components

### A2ANode
- Represents a remote agent in the graph
- Extends the `BaseNode` to execute remote agents via A2A protocol
- Supports capability-based discovery and execution

### A2AGraphBuilder
- Extends the regular graph builder
- Provides methods to add A2A nodes to graphs
- Supports discovery of agents by capability

### A2AGraphExecutor
- Graph execution engine that handles A2A nodes
- Manages state propagation between local and remote agents

## Usage Examples

### Basic Example: Adding Remote Nodes to a Graph

```go
package main

import (
	"context"
	"fmt"
	
	"github.com/seyi/dagens/pkg/a2a"
	"github.com/seyi/dagens/pkg/graph"
)

func main() {
	ctx := context.Background()
	
	// Create A2A client with discovery registry
	registry := a2a.NewDiscoveryRegistry()
	client := a2a.NewHTTPA2AClient(registry)
	
	// Register some remote agents with capabilities
	card := &a2a.AgentCard{
		ID:          "research-agent",
		Name:        "Research Agent",
		Description: "Performs web research",
		Endpoint:    "http://research-service:8080/invoke",
		Capabilities: []a2a.Capability{
			{Name: "web_research", Description: "Search web content"},
		},
	}
	registry.Register(card)
	
	// Create graph with A2A builder
	builder := graph.NewA2AGraphBuilder("research-workflow", client)
	
	// Add local preprocessing node
	builder.AddFunction("preprocess", func(ctx context.Context, state graph.State) error {
		state.Set("query", "latest developments in AI")
		state.Set("timestamp", "2025-01-01")
		return nil
	})
	
	// Add A2A node for remote research
	builder.AddA2ANode(graph.A2ANodeConfig{
		ID:      "research-step",
		Name:    "Remote Research",
		Client:  client,
		AgentID: "research-agent",
	})
	
	// Add local postprocessing node
	builder.AddFunction("postprocess", func(ctx context.Context, state graph.State) error {
		result, _ := state.Get("a2a_result")
		fmt.Printf("Research result: %v\n", result)
		return nil
	})
	
	// Set up graph structure
	builder.AddEdge("preprocess", "research-step")
	builder.AddEdge("research-step", "postprocess")
	
	// Set entry and finish nodes
	builder.SetEntry("preprocess")
	builder.AddFinish("postprocess")
	
	// Build the graph
	graph, err := builder.Build()
	if err != nil {
		panic(err)
	}
	
	// Create executor and run the graph
	executor := graph.NewA2AGraphExecutor(graph, client)
	initialState := graph.NewMemoryState()
	result, err := executor.Execute(ctx, initialState)
	
	if err != nil {
		fmt.Printf("Execution failed: %v\n", err)
	} else {
		fmt.Printf("Execution completed successfully: %v nodes executed\n", len(result.Nodes))
	}
}
```

### Discovery-Based Example: Adding Agents by Capability

```go
// Discover and add agents dynamically based on capabilities
builder := graph.NewA2AGraphBuilder("dynamic-workflow", a2aClient)

// Add agents that support specific capabilities
builder.AddA2ANodeByCapability(ctx, "data_processing", "processor")
builder.AddA2ANodeByCapability(ctx, "report_generation", "reporter")

// The builder will automatically create nodes for all discovered agents
// with the specified capabilities
```

## Integration Benefits

1. **Scalability**: Distribute workload across multiple agent instances
2. **Specialization**: Use remote agents with specific expertise
3. **Resilience**: Failure of one agent doesn't necessarily impact others
4. **Flexibility**: Mix and match local and remote processing nodes
5. **Service Discovery**: Dynamically discover and connect to available agents

## State Management

The integration maintains state consistency across local and remote nodes:
- Graph state is passed to remote agents as context
- Remote agent outputs are merged back into the graph state
- Metadata is preserved throughout the execution flow

## Security Considerations

- All remote calls use the A2A protocol with configurable authentication
- Agent cards provide capability and endpoint verification
- Discovery system tracks agent availability and status