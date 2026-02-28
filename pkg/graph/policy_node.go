package graph

import (
	"context"
	"fmt"

	"github.com/seyi/dagens/pkg/models"
	"github.com/seyi/dagens/pkg/policy"
	"github.com/seyi/dagens/pkg/policy/audit"
	"github.com/seyi/dagens/pkg/policy/rules"
)

// PolicyNode applies policy rules to filter/redact agent outputs
type PolicyNode struct {
	*BaseNode
	config *policy.PolicyConfig
	engine *policy.Engine
}

// PolicyNodeConfig holds configuration for creating a policy node
type PolicyNodeConfig struct {
	ID       string
	Rules    []policy.Rule
	FailOpen bool
	Metadata map[string]interface{}
}

// NewPolicyNode creates a new policy node
func NewPolicyNode(cfg PolicyNodeConfig) *PolicyNode {
	// Create engine with default stdout logger
	engine := policy.NewEngine(audit.NewStdoutAuditLogger(false))

	// Register built-in evaluators
	engine.RegisterEvaluator(&rules.PIIEvaluator{})
	engine.RegisterEvaluator(&rules.ContentEvaluator{})
	engine.RegisterEvaluator(&rules.LengthEvaluator{})
	engine.RegisterEvaluator(&rules.RegexEvaluator{})
	engine.RegisterEvaluator(&rules.LLMEvaluator{})

	node := &PolicyNode{
		BaseNode: NewBaseNode(cfg.ID, "policy"),
		config: &policy.PolicyConfig{
			Rules:    cfg.Rules,
			FailOpen: cfg.FailOpen,
		},
		engine: engine,
	}

	if cfg.Metadata != nil {
		for k, v := range cfg.Metadata {
			node.SetMetadata(k, v)
		}
	}

	return node
}

// NewPolicyNodeWithEngine creates a policy node with a custom engine (for testing)
func NewPolicyNodeWithEngine(cfg PolicyNodeConfig, engine *policy.Engine) *PolicyNode {
	node := &PolicyNode{
		BaseNode: NewBaseNode(cfg.ID, "policy"),
		config: &policy.PolicyConfig{
			Rules:    cfg.Rules,
			FailOpen: cfg.FailOpen,
		},
		engine: engine,
	}

	if cfg.Metadata != nil {
		for k, v := range cfg.Metadata {
			node.SetMetadata(k, v)
		}
	}

	return node
}

// Execute runs policy evaluation on the last message in state
func (n *PolicyNode) Execute(ctx context.Context, state State) error {
	// Try to sanitize Input first (if this node sits before an agent)
	if inputVal, exists := state.Get("input_instruction"); exists {
		if inputStr, ok := inputVal.(string); ok && inputStr != "" {
			if err := n.evaluateAndApply(ctx, state, inputStr, "input_instruction"); err != nil {
				return err
			}
		}
	}

	// Try to sanitize Output (last agent message)
	var content string
	lastMsg, exists := state.Get("last_message")
	if exists {
		content = extractContent(lastMsg)
	} else {
		// Try alternative key
		lastMsg, exists = state.Get("last_response")
		if exists {
			content = extractContent(lastMsg)
		}
	}

	if content != "" {
		return n.evaluateAndApply(ctx, state, content, "last_message")
	}

	return nil
}

func (n *PolicyNode) evaluateAndApply(ctx context.Context, state State, content string, targetKey string) error {
	// Get metadata for audit
	sessionID := ""
	if sidVal, exists := state.Get("session_id"); exists {
		if sid, ok := sidVal.(string); ok {
			sessionID = sid
		}
	}
	jobID := ""
	if jidVal, exists := state.Get("job_id"); exists {
		if jid, ok := jidVal.(string); ok {
			jobID = jid
		}
	}

	metadata := policy.EvaluationMetadata{
		SessionID: sessionID,
		JobID:     jobID,
		NodeID:    n.ID(),
	}

	// Evaluate policies
	result, err := n.engine.Evaluate(ctx, n.config, content, metadata)
	if err != nil {
		return fmt.Errorf("policy evaluation failed: %w", err)
	}

	// Handle result
	if !result.Allowed {
		state.Set("policy_blocked", true)
		state.Set("policy_block_reason", result.BlockReason)
		return &policy.ErrPolicyViolation{
			Reason: result.BlockReason,
			RuleID: "v0-blocked",
		}
	}

	// Update state with potentially redacted output
	if result.FinalAction == policy.ActionRedact {
		state.Set(targetKey, result.FinalOutput)
		state.Set("policy_redacted", true)
	}

	// Store policy results for inspection
	state.Set("policy_results", result.Results)
	state.Set("policy_action", string(result.FinalAction))

	return nil
}


// extractContent attempts to extract string content from various types
func extractContent(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case models.Message:
		return val.Content
	case *models.Message:
		if val != nil {
			return val.Content
		}
	case map[string]interface{}:
		if content, ok := val["content"].(string); ok {
			return content
		}
		if content, ok := val["Content"].(string); ok {
			return content
		}
	}
	return ""
}

// Config returns the policy configuration
func (n *PolicyNode) Config() *policy.PolicyConfig {
	return n.config
}
