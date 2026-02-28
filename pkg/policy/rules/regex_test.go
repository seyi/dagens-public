package rules

import (
	"context"
	"testing"

	"github.com/seyi/dagens/pkg/policy"
)

func TestRegexEvaluator_Type(t *testing.T) {
	e := &RegexEvaluator{}
	if e.Type() != "regex" {
		t.Errorf("expected type 'regex', got '%s'", e.Type())
	}
}

func TestRegexEvaluator_MatchesPattern(t *testing.T) {
	e := &RegexEvaluator{}
	ctx := context.Background()

	content := "The API key is ABC123XYZ"
	config := map[string]interface{}{
		"pattern": `[A-Z]{3}\d{3}[A-Z]{3}`,
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Matched {
		t.Error("expected match for pattern")
	}

	if len(result.Matches) != 1 {
		t.Errorf("expected 1 match, got %d", len(result.Matches))
	}

	if result.Matches[0].Value != "ABC123XYZ" {
		t.Errorf("expected value 'ABC123XYZ', got '%s'", result.Matches[0].Value)
	}
}

func TestRegexEvaluator_NoMatch(t *testing.T) {
	e := &RegexEvaluator{}
	ctx := context.Background()

	content := "This is normal text"
	config := map[string]interface{}{
		"pattern": `\d{10}`,
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Matched {
		t.Error("expected no match")
	}
}

func TestRegexEvaluator_MultipleMatches(t *testing.T) {
	e := &RegexEvaluator{}
	ctx := context.Background()

	content := "Keys: ABC123, DEF456, GHI789"
	config := map[string]interface{}{
		"pattern": `[A-Z]{3}\d{3}`,
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Matched {
		t.Error("expected matches")
	}

	if len(result.Matches) != 3 {
		t.Errorf("expected 3 matches, got %d", len(result.Matches))
	}
}

func TestRegexEvaluator_RedactsWithDefault(t *testing.T) {
	e := &RegexEvaluator{}
	ctx := context.Background()

	content := "Password: secret123"
	config := map[string]interface{}{
		"pattern": `secret\d+`,
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Redacted == "" {
		t.Error("expected redacted content")
	}

	if result.Redacted != "Password: [REDACTED]" {
		t.Errorf("expected 'Password: [REDACTED]', got '%s'", result.Redacted)
	}
}

func TestRegexEvaluator_CustomReplacement(t *testing.T) {
	e := &RegexEvaluator{}
	ctx := context.Background()

	content := "Password: secret123"
	config := map[string]interface{}{
		"pattern":     `secret\d+`,
		"replacement": "***HIDDEN***",
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Redacted != "Password: ***HIDDEN***" {
		t.Errorf("expected 'Password: ***HIDDEN***', got '%s'", result.Redacted)
	}
}

func TestRegexEvaluator_InvalidPattern(t *testing.T) {
	e := &RegexEvaluator{}
	ctx := context.Background()

	content := "Some content"
	config := map[string]interface{}{
		"pattern": `[invalid(`,
	}

	_, err := e.Evaluate(ctx, content, config)
	if err == nil {
		t.Error("expected error for invalid pattern")
	}
}

func TestRegexEvaluator_NoPattern(t *testing.T) {
	e := &RegexEvaluator{}
	ctx := context.Background()

	content := "Some content"
	result, err := e.Evaluate(ctx, content, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Matched {
		t.Error("expected no match when no pattern configured")
	}
}

func TestRegexEvaluator_EmptyPattern(t *testing.T) {
	e := &RegexEvaluator{}
	ctx := context.Background()

	content := "Some content"
	config := map[string]interface{}{
		"pattern": "",
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Matched {
		t.Error("expected no match for empty pattern")
	}
}

func TestRegexEvaluator_MatchIndices(t *testing.T) {
	e := &RegexEvaluator{}
	ctx := context.Background()

	content := "Find 12345 here"
	config := map[string]interface{}{
		"pattern": `\d{5}`,
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(result.Matches))
	}

	match := result.Matches[0]
	if match.StartIndex != 5 {
		t.Errorf("expected start index 5, got %d", match.StartIndex)
	}
	if match.EndIndex != 10 {
		t.Errorf("expected end index 10, got %d", match.EndIndex)
	}
}

func TestRegexEvaluator_SpecialCharacters(t *testing.T) {
	e := &RegexEvaluator{}
	ctx := context.Background()

	content := "Email: test@example.com"
	config := map[string]interface{}{
		"pattern": `[\w.+-]+@[\w.-]+\.\w+`,
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Matched {
		t.Error("expected match for email pattern")
	}

	if result.Matches[0].Value != "test@example.com" {
		t.Errorf("expected 'test@example.com', got '%s'", result.Matches[0].Value)
	}
}

func TestRegexEvaluator_PreservesOriginal(t *testing.T) {
	e := &RegexEvaluator{}
	ctx := context.Background()

	content := "Original content with secret123"
	config := map[string]interface{}{
		"pattern": `secret\d+`,
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Original != content {
		t.Errorf("expected original to be preserved, got '%s'", result.Original)
	}
}

// Ensure RegexEvaluator implements RuleEvaluator interface
var _ policy.RuleEvaluator = (*RegexEvaluator)(nil)
