package rules

import (
	"context"
	"regexp"

	"github.com/seyi/dagens/pkg/policy"
)

// PIIEvaluator detects and redacts personally identifiable information
type PIIEvaluator struct{}

// Type returns the rule type identifier
func (e *PIIEvaluator) Type() string { return "pii" }

// Default PII patterns
var piiPatterns = map[string]*regexp.Regexp{
	"email":       regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`),
	"phone_us":    regexp.MustCompile(`(\+1[-.\s]?)?\(?\d{3}\)?[-.\s]?\d{3}[-.\s]?\d{4}`),
	"ssn":         regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`),
	"credit_card": regexp.MustCompile(`\b(?:\d{4}[-\s]?){3}\d{4}\b`),
	"ip_address":  regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`),
}

// Evaluate checks the content for PII and optionally redacts it
func (e *PIIEvaluator) Evaluate(ctx context.Context, content string, config map[string]interface{}) (*policy.EvaluationResult, error) {
	result := &policy.EvaluationResult{
		Matched: false,
		Matches: make([]policy.Match, 0),
	}

	// Get enabled PII types from config, default to common types
	enabledTypes := []string{"email", "phone_us", "ssn", "credit_card"}
	if types, ok := config["types"].([]interface{}); ok {
		enabledTypes = make([]string, len(types))
		for i, t := range types {
			if str, ok := t.(string); ok {
				enabledTypes[i] = str
			}
		}
	}

	redacted := content
	for _, piiType := range enabledTypes {
		pattern, exists := piiPatterns[piiType]
		if !exists {
			continue
		}

		matches := pattern.FindAllStringIndex(content, -1)
		for _, match := range matches {
			result.Matched = true
			result.Matches = append(result.Matches, policy.Match{
				Type:       piiType,
				Value:      maskValue(content[match[0]:match[1]]),
				StartIndex: match[0],
				EndIndex:   match[1],
			})
		}
		// Redact in output
		redacted = pattern.ReplaceAllString(redacted, "[REDACTED-"+piiType+"]")
	}

	result.Original = content
	result.Redacted = redacted
	if result.Matched {
		result.Reason = "PII detected in output"
	}

	return result, nil
}

// maskValue partially masks a value for logging (shows first 2 and last 2 chars)
func maskValue(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + "****" + s[len(s)-2:]
}
