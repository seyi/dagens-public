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

// Package runtime provides advanced ADK features for LLM agents.
//
// This file implements features inspired by Google's ADK Go that we were missing:
// - InstructionProvider for dynamic instructions
// - GlobalInstruction for shared instructions across agent tree
// - InputSchema/OutputSchema validation
// - State key prefixes (app:, user:, temp:)
// - OutputKey for saving agent output to state
// - Transfer controls (DisallowTransferToParent/Peers)
// - Branch tracking for parallel agent conversation isolation
// - ArtifactDelta for tracking artifact versions
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ============================================================================
// State Key Prefixes (ADK Pattern)
// ============================================================================

// State key prefixes for different scopes
const (
	// KeyPrefixApp is for app-level state shared across all users and sessions
	KeyPrefixApp = "app:"
	// KeyPrefixUser is for user-level state shared across sessions for a user
	KeyPrefixUser = "user:"
	// KeyPrefixTemp is for temporary state discarded after invocation
	KeyPrefixTemp = "temp:"
)

// ScopedState wraps state operations with scope awareness
type ScopedState struct {
	state     map[string]interface{}
	appState  map[string]interface{} // Shared across all users
	userState map[string]interface{} // Shared for this user
	tempState map[string]interface{} // Discarded after invocation
}

// NewScopedState creates a new scoped state manager
func NewScopedState() *ScopedState {
	return &ScopedState{
		state:     make(map[string]interface{}),
		appState:  make(map[string]interface{}),
		userState: make(map[string]interface{}),
		tempState: make(map[string]interface{}),
	}
}

// Get retrieves a value, respecting scope prefixes
func (s *ScopedState) Get(key string) (interface{}, bool) {
	if strings.HasPrefix(key, KeyPrefixApp) {
		val, ok := s.appState[strings.TrimPrefix(key, KeyPrefixApp)]
		return val, ok
	}
	if strings.HasPrefix(key, KeyPrefixUser) {
		val, ok := s.userState[strings.TrimPrefix(key, KeyPrefixUser)]
		return val, ok
	}
	if strings.HasPrefix(key, KeyPrefixTemp) {
		val, ok := s.tempState[strings.TrimPrefix(key, KeyPrefixTemp)]
		return val, ok
	}
	val, ok := s.state[key]
	return val, ok
}

// Set stores a value, respecting scope prefixes
func (s *ScopedState) Set(key string, value interface{}) {
	if strings.HasPrefix(key, KeyPrefixApp) {
		s.appState[strings.TrimPrefix(key, KeyPrefixApp)] = value
		return
	}
	if strings.HasPrefix(key, KeyPrefixUser) {
		s.userState[strings.TrimPrefix(key, KeyPrefixUser)] = value
		return
	}
	if strings.HasPrefix(key, KeyPrefixTemp) {
		s.tempState[strings.TrimPrefix(key, KeyPrefixTemp)] = value
		return
	}
	s.state[key] = value
}

// ClearTemp clears all temporary state
func (s *ScopedState) ClearTemp() {
	s.tempState = make(map[string]interface{})
}

// All returns all state (merging all scopes)
func (s *ScopedState) All() map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range s.state {
		result[k] = v
	}
	for k, v := range s.appState {
		result[KeyPrefixApp+k] = v
	}
	for k, v := range s.userState {
		result[KeyPrefixUser+k] = v
	}
	for k, v := range s.tempState {
		result[KeyPrefixTemp+k] = v
	}
	return result
}

// ErrStateKeyNotExist is returned when a state key doesn't exist
var ErrStateKeyNotExist = errors.New("state key does not exist")

// ============================================================================
// Instruction Provider (ADK Pattern)
// ============================================================================

// InstructionProvider generates instructions dynamically based on context
type InstructionProvider func(ctx *ReadonlyContext) (string, error)

// InstructionConfig configures instruction handling for an agent
type InstructionConfig struct {
	// Static instruction template
	Instruction string
	// Dynamic instruction provider (takes precedence over Instruction)
	InstructionProvider InstructionProvider
	// Global instruction shared across agent tree
	GlobalInstruction string
	// Dynamic global instruction provider
	GlobalInstructionProvider InstructionProvider
}

// ResolveInstruction resolves the instruction using provider or static config
func (c *InstructionConfig) ResolveInstruction(ctx *ReadonlyContext) (string, error) {
	var instruction string
	var err error

	// Use provider if available
	if c.InstructionProvider != nil {
		instruction, err = c.InstructionProvider(ctx)
		if err != nil {
			return "", fmt.Errorf("instruction provider error: %w", err)
		}
	} else {
		instruction = c.Instruction
	}

	// Process template variables
	instruction = processInstructionVariables(instruction, ctx)

	return instruction, nil
}

// ResolveGlobalInstruction resolves the global instruction
func (c *InstructionConfig) ResolveGlobalInstruction(ctx *ReadonlyContext) (string, error) {
	if c.GlobalInstructionProvider != nil {
		return c.GlobalInstructionProvider(ctx)
	}
	return c.GlobalInstruction, nil
}

// Variable pattern: {key_name} or {artifact.key_name} or {key_name?} for optional
var variablePattern = regexp.MustCompile(`\{([a-zA-Z_][a-zA-Z0-9_.]*\??)\}`)

// processInstructionVariables replaces template variables in instructions
func processInstructionVariables(instruction string, ctx *ReadonlyContext) string {
	return variablePattern.ReplaceAllStringFunc(instruction, func(match string) string {
		// Extract variable name (remove braces)
		varName := match[1 : len(match)-1]
		optional := false

		// Check for optional marker
		if strings.HasSuffix(varName, "?") {
			optional = true
			varName = varName[:len(varName)-1]
		}

		// Check for artifact prefix
		if strings.HasPrefix(varName, "artifact.") {
			// TODO: Load artifact content using artifact service
			// artifactName := strings.TrimPrefix(varName, "artifact.")
			if optional {
				return ""
			}
			return match // Keep as-is if not found
		}

		// Look up in state
		if ctx != nil && ctx.invCtx != nil {
			if val, exists := ctx.invCtx.Get(varName); exists {
				return fmt.Sprintf("%v", val)
			}
		}

		if optional {
			return ""
		}
		return match // Keep as-is if not found
	})
}

// ============================================================================
// Input/Output Schema Validation (ADK Pattern)
// ============================================================================

// Schema defines the structure for input/output validation
type Schema struct {
	Type        string             `json:"type"`
	Description string             `json:"description,omitempty"`
	Properties  map[string]*Schema `json:"properties,omitempty"`
	Required    []string           `json:"required,omitempty"`
	Items       *Schema            `json:"items,omitempty"`
	Enum        []string           `json:"enum,omitempty"`
	Format      string             `json:"format,omitempty"`
	Default     interface{}        `json:"default,omitempty"`
}

// SchemaConfig configures input/output validation for an agent
type SchemaConfig struct {
	// InputSchema validates input when agent is used as a tool
	InputSchema *Schema
	// OutputSchema constrains agent responses (disables tool use when set)
	OutputSchema *Schema
}

// ValidateInput validates input against the schema
func (c *SchemaConfig) ValidateInput(input map[string]interface{}) error {
	if c.InputSchema == nil {
		return nil
	}
	return validateAgainstSchema(input, c.InputSchema)
}

// ValidateOutput validates output against the schema
func (c *SchemaConfig) ValidateOutput(output interface{}) error {
	if c.OutputSchema == nil {
		return nil
	}
	if outputMap, ok := output.(map[string]interface{}); ok {
		return validateAgainstSchema(outputMap, c.OutputSchema)
	}
	return nil
}

func validateAgainstSchema(data map[string]interface{}, schema *Schema) error {
	// Check required fields
	for _, required := range schema.Required {
		if _, exists := data[required]; !exists {
			return fmt.Errorf("missing required field: %s", required)
		}
	}

	// Validate property types
	for key, propSchema := range schema.Properties {
		if val, exists := data[key]; exists {
			if err := validateValue(val, propSchema); err != nil {
				return fmt.Errorf("field %s: %w", key, err)
			}
		}
	}

	return nil
}

func validateValue(val interface{}, schema *Schema) error {
	switch schema.Type {
	case "string":
		if _, ok := val.(string); !ok {
			return fmt.Errorf("expected string, got %T", val)
		}
		// Check enum
		if len(schema.Enum) > 0 {
			strVal := val.(string)
			found := false
			for _, e := range schema.Enum {
				if e == strVal {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("value %q not in enum %v", strVal, schema.Enum)
			}
		}
	case "number", "integer":
		switch val.(type) {
		case int, int32, int64, float32, float64:
			// Valid
		default:
			return fmt.Errorf("expected number, got %T", val)
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("expected boolean, got %T", val)
		}
	case "array":
		arr, ok := val.([]interface{})
		if !ok {
			return fmt.Errorf("expected array, got %T", val)
		}
		if schema.Items != nil {
			for i, item := range arr {
				if err := validateValue(item, schema.Items); err != nil {
					return fmt.Errorf("item %d: %w", i, err)
				}
			}
		}
	case "object":
		obj, ok := val.(map[string]interface{})
		if !ok {
			return fmt.Errorf("expected object, got %T", val)
		}
		if err := validateAgainstSchema(obj, schema); err != nil {
			return err
		}
	}
	return nil
}

// ============================================================================
// Transfer Controls (ADK Pattern)
// ============================================================================

// TransferConfig controls agent transfer behavior
type TransferConfig struct {
	// DisallowTransferToParent prevents transfer to parent agent
	DisallowTransferToParent bool
	// DisallowTransferToPeers prevents transfer to sibling agents
	DisallowTransferToPeers bool
	// AllowedTransferTargets limits which agents can receive transfers
	AllowedTransferTargets []string
	// TransferScope defines the transfer scope (tree, siblings, children)
	TransferScope TransferScope
}

// TransferScope defines allowed transfer directions
type TransferScope string

const (
	// TransferScopeAll allows transfer to any agent in the tree
	TransferScopeAll TransferScope = "all"
	// TransferScopeChildren allows transfer only to sub-agents
	TransferScopeChildren TransferScope = "children"
	// TransferScopeSiblings allows transfer only to peer agents
	TransferScopeSiblings TransferScope = "siblings"
	// TransferScopeNone disables agent transfer
	TransferScopeNone TransferScope = "none"
)

// CanTransferTo checks if transfer to target agent is allowed
func (c *TransferConfig) CanTransferTo(current, target string, relationship AgentRelationship) bool {
	// Check scope
	switch c.TransferScope {
	case TransferScopeNone:
		return false
	case TransferScopeChildren:
		if relationship != RelationshipChild {
			return false
		}
	case TransferScopeSiblings:
		if relationship != RelationshipSibling {
			return false
		}
	}

	// Check parent restriction
	if c.DisallowTransferToParent && relationship == RelationshipParent {
		return false
	}

	// Check peer restriction
	if c.DisallowTransferToPeers && relationship == RelationshipSibling {
		return false
	}

	// Check allowed targets
	if len(c.AllowedTransferTargets) > 0 {
		for _, allowed := range c.AllowedTransferTargets {
			if allowed == target {
				return true
			}
		}
		return false
	}

	return true
}

// AgentRelationship describes the relationship between two agents
type AgentRelationship string

const (
	RelationshipParent  AgentRelationship = "parent"
	RelationshipChild   AgentRelationship = "child"
	RelationshipSibling AgentRelationship = "sibling"
	RelationshipOther   AgentRelationship = "other"
)

// ============================================================================
// Branch Tracking (ADK Pattern for Parallel Agents)
// ============================================================================

// Branch represents a conversation branch for parallel agent isolation
type Branch struct {
	// Path is the branch path (e.g., "agent1.agent2.agent3")
	Path string
	// ParentBranch is the parent branch path
	ParentBranch string
}

// NewBranch creates a new branch
func NewBranch(path string) *Branch {
	parts := strings.Split(path, ".")
	parentPath := ""
	if len(parts) > 1 {
		parentPath = strings.Join(parts[:len(parts)-1], ".")
	}
	return &Branch{
		Path:         path,
		ParentBranch: parentPath,
	}
}

// Child creates a child branch
func (b *Branch) Child(agentName string) *Branch {
	if b.Path == "" {
		return NewBranch(agentName)
	}
	return NewBranch(b.Path + "." + agentName)
}

// IsParentOf checks if this branch is a parent of another
func (b *Branch) IsParentOf(other *Branch) bool {
	return strings.HasPrefix(other.Path, b.Path+".")
}

// IsSiblingOf checks if this branch is a sibling of another
func (b *Branch) IsSiblingOf(other *Branch) bool {
	return b.ParentBranch == other.ParentBranch && b.Path != other.Path
}

// BranchedInvocationContext extends InvocationContext with branch awareness
type BranchedInvocationContext struct {
	*InvocationContext
	Branch *Branch
}

// NewBranchedInvocationContext creates a branched context
func NewBranchedInvocationContext(ctx context.Context, input interface{}, branch *Branch) *BranchedInvocationContext {
	// Create a basic invocation context
	invCtx := &InvocationContext{
		InvocationID: generateID("inv"),
		State:        make(map[string]interface{}),
	}
	return &BranchedInvocationContext{
		InvocationContext: invCtx,
		Branch:            branch,
	}
}

// ============================================================================
// Output Key (ADK Pattern)
// ============================================================================

// OutputKeyConfig configures automatic output saving to state
type OutputKeyConfig struct {
	// OutputKey is the state key where agent output is saved
	OutputKey string
	// SavePartialOutputs saves streaming chunks if true
	SavePartialOutputs bool
}

// SaveOutput saves agent output to state
func (c *OutputKeyConfig) SaveOutput(invCtx *InvocationContext, output interface{}) {
	if c.OutputKey == "" {
		return
	}

	// Convert output to storable format
	var outputValue interface{}
	switch v := output.(type) {
	case string:
		outputValue = v
	case *AsyncEvent:
		if v.Content != nil {
			outputValue = v.Content
		}
	default:
		// Try JSON encoding
		if data, err := json.Marshal(output); err == nil {
			outputValue = string(data)
		}
	}

	if outputValue != nil {
		invCtx.Set(c.OutputKey, outputValue)
	}
}

// ============================================================================
// Artifact Delta (ADK Pattern)
// ============================================================================

// ArtifactVersionDelta tracks artifact version changes
type ArtifactVersionDelta map[string]int64

// ExtendedEventActions adds artifact tracking to EventActions
type ExtendedEventActions struct {
	*EventActions
	// ArtifactDelta tracks artifact version updates
	ArtifactDelta ArtifactVersionDelta
	// SkipSummarization prevents function response summarization
	SkipSummarization bool
	// Escalate indicates escalation to higher-level agent
	Escalate bool
}

// NewExtendedEventActions creates extended event actions
func NewExtendedEventActions() *ExtendedEventActions {
	return &ExtendedEventActions{
		EventActions: &EventActions{
			StateDelta: make(map[string]interface{}),
		},
		ArtifactDelta: make(ArtifactVersionDelta),
	}
}

// RecordArtifactUpdate records an artifact version update
func (a *ExtendedEventActions) RecordArtifactUpdate(filename string, version int64) {
	a.ArtifactDelta[filename] = version
}

// ============================================================================
// Include Contents Control (ADK Pattern)
// ============================================================================

// IncludeContents controls what conversation history the agent receives
type IncludeContents string

const (
	// IncludeContentsNone - agent only sees current turn
	IncludeContentsNone IncludeContents = "none"
	// IncludeContentsDefault - agent receives relevant history
	IncludeContentsDefault IncludeContents = "default"
	// IncludeContentsAll - agent receives all history
	IncludeContentsAll IncludeContents = "all"
	// IncludeContentsBranch - agent only sees its branch history
	IncludeContentsBranch IncludeContents = "branch"
)

// ContentsFilter filters conversation contents based on include mode
type ContentsFilter struct {
	Mode   IncludeContents
	Branch *Branch
}

// FilterContents filters contents based on the mode
func (f *ContentsFilter) FilterContents(contents []Content, currentBranch string) []Content {
	switch f.Mode {
	case IncludeContentsNone:
		return nil
	case IncludeContentsAll:
		return contents
	case IncludeContentsBranch:
		if f.Branch == nil {
			return contents
		}
		// Filter to only include contents from this branch
		filtered := make([]Content, 0)
		for _, c := range contents {
			// TODO: Add branch tracking to Content
			filtered = append(filtered, c)
		}
		return filtered
	default:
		return contents
	}
}

// ============================================================================
// Advanced LLM Agent Configuration
// ============================================================================

// AdvancedLLMAgentConfig provides full ADK-compatible configuration
type AdvancedLLMAgentConfig struct {
	// Basic config
	Name        string
	Description string

	// Instructions
	InstructionConfig *InstructionConfig

	// Schema validation
	SchemaConfig *SchemaConfig

	// Transfer controls
	TransferConfig *TransferConfig

	// Output handling
	OutputKeyConfig *OutputKeyConfig

	// Contents control
	IncludeContents IncludeContents

	// Sub-agents
	SubAgents []IteratorAgent

	// Model and tools
	Model interface{} // LLM model
	Tools []ToolDefinition

	// Callbacks
	BeforeAgentCallbacks []BeforeAgentCallback
	AfterAgentCallbacks  []AfterAgentCallback
	BeforeModelCallbacks []BeforeModelCallback
	AfterModelCallbacks  []AfterModelCallback
}

// Validate validates the configuration
func (c *AdvancedLLMAgentConfig) Validate() error {
	if c.Name == "" {
		return errors.New("agent name is required")
	}
	if c.Name == "user" {
		return errors.New("agent name cannot be 'user' (reserved)")
	}

	// Check for duplicate sub-agent names
	seen := make(map[string]bool)
	for _, sub := range c.SubAgents {
		if seen[sub.Name()] {
			return fmt.Errorf("duplicate sub-agent name: %s", sub.Name())
		}
		seen[sub.Name()] = true
	}

	return nil
}
