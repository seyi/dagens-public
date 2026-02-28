package policy

import (
	"time"
)

// Action defines what happens when a rule matches
type Action string

const (
	ActionPass   Action = "pass"   // Allow output unchanged
	ActionBlock  Action = "block"  // Reject output entirely
	ActionRedact Action = "redact" // Replace matched content with [REDACTED]
	ActionWarn   Action = "warn"   // Allow but log warning
)

// Severity indicates the seriousness of a policy violation
type Severity string

const (
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// Rule defines a single policy rule
type Rule struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	Type     string                 `json:"type"` // "pii", "content", "length", "regex", "custom"
	Action   Action                 `json:"action"`
	Severity Severity               `json:"severity"`
	Enabled  bool                   `json:"enabled"`
	Config   map[string]interface{} `json:"config"`
}

// EvaluationResult holds the result of evaluating a rule
type EvaluationResult struct {
	RuleID   string
	RuleName string
	Matched  bool
	Action   Action
	Severity Severity
	Original string  // Original content (for audit)
	Redacted string  // Redacted content (if action=redact)
	Matches  []Match // What was matched
	Reason   string  // Human-readable explanation
}

// Match represents a single match within the content
type Match struct {
	Type       string // "email", "phone", "ssn", "keyword", etc.
	Value      string // The matched value (may be partially masked in logs)
	StartIndex int
	EndIndex   int
}

// PolicyConfig holds the complete policy configuration for a node
type PolicyConfig struct {
	Rules            []Rule `json:"rules"`
	FailOpen         bool   `json:"fail_open"`           // If true, allow on rule error
	StopOnFirstMatch bool   `json:"stop_on_first_match"` // Short-circuit evaluation
}

// AuditEvent records a policy decision for compliance
type AuditEvent struct {
	ID          string             `json:"id"`
	Timestamp   time.Time          `json:"timestamp"`
	SessionID   string             `json:"session_id"`
	JobID       string             `json:"job_id"`
	NodeID      string             `json:"node_id"`
	TraceID     string             `json:"trace_id,omitempty"`    // OTEL Trace ID
	SpanID      string             `json:"span_id,omitempty"`     // OTEL Span ID
	InputHash   string             `json:"input_hash"`            // SHA256 of input (not raw for privacy)
	OutputHash  string             `json:"output_hash"`           // SHA256 of output
	Results     []EvaluationResult `json:"results"`
	FinalAction Action             `json:"final_action"`
	FinalOutput string             `json:"final_output,omitempty"` // Only if redacted
	DurationMs  int64              `json:"duration_ms"`
}

// EngineResult holds the final policy decision
type EngineResult struct {
	Allowed     bool
	FinalAction Action
	FinalOutput string
	BlockReason string
	Results     []EvaluationResult
}

// EvaluationMetadata provides context for audit logging
type EvaluationMetadata struct {
	SessionID string
	JobID     string
	NodeID    string
	TraceID   string
	SpanID    string
}

// ErrPolicyViolation is returned when a policy blocks an execution
type ErrPolicyViolation struct {
	Reason string
	RuleID string
}

func (e *ErrPolicyViolation) Error() string {
	return "policy violation: " + e.Reason
}

