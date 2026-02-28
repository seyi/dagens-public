# Model Context Protocol (MCP) Support

Implementation of the Model Context Protocol (MCP) for third-party tool integration in Dagens.

## Overview

The MCP package provides ADK-compatible third-party tool integration with distributed execution support for the Dagens runtime. It enables integration with the MCP ecosystem while preserving a distributed-first architecture.

## Features

✅ **JSON-RPC 2.0 Protocol** - Full MCP specification compliance
✅ **Stdio Transport** - Subprocess communication (default for npm packages)
✅ **HTTP Transport** - Remote server communication
✅ **Dynamic Tool Discovery** - Auto-discover tools from MCP servers
✅ **Distributed Execution** - Run MCP tools across distributed workers
✅ **Metrics & Observability** - Detailed performance metrics
✅ **Connection Pooling** - Efficient resource management
✅ **ADK Parity** - Follows the same usage model as Google ADK's MCPToolset

## Quick Start

### Basic Usage

```go
package main

import (
    "context"
    "log"

    "github.com/seyi/dagens/pkg/tools"
    "github.com/seyi/dagens/pkg/tools/mcp"
)

func main() {
    ctx := context.Background()

    // Create MCP toolset (matches ADK pattern)
    toolset, err := mcp.NewMCPToolset(mcp.StdioConnectionParams{
        Command: "npx",
        Args:    []string{"-y", "agentql-mcp"},
        Env: map[string]string{
            "AGENTQL_API_KEY": "your-api-key",
        },
        Timeout: 300,
    })

    if err != nil {
        log.Fatal(err)
    }

    // Connect and discover tools
    if err := toolset.Connect(ctx); err != nil {
        log.Fatal(err)
    }
    defer toolset.Disconnect()

    // Register in tool registry
    registry := tools.NewToolRegistry()
    toolset.RegisterTools(registry)

    // Tools are now available to agents!
}
```

### With LLM Agents

```go
// Create agent with MCP tools
agent := agents.NewLlmAgent(agents.LlmAgentConfig{
    Name:        "web-scraper",
    Instruction: "Extract data from websites",
    Tools:       []string{"extract-web-data"}, // From MCP server
}, modelProvider, registry)

// Execute - uses third-party tool!
output, _ := agent.Execute(ctx, &agent.AgentInput{
    Instruction: "Extract product prices from https://example.com",
})
```

## Architecture

### Transport Layer

```
┌─────────────────────────────────────────┐
│         MCP Client                       │
│  (JSON-RPC 2.0 Protocol)                │
└────────────┬────────────┬───────────────┘
             │            │
   ┌─────────▼──────┐   ┌▼────────────────┐
   │ Stdio Transport│   │  HTTP Transport  │
   │  (subprocess)  │   │  (remote server) │
   └─────────┬──────┘   └┬────────────────┘
             │            │
   ┌─────────▼────────────▼────────┐
   │    MCP Server Process         │
   │  (npx, node, custom binary)   │
   └───────────────────────────────┘
```

### Integration with Tools

```
┌──────────────────────────────────────┐
│      MCPToolset                      │
│  - Tool discovery                    │
│  - Tool wrapping                     │
│  - Metrics collection                │
└────────────┬─────────────────────────┘
             │
   ┌─────────▼────────────┐
   │   ToolRegistry       │
   │  (tools.ToolRegistry)│
   └─────────┬────────────┘
             │
   ┌─────────▼────────────┐
   │    LLM Agents        │
   │  (agents.LlmAgent)   │
   └──────────────────────┘
```

## Connection Types

### Stdio Transport (Local Subprocesses)

**Use for**: npm packages, local scripts, command-line tools

```go
params := mcp.StdioConnectionParams{
    Command: "npx",
    Args:    []string{"-y", "@modelcontextprotocol/server-brave-search"},
    Env: map[string]string{
        "BRAVE_API_KEY": "your-key",
    },
    Timeout: 300, // seconds
}

toolset, _ := mcp.NewMCPToolset(params)
```

**Supported MCP Servers** (all via npm):
- `agentql-mcp` - Web scraping with AI
- `@modelcontextprotocol/server-brave-search` - Web search
- `@modelcontextprotocol/server-puppeteer` - Browser automation
- `@modelcontextprotocol/server-filesystem` - File operations
- `@modelcontextprotocol/server-postgres` - PostgreSQL access
- `@modelcontextprotocol/server-github` - GitHub API
- `@modelcontextprotocol/server-slack` - Slack integration
- 100+ more on npm!

### HTTP Transport (Remote Servers)

**Use for**: Remote MCP services, cloud-hosted tools

```go
params := mcp.HTTPConnectionParams{
    URL: "https://mcp-server.example.com/api",
    Headers: map[string]string{
        "Authorization": "Bearer token",
    },
    Timeout:    300,
    SessionID:  "session-123",  // Optional: stateful connections
    EnableCORS: true,
}

toolset, _ := mcp.NewMCPToolset(params)
```

## Distributed Execution

### Per-Partition Toolsets

Run MCP tools across distributed workers:

```go
// Create distributed toolset manager
distributed := mcp.NewDistributedMCPToolset(
    params,
    &mcp.DistributedMCPConfig{
        PartitionStrategy:  "round-robin",
        MaxConcurrentCalls: 10,
        EnableMetrics:      true,
    },
)

// Get toolset for specific partition
toolset, _ := distributed.GetOrCreateToolset(ctx, "partition-0")

// Each partition has its own MCP server connection
// Enables parallel tool execution across cluster
```

### Partition Strategies

- **round-robin**: Distribute calls evenly
- **least-loaded**: Route to partition with fewest active calls
- **sticky-session**: Keep user requests on same partition
- **broadcast**: Execute on all partitions (for aggregation)

### Distributed Benefits

1. **Parallel Execution**: Multiple MCP servers across partitions
2. **Load Distribution**: Spread tool calls across cluster
3. **Fault Isolation**: Partition failure doesn't affect others
4. **Data Locality**: Keep data and tools co-located

## Metrics & Monitoring

### Tool Call Metrics

```go
// Get metrics from toolset
metrics := toolset.GetMetrics()

for _, m := range metrics {
    fmt.Printf("Tool: %s\n", m.ToolName)
    fmt.Printf("Duration: %.2fms\n", m.DurationMs)
    fmt.Printf("Success: %v\n", m.Success)
    fmt.Printf("Partition: %s\n", m.PartitionID)
    fmt.Printf("Transport: %s\n", m.TransportType)
}
```

### Distributed Metrics

```go
// Get aggregated metrics across all partitions
allMetrics := distributed.GetAggregatedMetrics()

// Analyze by partition
metricsPerPartition := make(map[string][]mcp.ToolCallMetrics)
for _, m := range allMetrics {
    metricsPerPartition[m.PartitionID] = append(
        metricsPerPartition[m.PartitionID], m)
}
```

## Integration Patterns

### With Agent Hierarchies

```go
// MCP tools work seamlessly with agent hierarchies
scraper := agents.NewLlmAgent(..., registry)  // Has MCP tools
analyzer := agents.NewLlmAgent(...)

coordinator := agent.NewAgent(agent.AgentConfig{
    SubAgents: []agent.Agent{scraper, analyzer},
})

// Coordinator can delegate to scraper which uses MCP tools
```

### With Workflow Agents

```go
// Use MCP tools in sequential workflows
searcher := agents.NewLlmAgent(..., registry)  // Uses brave_search (MCP)
summarizer := agents.NewLlmAgent(...)

pipeline := agents.NewSequential().
    Add(searcher).  // MCP tool execution
    Add(summarizer).
    Build()
```

### With Parallel Agents

```go
// Run multiple MCP tools in parallel
toolA := agents.NewLlmAgent(..., registryA)  // MCP tool 1
toolB := agents.NewLlmAgent(..., registryB)  // MCP tool 2
toolC := agents.NewLlmAgent(..., registryC)  // MCP tool 3

parallel := agents.NewParallel(agents.VoteAggregation).
    Add(toolA).
    Add(toolB).
    Add(toolC).
    Build()

// All three MCP tools execute concurrently
```

## Error Handling

### Connection Errors

```go
toolset, err := mcp.NewMCPToolset(params)
if err != nil {
    // Invalid parameters
}

if err := toolset.Connect(ctx); err != nil {
    // Connection failed - server not started, network issue
}
```

### Tool Execution Errors

```go
result, err := client.CallTool(ctx, "tool_name", args)
if err != nil {
    // Tool call failed
    // Check error for JSON-RPC error details
}

if result.IsError {
    // Tool returned error result
}
```

### Timeout Handling

```go
// Set timeout in connection params
params := mcp.StdioConnectionParams{
    Timeout: 60, // 60 seconds
}

// Or use context deadline
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

result, err := client.CallTool(ctx, "slow_tool", args)
// Will timeout after 30 seconds
```

## Testing

### Unit Tests

```bash
# Run unit tests
go test ./pkg/tools/mcp -v -short

# Output:
# TestConnectionParamsValidation ... PASS
# TestJSONRPCError ... PASS
# TestMCPClient ... PASS
# TestMCPToolset ... PASS
# ... 11 tests PASS
```

### Integration Tests

```bash
# Run with real MCP servers (requires npx/node)
go test ./pkg/tools/mcp -v

# Run with real servers enabled
TEST_REAL_MCP_SERVER=1 go test ./pkg/tools/mcp -v
```

### Mock MCP Server

```go
// Use mock transport for testing
transport := newMockTransport()
transport.setResponse(mcp.MethodListTools, mockResponse)

client := mcp.NewClient(transport)
// Test without actual MCP server
```

## Performance

### Benchmarks

```bash
go test ./pkg/tools/mcp -bench=. -benchmem

# Results (on typical hardware):
# BenchmarkMCPClientCallTool-8   10000   115 μs/op   512 B/op
```

### Optimization Tips

1. **Connection Pooling**: Reuse toolsets across requests
2. **Partition Strategy**: Choose based on workload
3. **Timeout Configuration**: Balance responsiveness vs success rate
4. **Concurrent Calls**: Limit via `MaxConcurrentCalls`
5. **Metrics Overhead**: Disable if not needed (`EnableMetrics: false`)

## Security Considerations

### Environment Variables

```go
// GOOD: Pass secrets via environment
Env: map[string]string{
    "API_KEY": os.Getenv("MCP_API_KEY"),
}

// BAD: Hardcode secrets
Env: map[string]string{
    "API_KEY": "sk-12345...",
}
```

### Subprocess Isolation

- Each MCP server runs in separate process
- Process terminated on Disconnect()
- Stderr captured for debugging
- No shared memory between toolsets

### HTTP Security

```go
// Use TLS for production
params := mcp.HTTPConnectionParams{
    URL: "https://...",  // Not http://
    TLSInsecure: false,  // Verify certificates
}

// Authentication
Headers: map[string]string{
    "Authorization": "Bearer " + token,
}
```

## Comparison with ADK

| Feature | ADK Python | Dagens | Status |
|---------|-----------|-----------------|--------|
| **MCPToolset** | ✅ | ✅ | ✅ Match |
| **Stdio Transport** | ✅ | ✅ | ✅ Match |
| **HTTP Transport** | ✅ | ✅ | ✅ Match |
| **Tool Discovery** | ✅ | ✅ | ✅ Match |
| **Dynamic Registration** | ✅ | ✅ | ✅ Match |
| **Tool Registry Integration** | ✅ | ✅ | ✅ Match |
| **Plus: Distributed** | ❌ | ✅ Spark partitions | ✅ **Enhanced** |
| **Plus: Metrics** | Partial | ✅ Full metrics | ✅ **Enhanced** |
| **Plus: Go Performance** | ❌ | ✅ Native Go | ✅ **Enhanced** |

## Examples

See [examples/mcp_tools_example.go](../../examples/mcp_tools_example.go) for complete examples:

1. **Basic MCP Tool Usage** - Simple integration
2. **Multiple Third-Party Tools** - Combine multiple MCP servers
3. **Distributed MCP Execution** - Spark partition-aware execution
4. **HTTP Transport** - Remote MCP servers
5. **Agent Hierarchy with MCP** - Hierarchical delegation
6. **MCP Tools with Workflows** - Sequential/parallel workflows

## Troubleshooting

### Issue: MCP server not starting

**Symptoms**: "failed to start MCP server" error

**Solutions**:
- Ensure `npx` is installed (`npm install -g npx`)
- Check command exists: `which npx`
- Test manually: `npx -y agentql-mcp`
- Verify network access (for npm downloads)

### Issue: Tool not found

**Symptoms**: "tool X not found" error

**Solutions**:
- Check tool was discovered: `toolset.GetTools()`
- Verify exact tool name from server
- Ensure Connect() was called before using tools

### Issue: Timeout errors

**Symptoms**: "request timeout after X" error

**Solutions**:
- Increase timeout in params
- Check network latency
- Verify MCP server is responsive
- Use context with longer deadline

### Issue: Environment variables not working

**Symptoms**: "unauthorized" or "missing API key" errors

**Solutions**:
- Print env vars to verify: `fmt.Println(params.Env)`
- Check server expects exact key name
- Ensure no typos in environment variable names

## API Reference

### Types

- `MCPToolset` - Main toolset wrapper
- `DistributedMCPToolset` - Multi-partition manager
- `StdioConnectionParams` - Subprocess configuration
- `HTTPConnectionParams` - HTTP server configuration
- `MCPClient` - JSON-RPC client interface
- `Transport` - Communication transport interface
- `ToolCallMetrics` - Performance metrics
- `DistributedMCPConfig` - Distributed execution config

### Functions

- `NewMCPToolset(params)` - Create toolset
- `NewDistributedMCPToolset(params, config)` - Create distributed manager
- `Connect(ctx)` - Establish connection and discover tools
- `Disconnect()` - Close connection
- `GetTools()` - Get discovered tools
- `RegisterTools(registry)` - Register in ToolRegistry
- `GetMetrics()` - Get performance metrics

## Contributing

Contributions welcome! Areas for enhancement:

1. **Additional Transports**: gRPC, WebSocket
2. **Caching**: Result caching for idempotent tools
3. **Retry Logic**: Automatic retry with backoff
4. **Load Balancing**: Advanced partition routing
5. **Tool Versioning**: Support for multiple tool versions

## License

Apache License 2.0 - See LICENSE file for details.

## References

- [Model Context Protocol Specification](https://modelcontextprotocol.io/)
- [MCP TypeScript SDK](https://github.com/modelcontextprotocol/typescript-sdk)
- [MCP Servers on npm](https://www.npmjs.com/search?q=keywords:mcp-server)
- [ADK Documentation](https://google.github.io/adk-docs/)
- [Dagens Documentation](../../README.md)
