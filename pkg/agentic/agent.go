package agentic

import (
	"context"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/telemetry"
)

// AgenticAgent wraps an existing agent with self-monitoring and self-healing capabilities.
// It is unopinionated about domain logic - agents define their own metrics via the
// MonitoringContext's domain methods.
type AgenticAgent struct {
	original   agent.Agent
	monitoring *MonitoringContext
	healing    *SelfHealing
	feedback   FeedbackReceiver
}

// AgenticOption configures an AgenticAgent.
type AgenticOption func(*AgenticAgent)

// WithMonitoring sets a custom monitoring context.
func WithMonitoring(mc *MonitoringContext) AgenticOption {
	return func(a *AgenticAgent) {
		a.monitoring = mc
	}
}

// WithHealing sets a custom self-healing configuration.
func WithHealing(config SelfHealingConfig) AgenticOption {
	return func(a *AgenticAgent) {
		a.healing = NewSelfHealing(config, a.monitoring)
	}
}

// WithFeedback sets a feedback receiver for external input.
func WithFeedback(fb FeedbackReceiver) AgenticOption {
	return func(a *AgenticAgent) {
		a.feedback = fb
	}
}

// WithMaxRetries sets the maximum number of retries for self-healing.
func WithMaxRetries(n int) AgenticOption {
	return func(a *AgenticAgent) {
		config := DefaultSelfHealingConfig()
		config.MaxRetries = n
		a.healing = NewSelfHealing(config, a.monitoring)
	}
}

// NewAgenticAgent wraps an existing agent with agentic capabilities.
func NewAgenticAgent(original agent.Agent, opts ...AgenticOption) *AgenticAgent {
	// Create default monitoring context
	collector := telemetry.NewTelemetryCollector()
	monitoring := NewMonitoringContext(
		original.ID(),
		original.Name(),
		"agentic",
		collector,
	)

	aa := &AgenticAgent{
		original:   original,
		monitoring: monitoring,
		healing:    NewSelfHealing(DefaultSelfHealingConfig(), monitoring),
		feedback:   NewSimpleFeedbackReceiver(),
	}

	// Apply options
	for _, opt := range opts {
		opt(aa)
	}

	// Ensure healing has monitoring reference
	if aa.healing != nil && aa.healing.monitoring == nil {
		aa.healing.monitoring = aa.monitoring
	}

	return aa
}

// Execute runs the agent with self-monitoring and self-healing.
func (a *AgenticAgent) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	startTime := time.Now()

	// Start trace span
	ctx, span := a.monitoring.StartSpan(ctx, "agent.execute")
	defer span.End()

	// Record execution start
	a.monitoring.RecordExecutionCount()
	span.SetAttribute("task.id", input.TaskID)

	// Execute with self-healing
	var result *agent.AgentOutput
	err := a.healing.ExecuteWithHealing(ctx, func(ctx context.Context) error {
		var execErr error
		result, execErr = a.original.Execute(ctx, input)
		return execErr
	})

	// Record execution duration
	duration := time.Since(startTime)
	a.monitoring.RecordExecutionDuration(duration)

	if err != nil {
		span.SetStatus(telemetry.StatusError, err.Error())
		a.monitoring.LogError("agent execution failed", map[string]interface{}{
			"error":    err.Error(),
			"duration": duration.Seconds(),
			"retries":  a.healing.CurrentRetry(),
		})
		return nil, err
	}

	// Record success
	span.SetStatus(telemetry.StatusOK, "success")
	a.monitoring.RecordHealth(HealthOK)

	// If result has metrics, record consumption
	if result != nil && result.Metrics != nil {
		if result.Metrics.TokensUsed > 0 {
			a.monitoring.RecordDomainCounter("domain.tokens.total", float64(result.Metrics.TokensUsed))
		}
	}

	return result, nil
}

// RecordOutcome allows the agent to record its outcome score.
// This should be called by the agent after evaluating its output quality.
func (a *AgenticAgent) RecordOutcome(score float64) {
	a.monitoring.RecordOutcome(score)
}

// RecordDomainMetric allows the agent to record domain-specific metrics.
func (a *AgenticAgent) RecordDomainMetric(name string, value float64) {
	a.monitoring.RecordDomainGauge(name, value)
}

// RecordFeedback records external feedback for the current task.
func (a *AgenticAgent) RecordFeedback(ctx context.Context, taskID string, score float64, source, comments string) {
	a.feedback.RecordFeedback(ctx, taskID, score, source, comments)
	// Also record as a domain metric for observability
	a.monitoring.RecordDomainGauge("domain.feedback.score", score)
}

// GetAverageFeedback returns the average feedback score.
func (a *AgenticAgent) GetAverageFeedback() float64 {
	return a.feedback.GetAverageScore()
}

// Monitoring returns the monitoring context for advanced usage.
func (a *AgenticAgent) Monitoring() *MonitoringContext {
	return a.monitoring
}

// Feedback returns the feedback receiver.
func (a *AgenticAgent) Feedback() FeedbackReceiver {
	return a.feedback
}

// =============================================================================
// DELEGATE METHODS TO ORIGINAL AGENT
// =============================================================================

// ID returns the agent's ID.
func (a *AgenticAgent) ID() string { return a.original.ID() }

// Name returns the agent's name.
func (a *AgenticAgent) Name() string { return a.original.Name() }

// Description returns the agent's description.
func (a *AgenticAgent) Description() string { return a.original.Description() }

// Capabilities returns the agent's capabilities.
func (a *AgenticAgent) Capabilities() []string { return a.original.Capabilities() }

// Dependencies returns the agent's dependencies.
func (a *AgenticAgent) Dependencies() []agent.Agent { return a.original.Dependencies() }

// Partition returns the agent's partition.
func (a *AgenticAgent) Partition() string { return a.original.Partition() }
