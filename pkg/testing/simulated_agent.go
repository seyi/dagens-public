package testing

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/seyi/dagens/pkg/agent"
)

// SimulatedAgent simulates an agent with configurable latency and error rate
type SimulatedAgent struct {
	*agent.BaseAgent
	minLatency time.Duration
	maxLatency time.Duration
	errorRate  float64 // 0.0 to 1.0
	mu         sync.RWMutex

	// Stats tracking
	callCount    atomic.Int64
	successCount atomic.Int64
	errorCount   atomic.Int64
}

// NewSimulatedAgent creates a new simulated agent with specified parameters
func NewSimulatedAgent(name string, minLat, maxLat time.Duration, errRate float64) *SimulatedAgent {
	sa := &SimulatedAgent{
		minLatency: minLat,
		maxLatency: maxLat,
		errorRate:  errRate,
	}

	executor := func(ctx context.Context, a agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
		sa.callCount.Add(1)

		// Read config under lock
		sa.mu.RLock()
		minLat := sa.minLatency
		maxLat := sa.maxLatency
		errRate := sa.errorRate
		sa.mu.RUnlock()

		// Simulate Latency
		latency := minLat
		if maxLat > minLat {
			jitter := time.Duration(rand.Int63n(int64(maxLat - minLat)))
			latency += jitter
		}

		select {
		case <-time.After(latency):
		case <-ctx.Done():
			return nil, ctx.Err()
		}

		// Simulate Error
		if rand.Float64() < errRate {
			sa.errorCount.Add(1)
			return nil, fmt.Errorf("simulated random failure")
		}

		sa.successCount.Add(1)
		return &agent.AgentOutput{
			Result: fmt.Sprintf("Processed by %s", name),
		}, nil
	}

	sa.BaseAgent = agent.NewAgent(agent.AgentConfig{
		Name:     name,
		Executor: agent.ExecutorFunc(executor),
	})

	return sa
}

// SetErrorRate allows dynamic changing of behavior during load tests
func (sa *SimulatedAgent) SetErrorRate(rate float64) {
	sa.mu.Lock()
	sa.errorRate = rate
	sa.mu.Unlock()
}

// SetLatencyRange sets the latency range for the simulated agent
func (sa *SimulatedAgent) SetLatencyRange(min, max time.Duration) {
	sa.mu.Lock()
	sa.minLatency = min
	sa.maxLatency = max
	sa.mu.Unlock()
}

// Stats returns execution statistics (calls, successes, errors)
func (sa *SimulatedAgent) Stats() (calls, successes, errors int64) {
	return sa.callCount.Load(), sa.successCount.Load(), sa.errorCount.Load()
}

// ResetStats resets all counters to zero
func (sa *SimulatedAgent) ResetStats() {
	sa.callCount.Store(0)
	sa.successCount.Store(0)
	sa.errorCount.Store(0)
}