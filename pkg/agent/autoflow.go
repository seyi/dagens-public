package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/events"
)

// TransferScope defines the scope within which agent transfers are allowed
type TransferScope int

const (
	// TransferScopeDisabled disables all transfers
	TransferScopeDisabled TransferScope = iota

	// TransferScopeParent allows transfers only to the parent agent
	TransferScopeParent

	// TransferScopeSiblings allows transfers to sibling agents (same parent)
	TransferScopeSiblings

	// TransferScopeSubAgents allows transfers only to direct sub-agents
	TransferScopeSubAgents

	// TransferScopeDescendants allows transfers to any descendant agent
	TransferScopeDescendants

	// TransferScopeAll allows transfers to any agent in the hierarchy
	TransferScopeAll
)

// String returns the string representation of TransferScope
func (ts TransferScope) String() string {
	switch ts {
	case TransferScopeDisabled:
		return "disabled"
	case TransferScopeParent:
		return "parent"
	case TransferScopeSiblings:
		return "siblings"
	case TransferScopeSubAgents:
		return "sub_agents"
	case TransferScopeDescendants:
		return "descendants"
	case TransferScopeAll:
		return "all"
	default:
		return "unknown"
	}
}

// AutoFlowConfig configures AutoFlow behavior
type AutoFlowConfig struct {
	// AllowTransfer enables or disables agent transfers
	AllowTransfer bool

	// TransferScope defines the scope of allowed transfers
	TransferScope TransferScope

	// MaxTransfers sets the maximum number of transfers allowed
	MaxTransfers int

	// EventBus for publishing transfer events (optional)
	EventBus events.EventBus

	// EnableDistributedSupport enables cross-partition transfer handling
	EnableDistributedSupport bool

	// NodeID for distributed tracking
	NodeID string
}

// DefaultAutoFlowConfig returns default configuration
func DefaultAutoFlowConfig() AutoFlowConfig {
	return AutoFlowConfig{
		AllowTransfer:            true,
		TransferScope:            TransferScopeSubAgents,
		MaxTransfers:             10,
		EnableDistributedSupport: true,
	}
}

// AutoFlow intercepts tool calls and handles LLM-driven agent transfers
// Inspired by ADK's AutoFlow pattern with Spark distributed computing support
type AutoFlow struct {
	// Root agent in the hierarchy
	rootAgent Agent

	// Configuration
	config AutoFlowConfig

	// Invocation context (set during execution)
	invocationCtx *InvocationContext
}

// NewAutoFlow creates a new AutoFlow interceptor
func NewAutoFlow(rootAgent Agent, config AutoFlowConfig) *AutoFlow {
	return &AutoFlow{
		rootAgent: rootAgent,
		config:    config,
	}
}

// Execute runs an agent with AutoFlow transfer interception
func (af *AutoFlow) Execute(ctx context.Context, agent Agent, input *AgentInput) (*AgentOutput, error) {
	// Create invocation context
	af.invocationCtx = NewInvocationContext(ctx, input, agent)
	af.invocationCtx.SetMaxTransfers(af.config.MaxTransfers)

	if af.config.NodeID != "" {
		af.invocationCtx.SetNodeID(af.config.NodeID)
	}

	// Execute with transfer support
	return af.executeWithTransfer(ctx, agent, input)
}

// executeWithTransfer executes an agent and handles potential transfers
func (af *AutoFlow) executeWithTransfer(ctx context.Context, agent Agent, input *AgentInput) (*AgentOutput, error) {
	// Inject invocation context and event bus into input for tools
	if input.Context == nil {
		input.Context = make(map[string]interface{})
	}
	input.Context["__invocation_context__"] = af.invocationCtx
	input.Context["__transfer_scope__"] = af.config.TransferScope.String()

	if af.config.EventBus != nil {
		input.Context["__event_bus__"] = af.config.EventBus
	}

	// Execute the agent
	output, err := agent.Execute(ctx, input)
	if err != nil {
		return nil, err
	}

	// Check if output contains a transfer request
	if af.config.AllowTransfer && af.isTransferRequest(output) {
		return af.handleTransferRequest(ctx, output, input)
	}

	return output, nil
}

// isTransferRequest checks if the output contains a transfer signal
func (af *AutoFlow) isTransferRequest(output *AgentOutput) bool {
	if output.Metadata == nil {
		return false
	}

	// Check for transfer_requested flag (from transfer_to_agent tool)
	if requested, ok := output.Metadata["transfer_requested"].(bool); ok && requested {
		return true
	}

	// Check for tool_call with transfer_to_agent
	if toolCall, ok := output.Metadata["tool_call"].(map[string]interface{}); ok {
		if toolName, ok := toolCall["name"].(string); ok && toolName == "transfer_to_agent" {
			return true
		}
	}

	return false
}

// handleTransferRequest processes a transfer request
func (af *AutoFlow) handleTransferRequest(ctx context.Context, output *AgentOutput, input *AgentInput) (*AgentOutput, error) {
	// Extract target agent name
	var targetAgentName string
	var reason string

	if output.Metadata != nil {
		if name, ok := output.Metadata["target_agent"].(string); ok {
			targetAgentName = name
		}
		if r, ok := output.Metadata["reason"].(string); ok {
			reason = r
		}
	}

	if targetAgentName == "" {
		return nil, fmt.Errorf("transfer request missing target_agent name")
	}

	// Find target agent in scope
	targetAgent, err := af.findAgentInScope(targetAgentName)
	if err != nil {
		// Publish transfer failed event
		if af.config.EventBus != nil {
			af.config.EventBus.Publish(events.NewTransferFailedEvent(
				af.invocationCtx.CurrentAgent.Name(),
				targetAgentName,
				reason,
				err.Error(),
				af.invocationCtx.CurrentPartition,
			))
		}
		return nil, fmt.Errorf("transfer validation failed: %w", err)
	}

	// Execute the transfer
	return af.executeTransfer(ctx, targetAgent, reason, input)
}

// executeTransfer performs the actual agent transfer
func (af *AutoFlow) executeTransfer(ctx context.Context, targetAgent Agent, reason string, input *AgentInput) (*AgentOutput, error) {
	startTime := time.Now()
	currentAgent := af.invocationCtx.CurrentAgent
	currentPartition := af.invocationCtx.CurrentPartition

	// Record transfer in context
	err := af.invocationCtx.TransferTo(targetAgent, reason)
	if err != nil {
		// Publish failure event
		if af.config.EventBus != nil {
			af.config.EventBus.Publish(events.NewTransferFailedEvent(
				currentAgent.Name(),
				targetAgent.Name(),
				reason,
				err.Error(),
				currentPartition,
			))
		}
		return nil, fmt.Errorf("transfer execution failed: %w", err)
	}

	// Determine transfer type for metrics
	transferType := "local"
	if currentPartition != targetAgent.Partition() {
		transferType = "remote"
	}

	// Execute target agent
	output, err := af.executeWithTransfer(ctx, targetAgent, input)

	duration := time.Since(startTime)

	if err != nil {
		// Publish failure event
		if af.config.EventBus != nil {
			af.config.EventBus.Publish(events.NewTransferFailedEvent(
				currentAgent.Name(),
				targetAgent.Name(),
				reason,
				err.Error(),
				currentPartition,
			))
		}
		return nil, fmt.Errorf("transferred agent execution failed: %w", err)
	}

	// Publish success event
	if af.config.EventBus != nil {
		af.config.EventBus.Publish(events.NewTransferCompletedEvent(
			currentAgent.Name(),
			targetAgent.Name(),
			currentPartition,
			targetAgent.Partition(),
			duration,
			transferType,
		))
	}

	// Add transfer metadata to output
	if output.Metadata == nil {
		output.Metadata = make(map[string]interface{})
	}
	output.Metadata["transfer_occurred"] = true
	output.Metadata["transfer_path"] = af.invocationCtx.GetTransferPath()
	output.Metadata["transfer_count"] = af.invocationCtx.GetTransferCount()
	output.Metadata["cross_partition"] = af.invocationCtx.HasTransferredAcrossPartitions()

	return output, nil
}

// findAgentInScope finds and validates the target agent based on transfer scope
func (af *AutoFlow) findAgentInScope(targetName string) (Agent, error) {
	currentAgent := af.invocationCtx.CurrentAgent

	switch af.config.TransferScope {
	case TransferScopeDisabled:
		return nil, fmt.Errorf("agent transfers are disabled")

	case TransferScopeParent:
		return af.findParent(currentAgent, targetName)

	case TransferScopeSiblings:
		return af.findSibling(currentAgent, targetName)

	case TransferScopeSubAgents:
		return af.findSubAgent(currentAgent, targetName)

	case TransferScopeDescendants:
		return af.findDescendant(currentAgent, targetName)

	case TransferScopeAll:
		return af.findInHierarchy(targetName)

	default:
		return nil, fmt.Errorf("unknown transfer scope: %v", af.config.TransferScope)
	}
}

// findParent finds the parent agent
func (af *AutoFlow) findParent(currentAgent Agent, targetName string) (Agent, error) {
	baseAgent, ok := currentAgent.(*BaseAgent)
	if !ok {
		return nil, fmt.Errorf("current agent does not support parent access")
	}

	parent := baseAgent.Parent()
	if parent == nil {
		return nil, fmt.Errorf("current agent has no parent")
	}

	if parent.Name() != targetName {
		return nil, fmt.Errorf("agent '%s' is not the parent (parent is '%s')", targetName, parent.Name())
	}

	return parent, nil
}

// findSibling finds a sibling agent (shares same parent)
func (af *AutoFlow) findSibling(currentAgent Agent, targetName string) (Agent, error) {
	baseAgent, ok := currentAgent.(*BaseAgent)
	if !ok {
		return nil, fmt.Errorf("current agent does not support sibling access")
	}

	parent := baseAgent.Parent()
	if parent == nil {
		return nil, fmt.Errorf("current agent has no parent, so no siblings")
	}

	parentBase, ok := parent.(*BaseAgent)
	if !ok {
		return nil, fmt.Errorf("parent agent does not support sub-agent access")
	}

	siblings := parentBase.SubAgents()
	for _, sibling := range siblings {
		if sibling.Name() == targetName && sibling.ID() != currentAgent.ID() {
			return sibling, nil
		}
	}

	return nil, fmt.Errorf("agent '%s' not found in siblings", targetName)
}

// findSubAgent finds a direct sub-agent
func (af *AutoFlow) findSubAgent(currentAgent Agent, targetName string) (Agent, error) {
	baseAgent, ok := currentAgent.(*BaseAgent)
	if !ok {
		return nil, fmt.Errorf("current agent does not support sub-agent access")
	}

	subAgents := baseAgent.SubAgents()
	for _, subAgent := range subAgents {
		if subAgent.Name() == targetName {
			return subAgent, nil
		}
	}

	return nil, fmt.Errorf("agent '%s' not found in direct sub-agents", targetName)
}

// findDescendant finds any descendant agent (recursive search)
func (af *AutoFlow) findDescendant(currentAgent Agent, targetName string) (Agent, error) {
	baseAgent, ok := currentAgent.(*BaseAgent)
	if !ok {
		return nil, fmt.Errorf("current agent does not support descendant access")
	}

	// Try to find in descendants
	targetAgent, err := baseAgent.FindAgent(targetName)
	if err != nil {
		return nil, fmt.Errorf("agent '%s' not found in descendants: %w", targetName, err)
	}

	return targetAgent, nil
}

// findInHierarchy finds any agent in the entire hierarchy
func (af *AutoFlow) findInHierarchy(targetName string) (Agent, error) {
	// Search from root
	rootBase, ok := af.rootAgent.(*BaseAgent)
	if !ok {
		return nil, fmt.Errorf("root agent does not support hierarchy search")
	}

	targetAgent, err := rootBase.FindAgent(targetName)
	if err != nil {
		return nil, fmt.Errorf("agent '%s' not found in hierarchy: %w", targetName, err)
	}

	return targetAgent, nil
}

// GetInvocationContext returns the current invocation context
func (af *AutoFlow) GetInvocationContext() *InvocationContext {
	return af.invocationCtx
}

// GetConfig returns the AutoFlow configuration
func (af *AutoFlow) GetConfig() AutoFlowConfig {
	return af.config
}

// SetTransferScope updates the transfer scope
func (af *AutoFlow) SetTransferScope(scope TransferScope) {
	af.config.TransferScope = scope
}

// SetAllowTransfer enables or disables transfers
func (af *AutoFlow) SetAllowTransfer(allow bool) {
	af.config.AllowTransfer = allow
}

// --- Distributed Computing Support ---

// SerializeContext serializes the current invocation context for remote execution
func (af *AutoFlow) SerializeContext() (*SerializableInvocationContext, error) {
	if af.invocationCtx == nil {
		return nil, fmt.Errorf("no invocation context available")
	}
	return af.invocationCtx.Serialize()
}

// ExecuteRemote executes an agent on a remote partition
// In a real Spark implementation, this would use RPC to execute on the target node
func (af *AutoFlow) ExecuteRemote(ctx context.Context, targetAgent Agent, input *AgentInput, serializedCtx *SerializableInvocationContext) (*AgentOutput, error) {
	// This is a placeholder for actual remote execution
	// In production, this would:
	// 1. Serialize input and context
	// 2. Send to remote node via RPC/network
	// 3. Execute on remote node
	// 4. Return serialized output

	// For now, execute locally but track as remote
	return af.executeWithTransfer(ctx, targetAgent, input)
}

// CheckPartitionLocality checks if a transfer would be local or remote
func (af *AutoFlow) CheckPartitionLocality(targetAgent Agent) (isLocal bool, sourcePartition, targetPartition string) {
	if af.invocationCtx == nil {
		return true, "", ""
	}

	sourcePartition = af.invocationCtx.CurrentPartition
	targetPartition = targetAgent.Partition()
	isLocal = sourcePartition == targetPartition

	return isLocal, sourcePartition, targetPartition
}
