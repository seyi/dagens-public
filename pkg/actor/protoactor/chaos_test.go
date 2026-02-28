//go:build chaos
// +build chaos

package protoactor

import (
	"fmt"
	"testing"
	"time"

	"github.com/asynkron/protoactor-go/actor"
)

// ============================================================================
// Helper Actors for Chaos Tests
// ============================================================================

// CrashableActor is an actor that panics when it receives a "crash" message.
// Used for testing supervisor restart strategies.
type CrashableActor struct{}

// NewCrashableActor creates a new instance of CrashableActor.
func NewCrashableActor() *CrashableActor {
	return &CrashableActor{}
}

// Receive handles incoming messages. It responds to "ping" and panics on "crash".
func (a *CrashableActor) Receive(ctx Context) {
	switch msg := ctx.Message.Payload.(type) {
	case *InvokeMessage:
		switch msg.Instruction {
		case "ping":
			ctx.Context.Respond("pong")
		case "crash":
			panic("simulating actor process crash")
		default:
			ctx.Context.Respond(fmt.Errorf("unknown instruction: %s", msg.Instruction))
		}
	}
}

// StatefulCrashActor is an actor that maintains a simple counter state.
// It can be instructed to panic, allowing tests to verify state reset on restart.
type StatefulCrashActor struct {
	count int
}

// NewStatefulCrashActor creates a new instance with its counter initialized to 0.
func NewStatefulCrashActor() *StatefulCrashActor {
	return &StatefulCrashActor{count: 0}
}

// Receive handles messages to increment, get, or corrupt (panic) its state.
func (a *StatefulCrashActor) Receive(ctx Context) {
	switch msg := ctx.Message.Payload.(type) {
	case *actor.Started:
		// Reset state on start/restart - this is the proper way to handle restarts
		a.count = 0
	case *InvokeMessage:
		switch msg.Instruction {
		case "increment":
			a.count++
			ctx.Context.Respond(a.count)
		case "get":
			ctx.Context.Respond(a.count)
		case "corrupt":
			panic("simulating state corruption and crash")
		default:
			ctx.Context.Respond(fmt.Errorf("unknown instruction"))
		}
	}
}

// ParentSupervisorActor is an actor that also acts as a supervisor for a child actor.
// It is designed to be crashed to test hierarchical supervision recovery.
type ParentSupervisorActor struct {
	system       *System
	supervisor   *Supervisor
	childPID     Address
	childName    string
	restartCount int
}

// NewParentSupervisorActor creates a new parent actor that can supervise a child.
func NewParentSupervisorActor(system *System, childName string) *ParentSupervisorActor {
	return &ParentSupervisorActor{
		system:       system,
		childName:    childName,
		restartCount: 0,
	}
}

// Receive handles lifecycle messages and commands. On start, it creates a child actor.
func (a *ParentSupervisorActor) Receive(ctx Context) {
	switch msg := ctx.Message.Payload.(type) {
	case *actor.Started:
		// On start or restart, create the supervisor and spawn the child actor.
		// Use unique name with restart count to avoid name collision
		a.restartCount++
		a.supervisor = NewSupervisor(OneForOne)
		childID := fmt.Sprintf("%s-%d", a.childName, a.restartCount)
		childConfig := ActorConfig{
			ID:    childID,
			Actor: NewCrashableActor(),
		}
		pid, err := a.supervisor.AddChild(a.system, childConfig)
		if err != nil {
			panic(fmt.Sprintf("parent actor failed to create child: %v", err))
		}
		a.childPID = pid
	case *InvokeMessage:
		switch msg.Instruction {
		case "crash":
			panic("simulating parent supervisor crash")
		case "get_child_pid":
			if a.childPID != nil {
				ctx.Context.Respond(a.childPID)
			} else {
				ctx.Context.Respond(fmt.Errorf("child actor not yet available"))
			}
		}
	}
}

// PartitionableActor is an actor that can be made unresponsive to simulate network partitions.
// Uses a boolean flag instead of channel to avoid deadlock.
type PartitionableActor struct {
	isPartitioned bool
}

// NewPartitionableActor creates a new responsive actor.
func NewPartitionableActor() *PartitionableActor {
	return &PartitionableActor{
		isPartitioned: false,
	}
}

// Receive handles messages. "ping" will not respond if the actor is "partitioned".
func (a *PartitionableActor) Receive(ctx Context) {
	switch msg := ctx.Message.Payload.(type) {
	case *InvokeMessage:
		switch msg.Instruction {
		case "ping":
			if a.isPartitioned {
				// Simulate partition: do not respond, causing timeout
				return
			}
			ctx.Context.Respond("pong")
		case "partition":
			a.isPartitioned = true
			ctx.Context.Respond("ok")
		case "heal":
			a.isPartitioned = false
			ctx.Context.Respond("ok")
		}
	}
}

// ============================================================================
// Chaos Test Functions
// ============================================================================

// TestResilience_ActorProcessCrash verifies that a supervisor correctly restarts a crashed child actor.
func TestResilience_ActorProcessCrash(t *testing.T) {
	system := NewSystem()
	defer system.Stop()

	supervisor := NewSupervisor(OneForOne)

	crashableConfig := ActorConfig{
		ID:    "crashable-actor",
		Actor: NewCrashableActor(),
	}
	pid, err := supervisor.AddChild(system, crashableConfig)
	if err != nil {
		t.Fatalf("Failed to spawn crashable actor: %v", err)
	}

	// 1. Verify it's alive initially.
	pingMsg := &InvokeMessage{Instruction: "ping"}
	response, err := system.RequestRaw(pid, pingMsg, 1*time.Second)
	if err != nil {
		t.Fatalf("Initial ping failed: %v", err)
	}
	if resp, ok := response.(string); !ok || resp != "pong" {
		t.Fatalf("Expected 'pong', got '%v'", response)
	}
	t.Log("Actor is alive initially.")

	// 2. Crash the actor by sending a panic-inducing message.
	crashMsg := &InvokeMessage{Instruction: "crash"}
	system.SendRaw(pid, crashMsg)
	t.Log("Crash message sent.")

	// 3. Poll until the actor is responsive again, confirming a restart.
	var finalResponse interface{}
	restarted := false
	for i := 0; i < 10; i++ {
		time.Sleep(50 * time.Millisecond)
		finalResponse, err = system.RequestRaw(pid, pingMsg, 100*time.Millisecond)
		if err == nil {
			restarted = true
			break
		}
	}

	if !restarted {
		t.Fatalf("Actor did not restart in time. Last error: %v", err)
	}
	if resp, ok := finalResponse.(string); !ok || resp != "pong" {
		t.Fatalf("Expected 'pong' after restart, got '%v'", finalResponse)
	}
	t.Log("Actor recovered successfully after crash.")
}

// TestResilience_SupervisorFailure verifies that a grandparent supervisor can restart a
// child supervisor, which in turn successfully recovers its own children.
func TestResilience_SupervisorFailure(t *testing.T) {
	system := NewSystem()
	defer system.Stop()

	grandparent := NewSupervisor(OneForOne)

	// 1. Spawn the parent supervisor actor under the grandparent.
	parentActor := NewParentSupervisorActor(system, "child-actor")
	parentConfig := ActorConfig{
		ID:    "parent-supervisor",
		Actor: parentActor,
	}
	parentPID, err := grandparent.AddChild(system, parentConfig)
	if err != nil {
		t.Fatalf("Failed to spawn parent supervisor: %v", err)
	}

	// 2. Get the child's PID from the parent and verify it's working.
	var childPID Address
	for i := 0; i < 10; i++ {
		res, _ := system.RequestRaw(parentPID, &InvokeMessage{Instruction: "get_child_pid"}, 100*time.Millisecond)
		if pid, ok := res.(Address); ok && pid != nil {
			childPID = pid
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if childPID == nil {
		t.Fatal("Failed to get child PID from parent")
	}

	// 3. Verify the original child is responsive.
	pingMsg := &InvokeMessage{Instruction: "ping"}
	res, err := system.RequestRaw(childPID, pingMsg, 1*time.Second)
	if err != nil {
		t.Fatalf("Child actor not responsive initially: %v", err)
	}
	if resp, ok := res.(string); !ok || resp != "pong" {
		t.Fatalf("Unexpected response from child: %v", res)
	}
	t.Log("Child is responsive before parent crash.")

	// 4. Crash the parent supervisor.
	crashMsg := &InvokeMessage{Instruction: "crash"}
	system.SendRaw(parentPID, crashMsg)
	t.Log("Crash message sent to parent supervisor.")

	// 5. Verify parent and child recover by getting the new child's PID.
	var newChildPID Address
	for i := 0; i < 20; i++ {
		res, _ := system.RequestRaw(parentPID, &InvokeMessage{Instruction: "get_child_pid"}, 100*time.Millisecond)
		if pid, ok := res.(Address); ok && pid != nil {
			newChildPID = pid
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if newChildPID == nil {
		t.Fatal("Parent did not restart and provide a new child PID")
	}

	// 6. Verify the new child is responsive.
	res, err = system.RequestRaw(newChildPID, pingMsg, 1*time.Second)
	if err != nil {
		t.Fatalf("New child actor not responsive: %v", err)
	}
	if resp, ok := res.(string); !ok || resp != "pong" {
		t.Fatalf("Unexpected response from new child: %v", res)
	}
	t.Log("Parent and child recovered successfully.")
}

// TestRecovery_ActorStateCorruption verifies that a crashed actor's state is reset upon restart.
func TestRecovery_ActorStateCorruption(t *testing.T) {
	system := NewSystem()
	defer system.Stop()

	supervisor := NewSupervisor(OneForOne)

	statefulConfig := ActorConfig{
		ID:    "stateful-actor",
		Actor: NewStatefulCrashActor(),
	}
	pid, err := supervisor.AddChild(system, statefulConfig)
	if err != nil {
		t.Fatalf("Failed to spawn stateful actor: %v", err)
	}

	// 1. Increment state to 2 and verify.
	incMsg := &InvokeMessage{Instruction: "increment"}
	getMsg := &InvokeMessage{Instruction: "get"}

	_, err = system.RequestRaw(pid, incMsg, 1*time.Second)
	if err != nil {
		t.Fatalf("Failed to increment actor state (first): %v", err)
	}

	res, err := system.RequestRaw(pid, incMsg, 1*time.Second)
	if err != nil {
		t.Fatalf("Failed to increment actor state (second): %v", err)
	}
	if resp, ok := res.(int); !ok || resp != 2 {
		t.Fatalf("Expected state to be 2, got %v", res)
	}
	t.Log("Actor state successfully incremented to 2.")

	// 2. Corrupt/crash the actor.
	corruptMsg := &InvokeMessage{Instruction: "corrupt"}
	system.SendRaw(pid, corruptMsg)
	t.Log("Corruption message sent.")

	// 3. Poll until actor restarts and verify its state has been reset to 0.
	restarted := false
	var stateAfterRestart interface{}
	for i := 0; i < 10; i++ {
		time.Sleep(50 * time.Millisecond)
		stateAfterRestart, err = system.RequestRaw(pid, getMsg, 100*time.Millisecond)
		if err == nil {
			restarted = true
			break
		}
	}

	if !restarted {
		t.Fatalf("Actor did not restart in time. Last error: %v", err)
	}

	// Verify state was reset to 0
	if count, ok := stateAfterRestart.(int); !ok || count != 0 {
		t.Fatalf("Expected state to be 0 after restart, got %v", stateAfterRestart)
	}
	t.Log("Actor recovered and state was successfully reset to 0.")
}

// TestResilience_SimulatedNetworkPartition verifies actor system behavior during timeouts and recovery.
func TestResilience_SimulatedNetworkPartition(t *testing.T) {
	system := NewSystem()
	defer system.Stop()

	supervisor := NewSupervisor(OneForOne)

	partitionableConfig := ActorConfig{
		ID:    "partitionable-actor",
		Actor: NewPartitionableActor(),
	}
	pid, err := supervisor.AddChild(system, partitionableConfig)
	if err != nil {
		t.Fatalf("Failed to spawn partitionable actor: %v", err)
	}

	pingMsg := &InvokeMessage{Instruction: "ping"}

	// 1. Verify it's responsive initially.
	response, err := system.RequestRaw(pid, pingMsg, 1*time.Second)
	if err != nil {
		t.Fatalf("Initial ping failed: %v", err)
	}
	if resp, ok := response.(string); !ok || resp != "pong" {
		t.Fatalf("Expected 'pong', got '%v'", response)
	}
	t.Log("Actor is responsive initially.")

	// 2. "Partition" the actor to make it unresponsive.
	partitionMsg := &InvokeMessage{Instruction: "partition"}
	_, err = system.RequestRaw(pid, partitionMsg, 1*time.Second)
	if err != nil {
		t.Fatalf("Failed to send partition message: %v", err)
	}
	t.Log("Actor is now partitioned.")

	// 3. Verify that a subsequent request times out.
	_, err = system.RequestRaw(pid, pingMsg, 100*time.Millisecond)
	if err == nil {
		t.Fatal("Expected request to time out, but it succeeded.")
	}
	t.Logf("Correctly received timeout error as expected: %v", err)

	// 4. "Heal" the partition.
	healMsg := &InvokeMessage{Instruction: "heal"}
	_, err = system.RequestRaw(pid, healMsg, 1*time.Second)
	if err != nil {
		t.Fatalf("Failed to send heal message: %v", err)
	}
	t.Log("Actor is now healed.")

	// 5. Verify it's responsive again.
	response, err = system.RequestRaw(pid, pingMsg, 1*time.Second)
	if err != nil {
		t.Fatalf("Actor should be responsive after healing, but got error: %v", err)
	}
	if resp, ok := response.(string); !ok || resp != "pong" {
		t.Fatalf("Expected 'pong' after healing, got %v", response)
	}
	t.Log("Actor is responsive again after partition is healed.")
}
