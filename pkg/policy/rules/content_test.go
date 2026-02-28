package rules

import (
	"context"
	"testing"

	"github.com/seyi/dagens/pkg/policy"
)

func TestContentEvaluator_Type(t *testing.T) {
	e := &ContentEvaluator{}
	if e.Type() != "content" {
		t.Errorf("expected type 'content', got '%s'", e.Type())
	}
}

func TestContentEvaluator_DetectsKeywords(t *testing.T) {
	e := &ContentEvaluator{}
	ctx := context.Background()

	content := "This contains secret information"
	config := map[string]interface{}{
		"keywords": []interface{}{"secret", "confidential"},
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Matched {
		t.Error("expected match for keyword 'secret'")
	}

	if len(result.Matches) != 1 {
		t.Errorf("expected 1 match, got %d", len(result.Matches))
	}

	if result.Matches[0].Value != "secret" {
		t.Errorf("expected matched value 'secret', got '%s'", result.Matches[0].Value)
	}
}

func TestContentEvaluator_CaseInsensitive(t *testing.T) {
	e := &ContentEvaluator{}
	ctx := context.Background()

	content := "This contains SECRET information"
	config := map[string]interface{}{
		"keywords":       []interface{}{"secret"},
		"case_sensitive": false,
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Matched {
		t.Error("expected case-insensitive match")
	}
}

func TestContentEvaluator_AlwaysCaseInsensitive(t *testing.T) {
	// Note: The ContentEvaluator always uses case-insensitive matching
	e := &ContentEvaluator{}
	ctx := context.Background()

	content := "This contains SECRET information"
	config := map[string]interface{}{
		"keywords": []interface{}{"secret"},
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Always case-insensitive, so should match
	if !result.Matched {
		t.Error("expected match (case-insensitive)")
	}
}

func TestContentEvaluator_MultipleKeywords(t *testing.T) {
	e := &ContentEvaluator{}
	ctx := context.Background()

	content := "This is secret and confidential data"
	config := map[string]interface{}{
		"keywords": []interface{}{"secret", "confidential", "private"},
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Matched {
		t.Error("expected matches")
	}

	// Should match both 'secret' and 'confidential'
	if len(result.Matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(result.Matches))
	}
}

func TestContentEvaluator_NoMatchCleanContent(t *testing.T) {
	e := &ContentEvaluator{}
	ctx := context.Background()

	content := "This is normal content"
	config := map[string]interface{}{
		"keywords": []interface{}{"secret", "confidential"},
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Matched {
		t.Error("expected no match for clean content")
	}
}

func TestContentEvaluator_NoKeywordsConfigured(t *testing.T) {
	e := &ContentEvaluator{}
	ctx := context.Background()

	content := "Some content"
	result, err := e.Evaluate(ctx, content, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Matched {
		t.Error("expected no match when no keywords configured")
	}
}

func TestContentEvaluator_MatchesKeywords(t *testing.T) {
	// Note: ContentEvaluator does not redact, only matches
	e := &ContentEvaluator{}
	ctx := context.Background()

	content := "The secret password is abc123"
	config := map[string]interface{}{
		"keywords": []interface{}{"secret", "password"},
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Matched {
		t.Error("expected match")
	}

	// Should have 2 matches
	if len(result.Matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(result.Matches))
	}

	// Original is preserved
	if result.Original != content {
		t.Error("expected original to be preserved")
	}
}

func TestContentEvaluator_Categories(t *testing.T) {
	e := &ContentEvaluator{}
	ctx := context.Background()

	// Note: Only "harmful" category has default keywords
	// "profanity" and "violence" categories are empty
	tests := []struct {
		name     string
		content  string
		category string
		want     bool
	}{
		{"harmful - hack", "How to hack the system", "harmful", true},
		{"harmful - steal", "How to steal data", "harmful", true},
		{"harmful - exploit", "exploit vulnerability in system", "harmful", true},
		{"harmful - no match", "This is normal text", "harmful", false},
		{"profanity - empty category", "This is damn annoying", "profanity", false}, // empty category
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := map[string]interface{}{
				"categories": []interface{}{tt.category},
			}

			result, err := e.Evaluate(ctx, tt.content, config)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result.Matched != tt.want {
				t.Errorf("expected matched=%v, got matched=%v", tt.want, result.Matched)
			}
		})
	}
}

// Ensure ContentEvaluator implements RuleEvaluator interface
var _ policy.RuleEvaluator = (*ContentEvaluator)(nil)
