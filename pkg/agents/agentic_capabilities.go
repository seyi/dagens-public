// Package agents provides agent implementations with agentic capabilities.
//
// This file provides backward-compatible wrappers that delegate to the
// pkg/agentic package for self-monitoring, self-healing, and feedback.
package agents

import (
	"context"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/agentic"
)

// WithAgenticCapabilities wraps an existing agent with agentic capabilities.
// This is the primary entry point for adding self-monitoring and self-healing.
//
// Example usage:
//
//	baseAgent := agent.NewAgent(config)
//	agenticAgent := agents.WithAgenticCapabilities(baseAgent,
//	    agents.WithMaxRetries(5),
//	)
//
//	// During execution, record domain-specific metrics:
//	agenticAgent.RecordDomainMetric("domain.graduation_rate", 0.85)
//	agenticAgent.RecordOutcome(0.9)
func WithAgenticCapabilities(a agent.Agent, opts ...AgenticOption) *AgenticAgent {
	// Convert options to agentic package options
	agenticOpts := make([]agentic.AgenticOption, 0, len(opts))
	for _, opt := range opts {
		if converted := opt.toAgenticOption(); converted != nil {
			agenticOpts = append(agenticOpts, converted)
		}
	}

	return &AgenticAgent{
		inner: agentic.NewAgenticAgent(a, agenticOpts...),
	}
}

// AgenticAgent wraps an agent with self-monitoring and self-healing capabilities.
type AgenticAgent struct {
	inner *agentic.AgenticAgent
}

// Execute runs the agent with self-monitoring and self-healing.
func (a *AgenticAgent) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	return a.inner.Execute(ctx, input)
}

// RecordOutcome records the primary success metric (0.0-1.0).
// Call this after evaluating output quality.
func (a *AgenticAgent) RecordOutcome(score float64) {
	a.inner.RecordOutcome(score)
}

// RecordDomainMetric records a domain-specific metric.
// Use "domain." prefix for consistency: domain.graduation_rate, domain.wind_speed
func (a *AgenticAgent) RecordDomainMetric(name string, value float64) {
	a.inner.RecordDomainMetric(name, value)
}

// RecordFeedback records external feedback (human, automated, peer agent).
func (a *AgenticAgent) RecordFeedback(ctx context.Context, taskID string, score float64, source, comments string) {
	a.inner.RecordFeedback(ctx, taskID, score, source, comments)
}

// GetAverageFeedback returns the average feedback score.
func (a *AgenticAgent) GetAverageFeedback() float64 {
	return a.inner.GetAverageFeedback()
}

// Monitoring returns the monitoring context for advanced usage.
func (a *AgenticAgent) Monitoring() *agentic.MonitoringContext {
	return a.inner.Monitoring()
}

// =============================================================================
// DELEGATE METHODS TO INNER AGENT
// =============================================================================

// ID returns the agent's ID.
func (a *AgenticAgent) ID() string { return a.inner.ID() }

// Name returns the agent's name.
func (a *AgenticAgent) Name() string { return a.inner.Name() }

// Description returns the agent's description.
func (a *AgenticAgent) Description() string { return a.inner.Description() }

// Capabilities returns the agent's capabilities.
func (a *AgenticAgent) Capabilities() []string { return a.inner.Capabilities() }

// Dependencies returns the agent's dependencies.
func (a *AgenticAgent) Dependencies() []agent.Agent { return a.inner.Dependencies() }

// Partition returns the agent's partition.
func (a *AgenticAgent) Partition() string { return a.inner.Partition() }

// =============================================================================
// OPTIONS
// =============================================================================

// AgenticOption configures an AgenticAgent.
type AgenticOption interface {
	toAgenticOption() agentic.AgenticOption
}

type maxRetriesOption struct {
	n int
}

func (o maxRetriesOption) toAgenticOption() agentic.AgenticOption {
	return agentic.WithMaxRetries(o.n)
}

// WithMaxRetries sets the maximum number of retries for self-healing.
func WithMaxRetries(n int) AgenticOption {
	return maxRetriesOption{n: n}
}

type healingOption struct {
	config agentic.SelfHealingConfig
}

func (o healingOption) toAgenticOption() agentic.AgenticOption {
	return agentic.WithHealing(o.config)
}

// WithHealing sets a custom self-healing configuration.
func WithHealing(maxRetries int, initialBackoffMs int) AgenticOption {
	config := agentic.DefaultSelfHealingConfig()
	config.MaxRetries = maxRetries
	return healingOption{config: config}
}

type feedbackOption struct {
	fb agentic.FeedbackReceiver
}

func (o feedbackOption) toAgenticOption() agentic.AgenticOption {
	return agentic.WithFeedback(o.fb)
}

// WithFeedback sets a custom feedback receiver.
func WithFeedback(fb agentic.FeedbackReceiver) AgenticOption {
	return feedbackOption{fb: fb}
}

// =============================================================================
// CONVENIENCE FUNCTIONS FOR DOMAIN METRICS
// =============================================================================

// Semantic convention helpers for common agent types.
// These help ensure consistent metric naming across different agents.

// Soccer Trainer domain metrics
const (
	DomainGraduationRate = "domain.graduation_rate"
	DomainPlayerMorale   = "domain.player_morale"
	DomainMethodology    = "domain.methodology"
)

// Art Replicator domain metrics
const (
	DomainStyleSimilarity = "domain.style_similarity"
	DomainCriticRating    = "domain.critic_rating"
	DomainIterations      = "domain.iterations"
)

// Sniper Agent domain metrics
const (
	DomainDistanceError      = "domain.distance_error"
	DomainWindSpeed          = "domain.wind_speed"
	DomainCorrectionAccuracy = "domain.correction_accuracy"
)

// LLM Agent domain metrics
const (
	DomainTokensPrompt      = "domain.tokens.prompt"
	DomainTokensCompletion  = "domain.tokens.completion"
	DomainConfidence        = "domain.confidence"
	DomainHallucinationRisk = "domain.hallucination_risk"
)
