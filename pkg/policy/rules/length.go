package rules

import (
	"context"
	"fmt"

	"github.com/seyi/dagens/pkg/policy"
)

// LengthEvaluator enforces output length limits
type LengthEvaluator struct{}

// Type returns the rule type identifier
func (e *LengthEvaluator) Type() string { return "length" }

// Evaluate checks if the content exceeds length limits
func (e *LengthEvaluator) Evaluate(ctx context.Context, content string, config map[string]interface{}) (*policy.EvaluationResult, error) {
	result := &policy.EvaluationResult{
		Matched:  false,
		Original: content,
		Matches:  make([]policy.Match, 0),
	}

	maxLength := 10000 // Default max length
	if ml, ok := config["max_length"].(float64); ok {
		maxLength = int(ml)
	} else if ml, ok := config["max_length"].(int); ok {
		maxLength = ml
	}

	minLength := 0
	if ml, ok := config["min_length"].(float64); ok {
		minLength = int(ml)
	} else if ml, ok := config["min_length"].(int); ok {
		minLength = ml
	}

	contentLength := len(content)

	if maxLength > 0 && contentLength > maxLength {
		result.Matched = true
		result.Reason = fmt.Sprintf("Output length %d exceeds maximum %d", contentLength, maxLength)
		result.Redacted = content[:maxLength] + "... [TRUNCATED]"
		result.Matches = append(result.Matches, policy.Match{
			Type:  "length:max",
			Value: fmt.Sprintf("%d > %d", contentLength, maxLength),
		})
	}

	if minLength > 0 && contentLength < minLength {
		result.Matched = true
		result.Reason = fmt.Sprintf("Output length %d below minimum %d", contentLength, minLength)
		result.Matches = append(result.Matches, policy.Match{
			Type:  "length:min",
			Value: fmt.Sprintf("%d < %d", contentLength, minLength),
		})
	}

	return result, nil
}
