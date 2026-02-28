package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/seyi/dagens/pkg/policy"
	"github.com/seyi/dagens/pkg/telemetry"
)

// FileAuditLogger writes audit events to a JSON-lines file
type FileAuditLogger struct {
	file *os.File
	mu   sync.Mutex
}

// NewFileAuditLogger creates a new file-based audit logger
func NewFileAuditLogger(path string) (*FileAuditLogger, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return &FileAuditLogger{file: f}, nil
}

// Log writes an audit event to the file
func (l *FileAuditLogger) Log(ctx context.Context, event *policy.AuditEvent) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	data, err := json.Marshal(event)
	if err != nil {
		return err
	}

	_, err = l.file.Write(append(data, '\n'))
	if err != nil {
		return err
	}

	log.Printf("[AUDIT] Policy decision: action=%s rules_triggered=%d session=%s",
		event.FinalAction, len(event.Results), event.SessionID)

	return nil
}

// Close closes the log file
func (l *FileAuditLogger) Close() error {
	return l.file.Close()
}

// StdoutAuditLogger logs audit events to stdout (for development)
type StdoutAuditLogger struct {
	Verbose bool
}

// NewStdoutAuditLogger creates a new stdout-based audit logger
func NewStdoutAuditLogger(verbose bool) *StdoutAuditLogger {
	return &StdoutAuditLogger{Verbose: verbose}
}

// Log writes an audit event to stdout
func (l *StdoutAuditLogger) Log(ctx context.Context, event *policy.AuditEvent) error {
	if l.Verbose {
		data, _ := json.MarshalIndent(event, "", "  ")
		log.Printf("[AUDIT] %s", string(data))
	} else {
		log.Printf("[AUDIT] Policy decision: action=%s rules_triggered=%d session=%s job=%s",
			event.FinalAction, len(event.Results), event.SessionID, event.JobID)
	}
	return nil
}

// NoOpAuditLogger does nothing (for testing or when audit is disabled)
type NoOpAuditLogger struct{}

// Log does nothing
func (l *NoOpAuditLogger) Log(ctx context.Context, event *policy.AuditEvent) error {
	return nil
}

// OTELAuditLogger sends audit events as span events to the distributed tracing system.
// This centralizes audit logs in Jaeger/Prometheus instead of fragmenting them across
// container filesystems in a distributed cluster.
//
// IMPORTANT: For events to appear in Jaeger, ensure OTEL is configured:
//   - Set OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4317 (or your Jaeger OTLP endpoint)
//   - Or set OTEL_EXPORTER_TYPE=otlp with the endpoint
//
// When properly configured, span.AddEvent() sends data to Jaeger, not just console.
type OTELAuditLogger struct {
	tracer  telemetry.Tracer
	verbose bool
}

// NewOTELAuditLogger creates a new OTEL-based audit logger.
// The tracer should be obtained from telemetry.GetGlobalTelemetry().GetTracer()
func NewOTELAuditLogger(tracer telemetry.Tracer, verbose bool) *OTELAuditLogger {
	return &OTELAuditLogger{
		tracer:  tracer,
		verbose: verbose,
	}
}

// Log sends an audit event as a span event to the distributed tracing system.
// The event is attached to the current span and will be exported to Jaeger
// when the span ends (assuming OTEL is configured with an OTLP exporter).
func (l *OTELAuditLogger) Log(ctx context.Context, event *policy.AuditEvent) error {
	span := l.tracer.GetSpan(ctx)
	if span == nil {
		// No active span - this happens if OTEL isn't configured or no parent span exists
		log.Printf("[AUDIT] No active span for policy audit: action=%s session=%s job=%s",
			event.FinalAction, event.SessionID, event.JobID)
		return nil
	}

	// Build event attributes for Jaeger
	attrs := map[string]interface{}{
		"audit.id":          event.ID,
		"audit.session_id":  event.SessionID,
		"audit.job_id":      event.JobID,
		"audit.node_id":     event.NodeID,
		"audit.action":      string(event.FinalAction),
		"audit.rules_count": len(event.Results),
		"audit.duration_ms": event.DurationMs,
		"audit.input_hash":  event.InputHash,
		"audit.output_hash": event.OutputHash,
	}

	// Add rule details for violations (always) or all rules (verbose mode)
	if l.verbose || event.FinalAction == policy.ActionBlock || event.FinalAction == policy.ActionRedact {
		for i, result := range event.Results {
			prefix := fmt.Sprintf("audit.result.%d.", i)
			attrs[prefix+"rule_id"] = result.RuleID
			attrs[prefix+"action"] = string(result.Action)
			attrs[prefix+"severity"] = string(result.Severity)
			attrs[prefix+"matched"] = result.Matched
			if result.Reason != "" {
				attrs[prefix+"reason"] = result.Reason
			}
		}
	}

	// Determine event name based on action - these show up as events in Jaeger UI
	eventName := "policy_audit"
	if event.FinalAction == policy.ActionBlock {
		eventName = "policy_violation" // High visibility in Jaeger
	} else if event.FinalAction == policy.ActionRedact {
		eventName = "policy_redaction"
	}

	// Add event to span - THIS IS WHAT GOES TO JAEGER
	span.AddEvent(eventName, attrs)

	// Set span attributes for Jaeger filtering/search
	span.SetAttribute("policy.action", string(event.FinalAction))
	span.SetAttribute("policy.rules_triggered", len(event.Results))
	if event.FinalAction == policy.ActionBlock {
		span.SetAttribute("policy.blocked", true)
	}

	return nil
}

// CompositeAuditLogger logs to multiple backends simultaneously
type CompositeAuditLogger struct {
	loggers []policy.AuditLogger
}

// NewCompositeAuditLogger creates a logger that writes to multiple backends
func NewCompositeAuditLogger(loggers ...policy.AuditLogger) *CompositeAuditLogger {
	return &CompositeAuditLogger{loggers: loggers}
}

// Log writes the event to all configured loggers
func (l *CompositeAuditLogger) Log(ctx context.Context, event *policy.AuditEvent) error {
	var lastErr error
	for _, logger := range l.loggers {
		if err := logger.Log(ctx, event); err != nil {
			log.Printf("[AUDIT] Logger error: %v", err)
			lastErr = err
		}
	}
	return lastErr
}