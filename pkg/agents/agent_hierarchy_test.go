package agents

import (
	"context"
	"fmt"
	"testing"

	"github.com/seyi/dagens/pkg/agent"
)

// TestAgentHierarchyBasics tests basic parent-child relationships
func TestAgentHierarchyBasics(t *testing.T) {
	t.Run("Create hierarchy via config", func(t *testing.T) {
		// Create child agents
		child1 := agent.NewAgent(agent.AgentConfig{
			Name: "child1",
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "child1 result"}, nil
			}),
		})

		child2 := agent.NewAgent(agent.AgentConfig{
			Name: "child2",
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "child2 result"}, nil
			}),
		})

		// Create parent with sub-agents
		parent := agent.NewAgent(agent.AgentConfig{
			Name:      "parent",
			SubAgents: []agent.Agent{child1, child2},
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "parent result"}, nil
			}),
		})

		// Verify parent references
		if child1.Parent() != parent {
			t.Error("child1 parent not set correctly")
		}

		if child2.Parent() != parent {
			t.Error("child2 parent not set correctly")
		}

		// Verify sub-agents list
		subAgents := parent.SubAgents()
		if len(subAgents) != 2 {
			t.Errorf("Expected 2 sub-agents, got %d", len(subAgents))
		}
	})

	t.Run("Add sub-agent dynamically", func(t *testing.T) {
		parent := agent.NewAgent(agent.AgentConfig{
			Name: "parent",
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "parent"}, nil
			}),
		})

		child := agent.NewAgent(agent.AgentConfig{
			Name: "child",
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "child"}, nil
			}),
		})

		// Add child dynamically
		err := parent.AddSubAgent(child)
		if err != nil {
			t.Fatalf("Failed to add sub-agent: %v", err)
		}

		// Verify relationship
		if child.Parent() != parent {
			t.Error("Parent not set after AddSubAgent")
		}

		if len(parent.SubAgents()) != 1 {
			t.Error("Sub-agent not added to parent's list")
		}
	})

	t.Run("Single parent rule", func(t *testing.T) {
		parent1 := agent.NewAgent(agent.AgentConfig{
			Name: "parent1",
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "p1"}, nil
			}),
		})

		parent2 := agent.NewAgent(agent.AgentConfig{
			Name: "parent2",
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "p2"}, nil
			}),
		})

		child := agent.NewAgent(agent.AgentConfig{
			Name: "child",
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "c"}, nil
			}),
		})

		// Add to first parent
		err := parent1.AddSubAgent(child)
		if err != nil {
			t.Fatalf("Failed to add to first parent: %v", err)
		}

		// Try to add to second parent (should fail)
		err = parent2.AddSubAgent(child)
		if err == nil {
			t.Error("Expected error when adding child with existing parent")
		}
	})
}

// TestAgentNavigation tests navigation methods
func TestAgentNavigation(t *testing.T) {
	t.Run("FindAgent by name", func(t *testing.T) {
		grandchild := agent.NewAgent(agent.AgentConfig{
			Name: "grandchild",
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "gc"}, nil
			}),
		})

		child := agent.NewAgent(agent.AgentConfig{
			Name:      "child",
			SubAgents: []agent.Agent{grandchild},
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "c"}, nil
			}),
		})

		parent := agent.NewAgent(agent.AgentConfig{
			Name:      "parent",
			SubAgents: []agent.Agent{child},
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "p"}, nil
			}),
		})

		// Find direct child
		found, err := parent.FindAgent("child")
		if err != nil {
			t.Fatalf("Failed to find direct child: %v", err)
		}
		if found.Name() != "child" {
			t.Error("Found wrong agent")
		}

		// Find grandchild
		found, err = parent.FindAgent("grandchild")
		if err != nil {
			t.Fatalf("Failed to find grandchild: %v", err)
		}
		if found.Name() != "grandchild" {
			t.Error("Found wrong agent")
		}

		// Try to find non-existent agent
		_, err = parent.FindAgent("nonexistent")
		if err == nil {
			t.Error("Expected error when finding non-existent agent")
		}
	})

	t.Run("GetRoot", func(t *testing.T) {
		grandchild := agent.NewAgent(agent.AgentConfig{
			Name: "grandchild",
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "gc"}, nil
			}),
		})

		child := agent.NewAgent(agent.AgentConfig{
			Name:      "child",
			SubAgents: []agent.Agent{grandchild},
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "c"}, nil
			}),
		})

		parent := agent.NewAgent(agent.AgentConfig{
			Name:      "parent",
			SubAgents: []agent.Agent{child},
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "p"}, nil
			}),
		})

		// Get root from grandchild
		root := grandchild.GetRoot()
		if root.Name() != "parent" {
			t.Errorf("Expected root 'parent', got %s", root.Name())
		}

		// Get root from parent (should be itself)
		root = parent.GetRoot()
		if root.Name() != "parent" {
			t.Error("Root of root should be itself")
		}
	})

	t.Run("GetPath", func(t *testing.T) {
		grandchild := agent.NewAgent(agent.AgentConfig{
			Name: "grandchild",
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "gc"}, nil
			}),
		})

		child := agent.NewAgent(agent.AgentConfig{
			Name:      "child",
			SubAgents: []agent.Agent{grandchild},
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "c"}, nil
			}),
		})

		_ = agent.NewAgent(agent.AgentConfig{
			Name:      "parent",
			SubAgents: []agent.Agent{child},
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "p"}, nil
			}),
		})

		// Get path from grandchild
		path := grandchild.GetPath()
		if len(path) != 3 {
			t.Errorf("Expected path length 3, got %d", len(path))
		}

		expectedNames := []string{"parent", "child", "grandchild"}
		for i, ag := range path {
			if ag.Name() != expectedNames[i] {
				t.Errorf("Path[%d]: expected %s, got %s", i, expectedNames[i], ag.Name())
			}
		}
	})

	t.Run("GetDepth", func(t *testing.T) {
		grandchild := agent.NewAgent(agent.AgentConfig{
			Name: "grandchild",
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "gc"}, nil
			}),
		})

		child := agent.NewAgent(agent.AgentConfig{
			Name:      "child",
			SubAgents: []agent.Agent{grandchild},
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "c"}, nil
			}),
		})

		parent := agent.NewAgent(agent.AgentConfig{
			Name:      "parent",
			SubAgents: []agent.Agent{child},
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "p"}, nil
			}),
		})

		if parent.GetDepth() != 0 {
			t.Errorf("Parent depth should be 0, got %d", parent.GetDepth())
		}

		if child.GetDepth() != 1 {
			t.Errorf("Child depth should be 1, got %d", child.GetDepth())
		}

		if grandchild.GetDepth() != 2 {
			t.Errorf("Grandchild depth should be 2, got %d", grandchild.GetDepth())
		}
	})
}

// TestAgentAsTooltest tests wrapping agents as tools
func TestAgentAsTool(t *testing.T) {
	t.Run("WrapAgentAsTool", func(t *testing.T) {
		testAgent := agent.NewAgent(agent.AgentConfig{
			Name:        "test-agent",
			Description: "A test agent",
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{
					Result: fmt.Sprintf("Processed: %s", input.Instruction),
				}, nil
			}),
		})

		tool := WrapAgentAsTool(testAgent)

		if tool.Name != "call_test-agent" {
			t.Errorf("Expected tool name 'call_test-agent', got %s", tool.Name)
		}

		// Test execution
		ctx := context.Background()
		result, err := tool.Handler(ctx, map[string]interface{}{
			"instruction": "do something",
		})

		if err != nil {
			t.Fatalf("Tool execution failed: %v", err)
		}

		expected := "Processed: do something"
		if result != expected {
			t.Errorf("Expected result '%s', got %v", expected, result)
		}
	})

	t.Run("CreateTransferTool", func(t *testing.T) {
		child := agent.NewAgent(agent.AgentConfig{
			Name: "specialist",
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{
					Result: fmt.Sprintf("Specialist handled: %s", input.Instruction),
				}, nil
			}),
		})

		parent := agent.NewAgent(agent.AgentConfig{
			Name:      "coordinator",
			SubAgents: []agent.Agent{child},
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "coordinator"}, nil
			}),
		})

		transferTool := CreateTransferTool(parent)

		if transferTool.Name != "transfer_to_agent" {
			t.Errorf("Expected tool name 'transfer_to_agent', got %s", transferTool.Name)
		}

		// Test transfer
		ctx := context.Background()
		result, err := transferTool.Handler(ctx, map[string]interface{}{
			"agent_name":  "specialist",
			"instruction": "do specialized work",
		})

		if err != nil {
			t.Fatalf("Transfer failed: %v", err)
		}

		expected := "Specialist handled: do specialized work"
		if result != expected {
			t.Errorf("Expected '%s', got %v", expected, result)
		}
	})

	t.Run("CreateHierarchicalToolRegistry", func(t *testing.T) {
		child1 := agent.NewAgent(agent.AgentConfig{
			Name: "agent1",
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "a1"}, nil
			}),
		})

		child2 := agent.NewAgent(agent.AgentConfig{
			Name: "agent2",
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "a2"}, nil
			}),
		})

		parent := agent.NewAgent(agent.AgentConfig{
			Name:      "parent",
			SubAgents: []agent.Agent{child1, child2},
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "p"}, nil
			}),
		})

		registry := CreateHierarchicalToolRegistry(parent)

		// Should have transfer_to_agent + 2 agents = 3 tools
		// Note: This assumes ToolRegistry has a way to count tools
		// If not available, we can test by trying to get each tool

		tool1, err := registry.Get("call_agent1")
		if err != nil {
			t.Error("agent1 tool not found in registry")
		}
		if tool1 == nil {
			t.Error("agent1 tool is nil")
		}

		tool2, err := registry.Get("call_agent2")
		if err != nil {
			t.Error("agent2 tool not found in registry")
		}
		if tool2 == nil {
			t.Error("agent2 tool is nil")
		}

		transferTool, err := registry.Get("transfer_to_agent")
		if err != nil {
			t.Error("transfer_to_agent tool not found in registry")
		}
		if transferTool == nil {
			t.Error("transfer_to_agent tool is nil")
		}
	})
}

// TestAgentToolBuilder tests the fluent builder
func TestAgentToolBuilder(t *testing.T) {
	t.Run("Custom agent tool", func(t *testing.T) {
		testAgent := agent.NewAgent(agent.AgentConfig{
			Name: "test",
			Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
				return &agent.AgentOutput{Result: "test result"}, nil
			}),
		})

		tool := NewAgentToolBuilder(testAgent).
			WithName("custom_tool").
			WithDescription("Custom description").
			WithPostProcess(func(output *agent.AgentOutput) (interface{}, error) {
				return fmt.Sprintf("Processed: %v", output.Result), nil
			}).
			Build()

		if tool.Name != "custom_tool" {
			t.Errorf("Expected name 'custom_tool', got %s", tool.Name)
		}

		if tool.Description != "Custom description" {
			t.Error("Description not set correctly")
		}

		// Test execution with post-processing
		ctx := context.Background()
		result, err := tool.Handler(ctx, map[string]interface{}{
			"instruction": "test",
		})

		if err != nil {
			t.Fatalf("Tool execution failed: %v", err)
		}

		expected := "Processed: test result"
		if result != expected {
			t.Errorf("Expected '%s', got %v", expected, result)
		}
	})
}

// TestRemoveSubAgent tests removing children
func TestRemoveSubAgent(t *testing.T) {
	child := agent.NewAgent(agent.AgentConfig{
		Name: "child",
		Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
			return &agent.AgentOutput{Result: "c"}, nil
		}),
	})

	parent := agent.NewAgent(agent.AgentConfig{
		Name:      "parent",
		SubAgents: []agent.Agent{child},
		Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
			return &agent.AgentOutput{Result: "p"}, nil
		}),
	})

	// Verify initial state
	if len(parent.SubAgents()) != 1 {
		t.Fatal("Child not added initially")
	}

	if child.Parent() != parent {
		t.Fatal("Parent not set initially")
	}

	// Remove child
	err := parent.RemoveSubAgent(child)
	if err != nil {
		t.Fatalf("Failed to remove sub-agent: %v", err)
	}

	// Verify removal
	if len(parent.SubAgents()) != 0 {
		t.Error("Child not removed from parent's list")
	}

	if child.Parent() != nil {
		t.Error("Parent reference not cleared")
	}

	// Try to remove again (should error)
	err = parent.RemoveSubAgent(child)
	if err == nil {
		t.Error("Expected error when removing non-existent child")
	}
}

// TestFormatAgentsForPrompt tests prompt formatting
func TestFormatAgentsForPrompt(t *testing.T) {
	child1 := agent.NewAgent(agent.AgentConfig{
		Name:         "greeter",
		Description:  "Greets users warmly",
		Capabilities: []string{"greeting", "welcoming"},
		Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
			return &agent.AgentOutput{Result: "hello"}, nil
		}),
	})

	child2 := agent.NewAgent(agent.AgentConfig{
		Name:         "calculator",
		Description:  "Performs calculations",
		Capabilities: []string{"math", "arithmetic"},
		Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
			return &agent.AgentOutput{Result: 42}, nil
		}),
	})

	parent := agent.NewAgent(agent.AgentConfig{
		Name:      "coordinator",
		SubAgents: []agent.Agent{child1, child2},
		Executor: agent.ExecutorFunc(func(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
			return &agent.AgentOutput{Result: "coord"}, nil
		}),
	})

	prompt := FormatAgentsForPrompt(parent)

	// Verify prompt contains agent names
	if !containsString(prompt, "greeter") {
		t.Error("Prompt missing 'greeter'")
	}

	if !containsString(prompt, "calculator") {
		t.Error("Prompt missing 'calculator'")
	}

	if !containsString(prompt, "transfer_to_agent") {
		t.Error("Prompt missing transfer instruction")
	}
}

// Helper function for string contains
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
