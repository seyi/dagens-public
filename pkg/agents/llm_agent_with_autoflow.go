package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/events"
	"github.com/seyi/dagens/pkg/model"
	"github.com/seyi/dagens/pkg/tools"
)

// LlmAgentWithAutoFlow provides LLM-based reasoning with agent transfer support
// Integrates AutoFlow interceptor for LLM-driven delegation (Phase 3)
type LlmAgentWithAutoFlow struct {
	*agent.BaseAgent

	// LLM configuration
	model         model.ModelProvider
	instruction   string
	toolRegistry  *tools.ToolRegistry
	enabledTools  []string
	temperature   float64
	maxIterations int
	useReAct      bool

	// AutoFlow configuration (Phase 3)
	autoFlow      *agent.AutoFlow
	allowTransfer bool
	transferScope agent.TransferScope
	eventBus      events.EventBus
}

// LlmAgentWithAutoFlowConfig configures an LLM agent with transfer support
type LlmAgentWithAutoFlowConfig struct {
	// Basic configuration
	Name          string
	Description   string
	ModelName     string
	Instruction   string
	Tools         []string
	Temperature   float64
	MaxIterations int
	UseReAct      bool
	Dependencies  []agent.Agent
	Partition     string

	// Transfer configuration (Phase 3)
	AllowTransfer           bool
	TransferScope           agent.TransferScope
	AutoRegisterTransferTools bool
	EventBus                events.EventBus
	NodeID                  string
	MaxTransfers            int
}

// DefaultLlmAgentWithAutoFlowConfig returns default configuration
func DefaultLlmAgentWithAutoFlowConfig() LlmAgentWithAutoFlowConfig {
	return LlmAgentWithAutoFlowConfig{
		Temperature:               0.7,
		MaxIterations:             5,
		UseReAct:                  false,
		AllowTransfer:             true,
		TransferScope:             agent.TransferScopeSubAgents,
		AutoRegisterTransferTools: true,
		MaxTransfers:              10,
	}
}

// NewLlmAgentWithAutoFlow creates a new LLM agent with AutoFlow support
func NewLlmAgentWithAutoFlow(
	config LlmAgentWithAutoFlowConfig,
	modelProvider model.ModelProvider,
	toolRegistry *tools.ToolRegistry,
) *LlmAgentWithAutoFlow {
	// Set defaults
	if config.Temperature == 0 {
		config.Temperature = 0.7
	}
	if config.MaxIterations == 0 {
		config.MaxIterations = 5
	}
	if config.MaxTransfers == 0 {
		config.MaxTransfers = 10
	}

	// Auto-register transfer tools if enabled
	if config.AllowTransfer && config.AutoRegisterTransferTools {
		tools.RegisterTransferTools(toolRegistry)

		// Add transfer tools to enabled tools if not already present
		transferTools := []string{"transfer_to_agent", "get_available_agents", "validate_transfer_scope"}
		for _, tool := range transferTools {
			found := false
			for _, enabledTool := range config.Tools {
				if enabledTool == tool {
					found = true
					break
				}
			}
			if !found {
				config.Tools = append(config.Tools, tool)
			}
		}
	}

	llmAgent := &LlmAgentWithAutoFlow{
		model:         modelProvider,
		instruction:   config.Instruction,
		toolRegistry:  toolRegistry,
		enabledTools:  config.Tools,
		temperature:   config.Temperature,
		maxIterations: config.MaxIterations,
		useReAct:      config.UseReAct,
		allowTransfer: config.AllowTransfer,
		transferScope: config.TransferScope,
		eventBus:      config.EventBus,
	}

	// Create executor
	executor := &llmWithAutoFlowExecutor{
		llmAgent: llmAgent,
	}

	// Create base agent
	baseAgent := agent.NewAgent(agent.AgentConfig{
		Name:         config.Name,
		Description:  config.Description,
		Executor:     executor,
		Dependencies: config.Dependencies,
		Partition:    config.Partition,
	})

	llmAgent.BaseAgent = baseAgent

	// Create AutoFlow if transfers enabled
	if config.AllowTransfer {
		autoFlowConfig := agent.AutoFlowConfig{
			AllowTransfer:            true,
			TransferScope:            config.TransferScope,
			MaxTransfers:             config.MaxTransfers,
			EventBus:                 config.EventBus,
			EnableDistributedSupport: true,
			NodeID:                   config.NodeID,
		}
		llmAgent.autoFlow = agent.NewAutoFlow(baseAgent, autoFlowConfig)
	}

	return llmAgent
}

// llmWithAutoFlowExecutor implements AgentExecutor with AutoFlow support
type llmWithAutoFlowExecutor struct {
	llmAgent *LlmAgentWithAutoFlow
}

func (e *llmWithAutoFlowExecutor) Execute(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// If transfers are enabled, use AutoFlow
	if e.llmAgent.allowTransfer && e.llmAgent.autoFlow != nil {
		return e.executeWithAutoFlow(ctx, ag, input)
	}

	// Otherwise, execute normally (backward compatible)
	return e.executeWithoutTransfer(ctx, input)
}

// executeWithAutoFlow executes through AutoFlow for transfer support
func (e *llmWithAutoFlowExecutor) executeWithAutoFlow(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// Create a mock output that simulates LLM tool calling
	// In production, this would parse actual LLM tool calls
	output, err := e.executeLLMAndDetectTransfer(ctx, input)
	if err != nil {
		return nil, err
	}

	// Check if LLM requested a transfer
	if output.Metadata != nil {
		if requested, ok := output.Metadata["transfer_requested"].(bool); ok && requested {
			// AutoFlow will handle the transfer
			// We return the output with transfer metadata
			return output, nil
		}
	}

	return output, nil
}

// executeLLMAndDetectTransfer calls LLM and detects if it wants to transfer
func (e *llmWithAutoFlowExecutor) executeLLMAndDetectTransfer(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	if input == nil {
		input = &agent.AgentInput{}
	}

	// Build prompt with available tools
	prompt := e.buildPrompt(input)

	// Add tool descriptions if tools are enabled
	toolDescriptions := e.getToolDescriptions()
	if toolDescriptions != "" {
		prompt = fmt.Sprintf("%s\n\nAvailable Tools:\n%s\n\nYou can call tools using the format: <tool_use name=\"tool_name\">{\"arg\": \"value\"}</tool_use>",
			prompt, toolDescriptions)
	}

	// Call model (in production, this would make actual LLM API call)
	// For now, we'll create a mock response
	modelInput := &model.ModelInput{
		Prompt:      prompt,
		Temperature: e.llmAgent.temperature,
		Context:     input.Context,
	}

	modelOutput, err := e.llmAgent.model.Generate(ctx, modelInput)
	if err != nil {
		return nil, fmt.Errorf("LLM generation failed: %w", err)
	}

	// Parse model output for tool calls
	output := &agent.AgentOutput{
		Result:   modelOutput.Text,
		Metadata: make(map[string]interface{}),
	}

	// Check if output contains transfer_to_agent call
	if transferCall, ok := e.extractTransferCall(modelOutput.Text); ok {
		output.Metadata["transfer_requested"] = true
		output.Metadata["target_agent"] = transferCall.TargetAgent
		output.Metadata["reason"] = transferCall.Reason
	}

	return output, nil
}

// executeWithoutTransfer executes without AutoFlow (backward compatible)
func (e *llmWithAutoFlowExecutor) executeWithoutTransfer(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	if input == nil {
		input = &agent.AgentInput{}
	}

	// Simple execution without transfer support
	prompt := e.buildPrompt(input)

	modelInput := &model.ModelInput{
		Prompt:      prompt,
		Temperature: e.llmAgent.temperature,
		Context:     input.Context,
	}

	modelOutput, err := e.llmAgent.model.Generate(ctx, modelInput)
	if err != nil {
		return nil, err
	}

	return &agent.AgentOutput{
		Result:   modelOutput.Text,
		Metadata: make(map[string]interface{}),
	}, nil
}

// buildPrompt constructs the LLM prompt
func (e *llmWithAutoFlowExecutor) buildPrompt(input *agent.AgentInput) string {
	if input == nil {
		input = &agent.AgentInput{}
	}

	parts := []string{}

	if e.llmAgent.instruction != "" {
		parts = append(parts, fmt.Sprintf("System: %s", e.llmAgent.instruction))
	}

	if input.Instruction != "" {
		parts = append(parts, fmt.Sprintf("User: %s", input.Instruction))
	}

	// Add context if available
	if prevResults, ok := input.Context["previous_results"]; ok {
		parts = append(parts, fmt.Sprintf("Context: %v", prevResults))
	}

	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		return strings.Join(parts, "\n\n")
	}
}

// getToolDescriptions returns formatted tool descriptions
func (e *llmWithAutoFlowExecutor) getToolDescriptions() string {
	if e.llmAgent.toolRegistry == nil || len(e.llmAgent.enabledTools) == 0 {
		return ""
	}

	descriptions := []string{}
	for _, toolName := range e.llmAgent.enabledTools {
		tool, err := e.llmAgent.toolRegistry.Get(toolName)
		if err != nil {
			continue
		}
		descriptions = append(descriptions, fmt.Sprintf("- %s: %s", tool.Name, tool.Description))
	}

	return strings.Join(descriptions, "\n")
}

// transferCall represents a parsed transfer request
type transferCall struct {
	TargetAgent string
	Reason      string
}

// extractTransferCall parses transfer_to_agent calls from LLM output
func (e *llmWithAutoFlowExecutor) extractTransferCall(output string) (*transferCall, bool) {
	startMarker := `<tool_use name="transfer_to_agent">`
	endMarker := `</tool_use>`
	start := strings.Index(output, startMarker)
	end := strings.Index(output, endMarker)
	if start == -1 || end == -1 || end <= start {
		return nil, false
	}
	content := output[start+len(startMarker) : end]
	var payload struct {
		AgentName string `json:"agent_name"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &payload); err != nil {
		return nil, false
	}
	if payload.AgentName == "" {
		return nil, false
	}

	return &transferCall{
		TargetAgent: payload.AgentName,
		Reason:      payload.Reason,
	}, true
}

// GetAutoFlow returns the AutoFlow instance (for testing)
func (l *LlmAgentWithAutoFlow) GetAutoFlow() *agent.AutoFlow {
	return l.autoFlow
}

// GetTransferScope returns the transfer scope
func (l *LlmAgentWithAutoFlow) GetTransferScope() agent.TransferScope {
	return l.transferScope
}

// SetTransferScope updates the transfer scope
func (l *LlmAgentWithAutoFlow) SetTransferScope(scope agent.TransferScope) {
	l.transferScope = scope
	if l.autoFlow != nil {
		l.autoFlow.SetTransferScope(scope)
	}
}

// IsTransferEnabled returns whether transfers are enabled
func (l *LlmAgentWithAutoFlow) IsTransferEnabled() bool {
	return l.allowTransfer
}
