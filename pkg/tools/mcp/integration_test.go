// Copyright 2025 Apache Spark AI Agents
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mcp

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/tools"
	"github.com/seyi/dagens/pkg/types"
)

// mockMCPServer is a simple MCP server for integration testing
type mockMCPServer struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

// startMockMCPServer starts a mock MCP server using a Go script
func startMockMCPServer(t *testing.T) *mockMCPServer {
	// Create a simple echo-based mock server
	// This simulates an MCP server that responds to JSON-RPC requests
	cmd := exec.Command("bash", "-c", createMockServerScript())

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("Failed to create stdin pipe: %v", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("Failed to create stdout pipe: %v", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("Failed to create stderr pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start mock server: %v", err)
	}

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	return &mockMCPServer{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
	}
}

func (s *mockMCPServer) stop() {
	if s.stdin != nil {
		s.stdin.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
		s.cmd.Wait()
	}
}

// createMockServerScript creates a bash script that simulates an MCP server
func createMockServerScript() string {
	return `
while IFS= read -r line; do
    # Parse JSON-RPC request
    method=$(echo "$line" | grep -o '"method":"[^"]*"' | cut -d'"' -f4)
    id=$(echo "$line" | grep -o '"id":[0-9]*' | cut -d':' -f2)

    case "$method" in
        "ping")
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{}}"
            ;;
        "tools/list")
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"tools\":[{\"name\":\"test_echo\",\"description\":\"Echo test tool\",\"inputSchema\":{\"type\":\"object\",\"properties\":{\"message\":{\"type\":\"string\"}}}}]}}"
            ;;
        "tools/call")
            # Extract message from arguments
            message=$(echo "$line" | grep -o '"message":"[^"]*"' | cut -d'"' -f4)
            if [ -z "$message" ]; then
                message="default echo"
            fi
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"Echo: $message\"}]}}"
            ;;
        *)
            echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"error\":{\"code\":-32601,\"message\":\"Method not found\"}}"
            ;;
    esac
done
`
}

// TestStdioTransportIntegration tests stdio transport with a real subprocess
func TestStdioTransportIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Start mock server
	server := startMockMCPServer(t)
	defer server.stop()

	// Create stdio transport using echo command (simpler test)
	// We'll use a different approach - direct command test
	params := StdioConnectionParams{
		Command: "bash",
		Args:    []string{"-c", createMockServerScript()},
		Timeout: 30,
	}

	transport, err := NewStdioTransport(params)
	if err != nil {
		t.Fatalf("NewStdioTransport() error = %v", err)
	}
	defer transport.Close()

	if !transport.IsConnected() {
		t.Error("Transport should be connected")
	}

	// Test ping
	req := &JSONRPCRequest{
		JSONRPC: JSONRPCVersion,
		ID:      1,
		Method:  MethodPing,
	}

	resp, err := transport.Send(ctx, req)
	if err != nil {
		t.Fatalf("Send(ping) error = %v", err)
	}

	if resp.ID != req.ID {
		t.Errorf("Response ID = %d, want %d", resp.ID, req.ID)
	}

	// Test list tools
	req = &JSONRPCRequest{
		JSONRPC: JSONRPCVersion,
		ID:      2,
		Method:  MethodListTools,
	}

	resp, err = transport.Send(ctx, req)
	if err != nil {
		t.Fatalf("Send(list tools) error = %v", err)
	}

	if resp.Result == nil {
		t.Fatal("ListTools response has no result")
	}

	// Test call tool
	req = &JSONRPCRequest{
		JSONRPC: JSONRPCVersion,
		ID:      3,
		Method:  MethodCallTool,
		Params: map[string]interface{}{
			"name": "test_echo",
			"arguments": map[string]interface{}{
				"message": "hello",
			},
		},
	}

	resp, err = transport.Send(ctx, req)
	if err != nil {
		t.Fatalf("Send(call tool) error = %v", err)
	}

	if resp.Result == nil {
		t.Fatal("CallTool response has no result")
	}
}

// TestMCPClientIntegration tests full client workflow
func TestMCPClientIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Create transport
	params := StdioConnectionParams{
		Command: "bash",
		Args:    []string{"-c", createMockServerScript()},
		Timeout: 30,
	}

	transport, err := NewStdioTransport(params)
	if err != nil {
		t.Fatalf("Failed to create transport: %v", err)
	}
	defer transport.Close()

	// Create client
	client := NewClient(transport)

	// Connect
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	// List tools
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}

	if len(tools) == 0 {
		t.Fatal("ListTools() returned no tools")
	}

	t.Logf("Found %d tools", len(tools))
	for _, tool := range tools {
		t.Logf("  - %s: %s", tool.Name, tool.Description)
	}

	// Call tool
	result, err := client.CallTool(ctx, "test_echo", map[string]interface{}{
		"message": "integration test",
	})

	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}

	if result == nil {
		t.Fatal("CallTool() returned nil result")
	}

	text := ExtractTextFromResult(result)
	t.Logf("Tool result: %s", text)

	if !strings.Contains(text, "Echo") {
		t.Errorf("Result should contain 'Echo', got: %s", text)
	}

	// Close
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// TestMCPToolsetIntegration tests full toolset workflow
func TestMCPToolsetIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Create transport and client manually for testing
	params := StdioConnectionParams{
		Command: "bash",
		Args:    []string{"-c", createMockServerScript()},
		Timeout: 30,
	}

	transport, err := NewStdioTransport(params)
	if err != nil {
		t.Fatalf("Failed to create transport: %v", err)
	}
	defer transport.Close()

	client := NewClient(transport)
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Failed to connect client: %v", err)
	}

	// Create toolset
	toolset := &MCPToolset{
		client:         client,
		params:         params,
		tools:          []*types.ToolDefinition{},
		distributedCfg: DefaultDistributedMCPConfig(),
		connected:      true,
		partitionID:    "test-partition",
		metrics:        make([]ToolCallMetrics, 0),
	}

	// Discover tools
	mcpTools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("Failed to list tools: %v", err)
	}

	for _, mcpTool := range mcpTools {
		tool := toolset.wrapMCPTool(mcpTool)
		toolset.tools = append(toolset.tools, tool)
	}

	// Get tools
	discoveredTools := toolset.GetTools()
	if len(discoveredTools) == 0 {
		t.Fatal("No tools discovered")
	}

	t.Logf("Discovered %d tools", len(discoveredTools))

	// Test tool handler
	tool := discoveredTools[0]
	result, err := tool.Handler(ctx, map[string]interface{}{
		"message": "toolset test",
	})

	if err != nil {
		t.Fatalf("Tool handler error = %v", err)
	}

	t.Logf("Tool result: %v", result)

	// Check metrics
	metrics := toolset.GetMetrics()
	if len(metrics) == 0 {
		t.Error("No metrics recorded")
	}

	if len(metrics) > 0 {
		t.Logf("Metrics: tool=%s, duration=%.2fms, partition=%s",
			metrics[0].ToolName,
			metrics[0].DurationMs,
			metrics[0].PartitionID)
	}
}

// TestToolRegistryIntegration tests integration with our tool registry
func TestToolRegistryIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Create mock client
	transport := newMockTransport()
	transport.setResponse(MethodListTools, &JSONRPCResponse{
		JSONRPC: JSONRPCVersion,
		Result: map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{
					"name":        "mock_tool",
					"description": "A mock tool for testing",
					"inputSchema": map[string]interface{}{
						"type": "object",
					},
				},
			},
		},
	})

	transport.setResponse(MethodCallTool, &JSONRPCResponse{
		JSONRPC: JSONRPCVersion,
		Result: map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{
					"type": "text",
					"text": "mock result",
				},
			},
		},
	})

	client := NewClient(transport)
	client.Connect(ctx)

	// Create toolset
	toolset := &MCPToolset{
		client:         client,
		tools:          []*types.ToolDefinition{},
		distributedCfg: DefaultDistributedMCPConfig(),
		connected:      true,
	}

	// Discover tools
	mcpTools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("Failed to list tools: %v", err)
	}

	for _, mcpTool := range mcpTools {
		tool := toolset.wrapMCPTool(mcpTool)
		toolset.tools = append(toolset.tools, tool)
	}

	// Create tool registry and register MCP tools
	registry := tools.NewToolRegistry()
	if err := toolset.RegisterTools(registry); err != nil {
		t.Fatalf("RegisterTools() error = %v", err)
	}

	// Execute tool via registry
	result, err := registry.Execute(ctx, "mock_tool", map[string]interface{}{
		"param": "value",
	})

	if err != nil {
		t.Fatalf("Registry.Execute() error = %v", err)
	}

	if result == nil {
		t.Fatal("Registry.Execute() returned nil")
	}

	t.Logf("Tool executed via registry: %v", result)
}

// TestMCPServerStderr tests stderr capture
func TestMCPServerStderr(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Create a script that writes to stderr
	script := `
echo "Server starting" >&2
while IFS= read -r line; do
    method=$(echo "$line" | grep -o '"method":"[^"]*"' | cut -d'"' -f4)
    id=$(echo "$line" | grep -o '"id":[0-9]*' | cut -d':' -f2)

    echo "Processing $method" >&2
    echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{}}"
done
`

	params := StdioConnectionParams{
		Command: "bash",
		Args:    []string{"-c", script},
		Timeout: 30,
	}

	transport, err := NewStdioTransport(params)
	if err != nil {
		t.Fatalf("NewStdioTransport() error = %v", err)
	}
	defer transport.Close()

	// Give stderr reader time to capture
	time.Sleep(200 * time.Millisecond)

	// Check if we can get stderr (if transport exposes it)
	if stdioTransport, ok := transport.(*stdioTransport); ok {
		stderrLines := stdioTransport.GetStderr()
		if len(stderrLines) > 0 {
			t.Logf("Captured %d stderr lines", len(stderrLines))
			for _, line := range stderrLines {
				t.Logf("  stderr: %s", line)
			}
		}
	}
}

// Helper function to check if a command exists
func commandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

// TestRealMCPServer tests with a real MCP server if available
func TestRealMCPServer(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping real MCP server test in short mode")
	}

	// Check if npx is available
	if !commandExists("npx") {
		t.Skip("npx not available, skipping real MCP server test")
	}

	// Check if we have a real MCP server package to test
	// For now, skip this test unless explicitly enabled
	if os.Getenv("TEST_REAL_MCP_SERVER") != "1" {
		t.Skip("Set TEST_REAL_MCP_SERVER=1 to test with real MCP servers")
	}

	ctx := context.Background()

	// Example: Test with a real MCP server (if available)
	params := StdioConnectionParams{
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-everything"},
		Timeout: 60,
	}

	client, err := NewClientFromParams(ctx, params)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// List tools
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}

	t.Logf("Found %d tools from real MCP server", len(tools))
	for _, tool := range tools {
		t.Logf("  - %s: %s", tool.Name, tool.Description)
	}
}

// TestDistributedExecution tests distributed tool execution simulation
func TestDistributedExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping distributed execution test in short mode")
	}

	ctx := context.Background()

	// Create mock transports for multiple partitions
	partitions := []string{"partition-0", "partition-1", "partition-2"}

	for _, partitionID := range partitions {
		transport := newMockTransport()

		transport.setResponse(MethodListTools, &JSONRPCResponse{
			JSONRPC: JSONRPCVersion,
			Result: map[string]interface{}{
				"tools": []interface{}{
					map[string]interface{}{
						"name":        "distributed_tool",
						"description": "Tool for distributed execution",
						"inputSchema": map[string]interface{}{},
					},
				},
			},
		})

		transport.setResponse(MethodCallTool, &JSONRPCResponse{
			JSONRPC: JSONRPCVersion,
			Result: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": fmt.Sprintf("Result from %s", partitionID),
					},
				},
			},
		})

		client := NewClient(transport)
		client.Connect(ctx)

		toolset := &MCPToolset{
			client:         client,
			tools:          []*types.ToolDefinition{},
			distributedCfg: DefaultDistributedMCPConfig(),
			connected:      true,
			partitionID:    partitionID,
			metrics:        make([]ToolCallMetrics, 0),
		}

		// Discover and execute
		mcpTools, _ := client.ListTools(ctx)
		for _, mcpTool := range mcpTools {
			tool := toolset.wrapMCPTool(mcpTool)
			toolset.tools = append(toolset.tools, tool)
		}

		// Execute tool on this partition
		if len(toolset.tools) > 0 {
			result, err := toolset.tools[0].Handler(ctx, map[string]interface{}{})
			if err != nil {
				t.Errorf("Partition %s: tool execution failed: %v", partitionID, err)
				continue
			}

			t.Logf("Partition %s: %v", partitionID, result)

			// Check metrics
			metrics := toolset.GetMetrics()
			if len(metrics) > 0 && metrics[0].PartitionID != partitionID {
				t.Errorf("Metric partition ID = %s, want %s", metrics[0].PartitionID, partitionID)
			}
		}
	}

	t.Logf("Successfully executed on %d partitions", len(partitions))
}
