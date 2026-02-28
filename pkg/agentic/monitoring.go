package agentic

import (
	"context"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/telemetry"
)

// MonitoringContext provides telemetry capabilities to agents.
// It wraps the telemetry package and provides convenient methods
// for recording metrics using semantic conventions.
type MonitoringContext struct {
	meter   telemetry.Meter
	tracer  telemetry.Tracer
	logger  telemetry.Logger
	agentID string
	agentName string
	agentType string

	// Domain metrics storage for agents that need to track custom values
	mu            sync.RWMutex
	domainMetrics map[string]float64
}

// NewMonitoringContext creates a monitoring context for an agent.
func NewMonitoringContext(agentID, agentName, agentType string, collector *telemetry.TelemetryCollector) *MonitoringContext {
	return &MonitoringContext{
		meter:         collector.GetMeter(),
		tracer:        collector.GetTracer(),
		logger:        collector.GetLogger(),
		agentID:       agentID,
		agentName:     agentName,
		agentType:     agentType,
		domainMetrics: make(map[string]float64),
	}
}

// NewMonitoringContextWithDefaults creates a monitoring context with a default collector.
func NewMonitoringContextWithDefaults(agentID, agentName, agentType string) *MonitoringContext {
	collector := telemetry.NewTelemetryCollector()
	return NewMonitoringContext(agentID, agentName, agentType, collector)
}

// =============================================================================
// UNIVERSAL DIMENSION METHODS
// =============================================================================

// RecordOutcome records the primary success metric (0.0-1.0 normalized).
func (mc *MonitoringContext) RecordOutcome(score float64) {
	mc.meter.Gauge(MetricOutcomeScore).Set(score)
	mc.logMetric("outcome", MetricOutcomeScore, score)
}

// RecordExecutionDuration records how long an operation took.
func (mc *MonitoringContext) RecordExecutionDuration(duration time.Duration) {
	mc.meter.Histogram(MetricExecutionDuration).Record(duration.Seconds())
}

// RecordExecutionCount increments the execution counter.
func (mc *MonitoringContext) RecordExecutionCount() {
	mc.meter.Counter(MetricExecutionCount).Inc()
}

// RecordConsumption records resource consumption (tokens, cost, time units).
func (mc *MonitoringContext) RecordConsumption(cost float64) {
	mc.meter.Counter(MetricConsumptionCost).Add(cost)
}

// RecordHealth records agent health status.
func (mc *MonitoringContext) RecordHealth(status float64) {
	mc.meter.Gauge(MetricHealthStatus).Set(status)
}

// RecordEnvironment records external dependency readiness (0.0-1.0).
func (mc *MonitoringContext) RecordEnvironment(ready float64) {
	mc.meter.Gauge(MetricEnvironmentReady).Set(ready)
}

// =============================================================================
// DOMAIN-SPECIFIC METHODS (for custom agent metrics)
// =============================================================================

// RecordDomainGauge records a domain-specific gauge value.
// Use the "domain." prefix for consistency: domain.graduation_rate, domain.wind_speed
func (mc *MonitoringContext) RecordDomainGauge(name string, value float64) {
	mc.meter.Gauge(name).Set(value)
	mc.mu.Lock()
	mc.domainMetrics[name] = value
	mc.mu.Unlock()
}

// RecordDomainCounter increments a domain-specific counter.
func (mc *MonitoringContext) RecordDomainCounter(name string, delta float64) {
	mc.meter.Counter(name).Add(delta)
}

// RecordDomainHistogram records a value in a domain-specific histogram.
func (mc *MonitoringContext) RecordDomainHistogram(name string, value float64) {
	mc.meter.Histogram(name).Record(value)
}

// GetDomainMetric retrieves a stored domain metric value.
func (mc *MonitoringContext) GetDomainMetric(name string) (float64, bool) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	v, ok := mc.domainMetrics[name]
	return v, ok
}

// =============================================================================
// TRACING METHODS
// =============================================================================

// StartSpan starts a new trace span for an operation.
func (mc *MonitoringContext) StartSpan(ctx context.Context, operation string) (context.Context, telemetry.Span) {
	ctx, span := mc.tracer.StartSpan(ctx, operation)
	span.SetAttribute(AttrAgentID, mc.agentID)
	span.SetAttribute(AttrAgentName, mc.agentName)
	span.SetAttribute(AttrAgentType, mc.agentType)
	return ctx, span
}

// =============================================================================
// LOGGING METHODS
// =============================================================================

// LogInfo logs an informational message with agent context.
func (mc *MonitoringContext) LogInfo(msg string, attrs map[string]interface{}) {
	mc.logger.Info(msg, mc.withAgentAttrs(attrs))
}

// LogWarn logs a warning message with agent context.
func (mc *MonitoringContext) LogWarn(msg string, attrs map[string]interface{}) {
	mc.logger.Warn(msg, mc.withAgentAttrs(attrs))
}

// LogError logs an error message with agent context.
func (mc *MonitoringContext) LogError(msg string, attrs map[string]interface{}) {
	mc.logger.Error(msg, mc.withAgentAttrs(attrs))
}

// LogDebug logs a debug message with agent context.
func (mc *MonitoringContext) LogDebug(msg string, attrs map[string]interface{}) {
	mc.logger.Debug(msg, mc.withAgentAttrs(attrs))
}

func (mc *MonitoringContext) withAgentAttrs(attrs map[string]interface{}) map[string]interface{} {
	result := map[string]interface{}{
		AttrAgentID:   mc.agentID,
		AttrAgentName: mc.agentName,
		AttrAgentType: mc.agentType,
	}
	for k, v := range attrs {
		result[k] = v
	}
	return result
}

func (mc *MonitoringContext) logMetric(dimension, metric string, value float64) {
	mc.logger.Debug("metric recorded", map[string]interface{}{
		"dimension": dimension,
		"metric":    metric,
		"value":     value,
	})
}

// =============================================================================
// ACCESSORS
// =============================================================================

// AgentID returns the agent's ID.
func (mc *MonitoringContext) AgentID() string { return mc.agentID }

// AgentName returns the agent's name.
func (mc *MonitoringContext) AgentName() string { return mc.agentName }

// AgentType returns the agent's type.
func (mc *MonitoringContext) AgentType() string { return mc.agentType }

// Meter returns the underlying meter for advanced usage.
func (mc *MonitoringContext) Meter() telemetry.Meter { return mc.meter }

// Tracer returns the underlying tracer for advanced usage.
func (mc *MonitoringContext) Tracer() telemetry.Tracer { return mc.tracer }

// Logger returns the underlying logger for advanced usage.
func (mc *MonitoringContext) Logger() telemetry.Logger { return mc.logger }
