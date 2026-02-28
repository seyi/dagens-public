// Package agents provides agent implementations with agentic capabilities.
package agents

import (
	"context"

	"github.com/seyi/dagens/pkg/agent"
)

// --- LoreMasterAgent ---

// LoreMasterResult is the output of the LoreMasterAgent.
type LoreMasterResult struct {
	CoherenceScore float64
	Feedback       string
}

// LoreMasterAgent is a peer agent that validates narrative coherence.
type LoreMasterAgent struct {
	*agent.BaseAgent
}

// NewLoreMasterAgent creates a new LoreMasterAgent.
func NewLoreMasterAgent() *LoreMasterAgent {
	lm := &LoreMasterAgent{}

	base := agent.NewAgent(agent.AgentConfig{
		Name:         "LoreMaster",
		Description:  "Validates story plots for narrative coherence and consistency with world lore.",
		Capabilities: []string{"lore_validation", "narrative_analysis"},
		Executor:     agent.ExecutorFunc(lm.execute),
	})
	lm.BaseAgent = base

	return lm
}

func (lm *LoreMasterAgent) execute(ctx context.Context, a agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// In a real implementation, this would analyze the input narrative.
	// Here, we return a mock score.
	result := &LoreMasterResult{
		CoherenceScore: 0.88, // Mock score
		Feedback:       "The introduction of the rogue dragon feels consistent with the ancient prophecies.",
	}

	return &agent.AgentOutput{
		TaskID: input.TaskID,
		Result: result,
	}, nil
}

// --- MusicTheoryAgent ---

// MusicTheoryResult is the output of the MusicTheoryAgent.
type MusicTheoryResult struct {
	HarmonicComplexity float64
	MelodicNovelty     float64
	Feedback           string
}

// MusicTheoryAgent is a peer agent that analyzes musical compositions.
type MusicTheoryAgent struct {
	*agent.BaseAgent
}

// NewMusicTheoryAgent creates a new MusicTheoryAgent.
func NewMusicTheoryAgent() *MusicTheoryAgent {
	mt := &MusicTheoryAgent{}

	base := agent.NewAgent(agent.AgentConfig{
		Name:         "MusicTheory",
		Description:  "Analyzes musical pieces for harmonic complexity and melodic novelty.",
		Capabilities: []string{"music_analysis", "harmonic_evaluation"},
		Executor:     agent.ExecutorFunc(mt.execute),
	})
	mt.BaseAgent = base

	return mt
}

func (mt *MusicTheoryAgent) execute(ctx context.Context, a agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// In a real implementation, this would perform music analysis.
	// Here, we return mock scores.
	result := &MusicTheoryResult{
		HarmonicComplexity: 0.75, // Mock score
		MelodicNovelty:     0.92, // Mock score
		Feedback:           "The use of modal interchange in the bridge is highly effective.",
	}

	return &agent.AgentOutput{
		TaskID: input.TaskID,
		Result: result,
	}, nil
}
