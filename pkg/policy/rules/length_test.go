package rules

import (
	"context"
	"strings"
	"testing"

	"github.com/seyi/dagens/pkg/policy"
)

func TestLengthEvaluator_Type(t *testing.T) {
	e := &LengthEvaluator{}
	if e.Type() != "length" {
		t.Errorf("expected type 'length', got '%s'", e.Type())
	}
}

func TestLengthEvaluator_ExceedsMaxLength(t *testing.T) {
	e := &LengthEvaluator{}
	ctx := context.Background()

	content := strings.Repeat("a", 150)
	config := map[string]interface{}{
		"max_length": float64(100), // JSON numbers are float64
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Matched {
		t.Error("expected match for content exceeding max length")
	}

	if result.Reason == "" {
		t.Error("expected reason to be set")
	}
}

func TestLengthEvaluator_WithinMaxLength(t *testing.T) {
	e := &LengthEvaluator{}
	ctx := context.Background()

	content := strings.Repeat("a", 50)
	config := map[string]interface{}{
		"max_length": float64(100),
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Matched {
		t.Error("expected no match for content within limit")
	}
}

func TestLengthEvaluator_ExactlyMaxLength(t *testing.T) {
	e := &LengthEvaluator{}
	ctx := context.Background()

	content := strings.Repeat("a", 100)
	config := map[string]interface{}{
		"max_length": float64(100),
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Matched {
		t.Error("expected no match for content at exact limit")
	}
}

func TestLengthEvaluator_TruncatesContent(t *testing.T) {
	e := &LengthEvaluator{}
	ctx := context.Background()

	content := "Hello World! This is a long message that should be truncated."
	config := map[string]interface{}{
		"max_length": float64(20),
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Matched {
		t.Error("expected match")
	}

	if result.Redacted == "" {
		t.Error("expected redacted/truncated content")
	}

	// Truncated format is: content[:max_length] + "... [TRUNCATED]"
	if len(result.Redacted) > 20+len("... [TRUNCATED]") {
		t.Errorf("truncated content too long: %d", len(result.Redacted))
	}
}

func TestLengthEvaluator_NoConfig(t *testing.T) {
	e := &LengthEvaluator{}
	ctx := context.Background()

	content := strings.Repeat("a", 1000)
	result, err := e.Evaluate(ctx, content, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With no config, should use default (10000) or not match
	if result.Matched {
		t.Error("expected no match with default high limit")
	}
}

func TestLengthEvaluator_DefaultMaxLength(t *testing.T) {
	e := &LengthEvaluator{}
	ctx := context.Background()

	// Create content larger than default (10000)
	content := strings.Repeat("a", 15000)
	result, err := e.Evaluate(ctx, content, map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Matched {
		t.Error("expected match for content exceeding default limit")
	}
}

func TestLengthEvaluator_EmptyContent(t *testing.T) {
	e := &LengthEvaluator{}
	ctx := context.Background()

	config := map[string]interface{}{
		"max_length": float64(100),
	}

	result, err := e.Evaluate(ctx, "", config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Matched {
		t.Error("expected no match for empty content")
	}
}

func TestLengthEvaluator_MatchDetails(t *testing.T) {
	e := &LengthEvaluator{}
	ctx := context.Background()

	content := strings.Repeat("x", 200)
	config := map[string]interface{}{
		"max_length": float64(100),
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Matches) != 1 {
		t.Errorf("expected 1 match, got %d", len(result.Matches))
	}

	// Type is "length:max" for max length violations
	if result.Matches[0].Type != "length:max" {
		t.Errorf("expected match type 'length:max', got '%s'", result.Matches[0].Type)
	}
}

// Ensure LengthEvaluator implements RuleEvaluator interface
var _ policy.RuleEvaluator = (*LengthEvaluator)(nil)
