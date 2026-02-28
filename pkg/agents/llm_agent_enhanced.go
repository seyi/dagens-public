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

// Package agents provides an enhanced LLM agent with ADK-compatible features.
//
// This file adds two key Google ADK Go features:
// 1. InstructionProvider - Dynamic instruction generation based on context
// 2. Short-circuit callbacks - Callbacks that can intercept and replace responses
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
	"github.com/seyi/dagens/pkg/tools"
)

// ============================================================================
// InstructionProvider Types (ADK Pattern)
// ============================================================================

// InstructionProvider generates instructions dynamically based on context.
// This matches Google ADK Go's InstructionProvider pattern.
//
// NOTE: When InstructionProvider is used, static Instruction is ignored.
type InstructionProvider func(ctx context.Context, input *agent.AgentInput) (string, error)

// GlobalInstructionProvider generates global instructions shared across agent tree.
type GlobalInstructionProvider func(ctx context.Context) (string, error)

// ============================================================================
// Enhanced LLM Agent with ADK Features
// ============================================================================

// EnhancedLlmAgent provides LLM-based reasoning with full ADK compatibility:
// - InstructionProvider for dynamic instructions
// - GlobalInstruction for shared instructions across agent tree
// - Short-circuit callbacks for execution interception
// - OutputKey for automatic state persistence
type EnhancedLlmAgent struct {
	*agent.BaseAgent
	model         model.ModelProvider
	toolRegistry  *tools.ToolRegistry
	enabledTools  []string
	temperature   float64
	maxIterations int
	useReAct      bool

	// Static instruction (used if InstructionProvider is nil)
	instruction string

	// Dynamic instruction provider (ADK pattern)
	instructionProvider InstructionProvider

	// Global instruction shared across agent tree
	globalInstruction string

	// Dynamic global instruction provider
	globalInstructionProvider GlobalInstructionProvider

	// OutputKey: If set, agent output is saved to state with this key
	outputKey string

	// Standard callbacks (advisory)
	beforeModel []ModelCallback
	afterModel  []ModelOutputCallback
	beforeTool  []ToolCallback
	afterTool   []ToolResultCallback

	// Short-circuit callbacks (can intercept execution)
	shortCircuitBeforeModel []ShortCircuitModelCallback
	shortCircuitAfterModel  []ShortCircuitAfterModelCallback
	shortCircuitBeforeAgent []ShortCircuitAgentCallback
	shortCircuitAfterAgent  []ShortCircuitAfterAgentCallback
	shortCircuitBeforeTool  []ShortCircuitToolCallback
	shortCircuitAfterTool   []ShortCircuitAfterToolCallback
}

// EnhancedLlmAgentConfig configures an enhanced LLM agent with ADK features
type EnhancedLlmAgentConfig struct {
	Name          string
	ModelName     string
	Tools         []string
	Temperature   float64
	MaxIterations int
	UseReAct      bool
	Dependencies  []agent.Agent
	SubAgents     []agent.Agent

	// Instruction configuration (ADK pattern)
	Instruction               string              // Static instruction
	InstructionProvider       InstructionProvider // Dynamic instruction (takes precedence)
	GlobalInstruction         string              // Shared across agent tree
	GlobalInstructionProvider GlobalInstructionProvider

	// OutputKey: If set, saves agent output to state with this key
	OutputKey string

	// Standard lifecycle callbacks
	BeforeExecute []agent.AgentCallback
	AfterExecute  []agent.AgentOutputCallback
	OnError       []agent.ErrorCallback

	// Standard LLM callbacks (advisory)
	BeforeModel []ModelCallback
	AfterModel  []ModelOutputCallback
	BeforeTool  []ToolCallback
	AfterTool   []ToolResultCallback

	// Short-circuit callbacks (can intercept execution)
	ShortCircuitBeforeModel []ShortCircuitModelCallback
	ShortCircuitAfterModel  []ShortCircuitAfterModelCallback
	ShortCircuitBeforeAgent []ShortCircuitAgentCallback
	ShortCircuitAfterAgent  []ShortCircuitAfterAgentCallback
	ShortCircuitBeforeTool  []ShortCircuitToolCallback
	ShortCircuitAfterTool   []ShortCircuitAfterToolCallback
}

// NewEnhancedLlmAgent creates a new enhanced LLM agent with ADK features
func NewEnhancedLlmAgent(
	config EnhancedLlmAgentConfig,
	modelProvider model.ModelProvider,
	toolRegistry *tools.ToolRegistry,
) *EnhancedLlmAgent {
	if config.Temperature == 0 {
		config.Temperature = 0.7
	}
	if config.MaxIterations == 0 {
		config.MaxIterations = 5
	}

	enhancedAgent := &EnhancedLlmAgent{
		model:                     modelProvider,
		toolRegistry:              toolRegistry,
		enabledTools:              config.Tools,
		temperature:               config.Temperature,
		maxIterations:             config.MaxIterations,
		useReAct:                  config.UseReAct,
		instruction:               config.Instruction,
		instructionProvider:       config.InstructionProvider,
		globalInstruction:         config.GlobalInstruction,
		globalInstructionProvider: config.GlobalInstructionProvider,
		outputKey:                 config.OutputKey,

		// Standard callbacks
		beforeModel: config.BeforeModel,
		afterModel:  config.AfterModel,
		beforeTool:  config.BeforeTool,
		afterTool:   config.AfterTool,

		// Short-circuit callbacks
		shortCircuitBeforeModel: config.ShortCircuitBeforeModel,
		shortCircuitAfterModel:  config.ShortCircuitAfterModel,
		shortCircuitBeforeAgent: config.ShortCircuitBeforeAgent,
		shortCircuitAfterAgent:  config.ShortCircuitAfterAgent,
		shortCircuitBeforeTool:  config.ShortCircuitBeforeTool,
		shortCircuitAfterTool:   config.ShortCircuitAfterTool,
	}

	// Create executor
	executor := &enhancedLlmExecutor{agent: enhancedAgent}

	// Create base agent
	baseAgent := agent.NewAgent(agent.AgentConfig{
		Name:          config.Name,
		Executor:      executor,
		Dependencies:  config.Dependencies,
		SubAgents:     config.SubAgents,
		BeforeExecute: config.BeforeExecute,
		AfterExecute:  config.AfterExecute,
		OnError:       config.OnError,
	})

	enhancedAgent.BaseAgent = baseAgent
	return enhancedAgent
}

// enhancedLlmExecutor implements AgentExecutor with ADK features
type enhancedLlmExecutor struct {
	agent *EnhancedLlmAgent
}

func (e *enhancedLlmExecutor) Execute(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// Execute short-circuit before-agent callbacks
	if len(e.agent.shortCircuitBeforeAgent) > 0 {
		executor := NewShortCircuitExecutor(false)
		output, shouldShortCircuit, err := executor.ExecuteBeforeAgent(ctx, e.agent.shortCircuitBeforeAgent, ag, input)
		if err != nil {
			return nil, err
		}
		if shouldShortCircuit {
			// Apply after-agent callbacks to short-circuit output
			if len(e.agent.shortCircuitAfterAgent) > 0 {
				output, err = executor.ExecuteAfterAgent(ctx, e.agent.shortCircuitAfterAgent, ag, input, output)
				if err != nil {
					return nil, err
				}
			}
			return e.maybeStoreOutput(ctx, input, output), nil
		}
	}

	// Normal execution
	var output *agent.AgentOutput
	var err error

	if e.agent.useReAct {
		output, err = e.executeReAct(ctx, input)
	} else {
		output, err = e.executeSimple(ctx, input)
	}

	if err != nil {
		return nil, err
	}

	// Execute short-circuit after-agent callbacks
	if len(e.agent.shortCircuitAfterAgent) > 0 {
		executor := NewShortCircuitExecutor(false)
		output, err = executor.ExecuteAfterAgent(ctx, e.agent.shortCircuitAfterAgent, ag, input, output)
		if err != nil {
			return nil, err
		}
	}

	return e.maybeStoreOutput(ctx, input, output), nil
}

// resolveInstruction resolves the instruction using provider or static config
func (e *enhancedLlmExecutor) resolveInstruction(ctx context.Context, input *agent.AgentInput) (string, error) {
	var parts []string

	// Resolve global instruction first
	if e.agent.globalInstructionProvider != nil {
		globalInstr, err := e.agent.globalInstructionProvider(ctx)
		if err != nil {
			return "", fmt.Errorf("global instruction provider failed: %w", err)
		}
		if globalInstr != "" {
			parts = append(parts, globalInstr)
		}
	} else if e.agent.globalInstruction != "" {
		parts = append(parts, e.agent.globalInstruction)
	}

	// Resolve agent-specific instruction
	if e.agent.instructionProvider != nil {
		instr, err := e.agent.instructionProvider(ctx, input)
		if err != nil {
			return "", fmt.Errorf("instruction provider failed: %w", err)
		}
		if instr != "" {
			parts = append(parts, instr)
		}
	} else if e.agent.instruction != "" {
		parts = append(parts, e.agent.instruction)
	}

	return strings.Join(parts, "\n\n"), nil
}

// executeSimple performs single LLM call with optional tool use
func (e *enhancedLlmExecutor) executeSimple(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	startTime := time.Now()

	runtime.EmitMessage(ctx, "Analyzing request...", true)

	// Resolve instruction dynamically
	instruction, err := e.resolveInstruction(ctx, input)
	if err != nil {
		return nil, err
	}

	// Build prompt
	prompt := e.buildPrompt(instruction, input)

	// Prepare model input
	modelInput := &model.ModelInput{
		Prompt:      prompt,
		Temperature: e.agent.temperature,
		Context:     input.Context,
	}

	// Add tool schemas if tools are enabled
	if len(e.agent.enabledTools) > 0 {
		toolSchemas := e.getToolSchemas()
		if modelInput.Context == nil {
			modelInput.Context = make(map[string]interface{})
		}
		modelInput.Context["available_tools"] = toolSchemas
	}

	// Execute short-circuit before-model callbacks
	var modelOutput *model.ModelOutput
	if len(e.agent.shortCircuitBeforeModel) > 0 {
		executor := NewShortCircuitExecutor(false)
		shortCircuitOutput, shouldShortCircuit, scErr := executor.ExecuteBeforeModel(ctx, e.agent.shortCircuitBeforeModel, modelInput)
		if scErr != nil {
			return nil, scErr
		}
		if shouldShortCircuit {
			modelOutput = shortCircuitOutput
			// Skip to after-model processing
			goto afterModel
		}
	}

	// Execute standard before-model callbacks (advisory)
	if len(e.agent.beforeModel) > 0 {
		executor := NewModelCallbackExecutor(false)
		if err := executor.ExecuteBefore(ctx, e.agent.beforeModel, modelInput); err != nil {
			fmt.Printf("Warning: before_model callback: %v\n", err)
		}
	}

	runtime.EmitMessage(ctx, "Calling LLM...", true)

	// Call LLM
	modelOutput, err = e.agent.model.Generate(ctx, modelInput)
	if err != nil {
		runtime.EmitEvent(ctx, runtime.EventTypeError, map[string]interface{}{
			"error": err.Error(),
			"stage": "llm_generation",
		}, false)
		return nil, fmt.Errorf("LLM generation failed: %w", err)
	}

afterModel:
	// Execute short-circuit after-model callbacks
	if len(e.agent.shortCircuitAfterModel) > 0 {
		executor := NewShortCircuitExecutor(false)
		modelOutput, err = executor.ExecuteAfterModel(ctx, e.agent.shortCircuitAfterModel, modelInput, modelOutput)
		if err != nil {
			return nil, err
		}
	}

	// Execute standard after-model callbacks (advisory)
	if len(e.agent.afterModel) > 0 {
		executor := NewModelCallbackExecutor(false)
		if err := executor.ExecuteAfter(ctx, e.agent.afterModel, modelInput, modelOutput); err != nil {
			fmt.Printf("Warning: after_model callback: %v\n", err)
		}
	}

	// Check if LLM wants to use tools
	if toolCall, ok := e.extractToolCall(modelOutput.Text); ok {
		runtime.EmitToolCall(ctx, toolCall.ToolName, toolCall.Arguments)

		toolResult, err := e.executeTool(ctx, toolCall.ToolName, toolCall.Arguments)
		runtime.EmitToolResult(ctx, toolCall.ToolName, toolResult, err)

		if err != nil {
			return nil, fmt.Errorf("tool execution failed: %w", err)
		}

		runtime.EmitMessage(ctx, fmt.Sprintf("Tool %s executed, processing results...", toolCall.ToolName), true)

		// Call LLM again with tool result
		followUpPrompt := e.buildPromptWithToolResult(instruction, input, toolCall, toolResult)
		modelInput.Prompt = followUpPrompt

		// Execute short-circuit before-model for follow-up
		if len(e.agent.shortCircuitBeforeModel) > 0 {
			executor := NewShortCircuitExecutor(false)
			shortCircuitOutput, shouldShortCircuit, scErr := executor.ExecuteBeforeModel(ctx, e.agent.shortCircuitBeforeModel, modelInput)
			if scErr != nil {
				return nil, scErr
			}
			if shouldShortCircuit {
				modelOutput = shortCircuitOutput
				goto finalOutput
			}
		}

		modelOutput, err = e.agent.model.Generate(ctx, modelInput)
		if err != nil {
			return nil, fmt.Errorf("LLM generation after tool use failed: %w", err)
		}

		// Execute short-circuit after-model for follow-up
		if len(e.agent.shortCircuitAfterModel) > 0 {
			executor := NewShortCircuitExecutor(false)
			modelOutput, err = executor.ExecuteAfterModel(ctx, e.agent.shortCircuitAfterModel, modelInput, modelOutput)
			if err != nil {
				return nil, err
			}
		}
	}

finalOutput:
	runtime.EmitEventWithDelta(ctx, runtime.EventTypeMessage, modelOutput.Text, false, map[string]interface{}{
		"llm_response":   modelOutput.Text,
		"tokens_used":    modelOutput.TokensUsed,
		"execution_time": time.Since(startTime).Seconds(),
	})

	return &agent.AgentOutput{
		Result: modelOutput.Text,
		Metadata: map[string]interface{}{
			"model":          e.agent.model.Name(),
			"tokens_used":    modelOutput.TokensUsed,
			"execution_time": time.Since(startTime).Seconds(),
			"finish_reason":  modelOutput.FinishReason,
		},
	}, nil
}

// executeReAct implements the ReAct pattern with ADK features
func (e *enhancedLlmExecutor) executeReAct(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	startTime := time.Now()

	runtime.EmitMessage(ctx, "Starting ReAct reasoning loop...", true)

	// Resolve instruction dynamically
	instruction, err := e.resolveInstruction(ctx, input)
	if err != nil {
		return nil, err
	}

	conversationHistory := []string{}
	var finalAnswer string
	iterations := 0

	systemPrompt := e.buildReActSystemPrompt(instruction)
	conversationHistory = append(conversationHistory, systemPrompt)
	conversationHistory = append(conversationHistory, fmt.Sprintf("Task: %s", input.Instruction))

	for iterations < e.agent.maxIterations {
		iterations++

		runtime.EmitMessage(ctx, fmt.Sprintf("ReAct iteration %d/%d...", iterations, e.agent.maxIterations), true)

		prompt := strings.Join(conversationHistory, "\n\n")

		modelInput := &model.ModelInput{
			Prompt:      prompt,
			Temperature: e.agent.temperature,
			Context:     input.Context,
		}

		// Execute short-circuit before-model
		var modelOutput *model.ModelOutput
		if len(e.agent.shortCircuitBeforeModel) > 0 {
			executor := NewShortCircuitExecutor(false)
			shortCircuitOutput, shouldShortCircuit, scErr := executor.ExecuteBeforeModel(ctx, e.agent.shortCircuitBeforeModel, modelInput)
			if scErr != nil {
				return nil, scErr
			}
			if shouldShortCircuit {
				modelOutput = shortCircuitOutput
				goto processReActOutput
			}
		}

		// Standard before-model callbacks
		if len(e.agent.beforeModel) > 0 {
			executor := NewModelCallbackExecutor(false)
			if err := executor.ExecuteBefore(ctx, e.agent.beforeModel, modelInput); err != nil {
				fmt.Printf("Warning: before_model callback (ReAct iteration %d): %v\n", iterations, err)
			}
		}

		modelOutput, err = e.agent.model.Generate(ctx, modelInput)
		if err != nil {
			return nil, fmt.Errorf("ReAct iteration %d failed: %w", iterations, err)
		}

	processReActOutput:
		// Execute short-circuit after-model
		if len(e.agent.shortCircuitAfterModel) > 0 {
			executor := NewShortCircuitExecutor(false)
			modelOutput, err = executor.ExecuteAfterModel(ctx, e.agent.shortCircuitAfterModel, modelInput, modelOutput)
			if err != nil {
				return nil, err
			}
		}

		// Standard after-model callbacks
		if len(e.agent.afterModel) > 0 {
			executor := NewModelCallbackExecutor(false)
			if err := executor.ExecuteAfter(ctx, e.agent.afterModel, modelInput, modelOutput); err != nil {
				fmt.Printf("Warning: after_model callback (ReAct iteration %d): %v\n", iterations, err)
			}
		}

		conversationHistory = append(conversationHistory, modelOutput.Text)

		reactStep := e.parseReActStep(modelOutput.Text)

		if reactStep.IsFinal {
			finalAnswer = reactStep.Answer
			break
		}

		if reactStep.Action != "" {
			runtime.EmitToolCall(ctx, reactStep.Action, reactStep.ActionInput)

			toolResult, err := e.executeTool(ctx, reactStep.Action, reactStep.ActionInput)
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

	runtime.EmitEventWithDelta(ctx, runtime.EventTypeMessage, finalAnswer, false, map[string]interface{}{
		"react_answer":   finalAnswer,
		"iterations":     iterations,
		"execution_time": time.Since(startTime).Seconds(),
	})

	return &agent.AgentOutput{
		Result: finalAnswer,
		Metadata: map[string]interface{}{
			"model":          e.agent.model.Name(),
			"execution_time": time.Since(startTime).Seconds(),
			"iterations":     iterations,
			"pattern":        "ReAct",
			"conversation":   conversationHistory,
		},
	}, nil
}

// maybeStoreOutput stores output to state if OutputKey is configured
func (e *enhancedLlmExecutor) maybeStoreOutput(ctx context.Context, input *agent.AgentInput, output *agent.AgentOutput) *agent.AgentOutput {
	if e.agent.outputKey == "" || output == nil {
		return output
	}

	// Store result in state delta
	if output.Metadata == nil {
		output.Metadata = make(map[string]interface{})
	}

	// Create state delta with output key
	stateDelta := make(map[string]interface{})
	if result, ok := output.Result.(string); ok {
		stateDelta[e.agent.outputKey] = result
	} else {
		stateDelta[e.agent.outputKey] = output.Result
	}
	output.Metadata["state_delta"] = stateDelta

	return output
}

// buildPrompt combines instruction with user input
func (e *enhancedLlmExecutor) buildPrompt(instruction string, input *agent.AgentInput) string {
	var parts []string

	if instruction != "" {
		parts = append(parts, fmt.Sprintf("System: %s", instruction))
	}

	parts = append(parts, fmt.Sprintf("User: %s", input.Instruction))

	if prevResults, ok := input.Context["previous_results"].([]interface{}); ok && len(prevResults) > 0 {
		parts = append(parts, fmt.Sprintf("Previous Results: %v", prevResults))
	}

	return strings.Join(parts, "\n\n")
}

// buildReActSystemPrompt creates the ReAct pattern system prompt
func (e *enhancedLlmExecutor) buildReActSystemPrompt(instruction string) string {
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
Action Input: [Your complete answer]`, instruction, toolDescriptions)
}

// enhancedReActStep represents one step in ReAct reasoning
type enhancedReActStep struct {
	Thought     string
	Action      string
	ActionInput string
	Answer      string
	IsFinal     bool
}

// parseReActStep parses the ReAct format output
func (e *enhancedLlmExecutor) parseReActStep(output string) *enhancedReActStep {
	step := &enhancedReActStep{}

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

// toolCall represents a tool invocation request
type enhancedToolCall struct {
	ToolName  string
	Arguments string
}

// extractToolCall checks if output contains a tool call
func (e *enhancedLlmExecutor) extractToolCall(output string) (*enhancedToolCall, bool) {
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

		return &enhancedToolCall{
			ToolName:  toolName,
			Arguments: arguments,
		}, true
	}

	return nil, false
}

// executeTool executes a tool with short-circuit support
func (e *enhancedLlmExecutor) executeTool(ctx context.Context, toolName string, arguments string) (string, error) {
	if e.agent.toolRegistry == nil {
		return "", fmt.Errorf("tool registry not configured")
	}

	tool, err := e.agent.toolRegistry.Get(toolName)
	if err != nil {
		return "", fmt.Errorf("tool not found: %s", toolName)
	}

	// Check if tool is enabled
	enabled := false
	for _, enabledTool := range e.agent.enabledTools {
		if enabledTool == toolName {
			enabled = true
			break
		}
	}
	if !enabled {
		return "", fmt.Errorf("tool not enabled: %s", toolName)
	}

	// Parse arguments
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		args = map[string]interface{}{"input": arguments}
	}

	// Execute short-circuit before-tool callbacks
	if len(e.agent.shortCircuitBeforeTool) > 0 {
		executor := NewShortCircuitExecutor(false)
		result, shouldShortCircuit, scErr := executor.ExecuteBeforeTool(ctx, e.agent.shortCircuitBeforeTool, toolName, args)
		if scErr != nil {
			return "", scErr
		}
		if shouldShortCircuit {
			// Convert result to string
			if str, ok := result.(string); ok {
				return str, nil
			}
			resultBytes, _ := json.Marshal(result)
			return string(resultBytes), nil
		}
	}

	// Standard before-tool callbacks
	if len(e.agent.beforeTool) > 0 {
		executor := NewToolCallbackExecutor(false)
		if err := executor.ExecuteBefore(ctx, e.agent.beforeTool, toolName, args); err != nil {
			fmt.Printf("Warning: before_tool callback for %s: %v\n", toolName, err)
		}
	}

	// Execute tool
	result, err := tool.Handler(ctx, args)
	if err != nil {
		return "", fmt.Errorf("tool execution failed: %w", err)
	}

	// Execute short-circuit after-tool callbacks
	if len(e.agent.shortCircuitAfterTool) > 0 {
		executor := NewShortCircuitExecutor(false)
		result, err = executor.ExecuteAfterTool(ctx, e.agent.shortCircuitAfterTool, toolName, args, result)
		if err != nil {
			return "", err
		}
	}

	// Standard after-tool callbacks
	if len(e.agent.afterTool) > 0 {
		executor := NewToolCallbackExecutor(false)
		if err := executor.ExecuteAfter(ctx, e.agent.afterTool, toolName, args, result); err != nil {
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
func (e *enhancedLlmExecutor) buildPromptWithToolResult(instruction string, input *agent.AgentInput, toolCall *enhancedToolCall, result string) string {
	return fmt.Sprintf(`%s

Tool Call: %s
Arguments: %s
Result: %s

Based on the tool result above, please provide your response to the user's request.`,
		e.buildPrompt(instruction, input),
		toolCall.ToolName,
		toolCall.Arguments,
		result)
}

// getToolSchemas returns schemas for enabled tools
func (e *enhancedLlmExecutor) getToolSchemas() []map[string]interface{} {
	schemas := []map[string]interface{}{}

	if e.agent.toolRegistry == nil {
		return schemas
	}

	for _, toolName := range e.agent.enabledTools {
		tool, err := e.agent.toolRegistry.Get(toolName)
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
func (e *enhancedLlmExecutor) getToolDescriptions() string {
	if e.agent.toolRegistry == nil || len(e.agent.enabledTools) == 0 {
		return "No tools available"
	}

	descriptions := []string{}
	for _, toolName := range e.agent.enabledTools {
		tool, err := e.agent.toolRegistry.Get(toolName)
		if err != nil {
			continue
		}
		descriptions = append(descriptions, fmt.Sprintf("- %s: %s", tool.Name, tool.Description))
	}

	return strings.Join(descriptions, "\n")
}

// ============================================================================
// Builder Methods
// ============================================================================

// WithInstructionProvider sets a dynamic instruction provider
func (a *EnhancedLlmAgent) WithInstructionProvider(provider InstructionProvider) *EnhancedLlmAgent {
	newAgent := *a
	newAgent.instructionProvider = provider
	return &newAgent
}

// WithGlobalInstruction sets a global instruction
func (a *EnhancedLlmAgent) WithGlobalInstruction(instruction string) *EnhancedLlmAgent {
	newAgent := *a
	newAgent.globalInstruction = instruction
	return &newAgent
}

// WithOutputKey sets the output key for state persistence
func (a *EnhancedLlmAgent) WithOutputKey(key string) *EnhancedLlmAgent {
	newAgent := *a
	newAgent.outputKey = key
	return &newAgent
}

// WithShortCircuitBeforeModel adds short-circuit before-model callbacks
func (a *EnhancedLlmAgent) WithShortCircuitBeforeModel(callbacks ...ShortCircuitModelCallback) *EnhancedLlmAgent {
	newAgent := *a
	newAgent.shortCircuitBeforeModel = append(newAgent.shortCircuitBeforeModel, callbacks...)
	return &newAgent
}

// WithShortCircuitAfterModel adds short-circuit after-model callbacks
func (a *EnhancedLlmAgent) WithShortCircuitAfterModel(callbacks ...ShortCircuitAfterModelCallback) *EnhancedLlmAgent {
	newAgent := *a
	newAgent.shortCircuitAfterModel = append(newAgent.shortCircuitAfterModel, callbacks...)
	return &newAgent
}

// WithTools returns a new agent with specified tools enabled
func (a *EnhancedLlmAgent) WithTools(tools ...string) *EnhancedLlmAgent {
	newAgent := *a
	newAgent.enabledTools = tools
	return &newAgent
}

// WithTemperature returns a new agent with specified temperature
func (a *EnhancedLlmAgent) WithTemperature(temp float64) *EnhancedLlmAgent {
	newAgent := *a
	newAgent.temperature = temp
	return &newAgent
}

// WithReAct returns a new agent with ReAct pattern enabled/disabled
func (a *EnhancedLlmAgent) WithReAct(enabled bool) *EnhancedLlmAgent {
	newAgent := *a
	newAgent.useReAct = enabled
	return &newAgent
}
