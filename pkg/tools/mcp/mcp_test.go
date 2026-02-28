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
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/types"
)

// mockTransport implements Transport for testing
type mockTransport struct {
	responses   map[string]*JSONRPCResponse
	requests    []*JSONRPCRequest
	mu          sync.Mutex
	connected   bool
	sendDelay   time.Duration
	shouldError bool
}

func newMockTransport() *mockTransport {
	return &mockTransport{
		responses: make(map[string]*JSONRPCResponse),
		requests:  make([]*JSONRPCRequest, 0),
		connected: true,
	}
}

func (m *mockTransport) Send(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.connected {
		return nil, fmt.Errorf("transport not connected")
	}

	if m.shouldError {
		return nil, fmt.Errorf("mock transport error")
	}

	// Record request
	m.requests = append(m.requests, req)

	// Simulate delay
	if m.sendDelay > 0 {
		time.Sleep(m.sendDelay)
	}

	// Return mock response based on method
	if resp, ok := m.responses[req.Method]; ok {
		// Create a copy with the request ID
		respCopy := *resp
		respCopy.ID = req.ID
		return &respCopy, nil
	}

	// Default response
	return &JSONRPCResponse{
		JSONRPC: JSONRPCVersion,
		ID:      req.ID,
		Result:  map[string]interface{}{},
	}, nil
}

func (m *mockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = false
	return nil
}

func (m *mockTransport) IsConnected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connected
}

func (m *mockTransport) setResponse(method string, resp *JSONRPCResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses[method] = resp
}

func (m *mockTransport) getRequests() []*JSONRPCRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	reqs := make([]*JSONRPCRequest, len(m.requests))
	copy(reqs, m.requests)
	return reqs
}

// TestConnectionParamsValidation tests connection parameter validation
func TestConnectionParamsValidation(t *testing.T) {
	tests := []struct {
		name    string
		params  ConnectionParams
		wantErr bool
	}{
		{
			name: "valid stdio params",
			params: StdioConnectionParams{
				Command: "npx",
				Args:    []string{"-y", "test-mcp"},
				Timeout: 300,
			},
			wantErr: false,
		},
		{
			name: "missing stdio command",
			params: StdioConnectionParams{
				Args: []string{"-y", "test-mcp"},
			},
			wantErr: true,
		},
		{
			name: "negative stdio timeout",
			params: StdioConnectionParams{
				Command: "npx",
				Timeout: -1,
			},
			wantErr: true,
		},
		{
			name: "valid http params",
			params: HTTPConnectionParams{
				URL:     "http://localhost:3000/mcp",
				Timeout: 300,
			},
			wantErr: false,
		},
		{
			name: "missing http URL",
			params: HTTPConnectionParams{
				Timeout: 300,
			},
			wantErr: true,
		},
		{
			name: "negative http timeout",
			params: HTTPConnectionParams{
				URL:     "http://localhost:3000",
				Timeout: -1,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.params.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestJSONRPCError tests JSON-RPC error handling
func TestJSONRPCError(t *testing.T) {
	err := &JSONRPCError{
		Code:    ErrorCodeInvalidRequest,
		Message: "Invalid request",
		Data:    "additional info",
	}

	errStr := err.Error()
	if errStr == "" {
		t.Error("Error() returned empty string")
	}

	t.Logf("Error string: %s", errStr)
}

// TestMCPClient tests the MCP client
func TestMCPClient(t *testing.T) {
	ctx := context.Background()

	// Create mock transport
	transport := newMockTransport()

	// Create client
	client := NewClient(transport)

	// Test Connect
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	if !client.IsConnected() {
		t.Error("IsConnected() = false, want true")
	}

	// Test ListTools
	toolsResp := &JSONRPCResponse{
		JSONRPC: JSONRPCVersion,
		Result: map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{
					"name":        "test_tool",
					"description": "A test tool",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"param1": map[string]interface{}{
								"type": "string",
							},
						},
					},
				},
			},
		},
	}
	transport.setResponse(MethodListTools, toolsResp)

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}

	if len(tools) != 1 {
		t.Fatalf("ListTools() returned %d tools, want 1", len(tools))
	}

	if tools[0].Name != "test_tool" {
		t.Errorf("tool name = %s, want test_tool", tools[0].Name)
	}

	// Test CallTool
	callResp := &JSONRPCResponse{
		JSONRPC: JSONRPCVersion,
		Result: map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{
					"type": "text",
					"text": "tool result",
				},
			},
		},
	}
	transport.setResponse(MethodCallTool, callResp)

	result, err := client.CallTool(ctx, "test_tool", map[string]interface{}{
		"param1": "value1",
	})

	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}

	if result == nil {
		t.Fatal("CallTool() returned nil result")
	}

	if len(result.Content) != 1 {
		t.Fatalf("result has %d content items, want 1", len(result.Content))
	}

	if result.Content[0].Text != "tool result" {
		t.Errorf("result text = %s, want 'tool result'", result.Content[0].Text)
	}

	// Test Close
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if client.IsConnected() {
		t.Error("IsConnected() = true after Close(), want false")
	}
}

// TestMCPClientErrors tests error handling
func TestMCPClientErrors(t *testing.T) {
	ctx := context.Background()

	// Create mock transport
	transport := newMockTransport()
	client := NewClient(transport)

	// Connect
	client.Connect(ctx)

	// Test ListTools with error response
	errorResp := &JSONRPCResponse{
		JSONRPC: JSONRPCVersion,
		Error: &JSONRPCError{
			Code:    ErrorCodeInternalError,
			Message: "Internal server error",
		},
	}
	transport.setResponse(MethodListTools, errorResp)

	_, err := client.ListTools(ctx)
	if err == nil {
		t.Error("ListTools() with error response should return error")
	}

	// Test CallTool with transport error
	transport.shouldError = true

	_, err = client.CallTool(ctx, "test_tool", map[string]interface{}{})
	if err == nil {
		t.Error("CallTool() with transport error should return error")
	}
}

// TestMCPToolset tests the MCP toolset wrapper
func TestMCPToolset(t *testing.T) {
	ctx := context.Background()

	// Create mock transport
	transport := newMockTransport()

	// Set up mock responses
	toolsResp := &JSONRPCResponse{
		JSONRPC: JSONRPCVersion,
		Result: map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{
					"name":        "extract_data",
					"description": "Extract data from web pages",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"url": map[string]interface{}{
								"type": "string",
							},
							"prompt": map[string]interface{}{
								"type": "string",
							},
						},
					},
				},
			},
		},
	}
	transport.setResponse(MethodListTools, toolsResp)

	callResp := &JSONRPCResponse{
		JSONRPC: JSONRPCVersion,
		Result: map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{
					"type": "text",
					"text": `{"products": [{"name": "Widget", "price": 29.99}]}`,
				},
			},
		},
	}
	transport.setResponse(MethodCallTool, callResp)

	// Create toolset (we can't use real params, so we'll mock this differently)
	// Instead, test the core logic with a mock client
	client := NewClient(transport)
	client.Connect(ctx)

	toolset := &MCPToolset{
		client:         client,
		tools:          []*types.ToolDefinition{},
		distributedCfg: DefaultDistributedMCPConfig(),
		connected:      true,
		partitionID:    "partition-0",
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

	// Test GetTools
	tools := toolset.GetTools()
	if len(tools) != 1 {
		t.Fatalf("GetTools() returned %d tools, want 1", len(tools))
	}

	if tools[0].Name != "extract_data" {
		t.Errorf("tool name = %s, want extract_data", tools[0].Name)
	}

	// Test tool handler
	result, err := tools[0].Handler(ctx, map[string]interface{}{
		"url":    "https://example.com",
		"prompt": "Extract products",
	})

	if err != nil {
		t.Fatalf("Tool handler error = %v", err)
	}

	if result == nil {
		t.Fatal("Tool handler returned nil result")
	}

	// Test metrics
	metrics := toolset.GetMetrics()
	if len(metrics) != 1 {
		t.Errorf("Got %d metrics, want 1", len(metrics))
	}

	if metrics[0].ToolName != "extract_data" {
		t.Errorf("metric tool name = %s, want extract_data", metrics[0].ToolName)
	}

	if metrics[0].PartitionID != "partition-0" {
		t.Errorf("metric partition ID = %s, want partition-0", metrics[0].PartitionID)
	}
}

// TestDistributedMCPToolset tests distributed toolset management
func TestDistributedMCPToolset(t *testing.T) {
	// This is a unit test for the distributed toolset structure
	// Integration tests will test actual distributed execution

	params := StdioConnectionParams{
		Command: "echo",
		Args:    []string{"test"},
	}

	config := &DistributedMCPConfig{
		PartitionStrategy:  "round-robin",
		MaxConcurrentCalls: 5,
		EnableMetrics:      true,
	}

	distributed := NewDistributedMCPToolset(params, config)

	if distributed == nil {
		t.Fatal("NewDistributedMCPToolset() returned nil")
	}

	if distributed.config.PartitionStrategy != "round-robin" {
		t.Errorf("partition strategy = %s, want round-robin", distributed.config.PartitionStrategy)
	}
}

// TestParseToolResult tests tool result parsing
func TestParseToolResult(t *testing.T) {
	resultData := map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": "test content",
			},
		},
		"isError": false,
	}

	result, err := ParseToolResult(resultData)
	if err != nil {
		t.Fatalf("ParseToolResult() error = %v", err)
	}

	if result == nil {
		t.Fatal("ParseToolResult() returned nil")
	}

	if len(result.Content) != 1 {
		t.Fatalf("result has %d content items, want 1", len(result.Content))
	}

	if result.Content[0].Text != "test content" {
		t.Errorf("content text = %s, want 'test content'", result.Content[0].Text)
	}

	if result.IsError {
		t.Error("IsError = true, want false")
	}
}

// TestExtractTextFromResult tests text extraction
func TestExtractTextFromResult(t *testing.T) {
	result := &MCPToolResult{
		Content: []MCPContent{
			{Type: "text", Text: "First line"},
			{Type: "text", Text: "Second line"},
		},
	}

	text := ExtractTextFromResult(result)
	if text == "" {
		t.Error("ExtractTextFromResult() returned empty string")
	}

	t.Logf("Extracted text: %s", text)

	// Test with nil result
	text = ExtractTextFromResult(nil)
	if text != "" {
		t.Error("ExtractTextFromResult(nil) should return empty string")
	}
}

// TestDefaultDistributedMCPConfig tests default configuration
func TestDefaultDistributedMCPConfig(t *testing.T) {
	config := DefaultDistributedMCPConfig()

	if config == nil {
		t.Fatal("DefaultDistributedMCPConfig() returned nil")
	}

	if config.PartitionStrategy != "round-robin" {
		t.Errorf("default partition strategy = %s, want round-robin", config.PartitionStrategy)
	}

	if config.MaxConcurrentCalls != 10 {
		t.Errorf("default max concurrent calls = %d, want 10", config.MaxConcurrentCalls)
	}

	if !config.EnableMetrics {
		t.Error("default EnableMetrics = false, want true")
	}
}

// TestConcurrentToolCalls tests concurrent tool execution
func TestConcurrentToolCalls(t *testing.T) {
	ctx := context.Background()

	// Create mock transport with slight delay
	transport := newMockTransport()
	transport.sendDelay = 10 * time.Millisecond

	callResp := &JSONRPCResponse{
		JSONRPC: JSONRPCVersion,
		Result: map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{
					"type": "text",
					"text": "result",
				},
			},
		},
	}
	transport.setResponse(MethodCallTool, callResp)

	client := NewClient(transport)
	client.Connect(ctx)

	// Make concurrent calls
	const numCalls = 10
	var wg sync.WaitGroup
	errors := make(chan error, numCalls)

	for i := 0; i < numCalls; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, err := client.CallTool(ctx, "test_tool", map[string]interface{}{
				"index": index,
			})
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent call error: %v", err)
	}

	// Verify all requests were received
	requests := transport.getRequests()
	// Subtract 1 for the Connect ping request
	actualCalls := len(requests) - 1
	if actualCalls != numCalls {
		t.Errorf("Got %d requests, want %d", actualCalls, numCalls)
	}
}

// TestToolsetConvertMCPResult tests result conversion
func TestToolsetConvertMCPResult(t *testing.T) {
	toolset := &MCPToolset{}

	tests := []struct {
		name   string
		result *MCPToolResult
		want   interface{}
	}{
		{
			name:   "nil result",
			result: nil,
			want:   nil,
		},
		{
			name: "single text content",
			result: &MCPToolResult{
				Content: []MCPContent{
					{Type: "text", Text: "simple text"},
				},
			},
			want: "simple text",
		},
		{
			name: "structured result",
			result: &MCPToolResult{
				StructuredResult: map[string]interface{}{
					"key": "value",
				},
			},
			want: map[string]interface{}{
				"key": "value",
			},
		},
		{
			name: "multiple contents",
			result: &MCPToolResult{
				Content: []MCPContent{
					{Type: "text", Text: "first"},
					{Type: "text", Text: "second"},
				},
			},
			want: map[string]interface{}{
				"contents": []map[string]interface{}{
					{"type": "text", "text": "first", "data": nil},
					{"type": "text", "text": "second", "data": nil},
				},
				"is_error": false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolset.convertMCPResult(tt.result)

			// Convert both to JSON for comparison
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tt.want)

			if string(gotJSON) != string(wantJSON) {
				t.Errorf("convertMCPResult() = %s, want %s", string(gotJSON), string(wantJSON))
			}
		})
	}
}

// BenchmarkMCPClientCallTool benchmarks tool calls
func BenchmarkMCPClientCallTool(b *testing.B) {
	ctx := context.Background()

	transport := newMockTransport()
	callResp := &JSONRPCResponse{
		JSONRPC: JSONRPCVersion,
		Result: map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{
					"type": "text",
					"text": "result",
				},
			},
		},
	}
	transport.setResponse(MethodCallTool, callResp)

	client := NewClient(transport)
	client.Connect(ctx)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		client.CallTool(ctx, "test_tool", map[string]interface{}{
			"param": "value",
		})
	}
}
