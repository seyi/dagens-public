package agents

import (
	"context"
	"errors"
	"testing"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/agentic"
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

func (m *mockAgent) ID() string            { return m.id }
func (m *mockAgent) Name() string          { return m.name }
func (m *mockAgent) Description() string   { return "test agent" }
func (m *mockAgent) Capabilities() []string { return []string{"test"} }
func (m *mockAgent) Dependencies() []agent.Agent { return nil }
func (m *mockAgent) Partition() string     { return "default" }

func (m *mockAgent) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	m.currentCall++
	if m.currentCall <= m.failCount {
		return nil, errors.New("simulated failure")
	}
	return &agent.AgentOutput{
		Result: "success: " + input.Instruction,
	}, nil
}

func TestWithAgenticCapabilities_Success(t *testing.T) {
	mock := newMockAgent("test-1", "Test Agent", 0)
	aa := WithAgenticCapabilities(mock)

	ctx := context.Background()
	input := &agent.AgentInput{
		TaskID:      "task-1",
		Instruction: "test input",
	}

	result, err := aa.Execute(ctx, input)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "success: test input", result.Result)
}

func TestWithAgenticCapabilities_SelfHealing(t *testing.T) {
	// Agent fails twice, then succeeds
	mock := newMockAgent("test-2", "Retry Agent", 2)
	aa := WithAgenticCapabilities(mock, WithMaxRetries(3))

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

func TestWithAgenticCapabilities_MaxRetriesExceeded(t *testing.T) {
	// Agent always fails
	mock := newMockAgent("test-3", "Fail Agent", 100)
	aa := WithAgenticCapabilities(mock, WithMaxRetries(2))

	ctx := context.Background()
	input := &agent.AgentInput{
		TaskID:      "task-3",
		Instruction: "fail test",
	}

	result, err := aa.Execute(ctx, input)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "max retries")
}

func TestWithAgenticCapabilities_DomainMetrics(t *testing.T) {
	mock := newMockAgent("test-4", "Soccer Trainer", 0)
	aa := WithAgenticCapabilities(mock)

	// Record domain-specific metrics
	aa.RecordDomainMetric(DomainGraduationRate, 0.85)
	aa.RecordDomainMetric(DomainPlayerMorale, 92)

	// Verify via monitoring context
	mc := aa.Monitoring()
	grad, ok := mc.GetDomainMetric(DomainGraduationRate)
	assert.True(t, ok)
	assert.Equal(t, 0.85, grad)
}

func TestWithAgenticCapabilities_Feedback(t *testing.T) {
	mock := newMockAgent("test-5", "Art Agent", 0)
	aa := WithAgenticCapabilities(mock)

	ctx := context.Background()

	// Record human feedback
	aa.RecordFeedback(ctx, "painting-1", 0.8, agentic.FeedbackSourceHuman, "good style")
	aa.RecordFeedback(ctx, "painting-2", 0.9, agentic.FeedbackSourceHuman, "excellent")

	// Check average
	avg := aa.GetAverageFeedback()
	assert.InDelta(t, 0.85, avg, 0.001)
}

func TestWithAgenticCapabilities_SniperAgent(t *testing.T) {
	mock := newMockAgent("sniper-1", "Sniper Agent", 0)
	aa := WithAgenticCapabilities(mock)

	// Record sniper domain metrics
	aa.RecordDomainMetric(DomainDistanceError, 0.05)     // 5cm from bullseye
	aa.RecordDomainMetric(DomainWindSpeed, 12.5)         // 12.5 m/s wind
	aa.RecordDomainMetric(DomainCorrectionAccuracy, 0.92) // 92% correction accuracy

	// Verify
	mc := aa.Monitoring()
	dist, ok := mc.GetDomainMetric(DomainDistanceError)
	assert.True(t, ok)
	assert.Equal(t, 0.05, dist)

	wind, ok := mc.GetDomainMetric(DomainWindSpeed)
	assert.True(t, ok)
	assert.Equal(t, 12.5, wind)
}

func TestWithAgenticCapabilities_LLMAgent(t *testing.T) {
	mock := newMockAgent("llm-1", "LLM Agent", 0)
	aa := WithAgenticCapabilities(mock)

	// Record LLM domain metrics
	aa.RecordDomainMetric(DomainTokensPrompt, 1024)
	aa.RecordDomainMetric(DomainTokensCompletion, 256)
	aa.RecordDomainMetric(DomainConfidence, 0.87)
	aa.RecordDomainMetric(DomainHallucinationRisk, 0.12)

	// Record outcome
	aa.RecordOutcome(0.87)

	// Verify
	mc := aa.Monitoring()
	conf, ok := mc.GetDomainMetric(DomainConfidence)
	assert.True(t, ok)
	assert.Equal(t, 0.87, conf)
}
