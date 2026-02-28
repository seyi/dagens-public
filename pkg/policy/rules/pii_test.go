package rules

import (
	"context"
	"testing"

	"github.com/seyi/dagens/pkg/policy"
)

func TestPIIEvaluator_Type(t *testing.T) {
	e := &PIIEvaluator{}
	if e.Type() != "pii" {
		t.Errorf("expected type 'pii', got '%s'", e.Type())
	}
}

func TestPIIEvaluator_DetectsEmail(t *testing.T) {
	e := &PIIEvaluator{}
	ctx := context.Background()

	content := "Contact me at john.doe@example.com for more info"
	result, err := e.Evaluate(ctx, content, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Matched {
		t.Error("expected match for email")
	}

	if len(result.Matches) != 1 {
		t.Errorf("expected 1 match, got %d", len(result.Matches))
	}

	if result.Matches[0].Type != "email" {
		t.Errorf("expected match type 'email', got '%s'", result.Matches[0].Type)
	}

	// Value is masked (first 2 + **** + last 2 chars)
	if result.Matches[0].Value != "jo****om" {
		t.Errorf("expected masked value 'jo****om', got '%s'", result.Matches[0].Value)
	}
}

func TestPIIEvaluator_DetectsPhone(t *testing.T) {
	e := &PIIEvaluator{}
	ctx := context.Background()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"with dashes", "Call me at 555-123-4567", "555-123-4567"},
		{"with dots", "Call me at 555.123.4567", "555.123.4567"},
		{"with parens", "Call me at (555) 123-4567", "(555) 123-4567"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := e.Evaluate(ctx, tt.content, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !result.Matched {
				t.Error("expected match for phone")
			}

			found := false
			for _, m := range result.Matches {
				if m.Type == "phone_us" {
					found = true
					break
				}
			}
			if !found {
				t.Error("expected phone_us match type")
			}
		})
	}
}

func TestPIIEvaluator_DetectsSSN(t *testing.T) {
	e := &PIIEvaluator{}
	ctx := context.Background()

	content := "My SSN is 123-45-6789"
	result, err := e.Evaluate(ctx, content, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Matched {
		t.Error("expected match for SSN")
	}

	found := false
	for _, m := range result.Matches {
		// Value is masked: "12****89"
		if m.Type == "ssn" && m.Value == "12****89" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected SSN match with masked value '12****89'")
	}
}

func TestPIIEvaluator_DetectsCreditCard(t *testing.T) {
	e := &PIIEvaluator{}
	ctx := context.Background()

	tests := []struct {
		name    string
		content string
	}{
		{"with spaces", "Card: 4111 1111 1111 1111"},
		{"with dashes", "Card: 4111-1111-1111-1111"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := e.Evaluate(ctx, tt.content, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !result.Matched {
				t.Error("expected match for credit card")
			}

			found := false
			for _, m := range result.Matches {
				if m.Type == "credit_card" {
					found = true
					break
				}
			}
			if !found {
				t.Error("expected credit_card match type")
			}
		})
	}
}

func TestPIIEvaluator_RedactsContent(t *testing.T) {
	e := &PIIEvaluator{}
	ctx := context.Background()

	content := "Email: test@example.com, SSN: 123-45-6789"
	result, err := e.Evaluate(ctx, content, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Redacted == "" {
		t.Error("expected redacted content")
	}

	// Should not contain original PII
	if contains(result.Redacted, "test@example.com") {
		t.Error("redacted content should not contain email")
	}
	if contains(result.Redacted, "123-45-6789") {
		t.Error("redacted content should not contain SSN")
	}
}

func TestPIIEvaluator_FilterByTypes(t *testing.T) {
	e := &PIIEvaluator{}
	ctx := context.Background()

	content := "Email: test@example.com, SSN: 123-45-6789"

	// Only detect email
	config := map[string]interface{}{
		"types": []interface{}{"email"},
	}

	result, err := e.Evaluate(ctx, content, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Matched {
		t.Error("expected match")
	}

	// Should only have email matches
	for _, m := range result.Matches {
		if m.Type != "email" {
			t.Errorf("unexpected match type: %s", m.Type)
		}
	}
}

func TestPIIEvaluator_NoMatchCleanContent(t *testing.T) {
	e := &PIIEvaluator{}
	ctx := context.Background()

	content := "This is clean content with no PII"
	result, err := e.Evaluate(ctx, content, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Matched {
		t.Error("expected no match for clean content")
	}

	if len(result.Matches) != 0 {
		t.Errorf("expected 0 matches, got %d", len(result.Matches))
	}
}

func TestPIIEvaluator_MultiplePII(t *testing.T) {
	e := &PIIEvaluator{}
	ctx := context.Background()

	content := "Contact: a@b.com, b@c.com, Phone: 555-111-2222"
	result, err := e.Evaluate(ctx, content, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Matched {
		t.Error("expected matches")
	}

	// Should have at least 3 matches (2 emails + 1 phone)
	if len(result.Matches) < 3 {
		t.Errorf("expected at least 3 matches, got %d", len(result.Matches))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Ensure PIIEvaluator implements RuleEvaluator interface
var _ policy.RuleEvaluator = (*PIIEvaluator)(nil)
