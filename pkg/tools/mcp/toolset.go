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
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/tools"
	"github.com/seyi/dagens/pkg/types"
)

// MCPToolset wraps an MCP server as a collection of types.ToolDefinition
// Compatible with ADK's MCPToolset while adding distributed execution support.
type MCPToolset struct {
	client         MCPClient
	tools          []*types.ToolDefinition
	params         ConnectionParams
	distributedCfg *DistributedMCPConfig
	connected      bool
	mu             sync.RWMutex

	// Distributed execution support
	partitionID string
	metrics     []ToolCallMetrics
	metricsMu   sync.Mutex
}

// NewMCPToolset creates a new MCP toolset from connection parameters
func NewMCPToolset(params ConnectionParams) (*MCPToolset, error) {
	if err := params.Validate(); err != nil {
		return nil, fmt.Errorf("invalid connection params: %w", err)
	}

	return &MCPToolset{
		params:         params,
		tools:          []*types.ToolDefinition{},
		distributedCfg: DefaultDistributedMCPConfig(),
		metrics:        make([]ToolCallMetrics, 0),
	}, nil
}

// WithDistributedConfig sets the distributed execution configuration
func (m *MCPToolset) WithDistributedConfig(cfg *DistributedMCPConfig) *MCPToolset {
	m.distributedCfg = cfg
	return m
}

// WithPartitionID sets the Spark partition ID for distributed execution
func (m *MCPToolset) WithPartitionID(partitionID string) *MCPToolset {
	m.partitionID = partitionID
	return m
}

// Connect establishes connection to the MCP server and discovers tools
func (m *MCPToolset) Connect(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.connected {
		return nil
	}

	// Create client
	client, err := NewClientFromParams(ctx, m.params)
	if err != nil {
		return fmt.Errorf("failed to create MCP client: %w", err)
	}

	m.client = client

	// Discover tools
	mcpTools, err := client.ListTools(ctx)
	if err != nil {
		client.Close()
		return fmt.Errorf("failed to list tools: %w", err)
	}

	// Convert MCP tools to our ToolDefinition format
	m.tools = make([]*types.ToolDefinition, 0, len(mcpTools))
	for _, mcpTool := range mcpTools {
		tool := m.wrapMCPTool(mcpTool)
		m.tools = append(m.tools, tool)
	}

	m.connected = true
	return nil
}

// Disconnect closes the MCP server connection
func (m *MCPToolset) Disconnect() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.connected {
		return nil
	}

	m.connected = false

	if m.client != nil {
		return m.client.Close()
	}

	return nil
}

// GetTools returns all discovered tools
func (m *MCPToolset) GetTools() []*types.ToolDefinition {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a copy to prevent external modification
	toolsCopy := make([]*types.ToolDefinition, len(m.tools))
	copy(toolsCopy, m.tools)
	return toolsCopy
}

// IsConnected returns true if connected to the MCP server
func (m *MCPToolset) IsConnected() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connected
}

// wrapMCPTool converts an MCP tool to our ToolDefinition format
func (m *MCPToolset) wrapMCPTool(mcpTool MCPToolInfo) *types.ToolDefinition {
	return &types.ToolDefinition{
		Name:        mcpTool.Name,
		Description: mcpTool.Description,
		Schema: &types.ToolSchema{
			InputSchema: mcpTool.InputSchema,
		},
		Handler: m.createToolHandler(mcpTool.Name),
		Enabled: true,
	}
}

// createToolHandler creates a handler function for an MCP tool
func (m *MCPToolset) createToolHandler(toolName string) types.ToolHandler {
	return func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
		// Check if connected
		m.mu.RLock()
		if !m.connected {
			m.mu.RUnlock()
			return nil, fmt.Errorf("MCP toolset not connected")
		}
		client := m.client
		m.mu.RUnlock()

		// Record start time
		startTime := time.Now()

		// Call MCP tool
		result, err := client.CallTool(ctx, toolName, params)

		// Record metrics if enabled
		if m.distributedCfg != nil && m.distributedCfg.EnableMetrics {
			transportType := "unknown"
			if m.params != nil {
				transportType = m.params.Type()
			}

			metric := ToolCallMetrics{
				ToolName:      toolName,
				DurationMs:    float64(time.Since(startTime).Milliseconds()),
				Success:       err == nil,
				PartitionID:   m.partitionID,
				TransportType: transportType,
			}

			if err != nil {
				metric.ErrorMessage = err.Error()
			}

			m.recordMetric(metric)
		}

		if err != nil {
			return nil, fmt.Errorf("MCP tool %s failed: %w", toolName, err)
		}

		// Convert MCP result to interface{}
		return m.convertMCPResult(result), nil
	}
}

// convertMCPResult converts MCPToolResult to a standard interface{} result
func (m *MCPToolset) convertMCPResult(result *MCPToolResult) interface{} {
	if result == nil {
		return nil
	}

	// If structured result is available, return it
	if result.StructuredResult != nil {
		return result.StructuredResult
	}

	// If there's a single text content, return it as string
	if len(result.Content) == 1 && result.Content[0].Type == "text" {
		return result.Content[0].Text
	}

	// Otherwise, return the full content array
	if len(result.Content) > 0 {
		// Convert to map for easier consumption
		contents := make([]map[string]interface{}, len(result.Content))
		for i, content := range result.Content {
			contents[i] = map[string]interface{}{
				"type": content.Type,
				"text": content.Text,
				"data": content.Data,
			}
		}
		return map[string]interface{}{
			"contents": contents,
			"is_error": result.IsError,
		}
	}

	return nil
}

// recordMetric records a tool call metric
func (m *MCPToolset) recordMetric(metric ToolCallMetrics) {
	m.metricsMu.Lock()
	defer m.metricsMu.Unlock()
	m.metrics = append(m.metrics, metric)
}

// GetMetrics returns all recorded metrics
func (m *MCPToolset) GetMetrics() []ToolCallMetrics {
	m.metricsMu.Lock()
	defer m.metricsMu.Unlock()

	// Return a copy
	metricsCopy := make([]ToolCallMetrics, len(m.metrics))
	copy(metricsCopy, m.metrics)
	return metricsCopy
}

// RegisterTools registers all MCP tools in the given tool registry
func (m *MCPToolset) RegisterTools(registry *tools.ToolRegistry) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.connected {
		return fmt.Errorf("toolset not connected - call Connect() first")
	}

	for _, tool := range m.tools {
		if err := registry.Register(tool); err != nil {
			return fmt.Errorf("failed to register tool %s: %w", tool.Name, err)
		}
	}

	return nil
}

// DistributedMCPToolset manages multiple MCP toolsets across partitions
type DistributedMCPToolset struct {
	toolsets map[string]*MCPToolset // partition ID -> toolset
	params   ConnectionParams
	config   *DistributedMCPConfig
	mu       sync.RWMutex
}

// NewDistributedMCPToolset creates a distributed MCP toolset manager
func NewDistributedMCPToolset(params ConnectionParams, config *DistributedMCPConfig) *DistributedMCPToolset {
	if config == nil {
		config = DefaultDistributedMCPConfig()
	}

	return &DistributedMCPToolset{
		toolsets: make(map[string]*MCPToolset),
		params:   params,
		config:   config,
	}
}

// GetOrCreateToolset returns a toolset for the given partition
func (d *DistributedMCPToolset) GetOrCreateToolset(ctx context.Context, partitionID string) (*MCPToolset, error) {
	d.mu.RLock()
	if toolset, exists := d.toolsets[partitionID]; exists {
		d.mu.RUnlock()
		return toolset, nil
	}
	d.mu.RUnlock()

	// Create new toolset for this partition
	d.mu.Lock()
	defer d.mu.Unlock()

	// Double-check after acquiring write lock
	if toolset, exists := d.toolsets[partitionID]; exists {
		return toolset, nil
	}

	toolset, err := NewMCPToolset(d.params)
	if err != nil {
		return nil, fmt.Errorf("failed to create toolset for partition %s: %w", partitionID, err)
	}

	toolset.WithDistributedConfig(d.config).WithPartitionID(partitionID)

	if err := toolset.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect toolset for partition %s: %w", partitionID, err)
	}

	d.toolsets[partitionID] = toolset
	return toolset, nil
}

// GetAggregatedMetrics returns metrics from all partitions
func (d *DistributedMCPToolset) GetAggregatedMetrics() []ToolCallMetrics {
	d.mu.RLock()
	defer d.mu.RUnlock()

	allMetrics := make([]ToolCallMetrics, 0)
	for _, toolset := range d.toolsets {
		allMetrics = append(allMetrics, toolset.GetMetrics()...)
	}

	return allMetrics
}

// Close closes all toolsets across all partitions
func (d *DistributedMCPToolset) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	var lastErr error
	for partitionID, toolset := range d.toolsets {
		if err := toolset.Disconnect(); err != nil {
			lastErr = fmt.Errorf("failed to close toolset for partition %s: %w", partitionID, err)
		}
	}

	d.toolsets = make(map[string]*MCPToolset)
	return lastErr
}
