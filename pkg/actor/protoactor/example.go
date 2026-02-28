// Package protoactor provides an example implementation using the ProtoActor-Go library
package protoactor

import (
	"fmt"
	"log"
	"time"

	"github.com/asynkron/protoactor-go/actor"
)

// EchoActor is a simple example actor that implements our Actor interface
type EchoActor struct {
	name string
}

// NewEchoActor creates a new echo actor
func NewEchoActor(name string) *EchoActor {
	return &EchoActor{name: name}
}

// Receive implements the Actor interface
func (e *EchoActor) Receive(ctx Context) {
	switch ctx.Message.Type {
	case "invoke":
		if msg, ok := ctx.Message.Payload.(*InvokeMessage); ok {
			result := fmt.Sprintf("Echo from %s: %s", e.name, msg.Instruction)

			// If there's a sender, respond back
			if ctx.Context.Sender() != nil {
				response := &ResponseMessage{
					TaskID: msg.TaskID,
					Result: result,
				}
				ctx.Context.Respond(response)
			}
		}
	case "ping":
		if msg, ok := ctx.Message.Payload.(*PingMessage); ok {
			if ctx.Context.Sender() != nil {
				response := &PongMessage{
					ID:        msg.ID,
					Timestamp: time.Now(),
				}
				ctx.Context.Respond(response)
			}
		}
	}
}

// CalculatorActor is an example actor that performs calculations
type CalculatorActor struct {
	name string
}

// NewCalculatorActor creates a new calculator actor
func NewCalculatorActor(name string) *CalculatorActor {
	return &CalculatorActor{name: name}
}

// Receive implements the Actor interface
func (c *CalculatorActor) Receive(ctx Context) {
	switch ctx.Message.Type {
	case "invoke":
		if msg, ok := ctx.Message.Payload.(*InvokeMessage); ok {
			result := c.calculate(msg.Instruction)

			if ctx.Context.Sender() != nil {
				response := &ResponseMessage{
					TaskID: msg.TaskID,
					Result: result,
				}
				ctx.Context.Respond(response)
			}
		}
	}
}

// calculate performs simple calculations based on the instruction
func (c *CalculatorActor) calculate(instruction string) interface{} {
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

// Make sure the actor package is considered used
var _ *actor.PID = nil

// Example usage of the ProtoActor-Go system
func ExampleUsage() {
	// Create a new actor system
	system := NewSystem()
	defer func() {
		// In a real application, you'd want more graceful shutdown
	}()

	// Create and spawn an echo actor
	echoActor := NewEchoActor("test-echo")
	echoPid, err := system.Spawn(echoActor, "echo-actor")
	if err != nil {
		log.Printf("Error spawning echo actor: %v", err)
		return
	}

	fmt.Printf("Echo actor spawned with PID: %s\n", echoPid)

	// Create and spawn a calculator actor
	calcActor := NewCalculatorActor("test-calc")
	calcPid, err := system.Spawn(calcActor, "calc-actor")
	if err != nil {
		log.Printf("Error spawning calculator actor: %v", err)
		return
	}

	fmt.Printf("Calculator actor spawned with PID: %s\n", calcPid)

	// Test sending a message to the echo actor
	invokeMsg := InvokeMessage{
		AgentID:     "echo-actor",
		Instruction: "Hello, ProtoActor-Go!",
		TaskID:      "task-1",
		Timeout:     5 * time.Second,
	}

	response, err := system.RequestRaw(echoPid, &invokeMsg, 5*time.Second)
	if err != nil {
		log.Printf("Error sending message to echo actor: %v", err)
	} else {
		fmt.Printf("Echo actor response: %v\n", response)
	}

	// Test sending a message to the calculator actor
	calcMsg := InvokeMessage{
		AgentID:     "calc-actor",
		Instruction: "add 2 3",
		TaskID:      "task-2",
		Timeout:     5 * time.Second,
	}

	response, err = system.RequestRaw(calcPid, &calcMsg, 5*time.Second)
	if err != nil {
		log.Printf("Error sending message to calculator actor: %v", err)
	} else {
		fmt.Printf("Calculator actor response: %v\n", response)
	}

	// Test ping message
	pingMsg := PingMessage{ID: "test-ping"}
	response, err = system.RequestRaw(echoPid, &pingMsg, 5*time.Second)
	if err != nil {
		log.Printf("Error sending ping to echo actor: %v", err)
	} else {
		fmt.Printf("Ping response: %v\n", response)
	}

	// Demonstrate supervisor usage
	supervisor := NewSupervisor(OneForOne)
	
	calcConfig := ActorConfig{
		ID:       "supervised-calc",
		Actor:    NewCalculatorActor("supervised"),
		Strategy: OneForOne,
	}
	
	supervisedPid, err := supervisor.AddChild(system, calcConfig)
	if err != nil {
		log.Printf("Error adding supervised child: %v", err)
	} else {
		fmt.Printf("Supervised actor spawned with PID: %s\n", supervisedPid)
		fmt.Printf("Supervisor now managing %d children\n", supervisor.ChildCount())
		
		// Test the supervised actor
		supervisedMsg := InvokeMessage{
			AgentID:     "supervised-calc",
			Instruction: "multiply 6 7",
			TaskID:      "task-3",
			Timeout:     5 * time.Second,
		}
		
		response, err = system.RequestRaw(supervisedPid, &supervisedMsg, 5*time.Second)
		if err != nil {
			log.Printf("Error sending message to supervised actor: %v", err)
		} else {
			fmt.Printf("Supervised actor response: %v\n", response)
		}
	}
}