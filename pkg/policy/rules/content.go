package rules

import (
	"context"
	"strings"

	"github.com/seyi/dagens/pkg/policy"
)

// ContentEvaluator filters content based on keywords and categories
type ContentEvaluator struct{}

// Type returns the rule type identifier
func (e *ContentEvaluator) Type() string { return "content" }

// Default blocked keywords by category
var defaultBlockedKeywords = map[string][]string{
	"harmful": {
		"how to hack",
		"how to steal",
		"illegal drugs",
		"make a bomb",
		"exploit vulnerability",
	},
	"profanity": {
		// Add as needed - keeping empty for now
	},
	"competitors": {
		// Company-specific - keeping empty for now
	},
}

// Evaluate checks the content for blocked keywords
func (e *ContentEvaluator) Evaluate(ctx context.Context, content string, config map[string]interface{}) (*policy.EvaluationResult, error) {
	result := &policy.EvaluationResult{
		Matched: false,
		Matches: make([]policy.Match, 0),
	}

	lowerContent := strings.ToLower(content)

	// Get categories to check
	categories := []string{"harmful"}
	if cats, ok := config["categories"].([]interface{}); ok {
		categories = make([]string, len(cats))
		for i, c := range cats {
			if str, ok := c.(string); ok {
				categories[i] = str
			}
		}
	}

	// Get custom keywords if provided
	customKeywords := []string{}
	if kw, ok := config["keywords"].([]interface{}); ok {
		for _, k := range kw {
			if str, ok := k.(string); ok {
				customKeywords = append(customKeywords, strings.ToLower(str))
			}
		}
	}

	// Check categories
	for _, category := range categories {
		keywords, exists := defaultBlockedKeywords[category]
		if !exists {
			continue
		}
		for _, keyword := range keywords {
			if strings.Contains(lowerContent, strings.ToLower(keyword)) {
				result.Matched = true
				result.Matches = append(result.Matches, policy.Match{
					Type:  "keyword:" + category,
					Value: keyword,
				})
			}
		}
	}

	// Check custom keywords
	for _, keyword := range customKeywords {
		if strings.Contains(lowerContent, keyword) {
			result.Matched = true
			result.Matches = append(result.Matches, policy.Match{
				Type:  "keyword:custom",
				Value: keyword,
			})
		}
	}

	result.Original = content
	if result.Matched {
		result.Reason = "Blocked content detected"
	}

	return result, nil
}
