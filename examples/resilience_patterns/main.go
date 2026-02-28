//go:build ignore

// Copyright 2025 Apache Spark AI Agents
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Example demonstrating production-grade resilience patterns for AI agents.
// These patterns ensure fault tolerance and graceful degradation in distributed systems.
//
// Run with: go run resilience_patterns_example.go
package main

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/resilience"
)

// Resilience_Example1_ExponentialBackoff demonstrates automatic retry with exponential backoff.
// This is essential for handling transient failures in distributed systems.
func Resilience_Example1_ExponentialBackoff() {
	fmt.Println("=== Example 1: Exponential Backoff ===")

	ctx := context.Background()

	// Configure backoff for production use
	config := resilience.BackoffConfig{
		InitialDelay: 100 * time.Millisecond, // Start with 100ms
		MaxDelay:     5 * time.Second,        // Cap at 5 seconds
		Multiplier:   2.0,                    // Double delay each retry
		JitterFactor: 0.1,                    // Add 10% randomness
		MaxRetries:   5,                      // Maximum 5 retry attempts
	}

	// Simulate a flaky service that fails 3 times before succeeding
	var attempts int32

	retrier := resilience.NewRetrier(config)
	err := retrier.Do(ctx, func(ctx context.Context) error {
		attempt := atomic.AddInt32(&attempts, 1)
		fmt.Printf("  Attempt %d...\n", attempt)

		if attempt < 4 {
			return errors.New("service unavailable")
		}
		return nil
	}, resilience.WithOnRetry(func(attempt int, err error, delay time.Duration) {
		fmt.Printf("  Retry %d after error: %v (waiting %v)\n", attempt+1, err, delay)
	}))

	if err != nil {
		fmt.Printf("Operation failed: %v\n", err)
	} else {
		fmt.Printf("Operation succeeded after %d attempts\n", attempts)
	}
	fmt.Println()
}

// Resilience_Example2_CircuitBreaker demonstrates the circuit breaker pattern.
// It protects systems from cascading failures by failing fast when a service is unhealthy.
func Resilience_Example2_CircuitBreaker() {
	fmt.Println("=== Example 2: Circuit Breaker ===")

	ctx := context.Background()

	// Configure circuit breaker for a specific service
	config := resilience.CircuitBreakerConfig{
		Name:             "external-api",
		FailureThreshold: 3,               // Open after 3 failures
		SuccessThreshold: 2,               // Close after 2 successes
		Timeout:          5 * time.Second, // Try half-open after 5s
		OnStateChange: func(name string, from, to resilience.CircuitState) {
			fmt.Printf("  Circuit '%s': %s -> %s\n", name, from, to)
		},
	}

	cb := resilience.NewCircuitBreaker(config)

	// Simulate failures to trip the circuit
	fmt.Println("Simulating service failures...")
	for i := 0; i < 5; i++ {
		err := cb.Execute(ctx, func(ctx context.Context) error {
			return errors.New("service timeout")
		})
		fmt.Printf("  Request %d: %v (state: %s)\n", i+1, err, cb.State())
	}

	// Show that requests are rejected when circuit is open
	fmt.Println("\nCircuit is now open - requests fail fast:")
	err := cb.Execute(ctx, func(ctx context.Context) error {
		fmt.Println("  This won't be called")
		return nil
	})
	fmt.Printf("  Request rejected: %v\n", err)

	// Reset for demonstration
	cb.Reset()
	fmt.Printf("\nCircuit reset to: %s\n", cb.State())
	fmt.Println()
}

// Resilience_Example3_RateLimiting demonstrates rate limiting using token bucket algorithm.
// This prevents overwhelming downstream services during traffic spikes.
func Resilience_Example3_RateLimiting() {
	fmt.Println("=== Example 3: Rate Limiting ===")

	// Create a rate limiter: 5 requests per second with burst of 3
	limiter := resilience.NewTokenBucket(5.0, 3)

	// Try to make 10 rapid requests
	fmt.Println("Making 10 rapid requests (limit: 5/s, burst: 3):")
	allowed := 0
	rejected := 0

	for i := 0; i < 10; i++ {
		if limiter.Allow() {
			allowed++
			fmt.Printf("  Request %d: ALLOWED\n", i+1)
		} else {
			rejected++
			fmt.Printf("  Request %d: RATE LIMITED\n", i+1)
		}
	}

	fmt.Printf("\nSummary: %d allowed, %d rejected\n", allowed, rejected)

	// Wait for token refresh
	fmt.Println("\nWaiting 1 second for tokens to refresh...")
	time.Sleep(1 * time.Second)

	if limiter.Allow() {
		fmt.Println("  New request: ALLOWED (tokens refreshed)")
	}
	fmt.Println()
}

// mockResilientAgent implements agent.Agent for demonstration
type mockResilientAgent struct {
	name       string
	callCount  int32
	shouldFail bool
}

func (m *mockResilientAgent) ID() string             { return "mock-" + m.name }
func (m *mockResilientAgent) Name() string           { return m.name }
func (m *mockResilientAgent) Description() string    { return "Mock agent for resilience demo" }
func (m *mockResilientAgent) Capabilities() []string { return []string{"mock"} }
func (m *mockResilientAgent) Dependencies() []agent.Agent { return nil }
func (m *mockResilientAgent) Partition() string      { return "" }
func (m *mockResilientAgent) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	atomic.AddInt32(&m.callCount, 1)
	if m.shouldFail {
		return nil, errors.New("agent execution failed")
	}
	return &agent.AgentOutput{
		TaskID: input.TaskID,
		Result: "Success from " + m.name,
	}, nil
}

// Resilience_Example4_ResilientAgentExecution demonstrates wrapping agents with resilience patterns.
// This is the recommended approach for production agent deployments.
func Resilience_Example4_ResilientAgentExecution() {
	fmt.Println("=== Example 4: Resilient Agent Execution ===")

	ctx := context.Background()

	// Create a mock agent
	mockAgent := &mockResilientAgent{name: "data-processor"}

	// Configure resilience for production
	config := agent.DefaultResilientAgentConfig()
	config.BackoffConfig.MaxRetries = 3
	config.BackoffConfig.InitialDelay = 50 * time.Millisecond
	config.RateLimitEnabled = true
	config.RateLimitPerSecond = 10
	config.RateLimitBurst = 5
	config.MetricsEnabled = false // Disable for demo
	config.LoggingEnabled = false // Disable for demo

	// Wrap the agent with resilience
	resilientAgent := agent.WrapWithResilienceConfig(mockAgent, config)

	// Execute with resilience patterns applied
	input := &agent.AgentInput{
		TaskID:      "task-123",
		Instruction: "Process data with fault tolerance",
	}

	output, err := resilientAgent.Execute(ctx, input)
	if err != nil {
		fmt.Printf("Execution failed: %v\n", err)
	} else {
		fmt.Printf("Execution succeeded: %v\n", output.Result)
	}

	fmt.Printf("Agent was called %d time(s)\n", mockAgent.callCount)
	fmt.Println()
}

// Resilience_Example5_CombinedPatterns demonstrates using multiple resilience patterns together.
// In production, you typically combine retry, circuit breaker, and rate limiting.
func Resilience_Example5_CombinedPatterns() {
	fmt.Println("=== Example 5: Combined Resilience Patterns ===")

	ctx := context.Background()

	// 1. Rate limiter - prevent overwhelming the service
	limiter := resilience.NewTokenBucket(10.0, 5)

	// 2. Circuit breaker - fail fast when service is unhealthy
	cbConfig := resilience.DefaultCircuitBreakerConfig("combined-service")
	cbConfig.FailureThreshold = 3
	cb := resilience.NewCircuitBreaker(cbConfig)

	// 3. Retrier - handle transient failures
	backoff := resilience.BackoffConfig{
		InitialDelay: 50 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
		MaxRetries:   2,
	}
	retrier := resilience.NewRetrier(backoff)

	// Combined execution function
	executeWithResilience := func(operation string) error {
		// Check rate limit first
		if !limiter.Allow() {
			return resilience.ErrRateLimited
		}

		// Execute with retry and circuit breaker
		return retrier.Do(ctx, func(ctx context.Context) error {
			return cb.Execute(ctx, func(ctx context.Context) error {
				// Simulate operation
				fmt.Printf("  Executing: %s\n", operation)
				return nil
			})
		})
	}

	// Execute multiple operations
	operations := []string{"fetch-data", "process-data", "save-results"}
	for _, op := range operations {
		err := executeWithResilience(op)
		if err != nil {
			fmt.Printf("  %s failed: %v\n", op, err)
		} else {
			fmt.Printf("  %s succeeded\n", op)
		}
	}

	fmt.Println("\nResilience Pattern Stack:")
	fmt.Println("  1. Rate Limiter -> Prevents overwhelming downstream")
	fmt.Println("  2. Retrier -> Handles transient failures")
	fmt.Println("  3. Circuit Breaker -> Fails fast on persistent failures")
	fmt.Println()
}

// Resilience_Example6_GracefulDegradation demonstrates fallback strategies.
// When the primary service fails, gracefully degrade to cached or default values.
func Resilience_Example6_GracefulDegradation() {
	fmt.Println("=== Example 6: Graceful Degradation ===")

	ctx := context.Background()

	// Simulate cache
	cache := map[string]string{
		"user:123": "cached_user_data",
	}

	// Circuit breaker for the primary service (pre-tripped for demo)
	cb := resilience.NewCircuitBreaker(resilience.DefaultCircuitBreakerConfig("primary-service"))

	// Trip the circuit breaker
	for i := 0; i < 5; i++ {
		cb.Execute(ctx, func(ctx context.Context) error {
			return errors.New("primary service down")
		})
	}

	// Function with fallback
	fetchWithFallback := func(userID string) (string, error) {
		// Try primary service through circuit breaker
		var result string
		err := cb.Execute(ctx, func(ctx context.Context) error {
			// This would be a real API call
			return errors.New("primary service unavailable")
		})

		if err != nil {
			fmt.Printf("  Primary service failed: %v\n", err)

			// Fallback to cache
			if cached, ok := cache[userID]; ok {
				fmt.Println("  Falling back to cached data")
				result = cached
				return result, nil
			}

			// Fallback to default
			fmt.Println("  Falling back to default value")
			result = "default_user_data"
			return result, nil
		}

		return result, nil
	}

	// Demonstrate graceful degradation
	fmt.Println("Fetching user data with graceful degradation:")
	data, _ := fetchWithFallback("user:123")
	fmt.Printf("  Result: %s\n", data)
	fmt.Println()
}

// Resilience_Example7_TimeoutHandling demonstrates proper timeout management.
// Timeouts prevent operations from hanging indefinitely.
func Resilience_Example7_TimeoutHandling() {
	fmt.Println("=== Example 7: Timeout Handling ===")

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Simulate a slow operation
	slowOperation := func(ctx context.Context) error {
		select {
		case <-time.After(1 * time.Second):
			fmt.Println("  Operation completed")
			return nil
		case <-ctx.Done():
			fmt.Println("  Operation cancelled due to timeout")
			return ctx.Err()
		}
	}

	// Execute with timeout
	fmt.Println("Executing slow operation with 500ms timeout:")
	start := time.Now()
	err := slowOperation(ctx)
	duration := time.Since(start)

	if err == context.DeadlineExceeded {
		fmt.Printf("  Timed out after %v\n", duration)
	}
	fmt.Println()
}

// Resilience_Example8_BulkheadPattern demonstrates resource isolation.
// Bulkheads prevent one failing component from exhausting shared resources.
func Resilience_Example8_BulkheadPattern() {
	fmt.Println("=== Example 8: Bulkhead Pattern ===")

	// Semaphore-based bulkhead (limits concurrent operations)
	type Bulkhead struct {
		sem chan struct{}
	}

	newBulkhead := func(maxConcurrent int) *Bulkhead {
		return &Bulkhead{
			sem: make(chan struct{}, maxConcurrent),
		}
	}

	execute := func(b *Bulkhead, name string, operation func()) error {
		select {
		case b.sem <- struct{}{}:
			defer func() { <-b.sem }()
			operation()
			return nil
		default:
			return errors.New("bulkhead full - rejected")
		}
	}

	// Create bulkhead with max 2 concurrent operations
	bulkhead := newBulkhead(2)

	// Try to execute 5 concurrent operations
	fmt.Println("Executing 5 operations with bulkhead (max concurrent: 2):")

	for i := 1; i <= 5; i++ {
		go func(id int) {
			err := execute(bulkhead, fmt.Sprintf("op-%d", id), func() {
				fmt.Printf("  Operation %d running\n", id)
				time.Sleep(100 * time.Millisecond)
				fmt.Printf("  Operation %d completed\n", id)
			})
			if err != nil {
				fmt.Printf("  Operation %d rejected: %v\n", id, err)
			}
		}(i)
	}

	time.Sleep(500 * time.Millisecond)
	fmt.Println()
}

// RunResilienceExamples runs all resilience pattern examples
func RunResilienceExamples() {
	fmt.Println("========================================")
	fmt.Println("Production Resilience Patterns Examples")
	fmt.Println("========================================\n")

	Resilience_Example1_ExponentialBackoff()
	Resilience_Example2_CircuitBreaker()
	Resilience_Example3_RateLimiting()
	Resilience_Example4_ResilientAgentExecution()
	Resilience_Example5_CombinedPatterns()
	Resilience_Example6_GracefulDegradation()
	Resilience_Example7_TimeoutHandling()
	Resilience_Example8_BulkheadPattern()

	fmt.Println("========================================")
	fmt.Println("Key Takeaways:")
	fmt.Println("========================================")
	fmt.Println("1. Always use exponential backoff for retries")
	fmt.Println("2. Circuit breakers prevent cascading failures")
	fmt.Println("3. Rate limiting protects downstream services")
	fmt.Println("4. Combine patterns for defense in depth")
	fmt.Println("5. Implement graceful degradation with fallbacks")
	fmt.Println("6. Always set timeouts on external operations")
	fmt.Println("7. Use bulkheads to isolate failures")
}

func main() {
	RunResilienceExamples()
}
