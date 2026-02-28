package testing

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/agent"
)

func TestSimulatedAgent_Basic(t *testing.T) {
	sa := NewSimulatedAgent("test-agent", 5*time.Millisecond, 10*time.Millisecond, 0.0)

	input := &agent.AgentInput{
		Instruction: "test",
		TaskID:      "test-task",
	}

	output, err := sa.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Expected success, got error: %v", err)
	}

	if output == nil {
		t.Fatal("Expected output, got nil")
	}

	calls, successes, errors := sa.Stats()
	if calls != 1 {
		t.Errorf("Expected 1 call, got %d", calls)
	}
	if successes != 1 {
		t.Errorf("Expected 1 success, got %d", successes)
	}
	if errors != 0 {
		t.Errorf("Expected 0 errors, got %d", errors)
	}
}

func TestSimulatedAgent_WithErrors(t *testing.T) {
	sa := NewSimulatedAgent("test-agent", 1*time.Millisecond, 2*time.Millisecond, 1.0) // 100% error rate

	input := &agent.AgentInput{
		Instruction: "test",
		TaskID:      "test-task",
	}

	_, err := sa.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("Expected error, got success")
	}

	calls, successes, errors := sa.Stats()
	if calls != 1 {
		t.Errorf("Expected 1 call, got %d", calls)
	}
	if successes != 0 {
		t.Errorf("Expected 0 successes, got %d", successes)
	}
	if errors != 1 {
		t.Errorf("Expected 1 error, got %d", errors)
	}
}

func TestSimulatedAgent_ContextCancellation(t *testing.T) {
	sa := NewSimulatedAgent("test-agent", 1*time.Second, 2*time.Second, 0.0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	input := &agent.AgentInput{
		Instruction: "test",
		TaskID:      "test-task",
	}

	_, err := sa.Execute(ctx, input)
	if err == nil {
		t.Fatal("Expected context error, got success")
	}

	if err != context.DeadlineExceeded {
		t.Errorf("Expected DeadlineExceeded, got %v", err)
	}
}

func TestSimulatedAgent_SetErrorRate(t *testing.T) {
	sa := NewSimulatedAgent("test-agent", 1*time.Millisecond, 2*time.Millisecond, 0.0)

	// Initially should succeed
	input := &agent.AgentInput{Instruction: "test", TaskID: "1"}
	_, err := sa.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Expected success initially, got: %v", err)
	}

	// Set 100% error rate
	sa.SetErrorRate(1.0)

	// Should fail now
	input.TaskID = "2"
	_, err = sa.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("Expected error after SetErrorRate(1.0)")
	}
}

func TestSimulatedAgent_SetLatencyRange(t *testing.T) {
	sa := NewSimulatedAgent("test-agent", 1*time.Millisecond, 2*time.Millisecond, 0.0)

	input := &agent.AgentInput{Instruction: "test", TaskID: "1"}

	// Measure initial latency
	start := time.Now()
	_, _ = sa.Execute(context.Background(), input)
	initialLatency := time.Since(start)

	// Set longer latency
	sa.SetLatencyRange(100*time.Millisecond, 150*time.Millisecond)

	// Measure new latency
	start = time.Now()
	_, _ = sa.Execute(context.Background(), input)
	newLatency := time.Since(start)

	// New latency should be significantly longer
	if newLatency < 50*time.Millisecond {
		t.Errorf("Expected latency >= 100ms after SetLatencyRange, got %v", newLatency)
	}

	if initialLatency > 50*time.Millisecond {
		t.Errorf("Initial latency should be < 50ms, got %v", initialLatency)
	}
}

func TestSimulatedAgent_ResetStats(t *testing.T) {
	sa := NewSimulatedAgent("test-agent", 1*time.Millisecond, 2*time.Millisecond, 0.0)

	input := &agent.AgentInput{Instruction: "test", TaskID: "1"}

	// Make some calls
	for i := 0; i < 5; i++ {
		_, _ = sa.Execute(context.Background(), input)
	}

	calls, _, _ := sa.Stats()
	if calls != 5 {
		t.Errorf("Expected 5 calls, got %d", calls)
	}

	// Reset
	sa.ResetStats()

	calls, successes, errors := sa.Stats()
	if calls != 0 || successes != 0 || errors != 0 {
		t.Errorf("Expected all zeros after reset, got calls=%d, successes=%d, errors=%d", calls, successes, errors)
	}
}

func TestSimulatedAgent_ConcurrentAccess(t *testing.T) {
	// This test verifies the race condition fix
	sa := NewSimulatedAgent("test-agent", 1*time.Millisecond, 5*time.Millisecond, 0.0)

	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Concurrent writers (modify error rate and latency)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					sa.SetErrorRate(float64(id) * 0.1)
					sa.SetLatencyRange(time.Duration(id)*time.Millisecond, time.Duration(id+5)*time.Millisecond)
					time.Sleep(time.Millisecond)
				}
			}
		}(i)
	}

	// Concurrent readers (execute)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			input := &agent.AgentInput{Instruction: "test", TaskID: "concurrent"}
			for {
				select {
				case <-ctx.Done():
					return
				default:
					_, _ = sa.Execute(ctx, input)
				}
			}
		}()
	}

	wg.Wait()

	// If we got here without a race detector panic, the test passed
	calls, _, _ := sa.Stats()
	if calls == 0 {
		t.Error("Expected some calls to complete")
	}
}

func TestSimulatedAgent_Name(t *testing.T) {
	sa := NewSimulatedAgent("my-test-agent", 1*time.Millisecond, 2*time.Millisecond, 0.0)

	if sa.Name() != "my-test-agent" {
		t.Errorf("Expected name 'my-test-agent', got '%s'", sa.Name())
	}
}
