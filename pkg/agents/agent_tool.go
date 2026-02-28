package agents

import (
	"context"
	"fmt"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/tools"
	"github.com/seyi/dagens/pkg/types"
)

// AgentTool wraps an agent as a tool that can be called by other agents
// This enables ADK-style agent delegation and hierarchical calling
type AgentTool struct {
	agent       agent.Agent
	description string
	schema      map[string]interface{}
}

// NewAgentTool creates a tool wrapper around an agent
func NewAgentTool(ag agent.Agent) *AgentTool {
	return &AgentTool{
		agent:       ag,
		description: ag.Description(),
		schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instruction": map[string]interface{}{
					"type":        "string",
					"description": "The instruction or task for the agent",
				},
				"context": map[string]interface{}{
					"type":        "object",
					"description": "Additional context for the agent",
				},
			},
			"required": []string{"instruction"},
		},
	}
}

// AsTool converts the AgentTool to a types.ToolDefinition
func (at *AgentTool) AsTool() *types.ToolDefinition {
	return &types.ToolDefinition{
		Name:        fmt.Sprintf("call_%s", at.agent.Name()),
		Description: fmt.Sprintf("Delegate task to %s agent: %s", at.agent.Name(), at.description),
		Schema: &types.ToolSchema{
			InputSchema: at.schema,
		},
		Handler: at.execute,
		Enabled: true,
	}
}

// execute is the tool handler that invokes the wrapped agent
func (at *AgentTool) execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// Extract instruction from params
	instruction, ok := params["instruction"].(string)
	if !ok {
		return nil, fmt.Errorf("missing or invalid 'instruction' parameter")
	}

	// Extract optional context
	agentContext, _ := params["context"].(map[string]interface{})
	if agentContext == nil {
		agentContext = make(map[string]interface{})
	}

	// Create agent input
	input := &agent.AgentInput{
		Instruction: instruction,
		Context:     agentContext,
	}

	// Execute the agent
	output, err := at.agent.Execute(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("agent %s execution failed: %w", at.agent.Name(), err)
	}

	return output.Result, nil
}

// WrapAgentAsTool converts an agent into a callable tool
// Convenience function for quick agent-to-tool conversion
func WrapAgentAsTool(ag agent.Agent) *types.ToolDefinition {
	agentTool := NewAgentTool(ag)
	return agentTool.AsTool()
}

// WrapSubAgentsAsTools wraps all sub-agents of a parent as tools
// Returns a tool registry with all sub-agents registered
func WrapSubAgentsAsTools(parent *agent.BaseAgent) *tools.ToolRegistry {
	registry := tools.NewToolRegistry()

	subAgents := parent.SubAgents()
	for _, subAgent := range subAgents {
		tool := WrapAgentAsTool(subAgent)
		registry.Register(tool)
	}

	return registry
}

// CreateTransferTool creates a special "transfer_to_agent" tool
// This enables LLM-driven delegation by agent name
func CreateTransferTool(parent *agent.BaseAgent) *types.ToolDefinition {
	return &types.ToolDefinition{
		Name:        "transfer_to_agent",
		Description: "Transfer control to another agent by name. Use this to delegate tasks to specialist agents.",
		Schema: &types.ToolSchema{
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"agent_name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the agent to transfer to",
					},
					"instruction": map[string]interface{}{
						"type":        "string",
						"description": "Task or instruction for the target agent",
					},
					"context": map[string]interface{}{
						"type":        "object",
						"description": "Additional context to pass to the agent",
					},
				},
				"required": []string{"agent_name", "instruction"},
			},
		},
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			// Extract agent name
			agentName, ok := params["agent_name"].(string)
			if !ok {
				return nil, fmt.Errorf("missing or invalid 'agent_name' parameter")
			}

			// Find the agent
			targetAgent, err := parent.FindAgent(agentName)
			if err != nil {
				return nil, fmt.Errorf("agent not found: %w", err)
			}

			// Extract instruction
			instruction, ok := params["instruction"].(string)
			if !ok {
				return nil, fmt.Errorf("missing or invalid 'instruction' parameter")
			}

			// Extract optional context
			agentContext, _ := params["context"].(map[string]interface{})
			if agentContext == nil {
				agentContext = make(map[string]interface{})
			}

			// Execute the target agent
			input := &agent.AgentInput{
				Instruction: instruction,
				Context:     agentContext,
			}

			output, err := targetAgent.Execute(ctx, input)
			if err != nil {
				return nil, fmt.Errorf("transfer to %s failed: %w", agentName, err)
			}

			return output.Result, nil
		},
		Enabled: true,
	}
}

// CreateHierarchicalToolRegistry creates a tool registry for a parent agent
// that includes all sub-agents as callable tools AND the transfer_to_agent tool
func CreateHierarchicalToolRegistry(parent *agent.BaseAgent, additionalTools ...*types.ToolDefinition) *tools.ToolRegistry {
	registry := tools.NewToolRegistry()

	// Add transfer tool for dynamic delegation
	transferTool := CreateTransferTool(parent)
	registry.Register(transferTool)

	// Add each sub-agent as a tool
	subAgents := parent.SubAgents()
	for _, subAgent := range subAgents {
		tool := WrapAgentAsTool(subAgent)
		registry.Register(tool)
	}

	// Add any additional tools
	for _, tool := range additionalTools {
		registry.Register(tool)
	}

	return registry
}

// AgentToolBuilder provides fluent API for building agent tools
type AgentToolBuilder struct {
	agent       agent.Agent
	name        string
	description string
	schema      map[string]interface{}
	preProcess  func(params map[string]interface{}) (*agent.AgentInput, error)
	postProcess func(output *agent.AgentOutput) (interface{}, error)
}

// NewAgentToolBuilder creates a builder for customizing agent tools
func NewAgentToolBuilder(ag agent.Agent) *AgentToolBuilder {
	return &AgentToolBuilder{
		agent:       ag,
		description: ag.Description(),
		schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"instruction": map[string]interface{}{
					"type":        "string",
					"description": "The instruction for the agent",
				},
			},
			"required": []string{"instruction"},
		},
	}
}

// WithName sets a custom tool name
func (b *AgentToolBuilder) WithName(name string) *AgentToolBuilder {
	b.name = name
	return b
}

// WithDescription sets a custom description
func (b *AgentToolBuilder) WithDescription(desc string) *AgentToolBuilder {
	b.description = desc
	return b
}

// WithSchema sets a custom parameter schema
func (b *AgentToolBuilder) WithSchema(schema map[string]interface{}) *AgentToolBuilder {
	b.schema = schema
	return b
}

// WithPreProcess sets a function to transform tool params into agent input
func (b *AgentToolBuilder) WithPreProcess(fn func(params map[string]interface{}) (*agent.AgentInput, error)) *AgentToolBuilder {
	b.preProcess = fn
	return b
}

// WithPostProcess sets a function to transform agent output into tool result
func (b *AgentToolBuilder) WithPostProcess(fn func(output *agent.AgentOutput) (interface{}, error)) *AgentToolBuilder {
	b.postProcess = fn
	return b
}

// Build creates the tool
func (b *AgentToolBuilder) Build() *types.ToolDefinition {
	name := b.name
	if name == "" {
		name = fmt.Sprintf("call_%s", b.agent.Name())
	}

	return &types.ToolDefinition{
		Name:        name,
		Description: b.description,
		Schema: &types.ToolSchema{
			InputSchema: b.schema,
		},
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			// Pre-process
			var input *agent.AgentInput
			var err error

			if b.preProcess != nil {
				input, err = b.preProcess(params)
			} else {
				// Default: extract instruction and context
				instruction, ok := params["instruction"].(string)
				if !ok {
					return nil, fmt.Errorf("missing instruction parameter")
				}

				agentContext, _ := params["context"].(map[string]interface{})
				if agentContext == nil {
					agentContext = make(map[string]interface{})
				}

				input = &agent.AgentInput{
					Instruction: instruction,
					Context:     agentContext,
				}
			}

			if err != nil {
				return nil, fmt.Errorf("pre-processing failed: %w", err)
			}

			// Execute agent
			output, err := b.agent.Execute(ctx, input)
			if err != nil {
				return nil, fmt.Errorf("agent execution failed: %w", err)
			}

			// Post-process
			if b.postProcess != nil {
				return b.postProcess(output)
			}

			return output.Result, nil
		},
		Enabled: true,
	}
}

// GetAvailableAgents returns descriptions of all agents in a hierarchy
// Useful for LLM context about available agents
func GetAvailableAgents(parent *agent.BaseAgent) []map[string]interface{} {
	subAgents := parent.SubAgents()
	agents := make([]map[string]interface{}, len(subAgents))

	for i, subAgent := range subAgents {
		agents[i] = map[string]interface{}{
			"name":         subAgent.Name(),
			"description":  subAgent.Description(),
			"capabilities": subAgent.Capabilities(),
		}
	}

	return agents
}

// FormatAgentsForPrompt creates a formatted string describing available agents
// Can be included in LLM system prompts
func FormatAgentsForPrompt(parent *agent.BaseAgent) string {
	subAgents := parent.SubAgents()
	if len(subAgents) == 0 {
		return "No sub-agents available."
	}

	prompt := "Available agents you can delegate to:\n\n"
	for _, subAgent := range subAgents {
		prompt += fmt.Sprintf("- **%s**: %s\n", subAgent.Name(), subAgent.Description())
		if caps := subAgent.Capabilities(); len(caps) > 0 {
			prompt += fmt.Sprintf("  Capabilities: %v\n", caps)
		}
	}

	prompt += "\nUse the transfer_to_agent tool to delegate tasks to these agents."
	return prompt
}
