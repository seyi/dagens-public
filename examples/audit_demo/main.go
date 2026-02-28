// package main
//
// Demonstrates agentic AI guardrails with full OTEL-to-audit correlation.
// Shows how LLM outputs are intercepted, evaluated, and logged with trace context.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/seyi/dagens/pkg/policy"
	"github.com/seyi/dagens/pkg/policy/audit"
	"github.com/seyi/dagens/pkg/policy/rules"
	"github.com/seyi/dagens/pkg/telemetry"
	"github.com/google/uuid"
)

// --- Mock AI Service ---

// MockLLM simulates a Large Language Model generation
type MockLLM struct{}

func (ai *MockLLM) Generate(ctx context.Context, prompt string) (string, error) {
	// Simulate "thinking" time
	time.Sleep(time.Duration(100+rand.Intn(200)) * time.Millisecond)

	// Deterministic responses based on keywords to demonstrate policy triggers
	lowerPrompt := strings.ToLower(prompt)

	if strings.Contains(lowerPrompt, "api key") || strings.Contains(lowerPrompt, "secret") {
		return "Sure, I can help with the integration. You should use the following configuration:\n\n" +
			"export API_ENDPOINT=https://api.example.com  \n" +
			"export API_KEY=sk-7da8s9d8a7sd98as7d98as7d98asd789\n\n" +
			"Make sure not to commit this file!", nil
	}

	if strings.Contains(lowerPrompt, "lab results") || strings.Contains(lowerPrompt, "contact") {
		return "I found the patient record.\n\n" +
			"Patient: John Doe\n" +
			"Diagnosis: Acute Bronchitis\n" +
			"Contact: john.doe@example.hospital.org / 555-019-8888\n\n" +
			"Sending results now.", nil
	}

	return "The influenza virus typically presents with symptoms such as high fever, " +
		"muscle aches, fatigue, and dry cough. It is recommended to stay hydrated and rest.", nil
}

// --- Main Demo ---

func main() {
	// Setup structured logging
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	
	fmt.Println("🚀 Dagens Agentic Guardrails Demo")
	fmt.Println("=================================")

	// 1. Initialize Telemetry
	if os.Getenv("OTEL_EXPORTER_TYPE") == "" {
		os.Setenv("OTEL_EXPORTER_TYPE", "console")
	}
	telemetry.InitGlobalTelemetry("medical-agent", "v1.0.0")
	tracer := telemetry.GetGlobalTelemetry().GetTracer()

	// 2. Setup Audit Logger
	connString := os.Getenv("DATABASE_URL")
	if connString == "" {
		connString = "postgres://postgres:postgres@localhost:5432/dagens?sslmode=disable"
	}
	
	config := audit.PostgresConfig{
		ConnectionString: connString,
		TableName:        "agent_audit_log",
		MigrateOnStart:   true,
		InsertTimeout:    2 * time.Second,
	}

	var auditLogger policy.AuditLogger
	pgLogger, err := audit.NewPostgresAuditLoggerFromURL(context.Background(), config, logger)
	if err != nil {
		logger.Warn("⚠️  DB Connection Failed (Using Console Fallback)", "error", err)
		auditLogger = &SimpleConsoleAuditLogger{logger: logger}
	} else {
		defer pgLogger.Close()
		auditLogger = pgLogger
		logger.Info("✅ Connected to Audit DB", "table", config.TableName)
	}

	// 3. Configure Guardrails (Policy Engine)
	engine := policy.NewEngine(auditLogger)
	engine.RegisterEvaluator(&rules.PIIEvaluator{})
	engine.RegisterEvaluator(&rules.RegexEvaluator{})

	medicalPolicy := &policy.PolicyConfig{
		FailOpen: false,
		Rules: []policy.Rule{
			{
				ID:       "sec-leak-prevention",
				Name:     "Block API Keys",
				Type:     "regex",
				Action:   policy.ActionBlock,
				Severity: policy.SeverityCritical,
				Enabled:  true,
				Config: map[string]interface{}{
					"pattern": `(sk-[a-zA-Z0-9]{32,})`,
				},
			},
			{
				ID:       "privacy-scrub",
				Name:     "Redact PII",
				Type:     "pii",
				Action:   policy.ActionRedact,
				Severity: policy.SeverityHigh,
				Enabled:  true,
				Config: map[string]interface{}{
					"types": []interface{}{"email", "phone_us"},
				},
			},
		},
	}

	// 4. Run Agent Loop
	llm := &MockLLM{}
	ctx := context.Background()

	// Scenario 1: Safe Query
	processRequest(ctx, tracer, llm, engine, medicalPolicy, "What are flu symptoms?")

	// Scenario 2: PII Leak (AI generates PII)
	processRequest(ctx, tracer, llm, engine, medicalPolicy, "Who is the patient with the lab results?")

	// Scenario 3: Security Leak (AI hallucinates credentials)
	processRequest(ctx, tracer, llm, engine, medicalPolicy, "How do I configure the API key integration?")

	// Ensure all traces are flushed to Jaeger before exiting
	telemetry.ForceFlushGlobalTelemetry(ctx)
	logger.Info("🎉 Demo Complete.")
}

// processRequest models the full lifecycle: User -> Agent -> LLM -> Guardrail -> User
func processRequest(ctx context.Context, tracer telemetry.Tracer, llm *MockLLM, engine *policy.Engine, policyCfg *policy.PolicyConfig, userPrompt string) {
	sessionID := uuid.New().String()
	
	// Start Root Span (The "Job")
	ctx, rootSpan := tracer.StartSpan(ctx, "agent.handle_request")
	defer rootSpan.End()
	rootSpan.SetAttribute("session.id", sessionID)
	rootSpan.SetAttribute("user.prompt", userPrompt)

	fmt.Printf("\n╭── 👤 User Request ──────────────────────────────────────────\n")
	fmt.Printf("│ Input: %q\n", userPrompt)
	fmt.Printf("│ TraceID: %s\n", rootSpan.TraceID())
	fmt.Printf("│\n")

	// Step 1: Agent calls LLM
	fmt.Printf("├── 🤖 AI Generation (Thinking...)\n")
	llmCtx, llmSpan := tracer.StartSpan(ctx, "llm.generate")
	rawResponse, _ := llm.Generate(llmCtx, userPrompt)
	llmSpan.SetAttribute("llm.raw_output", rawResponse)
	llmSpan.End()
	fmt.Printf("│   Raw Output: %q\n", shorten(rawResponse, 60))

	// Step 2: Guardrail Interception
	fmt.Printf("│\n")
	fmt.Printf("├── 🛡️  Guardrail Enforcement\n")
	
	policyCtx, policySpan := tracer.StartSpan(ctx, "policy.evaluate")
	defer policySpan.End() // Ensure span is closed even if evaluation fails
	
	policySpan.SetAttribute("policy.rules_count", len(policyCfg.Rules))

	metadata := policy.EvaluationMetadata{
		SessionID: sessionID,
		JobID:     rootSpan.SpanID(), // Root Span ID as Job ID
		NodeID:    "guardrail-layer",
		TraceID:   policySpan.TraceID(),
		SpanID:    policySpan.SpanID(),
	}

	// Actual Policy Check
	result, err := engine.Evaluate(policyCtx, policyCfg, rawResponse, metadata)

	if err != nil {
		fmt.Printf("│   ❌ Error executing policy: %v\n", err)
		return
	}

	// Step 3: Final Decision
	if !result.Allowed {
		rootSpan.SetStatus(telemetry.StatusError, "Blocked by Policy")
		fmt.Printf("│   ❌ STATUS: BLOCKED\n")
		fmt.Printf("│   Reason: %s\n", result.BlockReason)
		fmt.Printf("╰────────────────────────────────────────────────────────────\n")
	} else {
		rootSpan.SetStatus(telemetry.StatusOK, "Allowed")
		if result.FinalAction == policy.ActionRedact {
			fmt.Printf("│   ⚠️  STATUS: REDACTED\n")
			fmt.Printf("│   Modifications: %d PII entities masked\n", countRedactions(result.FinalOutput))
		} else {
			fmt.Printf("│   ✅ STATUS: PASSED\n")
		}
		fmt.Printf("│\n")
		fmt.Printf("╰── 📤 Final Response to User ───────────────────────────────\n")
		fmt.Printf("    %q\n", result.FinalOutput)
	}
}

// Helpers

func shorten(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

func countRedactions(s string) int {
	return strings.Count(s, "[REDACTED")
}

type SimpleConsoleAuditLogger struct {
	logger *slog.Logger
}

func (l *SimpleConsoleAuditLogger) Log(ctx context.Context, event *policy.AuditEvent) error {
	// NOW SHOW TRACE CORRELATION IN CONSOLE FALLBACK
	l.logger.Info("📝 Audit Event Recorded",
		"action", event.FinalAction,
		"trace_id", event.TraceID,
		"span_id", event.SpanID,
		"rules_matched", len(event.Results),
	)
	return nil
}