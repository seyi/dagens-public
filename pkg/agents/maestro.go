// Package agents provides agent implementations with agentic capabilities.
package agents

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/agent"
)

// Composer domain metrics
const (
	DomainStylisticSimilarity = "domain.composer.stylistic_similarity"
	DomainMelodicNovelty      = "domain.composer.melodic_novelty"
	DomainHarmonicComplexity  = "domain.composer.harmonic_complexity"
	DomainCompositionLength   = "domain.composer.composition_length_seconds"
)

// MaestroAgent is a generative agent that composes original orchestral music.
type MaestroAgent struct {
	*agent.BaseAgent
	theoryAgent agent.Agent
	mu          sync.Mutex
	execCount   int
}

// NewMaestroAgent creates a new MaestroAgent.
func NewMaestroAgent(theoryAgent agent.Agent) *MaestroAgent {
	ma := &MaestroAgent{
		theoryAgent: theoryAgent,
	}

	base := agent.NewAgent(agent.AgentConfig{
		Name:         "Maestro",
		Description:  "Composes original orchestral music in a specified style.",
		Capabilities: []string{"music_composition", "style_adaptation", "orchestration"},
		Dependencies: []agent.Agent{theoryAgent},
		Executor:     agent.ExecutorFunc(ma.execute),
	})
	ma.BaseAgent = base

	return ma
}

// execute simulates the core logic of the music composition agent.
func (ma *MaestroAgent) execute(ctx context.Context, a agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	ma.mu.Lock()
	ma.execCount++
	currentAttempt := ma.execCount
	ma.mu.Unlock()

	// --- SELF-HEALING DEMO ---
	if failCount, ok := input.Context["force_fail_count"].(int); ok {
		if currentAttempt <= failCount {
			return nil, fmt.Errorf("simulated failure: rendering service API failed on attempt %d", currentAttempt)
		}
	}

	// --- CORE LOGIC ---
	// 1. Generate composition (simulated).
	time.Sleep(100 * time.Millisecond) // Simulate heavy computation
	composition := "Sonata in C Minor, Op. 1 - Movement I: Allegro con brio"

	// 2. Use peer agent for analysis.
	var theoryOutput *agent.AgentOutput
	if ma.theoryAgent != nil {
		theoryInput := &agent.AgentInput{
			TaskID:      input.TaskID,
			Instruction: composition,
		}
		var err error
		theoryOutput, err = ma.theoryAgent.Execute(ctx, theoryInput)
		if err != nil {
			fmt.Printf("Warning: MusicTheory peer agent failed: %v\n", err)
		}
	}

	// --- PREPARE OUTPUT ---
	output := &agent.AgentOutput{
		TaskID: input.TaskID,
		Result: composition,
		Metrics: &agent.ExecutionMetrics{
			TokensUsed: 5000, // Simulate resource consumption
		},
		Metadata: make(map[string]interface{}),
	}

	if theoryOutput != nil {
		output.Metadata["music_theory_analysis"] = theoryOutput.Result
	}

	return output, nil
}

// ResetExecCount resets the execution counter for testing.
func (ma *MaestroAgent) ResetExecCount() {
	ma.mu.Lock()
	ma.execCount = 0
	ma.mu.Unlock()
}
