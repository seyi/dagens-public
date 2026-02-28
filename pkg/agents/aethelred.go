// Package agents provides agent implementations with agentic capabilities.
package agents

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/agent"
)

// Dungeon Master domain metrics
const (
	DomainPlayerAgencyScore     = "domain.dm.player_agency_score"
	DomainNarrativeCoherence    = "domain.dm.narrative_coherence"
	DomainChallengeBalance      = "domain.dm.challenge_balance_rating"
	DomainSessionEngagementTime = "domain.dm.session_engagement_time"
)

// AethelredAgent is an AI game master that generates narrative and adapts to players.
type AethelredAgent struct {
	*agent.BaseAgent
	loreMaster agent.Agent
	mu         sync.Mutex
	execCount  int
}

// NewAethelredAgent creates a new AethelredAgent.
func NewAethelredAgent(loreMaster agent.Agent) *AethelredAgent {
	aa := &AethelredAgent{
		loreMaster: loreMaster,
	}

	base := agent.NewAgent(agent.AgentConfig{
		Name:         "Aethelred",
		Description:  "An AI game master for tabletop RPGs that generates narrative and adapts to players.",
		Capabilities: []string{"narrative_generation", "npc_control", "difficulty_adaptation"},
		Dependencies: []agent.Agent{loreMaster},
		Executor:     agent.ExecutorFunc(aa.execute),
	})
	aa.BaseAgent = base

	return aa
}

// execute simulates the core logic of the dungeon master agent.
func (aa *AethelredAgent) execute(ctx context.Context, a agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	aa.mu.Lock()
	aa.execCount++
	currentAttempt := aa.execCount
	aa.mu.Unlock()

	// --- SELF-HEALING DEMO ---
	// Check context for a failure simulation flag.
	if failCount, ok := input.Context["force_fail_count"].(int); ok {
		if currentAttempt <= failCount {
			return nil, fmt.Errorf("simulated failure: LLM API timed out on attempt %d", currentAttempt)
		}
	}

	// --- CORE LOGIC ---
	// 1. Generate narrative (simulated).
	time.Sleep(50 * time.Millisecond) // Simulate work
	narrative := "A shadowy figure emerges from the ancient crypt, clutching a glowing artifact..."

	// 2. Use peer agent for feedback (optional, for demonstration).
	var loreOutput *agent.AgentOutput
	if aa.loreMaster != nil {
		loreInput := &agent.AgentInput{
			TaskID:      input.TaskID,
			Instruction: narrative,
		}
		var err error
		loreOutput, err = aa.loreMaster.Execute(ctx, loreInput)
		if err != nil {
			fmt.Printf("Warning: LoreMaster peer agent failed: %v\n", err)
		}
	}

	// --- PREPARE OUTPUT ---
	output := &agent.AgentOutput{
		TaskID: input.TaskID,
		Result: narrative,
		Metrics: &agent.ExecutionMetrics{
			TokensUsed: 1200, // Simulate token consumption for monitoring.
		},
		Metadata: make(map[string]interface{}),
	}

	if loreOutput != nil {
		output.Metadata["lore_master_feedback"] = loreOutput.Result
	}

	return output, nil
}

// ResetExecCount resets the execution counter for testing.
func (aa *AethelredAgent) ResetExecCount() {
	aa.mu.Lock()
	aa.execCount = 0
	aa.mu.Unlock()
}
