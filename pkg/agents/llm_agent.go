package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/model"
	"github.com/seyi/dagens/pkg/runtime"
	"github.com/seyi/dagens/pkg/telemetry"
	"github.com/seyi/dagens/pkg/tools"
)

// LlmAgent provides LLM-based reasoning and generation with automatic tool calling
// Inspired by ADK's LlmAgent but built on our distributed infrastructure
type LlmAgent struct {
	*agent.BaseAgent
	model         model.ModelProvider
	instruction   string
	toolRegistry  *tools.ToolRegistry
	enabledTools  []string
	temperature   float64
	maxIterations int
	useReAct      bool // Enable ReAct (Reasoning + Acting) pattern

	// LLM-specific callbacks (ADK-compatible)
	beforeModel []ModelCallback
	afterModel  []ModelOutputCallback
	beforeTool  []ToolCallback
	afterTool   []ToolResultCallback
}

// LlmAgentConfig configures an LLM agent
type LlmAgentConfig struct {
	Name          string
	ModelName     string
	Instruction   string
	Tools         []string // Tool names to enable
	Temperature   float64
	MaxIterations int  // For ReAct loop
	UseReAct      bool // Enable ReAct pattern
	Dependencies  []agent.Agent

	// Lifecycle callbacks (ADK-compatible)
	BeforeExecute []agent.AgentCallback       // Called before agent execution
	AfterExecute  []agent.AgentOutputCallback // Called after agent execution
	OnError       []agent.ErrorCallback       // Called on execution error

	// LLM-specific callbacks (ADK-compatible)
	BeforeModel []ModelCallback       // Called before model invocation
	AfterModel  []ModelOutputCallback // Called after model response
	BeforeTool  []ToolCallback        // Called before tool execution
	AfterTool   []ToolResultCallback  // Called after tool completion
}

// NewLlmAgent creates a new LLM agent
func NewLlmAgent(config LlmAgentConfig, modelProvider model.ModelProvider, toolRegistry *tools.ToolRegistry) *LlmAgent {
	if config.Temperature == 0 {
		config.Temperature = 0.7
	}
	if config.MaxIterations == 0 {
		config.MaxIterations = 5
	}

	llmAgent := &LlmAgent{
		model:         modelProvider,
		instruction:   config.Instruction,
		toolRegistry:  toolRegistry,
		enabledTools:  config.Tools,
		temperature:   config.Temperature,
		maxIterations: config.MaxIterations,
		useReAct:      config.UseReAct,

		// Initialize LLM-specific callbacks (ADK-compatible)
		beforeModel: config.BeforeModel,
		afterModel:  config.AfterModel,
		beforeTool:  config.BeforeTool,
		afterTool:   config.AfterTool,
	}

	// Create executor that uses LLM
	executor := &llmExecutor{
		llmAgent: llmAgent,
	}

	// Create base agent with agent-level callbacks (ADK-compatible)
	baseAgent := agent.NewAgent(agent.AgentConfig{
		Name:          config.Name,
		Executor:      executor,
		Dependencies:  config.Dependencies,
		BeforeExecute: config.BeforeExecute,
		AfterExecute:  config.AfterExecute,
		OnError:       config.OnError,
	})

	llmAgent.BaseAgent = baseAgent
	return llmAgent
}

// llmExecutor implements AgentExecutor for LLM-based execution
type llmExecutor struct {
	llmAgent *LlmAgent
}

func (e *llmExecutor) Execute(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	tracer := telemetry.GetGlobalTelemetry().GetTracer()
	ctx, span := tracer.StartSpan(ctx, "agent.llm_executor.execute")
	defer span.End()

	span.SetAttribute("agent.llm.use_react", e.llmAgent.useReAct)

	if e.llmAgent.useReAct {
		output, err := e.executeReAct(ctx, input)
		if err != nil {
			span.SetStatus(telemetry.StatusError, err.Error())
		}
		return output, err
	}

	output, err := e.executeSimple(ctx, input)
	if err != nil {
		span.SetStatus(telemetry.StatusError, err.Error())
	}
	return output, err
}

// executeSimple performs single LLM call with optional tool use
func (e *llmExecutor) executeSimple(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	startTime := time.Now()

	// Runtime integration: Emit partial event for "thinking"
	runtime.EmitMessage(ctx, "Analyzing request...", true)

	// Build prompt combining instruction and user input
	prompt := e.buildPrompt(input)

	// Prepare model input
	modelInput := &model.ModelInput{
		Prompt:      prompt,
		Temperature: e.llmAgent.temperature,
		Context:     input.Context,
	}

	// Add tool schemas if tools are enabled
	if len(e.llmAgent.enabledTools) > 0 {
		if modelInput.Context == nil {
			modelInput.Context = make(map[string]interface{})
		}
		toolSchemas := e.getToolSchemas()
		modelInput.Context["available_tools"] = toolSchemas
	}

	// Call LLM or create a mock tool call
	var modelOutput *model.ModelOutput
	var err error
	if e.llmAgent.model != nil {
		// ADK-compatible: Execute BeforeModel callbacks
		if len(e.llmAgent.beforeModel) > 0 {
			executor := NewModelCallbackExecutor(false) // Don't stop on callback errors
			if err := executor.ExecuteBefore(ctx, e.llmAgent.beforeModel, modelInput); err != nil {
				// Log warning but continue (ADK-compatible behavior)
				fmt.Printf("Warning: before_model callback: %v\n", err)
			}
		}
		// Runtime integration: Emit event before model call
		runtime.EmitMessage(ctx, "Calling LLM...", true)
		modelOutput, err = e.llmAgent.model.Generate(ctx, modelInput)
	} else if len(e.llmAgent.enabledTools) == 1 {
		// If no model is provided and there is only one tool, attempt to call it directly.
		// This is a special case for simple, tool-based agents.
		toolName := e.llmAgent.enabledTools[0]
		// Create a mock tool call from the instruction. For this simple case, we'll hardcode the arguments.
		modelOutput = &model.ModelOutput{
			Text: fmt.Sprintf(`<tool_use name="%s">%s</tool_use>`, toolName, `{"name":"Dagens"}`),
		}
	} else {
		return nil, fmt.Errorf("LlmAgent requires a model provider or a single enabled tool")
	}

	if err != nil {
		// Runtime integration: Emit error event
		runtime.EmitEvent(ctx, runtime.EventTypeError, map[string]interface{}{
			"error": err.Error(),
			"stage": "llm_generation",
		}, false)
		return nil, fmt.Errorf("LLM generation failed: %w", err)
	}

	if e.llmAgent.model != nil {
		// ADK-compatible: Execute AfterModel callbacks
		if len(e.llmAgent.afterModel) > 0 {
			executor := NewModelCallbackExecutor(false)
			if err := executor.ExecuteAfter(ctx, e.llmAgent.afterModel, modelInput, modelOutput); err != nil {
				fmt.Printf("Warning: after_model callback: %v\n", err)
			}
		}
	}

	// Check if LLM wants to use tools
	if toolCall, ok := e.extractToolCall(modelOutput.Text); ok {
		// Runtime integration: Emit tool call event
		runtime.EmitToolCall(ctx, toolCall.ToolName, toolCall.Arguments)

		// Execute tool and get result
		toolResult, err := e.executeTool(ctx, toolCall.ToolName, toolCall.Arguments)

		// Runtime integration: Emit tool result event
		runtime.EmitToolResult(ctx, toolCall.ToolName, toolResult, err)

		if err != nil {
			return nil, fmt.Errorf("tool execution failed: %w", err)
		}

		// Runtime integration: Emit partial event
		runtime.EmitMessage(ctx, fmt.Sprintf("Tool %s executed, processing results...", toolCall.ToolName), true)

		// Call LLM again with tool result
		followUpPrompt := e.buildPromptWithToolResult(input, toolCall, toolResult)
		modelInput.Prompt = followUpPrompt

		// ADK-compatible: Execute BeforeModel callbacks for follow-up call
		if len(e.llmAgent.beforeModel) > 0 {
			executor := NewModelCallbackExecutor(false)
			if err := executor.ExecuteBefore(ctx, e.llmAgent.beforeModel, modelInput); err != nil {
				fmt.Printf("Warning: before_model callback (follow-up): %v\n", err)
			}
		}

		// If a model is available, call it again with the tool result.
		// Otherwise, the tool result is the final answer.
		if e.llmAgent.model != nil {
			modelOutput, err = e.llmAgent.model.Generate(ctx, modelInput)
			if err != nil {
				return nil, fmt.Errorf("LLM generation after tool use failed: %w", err)
			}
		} else {
			modelOutput = &model.ModelOutput{
				Text: toolResult,
			}
		}

		// // ADK-compatible: Execute AfterModel callbacks for follow-up call
		// if len(e.llmAgent.afterModel) > 0 {
		// 	executor := NewModelCallbackExecutor(false)
		// 	if err := executor.ExecuteAfter(ctx, e.llmAgent.afterModel, modelInput, modelOutput); err != nil {
		// 		fmt.Printf("Warning: after_model callback (follow-up): %v\n", err)
		// 	}
		// }
	}

	// Runtime integration: Emit final message event with state delta
	runtime.EmitEventWithDelta(ctx, runtime.EventTypeMessage, modelOutput.Text, false, map[string]interface{}{
		"llm_response":   modelOutput.Text,
		"tokens_used":    modelOutput.TokensUsed,
		"execution_time": time.Since(startTime).Seconds(),
	})

	return &agent.AgentOutput{
		Result: modelOutput.Text,
		Metadata: func() map[string]interface{} {
			metadata := map[string]interface{}{
				"tokens_used":     modelOutput.TokensUsed,
				"execution_time":  time.Since(startTime).Seconds(),
				"finish_reason":   modelOutput.FinishReason,
			}
			if e.llmAgent.model != nil {
				metadata["model"] = e.llmAgent.model.Name()
			}
			return metadata
		}(),
	}, nil
}

// executeReAct implements the ReAct (Reasoning + Acting) pattern
// Iteratively reasons about the task and takes actions until complete
func (e *llmExecutor) executeReAct(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	startTime := time.Now()

	// Runtime integration: Emit ReAct start event
	runtime.EmitMessage(ctx, "Starting ReAct reasoning loop...", true)

	conversationHistory := []string{}
	var finalAnswer string
	iterations := 0

	// ReAct prompt template
	systemPrompt := e.buildReActSystemPrompt()
	conversationHistory = append(conversationHistory, systemPrompt)
	conversationHistory = append(conversationHistory, fmt.Sprintf("Task: %s", input.Instruction))

	for iterations < e.llmAgent.maxIterations {
		iterations++

		// Runtime integration: Emit iteration start
		runtime.EmitMessage(ctx, fmt.Sprintf("ReAct iteration %d/%d...", iterations, e.llmAgent.maxIterations), true)

		// Build prompt with conversation history
		prompt := strings.Join(conversationHistory, "\n\n")

		modelInput := &model.ModelInput{
			Prompt:      prompt,
			Temperature: e.llmAgent.temperature,
			Context:     input.Context,
		}

		// ADK-compatible: Execute BeforeModel callbacks
		if len(e.llmAgent.beforeModel) > 0 {
			executor := NewModelCallbackExecutor(false)
			if err := executor.ExecuteBefore(ctx, e.llmAgent.beforeModel, modelInput); err != nil {
				fmt.Printf("Warning: before_model callback (ReAct iteration %d): %v\n", iterations, err)
			}
		}

		// Call LLM for reasoning
		modelOutput, err := e.llmAgent.model.Generate(ctx, modelInput)
		if err != nil {
			return nil, fmt.Errorf("ReAct iteration %d failed: %w", iterations, err)
		}

		// ADK-compatible: Execute AfterModel callbacks
		if len(e.llmAgent.afterModel) > 0 {
			executor := NewModelCallbackExecutor(false)
			if err := executor.ExecuteAfter(ctx, e.llmAgent.afterModel, modelInput, modelOutput); err != nil {
				fmt.Printf("Warning: after_model callback (ReAct iteration %d): %v\n", iterations, err)
			}
		}

		conversationHistory = append(conversationHistory, modelOutput.Text)

		// Parse ReAct output (Thought, Action, Observation pattern)
		reactStep := e.parseReActStep(modelOutput.Text)

		// Check if we have final answer
		if reactStep.IsFinal {
			finalAnswer = reactStep.Answer
			break
		}

		// Execute action if present
		if reactStep.Action != "" {
			// Runtime integration: Emit tool call event
			runtime.EmitToolCall(ctx, reactStep.Action, reactStep.ActionInput)

			toolResult, err := e.executeTool(ctx, reactStep.Action, reactStep.ActionInput)

			// Runtime integration: Emit tool result event
			runtime.EmitToolResult(ctx, reactStep.Action, toolResult, err)

			if err != nil {
				observation := fmt.Sprintf("Observation: Error executing %s: %v", reactStep.Action, err)
				conversationHistory = append(conversationHistory, observation)
				continue
			}

			observation := fmt.Sprintf("Observation: %s", toolResult)
			conversationHistory = append(conversationHistory, observation)
		}
	}

	if finalAnswer == "" {
		finalAnswer = "Maximum iterations reached without final answer"
	}

	// Runtime integration: Emit final answer event
	runtime.EmitEventWithDelta(ctx, runtime.EventTypeMessage, finalAnswer, false, map[string]interface{}{
		"react_answer":    finalAnswer,
		"iterations":      iterations,
		"execution_time":  time.Since(startTime).Seconds(),
	})

	return &agent.AgentOutput{
		Result: finalAnswer,
		Metadata: map[string]interface{}{
			"model":           e.llmAgent.model.Name(),
			"execution_time":  time.Since(startTime).Seconds(),
			"iterations":      iterations,
			"pattern":         "ReAct",
			"conversation":    conversationHistory,
		},
	}, nil
}

// buildPrompt combines instruction with user input
func (e *llmExecutor) buildPrompt(input *agent.AgentInput) string {
	var parts []string

	if e.llmAgent.instruction != "" {
		parts = append(parts, fmt.Sprintf("System: %s", e.llmAgent.instruction))
	}

	parts = append(parts, fmt.Sprintf("User: %s", input.Instruction))

	// Add previous context if available
	if prevResults, ok := input.Context["previous_results"].([]interface{}); ok && len(prevResults) > 0 {
		parts = append(parts, fmt.Sprintf("Previous Results: %v", prevResults))
	}

	return strings.Join(parts, "\n\n")
}

// buildReActSystemPrompt creates the ReAct pattern system prompt
func (e *llmExecutor) buildReActSystemPrompt() string {
	toolDescriptions := e.getToolDescriptions()

	return fmt.Sprintf(`You are an AI assistant that uses the ReAct (Reasoning + Acting) pattern to solve tasks.

Instructions:
%s

Available Tools:
%s

For each step, follow this format:
Thought: [Your reasoning about what to do next]
Action: [Tool name to use, or "Final Answer" when done]
Action Input: [Input for the tool, as JSON]

After each action, you will receive an observation with the result.
Continue this process until you can provide a final answer.

When you have the final answer, use:
Thought: [Final reasoning]
Action: Final Answer
Action Input: [Your complete answer]`, e.llmAgent.instruction, toolDescriptions)
}

// parseReActStep parses the ReAct format output
func (e *llmExecutor) parseReActStep(output string) *ReActStep {
	step := &ReActStep{}

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "Thought:") {
			step.Thought = strings.TrimSpace(strings.TrimPrefix(line, "Thought:"))
		} else if strings.HasPrefix(line, "Action:") {
			action := strings.TrimSpace(strings.TrimPrefix(line, "Action:"))
			if action == "Final Answer" {
				step.IsFinal = true
			} else {
				step.Action = action
			}
		} else if strings.HasPrefix(line, "Action Input:") {
			inputStr := strings.TrimSpace(strings.TrimPrefix(line, "Action Input:"))
			if step.IsFinal {
				step.Answer = inputStr
			} else {
				step.ActionInput = inputStr
			}
		}
	}

	return step
}

// ReActStep represents one step in ReAct reasoning
type ReActStep struct {
	Thought     string
	Action      string
	ActionInput string
	Answer      string
	IsFinal     bool
}

// toolCall represents a tool invocation request
type toolCall struct {
	ToolName  string
	Arguments string
}

// extractToolCall checks if output contains a tool call
func (e *llmExecutor) extractToolCall(output string) (*toolCall, bool) {
	// Look for tool call format: <tool_use name="tool_name">arguments</tool_use>
	if strings.Contains(output, "<tool_use") {
		startIdx := strings.Index(output, "name=\"")
		if startIdx == -1 {
			return nil, false
		}
		startIdx += 6
		endIdx := strings.Index(output[startIdx:], "\"")
		if endIdx == -1 {
			return nil, false
		}
		toolName := output[startIdx : startIdx+endIdx]

		argStart := strings.Index(output, ">")
		argEnd := strings.Index(output, "</tool_use>")
		if argStart == -1 || argEnd == -1 {
			return nil, false
		}
		arguments := strings.TrimSpace(output[argStart+1 : argEnd])

		return &toolCall{
			ToolName:  toolName,
			Arguments: arguments,
		}, true
	}

	return nil, false
}

// executeTool executes a tool by name with arguments
func (e *llmExecutor) executeTool(ctx context.Context, toolName string, arguments string) (string, error) {
	if e.llmAgent.toolRegistry == nil {
		return "", fmt.Errorf("tool registry not configured")
	}

	tool, err := e.llmAgent.toolRegistry.Get(toolName)
	if err != nil {
		return "", fmt.Errorf("tool not found: %s", toolName)
	}

	// Check if tool is enabled
	enabled := false
	for _, enabledTool := range e.llmAgent.enabledTools {
		if enabledTool == toolName {
			enabled = true
			break
		}
	}
	if !enabled {
		return "", fmt.Errorf("tool not enabled: %s", toolName)
	}

	// Parse arguments as JSON
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		// If not JSON, use as single string argument
		args = map[string]interface{}{"input": arguments}
	}

	// ADK-compatible: Execute BeforeTool callbacks
	if len(e.llmAgent.beforeTool) > 0 {
		executor := NewToolCallbackExecutor(false)
		if err := executor.ExecuteBefore(ctx, e.llmAgent.beforeTool, toolName, args); err != nil {
			fmt.Printf("Warning: before_tool callback for %s: %v\n", toolName, err)
		}
	}

	// Execute tool
	result, err := tool.Handler(ctx, args)
	if err != nil {
		return "", fmt.Errorf("tool execution failed: %w", err)
	}

	// ADK-compatible: Execute AfterTool callbacks
	if len(e.llmAgent.afterTool) > 0 {
		executor := NewToolCallbackExecutor(false)
		if err := executor.ExecuteAfter(ctx, e.llmAgent.afterTool, toolName, args, result); err != nil {
			fmt.Printf("Warning: after_tool callback for %s: %v\n", toolName, err)
		}
	}

	// Convert result to string
	resultStr, ok := result.(string)
	if !ok {
		resultBytes, _ := json.Marshal(result)
		resultStr = string(resultBytes)
	}

	return resultStr, nil
}

// buildPromptWithToolResult builds prompt including tool result
func (e *llmExecutor) buildPromptWithToolResult(input *agent.AgentInput, toolCall *toolCall, result string) string {
	return fmt.Sprintf(`%s

Tool Call: %s
Arguments: %s
Result: %s

Based on the tool result above, please provide your response to the user's request.`,
		e.buildPrompt(input),
		toolCall.ToolName,
		toolCall.Arguments,
		result)
}

// getToolSchemas returns schemas for enabled tools
func (e *llmExecutor) getToolSchemas() []map[string]interface{} {
	schemas := []map[string]interface{}{}

	if e.llmAgent.toolRegistry == nil {
		return schemas
	}

	for _, toolName := range e.llmAgent.enabledTools {
		tool, err := e.llmAgent.toolRegistry.Get(toolName)
		if err != nil {
			continue
		}

		schema := map[string]interface{}{
			"name":        tool.Name,
			"description": tool.Description,
		}
		schemas = append(schemas, schema)
	}

	return schemas
}

// getToolDescriptions returns formatted descriptions of enabled tools
func (e *llmExecutor) getToolDescriptions() string {
	if e.llmAgent.toolRegistry == nil || len(e.llmAgent.enabledTools) == 0 {
		return "No tools available"
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

// WithTools returns a new LlmAgent with specified tools enabled
func (a *LlmAgent) WithTools(tools ...string) *LlmAgent {
	newAgent := *a
	newAgent.enabledTools = tools
	return &newAgent
}

// WithTemperature returns a new LlmAgent with specified temperature
func (a *LlmAgent) WithTemperature(temp float64) *LlmAgent {
	newAgent := *a
	newAgent.temperature = temp
	return &newAgent
}

// WithReAct returns a new LlmAgent with ReAct pattern enabled/disabled
func (a *LlmAgent) WithReAct(enabled bool) *LlmAgent {
	newAgent := *a
	newAgent.useReAct = enabled
	return &newAgent
}
