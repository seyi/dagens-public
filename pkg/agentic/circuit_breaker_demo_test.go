package agentic

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/resilience"
	"github.com/stretchr/testify/assert"
)

func TestSelfHealing_WithCircuitBreaker(t *testing.T) {
	// 1. Setup a Circuit Breaker with a very low threshold for the demo
	cbName := "demo-breaker"
	cbConfig := resilience.CircuitBreakerConfig{
		Name:             cbName,
		FailureThreshold: 3,                // Trip after 3 failures
		Timeout:          100 * time.Millisecond, // Stay open for 100ms
	}
	cb := resilience.NewCircuitBreaker(cbConfig)

	// 2. Setup SelfHealing with this Circuit Breaker
	config := SelfHealingConfig{
		MaxRetries:     5, // Allow up to 5 retries
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
		BackoffFactor:  2.0,
	}
	sh := NewSelfHealing(config, nil, WithCircuitBreaker(cb))

	// 3. First execution: Trip the circuit
	// We will simulate a service that is completely down
	callCount := 0
	err := sh.ExecuteWithHealing(context.Background(), func(ctx context.Context) error {
		callCount++
		return errors.New("downstream service down")
	})

	// Analysis:
	// - Initial call: Failure (1)
	// - Retry 1: Failure (2)
	// - Retry 2: Failure (3) -> Circuit TRIPS here
	// - Retry 3: Circuit is OPEN -> Returns ErrCircuitOpen immediately
	
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "circuit breaker open")
	assert.Equal(t, 3, callCount, "Should have stopped after 3 calls because the circuit tripped")
	assert.Equal(t, resilience.StateOpen, cb.State())

	// 4. Second execution: Fast failure
	// Now that the circuit is OPEN, a new call should fail IMMEDIATELY without even trying once
	callCountBefore := callCount
	err2 := sh.ExecuteWithHealing(context.Background(), func(ctx context.Context) error {
		callCount++
		return nil
	})

	assert.Error(t, err2)
	assert.Equal(t, resilience.ErrCircuitOpen, errors.Unwrap(err2))
	assert.Equal(t, callCountBefore, callCount, "Should not have called the function at all while circuit is open")
	
	t.Logf("✅ Verified: Circuit Breaker stopped execution after %d failures and prevented subsequent calls.", callCount)
}
