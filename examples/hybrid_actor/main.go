// Example demonstrating the hybrid A2A + Actor model approach
package main

import (
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/actor"
	"github.com/seyi/dagens/pkg/a2a"
	"github.com/seyi/dagens/pkg/agent"
)

// SimpleCalculatorActor implements a basic calculator as an actor
type SimpleCalculatorActor struct {
	name string
}

func (s *SimpleCalculatorActor) Receive(ctx actor.Context) {
	switch ctx.Message.Type {
	case actor.MessageTypeInvoke:
		if invokeMsg, ok := ctx.Message.Payload.(actor.InvokeMessage); ok {
			// Parse the instruction to perform a calculation
			result := s.calculate(invokeMsg.Instruction)
			
			response := actor.Message{
				Type: actor.MessageTypeResponse,
				Payload: actor.ResponseMessage{
					TaskID: invokeMsg.TaskID,
					Result: result,
				},
			}
			
			if ctx.Message.ReplyTo != nil {
				ctx.Message.ReplyTo <- response
			}
		}
	case actor.MessageTypePing:
		if ctx.Message.ReplyTo != nil {
			response := actor.Message{
				Type:    actor.MessageTypePong,
				Payload: actor.PongMessage{ID: s.name, Timestamp: time.Now()},
			}
			ctx.Message.ReplyTo <- response
		}
	}
}

func (s *SimpleCalculatorActor) calculate(instruction string) interface{} {
	// Simple calculation based on instruction
	// In a real implementation, this would parse the instruction and perform actual calculations
	switch instruction {
	case "add 2 3":
		return 5
	case "multiply 4 5":
		return 20
	case "subtract 10 3":
		return 7
	default:
		return fmt.Sprintf("Calculated: %s", instruction)
	}
}

func main() {
	fmt.Println("=== Hybrid A2A + Actor Model Demo ===\n")
	
	// 1. Create the actor system (internal communication)
	fmt.Println("1. Creating actor system for internal agent communication...")
	actorSystem := actor.NewSystem()
	defer actorSystem.Stop()
	
	// 2. Create and register internal agents as actors
	fmt.Println("2. Registering internal agents as actors...")
	calcActor := &SimpleCalculatorActor{name: "calculator-1"}
	calcAddr, err := actorSystem.Spawn(calcActor, "calculator-agent")
	if err != nil {
		fmt.Printf("Error spawning calculator actor: %v\n", err)
		return
	}
	
	fmt.Printf("   Calculator agent spawned with address: %s\n", calcAddr)
	
	// 3. Demonstrate internal actor communication
	fmt.Println("\n3. Testing internal actor communication...")
	
	invokeMsg := actor.Message{
		Type: actor.MessageTypeInvoke,
		Payload: actor.InvokeMessage{
			Instruction: "add 2 3",
			TaskID:      "task-internal",
		},
	}
	
	response, err := actorSystem.Request(calcAddr, invokeMsg, 5*time.Second)
	if err != nil {
		fmt.Printf("Error sending internal request: %v\n", err)
		return
	}
	
	if response.Type == actor.MessageTypeResponse {
		if resultMsg, ok := response.Payload.(actor.ResponseMessage); ok {
			fmt.Printf("   Internal result: %v\n", resultMsg.Result)
		}
	}
	
	// 4. Show how A2A protocol integrates with actor system
	fmt.Println("\n4. Demonstrating A2A protocol integration with actor system...")
	
	// Create a mock A2A client (in real usage, this would connect to external services)
	registry := a2a.NewDiscoveryRegistry()
	mockClient := a2a.NewHTTPA2AClient(registry)
	
	// Create the A2A-Actor adapter
	adapter := actor.NewA2AActorAdapter(actorSystem, mockClient)
	
	// Create agent input for the internal actor
	input := &agent.AgentInput{
		Instruction: "multiply 4 5",
		TaskID:      "task-adapter",
		Context:     make(map[string]interface{}),
		Timeout:     5 * time.Second,
	}
	
	// This would normally invoke an external agent via A2A, but since the agent
	// exists internally, it will use the actor system directly
	output, err := adapter.InvokeActor(nil, "calculator-agent", input)
	if err != nil {
		fmt.Printf("Error invoking via adapter: %v\n", err)
		return
	}
	
	fmt.Printf("   A2A-Actor adapter result: %v\n", output.Result)
	
	// 5. Show supervisor functionality
	fmt.Println("\n5. Demonstrating supervisor functionality...")
	supervisor := actor.NewSupervisor(actor.OneForOne, 3, 10*time.Second)
	supervisorAddr, err := actorSystem.Spawn(supervisor, "main-supervisor")
	if err != nil {
		fmt.Printf("Error spawning supervisor: %v\n", err)
		return
	}
	
	fmt.Printf("   Supervisor spawned with address: %s\n", supervisorAddr)
	fmt.Printf("   Supervisor managing %d children\n", supervisor.ChildCount())
	
	// 6. Performance comparison note
	fmt.Println("\n6. Performance characteristics:")
	fmt.Println("   - Internal actor communication: Microsecond latency")
	fmt.Println("   - External A2A communication: Millisecond latency (network)")
	fmt.Println("   - Actor system: Thousands of lightweight processes")
	fmt.Println("   - A2A protocol: Interoperable with external systems")
	
	fmt.Println("\n=== Demo Complete ===")
	fmt.Println("\nThis hybrid approach provides:")
	fmt.Println("- High-performance internal communication via actors")
	fmt.Println("- Fault tolerance through supervision")
	fmt.Println("- External interoperability via A2A protocol")
	fmt.Println("- Seamless integration between both systems")
}