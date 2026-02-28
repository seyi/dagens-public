package agent

import (
	"fmt"
	"regexp"
	"strings"
)

// Template interpolation for ADK-compatible {key} syntax
// Enables declarative instruction definitions with state references

var templateRegex = regexp.MustCompile(`\{(\w+(?:\.\w+)*)\}`)

// InterpolateInstruction replaces {key} placeholders with values from context
// Supports both simple keys and nested keys (e.g., {data.field})
//
// Example:
//   context := map[string]interface{}{
//       "data": "processed_data",
//       "count": 42,
//   }
//   InterpolateInstruction("Process {data} with count {count}", context)
//   // Returns: "Process processed_data with count 42"
func InterpolateInstruction(instruction string, context map[string]interface{}) (string, error) {
	if context == nil {
		return instruction, nil
	}

	var errors []string

	result := templateRegex.ReplaceAllStringFunc(instruction, func(match string) string {
		// Extract key from {key}
		key := match[1 : len(match)-1]

		// Handle nested keys (e.g., data.field)
		value, err := getNestedValue(context, key)
		if err != nil {
			errors = append(errors, fmt.Sprintf("template key '%s' not found", key))
			return match // Keep placeholder if not found
		}

		return fmt.Sprintf("%v", value)
	})

	if len(errors) > 0 {
		// Return original with error for debugging, but don't fail
		// This matches ADK's lenient behavior
		return result, fmt.Errorf("template interpolation warnings: %s", strings.Join(errors, "; "))
	}

	return result, nil
}

// getNestedValue retrieves a value from a nested map using dot notation
// Example: getNestedValue({"data": {"field": "value"}}, "data.field") returns "value"
func getNestedValue(context map[string]interface{}, key string) (interface{}, error) {
	parts := strings.Split(key, ".")

	var current interface{} = context
	for i, part := range parts {
		switch v := current.(type) {
		case map[string]interface{}:
			val, ok := v[part]
			if !ok {
				return nil, fmt.Errorf("key '%s' not found at level %d", part, i)
			}
			current = val
		default:
			return nil, fmt.Errorf("cannot access field '%s' on non-map value", part)
		}
	}

	return current, nil
}

// InterpolateInstructionStrict is like InterpolateInstruction but returns error on missing keys
func InterpolateInstructionStrict(instruction string, context map[string]interface{}) (string, error) {
	result, err := InterpolateInstruction(instruction, context)
	if err != nil {
		return "", err
	}

	// Check if any placeholders remain (indicating missing keys)
	if templateRegex.MatchString(result) {
		return "", fmt.Errorf("template contains unresolved placeholders")
	}

	return result, nil
}

// HasTemplates returns true if the instruction contains {key} templates
func HasTemplates(instruction string) bool {
	return templateRegex.MatchString(instruction)
}

// ExtractTemplateKeys returns all template keys found in the instruction
func ExtractTemplateKeys(instruction string) []string {
	matches := templateRegex.FindAllStringSubmatch(instruction, -1)
	keys := make([]string, len(matches))
	for i, match := range matches {
		keys[i] = match[1] // Extract key from {key}
	}
	return keys
}
