package tools

import (
	"context"
	"fmt"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/events"
	"github.com/seyi/dagens/pkg/types"
)

// TransferToAgentTool creates a tool that allows an agent to transfer control
// to another agent.
func TransferToAgentTool() *types.ToolDefinition {
	return &types.ToolDefinition{
		Name:        "transfer_to_agent",
		Description: "Transfer execution to another agent by name. Use when the current agent is not the best suited to handle the request, and another specialized agent should take over.",
		Schema: &types.ToolSchema{
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"agent_name": map[string]interface{}{
						"type":        "string",
						"description": "The name of the agent to transfer to.",
					},
					"instruction": map[string]interface{}{
						"type":        "string",
						"description": "The instruction for the target agent.",
					},
				},
				"required": []string{"agent_name", "instruction"},
			},
		},
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			agentName, ok := params["agent_name"].(string)
			if !ok || agentName == "" {
				return nil, fmt.Errorf("agent_name parameter is required")
			}
			instruction, _ := params["instruction"].(string)

			// Publish event if event bus is available
			if eventBus, ok := params["__event_bus__"].(events.EventBus); ok {
				invocationCtx, _ := params["__invocation_context__"].(*agent.InvocationContext)
				fromAgent := ""
				fromPartition := ""
				if invocationCtx != nil && invocationCtx.CurrentAgent != nil {
					fromAgent = invocationCtx.CurrentAgent.Name()
					fromPartition = invocationCtx.CurrentAgent.Partition()
				}

				event := events.NewTransferRequestEvent(fromAgent, agentName, fmt.Sprintf("%v", params["reason"]), fromPartition)
				eventBus.Publish(event)
			}

			// The actual transfer is handled by the agent runtime, which will
			// interpret this special return value.
			return map[string]interface{}{
				"transfer_requested": true,
				"target_agent":       agentName,
				"instruction":        instruction,
				"reason":             params["reason"],
			}, nil
		},
		Enabled: true,
	}
}

// GetAvailableAgentsTool creates a tool that lists the available agents in the
// current hierarchy.
func GetAvailableAgentsTool() *types.ToolDefinition {
	return &types.ToolDefinition{
		Name:        "get_available_agents",
		Description: "Get a list of available agents that can be used for transfer.",
		Schema:      &types.ToolSchema{},
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			// This tool requires access to the current agent's context to find
			// its siblings or sub-agents.
			invocationCtx, ok := params["__invocation_context__"].(*agent.InvocationContext)
			if !ok || invocationCtx == nil {
				return map[string]interface{}{"agents": []map[string]interface{}{}, "count": 0}, nil
			}

			currentAgent := invocationCtx.CurrentAgent
			if currentAgent == nil {
				return map[string]interface{}{"agents": []map[string]interface{}{}, "count": 0}, nil
			}

			// In this implementation, we'll return the sub-agents of the current agent.
			baseAgent, ok := currentAgent.(*agent.BaseAgent)
			if !ok {
				return map[string]interface{}{"agents": []map[string]interface{}{}, "count": 0}, nil
			}
			subAgents := baseAgent.SubAgents()
			agentInfos := make([]map[string]interface{}, len(subAgents))
			for i, subAgent := range subAgents {
				agentInfos[i] = map[string]interface{}{
					"name":        subAgent.Name(),
					"description": subAgent.Description(),
				}
			}

			return map[string]interface{}{
				"agents": agentInfos,
				"count":  len(agentInfos),
			}, nil
		},
		Enabled: true,
	}
}

// ValidateTransferScopeTool creates a tool that can be used to validate if a
// transfer to another agent is allowed within the current scope.
func ValidateTransferScopeTool() *types.ToolDefinition {
	return &types.ToolDefinition{
		Name:        "validate_transfer_scope",
		Description: "Validate if a transfer to another agent is allowed.",
		Schema: &types.ToolSchema{
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			agentName, ok := params["agent_name"].(string)
			if !ok || agentName == "" {
				return map[string]interface{}{"valid": false, "reason": "agent_name parameter is required"}, nil
			}

			invocationCtx, ok := params["__invocation_context__"].(*agent.InvocationContext)
			if !ok || invocationCtx == nil {
				return map[string]interface{}{"valid": false, "reason": "invocation context not found"}, nil
			}

			currentAgent := invocationCtx.CurrentAgent
			if currentAgent == nil {
				return map[string]interface{}{"valid": false, "reason": "current agent not found in context"}, nil
			}

			baseAgent, ok := currentAgent.(*agent.BaseAgent)
			if !ok {
				return map[string]interface{}{"valid": false, "reason": "current agent is not a valid agent type for transfers"}, nil
			}

			// Check if the agent exists as a sub-agent.
			_, err := baseAgent.FindAgent(agentName)
			isValid := err == nil

			return map[string]interface{}{
				"valid": isValid,
			}, nil
		},
		Enabled: true,
	}
}

// RegisterTransferTools registers all transfer-related tools in the registry.
// The rootAgent parameter is optional and reserved for future use.
func RegisterTransferTools(registry *ToolRegistry, rootAgent ...agent.Agent) error {
	tools := []*types.ToolDefinition{
		TransferToAgentTool(),
		GetAvailableAgentsTool(),
		ValidateTransferScopeTool(),
	}

	for _, tool := range tools {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	return nil
}