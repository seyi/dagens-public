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

// Package runtime provides default request/response processors for LLM flows.
//
// This file implements default processors similar to Google's ADK Go:
// - Basic request processor (validates request)
// - Instructions processor (sets system instruction)
// - Contents processor (manages conversation history)
// - Identity processor (sets agent identity)
// - Auth preprocessor (handles authentication)
// - Agent transfer processor (handles agent delegation)
package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ============================================================================
// Default Request Processors
// ============================================================================

// DefaultRequestProcessors returns the default set of request processors
// matching Google ADK Go's default processor chain
func DefaultRequestProcessors() []RequestProcessorFunc {
	return []RequestProcessorFunc{
		BasicRequestProcessor,
		InstructionsRequestProcessor,
		IdentityRequestProcessor,
		ContentsRequestProcessor,
		AgentTransferRequestProcessor,
	}
}

// DefaultResponseProcessors returns the default set of response processors
func DefaultResponseProcessors() []ResponseProcessorFunc {
	return []ResponseProcessorFunc{
		BasicResponseProcessor,
	}
}

// RequestProcessorFunc is a function-based request processor
type RequestProcessorFunc func(ctx context.Context, invCtx *InvocationContext, req *LlmRequest) error

// ResponseProcessorFunc is a function-based response processor
type ResponseProcessorFunc func(ctx context.Context, invCtx *InvocationContext, req *LlmRequest, resp *LlmResponse) error

// FuncRequestProcessor adapts a function to the RequestProcessor interface
type FuncRequestProcessor struct {
	ProcessFn func(ctx context.Context, invCtx *InvocationContext, req *LlmRequest) error
}

func (p *FuncRequestProcessor) ProcessRequest(ctx context.Context, invCtx *InvocationContext, req *LlmRequest) <-chan *AsyncEvent {
	ch := make(chan *AsyncEvent)
	go func() {
		defer close(ch)
		if err := p.ProcessFn(ctx, invCtx, req); err != nil {
			errorEvent := NewAsyncEvent(EventTypeError, err.Error(), "processor")
			errorEvent.Error = err
			ch <- errorEvent
		}
	}()
	return ch
}

// FuncResponseProcessor adapts a function to the ResponseProcessor interface
type FuncResponseProcessor struct {
	ProcessFn func(ctx context.Context, invCtx *InvocationContext, resp *LlmResponse) error
}

func (p *FuncResponseProcessor) ProcessResponse(ctx context.Context, invCtx *InvocationContext, resp *LlmResponse) <-chan *AsyncEvent {
	ch := make(chan *AsyncEvent)
	go func() {
		defer close(ch)
		if err := p.ProcessFn(ctx, invCtx, resp); err != nil {
			errorEvent := NewAsyncEvent(EventTypeError, err.Error(), "processor")
			errorEvent.Error = err
			ch <- errorEvent
		}
	}()
	return ch
}

// ============================================================================
// Basic Request Processor
// ============================================================================

// BasicRequestProcessor validates and initializes the LLM request.
// This is similar to ADK's basicRequestProcessor.
func BasicRequestProcessor(ctx context.Context, invCtx *InvocationContext, req *LlmRequest) error {
	// Initialize tools dict if not set
	if req.ToolsDict == nil {
		req.ToolsDict = make(map[string]ToolDefinition)
	}

	// Populate tools dict from tools array
	for _, tool := range req.Tools {
		req.ToolsDict[tool.Name] = tool
	}

	// Set default config if not provided
	if req.Config == nil {
		req.Config = &GenerateConfig{
			Temperature:     0.7,
			MaxOutputTokens: 2048,
		}
	}

	// Initialize labels for billing/tracking
	if req.Labels == nil {
		req.Labels = make(map[string]string)
	}

	// Add agent name label (ADK pattern for billing)
	if invCtx.AgentName != "" {
		req.Labels["agent_name"] = invCtx.AgentName
	}

	// Add session and invocation IDs
	req.Labels["session_id"] = invCtx.SessionID
	req.Labels["invocation_id"] = invCtx.InvocationID

	return nil
}

// ============================================================================
// Instructions Request Processor
// ============================================================================

// InstructionsRequestProcessor sets the system instruction.
// This is similar to ADK's instructionsRequestProcessor.
func InstructionsRequestProcessor(ctx context.Context, invCtx *InvocationContext, req *LlmRequest) error {
	// Check if instruction is provided in input
	if invCtx.Input != nil && invCtx.Input.Instruction != "" {
		// If no system instruction set, use the input instruction
		if req.SystemInstruction == "" {
			req.SystemInstruction = invCtx.Input.Instruction
		}
	}

	// Check for instruction in state
	if instruction, exists := invCtx.Get("system_instruction"); exists {
		if instructionStr, ok := instruction.(string); ok && instructionStr != "" {
			req.SystemInstruction = instructionStr
		}
	}

	// Process instruction templates (replace variables)
	if req.SystemInstruction != "" {
		req.SystemInstruction = processInstructionTemplate(req.SystemInstruction, invCtx)
	}

	return nil
}

// processInstructionTemplate replaces template variables in instructions
func processInstructionTemplate(instruction string, invCtx *InvocationContext) string {
	// Replace common template variables
	result := instruction

	// Replace {{agent_name}} with actual agent name
	result = strings.ReplaceAll(result, "{{agent_name}}", invCtx.AgentName)

	// Replace {{session_id}} with session ID
	result = strings.ReplaceAll(result, "{{session_id}}", invCtx.SessionID)

	// Replace {{user_id}} with user ID
	result = strings.ReplaceAll(result, "{{user_id}}", invCtx.UserID)

	// Replace {{date}} with current date
	result = strings.ReplaceAll(result, "{{date}}", time.Now().Format("2006-01-02"))

	// Replace {{time}} with current time
	result = strings.ReplaceAll(result, "{{time}}", time.Now().Format("15:04:05"))

	// Replace state variables: {{state.key}}
	for key, value := range invCtx.State {
		placeholder := fmt.Sprintf("{{state.%s}}", key)
		result = strings.ReplaceAll(result, placeholder, fmt.Sprintf("%v", value))
	}

	return result
}

// ============================================================================
// Identity Request Processor
// ============================================================================

// IdentityRequestProcessor adds agent identity context.
// This is similar to ADK's identityRequestProcessor.
func IdentityRequestProcessor(ctx context.Context, invCtx *InvocationContext, req *LlmRequest) error {
	// Add identity context if agent name is set
	if invCtx.AgentName != "" {
		// Check if identity prefix should be added to system instruction
		addIdentity, _ := invCtx.Get("add_identity_to_instruction")
		if addIdentity == true || addIdentity == "true" {
			identityPrefix := fmt.Sprintf("You are %s. ", invCtx.AgentName)
			if !strings.HasPrefix(req.SystemInstruction, identityPrefix) {
				req.SystemInstruction = identityPrefix + req.SystemInstruction
			}
		}
	}

	return nil
}

// ============================================================================
// Contents Request Processor
// ============================================================================

// ContentsRequestProcessor manages conversation history.
// This is similar to ADK's ContentsRequestProcessor.
func ContentsRequestProcessor(ctx context.Context, invCtx *InvocationContext, req *LlmRequest) error {
	// Load conversation history from session if available
	if invCtx.Services != nil && invCtx.Services.SessionService != nil {
		session, err := invCtx.Services.SessionService.GetSession(ctx, invCtx.SessionID)
		if err == nil && session != nil {
			// Load history from session state
			if history, exists := session.State["conversation_history"]; exists {
				if historyContents, ok := history.([]Content); ok {
					// Prepend history to current contents
					req.Contents = append(historyContents, req.Contents...)
				}
			}
		}
	}

	// Add user message if provided and not already in contents
	if invCtx.Input != nil && invCtx.Input.Instruction != "" {
		hasUserMessage := false
		for _, content := range req.Contents {
			if content.Role == "user" {
				hasUserMessage = true
				break
			}
		}

		if !hasUserMessage && req.SystemInstruction != invCtx.Input.Instruction {
			// Add user instruction as a message
			req.Contents = append(req.Contents, Content{
				Role: "user",
				Parts: []Part{
					{Text: invCtx.Input.Instruction},
				},
			})
		}
	}

	// Apply content window/truncation if needed
	maxContents, _ := invCtx.Get("max_conversation_history")
	if maxContentInt, ok := maxContents.(int); ok && maxContentInt > 0 {
		if len(req.Contents) > maxContentInt {
			// Keep most recent contents
			req.Contents = req.Contents[len(req.Contents)-maxContentInt:]
		}
	}

	return nil
}

// ============================================================================
// Agent Transfer Request Processor
// ============================================================================

// AgentTransferRequestProcessor handles agent-to-agent transfer setup.
// This is similar to ADK's AgentTransferRequestProcessor.
func AgentTransferRequestProcessor(ctx context.Context, invCtx *InvocationContext, req *LlmRequest) error {
	// Check if transfer tools should be added
	enableTransfer, _ := invCtx.Get("enable_agent_transfer")
	if enableTransfer != true && enableTransfer != "true" {
		return nil
	}

	// Check if transfer_to_agent tool already exists
	for _, tool := range req.Tools {
		if tool.Name == "transfer_to_agent" {
			return nil // Already has transfer tool
		}
	}

	// Get available transfer targets
	transferTargets := getTransferTargets(invCtx)
	if len(transferTargets) == 0 {
		return nil // No agents to transfer to
	}

	// Build the transfer tool with available agents
	agentDescriptions := make([]string, 0, len(transferTargets))
	for _, target := range transferTargets {
		agentDescriptions = append(agentDescriptions,
			fmt.Sprintf("- %s: %s", target.Name, target.Description))
	}

	transferTool := ToolDefinition{
		Name: "transfer_to_agent",
		Description: fmt.Sprintf(
			"Transfer execution to another specialized agent. Available agents:\n%s",
			strings.Join(agentDescriptions, "\n"),
		),
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"agent_name": map[string]interface{}{
					"type":        "string",
					"description": "Name of the agent to transfer to",
					"enum":        getAgentNames(transferTargets),
				},
				"reason": map[string]interface{}{
					"type":        "string",
					"description": "Reason for the transfer",
				},
			},
			"required": []string{"agent_name"},
		},
	}

	req.Tools = append(req.Tools, transferTool)
	req.ToolsDict["transfer_to_agent"] = transferTool

	return nil
}

// TransferTarget represents an agent that can receive transfers
type TransferTarget struct {
	Name        string
	Description string
}

// getTransferTargets retrieves available transfer targets from context
func getTransferTargets(invCtx *InvocationContext) []TransferTarget {
	var targets []TransferTarget

	// Check for transfer targets in state
	if targetList, exists := invCtx.Get("transfer_targets"); exists {
		if targetsSlice, ok := targetList.([]TransferTarget); ok {
			return targetsSlice
		}
		// Try to convert from interface slice
		if targetsInterface, ok := targetList.([]interface{}); ok {
			for _, t := range targetsInterface {
				if targetMap, ok := t.(map[string]interface{}); ok {
					name, _ := targetMap["name"].(string)
					desc, _ := targetMap["description"].(string)
					if name != "" {
						targets = append(targets, TransferTarget{
							Name:        name,
							Description: desc,
						})
					}
				}
			}
		}
	}

	return targets
}

func getAgentNames(targets []TransferTarget) []string {
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.Name
	}
	return names
}

// ============================================================================
// Auth Preprocessor
// ============================================================================

// AuthRequestProcessor handles authentication preprocessing.
// This is similar to ADK's authPreprocessor.
func AuthRequestProcessor(ctx context.Context, invCtx *InvocationContext, req *LlmRequest) error {
	// Check for pending auth requests
	if pendingAuth, exists := invCtx.Get("pending_auth"); exists {
		if authConfig, ok := pendingAuth.(map[string]interface{}); ok {
			// Add auth context to labels
			if authType, ok := authConfig["type"].(string); ok {
				req.Labels["auth_type"] = authType
			}
			if authScope, ok := authConfig["scope"].(string); ok {
				req.Labels["auth_scope"] = authScope
			}
		}
	}

	return nil
}

// ============================================================================
// Basic Response Processor
// ============================================================================

// BasicResponseProcessor performs basic response processing.
func BasicResponseProcessor(ctx context.Context, invCtx *InvocationContext, req *LlmRequest, resp *LlmResponse) error {
	// Validate response
	if resp == nil {
		return fmt.Errorf("nil response from LLM")
	}

	// Check for error response
	if resp.ErrorCode != "" {
		return fmt.Errorf("LLM error [%s]: %s", resp.ErrorCode, resp.ErrorMessage)
	}

	// Populate function call IDs if missing
	if resp.Content != nil {
		for i := range resp.Content.Parts {
			if resp.Content.Parts[i].FunctionCall != nil {
				fc := resp.Content.Parts[i].FunctionCall
				if fc.ID == "" {
					fc.ID = generateID("fc")
				}
			}
		}
	}

	// Track token usage in state
	if resp.UsageMetadata != nil {
		invCtx.Set("last_prompt_tokens", resp.UsageMetadata.PromptTokens)
		invCtx.Set("last_completion_tokens", resp.UsageMetadata.CompletionTokens)
		invCtx.Set("last_total_tokens", resp.UsageMetadata.TotalTokens)

		// Accumulate total tokens
		totalPrompt, _ := invCtx.Get("total_prompt_tokens")
		totalCompletion, _ := invCtx.Get("total_completion_tokens")

		if tp, ok := totalPrompt.(int); ok {
			invCtx.Set("total_prompt_tokens", tp+resp.UsageMetadata.PromptTokens)
		} else {
			invCtx.Set("total_prompt_tokens", resp.UsageMetadata.PromptTokens)
		}

		if tc, ok := totalCompletion.(int); ok {
			invCtx.Set("total_completion_tokens", tc+resp.UsageMetadata.CompletionTokens)
		} else {
			invCtx.Set("total_completion_tokens", resp.UsageMetadata.CompletionTokens)
		}
	}

	return nil
}

// ============================================================================
// Processor Chain Builder
// ============================================================================

// ProcessorChain combines multiple processors into a chain
type ProcessorChain struct {
	requestProcessors  []RequestProcessorFunc
	responseProcessors []ResponseProcessorFunc
}

// NewProcessorChain creates a new processor chain
func NewProcessorChain() *ProcessorChain {
	return &ProcessorChain{
		requestProcessors:  make([]RequestProcessorFunc, 0),
		responseProcessors: make([]ResponseProcessorFunc, 0),
	}
}

// WithDefaults adds default processors
func (c *ProcessorChain) WithDefaults() *ProcessorChain {
	c.requestProcessors = append(c.requestProcessors, DefaultRequestProcessors()...)
	c.responseProcessors = append(c.responseProcessors, DefaultResponseProcessors()...)
	return c
}

// AddRequestProcessor adds a request processor
func (c *ProcessorChain) AddRequestProcessor(p RequestProcessorFunc) *ProcessorChain {
	c.requestProcessors = append(c.requestProcessors, p)
	return c
}

// AddResponseProcessor adds a response processor
func (c *ProcessorChain) AddResponseProcessor(p ResponseProcessorFunc) *ProcessorChain {
	c.responseProcessors = append(c.responseProcessors, p)
	return c
}

// ProcessRequest runs all request processors in order
func (c *ProcessorChain) ProcessRequest(ctx context.Context, invCtx *InvocationContext, req *LlmRequest) error {
	for _, processor := range c.requestProcessors {
		if err := processor(ctx, invCtx, req); err != nil {
			return fmt.Errorf("request processor error: %w", err)
		}
	}
	return nil
}

// ProcessResponse runs all response processors in order
func (c *ProcessorChain) ProcessResponse(ctx context.Context, invCtx *InvocationContext, req *LlmRequest, resp *LlmResponse) error {
	for _, processor := range c.responseProcessors {
		if err := processor(ctx, invCtx, req, resp); err != nil {
			return fmt.Errorf("response processor error: %w", err)
		}
	}
	return nil
}

// Build creates RequestProcessor and ResponseProcessor interfaces
func (c *ProcessorChain) Build() ([]RequestProcessor, []ResponseProcessor) {
	reqProcessors := make([]RequestProcessor, len(c.requestProcessors))
	respProcessors := make([]ResponseProcessor, len(c.responseProcessors))

	for i, p := range c.requestProcessors {
		processor := p // capture for closure
		reqProcessors[i] = &FuncRequestProcessor{
			ProcessFn: processor,
		}
	}

	for i, p := range c.responseProcessors {
		processor := p // capture for closure
		respProcessors[i] = &FuncResponseProcessor{
			ProcessFn: func(ctx context.Context, invCtx *InvocationContext, resp *LlmResponse) error {
				return processor(ctx, invCtx, nil, resp)
			},
		}
	}

	return reqProcessors, respProcessors
}
