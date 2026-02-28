package policy

import (
	"context"
	"testing"
)

// mockEvaluator is a test implementation of RuleEvaluator
type mockEvaluator struct {
	ruleType    string
	shouldMatch bool
	matchValue  string
	shouldError bool
}

func (m *mockEvaluator) Type() string { return m.ruleType }

func (m *mockEvaluator) Evaluate(ctx context.Context, content string, config map[string]interface{}) (*EvaluationResult, error) {
	if m.shouldError {
		return nil, context.DeadlineExceeded
	}
	result := &EvaluationResult{
		Matched:  m.shouldMatch,
		Original: content,
		Matches:  []Match{},
	}
	if m.shouldMatch {
		result.Matches = append(result.Matches, Match{
			Type:  m.ruleType,
			Value: m.matchValue,
		})
		result.Redacted = "[REDACTED]"
		result.Reason = "mock match"
	}
	return result, nil
}

// mockAuditLogger captures audit events for testing
type mockAuditLogger struct {
	events []*AuditEvent
}

func (m *mockAuditLogger) Log(ctx context.Context, event *AuditEvent) error {
	m.events = append(m.events, event)
	return nil
}

func TestEngine_NewEngine(t *testing.T) {
	logger := &mockAuditLogger{}
	engine := NewEngine(logger)

	if engine == nil {
		t.Fatal("expected non-nil engine")
	}
	if engine.registry == nil {
		t.Error("expected non-nil registry")
	}
	if engine.auditLogger != logger {
		t.Error("expected audit logger to be set")
	}
}

func TestEngine_RegisterEvaluator(t *testing.T) {
	engine := NewEngine(nil)
	eval := &mockEvaluator{ruleType: "test"}

	engine.RegisterEvaluator(eval)

	if _, ok := engine.registry.Get("test"); !ok {
		t.Error("expected evaluator to be registered")
	}
}

func TestEngine_EvaluatePassesCleanContent(t *testing.T) {
	logger := &mockAuditLogger{}
	engine := NewEngine(logger)
	engine.RegisterEvaluator(&mockEvaluator{ruleType: "test", shouldMatch: false})

	config := &PolicyConfig{
		Rules: []Rule{
			{ID: "rule1", Type: "test", Action: ActionBlock, Enabled: true},
		},
	}

	result, err := engine.Evaluate(context.Background(), config, "clean content", EvaluationMetadata{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Allowed {
		t.Error("expected content to be allowed")
	}
	if result.FinalAction != ActionPass {
		t.Errorf("expected ActionPass, got %s", result.FinalAction)
	}
	if result.FinalOutput != "clean content" {
		t.Errorf("expected original content in output")
	}
}

func TestEngine_EvaluateBlocksMatchedContent(t *testing.T) {
	logger := &mockAuditLogger{}
	engine := NewEngine(logger)
	engine.RegisterEvaluator(&mockEvaluator{ruleType: "test", shouldMatch: true, matchValue: "bad"})

	config := &PolicyConfig{
		Rules: []Rule{
			{ID: "rule1", Type: "test", Action: ActionBlock, Enabled: true},
		},
	}

	result, err := engine.Evaluate(context.Background(), config, "bad content", EvaluationMetadata{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Allowed {
		t.Error("expected content to be blocked")
	}
	if result.FinalAction != ActionBlock {
		t.Errorf("expected ActionBlock, got %s", result.FinalAction)
	}
	if result.FinalOutput != "" {
		t.Error("expected empty output when blocked")
	}
}

func TestEngine_EvaluateRedactsContent(t *testing.T) {
	logger := &mockAuditLogger{}
	engine := NewEngine(logger)
	engine.RegisterEvaluator(&mockEvaluator{ruleType: "test", shouldMatch: true, matchValue: "secret"})

	config := &PolicyConfig{
		Rules: []Rule{
			{ID: "rule1", Type: "test", Action: ActionRedact, Enabled: true},
		},
	}

	result, err := engine.Evaluate(context.Background(), config, "secret content", EvaluationMetadata{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Allowed {
		t.Error("expected content to be allowed after redaction")
	}
	if result.FinalAction != ActionRedact {
		t.Errorf("expected ActionRedact, got %s", result.FinalAction)
	}
	if result.FinalOutput != "[REDACTED]" {
		t.Errorf("expected redacted output, got '%s'", result.FinalOutput)
	}
}

func TestEngine_EvaluateWarnsButAllows(t *testing.T) {
	logger := &mockAuditLogger{}
	engine := NewEngine(logger)
	engine.RegisterEvaluator(&mockEvaluator{ruleType: "test", shouldMatch: true, matchValue: "warn"})

	config := &PolicyConfig{
		Rules: []Rule{
			{ID: "rule1", Type: "test", Action: ActionWarn, Enabled: true},
		},
	}

	result, err := engine.Evaluate(context.Background(), config, "warn content", EvaluationMetadata{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Allowed {
		t.Error("expected content to be allowed with warning")
	}
	if result.FinalAction != ActionPass {
		t.Errorf("expected ActionPass (warn doesn't change action), got %s", result.FinalAction)
	}
}

func TestEngine_EvaluateSkipsDisabledRules(t *testing.T) {
	logger := &mockAuditLogger{}
	engine := NewEngine(logger)
	engine.RegisterEvaluator(&mockEvaluator{ruleType: "test", shouldMatch: true})

	config := &PolicyConfig{
		Rules: []Rule{
			{ID: "rule1", Type: "test", Action: ActionBlock, Enabled: false},
		},
	}

	result, err := engine.Evaluate(context.Background(), config, "content", EvaluationMetadata{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Allowed {
		t.Error("expected content to be allowed when rule disabled")
	}
}

func TestEngine_EvaluateSkipsUnknownRuleTypes(t *testing.T) {
	logger := &mockAuditLogger{}
	engine := NewEngine(logger)

	config := &PolicyConfig{
		Rules: []Rule{
			{ID: "rule1", Type: "unknown", Action: ActionBlock, Enabled: true},
		},
	}

	result, err := engine.Evaluate(context.Background(), config, "content", EvaluationMetadata{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Allowed {
		t.Error("expected content to be allowed when rule type unknown")
	}
}

func TestEngine_EvaluateFailOpenOnError(t *testing.T) {
	logger := &mockAuditLogger{}
	engine := NewEngine(logger)
	engine.RegisterEvaluator(&mockEvaluator{ruleType: "test", shouldError: true})

	config := &PolicyConfig{
		Rules: []Rule{
			{ID: "rule1", Type: "test", Action: ActionBlock, Enabled: true},
		},
		FailOpen: true,
	}

	result, err := engine.Evaluate(context.Background(), config, "content", EvaluationMetadata{})
	if err != nil {
		t.Fatalf("unexpected error with fail-open: %v", err)
	}

	if !result.Allowed {
		t.Error("expected content to be allowed with fail-open")
	}
}

func TestEngine_EvaluateFailClosedOnError(t *testing.T) {
	logger := &mockAuditLogger{}
	engine := NewEngine(logger)
	engine.RegisterEvaluator(&mockEvaluator{ruleType: "test", shouldError: true})

	config := &PolicyConfig{
		Rules: []Rule{
			{ID: "rule1", Type: "test", Action: ActionBlock, Enabled: true},
		},
		FailOpen: false,
	}

	_, err := engine.Evaluate(context.Background(), config, "content", EvaluationMetadata{})
	if err == nil {
		t.Error("expected error with fail-closed")
	}
}

func TestEngine_EvaluateStopOnFirstMatch(t *testing.T) {
	logger := &mockAuditLogger{}
	engine := NewEngine(logger)
	engine.RegisterEvaluator(&mockEvaluator{ruleType: "test1", shouldMatch: true})
	engine.RegisterEvaluator(&mockEvaluator{ruleType: "test2", shouldMatch: true})

	config := &PolicyConfig{
		Rules: []Rule{
			{ID: "rule1", Type: "test1", Action: ActionBlock, Enabled: true},
			{ID: "rule2", Type: "test2", Action: ActionBlock, Enabled: true},
		},
		StopOnFirstMatch: true,
	}

	result, err := engine.Evaluate(context.Background(), config, "content", EvaluationMetadata{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should stop after first block
	if len(result.Results) != 1 {
		t.Errorf("expected 1 result with stop-on-first-match, got %d", len(result.Results))
	}
}

func TestEngine_EvaluateLogsAuditEvent(t *testing.T) {
	logger := &mockAuditLogger{}
	engine := NewEngine(logger)
	engine.RegisterEvaluator(&mockEvaluator{ruleType: "test", shouldMatch: true})

	config := &PolicyConfig{
		Rules: []Rule{
			{ID: "rule1", Type: "test", Action: ActionWarn, Enabled: true},
		},
	}

	metadata := EvaluationMetadata{
		SessionID: "sess-123",
		JobID:     "job-456",
		NodeID:    "node-789",
	}

	_, err := engine.Evaluate(context.Background(), config, "content", metadata)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(logger.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(logger.events))
	}

	event := logger.events[0]
	if event.SessionID != "sess-123" {
		t.Errorf("expected session ID 'sess-123', got '%s'", event.SessionID)
	}
	if event.JobID != "job-456" {
		t.Errorf("expected job ID 'job-456', got '%s'", event.JobID)
	}
	if event.NodeID != "node-789" {
		t.Errorf("expected node ID 'node-789', got '%s'", event.NodeID)
	}
}

func TestEngine_EvaluateMultipleRules(t *testing.T) {
	logger := &mockAuditLogger{}
	engine := NewEngine(logger)
	engine.RegisterEvaluator(&mockEvaluator{ruleType: "pii", shouldMatch: true, matchValue: "email"})
	engine.RegisterEvaluator(&mockEvaluator{ruleType: "content", shouldMatch: false})

	config := &PolicyConfig{
		Rules: []Rule{
			{ID: "rule1", Type: "pii", Action: ActionRedact, Enabled: true},
			{ID: "rule2", Type: "content", Action: ActionBlock, Enabled: true},
		},
	}

	result, err := engine.Evaluate(context.Background(), config, "content with email", EvaluationMetadata{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Allowed {
		t.Error("expected content to be allowed after redaction")
	}
	if result.FinalAction != ActionRedact {
		t.Errorf("expected ActionRedact, got %s", result.FinalAction)
	}
	if len(result.Results) != 1 {
		t.Errorf("expected 1 result (only matched rule), got %d", len(result.Results))
	}
}

func TestEngine_BlockOverridesRedact(t *testing.T) {
	logger := &mockAuditLogger{}
	engine := NewEngine(logger)
	engine.RegisterEvaluator(&mockEvaluator{ruleType: "redact", shouldMatch: true})
	engine.RegisterEvaluator(&mockEvaluator{ruleType: "block", shouldMatch: true})

	config := &PolicyConfig{
		Rules: []Rule{
			{ID: "rule1", Type: "redact", Action: ActionRedact, Enabled: true},
			{ID: "rule2", Type: "block", Action: ActionBlock, Enabled: true},
		},
	}

	result, err := engine.Evaluate(context.Background(), config, "content", EvaluationMetadata{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Allowed {
		t.Error("expected content to be blocked")
	}
	if result.FinalAction != ActionBlock {
		t.Errorf("expected ActionBlock to override redact, got %s", result.FinalAction)
	}
}
