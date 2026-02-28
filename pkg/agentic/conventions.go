// Package agentic provides self-monitoring capabilities for agents using
// semantic conventions and direct telemetry integration.
//
// This package follows the principle of being UNOPINIONATED about domain logic.
// The framework provides transport (telemetry hooks), agents provide meaning (domain metrics).
package agentic

// Semantic Conventions for Agent Monitoring
//
// These are DOCUMENTED conventions, not enforced in code.
// All agents should emit metrics using these namespaces for consistency
// in dashboards and observability tools.
//
// Universal Dimensions (all agents should emit these):
//
//	dagens.outcome.score       - Primary success metric (0.0-1.0 normalized)
//	dagens.execution.duration  - How long the operation took (seconds)
//	dagens.execution.count     - Number of operations completed
//	dagens.consumption.cost    - Resource cost (tokens, $, time units)
//	dagens.health.status       - Agent health (1=ok, 0.5=degraded, 0=failing)
//	dagens.environment.ready   - External dependencies ready (0.0-1.0)
//
// Domain-Specific (agents define their own):
//
//	domain.*                   - Any agent-specific metrics
//
// Examples by Agent Type:
//
//	LLM Agent:
//	  domain.tokens.prompt      - Prompt tokens used
//	  domain.tokens.completion  - Completion tokens used
//	  domain.confidence         - Model confidence score
//	  domain.hallucination_risk - Estimated hallucination probability
//
//	Soccer Trainer Agent:
//	  domain.graduation_rate    - Players graduating to first team
//	  domain.player_morale      - Average player morale (0-100)
//	  domain.methodology        - Current training methodology (label)
//
//	Art Replicator Agent:
//	  domain.style_similarity   - Similarity to target style (0.0-1.0)
//	  domain.critic_rating      - Human critic rating (1-5 stars)
//	  domain.iterations         - Learning iterations completed
//
//	Sniper Agent:
//	  domain.distance_error     - Distance from bullseye (meters)
//	  domain.wind_speed         - Current wind speed (m/s)
//	  domain.correction_accuracy - Accuracy of aim adjustments (0.0-1.0)

// Metric name constants for universal dimensions
const (
	// Outcome dimension - Was the output good?
	MetricOutcomeScore = "dagens.outcome.score"

	// Execution dimension - Did I do the work?
	MetricExecutionDuration = "dagens.execution.duration"
	MetricExecutionCount    = "dagens.execution.count"

	// Consumption dimension - What resources did I use?
	MetricConsumptionCost = "dagens.consumption.cost"

	// Health dimension - Am I functioning well?
	MetricHealthStatus = "dagens.health.status"

	// Environment dimension - What external factors affect me?
	MetricEnvironmentReady = "dagens.environment.ready"
)

// Attribute keys for common labels
const (
	AttrAgentID   = "agent.id"
	AttrAgentName = "agent.name"
	AttrAgentType = "agent.type"
	AttrTaskID    = "task.id"
)

// HealthStatus values
const (
	HealthOK       = 1.0
	HealthDegraded = 0.5
	HealthFailing  = 0.0
)
