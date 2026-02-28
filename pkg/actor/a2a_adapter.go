// Package actor provides integration with the A2A (Agent-to-Agent) protocol
// This adapter allows the actor system to work with the existing A2A infrastructure
package actor

import (
	"context"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/a2a"
	"github.com/seyi/dagens/pkg/agent"
)

// A2AActorAdapter adapts the A2A protocol to work with the actor system
type A2AActorAdapter struct {
	actorSystem *System
	client      a2a.A2AClient
}

// NewA2AActorAdapter creates a new adapter between A2A protocol and actor system
func NewA2AActorAdapter(actorSystem *System, client a2a.A2AClient) *A2AActorAdapter {
	return &A2AActorAdapter{
		actorSystem: actorSystem,
		client:      client,
	}
}

// InvokeActor invokes an actor through the actor system, with fallback to A2A if needed
func (a *A2AActorAdapter) InvokeActor(ctx context.Context, agentID string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// First, check if the agent exists as an internal actor
	actorAddr := Address(agentID)
	
	if _, exists := a.actorSystem.GetActor(actorAddr); exists {
		// Agent exists as internal actor, use actor system for communication
		return a.invokeInternalActor(ctx, actorAddr, input)
	}
	
	// Agent doesn't exist as internal actor, use A2A protocol
	return a.invokeExternalAgent(ctx, agentID, input)
}

// invokeInternalActor invokes an agent that exists as an internal actor
func (a *A2AActorAdapter) invokeInternalActor(ctx context.Context, actorAddr Address, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// Convert agent input to actor message
	invokeMsg := Message{
		Type: MessageTypeInvoke,
		Payload: InvokeMessage{
			AgentID:     string(actorAddr),
			Instruction: input.Instruction,
			Context:     input.Context,
			Timeout:     input.Timeout,
			TaskID:      input.TaskID,
		},
	}
	
	// Send message and wait for response
	response, err := a.actorSystem.Request(actorAddr, invokeMsg, input.Timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke internal actor: %w", err)
	}
	
	// Convert response back to agent output
	switch response.Type {
	case MessageTypeResponse:
		if respMsg, ok := response.Payload.(ResponseMessage); ok {
			return &agent.AgentOutput{
				TaskID: respMsg.TaskID,
				Result: respMsg.Result,
			}, nil
		}
	case MessageTypeError:
		if errorMsg, ok := response.Payload.(ErrorMessage); ok {
			return nil, fmt.Errorf("actor error: %s", errorMsg.Error)
		}
	}
	
	return nil, fmt.Errorf("unexpected response type: %s", response.Type)
}

// invokeExternalAgent invokes an agent through the A2A protocol
func (a *A2AActorAdapter) invokeExternalAgent(ctx context.Context, agentID string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	if a.client == nil {
		return nil, fmt.Errorf("A2A client not configured for external agent invocation")
	}
	
	return a.client.InvokeAgent(ctx, agentID, input)
}

// ActorAgentAdapter wraps an agent.Agent to make it compatible with the actor system
type ActorAgentAdapter struct {
	agent.Agent
}

// NewActorAgentAdapter creates a new adapter that makes an agent.Agent compatible with the actor system
func NewActorAgentAdapter(agent agent.Agent) *ActorAgentAdapter {
	return &ActorAgentAdapter{
		Agent: agent,
	}
}

// Receive implements the Actor interface, allowing agent.Agent to work with the actor system
func (a *ActorAgentAdapter) Receive(ctx Context) {
	switch ctx.Message.Type {
	case MessageTypeInvoke:
		if invokeMsg, ok := ctx.Message.Payload.(InvokeMessage); ok {
			// Convert actor message to agent input
			input := &agent.AgentInput{
				Instruction: invokeMsg.Instruction,
				Context:     invokeMsg.Context,
				TaskID:      invokeMsg.TaskID,
				Timeout:     invokeMsg.Timeout,
			}
			
			// Execute the agent
			output, err := a.Agent.Execute(ctx.System.ctx, input)
			
			// Create response message
			var response Message
			if err != nil {
				response = Message{
					Type: MessageTypeError,
					Payload: ErrorMessage{
						Error:   err.Error(),
						Details: nil,
					},
				}
			} else {
				response = Message{
					Type: MessageTypeResponse,
					Payload: ResponseMessage{
						TaskID: output.TaskID,
						Result: output.Result,
						Error:  nil,
					},
				}
			}
			
			// Send response back if there's a reply channel
			if ctx.Message.ReplyTo != nil {
				ctx.Message.ReplyTo <- response
			}
		}
	case MessageTypePing:
		// Simple health check
		if ctx.Message.ReplyTo != nil {
			response := Message{
				Type:    MessageTypePong,
				Payload: PongMessage{ID: string(ctx.Self), Timestamp: time.Now()},
			}
			ctx.Message.ReplyTo <- response
		}
	}
}

// A2AServerActorAdapter adapts the A2A server to work with the actor system
type A2AServerActorAdapter struct {
	actorSystem *System
	registry    *a2a.DiscoveryRegistry
}

// NewA2AServerActorAdapter creates a new adapter for A2A server functionality using actors
func NewA2AServerActorAdapter(actorSystem *System, registry *a2a.DiscoveryRegistry) *A2AServerActorAdapter {
	return &A2AServerActorAdapter{
		actorSystem: actorSystem,
		registry:    registry,
	}
}

// HandleInvocation handles an A2A invocation by routing it to the appropriate internal actor
func (a *A2AServerActorAdapter) HandleInvocation(ctx context.Context, req *a2a.InvocationRequest) (*a2a.InvocationResponse, error) {
	// Extract agent ID from the request method or parameters
	agentID := extractAgentIDFromRequest(req)
	
	// Convert A2A request to actor message
	invokeMsg := Message{
		Type: MessageTypeInvoke,
		Payload: InvokeMessage{
			AgentID:     agentID,
			Instruction: extractInstructionFromParams(req.Params),
			Context:     req.Params,
			TaskID:      req.ID,
		},
	}
	
	// Send to internal actor
	actorAddr := Address(agentID)
	response, err := a.actorSystem.Request(actorAddr, invokeMsg, 30*time.Second) // Default timeout
	if err != nil {
		return &a2a.InvocationResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &a2a.RPCError{
				Code:    -32000,
				Message: fmt.Sprintf("Failed to invoke internal agent: %v", err),
			},
		}, nil
	}
	
	// Convert actor response to A2A response
	switch response.Type {
	case MessageTypeResponse:
		if respMsg, ok := response.Payload.(ResponseMessage); ok {
			return &a2a.InvocationResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  respMsg.Result,
			}, nil
		}
	case MessageTypeError:
		if errorMsg, ok := response.Payload.(ErrorMessage); ok {
			return &a2a.InvocationResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error: &a2a.RPCError{
					Code:    -32001,
					Message: errorMsg.Error,
				},
			}, nil
		}
	}
	
	return &a2a.InvocationResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Error: &a2a.RPCError{
			Code:    -32002,
			Message: "Unexpected response type from agent",
		},
	}, nil
}

// extractAgentIDFromRequest extracts the agent ID from an A2A invocation request
func extractAgentIDFromRequest(req *a2a.InvocationRequest) string {
	// In a real implementation, this would extract the agent ID from the method name
	// or from the parameters. For now, we'll assume the method is the agent ID.
	return req.Method
}

// extractInstructionFromParams extracts the instruction from request parameters
func extractInstructionFromParams(params map[string]interface{}) string {
	if instr, ok := params["instruction"].(string); ok {
		return instr
	}
	if prompt, ok := params["prompt"].(string); ok {
		return prompt
	}
	if query, ok := params["query"].(string); ok {
		return query
	}
	return ""
}