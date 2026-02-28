package loadtest

import (
	"context"
	"testing"
	"time"
)

func TestMockAgent(t *testing.T) {
	agent := NewMockAgent("test", 10*time.Millisecond, 0.0)

	input := &SimpleRequestGenerator{}
	req, _ := input.Generate()

	output, err := agent.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Expected success, got error: %v", err)
	}

	if output == nil {
		t.Fatal("Expected output, got nil")
	}

	calls, successes, errors := agent.Stats()
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

func TestMockAgent_WithErrors(t *testing.T) {
	agent := NewMockAgent("test", 1*time.Millisecond, 1.0) // 100% error rate

	input := &SimpleRequestGenerator{}
	req, _ := input.Generate()

	_, err := agent.Execute(context.Background(), req)
	if err == nil {
		t.Fatal("Expected error, got success")
	}

	calls, successes, errors := agent.Stats()
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

func TestMockAgent_ContextCancellation(t *testing.T) {
	agent := NewMockAgent("test", 1*time.Second, 0.0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	input := &SimpleRequestGenerator{}
	req, _ := input.Generate()

	_, err := agent.Execute(ctx, req)
	if err == nil {
		t.Fatal("Expected context error, got success")
	}

	if err != context.DeadlineExceeded {
		t.Errorf("Expected DeadlineExceeded, got %v", err)
	}
}

func TestSimpleRequestGenerator(t *testing.T) {
	gen := NewSimpleRequestGenerator([]string{"instruction 1", "instruction 2"})

	req1, err := gen.Generate()
	if err != nil {
		t.Fatalf("Failed to generate request: %v", err)
	}

	if req1.TaskID == "" {
		t.Error("Expected non-empty task ID")
	}

	if req1.Instruction != "instruction 2" { // counter starts at 1, mod 2 = 1
		t.Errorf("Expected 'instruction 2', got %s", req1.Instruction)
	}

	req2, _ := gen.Generate()
	if req2.TaskID == req1.TaskID {
		t.Error("Expected unique task IDs")
	}
}

func TestSimpleRequestGenerator_DefaultInstructions(t *testing.T) {
	gen := NewSimpleRequestGenerator(nil)

	req, err := gen.Generate()
	if err != nil {
		t.Fatalf("Failed to generate request: %v", err)
	}

	if req.Instruction == "" {
		t.Error("Expected non-empty instruction")
	}
}

func TestLoadTestConfig_Defaults(t *testing.T) {
	config := DefaultLoadTestConfig()

	if config.Duration != 60*time.Second {
		t.Errorf("Expected 60s duration, got %v", config.Duration)
	}

	if config.TargetRPS != 100 {
		t.Errorf("Expected 100 RPS, got %f", config.TargetRPS)
	}

	if config.MaxConcurrency != 50 {
		t.Errorf("Expected 50 max concurrency, got %d", config.MaxConcurrency)
	}
}

func TestLoadTestHarness_ShortRun(t *testing.T) {
	agent := NewMockAgent("test", 5*time.Millisecond, 0.0)
	config := LoadTestConfig{
		Name:           "test-run",
		Duration:       500 * time.Millisecond,
		TargetRPS:      20,
		MaxConcurrency: 10,
		RequestTimeout: 1 * time.Second,
		Verbose:        false,
	}

	harness := NewLoadTestHarness(config, agent, nil)

	result, err := harness.Run(context.Background())
	if err != nil {
		t.Fatalf("Load test failed: %v", err)
	}

	if result.TotalRequests == 0 {
		t.Error("Expected some requests to be made")
	}

	if result.SuccessfulRequests == 0 {
		t.Error("Expected some successful requests")
	}

	if result.ActualRPS == 0 {
		t.Error("Expected non-zero RPS")
	}
}

func TestLoadTestHarness_WithErrors(t *testing.T) {
	agent := NewMockAgent("test", 5*time.Millisecond, 0.5) // 50% error rate
	config := LoadTestConfig{
		Name:           "test-errors",
		Duration:       500 * time.Millisecond,
		TargetRPS:      20,
		MaxConcurrency: 10,
		RequestTimeout: 1 * time.Second,
	}

	harness := NewLoadTestHarness(config, agent, nil)

	result, err := harness.Run(context.Background())
	if err != nil {
		t.Fatalf("Load test failed: %v", err)
	}

	// With 50% error rate, we should have both successes and failures
	if result.SuccessfulRequests == 0 {
		t.Error("Expected some successful requests")
	}

	if result.FailedRequests == 0 {
		t.Error("Expected some failed requests")
	}
}

func TestLoadTestHarness_Stop(t *testing.T) {
	agent := NewMockAgent("test", 10*time.Millisecond, 0.0)
	config := LoadTestConfig{
		Name:           "test-stop",
		Duration:       10 * time.Second, // Long duration
		TargetRPS:      10,
		MaxConcurrency: 5,
		RequestTimeout: 1 * time.Second,
	}

	harness := NewLoadTestHarness(config, agent, nil)

	// Run in background
	done := make(chan *LoadTestResult)
	go func() {
		result, _ := harness.Run(context.Background())
		done <- result
	}()

	// Stop after short delay
	time.Sleep(100 * time.Millisecond)
	harness.Stop()

	select {
	case result := <-done:
		// Test stopped successfully
		if result.TotalRequests == 0 {
			t.Error("Expected some requests before stop")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Test did not stop in time")
	}
}

func TestLoadTestResult_Percentiles(t *testing.T) {
	result := &LoadTestResult{
		MinLatency: 10,
		MaxLatency: 100,
		AvgLatency: 50,
		P50Latency: 45,
		P90Latency: 80,
		P95Latency: 90,
		P99Latency: 99,
	}

	// Just verify the values are set correctly
	if result.P50Latency > result.P90Latency {
		t.Error("P50 should be less than P90")
	}

	if result.P90Latency > result.P95Latency {
		t.Error("P90 should be less than P95")
	}

	if result.P95Latency > result.P99Latency {
		t.Error("P95 should be less than P99")
	}
}

func TestLoadTestResult_PrintSummary(t *testing.T) {
	result := &LoadTestResult{
		Config: LoadTestConfig{
			Name:      "test",
			TargetRPS: 100,
		},
		Duration:           60 * time.Second,
		TotalRequests:      5000,
		SuccessfulRequests: 4950,
		FailedRequests:     40,
		TimeoutRequests:    10,
		ActualRPS:          83.3,
		MinLatency:         5,
		MaxLatency:         500,
		AvgLatency:         25,
		P50Latency:         20,
		P90Latency:         50,
		P95Latency:         100,
		P99Latency:         300,
		ErrorCounts:        map[string]int64{"execution_error": 40},
	}

	// Just verify it doesn't panic
	result.PrintSummary()
}

func TestAverage(t *testing.T) {
	tests := []struct {
		values   []float64
		expected float64
	}{
		{[]float64{}, 0},
		{[]float64{10}, 10},
		{[]float64{10, 20, 30}, 20},
		{[]float64{1, 2, 3, 4, 5}, 3},
	}

	for _, tt := range tests {
		result := average(tt.values)
		if result != tt.expected {
			t.Errorf("average(%v) = %f, expected %f", tt.values, result, tt.expected)
		}
	}
}

func TestPercentile(t *testing.T) {
	sorted := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	// With sorted = [1,2,3,4,5,6,7,8,9,10], index calculation is:
	// idx = len(sorted) * p / 100, then cap at len-1
	tests := []struct {
		p        int
		expected float64
	}{
		{0, 1},   // idx = 0, sorted[0] = 1
		{50, 6},  // idx = 5, sorted[5] = 6
		{90, 10}, // idx = 9, sorted[9] = 10
		{99, 10}, // idx = 9, sorted[9] = 10
		{100, 10}, // idx = 10 capped to 9, sorted[9] = 10
	}

	for _, tt := range tests {
		result := percentile(sorted, tt.p)
		if result != tt.expected {
			t.Errorf("percentile(sorted, %d) = %f, expected %f", tt.p, result, tt.expected)
		}
	}
}
