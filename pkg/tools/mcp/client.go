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
	"sync/atomic"
	"time"
)

// client implements the MCPClient interface
type client struct {
	transport  Transport
	connected  bool
	serverInfo *MCPServerInfo
	mu         sync.RWMutex
	requestID  int32
	metrics    []ToolCallMetrics
}

// NewClient creates a new MCP client with the given transport
func NewClient(transport Transport) MCPClient {
	return &client{
		transport: transport,
		metrics:   make([]ToolCallMetrics, 0),
	}
}

// NewClientFromParams creates a new MCP client from connection parameters
func NewClientFromParams(ctx context.Context, params ConnectionParams) (MCPClient, error) {
	if err := params.Validate(); err != nil {
		return nil, fmt.Errorf("invalid connection params: %w", err)
	}

	var transport Transport
	var err error

	switch params.Type() {
	case "stdio":
		stdioParams := params.(StdioConnectionParams)
		transport, err = NewStdioTransport(stdioParams)
	case "http":
		httpParams := params.(HTTPConnectionParams)
		transport, err = NewHTTPTransport(httpParams)
	default:
		return nil, fmt.Errorf("unsupported connection type: %s", params.Type())
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create transport: %w", err)
	}

	client := NewClient(transport)
	if err := client.Connect(ctx); err != nil {
		transport.Close()
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	return client, nil
}

// Connect establishes connection to the MCP server
func (c *client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return nil
	}

	// Test connection with ping
	req := &JSONRPCRequest{
		JSONRPC: JSONRPCVersion,
		ID:      int(atomic.AddInt32(&c.requestID, 1)),
		Method:  MethodPing,
	}

	resp, err := c.transport.Send(ctx, req)
	if err != nil {
		return fmt.Errorf("connection test failed: %w", err)
	}

	if resp.Error != nil {
		// Ping not supported is OK - some servers don't implement it
		if resp.Error.Code != ErrorCodeMethodNotFound {
			return fmt.Errorf("connection test failed: %w", resp.Error)
		}
	}

	c.connected = true
	return nil
}

// ListTools retrieves all available tools from the server
func (c *client) ListTools(ctx context.Context) ([]MCPToolInfo, error) {
	c.mu.RLock()
	if !c.connected {
		c.mu.RUnlock()
		return nil, fmt.Errorf("client not connected")
	}
	c.mu.RUnlock()

	req := &JSONRPCRequest{
		JSONRPC: JSONRPCVersion,
		ID:      int(atomic.AddInt32(&c.requestID, 1)),
		Method:  MethodListTools,
	}

	resp, err := c.transport.Send(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("server returned error: %w", resp.Error)
	}

	// Parse tools from result
	toolsData, ok := resp.Result["tools"]
	if !ok {
		return nil, fmt.Errorf("response missing 'tools' field")
	}

	// Convert to JSON and back to get proper types
	toolsJSON, err := json.Marshal(toolsData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tools: %w", err)
	}

	var tools []MCPToolInfo
	if err := json.Unmarshal(toolsJSON, &tools); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tools: %w", err)
	}

	return tools, nil
}

// CallTool invokes a tool with the given arguments
func (c *client) CallTool(ctx context.Context, name string, args map[string]interface{}) (*MCPToolResult, error) {
	c.mu.RLock()
	if !c.connected {
		c.mu.RUnlock()
		return nil, fmt.Errorf("client not connected")
	}
	c.mu.RUnlock()

	startTime := time.Now()

	params := map[string]interface{}{
		"name":      name,
		"arguments": args,
	}

	req := &JSONRPCRequest{
		JSONRPC: JSONRPCVersion,
		ID:      int(atomic.AddInt32(&c.requestID, 1)),
		Method:  MethodCallTool,
		Params:  params,
	}

	// Marshal request for metrics
	reqJSON, _ := json.Marshal(req)
	requestSize := len(reqJSON)

	resp, err := c.transport.Send(ctx, req)

	// Record metrics
	metric := ToolCallMetrics{
		ToolName:      name,
		DurationMs:    float64(time.Since(startTime).Milliseconds()),
		RequestSize:   requestSize,
		Success:       err == nil && (resp == nil || resp.Error == nil),
		TransportType: c.getTransportType(),
	}

	if c.serverInfo != nil {
		metric.ServerName = c.serverInfo.Name
		metric.ServerVersion = c.serverInfo.Version
	}

	if err != nil {
		metric.ErrorMessage = err.Error()
		c.recordMetric(metric)
		return nil, fmt.Errorf("failed to call tool: %w", err)
	}

	// Marshal response for metrics
	respJSON, _ := json.Marshal(resp)
	metric.ResponseSize = len(respJSON)

	if resp.Error != nil {
		metric.ErrorMessage = resp.Error.Error()
		c.recordMetric(metric)
		return nil, fmt.Errorf("tool call failed: %w", resp.Error)
	}

	c.recordMetric(metric)

	// Parse tool result
	result, err := ParseToolResult(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to parse tool result: %w", err)
	}

	return result, nil
}

// GetServerInfo returns information about the MCP server
func (c *client) GetServerInfo(ctx context.Context) (*MCPServerInfo, error) {
	c.mu.RLock()
	if c.serverInfo != nil {
		defer c.mu.RUnlock()
		return c.serverInfo, nil
	}
	c.mu.RUnlock()

	// Try to get server info (not all servers support this)
	req := &JSONRPCRequest{
		JSONRPC: JSONRPCVersion,
		ID:      int(atomic.AddInt32(&c.requestID, 1)),
		Method:  "server/info",
	}

	resp, err := c.transport.Send(ctx, req)
	if err != nil {
		// Return minimal info if server doesn't support this method
		return &MCPServerInfo{
			Name:            "unknown",
			Version:         "unknown",
			ProtocolVersion: "1.0",
		}, nil
	}

	if resp.Error != nil {
		// Return minimal info if method not found
		if resp.Error.Code == ErrorCodeMethodNotFound {
			return &MCPServerInfo{
				Name:            "unknown",
				Version:         "unknown",
				ProtocolVersion: "1.0",
			}, nil
		}
		return nil, fmt.Errorf("failed to get server info: %w", resp.Error)
	}

	// Parse server info
	infoJSON, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal server info: %w", err)
	}

	var info MCPServerInfo
	if err := json.Unmarshal(infoJSON, &info); err != nil {
		return nil, fmt.Errorf("failed to unmarshal server info: %w", err)
	}

	c.mu.Lock()
	c.serverInfo = &info
	c.mu.Unlock()

	return &info, nil
}

// Close closes the client connection
func (c *client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return nil
	}

	c.connected = false
	return c.transport.Close()
}

// IsConnected returns true if the client is connected
func (c *client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// getTransportType returns the transport type
func (c *client) getTransportType() string {
	switch c.transport.(type) {
	case *stdioTransport:
		return "stdio"
	case *httpTransport:
		return "http"
	default:
		return "unknown"
	}
}

// recordMetric records a tool call metric
func (c *client) recordMetric(metric ToolCallMetrics) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metrics = append(c.metrics, metric)
}

// GetMetrics returns all recorded metrics
func (c *client) GetMetrics() []ToolCallMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Return a copy to prevent concurrent modification
	metrics := make([]ToolCallMetrics, len(c.metrics))
	copy(metrics, c.metrics)
	return metrics
}
