package main

import (
	"fmt"
	"log"

	"github.com/seyi/dagens/pkg/actor/protoactor"
)

func main() {
	fmt.Println("=== ProtoActor-Go Implementation Demo ===\n")
	
	// 1. Create the ProtoActor-Go system
	fmt.Println("1. Creating ProtoActor-Go system...")
	system := protoactor.NewSystem()
	defer func() {
		// In a real application, you'd want proper cleanup
	}()
	
	// 2. Create and spawn actors
	fmt.Println("2. Creating and spawning actors...")
	
	echoActor := protoactor.NewEchoActor("demo-echo")
	echoPid, err := system.Spawn(echoActor, "echo-actor")
	if err != nil {
		log.Printf("Error spawning echo actor: %v", err)
		return
	}
	
	calcActor := protoactor.NewCalculatorActor("demo-calc") 
	calcPid, err := system.Spawn(calcActor, "calc-actor")
	if err != nil {
		log.Printf("Error spawning calculator actor: %v", err)
		return
	}
	
	fmt.Printf("   Echo actor spawned: %s\n", echoPid)
	fmt.Printf("   Calculator actor spawned: %s\n", calcPid)
	
	// 3. Test actor communication
	fmt.Println("\n3. Testing actor communication...")
	
	// Test echo actor
	echoMsg := protoactor.InvokeMessage{
		AgentID:     "echo-actor",
		Instruction: "Hello from ProtoActor-Go!",
		TaskID:      "test-echo",
	}
	
	response, err := system.RequestRaw(echoPid, &echoMsg, 5)
	if err != nil {
		log.Printf("Error sending to echo actor: %v", err)
	} else {
		fmt.Printf("   Echo response: %v\n", response)
	}
	
	// Test calculator actor
	calcMsg := protoactor.InvokeMessage{
		AgentID:     "calc-actor",
		Instruction: "multiply 6 7",
		TaskID:      "test-calc",
	}
	
	response, err = system.RequestRaw(calcPid, &calcMsg, 5)
	if err != nil {
		log.Printf("Error sending to calculator actor: %v", err)
	} else {
		fmt.Printf("   Calc response: %v\n", response)
	}
	
	// 4. Test supervisor functionality
	fmt.Println("\n4. Testing supervisor functionality...")
	supervisor := protoactor.NewSupervisor(protoactor.OneForOne)
	
	calcConfig := protoactor.ActorConfig{
		ID:       "supervised-calc",
		Actor:    protoactor.NewCalculatorActor("supervised"),
		Strategy: protoactor.OneForOne,
	}
	
	supervisedPid, err := supervisor.AddChild(system, calcConfig)
	if err != nil {
		log.Printf("Error adding supervised child: %v", err)
	} else {
		fmt.Printf("   Supervised actor spawned: %s\n", supervisedPid)
		fmt.Printf("   Supervisor managing %d children\n", supervisor.ChildCount())
		
		// Test the supervised actor
		supervisedMsg := protoactor.InvokeMessage{
			AgentID:     "supervised-calc",
			Instruction: "add 10 5",
			TaskID:      "test-supervised",
		}
		
		response, err := system.RequestRaw(supervisedPid, &supervisedMsg, 5)
		if err != nil {
			log.Printf("Error sending to supervised actor: %v", err)
		} else {
			fmt.Printf("   Supervised response: %v\n", response)
		}
	}
	
	// 5. Compare with original custom actor system
	fmt.Println("\n5. Comparison with original system:")
	fmt.Println("   - ProtoActor-Go: Production-ready with distributed capabilities")
	fmt.Println("   - ProtoActor-Go: Built-in supervision and fault tolerance")
	fmt.Println("   - ProtoActor-Go: Battle-tested in production systems")
	fmt.Println("   - Original: Simpler, custom implementation")
	fmt.Println("   - Original: More control over implementation details")
	
	fmt.Println("\n=== Demo Complete ===")
	fmt.Println("\nProtoActor-Go implementation provides:")
	fmt.Println("- Mature actor model implementation")
	fmt.Println("- Built-in supervision strategies") 
	fmt.Println("- Distributed actor capabilities")
	fmt.Println("- Production-ready performance and reliability")
}