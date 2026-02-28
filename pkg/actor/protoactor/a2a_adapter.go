// Package protoactor provides integration with the A2A (Agent-to-Agent) protocol
package protoactor

import (
	"context"
	"fmt"
	"time"

	"github.com/asynkron/protoactor-go/actor"
	"github.com/seyi/dagens/pkg/a2a"
	"github.com/seyi/dagens/pkg/agent"
)

// A2AProtoActorAdapter adapts the A2A protocol to work with the ProtoActor-Go system
type A2AProtoActorAdapter struct {
	actorSystem *System
	client      a2a.A2AClient
}

// NewA2AProtoActorAdapter creates a new adapter between A2A protocol and ProtoActor-Go system
func NewA2AProtoActorAdapter(actorSystem *System, client a2a.A2AClient) *A2AProtoActorAdapter {
	return &A2AProtoActorAdapter{
		actorSystem: actorSystem,
		client:      client,
	}
}

// InvokeActor invokes an actor through the ProtoActor-Go system, with fallback to A2A if needed
func (a *A2AProtoActorAdapter) InvokeActor(ctx context.Context, agentID string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// In ProtoActor-Go, we need to have a way to look up actors by agentID
	// For this implementation, we'll create a PID for the agent ID
	pid := a.actorSystem.actorSystem.NewLocalPID(agentID)

	// Check if the actor actually exists by trying to send a message
	// For now, we'll assume the actor exists if we have a PID
	// In a real implementation, we might need to maintain a registry of active actors

	// Agent exists as internal actor, use ProtoActor-Go for communication
	return a.invokeInternalActor(ctx, pid, input)
}

// invokeInternalActor invokes an agent that exists as an internal ProtoActor-Go actor
func (a *A2AProtoActorAdapter) invokeInternalActor(ctx context.Context, pid Address, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// Convert agent input to ProtoActor-Go message
	invokeMsg := &InvokeMessage{
		AgentID:     pid.Id,
		Instruction: input.Instruction,
		Context:     input.Context,
		Timeout:     input.Timeout,
		TaskID:      input.TaskID,
	}
	
	// Send message and wait for response
	response, err := a.actorSystem.RequestRaw(pid, invokeMsg, input.Timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke internal actor: %w", err)
	}
	
	// Convert response back to agent output
	// The actual conversion depends on what the actor returns
	// For now, we'll handle common response types
	
	switch resp := response.(type) {
	case *ResponseMessage:
		return &agent.AgentOutput{
			TaskID: resp.TaskID,
			Result: resp.Result,
		}, nil
	case *ErrorMessage:
		return nil, fmt.Errorf("actor error: %s", resp.Error)
	case *agent.AgentOutput:
		return resp, nil
	default:
		// If it's not one of our known types, treat as a generic result
		return &agent.AgentOutput{
			TaskID: input.TaskID,
			Result: response,
		}, nil
	}
}

// invokeExternalAgent invokes an agent through the A2A protocol
func (a *A2AProtoActorAdapter) invokeExternalAgent(ctx context.Context, agentID string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	if a.client == nil {
		return nil, fmt.Errorf("A2A client not configured for external agent invocation")
	}
	
	return a.client.InvokeAgent(ctx, agentID, input)
}

// ProtoActorAgentAdapter wraps an agent.Agent to make it compatible with ProtoActor-Go
type ProtoActorAgentAdapter struct {
	agent.Agent
}

// NewProtoActorAgentAdapter creates a new adapter that makes an agent.Agent compatible with ProtoActor-Go
func NewProtoActorAgentAdapter(agent agent.Agent) *ProtoActorAgentAdapter {
	return &ProtoActorAgentAdapter{
		Agent: agent,
	}
}

// Receive implements the ProtoActor-Go Actor interface, allowing agent.Agent to work with ProtoActor-Go
func (a *ProtoActorAgentAdapter) Receive(ctx actor.Context) {
	switch msg := ctx.Message().(type) {
	case *InvokeMessage:
		// Convert ProtoActor message to agent input
		input := &agent.AgentInput{
			Instruction: msg.Instruction,
			Context:     msg.Context,
			TaskID:      msg.TaskID,
			Timeout:     msg.Timeout,
		}
		
		// Execute the agent
		output, err := a.Agent.Execute(context.Background(), input)
		
		// Send response back
		if ctx.Sender() != nil {
			var response interface{}
			if err != nil {
				response = &ErrorMessage{
					Error:   err.Error(),
					Details: nil,
				}
			} else {
				response = &ResponseMessage{
					TaskID: output.TaskID,
					Result: output.Result,
					Error:  nil,
				}
			}
			
			ctx.Respond(response)
		}
	case *PingMessage:
		// Simple health check
		if ctx.Sender() != nil {
			response := &PongMessage{
				ID:        msg.ID,
				Timestamp: time.Now(),
			}
			ctx.Respond(response)
		}
	default:
		// Handle other message types if needed
		// For now, just log or ignore
	}
}

// A2AServerProtoActorAdapter adapts the A2A server to work with ProtoActor-Go
type A2AServerProtoActorAdapter struct {
	actorSystem *System
	registry    *a2a.DiscoveryRegistry
}

// NewA2AServerProtoActorAdapter creates a new adapter for A2A server functionality using ProtoActor-Go
func NewA2AServerProtoActorAdapter(actorSystem *System, registry *a2a.DiscoveryRegistry) *A2AServerProtoActorAdapter {
	return &A2AServerProtoActorAdapter{
		actorSystem: actorSystem,
		registry:    registry,
	}
}

// HandleInvocation handles an A2A invocation by routing it to the appropriate internal actor
func (a *A2AServerProtoActorAdapter) HandleInvocation(ctx context.Context, req *a2a.InvocationRequest) (*a2a.InvocationResponse, error) {
	// Extract agent ID from the request method or parameters
	agentID := extractAgentIDFromRequest(req)
	
	// Create a PID for the agent ID
	pid := a.actorSystem.actorSystem.NewLocalPID(agentID)
	// In a real implementation, we might want to check if the actor is actually alive
	// For now, we proceed assuming the actor exists
	
	// Convert A2A request to ProtoActor message
	invokeMsg := &InvokeMessage{
		AgentID:     agentID,
		Instruction: extractInstructionFromParams(req.Params),
		Context:     req.Params,
		TaskID:      req.ID,
		Timeout:     30 * time.Second, // Default timeout
	}
	
	// Send to internal actor
	response, err := a.actorSystem.RequestRaw(pid, invokeMsg, 30*time.Second)
	if err != nil {
		return &a2a.InvocationResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &a2a.RPCError{
				Code:    -32001,
				Message: fmt.Sprintf("Failed to invoke internal agent: %v", err),
			},
		}, nil
	}
	
	// Convert ProtoActor response to A2A response
	switch resp := response.(type) {
	case *ResponseMessage:
		return &a2a.InvocationResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  resp.Result,
		}, nil
	case *ErrorMessage:
		return &a2a.InvocationResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &a2a.RPCError{
				Code:    -32002,
				Message: resp.Error,
			},
		}, nil
	default:
		// If response is not one of our defined types, treat as generic result
		return &a2a.InvocationResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  response,
		}, nil
	}
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