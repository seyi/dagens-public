package policy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	"github.com/seyi/dagens/pkg/telemetry"
	"github.com/google/uuid"
)

// AuditLogger interface for logging policy decisions
type AuditLogger interface {
	Log(ctx context.Context, event *AuditEvent) error
}

// Engine orchestrates policy evaluation
type Engine struct {
	registry    *Registry
	auditLogger AuditLogger
}

// NewEngine creates a new policy engine with the given audit logger
func NewEngine(auditLogger AuditLogger) *Engine {
	return &Engine{
		registry:    NewRegistry(),
		auditLogger: auditLogger,
	}
}

// RegisterEvaluator adds a rule evaluator to the engine
func (e *Engine) RegisterEvaluator(evaluator RuleEvaluator) {
	e.registry.Register(evaluator)
}

// Evaluate runs all rules against the content and returns the final result
func (e *Engine) Evaluate(ctx context.Context, config *PolicyConfig, content string, metadata EvaluationMetadata) (*EngineResult, error) {
	startTime := time.Now()

	// Get tracer
	tracer := telemetry.GetGlobalTelemetry().GetTracer()
	_, span := tracer.StartSpan(ctx, "policy.evaluate")
	defer span.End()

	result := &EngineResult{
		Allowed:     true,
		FinalAction: ActionPass,
		FinalOutput: content,
		Results:     make([]EvaluationResult, 0),
	}

	for _, rule := range config.Rules {
		if !rule.Enabled {
			continue
		}

		evaluator, ok := e.registry.Get(rule.Type)
		if !ok {
			log.Printf("[POLICY] Unknown rule type: %s", rule.Type)
			continue
		}

		evalResult, err := evaluator.Evaluate(ctx, result.FinalOutput, rule.Config)
		if err != nil {
			if config.FailOpen {
				log.Printf("[POLICY] Rule %s error (fail-open): %v", rule.ID, err)
				span.AddEvent("rule_error", map[string]interface{}{
					"rule_id": rule.ID,
					"error":   err.Error(),
				})
				continue
			}
			return nil, fmt.Errorf("rule %s evaluation failed: %w", rule.ID, err)
		}

		evalResult.RuleID = rule.ID
		evalResult.RuleName = rule.Name
		evalResult.Action = rule.Action
		evalResult.Severity = rule.Severity

		if evalResult.Matched {
			result.Results = append(result.Results, *evalResult)
			
			// Record hit in OTEL
			span.AddEvent("policy_match", map[string]interface{}{
				"rule_id":   rule.ID,
				"rule_name": rule.Name,
				"action":    string(rule.Action),
				"severity":  string(rule.Severity),
				"reason":    evalResult.Reason,
			})

			switch rule.Action {
			case ActionBlock:
				result.Allowed = false
				result.FinalAction = ActionBlock
				result.FinalOutput = ""
				result.BlockReason = evalResult.Reason
				log.Printf("[POLICY] BLOCKED by rule %s: %s", rule.ID, evalResult.Reason)

			case ActionRedact:
				if evalResult.Redacted != "" {
					result.FinalOutput = evalResult.Redacted
				}
				if result.FinalAction != ActionBlock {
					result.FinalAction = ActionRedact
				}
				log.Printf("[POLICY] REDACTED by rule %s: %d matches", rule.ID, len(evalResult.Matches))

			case ActionWarn:
				log.Printf("[POLICY] WARNING from rule %s: %s", rule.ID, evalResult.Reason)
			}

			if config.StopOnFirstMatch && rule.Action == ActionBlock {
				break
			}
		}
	}

	// Log audit event
	if e.auditLogger != nil {
		// Determine Trace/Span IDs for the audit event.
		// Prioritize explicit metadata from the caller, then fall back to the active internal span.
		traceID := metadata.TraceID
		spanID := metadata.SpanID

		if traceID == "" || spanID == "" {
			spanCtx := span.SpanContext()
			if spanCtx.IsValid() {
				if traceID == "" {
					traceID = span.TraceID()
				}
				if spanID == "" {
					spanID = span.SpanID()
				}
			}
		}

		auditEvent := &AuditEvent{
			ID:          uuid.New().String(),
			Timestamp:   time.Now(),
			SessionID:   metadata.SessionID,
			JobID:       metadata.JobID,
			NodeID:      metadata.NodeID,
			TraceID:     traceID,
			SpanID:      spanID,
			InputHash:   hashContent(content),
			OutputHash:  hashContent(result.FinalOutput),
			Results:     result.Results,
			FinalAction: result.FinalAction,
			DurationMs:  time.Since(startTime).Milliseconds(),
		}
		if result.FinalAction == ActionRedact {
			auditEvent.FinalOutput = result.FinalOutput
		}
		if err := e.auditLogger.Log(ctx, auditEvent); err != nil {
			log.Printf("[POLICY] Failed to log audit event: %v", err)
		}
	}

	return result, nil
}


// hashContent returns a SHA256 hash of the content for audit purposes
func hashContent(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
