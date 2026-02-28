package graph

import (
	"context"
	"fmt"

	"github.com/seyi/dagens/pkg/models"
	"github.com/seyi/dagens/pkg/telemetry"
)

// Builder provides a fluent API for constructing graphs.
type Builder struct {
	graph  *Graph
	errors []error
}

// NewBuilder creates a new graph builder with the given name.
func NewBuilder(name string) *Builder {
	return &Builder{
		graph:  NewGraph(name),
		errors: make([]error, 0),
	}
}

// AddNode adds a node to the graph.
func (b *Builder) AddNode(node Node) *Builder {
	if err := b.graph.AddNode(node); err != nil {
		b.errors = append(b.errors, err)
	}
	return b
}

// AddFunction adds a function node to the graph.
func (b *Builder) AddFunction(id string, fn func(ctx context.Context, state State) error) *Builder {
	node := NewFunctionNode(id, fn)
	return b.AddNode(node)
}

// AddAgent adds an agent node to the graph.
// This is a convenience method that creates an AgentNode with the given config.
func (b *Builder) AddAgent(id string, cfg AgentConfig) *Builder {
	node, err := NewAgentNode(id, cfg)
	if err != nil {
		b.errors = append(b.errors, err)
		return b
	}
	return b.AddNode(node)
}

// AddPassthrough adds a passthrough node to the graph.
func (b *Builder) AddPassthrough(id string) *Builder {
	node := NewPassthroughNode(id)
	return b.AddNode(node)
}

// AddEdge adds an edge between two nodes.
func (b *Builder) AddEdge(from, to string) *Builder {
	edge := NewDirectEdge(from, to)
	if err := b.graph.AddEdge(edge); err != nil {
		b.errors = append(b.errors, err)
	}
	return b
}

// AddConditionalEdge adds a conditional edge between two nodes.
func (b *Builder) AddConditionalEdge(from, to string, condition func(state State) bool) *Builder {
	edge := NewConditionalEdge(ConditionalEdgeConfig{
		From:      from,
		To:        to,
		Condition: condition,
	})
	if err := b.graph.AddEdge(edge); err != nil {
		b.errors = append(b.errors, err)
	}
	return b
}

// AddDynamicEdge adds a dynamic edge that selects targets based on state.
func (b *Builder) AddDynamicEdge(from string, selector func(state State) string) *Builder {
	edge := NewDynamicEdge(DynamicEdgeConfig{
		From:     from,
		Selector: selector,
	})
	if err := b.graph.AddEdge(edge); err != nil {
		b.errors = append(b.errors, err)
	}
	return b
}

// SetEntry sets the entry point for the graph.
func (b *Builder) SetEntry(nodeID string) *Builder {
	if err := b.graph.SetEntry(nodeID); err != nil {
		b.errors = append(b.errors, err)
	}
	return b
}

// AddFinish marks a node as a potential finish node.
func (b *Builder) AddFinish(nodeID string) *Builder {
	if err := b.graph.AddFinish(nodeID); err != nil {
		b.errors = append(b.errors, err)
	}
	return b
}

// SetMetadata sets graph-level metadata.
func (b *Builder) SetMetadata(key string, value interface{}) *Builder {
	b.graph.SetMetadata(key, value)
	return b
}

// Build finalizes the graph and returns it.
// Returns an error if any operations failed or if the graph is invalid.
func (b *Builder) Build() (*Graph, error) {
	// Check if any errors occurred during building
	if len(b.errors) > 0 {
		return nil, fmt.Errorf("graph build failed with %d errors: %v", len(b.errors), b.errors[0])
	}

	// Validate the graph
	if err := b.graph.Validate(); err != nil {
		return nil, fmt.Errorf("graph validation failed: %w", err)
	}

	return b.graph, nil
}

// BuildAndExecuteWithObservability builds the graph and executes it with full observability.
func (b *Builder) BuildAndExecuteWithObservability(ctx context.Context, state State, collector *telemetry.TelemetryCollector) error {
	graph, err := b.Build()
	if err != nil {
		return err
	}

	return graph.ExecuteWithObservability(ctx, state, collector)
}

// AgentConfig holds configuration for creating an agent node.
type AgentConfig struct {
	Provider     models.ModelProvider
	SystemPrompt string
	Temperature  float64
	MaxTokens    int
	Tools        []models.ToolDefinition
	Metadata     map[string]interface{}
}

// AgentNode represents an AI agent that uses a model provider.
type AgentNode struct {
	*BaseNode
	provider     models.ModelProvider
	systemPrompt string
	temperature  float64
	maxTokens    int
	tools        []models.ToolDefinition
}

// NewAgentNode creates a new agent node.
func NewAgentNode(id string, cfg AgentConfig) (*AgentNode, error) {
	if cfg.Provider == nil {
		return nil, fmt.Errorf("agent node requires a provider")
	}

	node := &AgentNode{
		BaseNode:     NewBaseNode(id, "agent"),
		provider:     cfg.Provider,
		systemPrompt: cfg.SystemPrompt,
		temperature:  cfg.Temperature,
		maxTokens:    cfg.MaxTokens,
		tools:        cfg.Tools,
	}

	// Copy metadata
	if cfg.Metadata != nil {
		for k, v := range cfg.Metadata {
			node.SetMetadata(k, v)
		}
	}

	return node, nil
}

// Execute runs the agent with the current state.
func (n *AgentNode) Execute(ctx context.Context, state State) error {
	// Build message history from state
	messages := n.buildMessages(state)

	// Create request
	req := &models.ModelRequest{
		Messages:        messages,
		Temperature:     n.temperature,
		MaxOutputTokens: n.maxTokens,
		Tools:           n.tools,
	}

	// Call provider
	resp, err := n.provider.Chat(ctx, req)
	if err != nil {
		return fmt.Errorf("agent %s execution failed: %w", n.ID(), err)
	}

	// Store response in state
	state.Set("last_message", resp.Message)
	state.Set("last_response", resp)

	// Append to conversation history
	history, _ := state.Get("messages")
	if history == nil {
		history = []models.Message{}
	}
	if msgHistory, ok := history.([]models.Message); ok {
		msgHistory = append(msgHistory, resp.Message)
		state.Set("messages", msgHistory)
	}

	// Handle tool calls if present
	if len(resp.Message.ToolCalls) > 0 {
		state.Set("tool_calls", resp.Message.ToolCalls)
		state.Set("requires_tool_execution", true)
	}

	return nil
}

// buildMessages constructs the message array for the model request.
func (n *AgentNode) buildMessages(state State) []models.Message {
	messages := make([]models.Message, 0)

	// Add system prompt if configured
	if n.systemPrompt != "" {
		messages = append(messages, models.Message{
			Role:    models.RoleSystem,
			Content: n.systemPrompt,
		})
	}

	// Get conversation history from state
	if history, exists := state.Get("messages"); exists {
		if msgHistory, ok := history.([]models.Message); ok {
			messages = append(messages, msgHistory...)
		}
	}

	// If no history, check for a single user message
	if len(messages) <= 1 { // Only system prompt or empty
		if userMsg, exists := state.Get("user_message"); exists {
			if msg, ok := userMsg.(string); ok {
				messages = append(messages, models.Message{
					Role:    models.RoleUser,
					Content: msg,
				})
			}
		}
	}

	return messages
}

// Provider returns the agent's model provider.
func (n *AgentNode) Provider() models.ModelProvider {
	return n.provider
}

// ToolNode represents a tool that can be executed.
type ToolNode struct {
	*BaseNode
	tool models.ToolDefinition
	exec func(ctx context.Context, args string) (string, error)
}

// ToolConfig holds configuration for creating a tool node.
type ToolConfig struct {
	ID         string
	Tool       models.ToolDefinition
	Execute    func(ctx context.Context, args string) (string, error)
	Metadata   map[string]interface{}
}

// NewToolNode creates a new tool node.
func NewToolNode(cfg ToolConfig) (*ToolNode, error) {
	if cfg.Execute == nil {
		return nil, fmt.Errorf("tool node requires an execute function")
	}

	node := &ToolNode{
		BaseNode: NewBaseNode(cfg.ID, "tool"),
		tool:     cfg.Tool,
		exec:     cfg.Execute,
	}

	// Copy metadata
	if cfg.Metadata != nil {
		for k, v := range cfg.Metadata {
			node.SetMetadata(k, v)
		}
	}

	return node, nil
}

// Execute runs the tool.
func (n *ToolNode) Execute(ctx context.Context, state State) error {
	// Get tool call from state
	toolCallsIface, exists := state.Get("tool_calls")
	if !exists {
		return fmt.Errorf("no tool calls in state")
	}

	toolCalls, ok := toolCallsIface.([]models.ToolCall)
	if !ok || len(toolCalls) == 0 {
		return fmt.Errorf("invalid tool calls in state")
	}

	// Find matching tool call
	var matchingCall *models.ToolCall
	for i := range toolCalls {
		if toolCalls[i].Function.Name == n.tool.Function.Name {
			matchingCall = &toolCalls[i]
			break
		}
	}

	if matchingCall == nil {
		return fmt.Errorf("no matching tool call for %s", n.tool.Function.Name)
	}

	// Execute tool
	result, err := n.exec(ctx, matchingCall.Function.Arguments)
	if err != nil {
		return fmt.Errorf("tool %s execution failed: %w", n.ID(), err)
	}

	// Store result in state
	state.Set(fmt.Sprintf("tool_result_%s", matchingCall.ID), result)

	// Add tool response to message history
	history, _ := state.Get("messages")
	if msgHistory, ok := history.([]models.Message); ok {
		msgHistory = append(msgHistory, models.Message{
			Role:       models.RoleTool,
			Content:    result,
			ToolCallID: matchingCall.ID,
		})
		state.Set("messages", msgHistory)
	}

	return nil
}

// Tool returns the tool definition.
func (n *ToolNode) Tool() models.ToolDefinition {
	return n.tool
}
