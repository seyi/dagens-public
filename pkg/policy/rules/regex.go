package rules

import (
	"context"
	"fmt"
	"regexp"

	"github.com/seyi/dagens/pkg/policy"
)

// RegexEvaluator matches content against custom regex patterns
type RegexEvaluator struct{}

// Type returns the rule type identifier
func (e *RegexEvaluator) Type() string { return "regex" }

// Evaluate checks the content against a custom regex pattern
func (e *RegexEvaluator) Evaluate(ctx context.Context, content string, config map[string]interface{}) (*policy.EvaluationResult, error) {
	result := &policy.EvaluationResult{
		Matched:  false,
		Matches:  make([]policy.Match, 0),
		Original: content,
	}

	patternStr, ok := config["pattern"].(string)
	if !ok || patternStr == "" {
		return result, nil // No pattern configured
	}

	pattern, err := regexp.Compile(patternStr)
	if err != nil {
		return nil, fmt.Errorf("invalid regex pattern '%s': %w", patternStr, err)
	}

	matches := pattern.FindAllStringIndex(content, -1)
	redacted := content

	for _, match := range matches {
		result.Matched = true
		matchedValue := content[match[0]:match[1]]
		result.Matches = append(result.Matches, policy.Match{
			Type:       "regex",
			Value:      matchedValue,
			StartIndex: match[0],
			EndIndex:   match[1],
		})
	}

	if result.Matched {
		replacement := "[REDACTED]"
		if r, ok := config["replacement"].(string); ok && r != "" {
			replacement = r
		}
		redacted = pattern.ReplaceAllString(content, replacement)
		result.Redacted = redacted
		result.Reason = "Custom pattern matched"
	}

	return result, nil
}
