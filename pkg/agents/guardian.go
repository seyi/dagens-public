// Package agents provides agent implementations with agentic capabilities.
package agents

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/agent"
)

// Digital Health Twin domain metrics
const (
	DomainPredictionHorizon   = "domain.health.prediction_horizon_minutes"
	DomainFalsePositiveRate   = "domain.health.false_positive_rate"
	DomainDataStreamIntegrity = "domain.health.data_stream_integrity_percent"
	DomainAlertsAcknowledged  = "domain.health.alerts_acknowledged_rate"
)

// WearableClient defines the interface for wearable device connections.
type WearableClient interface {
	CheckConnection() (float64, error)
}

// MockWearableClient simulates a connection to a patient's wearable device.
type MockWearableClient struct {
	IsConnected bool
	BatteryLvl  float64
}

// CheckConnection checks the connection to the wearable device.
func (c *MockWearableClient) CheckConnection() (float64, error) {
	if !c.IsConnected || c.BatteryLvl < 0.1 {
		return 0.0, fmt.Errorf("environment check failed: connection lost or battery low (%.2f%%)", c.BatteryLvl*100)
	}
	// Return a connection quality score
	return 0.95, nil
}

// GuardianAgent monitors real-time health data to predict and preempt adverse events.
type GuardianAgent struct {
	*agent.BaseAgent
	wearableClient WearableClient
	mu             sync.Mutex
	execCount      int
	// lastEnvScore stores the last environment readiness score for access after execution
	lastEnvScore float64
}

// NewGuardianAgent creates a new GuardianAgent.
func NewGuardianAgent(client WearableClient) *GuardianAgent {
	ga := &GuardianAgent{
		wearableClient: client,
	}

	base := agent.NewAgent(agent.AgentConfig{
		Name:         "Guardian",
		Description:  "Monitors real-time health data to predict and preempt adverse health events.",
		Capabilities: []string{"health_monitoring", "event_prediction", "alert_dispatch"},
		Executor:     agent.ExecutorFunc(ga.execute),
	})
	ga.BaseAgent = base

	return ga
}

// execute simulates the core logic of the health monitoring agent.
func (ga *GuardianAgent) execute(ctx context.Context, a agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	ga.mu.Lock()
	ga.execCount++
	currentAttempt := ga.execCount
	ga.mu.Unlock()

	// --- ENVIRONMENT MONITORING ---
	connectionQuality, err := ga.wearableClient.CheckConnection()
	ga.mu.Lock()
	ga.lastEnvScore = connectionQuality
	ga.mu.Unlock()

	if err != nil {
		// This failure is due to the environment and might not be fixable by a simple retry,
		// but the self-healing wrapper will try anyway.
		return nil, err
	}

	// --- SELF-HEALING DEMO ---
	if failCount, ok := input.Context["force_fail_count"].(int); ok {
		if currentAttempt <= failCount {
			return nil, fmt.Errorf("simulated failure: notification service unresponsive on attempt %d", currentAttempt)
		}
	}

	// --- CORE LOGIC ---
	// 1. Analyze data stream (simulated).
	time.Sleep(20 * time.Millisecond)
	prediction := "Predicted hypoglycemic event in 15 minutes. Confidence: 98%."
	shouldAlert := rand.Float64() > 0.1 // 90% chance of being a real alert

	// 2. Send alert (this is the part that might need retries).
	fmt.Println("Guardian: Sending critical alert...")
	time.Sleep(30 * time.Millisecond) // Simulate network call
	fmt.Println("Guardian: Alert delivered successfully.")

	return &agent.AgentOutput{
		TaskID: input.TaskID,
		Result: prediction,
		Metadata: map[string]interface{}{
			"alert_sent":         shouldAlert,
			"connection_quality": connectionQuality,
		},
	}, nil
}

// LastEnvironmentScore returns the last recorded environment readiness score.
func (ga *GuardianAgent) LastEnvironmentScore() float64 {
	ga.mu.Lock()
	defer ga.mu.Unlock()
	return ga.lastEnvScore
}

// ResetExecCount resets the execution counter for testing.
func (ga *GuardianAgent) ResetExecCount() {
	ga.mu.Lock()
	ga.execCount = 0
	ga.mu.Unlock()
}
