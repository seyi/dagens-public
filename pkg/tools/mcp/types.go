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

// Package mcp provides Model Context Protocol (MCP) support for third-party tools.
//
// MCP is a standardized protocol that enables AI applications to integrate external
// tools and data sources. This implementation supports:
// - JSON-RPC 2.0 protocol
// - Stdio transport (subprocess communication)
// - HTTP transport (remote server communication)
// - Dynamic tool discovery
// - Distributed execution via Spark partitions
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	// JSONRPCVersion is the supported JSON-RPC protocol version
	JSONRPCVersion = "2.0"

	// MCP protocol methods
	MethodListTools = "tools/list"
	MethodCallTool  = "tools/call"
	MethodPing      = "ping"
)

// JSONRPCRequest represents a JSON-RPC 2.0 request
type JSONRPCRequest struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      int                    `json:"id"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params,omitempty"`
}

// JSONRPCResponse represents a JSON-RPC 2.0 response
type JSONRPCResponse struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      int                    `json:"id"`
	Result  map[string]interface{} `json:"result,omitempty"`
	Error   *JSONRPCError          `json:"error,omitempty"`
}

// JSONRPCError represents a JSON-RPC 2.0 error
type JSONRPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Error implements the error interface
func (e *JSONRPCError) Error() string {
	if e.Data != nil {
		return fmt.Sprintf("JSON-RPC error %d: %s (data: %v)", e.Code, e.Message, e.Data)
	}
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

// Standard JSON-RPC error codes
const (
	ErrorCodeParseError     = -32700
	ErrorCodeInvalidRequest = -32600
	ErrorCodeMethodNotFound = -32601
	ErrorCodeInvalidParams  = -32602
	ErrorCodeInternalError  = -32603
)

// MCPToolInfo describes a tool exposed by an MCP server
type MCPToolInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

// MCPToolResult represents the result of calling an MCP tool
type MCPToolResult struct {
	Content          []MCPContent `json:"content"`
	IsError          bool         `json:"isError,omitempty"`
	StructuredResult interface{}  `json:"structuredResult,omitempty"`
}

// MCPContent represents content returned by an MCP tool
type MCPContent struct {
	Type string      `json:"type"` // "text", "image", "resource"
	Text string      `json:"text,omitempty"`
	Data interface{} `json:"data,omitempty"`
}

// MCPServerInfo describes an MCP server
type MCPServerInfo struct {
	Name            string                 `json:"name"`
	Version         string                 `json:"version"`
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities,omitempty"`
}

// Transport defines the interface for MCP communication transports
type Transport interface {
	// Send sends a request and returns the response
	Send(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error)

	// Close closes the transport
	Close() error

	// IsConnected returns true if the transport is connected
	IsConnected() bool
}

// MCPClient defines the interface for MCP protocol clients
type MCPClient interface {
	// Connect establishes connection to the MCP server
	Connect(ctx context.Context) error

	// ListTools retrieves all available tools from the server
	ListTools(ctx context.Context) ([]MCPToolInfo, error)

	// CallTool invokes a tool with the given arguments
	CallTool(ctx context.Context, name string, args map[string]interface{}) (*MCPToolResult, error)

	// GetServerInfo returns information about the MCP server
	GetServerInfo(ctx context.Context) (*MCPServerInfo, error)

	// Close closes the client connection
	Close() error

	// IsConnected returns true if the client is connected
	IsConnected() bool
}

// ConnectionParams defines connection parameters for MCP servers
type ConnectionParams interface {
	// Type returns the connection type ("stdio" or "http")
	Type() string

	// Validate validates the connection parameters
	Validate() error
}

// StdioConnectionParams configures stdio transport to subprocess
type StdioConnectionParams struct {
	Command string            // Command to execute (e.g., "npx")
	Args    []string          // Command arguments (e.g., ["-y", "agentql-mcp"])
	Env     map[string]string // Environment variables (e.g., {"AGENTQL_API_KEY": "..."})
	Timeout int               // Timeout in seconds (default: 300)
}

// Type returns the connection type
func (p StdioConnectionParams) Type() string {
	return "stdio"
}

// Validate validates the parameters
func (p StdioConnectionParams) Validate() error {
	if p.Command == "" {
		return fmt.Errorf("command is required")
	}
	if p.Timeout < 0 {
		return fmt.Errorf("timeout must be non-negative")
	}
	return nil
}

// HTTPConnectionParams configures HTTP transport to remote server
type HTTPConnectionParams struct {
	URL         string            // Server URL (e.g., "http://localhost:3000/mcp")
	Headers     map[string]string // HTTP headers (e.g., {"Authorization": "Bearer ..."})
	Timeout     int               // Timeout in seconds (default: 300)
	SessionID   string            // Optional session ID for stateful connections
	EnableCORS  bool              // Enable CORS support
	TLSInsecure bool              // Skip TLS verification (use with caution)
}

// Type returns the connection type
func (p HTTPConnectionParams) Type() string {
	return "http"
}

// Validate validates the parameters
func (p HTTPConnectionParams) Validate() error {
	if p.URL == "" {
		return fmt.Errorf("URL is required")
	}
	if p.Timeout < 0 {
		return fmt.Errorf("timeout must be non-negative")
	}
	return nil
}

// ToolCallMetrics tracks metrics for MCP tool calls
type ToolCallMetrics struct {
	ToolName       string  `json:"tool_name"`
	DurationMs     float64 `json:"duration_ms"`
	RequestSize    int     `json:"request_size"`
	ResponseSize   int     `json:"response_size"`
	Success        bool    `json:"success"`
	ErrorMessage   string  `json:"error_message,omitempty"`
	PartitionID    string  `json:"partition_id,omitempty"` // Spark partition for distributed execution
	TransportType  string  `json:"transport_type"`
	ServerName     string  `json:"server_name,omitempty"`
	ServerVersion  string  `json:"server_version,omitempty"`
}

// ParseToolResult parses an MCP tool result from JSON-RPC response
func ParseToolResult(result map[string]interface{}) (*MCPToolResult, error) {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	var toolResult MCPToolResult
	if err := json.Unmarshal(resultJSON, &toolResult); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tool result: %w", err)
	}

	return &toolResult, nil
}

// ExtractTextFromResult extracts text content from MCP tool result
func ExtractTextFromResult(result *MCPToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}

	var text string
	for _, content := range result.Content {
		if content.Type == "text" && content.Text != "" {
			text += content.Text + "\n"
		}
	}

	return text
}

// DistributedMCPConfig configures distributed MCP tool execution
type DistributedMCPConfig struct {
	// PartitionStrategy determines how tool calls are distributed
	// Options: "round-robin", "least-loaded", "sticky-session", "broadcast"
	PartitionStrategy string

	// MaxConcurrentCalls limits concurrent tool calls per partition
	MaxConcurrentCalls int

	// EnableCaching enables result caching for idempotent tool calls
	EnableCaching bool

	// CacheTTLSeconds is the cache time-to-live in seconds
	CacheTTLSeconds int

	// EnableMetrics enables detailed metrics collection
	EnableMetrics bool
}

// DefaultDistributedMCPConfig returns default distributed MCP configuration
func DefaultDistributedMCPConfig() *DistributedMCPConfig {
	return &DistributedMCPConfig{
		PartitionStrategy:  "round-robin",
		MaxConcurrentCalls: 10,
		EnableCaching:      false,
		CacheTTLSeconds:    300,
		EnableMetrics:      true,
	}
}
