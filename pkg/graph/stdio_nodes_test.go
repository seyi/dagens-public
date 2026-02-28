package graph

import (
	"testing"
)

func TestStdinNode(t *testing.T) {
	// This is a basic test to make sure the StdinNode compiles and has the expected interface
	node := NewStdinNode(StdinNodeConfig{
		ID:       "test_stdin",
		Prompt:   "Enter value: ",
		StateKey: "test_input",
		TrimSpace: true,
	})
	
	if node.ID() != "test_stdin" {
		t.Errorf("Expected ID 'test_stdin', got '%s'", node.ID())
	}
	
	if node.Type() != "stdin" {
		t.Errorf("Expected type 'stdin', got '%s'", node.Type())
	}
}

func TestStdoutNode(t *testing.T) {
	// This is a basic test to make sure the StdoutNode compiles and has the expected interface
	node := NewStdoutNode(StdoutNodeConfig{
		ID:       "test_stdout",
		Template: "Value: {{.test_key}}",
	})
	
	if node.ID() != "test_stdout" {
		t.Errorf("Expected ID 'test_stdout', got '%s'", node.ID())
	}
	
	if node.Type() != "stdout" {
		t.Errorf("Expected type 'stdout', got '%s'", node.Type())
	}
}

func TestStdioCheckpointNode(t *testing.T) {
	// This is a basic test to make sure the StdioCheckpointNode compiles and has the expected interface
	node := NewStdioCheckpointNode(StdioCheckpointNodeConfig{
		ID:       "test_checkpoint",
		Prompt:   "Continue? ",
		StateKey: "checkpoint_input",
		ContinueKey: "should_continue",
		Timeout: 10,
	})
	
	if node.ID() != "test_checkpoint" {
		t.Errorf("Expected ID 'test_checkpoint', got '%s'", node.ID())
	}
	
	if node.Type() != "stdio_checkpoint" {
		t.Errorf("Expected type 'stdio_checkpoint', got '%s'", node.Type())
	}
}

func TestStdoutNodeTemplateFormatting(t *testing.T) {
	node := NewStdoutNode(StdoutNodeConfig{
		ID:       "test_stdout",
		Template: "Value: {{.test_key}}",
	})
	
	state := NewMemoryState()
	state.Set("test_key", "test_value")
	
	result := node.formatTemplate("Value: {{.test_key}}", state)
	expected := "Value: test_value"
	
	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}

func TestStdoutNodeTemplateFormattingMultipleKeys(t *testing.T) {
	node := NewStdoutNode(StdoutNodeConfig{
		ID:       "test_stdout",
		Template: "First: {{.first_key}}, Second: {{.second_key}}",
	})
	
	state := NewMemoryState()
	state.Set("first_key", "first_value")
	state.Set("second_key", "second_value")
	
	result := node.formatTemplate("First: {{.first_key}}, Second: {{.second_key}}", state)
	expected := "First: first_value, Second: second_value"
	
	if result != expected {
		t.Errorf("Expected '%s', got '%s'", expected, result)
	}
}