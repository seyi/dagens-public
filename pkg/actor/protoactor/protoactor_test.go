package protoactor

import (
	"testing"
	"time"
)

func TestProtoActorSystem(t *testing.T) {
	// Create a new actor system
	system := NewSystem()
	defer func() {
		// In a real test, we might need to properly shut down actors
	}()

	// Create and spawn an echo actor
	echoActor := NewEchoActor("test-echo")
	echoPid, err := system.Spawn(echoActor, "echo-actor")
	if err != nil {
		t.Fatalf("Failed to spawn echo actor: %v", err)
	}

	// Test sending a message to the actor
	invokeMsg := InvokeMessage{
		AgentID:     "echo-actor",
		Instruction: "Hello, ProtoActor-Go!",
		TaskID:      "task-1",
		Timeout:     5 * time.Second,
	}

	response, err := system.RequestRaw(echoPid, &invokeMsg, 5*time.Second)
	if err != nil {
		t.Fatalf("Error sending message to echo actor: %v", err)
	}

	// Check the response
	respMsg, ok := response.(*ResponseMessage)
	if !ok {
		t.Fatalf("Expected ResponseMessage, got %T", response)
	}

	expected := "Echo from test-echo: Hello, ProtoActor-Go!"
	if result, ok := respMsg.Result.(string); !ok || result != expected {
		t.Errorf("Expected result '%s', got '%s'", expected, respMsg.Result)
	}

	t.Logf("ProtoActor system test passed: %v", respMsg.Result)
}

func TestSupervisor(t *testing.T) {
	// Create a new actor system
	system := NewSystem()
	defer func() {
		// In a real test, we might need to properly shut down actors
	}()

	// Create a supervisor
	supervisor := NewSupervisor(OneForOne)

	// Create a child actor config
	calcConfig := ActorConfig{
		ID:       "supervised-calc",
		Actor:    NewCalculatorActor("supervised"),
		Strategy: OneForOne,
	}

	supervisedPid, err := supervisor.AddChild(system, calcConfig)
	if err != nil {
		t.Fatalf("Error adding supervised child: %v", err)
	}

	// Verify child was added
	if supervisor.ChildCount() != 1 {
		t.Errorf("Expected 1 child, got %d", supervisor.ChildCount())
	}

	// Test the supervised actor
	supervisedMsg := InvokeMessage{
		AgentID:     "supervised-calc",
		Instruction: "add 2 3",
		TaskID:      "task-2",
		Timeout:     5 * time.Second,
	}

	response, err := system.RequestRaw(supervisedPid, &supervisedMsg, 5*time.Second)
	if err != nil {
		t.Fatalf("Error sending message to supervised actor: %v", err)
	}

	// Check the response
	respMsg, ok := response.(*ResponseMessage)
	if !ok {
		t.Fatalf("Expected ResponseMessage, got %T", response)
	}

	expected := 5
	if result, ok := respMsg.Result.(int); !ok || result != expected {
		t.Errorf("Expected result %d, got %d", expected, respMsg.Result)
	}

	t.Logf("Supervisor test passed: supervisor managing %d child, result: %v", supervisor.ChildCount(), respMsg.Result)
}

func TestCalculatorActor(t *testing.T) {
	// Create a new actor system
	system := NewSystem()
	defer func() {
		// In a real test, we might need to properly shut down actors
	}()

	// Create and spawn a calculator actor
	calcActor := NewCalculatorActor("test-calc")
	calcPid, err := system.Spawn(calcActor, "calc-actor")
	if err != nil {
		t.Fatalf("Failed to spawn calculator actor: %v", err)
	}

	// Test various calculations
	testCases := []struct {
		instruction string
		expected    interface{}
	}{
		{"add 2 3", 5},
		{"multiply 4 5", 20},
		{"subtract 10 3", 7},
	}

	for _, tc := range testCases {
		calcMsg := InvokeMessage{
			AgentID:     "calc-actor",
			Instruction: tc.instruction,
			TaskID:      "task-test",
			Timeout:     5 * time.Second,
		}

		response, err := system.RequestRaw(calcPid, &calcMsg, 5*time.Second)
		if err != nil {
			t.Fatalf("Error sending message to calculator actor: %v", err)
		}

		respMsg, ok := response.(*ResponseMessage)
		if !ok {
			t.Fatalf("Expected ResponseMessage for '%s', got %T", tc.instruction, response)
		}

		if respMsg.Result != tc.expected {
			t.Errorf("For instruction '%s', expected %v, got %v", tc.instruction, tc.expected, respMsg.Result)
		}
	}

	t.Log("Calculator actor test passed")
}