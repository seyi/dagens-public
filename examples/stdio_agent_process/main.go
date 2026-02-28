// Simple agent process that communicates via stdin/stdout
// This process can be started separately and communicate with the main application
package main

import (
	"context"
	"log"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/ipc"
)

// SimpleEchoAgent implements a basic echo agent
type SimpleEchoAgent struct {
	id string
}

func (a *SimpleEchoAgent) ID() string { return a.id }
func (a *SimpleEchoAgent) Name() string { return a.id }
func (a *SimpleEchoAgent) Description() string { return "Simple echo agent" }
func (a *SimpleEchoAgent) Capabilities() []string { return []string{"echo"} }
func (a *SimpleEchoAgent) Dependencies() []agent.Agent { return []agent.Agent{} }
func (a *SimpleEchoAgent) Partition() string { return "" }

func (a *SimpleEchoAgent) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	result := input.Instruction
	if result == "" {
		// If no instruction, try to get from context
		if val, ok := input.Context["input"]; ok {
			result = val.(string)
		} else {
			result = "No input provided"
		}
	}
	
	return &agent.AgentOutput{
		Result: "Echo: " + result,
		Metadata: map[string]interface{}{
			"agent": "simple-echo",
		},
	}, nil
}

func main() {
	log.Println("Starting stdio agent process...")
	
	// Create the stdio agent server
	server := ipc.NewStdioAgentServer()
	
	// Register agents
	server.RegisterAgent("echo", &SimpleEchoAgent{id: "echo"})
	
	// Start listening on stdin/stdout
	log.Println("Agent server ready, listening on stdin...")
	if err := server.Start(); err != nil {
		log.Printf("Server error: %v", err)
	}
}