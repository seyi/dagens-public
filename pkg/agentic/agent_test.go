package agentic

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/stretchr/testify/assert"
)

// mockAgent implements agent.Agent for testing
type mockAgent struct {
	id          string
	name        string
	failCount   int
	currentCall int
}

func newMockAgent(id, name string, failCount int) *mockAgent {
	return &mockAgent{
		id:        id,
		name:      name,
		failCount: failCount,
	}
}

func (m *mockAgent) ID() string          { return m.id }
func (m *mockAgent) Name() string        { return m.name }
func (m *mockAgent) Description() string { return "test agent" }
func (m *mockAgent) Capabilities() []string { return []string{"test"} }
func (m *mockAgent) Dependencies() []agent.Agent { return nil }
func (m *mockAgent) Partition() string   { return "default" }

func (m *mockAgent) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	m.currentCall++
	if m.currentCall <= m.failCount {
		return nil, errors.New("simulated failure")
	}
	return &agent.AgentOutput{
		Result: "success",
		Metrics: &agent.ExecutionMetrics{
			TokensUsed: 100,
		},
	}, nil
}

func TestAgenticAgent_Execute_Success(t *testing.T) {
	mock := newMockAgent("test-1", "Test Agent", 0)
	aa := NewAgenticAgent(mock)

	ctx := context.Background()
	input := &agent.AgentInput{
		TaskID:      "task-1",
		Instruction: "test instruction",
	}

	result, err := aa.Execute(ctx, input)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "success", result.Result)
	assert.Equal(t, 1, mock.currentCall)
}

func TestAgenticAgent_Execute_WithRetry(t *testing.T) {
	// Agent fails twice, then succeeds
	mock := newMockAgent("test-2", "Retry Agent", 2)
	aa := NewAgenticAgent(mock, WithMaxRetries(3))

	ctx := context.Background()
	input := &agent.AgentInput{
		TaskID:      "task-2",
		Instruction: "retry test",
	}

	result, err := aa.Execute(ctx, input)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 3, mock.currentCall) // 2 failures + 1 success
}

func TestAgenticAgent_Execute_MaxRetriesExceeded(t *testing.T) {
	// Agent always fails
	mock := newMockAgent("test-3", "Fail Agent", 100)
	aa := NewAgenticAgent(mock, WithMaxRetries(2))

	ctx := context.Background()
	input := &agent.AgentInput{
		TaskID:      "task-3",
		Instruction: "fail test",
	}

	result, err := aa.Execute(ctx, input)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "max retries")
	assert.Equal(t, 3, mock.currentCall) // 1 initial + 2 retries
}

func TestAgenticAgent_RecordDomainMetric(t *testing.T) {
	mock := newMockAgent("test-4", "Metric Agent", 0)
	aa := NewAgenticAgent(mock)

	// Record domain metrics
	aa.RecordDomainMetric("domain.graduation_rate", 0.85)
	aa.RecordDomainMetric("domain.player_morale", 90)

	// Verify via monitoring context
	mc := aa.Monitoring()
	grad, ok := mc.GetDomainMetric("domain.graduation_rate")
	assert.True(t, ok)
	assert.Equal(t, 0.85, grad)

	morale, ok := mc.GetDomainMetric("domain.player_morale")
	assert.True(t, ok)
	assert.Equal(t, 90.0, morale)
}

func TestAgenticAgent_RecordFeedback(t *testing.T) {
	mock := newMockAgent("test-5", "Feedback Agent", 0)
	aa := NewAgenticAgent(mock)

	ctx := context.Background()

	// Record feedback
	aa.RecordFeedback(ctx, "task-1", 0.8, FeedbackSourceHuman, "good work")
	aa.RecordFeedback(ctx, "task-2", 0.9, FeedbackSourceAutomated, "")

	// Check average
	avg := aa.GetAverageFeedback()
	assert.InDelta(t, 0.85, avg, 0.001)
}

func TestAgenticAgent_RecordOutcome(t *testing.T) {
	mock := newMockAgent("test-6", "Outcome Agent", 0)
	aa := NewAgenticAgent(mock)

	// Record outcome
	aa.RecordOutcome(0.95)

	// No error means success - outcome is recorded to telemetry
}

func TestMonitoringContext_DomainMetrics(t *testing.T) {
	mc := NewMonitoringContextWithDefaults("agent-1", "Test", "test")

	// Record various domain metrics
	mc.RecordDomainGauge("domain.wind_speed", 12.5)
	mc.RecordDomainGauge("domain.distance_error", 0.05)

	// Retrieve
	wind, ok := mc.GetDomainMetric("domain.wind_speed")
	assert.True(t, ok)
	assert.Equal(t, 12.5, wind)

	dist, ok := mc.GetDomainMetric("domain.distance_error")
	assert.True(t, ok)
	assert.Equal(t, 0.05, dist)

	// Non-existent metric
	_, ok = mc.GetDomainMetric("domain.nonexistent")
	assert.False(t, ok)
}

func TestSelfHealing_ExponentialBackoff(t *testing.T) {
	config := SelfHealingConfig{
		MaxRetries:     3,
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     100 * time.Millisecond,
		BackoffFactor:  2.0,
	}
	sh := NewSelfHealing(config, nil)

	callCount := 0
	start := time.Now()

	err := sh.ExecuteWithHealing(context.Background(), func(ctx context.Context) error {
		callCount++
		if callCount < 3 {
			return errors.New("retry")
		}
		return nil
	})

	elapsed := time.Since(start)

	assert.NoError(t, err)
	assert.Equal(t, 3, callCount)
	// Should have waited ~10ms + ~20ms = ~30ms minimum
	assert.True(t, elapsed >= 25*time.Millisecond, "expected backoff delays")
}

func TestFeedbackReceiver(t *testing.T) {
	fb := NewSimpleFeedbackReceiver()

	ctx := context.Background()
	fb.RecordFeedback(ctx, "task-1", 0.7, FeedbackSourceHuman, "needs improvement")
	fb.RecordFeedback(ctx, "task-2", 0.9, FeedbackSourceAutomated, "")

	// Get specific feedback
	f1, ok := fb.GetFeedback("task-1")
	assert.True(t, ok)
	assert.Equal(t, 0.7, f1.Score)
	assert.Equal(t, FeedbackSourceHuman, f1.Source)
	assert.Equal(t, "needs improvement", f1.Comments)

	// Get average
	avg := fb.GetAverageScore()
	assert.InDelta(t, 0.8, avg, 0.001)

	// Clear
	fb.Clear()
	avg = fb.GetAverageScore()
	assert.Equal(t, 0.0, avg)
}
