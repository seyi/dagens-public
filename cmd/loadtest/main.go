package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/agents"
	"github.com/seyi/dagens/pkg/observability"
	"github.com/seyi/dagens/pkg/testing"
)

var (
	concurrencyLevel = flag.Int("concurrency", 50, "Number of concurrent goroutines")
	duration         = flag.Duration("duration", 30*time.Second, "Duration of the test")
	scenario         = flag.String("scenario", "throughput", "Test scenario: throughput, circuit_breaker, least_loaded")
	strategy         = flag.String("strategy", "round-robin", "Load balancing strategy: round-robin, random, least-loaded")
	targetRPS        = flag.Float64("rps", 100, "Target requests per second")
	outputJSON       = flag.Bool("json", false, "Output results as JSON")
	warmupDuration   = flag.Duration("warmup", 5*time.Second, "Warmup duration before measurements")
)

// TestResult holds comprehensive test results
type TestResult struct {
	Scenario       string                 `json:"scenario"`
	Strategy       string                 `json:"strategy"`
	Duration       string                 `json:"duration"`
	Concurrency    int                    `json:"concurrency"`
	TotalRequests  int64                  `json:"total_requests"`
	Successes      int64                  `json:"successes"`
	Errors         int64                  `json:"errors"`
	ActualRPS      float64                `json:"actual_rps"`
	ErrorRate      float64                `json:"error_rate_percent"`
	Latency        LatencyStats           `json:"latency_ms"`
	AgentStats     []AgentStat            `json:"agent_stats,omitempty"`
	CircuitBreaker *CircuitBreakerResult  `json:"circuit_breaker,omitempty"`
	Passed         bool                   `json:"passed"`
	FailureReason  string                 `json:"failure_reason,omitempty"`
}

type LatencyStats struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
	Avg float64 `json:"avg"`
	P50 float64 `json:"p50"`
	P90 float64 `json:"p90"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
}

type AgentStat struct {
	Name      string  `json:"name"`
	Calls     int64   `json:"calls"`
	Successes int64   `json:"successes"`
	Errors    int64   `json:"errors"`
	Percent   float64 `json:"percent_of_total"`
}

type CircuitBreakerResult struct {
	TrippedAsExpected   bool   `json:"tripped_as_expected"`
	RecoveredAsExpected bool   `json:"recovered_as_expected"`
	FailureInjectedAt   string `json:"failure_injected_at"`
	HealedAt            string `json:"healed_at"`
}

func main() {
	flag.Parse()

	// Setup signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\nReceived shutdown signal...")
		cancel()
	}()

	// Start metrics server
	go func() {
		http.Handle("/metrics", observability.Handler())
		fmt.Println("Metrics server starting on :8080")
		if err := http.ListenAndServe(":8080", nil); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Metrics server error: %v\n", err)
		}
	}()

	// Give metrics server time to start
	time.Sleep(100 * time.Millisecond)

	var result *TestResult
	var err error

	// Run test based on scenario
	switch *scenario {
	case "throughput":
		result, err = runThroughputTest(ctx)
	case "circuit_breaker":
		result, err = runCircuitBreakerTest(ctx)
	case "least_loaded":
		result, err = runLeastLoadedTest(ctx)
	default:
		fmt.Printf("Unknown scenario: %s\n", *scenario)
		os.Exit(1)
	}

	if err != nil {
		fmt.Printf("Test failed: %v\n", err)
		os.Exit(1)
	}

	// Output results
	if *outputJSON {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(data))
	} else {
		printResult(result)
	}

	// Exit with appropriate code
	if !result.Passed {
		os.Exit(1)
	}
}

func runThroughputTest(ctx context.Context) (*TestResult, error) {
	fmt.Printf("=== THROUGHPUT TEST ===\n")
	fmt.Printf("Concurrency: %d, Duration: %v, Strategy: %s, Target RPS: %.0f\n\n",
		*concurrencyLevel, *duration, *strategy, *targetRPS)

	// Setup simulated agents
	workers := []*testing.SimulatedAgent{
		testing.NewSimulatedAgent("worker-1", 10*time.Millisecond, 50*time.Millisecond, 0.0),
		testing.NewSimulatedAgent("worker-2", 10*time.Millisecond, 50*time.Millisecond, 0.0),
		testing.NewSimulatedAgent("worker-3", 10*time.Millisecond, 50*time.Millisecond, 0.0),
		testing.NewSimulatedAgent("worker-4", 10*time.Millisecond, 50*time.Millisecond, 0.0),
		testing.NewSimulatedAgent("worker-5", 10*time.Millisecond, 50*time.Millisecond, 0.0),
	}

	// Convert to agent.Agent slice
	agentSlice := make([]agent.Agent, len(workers))
	for i, w := range workers {
		agentSlice[i] = w
	}

	// Setup load balancer
	lbStrategy := getStrategy(*strategy)
	lb := agents.NewLoadBalancerAgent(agents.LoadBalancerAgentConfig{
		Name:               "throughput-test-lb",
		Agents:             agentSlice,
		Strategy:           lbStrategy,
		EnableHealthChecks: true,
	})

	// Run load test with latency tracking
	result := runLoadWithLatencyTracking(ctx, lb, workers)
	result.Scenario = "throughput"
	result.Strategy = *strategy

	// Validation criteria: error rate < 1%
	result.Passed = result.ErrorRate < 1.0
	if !result.Passed {
		result.FailureReason = fmt.Sprintf("Error rate %.2f%% exceeds 1%% threshold", result.ErrorRate)
	}

	return result, nil
}

func runCircuitBreakerTest(ctx context.Context) (*TestResult, error) {
	fmt.Printf("=== CIRCUIT BREAKER TEST ===\n")
	fmt.Printf("Concurrency: %d, Duration: %v\n", *concurrencyLevel, *duration)
	fmt.Printf("Will inject 100%% failure at T+10s, heal at T+20s\n\n")

	// Setup simulated agents
	healthyWorker := testing.NewSimulatedAgent("healthy-worker", 10*time.Millisecond, 50*time.Millisecond, 0.0)
	failingWorker := testing.NewSimulatedAgent("failing-worker", 10*time.Millisecond, 50*time.Millisecond, 0.0)
	backupWorker := testing.NewSimulatedAgent("backup-worker", 10*time.Millisecond, 50*time.Millisecond, 0.0)

	workers := []*testing.SimulatedAgent{healthyWorker, failingWorker, backupWorker}
	agentSlice := make([]agent.Agent, len(workers))
	for i, w := range workers {
		agentSlice[i] = w
	}

	// Setup load balancer with circuit breakers
	lb := agents.NewLoadBalancerAgent(agents.LoadBalancerAgentConfig{
		Name:               "circuit-breaker-test-lb",
		Agents:             agentSlice,
		Strategy:           agents.RoundRobin,
		EnableHealthChecks: true,
	})

	cbResult := &CircuitBreakerResult{}

	// Track circuit breaker behavior
	var failingWorkerCallsBefore, failingWorkerCallsAfterInjection int64
	var cbTripped, cbRecovered bool

	// Failure injection goroutine
	go func() {
		// Wait 10 seconds, then inject failure
		select {
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
			return
		}

		failingWorkerCallsBefore, _, _ = failingWorker.Stats()
		fmt.Println(">>> INJECTING FAILURE: failing-worker set to 100% error rate")
		cbResult.FailureInjectedAt = time.Now().Format(time.RFC3339)
		failingWorker.SetErrorRate(1.0)

		// Wait 10 more seconds, then heal
		select {
		case <-time.After(10 * time.Second):
		case <-ctx.Done():
			return
		}

		failingWorkerCallsAfterInjection, _, _ = failingWorker.Stats()
		fmt.Println(">>> HEALING: failing-worker set to 0% error rate")
		cbResult.HealedAt = time.Now().Format(time.RFC3339)
		failingWorker.SetErrorRate(0.0)
	}()

	// Run load test
	result := runLoadWithLatencyTracking(ctx, lb, workers)
	result.Scenario = "circuit_breaker"
	result.Strategy = "round-robin"

	// Check if circuit breaker behavior was correct
	// After injection, the failing worker should receive significantly fewer calls
	// because the circuit breaker should trip
	finalCalls, _, finalErrors := failingWorker.Stats()

	// If CB tripped, calls during failure period should be much lower than healthy workers
	healthyCalls, _, _ := healthyWorker.Stats()

	// Circuit breaker should have reduced traffic to failing worker
	// We expect failing worker to have received significantly fewer calls during failure window
	callsDuringFailure := failingWorkerCallsAfterInjection - failingWorkerCallsBefore
	cbTripped = callsDuringFailure < healthyCalls/3 // Failing worker should get much less traffic

	// After healing, failing worker should start receiving traffic again
	callsAfterHealing := finalCalls - failingWorkerCallsAfterInjection
	cbRecovered = callsAfterHealing > 0

	cbResult.TrippedAsExpected = cbTripped
	cbResult.RecoveredAsExpected = cbRecovered
	result.CircuitBreaker = cbResult

	// Validation
	result.Passed = cbTripped && result.ErrorRate < 20.0 // Some errors expected during failure window
	if !result.Passed {
		if !cbTripped {
			result.FailureReason = "Circuit breaker did not trip as expected"
		} else {
			result.FailureReason = fmt.Sprintf("Error rate %.2f%% too high", result.ErrorRate)
		}
	}

	fmt.Printf("\nCircuit Breaker Analysis:\n")
	fmt.Printf("  Failing worker total calls: %d (errors: %d)\n", finalCalls, finalErrors)
	fmt.Printf("  Healthy worker total calls: %d\n", healthyCalls)
	fmt.Printf("  CB tripped correctly: %v\n", cbTripped)
	fmt.Printf("  CB recovered correctly: %v\n", cbRecovered)

	return result, nil
}

func runLeastLoadedTest(ctx context.Context) (*TestResult, error) {
	fmt.Printf("=== LEAST LOADED TEST ===\n")
	fmt.Printf("Concurrency: %d, Duration: %v\n", *concurrencyLevel, *duration)
	fmt.Printf("Fast worker: 10-20ms, Slow worker: 500-600ms\n\n")

	// Setup simulated agents with different latencies
	fastWorker := testing.NewSimulatedAgent("fast-worker", 10*time.Millisecond, 20*time.Millisecond, 0.0)
	slowWorker := testing.NewSimulatedAgent("slow-worker", 500*time.Millisecond, 600*time.Millisecond, 0.0)

	workers := []*testing.SimulatedAgent{fastWorker, slowWorker}
	agentSlice := make([]agent.Agent, len(workers))
	for i, w := range workers {
		agentSlice[i] = w
	}

	// Setup load balancer with LeastLoaded strategy
	lb := agents.NewLoadBalancerAgent(agents.LoadBalancerAgentConfig{
		Name:               "least-loaded-test-lb",
		Agents:             agentSlice,
		Strategy:           agents.LeastLoaded,
		EnableHealthChecks: true,
	})

	// Run load test
	result := runLoadWithLatencyTracking(ctx, lb, workers)
	result.Scenario = "least_loaded"
	result.Strategy = "least-loaded"

	// Get distribution stats
	fastCalls, fastSuccesses, fastErrors := fastWorker.Stats()
	slowCalls, slowSuccesses, slowErrors := slowWorker.Stats()
	totalCalls := fastCalls + slowCalls

	result.AgentStats = []AgentStat{
		{
			Name:      "fast-worker",
			Calls:     fastCalls,
			Successes: fastSuccesses,
			Errors:    fastErrors,
			Percent:   float64(fastCalls) / float64(totalCalls) * 100,
		},
		{
			Name:      "slow-worker",
			Calls:     slowCalls,
			Successes: slowSuccesses,
			Errors:    slowErrors,
			Percent:   float64(slowCalls) / float64(totalCalls) * 100,
		},
	}

	// Validation: fast worker should handle significantly more requests
	// With 10-20ms vs 500-600ms, fast worker should get ~25-50x more requests
	ratio := float64(fastCalls) / float64(slowCalls+1) // +1 to avoid divide by zero
	result.Passed = ratio > 5.0 // Fast worker should get at least 5x more requests
	if !result.Passed {
		result.FailureReason = fmt.Sprintf("Load distribution ratio %.1f:1 is too low (expected >5:1)", ratio)
	}

	fmt.Printf("\nLoad Distribution Analysis:\n")
	fmt.Printf("  Fast worker: %d calls (%.1f%%)\n", fastCalls, result.AgentStats[0].Percent)
	fmt.Printf("  Slow worker: %d calls (%.1f%%)\n", slowCalls, result.AgentStats[1].Percent)
	fmt.Printf("  Ratio: %.1f:1 (fast:slow)\n", ratio)

	return result, nil
}

func runLoadWithLatencyTracking(ctx context.Context, lb *agents.LoadBalancerAgent, workers []*testing.SimulatedAgent) *TestResult {
	var wg sync.WaitGroup
	var requestCount, errorCount int64
	var latencies []float64
	var latenciesMu sync.Mutex

	// Create test context
	testCtx, cancel := context.WithTimeout(ctx, *duration+*warmupDuration)
	defer cancel()

	// Warmup phase
	if *warmupDuration > 0 {
		fmt.Printf("Warming up for %v...\n", *warmupDuration)
		time.Sleep(*warmupDuration)
		// Reset worker stats after warmup
		for _, w := range workers {
			w.ResetStats()
		}
	}

	measureStart := time.Now()
	fmt.Println("Running load test...")

	// Launch workers
	for i := 0; i < *concurrencyLevel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-testCtx.Done():
					return
				default:
					input := &agent.AgentInput{
						Instruction: "load_test_ping",
						TaskID:      fmt.Sprintf("req-%d", time.Now().UnixNano()),
					}

					start := time.Now()
					_, err := lb.Execute(testCtx, input)
					latency := time.Since(start)

					if err != nil {
						atomic.AddInt64(&errorCount, 1)
					} else {
						atomic.AddInt64(&requestCount, 1)
					}

					// Record latency
					latenciesMu.Lock()
					latencies = append(latencies, float64(latency.Milliseconds()))
					latenciesMu.Unlock()
				}
			}
		}()
	}

	// Wait for duration
	select {
	case <-time.After(*duration):
	case <-ctx.Done():
	}
	cancel()
	wg.Wait()

	elapsed := time.Since(measureStart)
	totalReqs := requestCount + errorCount

	// Calculate latency percentiles
	latencyStats := calculateLatencyStats(latencies)

	result := &TestResult{
		Duration:      elapsed.String(),
		Concurrency:   *concurrencyLevel,
		TotalRequests: totalReqs,
		Successes:     requestCount,
		Errors:        errorCount,
		ActualRPS:     float64(totalReqs) / elapsed.Seconds(),
		ErrorRate:     float64(errorCount) / float64(totalReqs) * 100,
		Latency:       latencyStats,
	}

	return result
}

func calculateLatencyStats(latencies []float64) LatencyStats {
	if len(latencies) == 0 {
		return LatencyStats{}
	}

	sorted := make([]float64, len(latencies))
	copy(sorted, latencies)
	sort.Float64s(sorted)

	var sum float64
	for _, v := range sorted {
		sum += v
	}

	return LatencyStats{
		Min: sorted[0],
		Max: sorted[len(sorted)-1],
		Avg: sum / float64(len(sorted)),
		P50: percentile(sorted, 50),
		P90: percentile(sorted, 90),
		P95: percentile(sorted, 95),
		P99: percentile(sorted, 99),
	}
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

func getStrategy(s string) agents.LoadBalanceStrategy {
	switch s {
	case "random":
		return agents.Random
	case "least-loaded":
		return agents.LeastLoaded
	default:
		return agents.RoundRobin
	}
}

func printResult(r *TestResult) {
	fmt.Println("\n" + repeatString("=", 60))
	fmt.Printf("TEST RESULTS: %s (%s)\n", r.Scenario, r.Strategy)
	fmt.Println(repeatString("=", 60))

	fmt.Printf("\n--- Summary ---\n")
	fmt.Printf("Duration:       %s\n", r.Duration)
	fmt.Printf("Concurrency:    %d\n", r.Concurrency)
	fmt.Printf("Total Requests: %d\n", r.TotalRequests)
	fmt.Printf("Successful:     %d\n", r.Successes)
	fmt.Printf("Errors:         %d\n", r.Errors)
	fmt.Printf("Actual RPS:     %.2f\n", r.ActualRPS)
	fmt.Printf("Error Rate:     %.2f%%\n", r.ErrorRate)

	fmt.Printf("\n--- Latency (ms) ---\n")
	fmt.Printf("Min: %.2f\n", r.Latency.Min)
	fmt.Printf("Avg: %.2f\n", r.Latency.Avg)
	fmt.Printf("P50: %.2f\n", r.Latency.P50)
	fmt.Printf("P90: %.2f\n", r.Latency.P90)
	fmt.Printf("P95: %.2f\n", r.Latency.P95)
	fmt.Printf("P99: %.2f\n", r.Latency.P99)
	fmt.Printf("Max: %.2f\n", r.Latency.Max)

	if len(r.AgentStats) > 0 {
		fmt.Printf("\n--- Agent Distribution ---\n")
		for _, s := range r.AgentStats {
			fmt.Printf("%s: %d calls (%.1f%%)\n", s.Name, s.Calls, s.Percent)
		}
	}

	if r.CircuitBreaker != nil {
		fmt.Printf("\n--- Circuit Breaker ---\n")
		fmt.Printf("Tripped as expected:   %v\n", r.CircuitBreaker.TrippedAsExpected)
		fmt.Printf("Recovered as expected: %v\n", r.CircuitBreaker.RecoveredAsExpected)
	}

	fmt.Printf("\n--- Result ---\n")
	if r.Passed {
		fmt.Println("✅ PASSED")
	} else {
		fmt.Printf("❌ FAILED: %s\n", r.FailureReason)
	}
	fmt.Println(repeatString("=", 60))
}

func repeatString(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}
