// Package loadtest provides a load testing harness for AI agents.
// Use this to stress test agent execution, identify bottlenecks, and validate resilience.
package loadtest

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/observability"
)

// LoadTestConfig configures a load test run
type LoadTestConfig struct {
	// Name of this load test
	Name string

	// Duration of the test
	Duration time.Duration

	// Target requests per second
	TargetRPS float64

	// Maximum concurrent requests
	MaxConcurrency int

	// Ramp-up duration (0 for immediate full load)
	RampUpDuration time.Duration

	// Request timeout
	RequestTimeout time.Duration

	// Enable verbose logging
	Verbose bool

	// Output file for results (empty for stdout)
	OutputFile string
}

// DefaultLoadTestConfig returns sensible defaults
func DefaultLoadTestConfig() LoadTestConfig {
	return LoadTestConfig{
		Name:           "default",
		Duration:       60 * time.Second,
		TargetRPS:      100,
		MaxConcurrency: 50,
		RampUpDuration: 10 * time.Second,
		RequestTimeout: 30 * time.Second,
		Verbose:        false,
	}
}

// LoadTestResult holds the results of a load test
type LoadTestResult struct {
	// Configuration
	Config LoadTestConfig `json:"config"`

	// Timing
	StartTime time.Time     `json:"start_time"`
	EndTime   time.Time     `json:"end_time"`
	Duration  time.Duration `json:"duration_ms"`

	// Request counts
	TotalRequests      int64 `json:"total_requests"`
	SuccessfulRequests int64 `json:"successful_requests"`
	FailedRequests     int64 `json:"failed_requests"`
	TimeoutRequests    int64 `json:"timeout_requests"`

	// Latency statistics (in milliseconds)
	MinLatency float64 `json:"min_latency_ms"`
	MaxLatency float64 `json:"max_latency_ms"`
	AvgLatency float64 `json:"avg_latency_ms"`
	P50Latency float64 `json:"p50_latency_ms"`
	P90Latency float64 `json:"p90_latency_ms"`
	P95Latency float64 `json:"p95_latency_ms"`
	P99Latency float64 `json:"p99_latency_ms"`

	// Throughput
	ActualRPS float64 `json:"actual_rps"`

	// Error breakdown
	ErrorCounts map[string]int64 `json:"error_counts"`

	// Per-second data (for graphing)
	PerSecondData []PerSecondMetrics `json:"per_second_data,omitempty"`
}

// PerSecondMetrics holds metrics for a single second
type PerSecondMetrics struct {
	Second      int     `json:"second"`
	Requests    int64   `json:"requests"`
	Successes   int64   `json:"successes"`
	Failures    int64   `json:"failures"`
	AvgLatency  float64 `json:"avg_latency_ms"`
	Concurrency int     `json:"concurrency"`
}

// RequestGenerator generates test requests
type RequestGenerator interface {
	// Generate creates a new test request
	Generate() (*agent.AgentInput, error)
}

// SimpleRequestGenerator generates basic test requests
type SimpleRequestGenerator struct {
	Instructions []string
	Context      map[string]interface{}
	counter      int64
}

// NewSimpleRequestGenerator creates a simple request generator
func NewSimpleRequestGenerator(instructions []string) *SimpleRequestGenerator {
	if len(instructions) == 0 {
		instructions = []string{
			"Analyze this data and provide insights",
			"Summarize the following information",
			"Generate a report based on the input",
			"Process and transform the provided data",
		}
	}
	return &SimpleRequestGenerator{
		Instructions: instructions,
		Context:      make(map[string]interface{}),
	}
}

// Generate creates a new test request
func (g *SimpleRequestGenerator) Generate() (*agent.AgentInput, error) {
	count := atomic.AddInt64(&g.counter, 1)

	// Pick instruction round-robin
	var instruction string
	if len(g.Instructions) > 0 {
		instruction = g.Instructions[int(count)%len(g.Instructions)]
	} else {
		instruction = "Process this test request"
	}

	return &agent.AgentInput{
		TaskID:      fmt.Sprintf("loadtest-%d-%d", time.Now().UnixNano(), count),
		Instruction: instruction,
		Context: map[string]interface{}{
			"test_id":    count,
			"timestamp":  time.Now().Unix(),
			"test_data":  generateTestData(),
			"request_id": fmt.Sprintf("req-%d", count),
		},
		MaxRetries: 3,
		Timeout:    30 * time.Second,
	}, nil
}

// generateTestData generates random test data
func generateTestData() map[string]interface{} {
	return map[string]interface{}{
		"values":    []int{rand.Intn(100), rand.Intn(100), rand.Intn(100)},
		"category":  []string{"A", "B", "C"}[rand.Intn(3)],
		"timestamp": time.Now().Format(time.RFC3339),
	}
}

// LoadTestHarness runs load tests against agents
type LoadTestHarness struct {
	config    LoadTestConfig
	agent     agent.Agent
	generator RequestGenerator
	logger    *observability.Logger

	// Runtime state
	running       atomic.Bool
	currentLoad   atomic.Int64
	latencies     []float64
	latenciesMu   sync.Mutex
	errorCounts   map[string]int64
	errorCountsMu sync.Mutex

	// Per-second tracking
	perSecondData   []PerSecondMetrics
	perSecondMu     sync.Mutex
	currentSecond   atomic.Int64
	secondRequests  atomic.Int64
	secondSuccesses atomic.Int64
	secondFailures  atomic.Int64
	secondLatencySum atomic.Int64
}

// NewLoadTestHarness creates a new load test harness
func NewLoadTestHarness(config LoadTestConfig, testAgent agent.Agent, generator RequestGenerator) *LoadTestHarness {
	if generator == nil {
		generator = NewSimpleRequestGenerator(nil)
	}

	return &LoadTestHarness{
		config:      config,
		agent:       testAgent,
		generator:   generator,
		logger:      observability.GetLogger(),
		latencies:   make([]float64, 0, 10000),
		errorCounts: make(map[string]int64),
	}
}

// Run executes the load test and returns results
func (h *LoadTestHarness) Run(ctx context.Context) (*LoadTestResult, error) {
	result := &LoadTestResult{
		Config:      h.config,
		StartTime:   time.Now(),
		ErrorCounts: make(map[string]int64),
	}

	h.running.Store(true)
	defer h.running.Store(false)

	// Create a context with timeout for the entire test
	testCtx, cancel := context.WithTimeout(ctx, h.config.Duration+h.config.RampUpDuration+10*time.Second)
	defer cancel()

	// Semaphore for concurrency control
	sem := make(chan struct{}, h.config.MaxConcurrency)
	var wg sync.WaitGroup

	// Stats tracking
	var totalRequests, successfulRequests, failedRequests, timeoutRequests int64

	// Start per-second metrics collection
	perSecondDone := make(chan struct{})
	go h.collectPerSecondMetrics(perSecondDone)

	// Calculate request interval
	baseInterval := time.Duration(float64(time.Second) / h.config.TargetRPS)

	// Main load generation loop
	startTime := time.Now()
	testEndTime := startTime.Add(h.config.Duration + h.config.RampUpDuration)

	for time.Now().Before(testEndTime) && h.running.Load() {
		select {
		case <-testCtx.Done():
			goto done
		case sem <- struct{}{}:
		}

		// Calculate current RPS based on ramp-up
		elapsed := time.Since(startTime)
		currentRPS := h.config.TargetRPS
		if h.config.RampUpDuration > 0 && elapsed < h.config.RampUpDuration {
			rampProgress := float64(elapsed) / float64(h.config.RampUpDuration)
			currentRPS = h.config.TargetRPS * rampProgress
			if currentRPS < 1 {
				currentRPS = 1
			}
		}
		interval := time.Duration(float64(time.Second) / currentRPS)

		// Generate and execute request
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			h.currentLoad.Add(1)
			defer h.currentLoad.Add(-1)

			atomic.AddInt64(&totalRequests, 1)
			h.secondRequests.Add(1)

			// Generate request
			input, err := h.generator.Generate()
			if err != nil {
				atomic.AddInt64(&failedRequests, 1)
				h.secondFailures.Add(1)
				h.recordError("generator_error")
				return
			}

			// Execute with timeout
			reqCtx, reqCancel := context.WithTimeout(testCtx, h.config.RequestTimeout)
			defer reqCancel()

			start := time.Now()
			_, err = h.agent.Execute(reqCtx, input)
			latency := time.Since(start)

			h.recordLatency(float64(latency.Milliseconds()))
			h.secondLatencySum.Add(latency.Milliseconds())

			if err != nil {
				if reqCtx.Err() == context.DeadlineExceeded {
					atomic.AddInt64(&timeoutRequests, 1)
					h.recordError("timeout")
				} else {
					atomic.AddInt64(&failedRequests, 1)
					h.recordError(categorizeError(err))
				}
				h.secondFailures.Add(1)
			} else {
				atomic.AddInt64(&successfulRequests, 1)
				h.secondSuccesses.Add(1)
			}
		}()

		// Wait for next request (with jitter)
		jitter := time.Duration(rand.Int63n(int64(interval / 10)))
		time.Sleep(interval + jitter - baseInterval/10)
	}

done:
	// Wait for all requests to complete
	wg.Wait()
	close(perSecondDone)

	// Calculate results
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(result.StartTime)
	result.TotalRequests = totalRequests
	result.SuccessfulRequests = successfulRequests
	result.FailedRequests = failedRequests
	result.TimeoutRequests = timeoutRequests
	result.ActualRPS = float64(totalRequests) / result.Duration.Seconds()

	// Calculate latency percentiles
	h.latenciesMu.Lock()
	latencies := make([]float64, len(h.latencies))
	copy(latencies, h.latencies)
	h.latenciesMu.Unlock()

	if len(latencies) > 0 {
		sort.Float64s(latencies)
		result.MinLatency = latencies[0]
		result.MaxLatency = latencies[len(latencies)-1]
		result.AvgLatency = average(latencies)
		result.P50Latency = percentile(latencies, 50)
		result.P90Latency = percentile(latencies, 90)
		result.P95Latency = percentile(latencies, 95)
		result.P99Latency = percentile(latencies, 99)
	}

	// Copy error counts
	h.errorCountsMu.Lock()
	for k, v := range h.errorCounts {
		result.ErrorCounts[k] = v
	}
	h.errorCountsMu.Unlock()

	// Copy per-second data
	h.perSecondMu.Lock()
	result.PerSecondData = h.perSecondData
	h.perSecondMu.Unlock()

	// Output results
	if err := h.outputResults(result); err != nil {
		return result, fmt.Errorf("failed to output results: %w", err)
	}

	return result, nil
}

// Stop stops the load test
func (h *LoadTestHarness) Stop() {
	h.running.Store(false)
}

// recordLatency records a latency measurement
func (h *LoadTestHarness) recordLatency(ms float64) {
	h.latenciesMu.Lock()
	h.latencies = append(h.latencies, ms)
	h.latenciesMu.Unlock()
}

// recordError records an error by type
func (h *LoadTestHarness) recordError(errType string) {
	h.errorCountsMu.Lock()
	h.errorCounts[errType]++
	h.errorCountsMu.Unlock()
}

// collectPerSecondMetrics collects metrics every second
func (h *LoadTestHarness) collectPerSecondMetrics(done <-chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	second := 0
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			second++

			reqs := h.secondRequests.Swap(0)
			successes := h.secondSuccesses.Swap(0)
			failures := h.secondFailures.Swap(0)
			latencySum := h.secondLatencySum.Swap(0)

			var avgLatency float64
			if reqs > 0 {
				avgLatency = float64(latencySum) / float64(reqs)
			}

			metrics := PerSecondMetrics{
				Second:      second,
				Requests:    reqs,
				Successes:   successes,
				Failures:    failures,
				AvgLatency:  avgLatency,
				Concurrency: int(h.currentLoad.Load()),
			}

			h.perSecondMu.Lock()
			h.perSecondData = append(h.perSecondData, metrics)
			h.perSecondMu.Unlock()

			if h.config.Verbose {
				h.logger.Info("load_test_second",
					observability.Field("second", second),
					observability.Field("requests", reqs),
					observability.Field("successes", successes),
					observability.Field("failures", failures),
					observability.Field("avg_latency_ms", avgLatency),
					observability.Field("concurrency", metrics.Concurrency),
				)
			}
		}
	}
}

// outputResults outputs test results
func (h *LoadTestHarness) outputResults(result *LoadTestResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}

	if h.config.OutputFile != "" {
		return os.WriteFile(h.config.OutputFile, data, 0644)
	}

	fmt.Println(string(data))
	return nil
}

// Helper functions

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func percentile(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)) * float64(p) / 100)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func categorizeError(err error) string {
	if err == nil {
		return "none"
	}
	switch err {
	case context.DeadlineExceeded:
		return "timeout"
	case context.Canceled:
		return "canceled"
	default:
		return "execution_error"
	}
}

// PrintSummary prints a human-readable summary of results
func (r *LoadTestResult) PrintSummary() {
	fmt.Println("\n" + "=" + repeatString("=", 60))
	fmt.Printf("Load Test Results: %s\n", r.Config.Name)
	fmt.Println(repeatString("=", 61))

	fmt.Printf("\nDuration: %v\n", r.Duration.Round(time.Second))
	fmt.Printf("Target RPS: %.1f\n", r.Config.TargetRPS)
	fmt.Printf("Actual RPS: %.1f\n", r.ActualRPS)

	fmt.Println("\n--- Requests ---")
	fmt.Printf("Total:      %d\n", r.TotalRequests)
	fmt.Printf("Successful: %d (%.1f%%)\n", r.SuccessfulRequests,
		float64(r.SuccessfulRequests)/float64(r.TotalRequests)*100)
	fmt.Printf("Failed:     %d (%.1f%%)\n", r.FailedRequests,
		float64(r.FailedRequests)/float64(r.TotalRequests)*100)
	fmt.Printf("Timeouts:   %d (%.1f%%)\n", r.TimeoutRequests,
		float64(r.TimeoutRequests)/float64(r.TotalRequests)*100)

	fmt.Println("\n--- Latency (ms) ---")
	fmt.Printf("Min: %.2f\n", r.MinLatency)
	fmt.Printf("Avg: %.2f\n", r.AvgLatency)
	fmt.Printf("P50: %.2f\n", r.P50Latency)
	fmt.Printf("P90: %.2f\n", r.P90Latency)
	fmt.Printf("P95: %.2f\n", r.P95Latency)
	fmt.Printf("P99: %.2f\n", r.P99Latency)
	fmt.Printf("Max: %.2f\n", r.MaxLatency)

	if len(r.ErrorCounts) > 0 {
		fmt.Println("\n--- Error Breakdown ---")
		for errType, count := range r.ErrorCounts {
			fmt.Printf("%s: %d\n", errType, count)
		}
	}

	fmt.Println(repeatString("=", 61))
}

func repeatString(s string, count int) string {
	result := ""
	for i := 0; i < count; i++ {
		result += s
	}
	return result
}

// MockAgent is a test agent for load testing
type MockAgent struct {
	name         string
	latency      time.Duration
	errorRate    float64
	mu           sync.Mutex
	callCount    int64
	successCount int64
	errorCount   int64
}

// NewMockAgent creates a mock agent for testing
func NewMockAgent(name string, latency time.Duration, errorRate float64) *MockAgent {
	return &MockAgent{
		name:      name,
		latency:   latency,
		errorRate: errorRate,
	}
}

func (m *MockAgent) ID() string           { return "mock-" + m.name }
func (m *MockAgent) Name() string         { return m.name }
func (m *MockAgent) Description() string  { return "Mock agent for load testing" }
func (m *MockAgent) Capabilities() []string { return []string{"mock"} }
func (m *MockAgent) Dependencies() []agent.Agent { return nil }
func (m *MockAgent) Partition() string    { return "" }

func (m *MockAgent) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	m.mu.Lock()
	m.callCount++
	m.mu.Unlock()

	// Simulate processing time with some jitter
	jitter := time.Duration(rand.Int63n(int64(m.latency / 10)))
	select {
	case <-time.After(m.latency + jitter):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Simulate errors based on error rate
	if rand.Float64() < m.errorRate {
		m.mu.Lock()
		m.errorCount++
		m.mu.Unlock()
		return nil, fmt.Errorf("simulated error")
	}

	m.mu.Lock()
	m.successCount++
	m.mu.Unlock()

	return &agent.AgentOutput{
		TaskID: input.TaskID,
		Result: map[string]interface{}{
			"status":    "completed",
			"processed": true,
		},
		Metrics: &agent.ExecutionMetrics{
			StartTime: time.Now().Add(-m.latency),
			EndTime:   time.Now(),
			Duration:  m.latency,
		},
	}, nil
}

// Stats returns mock agent statistics
func (m *MockAgent) Stats() (calls, successes, errors int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.callCount, m.successCount, m.errorCount
}
