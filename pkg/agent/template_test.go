package agent

import (
	"testing"
)

func TestInterpolateInstructionSimple(t *testing.T) {
	context := map[string]interface{}{
		"name":  "Alice",
		"count": 42,
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "single key",
			input:    "Hello {name}",
			expected: "Hello Alice",
		},
		{
			name:     "multiple keys",
			input:    "Process {count} items for {name}",
			expected: "Process 42 items for Alice",
		},
		{
			name:     "no templates",
			input:    "No templates here",
			expected: "No templates here",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := InterpolateInstruction(tt.input, context)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestInterpolateInstructionNested(t *testing.T) {
	context := map[string]interface{}{
		"user": map[string]interface{}{
			"name": "Bob",
			"age":  30,
			"address": map[string]interface{}{
				"city": "New York",
			},
		},
		"order": map[string]interface{}{
			"id":    "12345",
			"total": 99.99,
		},
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "one level nested",
			input:    "User: {user.name}, Age: {user.age}",
			expected: "User: Bob, Age: 30",
		},
		{
			name:     "two levels nested",
			input:    "City: {user.address.city}",
			expected: "City: New York",
		},
		{
			name:     "mixed nested and simple",
			input:    "Order {order.id} for {user.name} total: {order.total}",
			expected: "Order 12345 for Bob total: 99.99",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := InterpolateInstruction(tt.input, context)
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestInterpolateInstructionMissingKeys(t *testing.T) {
	context := map[string]interface{}{
		"existing": "value",
	}

	input := "Has {existing} but missing {nonexistent}"
	result, err := InterpolateInstruction(input, context)

	// Should warn about missing key
	if err == nil {
		t.Error("Expected error for missing key")
	}

	// Should still return partially interpolated result
	if result != "Has value but missing {nonexistent}" {
		t.Errorf("Expected partial interpolation, got '%s'", result)
	}
}

func TestInterpolateInstructionNilContext(t *testing.T) {
	input := "No context available"
	result, err := InterpolateInstruction(input, nil)

	if err != nil {
		t.Errorf("Unexpected error with nil context: %v", err)
	}

	if result != input {
		t.Errorf("Expected unchanged input, got '%s'", result)
	}
}

func TestGetNestedValue(t *testing.T) {
	context := map[string]interface{}{
		"level1": map[string]interface{}{
			"level2": map[string]interface{}{
				"level3": "deep_value",
			},
			"simple": "value",
		},
		"top": "top_value",
	}

	tests := []struct {
		name     string
		key      string
		expected interface{}
		hasError bool
	}{
		{
			name:     "top level",
			key:      "top",
			expected: "top_value",
			hasError: false,
		},
		{
			name:     "one level deep",
			key:      "level1.simple",
			expected: "value",
			hasError: false,
		},
		{
			name:     "three levels deep",
			key:      "level1.level2.level3",
			expected: "deep_value",
			hasError: false,
		},
		{
			name:     "nonexistent key",
			key:      "nonexistent",
			expected: nil,
			hasError: true,
		},
		{
			name:     "nonexistent nested",
			key:      "level1.nonexistent",
			expected: nil,
			hasError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := getNestedValue(context, tt.key)

			if tt.hasError {
				if err == nil {
					t.Error("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}

				if result != tt.expected {
					t.Errorf("Expected %v, got %v", tt.expected, result)
				}
			}
		})
	}
}

func TestHasTemplates(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"{key}", true},
		{"No templates", false},
		{"Has {template}", true},
		{"Multiple {key1} and {key2}", true},
		{"Nested {data.field}", true},
		{"{}", false}, // Empty braces don't count
		{"", false},
	}

	for _, tt := range tests {
		result := HasTemplates(tt.input)
		if result != tt.expected {
			t.Errorf("HasTemplates('%s') = %v, expected %v", tt.input, result, tt.expected)
		}
	}
}

func TestExtractTemplateKeys(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{
			input:    "{key1} and {key2}",
			expected: []string{"key1", "key2"},
		},
		{
			input:    "Nested {data.field}",
			expected: []string{"data.field"},
		},
		{
			input:    "No templates",
			expected: []string{},
		},
		{
			input:    "{a} {b} {c}",
			expected: []string{"a", "b", "c"},
		},
	}

	for _, tt := range tests {
		result := ExtractTemplateKeys(tt.input)

		if len(result) != len(tt.expected) {
			t.Errorf("ExtractTemplateKeys('%s') returned %d keys, expected %d", tt.input, len(result), len(tt.expected))
			continue
		}

		for i, key := range result {
			if key != tt.expected[i] {
				t.Errorf("ExtractTemplateKeys('%s')[%d] = '%s', expected '%s'", tt.input, i, key, tt.expected[i])
			}
		}
	}
}

func TestInterpolateInstructionStrict(t *testing.T) {
	context := map[string]interface{}{
		"name": "Alice",
	}

	// Should succeed when all keys present
	result, err := InterpolateInstructionStrict("Hello {name}", context)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if result != "Hello Alice" {
		t.Errorf("Expected 'Hello Alice', got '%s'", result)
	}

	// Should fail when key missing
	_, err = InterpolateInstructionStrict("Hello {name} and {missing}", context)
	if err == nil {
		t.Error("Expected error for missing key in strict mode")
	}
}

func BenchmarkInterpolateInstruction(b *testing.B) {
	context := map[string]interface{}{
		"user": map[string]interface{}{
			"name": "Alice",
			"id":   12345,
		},
		"action": "update",
		"count":  42,
	}

	instruction := "User {user.name} (ID: {user.id}) will {action} {count} items"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = InterpolateInstruction(instruction, context)
	}
}
