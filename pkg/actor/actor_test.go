package actor

import (
	"fmt"
	"testing"
	"time"
)

// EchoActor is a simple test actor that echoes messages back
type EchoActor struct {
	name string
}

func (e *EchoActor) Receive(ctx Context) {
	switch ctx.Message.Type {
	case MessageTypeInvoke:
		if msg, ok := ctx.Message.Payload.(InvokeMessage); ok {
			response := Message{
				Type: MessageTypeResponse,
				Payload: ResponseMessage{
					TaskID: msg.TaskID,
					Result: fmt.Sprintf("Echo: %s", msg.Instruction),
				},
			}
			
			// Send response back if there's a reply channel
			if ctx.Message.ReplyTo != nil {
				ctx.Message.ReplyTo <- response
			}
		}
	case MessageTypePing:
		if ctx.Message.ReplyTo != nil {
			response := Message{
				Type:    MessageTypePong,
				Payload: PongMessage{ID: "test", Timestamp: time.Now()},
			}
			ctx.Message.ReplyTo <- response
		}
	}
}

func TestActorSystem(t *testing.T) {
	// Create a new actor system
	system := NewSystem()
	defer system.Stop()

	// Create and spawn an echo actor
	echoActor := &EchoActor{name: "test-echo"}
	address, err := system.Spawn(echoActor, "echo-1")
	if err != nil {
		t.Fatalf("Failed to spawn actor: %v", err)
	}

	// Test sending a message
	msg := Message{
		Type: MessageTypeInvoke,
		Payload: InvokeMessage{
			Instruction: "Hello, Actor!",
			TaskID:      "test-1",
		},
	}

	// Send and wait for response
	response, err := system.Request(address, msg, 1*time.Second)
	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}

	if response.Type != MessageTypeResponse {
		t.Errorf("Expected response type %s, got %s", MessageTypeResponse, response.Type)
	}

	respMsg, ok := response.Payload.(ResponseMessage)
	if !ok {
		t.Fatalf("Expected ResponseMessage, got %T", response.Payload)
	}

	expected := "Echo: Hello, Actor!"
	if respMsg.Result != expected {
		t.Errorf("Expected result '%s', got '%s'", expected, respMsg.Result)
	}

	t.Logf("Actor system test passed: %v", respMsg.Result)
}

func TestSupervisor(t *testing.T) {
	// Create a new actor system
	system := NewSystem()
	defer system.Stop()

	// Create a supervisor
	supervisor := NewSupervisor(OneForOne, 3, 5*time.Second)
	supervisorAddr, err := system.Spawn(supervisor, "supervisor-1")
	if err != nil {
		t.Fatalf("Failed to spawn supervisor: %v", err)
	}

	// Create a child actor config
	childConfig := ActorConfig{
		ID:    "child-1",
		Actor: &EchoActor{name: "child"},
	}
	
	// Add child to supervisor
	_, err = supervisor.AddChild(childConfig)
	if err != nil {
		t.Fatalf("Failed to add child to supervisor: %v", err)
	}

	// Verify child was added
	if supervisor.ChildCount() != 1 {
		t.Errorf("Expected 1 child, got %d", supervisor.ChildCount())
	}

	t.Logf("Supervisor test passed: supervisor %s has %d child", supervisorAddr, supervisor.ChildCount())
}

func TestActorSystemConcurrent(t *testing.T) {
	// Create a new actor system
	system := NewSystem()
	defer system.Stop()

	// Spawn multiple actors
	actorCount := 10
	for i := 0; i < actorCount; i++ {
		actor := &EchoActor{name: fmt.Sprintf("echo-%d", i)}
		_, err := system.Spawn(actor, fmt.Sprintf("echo-%d", i))
		if err != nil {
			t.Fatalf("Failed to spawn actor %d: %v", i, err)
		}
	}

	// Verify all actors were created
	if system.ActorCount() != actorCount {
		t.Errorf("Expected %d actors, got %d", actorCount, system.ActorCount())
	}

	// Test concurrent messaging to different actors
	for i := 0; i < actorCount; i++ {
		go func(idx int) {
			address := Address(fmt.Sprintf("echo-%d", idx))
			msg := Message{
				Type: MessageTypeInvoke,
				Payload: InvokeMessage{
					Instruction: fmt.Sprintf("Message-%d", idx),
					TaskID:      fmt.Sprintf("task-%d", idx),
				},
			}

			// Don't check response in goroutine to avoid race in test
			// Just send the message
			err := system.Send(address, msg)
			if err != nil {
				t.Errorf("Failed to send message to actor %d: %v", idx, err)
			}
		}(i)
	}

	// Give some time for messages to be processed
	time.Sleep(100 * time.Millisecond)

	t.Logf("Concurrent actor test passed: %d actors created", system.ActorCount())
}