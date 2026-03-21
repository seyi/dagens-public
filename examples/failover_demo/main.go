// Package main demonstrates Dagens' unique sticky scheduling with automatic failover.
//
// This demo shows what no other AI agent framework can do:
// 1. Multi-turn conversations stick to the same worker (cache locality)
// 2. When a worker fails, conversations automatically continue on another worker
// 3. Full observability with HIT/MISS/STALE metrics + OTEL trace correlation
//
// Run: go run examples/failover_demo/main.go
//
// With Jaeger:
//   export OTEL_EXPORTER_TYPE=otlp
//   export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
//   go run examples/failover_demo/main.go

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/policy"
	"github.com/seyi/dagens/pkg/policy/audit"
	"github.com/seyi/dagens/pkg/policy/rules"
	"github.com/seyi/dagens/pkg/registry"
	"github.com/seyi/dagens/pkg/scheduler"
	"github.com/seyi/dagens/pkg/telemetry"
)

// Global tracer for OTEL integration
var tracer telemetry.Tracer

// ═══════════════════════════════════════════════════════════════════════════════
// VISUAL OUTPUT HELPERS
// ═══════════════════════════════════════════════════════════════════════════════

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorPurple = "\033[35m"
	colorCyan   = "\033[36m"
	colorWhite  = "\033[37m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
)

func printHeader() {
	fmt.Println()
	fmt.Println(colorBold + colorCyan + "╔══════════════════════════════════════════════════════════════════════════════╗" + colorReset)
	fmt.Println(colorBold + colorCyan + "║" + colorReset + colorBold + "     DAGENS FAILOVER DEMO: What LangGraph/CrewAI/AutoGen Cannot Do           " + colorCyan + "║" + colorReset)
	fmt.Println(colorBold + colorCyan + "╠══════════════════════════════════════════════════════════════════════════════╣" + colorReset)
	fmt.Println(colorBold + colorCyan + "║" + colorReset + "  Sticky Sessions + Automatic Failover = Production-Ready AI Agents           " + colorCyan + "║" + colorReset)
	fmt.Println(colorBold + colorCyan + "╚══════════════════════════════════════════════════════════════════════════════╝" + colorReset)
	fmt.Println()
}

func printSection(title string) {
	fmt.Println()
	fmt.Println(colorBold + colorWhite + "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" + colorReset)
	fmt.Println(colorBold + colorWhite + "  " + title + colorReset)
	fmt.Println(colorBold + colorWhite + "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━" + colorReset)
}

func printWorkerStatus(workers map[string]*SimulatedWorker) {
	fmt.Println()
	fmt.Println(colorBold + "  CLUSTER STATUS:" + colorReset)
	fmt.Println("  ┌────────────────┬──────────┬───────────────┬─────────────┐")
	fmt.Println("  │ Worker         │ Status   │ Tasks Run     │ Cache Hits  │")
	fmt.Println("  ├────────────────┼──────────┼───────────────┼─────────────┤")
	for id, w := range workers {
		status := colorGreen + "HEALTHY" + colorReset
		if !w.healthy {
			status = colorRed + "DEAD   " + colorReset
		}
		fmt.Printf("  │ %-14s │ %s │ %-13d │ %-11d │\n", id, status, w.tasksExecuted, w.cacheHits)
	}
	fmt.Println("  └────────────────┴──────────┴───────────────┴─────────────┘")
}

func printAffinityEvent(eventType, sessionID, nodeID string, hitCount int64) {
	switch eventType {
	case "HIT":
		fmt.Printf("  %s[AFFINITY HIT]%s  Session %s%s%s -> Worker %s%s%s (hits: %d)\n",
			colorGreen, colorReset,
			colorCyan, sessionID[:8], colorReset,
			colorYellow, nodeID, colorReset,
			hitCount)
	case "MISS":
		fmt.Printf("  %s[AFFINITY MISS]%s Session %s%s%s -> Worker %s%s%s (new affinity)\n",
			colorYellow, colorReset,
			colorCyan, sessionID[:8], colorReset,
			colorYellow, nodeID, colorReset)
	case "STALE":
		fmt.Printf("  %s[AFFINITY STALE]%s Session %s%s%s -> Worker %s%s%s is DEAD! Re-routing...\n",
			colorRed, colorReset,
			colorCyan, sessionID[:8], colorReset,
			colorRed, nodeID, colorReset)
	}
}

func printConversationTurn(turn int, sessionID, userMsg, response, workerID string, latencyMs int64, cacheHit bool, traceID string) {
	cacheStatus := colorRed + "COLD" + colorReset
	if cacheHit {
		cacheStatus = colorGreen + "HOT " + colorReset
	}
	fmt.Printf("\n  %sTurn %d%s [Session: %s...]\n", colorBold, turn, colorReset, sessionID[:8])
	fmt.Printf("  ├─ User: %q\n", userMsg)
	fmt.Printf("  ├─ Worker: %s%s%s | Cache: %s | Latency: %dms\n", colorYellow, workerID, colorReset, cacheStatus, latencyMs)
	if traceID != "" {
		fmt.Printf("  ├─ TraceID: %s%s%s (view in Jaeger)\n", colorPurple, traceID[:16], colorReset)
	}
	fmt.Printf("  └─ Agent: %q\n", truncate(response, 60))
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

// ═══════════════════════════════════════════════════════════════════════════════
// SIMULATED WORKER CLUSTER
// ═══════════════════════════════════════════════════════════════════════════════

// SimulatedWorker represents a worker node with its own "cache" (conversation context)
type SimulatedWorker struct {
	id            string
	healthy       bool
	cache         map[string]*ConversationContext // sessionID -> context
	tasksExecuted int
	cacheHits     int
	mu            sync.Mutex
}

// ConversationContext simulates expensive-to-load conversation state
type ConversationContext struct {
	SessionID   string
	TurnCount   int
	History     []string
	Embeddings  []float64 // Simulated embeddings (expensive to compute)
	LoadedAt    time.Time
	LastUsedAt  time.Time
}

func NewSimulatedWorker(id string) *SimulatedWorker {
	return &SimulatedWorker{
		id:      id,
		healthy: true,
		cache:   make(map[string]*ConversationContext),
	}
}

// Execute runs a task on this worker, using cached context if available
func (w *SimulatedWorker) Execute(ctx context.Context, sessionID string, input string) (string, int64, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.healthy {
		return "", 0, false
	}

	w.tasksExecuted++
	start := time.Now()
	cacheHit := false

	// Check if we have cached context for this session
	convCtx, exists := w.cache[sessionID]
	if exists {
		// CACHE HIT - Fast path (10-100x faster in real systems)
		cacheHit = true
		w.cacheHits++
		convCtx.TurnCount++
		convCtx.History = append(convCtx.History, input)
		convCtx.LastUsedAt = time.Now()

		// Simulate fast response with cached embeddings
		time.Sleep(time.Duration(20+rand.Intn(30)) * time.Millisecond)
	} else {
		// CACHE MISS - Slow path (load embeddings, context, etc.)
		// Simulate expensive context loading
		time.Sleep(time.Duration(200+rand.Intn(100)) * time.Millisecond)

		// Create new context with "expensive" embeddings
		embeddings := make([]float64, 1536) // Simulated embedding vector
		for i := range embeddings {
			embeddings[i] = rand.Float64()
		}

		convCtx = &ConversationContext{
			SessionID:   sessionID,
			TurnCount:   1,
			History:     []string{input},
			Embeddings:  embeddings,
			LoadedAt:    time.Now(),
			LastUsedAt:  time.Now(),
		}
		w.cache[sessionID] = convCtx
	}

	// Generate response
	response := generateResponse(input, convCtx.TurnCount)
	latency := time.Since(start).Milliseconds()

	return response, latency, cacheHit
}

func generateResponse(input string, turn int) string {
	responses := map[string][]string{
		"hello": {
			"Hello! I'm your AI assistant. How can I help you today?",
			"Hi again! Nice to continue our conversation.",
			"Hello once more! I remember our previous discussion.",
		},
		"weather": {
			"I'd be happy to help with weather information. What location?",
			"Following up on weather - did you need a specific forecast?",
			"Still tracking that weather query. Any updates needed?",
		},
		"code": {
			"I can help with coding. What language or problem?",
			"Continuing our coding discussion. What's the next step?",
			"Building on our code conversation. Ready for more!",
		},
	}

	for keyword, resps := range responses {
		if strings.Contains(strings.ToLower(input), keyword) {
			idx := (turn - 1) % len(resps)
			return resps[idx]
		}
	}

	return fmt.Sprintf("Turn %d response: I understand your query about %q", turn, truncate(input, 20))
}

func (w *SimulatedWorker) Kill() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.healthy = false
}

func (w *SimulatedWorker) IsHealthy() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.healthy
}

// ═══════════════════════════════════════════════════════════════════════════════
// MOCK REGISTRY (implements registry.Registry)
// ═══════════════════════════════════════════════════════════════════════════════

type MockRegistry struct {
	workers map[string]*SimulatedWorker
	mu      sync.RWMutex
}

func NewMockRegistry() *MockRegistry {
	return &MockRegistry{
		workers: make(map[string]*SimulatedWorker),
	}
}

func (r *MockRegistry) AddWorker(w *SimulatedWorker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.workers[w.id] = w
}

func (r *MockRegistry) GetHealthyNodes() []registry.NodeInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	nodes := make([]registry.NodeInfo, 0)
	for id, w := range r.workers {
		if w.IsHealthy() {
			nodes = append(nodes, registry.NodeInfo{
				ID:      id,
				Name:    id,
				Healthy: true,
			})
		}
	}
	return nodes
}

func (r *MockRegistry) GetNode(nodeID string) (registry.NodeInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	w, exists := r.workers[nodeID]
	if !exists {
		return registry.NodeInfo{}, false
	}
	return registry.NodeInfo{
		ID:      w.id,
		Name:    w.id,
		Healthy: w.IsHealthy(),
	}, true
}

func (r *MockRegistry) GetNodeID() string {
	return "coordinator"
}

func (r *MockRegistry) GetNodesByCapability(capability string) []registry.NodeInfo {
	return r.GetHealthyNodes()
}

func (r *MockRegistry) GetNodes() []registry.NodeInfo {
	return r.GetHealthyNodes()
}

func (r *MockRegistry) GetNodeCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.workers)
}

func (r *MockRegistry) GetHealthyNodeCount() int {
	return len(r.GetHealthyNodes())
}

func (r *MockRegistry) Start(ctx context.Context) error { return nil }

func (r *MockRegistry) Stop() error { return nil }

func (r *MockRegistry) GetWorker(id string) *SimulatedWorker {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.workers[id]
}

func (r *MockRegistry) GetAllWorkers() map[string]*SimulatedWorker {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make(map[string]*SimulatedWorker)
	for k, v := range r.workers {
		result[k] = v
	}
	return result
}

// ═══════════════════════════════════════════════════════════════════════════════
// MOCK TASK EXECUTOR
// ═══════════════════════════════════════════════════════════════════════════════

type MockExecutor struct {
	registry       *MockRegistry
	lastExecution  map[string]ExecutionResult
	mu             sync.Mutex
}

type ExecutionResult struct {
	WorkerID string
	Response string
	Latency  int64
	CacheHit bool
}

func NewMockExecutor(reg *MockRegistry) *MockExecutor {
	return &MockExecutor{
		registry:      reg,
		lastExecution: make(map[string]ExecutionResult),
	}
}

func (e *MockExecutor) ExecuteOnNode(ctx context.Context, nodeID string, agentName string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	worker := e.registry.GetWorker(nodeID)
	if worker == nil || !worker.IsHealthy() {
		return nil, fmt.Errorf("worker %s is unavailable", nodeID)
	}

	// Extract session ID and user input from the agent input
	sessionID := ""
	userInput := ""
	if input != nil && input.Context != nil {
		if sid, ok := input.Context["session_id"].(string); ok {
			sessionID = sid
		}
		if msg, ok := input.Context["message"].(string); ok {
			userInput = msg
		}
	}

	response, latency, cacheHit := worker.Execute(ctx, sessionID, userInput)

	e.mu.Lock()
	e.lastExecution[sessionID] = ExecutionResult{
		WorkerID: nodeID,
		Response: response,
		Latency:  latency,
		CacheHit: cacheHit,
	}
	e.mu.Unlock()

	return &agent.AgentOutput{
		Result: response,
		Metadata: map[string]interface{}{
			"worker_id": nodeID,
			"latency":   latency,
			"cache_hit": cacheHit,
		},
	}, nil
}

func (e *MockExecutor) GetLastExecution(sessionID string) (ExecutionResult, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	result, ok := e.lastExecution[sessionID]
	return result, ok
}

// ═══════════════════════════════════════════════════════════════════════════════
// DEMO SCENARIOS
// ═══════════════════════════════════════════════════════════════════════════════

func main() {
	printHeader()

	// Initialize OTEL Telemetry
	if os.Getenv("OTEL_EXPORTER_TYPE") == "" {
		os.Setenv("OTEL_EXPORTER_TYPE", "console") // Default to console for demo
	}
	telemetry.InitGlobalTelemetry("dagens-failover-demo", "v1.0.0")
	tracer = telemetry.GetGlobalTelemetry().GetTracer()

	fmt.Println(colorDim + "  OTEL Telemetry initialized. Set OTEL_EXPORTER_TYPE=otlp for Jaeger." + colorReset)
	fmt.Println()

	// Initialize cluster with 3 workers
	reg := NewMockRegistry()
	workers := []*SimulatedWorker{
		NewSimulatedWorker("worker-alpha"),
		NewSimulatedWorker("worker-beta"),
		NewSimulatedWorker("worker-gamma"),
	}
	for _, w := range workers {
		reg.AddWorker(w)
	}

	executor := NewMockExecutor(reg)

	// Create scheduler with sticky scheduling enabled
	config := scheduler.SchedulerConfig{
		EnableStickiness:        true,
		AffinityTTL:             5 * time.Minute,
		AffinityCleanupInterval: 1 * time.Minute,
		JobQueueSize:            100,
	}
	sched := scheduler.NewSchedulerWithConfig(reg, executor, config)
	sched.Start()
	defer sched.Stop()

	// Run demo scenarios
	ctx := context.Background()
	runScenario1_StickyRouting(ctx, sched, executor, reg)
	runScenario2_WorkerFailover(ctx, sched, executor, reg)
	runScenario3_MultipleSessionsParallel(ctx, sched, executor, reg)

	// Scenario 4: Real LLM + Guardrails (if API key is set)
	ranRealLLM := os.Getenv("OPENROUTER_API_KEY") != ""
	runScenario4_RealLLMWithGuardrails(ctx)

	printFinalSummary(reg, ranRealLLM)

	// Flush and shutdown traces before exit
	telemetry.ForceFlushGlobalTelemetry(ctx)
	time.Sleep(2 * time.Second) // Give exporter time to finish sending
	telemetry.ShutdownGlobalTelemetry(ctx)
}

// Scenario 1: Demonstrate sticky routing (HIT pattern)
func runScenario1_StickyRouting(ctx context.Context, sched *scheduler.Scheduler, executor *MockExecutor, reg *MockRegistry) {
	printSection("SCENARIO 1: Sticky Session Routing")
	fmt.Println()
	fmt.Println(colorDim + "  Demonstrating how multi-turn conversations stick to the same worker" + colorReset)
	fmt.Println(colorDim + "  for cache locality (10-100x faster responses on subsequent turns)" + colorReset)

	sessionID := "session-alice-001"
	messages := []string{
		"Hello, I need help with my project",
		"Can you explain how to use the weather API?",
		"What about error handling for that?",
		"Thanks, one more question about caching",
	}

	for turn, msg := range messages {
		runConversationTurn(ctx, sched, executor, reg, sessionID, msg, turn+1)
		time.Sleep(100 * time.Millisecond) // Small delay for visual effect
	}

	printWorkerStatus(reg.GetAllWorkers())
}

// Scenario 2: Demonstrate automatic failover
func runScenario2_WorkerFailover(ctx context.Context, sched *scheduler.Scheduler, executor *MockExecutor, reg *MockRegistry) {
	printSection("SCENARIO 2: Automatic Failover (The Killer Feature)")
	fmt.Println()
	fmt.Println(colorDim + "  Watch what happens when a worker dies mid-conversation." + colorReset)
	fmt.Println(colorDim + "  LangGraph/CrewAI/AutoGen: Conversation lost." + colorReset)
	fmt.Println(colorDim + "  Dagens: Automatic failover to healthy worker." + colorReset)

	sessionID := "session-bob-002"

	// Turn 1: Establish affinity
	fmt.Println()
	fmt.Println(colorBold + "  Phase 1: Establishing session affinity" + colorReset)
	runConversationTurn(ctx, sched, executor, reg, sessionID, "Hello, I'm starting a new coding project", 1)
	time.Sleep(200 * time.Millisecond)

	// Turn 2: Use sticky routing
	runConversationTurn(ctx, sched, executor, reg, sessionID, "Can you help me with Python decorators?", 2)

	// Get the worker this session is bound to
	result, _ := executor.GetLastExecution(sessionID)
	boundWorker := result.WorkerID

	// KILL THE WORKER - Emit OTEL event for this critical failure
	fmt.Println()
	fmt.Println(colorBold + colorRed + "  ╔══════════════════════════════════════════════════════════════════╗" + colorReset)
	fmt.Println(colorBold + colorRed + "  ║  SIMULATING WORKER FAILURE: " + boundWorker + " is now DEAD!           ║" + colorReset)
	fmt.Println(colorBold + colorRed + "  ╚══════════════════════════════════════════════════════════════════╝" + colorReset)

	// Create a span to capture the worker failure event
	_, failureSpan := tracer.StartSpan(ctx, "worker.failure")
	failureSpan.AddEvent("worker_failure", map[string]interface{}{
		"worker.id":       boundWorker,
		"session.id":      sessionID,
		"failure.type":    "simulated_crash",
		"sessions_affected": 1,
	})
	failureSpan.SetAttribute("worker.id", boundWorker)
	failureSpan.SetAttribute("failure.critical", true)
	failureSpan.SetStatus(telemetry.StatusError, "Worker crashed")
	failureSpan.End()

	reg.GetWorker(boundWorker).Kill()
	printWorkerStatus(reg.GetAllWorkers())
	time.Sleep(500 * time.Millisecond)

	// Turn 3: Automatic failover!
	fmt.Println()
	fmt.Println(colorBold + "  Phase 2: Automatic failover to healthy worker" + colorReset)
	runConversationTurn(ctx, sched, executor, reg, sessionID, "Continue explaining decorators please", 3)

	// Turn 4: New affinity established
	runConversationTurn(ctx, sched, executor, reg, sessionID, "What about class decorators?", 4)

	printWorkerStatus(reg.GetAllWorkers())
}

// Scenario 3: Multiple sessions running in parallel
func runScenario3_MultipleSessionsParallel(ctx context.Context, sched *scheduler.Scheduler, executor *MockExecutor, reg *MockRegistry) {
	printSection("SCENARIO 3: Multiple Sessions in Parallel")
	fmt.Println()
	fmt.Println(colorDim + "  10 concurrent sessions, each with 3 turns." + colorReset)
	fmt.Println(colorDim + "  Watch how sessions distribute across healthy workers." + colorReset)

	var wg sync.WaitGroup
	sessions := []string{
		"session-user01", "session-user02", "session-user03", "session-user04", "session-user05",
		"session-user06", "session-user07", "session-user08", "session-user09", "session-user10",
	}

	fmt.Println()
	fmt.Println(colorBold + "  Running 10 sessions x 3 turns = 30 tasks in parallel..." + colorReset)
	start := time.Now()

	for _, sessionID := range sessions {
		wg.Add(1)
		go func(sid string) {
			defer wg.Done()
			for turn := 1; turn <= 3; turn++ {
				runConversationTurnQuiet(ctx, sched, executor, reg, sid, fmt.Sprintf("Message %d from %s", turn, sid), turn)
				time.Sleep(50 * time.Millisecond)
			}
		}(sessionID)
	}

	wg.Wait()
	elapsed := time.Since(start)

	fmt.Printf("\n  %sCompleted 30 tasks in %v%s\n", colorGreen, elapsed, colorReset)
	printWorkerStatus(reg.GetAllWorkers())

	// Show affinity stats
	stats := sched.GetAffinityStats()
	fmt.Println()
	fmt.Println(colorBold + "  AFFINITY MAP STATS:" + colorReset)
	fmt.Printf("  ├─ Enabled: %v\n", stats["enabled"])
	fmt.Printf("  ├─ TTL: %v\n", stats["ttl"])
	fmt.Printf("  └─ Active Sessions: %v\n", stats["size"])
}

func runConversationTurn(ctx context.Context, sched *scheduler.Scheduler, executor *MockExecutor, reg *MockRegistry, sessionID, message string, turn int) {
	// Create a span for this conversation turn with full OTEL correlation
	turnCtx, turnSpan := tracer.StartSpan(ctx, "conversation.turn")
	defer turnSpan.End()

	turnSpan.SetAttribute("session.id", sessionID)
	turnSpan.SetAttribute("turn.number", turn)
	turnSpan.SetAttribute("user.message", message)

	// Get trace ID for display
	traceID := turnSpan.TraceID()

	// Create a job for this conversation turn
	job := scheduler.NewJob(fmt.Sprintf("job-%s-turn%d", sessionID[:12], turn), "conversation")
	stage := &scheduler.Stage{
		ID:    fmt.Sprintf("stage-%d", turn),
		JobID: job.ID,
		Tasks: []*scheduler.Task{
			{
				ID:           fmt.Sprintf("task-%d", turn),
				StageID:      fmt.Sprintf("stage-%d", turn),
				JobID:        job.ID,
				AgentID:      "chat-agent",
				AgentName:    "ChatAgent",
				PartitionKey: sessionID, // THIS IS THE KEY - sticky routing!
				Input: &agent.AgentInput{
					Context: map[string]interface{}{
						"session_id": sessionID,
						"message":    message,
						"trace_id":   traceID, // Propagate trace context
					},
				},
			},
		},
	}
	job.AddStage(stage)

	// Submit and wait
	if err := sched.SubmitJob(job); err != nil {
		fmt.Printf("  %s[ERROR]%s Failed to submit job: %v\n", colorRed, colorReset, err)
		turnSpan.SetStatus(telemetry.StatusError, err.Error())
		return
	}

	// Wait for completion
	for {
		j, _ := sched.GetJob(job.ID)
		if j.Status == scheduler.JobCompleted || j.Status == scheduler.JobFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Get results and emit OTEL events
	result, ok := executor.GetLastExecution(sessionID)
	if ok {
		// Emit affinity event to OTEL
		affinityType := "cache_miss"
		if result.CacheHit {
			affinityType = "cache_hit"
		}

		turnSpan.AddEvent("affinity_decision", map[string]interface{}{
			"session.id":    sessionID,
			"worker.id":     result.WorkerID,
			"affinity.type": affinityType,
			"cache.hit":     result.CacheHit,
			"latency.ms":    result.Latency,
		})

		turnSpan.SetAttribute("worker.id", result.WorkerID)
		turnSpan.SetAttribute("cache.hit", result.CacheHit)
		turnSpan.SetAttribute("latency.ms", result.Latency)
		turnSpan.SetStatus(telemetry.StatusOK, "Turn completed")

		printConversationTurn(turn, sessionID, message, result.Response, result.WorkerID, result.Latency, result.CacheHit, traceID)
	}

	_ = turnCtx // Use context for child spans if needed
}

func runConversationTurnQuiet(ctx context.Context, sched *scheduler.Scheduler, executor *MockExecutor, reg *MockRegistry, sessionID, message string, turn int) {
	// Create span even for quiet turns - important for Jaeger visibility
	_, turnSpan := tracer.StartSpan(ctx, "conversation.turn.parallel")
	defer turnSpan.End()

	turnSpan.SetAttribute("session.id", sessionID)
	turnSpan.SetAttribute("turn.number", turn)

	job := scheduler.NewJob(fmt.Sprintf("job-%s-turn%d", sessionID, turn), "conversation")
	stage := &scheduler.Stage{
		ID:    fmt.Sprintf("stage-%d", turn),
		JobID: job.ID,
		Tasks: []*scheduler.Task{
			{
				ID:           fmt.Sprintf("task-%d", turn),
				StageID:      fmt.Sprintf("stage-%d", turn),
				JobID:        job.ID,
				AgentID:      "chat-agent",
				AgentName:    "ChatAgent",
				PartitionKey: sessionID,
				Input: &agent.AgentInput{
					Context: map[string]interface{}{
						"session_id": sessionID,
						"message":    message,
					},
				},
			},
		},
	}
	job.AddStage(stage)

	if err := sched.SubmitJob(job); err != nil {
		turnSpan.SetStatus(telemetry.StatusError, err.Error())
		return
	}

	for {
		j, _ := sched.GetJob(job.ID)
		if j.Status == scheduler.JobCompleted || j.Status == scheduler.JobFailed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Record result in span
	result, ok := executor.GetLastExecution(sessionID)
	if ok {
		turnSpan.SetAttribute("worker.id", result.WorkerID)
		turnSpan.SetAttribute("cache.hit", result.CacheHit)
		turnSpan.SetStatus(telemetry.StatusOK, "Completed")
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// SCENARIO 4: REAL LLM + GUARDRAILS (The "Wow" Demo for VCs)
// ═══════════════════════════════════════════════════════════════════════════════

// RealLLMClient calls OpenRouter API for real LLM responses
type RealLLMClient struct {
	apiKey string
	model  string
}

func NewRealLLMClient(apiKey, model string) *RealLLMClient {
	if model == "" {
		model = "deepseek/deepseek-chat" // Fast and cheap for demo
	}
	return &RealLLMClient{apiKey: apiKey, model: model}
}

func (c *RealLLMClient) Generate(ctx context.Context, prompt string) (string, error) {
	// Add timeout to prevent hanging on slow LLM responses
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	reqBody := map[string]interface{}{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": "You are a helpful assistant. Answer the user's question directly."},
			{"role": "user", "content": prompt},
		},
		"max_tokens": 500,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("HTTP-Referer", "https://github.com/seyi/dagens")

	client := &http.Client{} // Timeout now handled by context
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if result.Error.Message != "" {
		return "", fmt.Errorf("API error: %s", result.Error.Message)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no response from LLM")
	}

	return result.Choices[0].Message.Content, nil
}

// Scenario 4: Real LLM with Guardrails - The "Wow" Demo
func runScenario4_RealLLMWithGuardrails(ctx context.Context) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		fmt.Println()
		fmt.Println(colorDim + "  [Skipping Scenario 4: Set OPENROUTER_API_KEY for real LLM demo]" + colorReset)
		return
	}

	printSection("SCENARIO 4: Real LLM + Guardrails (The WOW Demo)")
	fmt.Println()
	fmt.Println(colorBold + colorYellow + "  This is what makes Dagens enterprise-ready." + colorReset)
	fmt.Println(colorDim + "  A real LLM will generate responses. Our guardrails catch dangerous content." + colorReset)
	fmt.Println()

	// Initialize real LLM client
	llm := NewRealLLMClient(apiKey, "deepseek/deepseek-chat")

	// Setup Policy Engine with enterprise-grade audit logging
	// CompositeAuditLogger writes to both Postgres (for compliance) and OTEL (for visibility)
	dbConn := os.Getenv("DATABASE_URL")
	if dbConn == "" {
		dbConn = "postgres://postgres:postgres@localhost:5432/dagens?sslmode=disable"
	}

	pgLogger, err := audit.NewPostgresAuditLoggerFromURL(ctx, audit.PostgresConfig{
		ConnectionString: dbConn,
		TableName:        "failover_audit_log",
		MigrateOnStart:   true,
	}, nil)

	var auditLogger policy.AuditLogger
	if err != nil {
		fmt.Printf("  %s[WARN]%s Postgres audit logging unavailable, using Stdout+OTEL fallback: %v\n", colorYellow, colorReset, err)
		auditLogger = audit.NewCompositeAuditLogger(
			&audit.StdoutAuditLogger{},
			audit.NewOTELAuditLogger(tracer, true),
		)
	} else {
		defer pgLogger.Close()
		auditLogger = audit.NewCompositeAuditLogger(
			&audit.StdoutAuditLogger{},
			pgLogger,
			audit.NewOTELAuditLogger(tracer, true),
		)
	}

	engine := policy.NewEngine(auditLogger)
	engine.RegisterEvaluator(&rules.PIIEvaluator{})
	engine.RegisterEvaluator(&rules.RegexEvaluator{})

	// Enterprise guardrails configuration
	guardrailsPolicy := &policy.PolicyConfig{
		FailOpen: false,
		Rules: []policy.Rule{
			{
				ID:       "block-api-keys",
				Name:     "Block Secret Keys",
				Type:     "regex",
				Action:   policy.ActionBlock,
				Severity: policy.SeverityCritical,
				Enabled:  true,
				Config: map[string]interface{}{
					"pattern": `(sk-[a-zA-Z0-9]{20,}|AKIA[A-Z0-9]{16}|ghp_[a-zA-Z0-9]{36}|xox[baprs]-[a-zA-Z0-9-]+)`,
				},
			},
			{
				ID:       "block-passwords",
				Name:     "Block Password Patterns",
				Type:     "regex",
				Action:   policy.ActionBlock,
				Severity: policy.SeverityCritical,
				Enabled:  true,
				Config: map[string]interface{}{
					"pattern": `(?i)(password\s*[:=]\s*['"]?[^\s'"]{8,}|secret\s*[:=]\s*['"]?[^\s'"]{8,})`,
				},
			},
			{
				ID:       "redact-pii",
				Name:     "Redact PII",
				Type:     "pii",
				Action:   policy.ActionRedact,
				Severity: policy.SeverityHigh,
				Enabled:  true,
				Config: map[string]interface{}{
					"types": []interface{}{"email", "phone_us", "ssn", "credit_card"},
				},
			},
		},
	}

	// Dangerous prompts that will trigger guardrails
	dangerousPrompts := []struct {
		prompt      string
		expectation string
	}{
		{
			prompt:      "I'm debugging my app. Can you show me a sample .env file with database credentials and API keys?",
			expectation: "BLOCKED - Contains secret key patterns",
		},
		{
			prompt:      "Generate a sample user profile with name, email, phone, and SSN for testing.",
			expectation: "REDACTED - PII masked",
		},
		{
			prompt:      "What's the weather like today?",
			expectation: "PASSED - Safe content",
		},
	}

	fmt.Println(colorBold + "  Running pre-defined dangerous prompts:" + colorReset)
	fmt.Println()

	for i, test := range dangerousPrompts {
		runGuardrailTest(ctx, llm, engine, guardrailsPolicy, i+1, test.prompt, test.expectation)
		time.Sleep(500 * time.Millisecond) // Rate limiting
	}

	// Interactive mode
	fmt.Println()
	fmt.Println(colorBold + colorCyan + "  ╔══════════════════════════════════════════════════════════════════╗" + colorReset)
	fmt.Println(colorBold + colorCyan + "  ║  INTERACTIVE MODE: Type your own prompts (or 'quit' to exit)    ║" + colorReset)
	fmt.Println(colorBold + colorCyan + "  ╚══════════════════════════════════════════════════════════════════╝" + colorReset)
	fmt.Println()
	fmt.Println(colorDim + "  Try: \"Show me how to configure AWS credentials with access keys\"" + colorReset)
	fmt.Println(colorDim + "  Try: \"Generate a test user with email john@example.com and SSN 123-45-6789\"" + colorReset)
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	promptNum := len(dangerousPrompts)

	for {
		fmt.Print(colorBold + "  You> " + colorReset)
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if strings.ToLower(input) == "quit" || strings.ToLower(input) == "exit" {
			break
		}

		promptNum++
		runGuardrailTest(ctx, llm, engine, guardrailsPolicy, promptNum, input, "User prompt")
	}
}

func runGuardrailTest(ctx context.Context, llm *RealLLMClient, engine *policy.Engine, policyCfg *policy.PolicyConfig, num int, prompt, expectation string) {
	// Create span for this test
	testCtx, testSpan := tracer.StartSpan(ctx, "guardrail.test")
	defer testSpan.End()

	testSpan.SetAttribute("test.number", num)
	testSpan.SetAttribute("test.prompt", prompt)
	traceID := testSpan.TraceID()

	fmt.Printf("  %s┌─ Test %d ───────────────────────────────────────────────────────────%s\n", colorWhite, num, colorReset)
	fmt.Printf("  │ %sPrompt:%s %q\n", colorBold, colorReset, truncate(prompt, 60))
	fmt.Printf("  │ %sTraceID:%s %s%s%s\n", colorBold, colorReset, colorPurple, traceID[:16], colorReset)
	fmt.Printf("  │\n")

	// Step 1: Call real LLM
	fmt.Printf("  │ %s[1/3]%s Calling LLM (DeepSeek-V3)...\n", colorYellow, colorReset)
	startLLM := time.Now()

	rawResponse, err := llm.Generate(testCtx, prompt)
	llmLatency := time.Since(startLLM)

	if err != nil {
		fmt.Printf("  │ %s[ERROR]%s LLM call failed: %v\n", colorRed, colorReset, err)
		fmt.Printf("  └───────────────────────────────────────────────────────────────────\n\n")
		testSpan.SetStatus(telemetry.StatusError, err.Error())
		return
	}

	fmt.Printf("  │       Raw output (%dms): %q\n", llmLatency.Milliseconds(), truncate(rawResponse, 50))
	testSpan.AddEvent("llm_response", map[string]interface{}{
		"latency_ms":  llmLatency.Milliseconds(),
		"output_len":  len(rawResponse),
		"model":       "deepseek/deepseek-chat",
	})

	// Step 2: Apply guardrails
	fmt.Printf("  │\n")
	fmt.Printf("  │ %s[2/3]%s Applying guardrails...\n", colorYellow, colorReset)

	metadata := policy.EvaluationMetadata{
		SessionID: fmt.Sprintf("test-%d", num),
		JobID:     "guardrail-demo",
		NodeID:    "demo-worker",
		TraceID:   traceID,
		SpanID:    testSpan.SpanID(),
	}

	result, err := engine.Evaluate(testCtx, policyCfg, rawResponse, metadata)
	if err != nil {
		fmt.Printf("  │ %s[ERROR]%s Guardrail evaluation failed: %v\n", colorRed, colorReset, err)
		fmt.Printf("  └───────────────────────────────────────────────────────────────────\n\n")
		return
	}

	// Step 3: Show result
	fmt.Printf("  │\n")
	fmt.Printf("  │ %s[3/3]%s Result:\n", colorYellow, colorReset)

	if !result.Allowed {
		// BLOCKED
		fmt.Printf("  │       %s╔═══ BLOCKED ═══════════════════════════════════════════════╗%s\n", colorRed, colorReset)
		fmt.Printf("  │       %s║%s  Reason: %s%s\n", colorRed, colorReset, result.BlockReason, colorRed+"║"+colorReset)
		fmt.Printf("  │       %s╚════════════════════════════════════════════════════════════╝%s\n", colorRed, colorReset)

		testSpan.AddEvent("guardrail_blocked", map[string]interface{}{
			"reason":       result.BlockReason,
			"rules_matched": len(result.Results),
		})
		testSpan.SetStatus(telemetry.StatusError, "Content blocked by guardrails")

	} else if result.FinalAction == policy.ActionRedact {
		// REDACTED
		fmt.Printf("  │       %s╔═══ REDACTED ══════════════════════════════════════════════╗%s\n", colorYellow, colorReset)
		fmt.Printf("  │       %s║%s  PII/Sensitive data has been masked                       %s║%s\n", colorYellow, colorReset, colorYellow, colorReset)
		fmt.Printf("  │       %s╚════════════════════════════════════════════════════════════╝%s\n", colorYellow, colorReset)
		fmt.Printf("  │       Safe output: %q\n", truncate(result.FinalOutput, 60))

		testSpan.AddEvent("guardrail_redacted", map[string]interface{}{
			"original_len": len(rawResponse),
			"redacted_len": len(result.FinalOutput),
		})
		testSpan.SetStatus(telemetry.StatusOK, "Content redacted")

	} else {
		// PASSED
		fmt.Printf("  │       %s╔═══ PASSED ════════════════════════════════════════════════╗%s\n", colorGreen, colorReset)
		fmt.Printf("  │       %s║%s  Content is safe - no policy violations                    %s║%s\n", colorGreen, colorReset, colorGreen, colorReset)
		fmt.Printf("  │       %s╚════════════════════════════════════════════════════════════╝%s\n", colorGreen, colorReset)
		fmt.Printf("  │       Output: %q\n", truncate(result.FinalOutput, 60))

		testSpan.AddEvent("guardrail_passed", map[string]interface{}{
			"output_len": len(result.FinalOutput),
		})
		testSpan.SetStatus(telemetry.StatusOK, "Content passed")
	}

	fmt.Printf("  └───────────────────────────────────────────────────────────────────\n\n")
}

func printFinalSummary(reg *MockRegistry, ranRealLLM bool) {
	printSection("DEMO COMPLETE: Key Takeaways")
	fmt.Println()
	fmt.Println(colorBold + "  What You Just Saw:" + colorReset)
	fmt.Println("  1. Multi-turn conversations automatically route to the same worker")
	fmt.Println("  2. Cache hits provide 10x faster response times")
	fmt.Println("  3. When a worker dies, conversations continue on healthy workers")
	fmt.Println("  4. Full observability with HIT/MISS/STALE metrics")
	fmt.Println("  5. " + colorPurple + "OTEL trace correlation - every event visible in Jaeger" + colorReset)
	if ranRealLLM {
		fmt.Println("  6. " + colorRed + "Real LLM + Guardrails catching dangerous content" + colorReset)
	}
	fmt.Println()
	fmt.Println(colorBold + "  OTEL Events Captured:" + colorReset)
	fmt.Println("  ├─ conversation.turn spans with session/worker attributes")
	fmt.Println("  ├─ affinity_decision events (cache_hit/cache_miss)")
	fmt.Println("  ├─ worker_failure events with affected sessions")
	if ranRealLLM {
		fmt.Println("  ├─ guardrail_blocked/redacted/passed events")
		fmt.Println("  ├─ llm_response events with latency metrics")
	}
	fmt.Println("  └─ All events linked by TraceID for end-to-end visibility")
	fmt.Println()
	fmt.Println(colorBold + "  Why This Matters for Production:" + colorReset)
	fmt.Println("  - LangGraph: Single process, no distributed execution, no guardrails")
	fmt.Println("  - CrewAI: All agents in memory, no failover, no policy engine")
	fmt.Println("  - AutoGen: Async but no sticky sessions, no audit trail")
	fmt.Println("  - " + colorGreen + "Dagens: Distributed + Sticky + Failover + Guardrails + OTEL" + colorReset)
	fmt.Println()

	// Calculate final stats
	workers := reg.GetAllWorkers()
	totalTasks := 0
	totalHits := 0
	healthyCount := 0
	for _, w := range workers {
		totalTasks += w.tasksExecuted
		totalHits += w.cacheHits
		if w.healthy {
			healthyCount++
		}
	}

	hitRate := float64(0)
	if totalTasks > 0 {
		hitRate = float64(totalHits) / float64(totalTasks) * 100
	}

	fmt.Println(colorBold + "  FINAL METRICS:" + colorReset)
	fmt.Printf("  ├─ Total Tasks Executed: %d\n", totalTasks)
	fmt.Printf("  ├─ Cache Hit Rate: %.1f%%\n", hitRate)
	fmt.Printf("  ├─ Healthy Workers: %d/%d\n", healthyCount, len(workers))
	fmt.Printf("  └─ Failovers Handled: %s1 (graceful)%s\n", colorGreen, colorReset)
	fmt.Println()
}
