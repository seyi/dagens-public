// Package agents provides examples for agent implementations.
package agents

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/agentic"
	"github.com/stretchr/testify/assert"
)

// TestExampleAethelred showcases the Adaptive Dungeon Master agent.
func TestExampleAethelred(t *testing.T) {
	ctx := context.Background()

	// 1. SETUP: Create the base agent and its dependencies (peer agent).
	loreMaster := NewLoreMasterAgent()
	baseAethelred := NewAethelredAgent(loreMaster)
	baseAethelred.ResetExecCount()

	// 2. WRAP: Add agentic capabilities like self-healing and monitoring.
	// We configure it to retry up to 3 times on failure.
	agenticAethelred := WithAgenticCapabilities(baseAethelred,
		WithMaxRetries(3),
	)

	// 3. EXECUTE: Run the agent with an input that forces 2 failures to demo self-healing.
	fmt.Println("\n--- Running Aethelred Agent (expecting 2 retries) ---")
	input := &agent.AgentInput{
		TaskID:      uuid.NewString(),
		Instruction: "Generate the next chapter of the adventure.",
		Context: map[string]interface{}{
			"force_fail_count": 2, // This will make the first 2 attempts fail.
		},
	}
	output, err := agenticAethelred.Execute(ctx, input)
	assert.NoError(t, err, "Aethelred execution should succeed after retries")
	assert.NotNil(t, output)
	fmt.Printf("Aethelred execution successful. Output: %s\n", output.Result)

	// 4. MONITOR & FEEDBACK: Record metrics and feedback based on the output.
	fmt.Println("--- Recording Metrics and Feedback for Aethelred ---")

	// Record the main outcome score. Let's say it was a great chapter.
	agenticAethelred.RecordOutcome(0.95)

	// Record domain-specific metrics.
	agenticAethelred.RecordDomainMetric(DomainPlayerAgencyScore, 0.8)
	agenticAethelred.RecordDomainMetric(DomainChallengeBalance, 0.85)

	// Use peer feedback to inform a domain metric.
	if meta := output.Metadata; meta != nil {
		if loreResult, ok := meta["lore_master_feedback"].(*LoreMasterResult); ok {
			agenticAethelred.RecordDomainMetric(DomainNarrativeCoherence, loreResult.CoherenceScore)
			fmt.Printf("Recorded narrative coherence from peer: %.2f\n", loreResult.CoherenceScore)
		}
	}

	// Record human feedback.
	agenticAethelred.RecordFeedback(ctx, input.TaskID, 1.0, agentic.FeedbackSourceHuman, "The players loved it!")

	// Verify metrics were recorded
	mc := agenticAethelred.Monitoring()
	agency, ok := mc.GetDomainMetric(DomainPlayerAgencyScore)
	assert.True(t, ok)
	assert.Equal(t, 0.8, agency)

	fmt.Println("--- Aethelred Example Finished ---")
}

// TestExampleGuardian showcases the Digital Health Twin agent.
func TestExampleGuardian(t *testing.T) {
	ctx := context.Background()

	// 1. SETUP: Create the agent and its environment dependency (the wearable client).
	mockClient := &MockWearableClient{IsConnected: true, BatteryLvl: 0.99}
	baseGuardian := NewGuardianAgent(mockClient)
	baseGuardian.ResetExecCount()

	// 2. WRAP: Add agentic capabilities.
	agenticGuardian := WithAgenticCapabilities(baseGuardian, WithMaxRetries(4))

	// 3. EXECUTE: Simulate an execution that fails to send a notification twice.
	fmt.Println("\n--- Running Guardian Agent (expecting 2 retries) ---")
	input := &agent.AgentInput{
		TaskID:      uuid.NewString(),
		Instruction: "Monitor patient vitals for anomalies.",
		Context: map[string]interface{}{
			"force_fail_count": 2,
		},
	}
	output, err := agenticGuardian.Execute(ctx, input)
	assert.NoError(t, err, "Guardian execution should succeed after retries")
	assert.NotNil(t, output)
	fmt.Printf("Guardian execution successful. Prediction: %s\n", output.Result)

	// 4. MONITOR & FEEDBACK: Record metrics after the fact.
	fmt.Println("--- Recording Metrics and Feedback for Guardian ---")

	// Environment quality was stored in output metadata
	if meta := output.Metadata; meta != nil {
		if connQuality, ok := meta["connection_quality"].(float64); ok {
			fmt.Printf("Environment connection quality: %.2f\n", connQuality)
		}
	}

	// Record outcome (e.g., the alert was acknowledged and averted an event).
	agenticGuardian.RecordOutcome(1.0)
	agenticGuardian.RecordDomainMetric(DomainAlertsAcknowledged, 1.0)
	agenticGuardian.RecordDomainMetric(DomainPredictionHorizon, 15.0) // 15 minutes
	agenticGuardian.RecordDomainMetric(DomainFalsePositiveRate, 0.01)

	// Record feedback from a clinician.
	agenticGuardian.RecordFeedback(ctx, input.TaskID, 1.0, agentic.FeedbackSourceHuman, "Alert was accurate and timely. Confirmed by patient.")

	// Verify metrics
	mc := agenticGuardian.Monitoring()
	horizon, ok := mc.GetDomainMetric(DomainPredictionHorizon)
	assert.True(t, ok)
	assert.Equal(t, 15.0, horizon)

	fmt.Println("--- Guardian Example Finished ---")
}

// TestExampleMaestro showcases the Generative Symphony Composer agent.
func TestExampleMaestro(t *testing.T) {
	ctx := context.Background()

	// 1. SETUP: Create the agent and its peer dependency.
	theoryAgent := NewMusicTheoryAgent()
	baseMaestro := NewMaestroAgent(theoryAgent)
	baseMaestro.ResetExecCount()

	// 2. WRAP: Add agentic capabilities.
	agenticMaestro := WithAgenticCapabilities(baseMaestro, WithMaxRetries(2))

	// 3. EXECUTE: Run with one simulated failure.
	fmt.Println("\n--- Running Maestro Agent (expecting 1 retry) ---")
	input := &agent.AgentInput{
		TaskID:      uuid.NewString(),
		Instruction: "Compose a short piano piece in the style of Debussy.",
		Context: map[string]interface{}{
			"force_fail_count": 1,
		},
	}

	output, err := agenticMaestro.Execute(ctx, input)
	assert.NoError(t, err, "Maestro execution should succeed after retry")
	assert.NotNil(t, output)
	fmt.Printf("Maestro execution successful. Composition: %s\n", output.Result)

	// 4. MONITOR & FEEDBACK
	fmt.Println("--- Recording Metrics and Feedback for Maestro ---")
	agenticMaestro.RecordOutcome(0.85) // Good, but not perfect.

	// Record domain metrics, using peer feedback.
	agenticMaestro.RecordDomainMetric(DomainStylisticSimilarity, 0.9)
	agenticMaestro.RecordDomainMetric(DomainCompositionLength, 180.0) // 3 minutes

	if meta := output.Metadata; meta != nil {
		if analysis, ok := meta["music_theory_analysis"].(*MusicTheoryResult); ok {
			agenticMaestro.RecordDomainMetric(DomainHarmonicComplexity, analysis.HarmonicComplexity)
			agenticMaestro.RecordDomainMetric(DomainMelodicNovelty, analysis.MelodicNovelty)
			fmt.Printf("Recorded metrics from peer: Complexity=%.2f, Novelty=%.2f\n", analysis.HarmonicComplexity, analysis.MelodicNovelty)
		}
	}

	// Record automated feedback (e.g., from a discriminator model).
	agenticMaestro.RecordFeedback(ctx, input.TaskID, 0.88, agentic.FeedbackSourceAutomated, "GAN Discriminator Score: 0.88")

	// Verify metrics
	mc := agenticMaestro.Monitoring()
	similarity, ok := mc.GetDomainMetric(DomainStylisticSimilarity)
	assert.True(t, ok)
	assert.Equal(t, 0.9, similarity)

	fmt.Println("--- Maestro Example Finished ---")
}

// TestExampleEnvironmentFailure demonstrates Guardian handling environment issues.
func TestExampleEnvironmentFailure(t *testing.T) {
	ctx := context.Background()

	// Setup with a disconnected wearable (environment problem)
	mockClient := &MockWearableClient{IsConnected: false, BatteryLvl: 0.05}
	baseGuardian := NewGuardianAgent(mockClient)
	baseGuardian.ResetExecCount()

	agenticGuardian := WithAgenticCapabilities(baseGuardian, WithMaxRetries(2))

	fmt.Println("\n--- Running Guardian Agent with disconnected wearable ---")
	input := &agent.AgentInput{
		TaskID:      uuid.NewString(),
		Instruction: "Monitor patient vitals for anomalies.",
	}

	// This should fail because the environment (wearable) is not available
	output, err := agenticGuardian.Execute(ctx, input)
	assert.Error(t, err, "Guardian should fail when environment is unavailable")
	assert.Nil(t, output)
	assert.Contains(t, err.Error(), "max retries")

	fmt.Printf("Guardian correctly failed due to environment issue: %v\n", err)
	fmt.Println("--- Environment Failure Example Finished ---")
}
