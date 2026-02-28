// Package tools provides composable tool implementations for agents
// Inspired by the tool-based architecture in Zen MCP and ADK
package tools

import (
	"context"
	"fmt"

	"github.com/seyi/dagens/pkg/types"
)

// ToolRegistry manages the registration and retrieval of tools that can be used
// by agents.
type ToolRegistry struct {
	tools map[string]*types.ToolDefinition
}

// NewToolRegistry creates and initializes a new ToolRegistry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]*types.ToolDefinition),
	}
}

// Register adds a tool to the registry. It returns an error if a tool with
// the same name is already registered.
func (tr *ToolRegistry) Register(tool *types.ToolDefinition) error {
	if _, exists := tr.tools[tool.Name]; exists {
		return fmt.Errorf("tool %s already registered", tool.Name)
	}

	tr.tools[tool.Name] = tool
	return nil
}

// Get retrieves a tool by its name. It returns an error if the tool is not
// found or is not enabled.
func (tr *ToolRegistry) Get(name string) (*types.ToolDefinition, error) {
	tool, exists := tr.tools[name]
	if !exists {
		return nil, fmt.Errorf("tool %s not found", name)
	}

	if !tool.Enabled {
		return nil, fmt.Errorf("tool %s is disabled", name)
	}

	return tool, nil
}

// List returns a slice of all enabled tools in the registry.
func (tr *ToolRegistry) List() []*types.ToolDefinition {
	tools := make([]*types.ToolDefinition, 0)
	for _, tool := range tr.tools {
		if tool.Enabled {
			tools = append(tools, tool)
		}
	}
	return tools
}

// Execute finds a tool by name and executes its handler with the provided
// parameters.
func (tr *ToolRegistry) Execute(ctx context.Context, name string, params map[string]interface{}) (interface{}, error) {
	tool, err := tr.Get(name)
	if err != nil {
		return nil, err
	}

	return tool.Handler(ctx, params)
}

// Built-in tools inspired by Zen MCP

// SearchTool performs distributed search across agent knowledge
func SearchTool() *types.ToolDefinition {
	return &types.ToolDefinition{
		Name:        "search",
		Description: "Search across distributed agent knowledge base",
		Schema: &types.ToolSchema{
			InputSchema: map[string]interface{}{
				"query": "string",
				"limit": "integer",
			},
		},
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			query, ok := params["query"].(string)
			if !ok {
				return nil, fmt.Errorf("query parameter required")
			}

			// In a real implementation, this would search distributed state
			return map[string]interface{}{
				"query":   query,
				"results": []interface{}{},
			}, nil
		},
		Enabled: true,
	}
}

// ConsensusToolReaches consensus across multiple agents
func ConsensusTool() *types.ToolDefinition {
	return &types.ToolDefinition{
		Name:        "consensus",
		Description: "Reach consensus across multiple distributed agents",
		Schema: &types.ToolSchema{
			InputSchema: map[string]interface{}{
				"agents":   "array",
				"question": "string",
			},
		},
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			question, ok := params["question"].(string)
			if !ok {
				return nil, fmt.Errorf("question parameter required")
			}

			// In a real implementation, this would coordinate multiple agents
			return map[string]interface{}{
				"question":  question,
				"consensus": "pending",
				"votes":     []interface{}{},
			}, nil
		},
		Enabled: true,
	}
}

// PlannerTool breaks down complex tasks
func PlannerTool() *types.ToolDefinition {
	return &types.ToolDefinition{
		Name:        "planner",
		Description: "Break down complex projects into structured plans",
		Schema: &types.ToolSchema{
			InputSchema: map[string]interface{}{
				"task":        "string",
				"constraints": "object",
			},
		},
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			task, ok := params["task"].(string)
			if !ok {
				return nil, fmt.Errorf("task parameter required")
			}

			// In a real implementation, this would use AI planning
			return map[string]interface{}{
				"task":  task,
				"steps": []interface{}{},
			}, nil
		},
		Enabled: true,
	}
}

// RegisterBuiltinTools registers all built-in tools
func RegisterBuiltinTools(registry *ToolRegistry) error {
	tools := []*types.ToolDefinition{
		SearchTool(),
		ConsensusTool(),
		PlannerTool(),
	}

	for _, tool := range tools {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}

	return nil
}
