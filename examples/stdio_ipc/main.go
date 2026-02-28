// Example demonstrating IPC communication between agents using stdin/stdout
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/graph"
	"github.com/seyi/dagens/pkg/ipc"
)

// EchoAgent implements a simple echo agent
type EchoAgent struct {
	id string
}

func (a *EchoAgent) ID() string { return a.id }
func (a *EchoAgent) Name() string { return a.id }
func (a *EchoAgent) Description() string { return "Echoes input back" }
func (a *EchoAgent) Capabilities() []string { return []string{"echo"} }
func (a *EchoAgent) Dependencies() []agent.Agent { return []agent.Agent{} }
func (a *EchoAgent) Partition() string { return "" }

func (a *EchoAgent) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	instruction := input.Instruction
	if instruction == "" {
		// Try to get from context if not in instruction
		if ctxVal, ok := input.Context["input"]; ok {
			instruction = fmt.Sprintf("%v", ctxVal)
		}
	}
	
	result := fmt.Sprintf("Echo: %s", instruction)
	
	return &agent.AgentOutput{
		Result: result,
		Metadata: map[string]interface{}{
			"agent": "echo",
			"processed_at": time.Now().Unix(),
		},
	}, nil
}

// CalculatorAgent implements a simple calculator agent
type CalculatorAgent struct {
	id string
}

func (a *CalculatorAgent) ID() string { return a.id }
func (a *CalculatorAgent) Name() string { return a.id }
func (a *CalculatorAgent) Description() string { return "Performs basic calculations" }
func (a *CalculatorAgent) Capabilities() []string { return []string{"calculate"} }
func (a *CalculatorAgent) Dependencies() []agent.Agent { return []agent.Agent{} }
func (a *CalculatorAgent) Partition() string { return "" }

func (a *CalculatorAgent) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// Extract operands from context
	left, ok1 := input.Context["left"].(float64)
	right, ok2 := input.Context["right"].(float64)
	op, ok3 := input.Context["operation"].(string)
	
	if !ok1 || !ok2 || !ok3 {
		return nil, fmt.Errorf("missing required parameters: left=%v, right=%v, operation=%v", ok1, ok2, ok3)
	}
	
	var result float64
	switch op {
	case "add":
		result = left + right
	case "subtract":
		result = left - right
	case "multiply":
		result = left * right
	case "divide":
		if right == 0 {
			return nil, fmt.Errorf("division by zero")
		}
		result = left / right
	default:
		return nil, fmt.Errorf("unknown operation: %s", op)
	}
	
	return &agent.AgentOutput{
		Result: result,
		Metadata: map[string]interface{}{
			"agent": "calculator",
			"operation": op,
			"processed_at": time.Now().Unix(),
		},
	}, nil
}

func main() {
	fmt.Println("=== Stdio IPC Agent Communication Demo ===")
	
	// Example 1: Using the server mode (for agent processes)
	fmt.Println("\nExample 1: Agent Server (for processes that respond to messages)")
	
	// Create a server that can handle multiple agents
	server := ipc.NewStdioAgentServer()
	
	// Register agents
	server.RegisterAgent("echo", &EchoAgent{id: "echo"})
	server.RegisterAgent("calculator", &CalculatorAgent{id: "calculator"})
	
	// In a real scenario, this would run as a separate process that listens on stdin/stdout
	// For this demo, we'll just show how it would be used
	fmt.Println("  Server ready to handle agent requests via stdin/stdout")
	fmt.Println("  Agents registered: echo, calculator")
	
	// Example 2: Using process agents (for agents running in separate processes)
	fmt.Println("\nExample 2: Process Agent Communication")
	
	// In a real implementation, we would have a separate binary that uses the server
	// For this demo, we'll simulate by creating a graph with a process agent node

	// For this demo, we'll create a mock process agent (in reality, this would connect to an actual process)
	// Since we can't start a real process here, we'll just show the structure
	fmt.Println("  Process agent would be started with: ./agent-process --mode=stdio")
	fmt.Println("  Communication would happen via stdin/stdout JSON messages")
	
	// Example 3: Graph with stdio agent nodes
	fmt.Println("\nExample 3: Graph with Stdio Agent Nodes")
	
	// Create a graph that uses stdio agent communication
	graphExample()
	
	fmt.Println("\n=== Demo Complete ===")
}

func graphExample() {
	// In a real implementation, we would create nodes that communicate with external processes
	// For this demo, we'll show how the nodes would be structured

	// Create a state with some initial data
	state := graph.NewMemoryState()
	state.Set("input", "Hello from the graph!")
	state.Set("operation", "echo")

	input, _ := state.Get("input")
	operation, _ := state.Get("operation")
	fmt.Printf("  Initial state: input=%v, operation=%v\n",
		input, operation)

	// In a real implementation, we would:
	// 1. Create a ProcessAgent that connects to an external process
	// 2. Create a StdioAgentNode that uses the ProcessAgent
	// 3. Add it to the graph and execute

	// Since we can't run external processes in this demo, we'll just show the concept:
	fmt.Println("  In a real implementation:")
	fmt.Println("  - ProcessAgent would start external agent processes")
	fmt.Println("  - StdioAgentNode would send/receive JSON messages via stdin/stdout")
	fmt.Println("  - Agents would respond asynchronously with structured data")
	fmt.Println("  - Communication would be similar to Erlang's message passing")

	// Simulate what would happen during execution
	fmt.Println("\n  Simulated execution flow:")
	fmt.Println("  1. Graph node prepares message with current state")
	fmt.Println("  2. Message sent to external agent process via stdin")
	fmt.Println("  3. External agent processes message and responds via stdout")
	fmt.Println("  4. Response parsed and state updated")
}