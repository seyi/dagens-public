// Package main demonstrates a multi-agent workflow using Phase 2A state management.
//
// This demo creates a simple Researcher -> Reviewer pipeline:
// 1. Researcher agent investigates a topic and stores findings in state
// 2. Reviewer agent reads the research and provides feedback
// 3. State is checkpointed after each agent for recovery/replay
//
// Usage:
//
//	export OPENAI_API_KEY=your-key
//	go run examples/multi_agent_demo/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/seyi/dagens/pkg/graph"
	"github.com/seyi/dagens/pkg/models"
	"github.com/seyi/dagens/pkg/models/openai"
)

func main() {
	fmt.Println("╔═══════════════════════════════════════════════════════════════╗")
	fmt.Println("║   MULTI-AGENT DEMO: Researcher + Reviewer with State Mgmt    ║")
	fmt.Println("╚═══════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Check API key
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable is required")
	}

	// Create OpenAI provider
	provider, err := openai.NewProvider(openai.Config{
		Model:  "gpt-4o-mini",
		APIKey: apiKey,
		Parameters: openai.Parameters{
			Temperature:     0.7,
			MaxOutputTokens: 500,
		},
	})
	if err != nil {
		log.Fatalf("Failed to create provider: %v", err)
	}
	defer provider.Close()

	fmt.Printf("Provider: %s\n", provider.Name())
	fmt.Printf("Context window: %d tokens\n\n", provider.ContextWindow())

	// Create state store for checkpointing
	store := graph.NewMemoryStateStore()
	ctx := context.Background()

	// Topic to research
	topic := "the benefits of microservices architecture"

	// ========================================
	// Phase 1: Researcher Agent
	// ========================================
	fmt.Println("─────────────────────────────────────────────────────────────────")
	fmt.Println("Phase 1: RESEARCHER AGENT")
	fmt.Println("─────────────────────────────────────────────────────────────────")

	// Initialize state with research request
	state := graph.NewMemoryState()
	state.Set("workflow.id", "research-001")
	state.Set("workflow.topic", topic)
	state.Set("workflow.started_at", time.Now().Format(time.RFC3339))
	state.Set("researcher.status", "pending")

	fmt.Printf("Topic: %s\n", topic)
	fmt.Printf("Initial state version: %d\n\n", state.Version())

	// Run researcher
	researchPrompt := fmt.Sprintf(`You are a technical researcher. Research the following topic and provide a concise summary with 3-4 key points.

Topic: %s

Format your response as:
SUMMARY: [1-2 sentence overview]
KEY POINTS:
1. [point 1]
2. [point 2]
3. [point 3]`, topic)

	fmt.Println("Researcher is working...")
	researchStart := time.Now()

	researchResp, err := provider.Chat(ctx, &models.ModelRequest{
		Messages: []models.Message{
			{Role: models.RoleUser, Content: researchPrompt},
		},
	})
	if err != nil {
		log.Fatalf("Researcher failed: %v", err)
	}

	researchDuration := time.Since(researchStart)

	// Store research results in state
	state.Set("researcher.output", researchResp.Message.Content)
	state.Set("researcher.status", "completed")
	state.Set("researcher.duration_ms", researchDuration.Milliseconds())
	state.Set("researcher.tokens_used", researchResp.Usage.TotalTokens)
	state.Set("researcher.completed_at", time.Now().Format(time.RFC3339))

	fmt.Printf("\nResearcher Output:\n%s\n", researchResp.Message.Content)
	fmt.Printf("\n[Duration: %v, Tokens: %d, State version: %d]\n",
		researchDuration, researchResp.Usage.TotalTokens, state.Version())

	// Checkpoint after researcher
	checkpoint1 := state.Snapshot()
	store.Save(ctx, "after-researcher", checkpoint1)
	fmt.Println("[Checkpoint saved: after-researcher]")

	// ========================================
	// Phase 2: Reviewer Agent
	// ========================================
	fmt.Println("\n─────────────────────────────────────────────────────────────────")
	fmt.Println("Phase 2: REVIEWER AGENT")
	fmt.Println("─────────────────────────────────────────────────────────────────")

	// Get research from state (demonstrating state passing between agents)
	researchOutput, _ := state.Get("researcher.output")
	state.Set("reviewer.status", "pending")

	reviewPrompt := fmt.Sprintf(`You are a technical reviewer. Review the following research and provide:
1. A quality score (1-10)
2. What's good about the research
3. One suggestion for improvement

Research to review:
%s

Format:
SCORE: [X/10]
STRENGTHS: [what's good]
IMPROVEMENT: [suggestion]`, researchOutput)

	fmt.Println("Reviewer is analyzing research...")
	reviewStart := time.Now()

	reviewResp, err := provider.Chat(ctx, &models.ModelRequest{
		Messages: []models.Message{
			{Role: models.RoleUser, Content: reviewPrompt},
		},
	})
	if err != nil {
		log.Fatalf("Reviewer failed: %v", err)
	}

	reviewDuration := time.Since(reviewStart)

	// Store review results in state
	state.Set("reviewer.output", reviewResp.Message.Content)
	state.Set("reviewer.status", "completed")
	state.Set("reviewer.duration_ms", reviewDuration.Milliseconds())
	state.Set("reviewer.tokens_used", reviewResp.Usage.TotalTokens)
	state.Set("reviewer.completed_at", time.Now().Format(time.RFC3339))
	state.Set("workflow.status", "completed")

	fmt.Printf("\nReviewer Output:\n%s\n", reviewResp.Message.Content)
	fmt.Printf("\n[Duration: %v, Tokens: %d, State version: %d]\n",
		reviewDuration, reviewResp.Usage.TotalTokens, state.Version())

	// Checkpoint after reviewer
	checkpoint2 := state.Snapshot()
	store.Save(ctx, "after-reviewer", checkpoint2)
	fmt.Println("[Checkpoint saved: after-reviewer]")

	// ========================================
	// Demonstrate State Features
	// ========================================
	fmt.Println("\n─────────────────────────────────────────────────────────────────")
	fmt.Println("STATE MANAGEMENT FEATURES DEMO")
	fmt.Println("─────────────────────────────────────────────────────────────────")

	// 1. Scope Iteration - get all researcher data
	fmt.Println("\n1. Scope Iteration (researcher.* keys):")
	state.Iterate(func(k string, v interface{}) bool {
		if strings.HasPrefix(k, "researcher.") {
			if k == "researcher.output" {
				fmt.Printf("   %s = [%d chars]\n", k, len(v.(string)))
			} else {
				fmt.Printf("   %s = %v\n", k, v)
			}
		}
		return true
	})

	// 2. KeysWithPrefix - list workflow keys
	fmt.Println("\n2. KeysWithPrefix (workflow.*):")
	workflowKeys := state.KeysWithPrefix("workflow.")
	for _, k := range workflowKeys {
		v, _ := state.Get(k)
		fmt.Printf("   %s = %v\n", k, v)
	}

	// 3. Serialization
	fmt.Println("\n3. Serialization:")
	serialized, _ := state.Marshal()
	fmt.Printf("   Serialized state size: %d bytes\n", len(serialized))

	// 4. Version tracking
	fmt.Println("\n4. Version Tracking:")
	fmt.Printf("   Final state version: %d\n", state.Version())

	// 5. Checkpoint recovery
	fmt.Println("\n5. Checkpoint Recovery:")
	checkpoints, _ := store.List(ctx)
	fmt.Printf("   Available checkpoints: %v\n", checkpoints)

	// Demonstrate recovery from earlier checkpoint
	earlierSnapshot, _ := store.Load(ctx, "after-researcher")
	recoveredState := graph.NewMemoryState()
	recoveredState.Restore(earlierSnapshot)

	recoveredStatus, _ := recoveredState.Get("reviewer.status")
	fmt.Printf("   Recovered state (after-researcher): reviewer.status = %v\n", recoveredStatus)

	// ========================================
	// Summary
	// ========================================
	fmt.Println("\n═══════════════════════════════════════════════════════════════════")
	fmt.Println("                        WORKFLOW COMPLETE")
	fmt.Println("═══════════════════════════════════════════════════════════════════")

	totalTokens := researchResp.Usage.TotalTokens + reviewResp.Usage.TotalTokens
	totalDuration := researchDuration + reviewDuration

	fmt.Printf("\nWorkflow Summary:\n")
	fmt.Printf("  Topic: %s\n", topic)
	fmt.Printf("  Agents executed: 2 (Researcher, Reviewer)\n")
	fmt.Printf("  Total duration: %v\n", totalDuration)
	fmt.Printf("  Total tokens: %d\n", totalTokens)
	fmt.Printf("  State transitions: %d\n", state.Version()-1)
	fmt.Printf("  Checkpoints saved: %d\n", len(checkpoints))

	fmt.Println("\nPhase 2A Features Used:")
	fmt.Println("  [x] State passing between agents")
	fmt.Println("  [x] Scoped state iteration (researcher.*, workflow.*)")
	fmt.Println("  [x] Checkpoint persistence (StateStore)")
	fmt.Println("  [x] State serialization (Marshal)")
	fmt.Println("  [x] Version tracking")
	fmt.Println("  [x] Checkpoint recovery (Restore)")
}
