// Package main demonstrates the actor-based agent communication system
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/a2a"
	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/actor/protoactor"
)

// AgentRouter implements the hybrid architecture that chooses between
// actor-based and A2A communication based on agent location
type AgentRouter struct {
	actorSystem *protoactor.System
	a2aClient   a2a.A2AClient
	localAgents map[string]bool // Tracks which agents are available locally
}

// NewAgentRouter creates a new agent router with the hybrid architecture
func NewAgentRouter(actorSystem *protoactor.System, a2aClient a2a.A2AClient) *AgentRouter {
	return &AgentRouter{
		actorSystem: actorSystem,
		a2aClient:   a2aClient,
		localAgents: make(map[string]bool),
	}
}

// RegisterLocalAgent registers an agent as available locally (via actors)
func (r *AgentRouter) RegisterLocalAgent(agentID string) {
	r.localAgents[agentID] = true
}

// Invoke routes agent invocation to the appropriate system based on agent location
func (r *AgentRouter) Invoke(agentID string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// Check if agent exists locally as actor
	if r.isLocalAgent(agentID) {
		// Fast path: local actor communication
		return r.invokeViaActor(agentID, input)
	}

	// Fallback: A2A HTTP communication for remote agents
	return r.invokeViaA2A(agentID, input)
}

// isLocalAgent checks if an agent is available locally
func (r *AgentRouter) isLocalAgent(agentID string) bool {
	_, exists := r.localAgents[agentID]
	return exists
}

// invokeViaActor handles invocation through the actor system
func (r *AgentRouter) invokeViaActor(agentID string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// Convert agent input to actor message
	invokeMsg := protoactor.InvokeMessage{
		AgentID:     agentID,
		Instruction: input.Instruction,
		Context:     input.Context,
		TaskID:      input.TaskID,
		Timeout:     input.Timeout,
	}

	// Send to actor and wait for response
	pid, ok := r.actorSystem.GetActorByID(agentID)
	if !ok {
		return nil, fmt.Errorf("actor %s not found", agentID)
	}
	response, err := r.actorSystem.RequestRaw(pid, invokeMsg, input.Timeout)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke actor %s: %w", agentID, err)
	}

	// Convert response back to agent output
	switch resp := response.(type) {
	case *protoactor.ResponseMessage:
		return &agent.AgentOutput{
			TaskID: resp.TaskID,
			Result: resp.Result,
		}, nil
	case *protoactor.ErrorMessage:
		return nil, fmt.Errorf("actor error: %s", resp.Error)
	default:
		return &agent.AgentOutput{
			TaskID: input.TaskID,
			Result: response,
		}, nil
	}
}

// invokeViaA2A handles invocation through the A2A protocol
func (r *AgentRouter) invokeViaA2A(agentID string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	if r.a2aClient == nil {
		return nil, fmt.Errorf("A2A client not configured for remote agent invocation")
	}

	return r.a2aClient.InvokeAgent(context.Background(), agentID, input)
}

// Example 1: High-Frequency Internal Agent Collaboration
// Coordinator agent orchestrates multiple specialist agents
func (r *AgentRouter) HighFrequencyCollaborationExample(topics []string) error {
	fmt.Println("=== High-Frequency Internal Agent Collaboration Example ===")
	
	// Register research agents as local
	for i := range topics {
		agentID := fmt.Sprintf("research-%d", i)
		r.RegisterLocalAgent(agentID)
	}

	// Spawn research agents as parallel actors
		var pid protoactor.Address
		for i, topic := range topics {
			agentID := fmt.Sprintf("research-%d", i)
			researchAgent := &ResearchAgent{
				Name:  agentID,
				Topic: topic,
			}
			
			var err error
			pid, err = r.actorSystem.Spawn(researchAgent, agentID)
			if err != nil {
				return fmt.Errorf("failed to spawn research agent %s: %w", agentID, err)
			}
	
			// Send research instruction
			invokeMsg := protoactor.InvokeMessage{
				Instruction: "research " + topic,
				TaskID:      fmt.Sprintf("research-task-%d", i),
			}
			
			// Send asynchronously
			r.actorSystem.SendRaw(pid, invokeMsg)
		}
	// Collect results and send to synthesis agent
	resultsChan := make(chan string, len(topics))
	
	// In a real implementation, we'd collect results from research agents
	// For this example, we'll simulate the collection process
	time.Sleep(100 * time.Millisecond)
	
	fmt.Printf("Collected results from %d research agents\n", len(topics))
	
	// Create synthesis agent
	synthesisAgent := &SynthesisAgent{Name: "synthesis-agent"}
	r.RegisterLocalAgent("synthesis-agent")
	
	synthPID, err := r.actorSystem.Spawn(synthesisAgent, "synthesis-agent")
	if err != nil {
		return fmt.Errorf("failed to spawn synthesis agent: %w", err)
	}
	
	// Send collected results to synthesis agent
	synthMsg := protoactor.InvokeMessage{
		Instruction: "synthesize research results",
		Context:     map[string]interface{}{"results": resultsChan},
		TaskID:      "synthesis-task",
	}
	
	r.actorSystem.SendRaw(synthPID, synthMsg)
	
	fmt.Println("High-frequency collaboration completed")
	return nil
}

// Example 2: Streaming Multi-Agent Pipelines
// Input → Parser → Analyzer → Responder
func (r *AgentRouter) StreamingPipelineExample(inputStream <-chan string) error {
	fmt.Println("\n=== Streaming Multi-Agent Pipeline Example ===")
	
	// Register pipeline agents as local
	r.RegisterLocalAgent("parser-agent")
	r.RegisterLocalAgent("analyzer-agent")
	r.RegisterLocalAgent("responder-agent")
	
	// Create pipeline agents
	parserAgent := &ParserAgent{Name: "parser-agent"}
	analyzerAgent := &AnalyzerAgent{Name: "analyzer-agent"}
	responderAgent := &ResponderAgent{Name: "responder-agent"}
	
	// Spawn agents
	parserPID, err := r.actorSystem.Spawn(parserAgent, "parser-agent")
	if err != nil {
		return fmt.Errorf("failed to spawn parser agent: %w", err)
	}
	
	_, err = r.actorSystem.Spawn(analyzerAgent, "analyzer-agent")
	if err != nil {
		return fmt.Errorf("failed to spawn analyzer agent: %w", err)
	}
	
	_, err = r.actorSystem.Spawn(responderAgent, "responder-agent")
	if err != nil {
		return fmt.Errorf("failed to spawn responder agent: %w", err)
	}
	
	// Set up pipeline connections
	// In a real implementation, we'd pass PIDs to each agent for forwarding
	// For this example, we'll simulate the pipeline
	go func() {
		for chunk := range inputStream {
			// Send chunk to parser
			parserMsg := protoactor.InvokeMessage{
				Instruction: "parse",
				Context:     map[string]interface{}{"chunk": chunk},
				TaskID:      "parse-task",
			}
			r.actorSystem.SendRaw(parserPID, parserMsg)
			
			// In a real pipeline, the parser would forward to analyzer, etc.
			time.Sleep(10 * time.Millisecond) // Simulate processing
		}
	}()
	
	fmt.Println("Streaming pipeline initiated")
	return nil
}

// Example 3: Fault-Tolerant Agent Workflows
// Supervisor with OneForOne strategy
func (r *AgentRouter) FaultTolerantWorkflowExample() error {
	fmt.Println("\n=== Fault-Tolerant Agent Workflow Example ===")
	
	// Create a supervisor
	supervisor := protoactor.NewSupervisor(protoactor.OneForOne)
	
	// Create critical agents with supervision
	agents := []struct {
		ID       string
		Strategy protoactor.SupervisorStrategy
	}{
		{"critical-agent-1", protoactor.OneForOne},
		{"critical-agent-2", protoactor.OneForOne},
		{"critical-agent-3", protoactor.OneForOne},
	}
	
	for _, agentInfo := range agents {
		criticalAgent := &CriticalAgent{
			Name: agentInfo.ID,
		}
		
		config := protoactor.ActorConfig{
			ID:       agentInfo.ID,
			Actor:    criticalAgent,
			Strategy: agentInfo.Strategy,
		}
		
		_, err := supervisor.AddChild(r.actorSystem, config)
		if err != nil {
			return fmt.Errorf("failed to add supervised child %s: %w", agentInfo.ID, err)
		}
		
		r.RegisterLocalAgent(agentInfo.ID)
	}
	
	fmt.Printf("Created supervisor managing %d critical agents\n", len(agents))
	fmt.Printf("Supervisor configured with OneForOne strategy\n")
	
	// Simulate the supervisor functionality
	fmt.Println("Supervisor ready - will restart failed agents automatically")
	return nil
}

// Example 4: Dynamic Agent Scaling
// Scale workers up/down based on demand
func (r *AgentRouter) DynamicScalingExample(targetWorkers int) error {
	fmt.Println("\n=== Dynamic Agent Scaling Example ===")
	
	currentWorkers := len(r.getWorkerAgents())
	
	if targetWorkers > currentWorkers {
		// Scale up
		for i := currentWorkers; i < targetWorkers; i++ {
			workerID := fmt.Sprintf("worker-%d", i)
			workerAgent := &WorkerAgent{
				Name: workerID,
			}
			
			_, err := r.actorSystem.Spawn(workerAgent, workerID)
			if err != nil {
				return fmt.Errorf("failed to spawn worker agent %s: %w", workerID, err)
			}
			
			r.RegisterLocalAgent(workerID)
			fmt.Printf("Spawned worker: %s\n", workerID)
		}
	} else if targetWorkers < currentWorkers {
		// Scale down
		workerAgents := r.getWorkerAgents()
		for i := targetWorkers; i < currentWorkers; i++ {
			workerID := fmt.Sprintf("worker-%d", i)
			if _, exists := workerAgents[workerID]; exists {
				// In a real implementation, we'd send a poison pill to gracefully shut down
				if pid, ok := r.actorSystem.GetActorByID(workerID); ok {
					r.actorSystem.PoisonPill(pid)
				}
				delete(r.localAgents, workerID)
				fmt.Printf("Terminated worker: %s\n", workerID)
			}
		}
	}
	
	fmt.Printf("Scaled to %d workers\n", targetWorkers)
	return nil
}

// getWorkerAgents returns all currently registered worker agents
func (r *AgentRouter) getWorkerAgents() map[string]bool {
	workers := make(map[string]bool)
	for agentID, isLocal := range r.localAgents {
		if isLocal && len(agentID) > 7 && agentID[:6] == "worker" {
			workers[agentID] = true
		}
	}
	return workers
}

// Example agent implementations for the demonstrations

// ResearchAgent implements a research-focused agent
type ResearchAgent struct {
	Name  string
	Topic string
}

func (r *ResearchAgent) Receive(ctx protoactor.Context) {
	switch ctx.Message.Type {
	case "invoke":
		if msg, ok := ctx.Message.Payload.(protoactor.InvokeMessage); ok {
			// Simulate research processing
			result := fmt.Sprintf("Research results for: %s", msg.Instruction)
			
			if ctx.Message.ReplyTo != nil {
				response := protoactor.Message{
					Type: "response",
					Payload: protoactor.ResponseMessage{
						TaskID: msg.TaskID,
						Result: result,
					},
				}
				ctx.Message.ReplyTo <- response
			}
		}
	}
}

// SynthesisAgent implements a synthesis agent
type SynthesisAgent struct {
	Name string
}

func (s *SynthesisAgent) Receive(ctx protoactor.Context) {
	switch ctx.Message.Type {
	case "invoke":
		if msg, ok := ctx.Message.Payload.(protoactor.InvokeMessage); ok {
			// Simulate synthesis processing
			result := fmt.Sprintf("Synthesized from: %s", msg.Instruction)
			
			if ctx.Message.ReplyTo != nil {
				response := protoactor.Message{
					Type: "response",
					Payload: protoactor.ResponseMessage{
						TaskID: msg.TaskID,
						Result: result,
					},
				}
				ctx.Message.ReplyTo <- response
			}
		}
	}
}

// ParserAgent implements a parsing agent for streaming
type ParserAgent struct {
	Name string
}

func (p *ParserAgent) Receive(ctx protoactor.Context) {
	switch ctx.Message.Type {
	case "invoke":
		if msg, ok := ctx.Message.Payload.(protoactor.InvokeMessage); ok {
			// Simulate parsing
			parsed := fmt.Sprintf("Parsed: %v", msg.Context["chunk"])
			
			// In a real implementation, we'd forward to the analyzer
			fmt.Printf("Parser %s processed: %s\n", p.Name, parsed)
		}
	}
}

// AnalyzerAgent implements an analysis agent for streaming
type AnalyzerAgent struct {
	Name string
}

func (a *AnalyzerAgent) Receive(ctx protoactor.Context) {
	switch ctx.Message.Type {
	case "invoke":
		if msg, ok := ctx.Message.Payload.(protoactor.InvokeMessage); ok {
			// Simulate analysis
			analyzed := fmt.Sprintf("Analyzed: %v", msg.Context["parsed_data"])
			
			// In a real implementation, we'd forward to the responder
			fmt.Printf("Analyzer %s processed: %s\n", a.Name, analyzed)
		}
	}
}

// ResponderAgent implements a response agent for streaming
type ResponderAgent struct {
	Name string
}

func (r *ResponderAgent) Receive(ctx protoactor.Context) {
	switch ctx.Message.Type {
	case "invoke":
		if msg, ok := ctx.Message.Payload.(protoactor.InvokeMessage); ok {
			// Simulate response generation
			response := fmt.Sprintf("Response to: %v", msg.Context["analyzed_data"])
			
			fmt.Printf("Responder %s generated: %s\n", r.Name, response)
		}
	}
}

// CriticalAgent implements a critical agent that needs supervision
type CriticalAgent struct {
	Name string
}

func (c *CriticalAgent) Receive(ctx protoactor.Context) {
	switch ctx.Message.Type {
	case "invoke":
		if msg, ok := ctx.Message.Payload.(protoactor.InvokeMessage); ok {
			// Simulate critical processing
			result := fmt.Sprintf("Processed critical task: %s", msg.Instruction)
			
			if ctx.Message.ReplyTo != nil {
				response := protoactor.Message{
					Type: "response",
					Payload: protoactor.ResponseMessage{
						TaskID: msg.TaskID,
						Result: result,
					},
				}
				ctx.Message.ReplyTo <- response
			}
		}
	}
}

// WorkerAgent implements a worker agent that can be scaled dynamically
type WorkerAgent struct {
	Name string
}

func (w *WorkerAgent) Receive(ctx protoactor.Context) {
	switch ctx.Message.Type {
	case "invoke":
		if msg, ok := ctx.Message.Payload.(protoactor.InvokeMessage); ok {
			// Simulate work processing
			result := fmt.Sprintf("Work completed by %s: %s", w.Name, msg.Instruction)

			if ctx.Message.ReplyTo != nil {
				response := protoactor.Message{
					Type: "response",
					Payload: protoactor.ResponseMessage{
						TaskID: msg.TaskID,
						Result: result,
					},
				}
				ctx.Message.ReplyTo <- response
			}
		}
	}
}

func main() {
	fmt.Println("=== Dagens Actor Communication Demo ===")
	fmt.Println()

	// Create actor system
	actorSystem := protoactor.NewSystem()

	// Create agent router (without A2A client for local demo)
	router := NewAgentRouter(actorSystem, nil)

	// Run examples
	topics := []string{"AI", "Distributed Systems", "Go Programming"}
	if err := router.HighFrequencyCollaborationExample(topics); err != nil {
		fmt.Printf("High-frequency collaboration error: %v\n", err)
	}

	// Create a simple input stream for streaming pipeline
	inputStream := make(chan string, 3)
	go func() {
		inputStream <- "chunk1"
		inputStream <- "chunk2"
		inputStream <- "chunk3"
		close(inputStream)
	}()

	if err := router.StreamingPipelineExample(inputStream); err != nil {
		fmt.Printf("Streaming pipeline error: %v\n", err)
	}

	if err := router.FaultTolerantWorkflowExample(); err != nil {
		fmt.Printf("Fault-tolerant workflow error: %v\n", err)
	}

	if err := router.DynamicScalingExample(5); err != nil {
		fmt.Printf("Dynamic scaling error: %v\n", err)
	}

	// Give some time for async operations
	time.Sleep(500 * time.Millisecond)

	fmt.Println("\n=== Demo Complete ===")
}