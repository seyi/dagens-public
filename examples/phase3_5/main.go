//go:build ignore

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/agents"
	"github.com/seyi/dagens/pkg/model"
	"github.com/seyi/dagens/pkg/tools"
)

// Example 1: Research Pipeline with Sequential LLM Agents
func Example1_ResearchPipeline() {
	ctx := context.Background()

	// Setup
	modelProvider := model.NewOpenAIProvider("gpt-4", "your-api-key")
	toolRegistry := tools.NewToolRegistry()
	toolRegistry.RegisterTool(tools.NewWebSearchTool())

	// Create research agent
	researchAgent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "researcher",
		ModelName:   "gpt-4",
		Instruction: "Research the given topic thoroughly using web search",
		Tools:       []string{"web_search"},
		Temperature: 0.7,
	}, modelProvider, toolRegistry)

	// Create summarization agent
	summarizeAgent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "summarizer",
		ModelName:   "gpt-4",
		Instruction: "Summarize research findings into key points",
		Temperature: 0.5,
	}, modelProvider, toolRegistry)

	// Create formatting agent
	formatAgent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "formatter",
		ModelName:   "gpt-4",
		Instruction: "Format summary as professional report with sections",
		Temperature: 0.3,
	}, modelProvider, toolRegistry)

	// Build sequential pipeline
	pipeline := agents.NewSequential().
		WithName("research-pipeline").
		Add(researchAgent).
		Add(summarizeAgent).
		Add(formatAgent).
		WithPassOutput(true).
		Build()

	// Execute
	output, err := pipeline.Execute(ctx, &agent.AgentInput{
		Instruction: "Research the latest developments in quantum computing",
	})

	if err != nil {
		fmt.Printf("Pipeline failed: %v\n", err)
		return
	}

	fmt.Printf("Research Report:\n%s\n", output.Result)
	fmt.Printf("Execution Time: %.2f seconds\n", output.Metadata["execution_time"])
	fmt.Printf("Steps Completed: %d\n", output.Metadata["steps_completed"])
}

// Example 2: Multi-Model Consensus with Parallel Agents
func Example2_MultiModelConsensus() {
	ctx := context.Background()

	// Setup different model providers
	gpt4 := model.NewOpenAIProvider("gpt-4", "your-api-key")
	claude := model.NewAnthropicProvider("claude-3", "your-api-key")
	gemini := model.NewGoogleProvider("gemini-pro", "your-api-key")

	// Create agents for each model
	gpt4Agent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "gpt4-classifier",
		ModelName:   "gpt-4",
		Instruction: "Classify the sentiment as positive, negative, or neutral",
		Temperature: 0.3,
	}, gpt4, nil)

	claudeAgent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "claude-classifier",
		ModelName:   "claude-3",
		Instruction: "Classify the sentiment as positive, negative, or neutral",
		Temperature: 0.3,
	}, claude, nil)

	geminiAgent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "gemini-classifier",
		ModelName:   "gemini-pro",
		Instruction: "Classify the sentiment as positive, negative, or neutral",
		Temperature: 0.3,
	}, gemini, nil)

	// Create parallel agent with vote aggregation
	ensemble := agents.NewParallel(agents.VoteAggregation).
		WithName("sentiment-ensemble").
		Add(gpt4Agent).
		Add(claudeAgent).
		Add(geminiAgent).
		WithMinSuccessful(2).  // At least 2 must agree
		Build()

	// Execute
	output, err := ensemble.Execute(ctx, &agent.AgentInput{
		Instruction: "The product exceeded my expectations and the customer service was excellent!",
	})

	if err != nil {
		fmt.Printf("Ensemble failed: %v\n", err)
		return
	}

	fmt.Printf("Consensus Sentiment: %s\n", output.Result)
	fmt.Printf("Models Successful: %d/3\n", output.Metadata["successful"])
	fmt.Printf("Execution Time: %.2f seconds\n", output.Metadata["execution_time"])
}

// Example 3: Iterative Refinement with Loop Agent
func Example3_IterativeRefinement() {
	ctx := context.Background()

	// Setup
	modelProvider := model.NewOpenAIProvider("gpt-4", "your-api-key")
	toolRegistry := tools.NewToolRegistry()

	// Create refiner agent
	refinerAgent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "refiner",
		ModelName:   "gpt-4",
		Instruction: "Improve the text quality, grammar, and clarity. Return quality_score (0-1) in metadata.",
		Temperature: 0.7,
	}, modelProvider, toolRegistry)

	// Create loop agent with quality threshold
	refinementLoop := agents.NewLoop(refinerAgent).
		WithName("quality-refinement").
		Until(func(output *agent.AgentOutput, iteration int) bool {
			// Stop when quality score >= 0.9
			if score, ok := output.Metadata["quality_score"].(float64); ok {
				return score >= 0.9
			}
			return false
		}).
		MaxIterations(5).
		AccumulateAll().  // Keep history of refinements
		Build()

	// Execute
	output, err := refinementLoop.Execute(ctx, &agent.AgentInput{
		Instruction: "The quick brown fox jump over the lazy dog. Its a beautiful day today.",
	})

	if err != nil {
		fmt.Printf("Refinement failed: %v\n", err)
		return
	}

	fmt.Printf("Final Text: %s\n", output.Result)
	fmt.Printf("Iterations: %d\n", output.Metadata["iterations"])
	fmt.Printf("All Versions:\n")

	if allResults, ok := output.Result.([]interface{}); ok {
		for i, result := range allResults {
			fmt.Printf("  Version %d: %s\n", i+1, result)
		}
	}
}

// Example 4: Complex Workflow - All Agent Types Combined
func Example4_ComplexWorkflow() {
	ctx := context.Background()

	// Setup
	modelProvider := model.NewOpenAIProvider("gpt-4", "your-api-key")
	toolRegistry := tools.NewToolRegistry()
	toolRegistry.RegisterTool(tools.NewWebSearchTool())
	toolRegistry.RegisterTool(tools.NewDatabaseTool())

	// Stage 1: Parallel data collection
	webAgent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "web-researcher",
		Instruction: "Search web for information",
		Tools:       []string{"web_search"},
	}, modelProvider, toolRegistry)

	dbAgent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "database-analyst",
		Instruction: "Query database for historical data",
		Tools:       []string{"database"},
	}, modelProvider, toolRegistry)

	dataCollection := agents.NewParallel(agents.AllAggregation).
		WithName("data-collection").
		Add(webAgent).
		Add(dbAgent).
		Build()

	// Stage 2: Analysis with loop for convergence
	analysisAgent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "analyst",
		Instruction: "Analyze data and provide insights",
	}, modelProvider, toolRegistry)

	analysisLoop := agents.NewLoop(analysisAgent).
		WithName("analysis-loop").
		Until(agents.ScoreThresholdCondition("confidence", 0.85)).
		MaxIterations(3).
		Build()

	// Stage 3: Parallel review by multiple reviewers
	reviewer1 := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "technical-reviewer",
		Instruction: "Review analysis for technical accuracy",
	}, modelProvider, toolRegistry)

	reviewer2 := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "business-reviewer",
		Instruction: "Review analysis for business value",
	}, modelProvider, toolRegistry)

	review := agents.NewParallel(agents.ConcatAggregation).
		WithName("peer-review").
		Add(reviewer1).
		Add(reviewer2).
		Build()

	// Stage 4: Final synthesis
	synthesisAgent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "synthesizer",
		Instruction: "Synthesize all inputs into final comprehensive report",
	}, modelProvider, toolRegistry)

	// Combine all stages into master pipeline
	masterPipeline := agents.NewSequential().
		WithName("comprehensive-analysis").
		Add(dataCollection).
		Add(analysisLoop).
		Add(review).
		Add(synthesisAgent).
		WithPassOutput(true).
		Build()

	// Execute
	startTime := time.Now()
	output, err := masterPipeline.Execute(ctx, &agent.AgentInput{
		Instruction: "Analyze market trends for electric vehicles in 2024",
	})

	if err != nil {
		fmt.Printf("Workflow failed: %v\n", err)
		return
	}

	fmt.Printf("=== Comprehensive Analysis Complete ===\n")
	fmt.Printf("Total Execution Time: %v\n", time.Since(startTime))
	fmt.Printf("Steps Completed: %d\n", output.Metadata["steps_completed"])
	fmt.Printf("\nFinal Report:\n%s\n", output.Result)
}

// Example 5: ReAct Pattern for Complex Problem Solving
func Example5_ReActPattern() {
	ctx := context.Background()

	// Setup
	modelProvider := model.NewOpenAIProvider("gpt-4", "your-api-key")
	toolRegistry := tools.NewToolRegistry()
	toolRegistry.RegisterTool(tools.NewCalculatorTool())
	toolRegistry.RegisterTool(tools.NewWebSearchTool())
	toolRegistry.RegisterTool(tools.NewWeatherTool())

	// Create ReAct agent
	reactAgent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:          "react-solver",
		ModelName:     "gpt-4",
		Instruction:   "Solve complex problems step by step using available tools",
		Tools:         []string{"calculator", "web_search", "weather"},
		UseReAct:      true,
		MaxIterations: 10,
		Temperature:   0.7,
	}, modelProvider, toolRegistry)

	// Execute complex task
	output, err := reactAgent.Execute(ctx, &agent.AgentInput{
		Instruction: `What is the population density of Tokyo compared to New York City?
		First find their populations and areas, then calculate and compare.`,
	})

	if err != nil {
		fmt.Printf("ReAct execution failed: %v\n", err)
		return
	}

	fmt.Printf("Final Answer: %s\n", output.Result)
	fmt.Printf("Iterations: %d\n", output.Metadata["iterations"])
	fmt.Printf("\nReasoning Steps:\n")

	if conversation, ok := output.Metadata["conversation"].([]string); ok {
		for i, step := range conversation {
			fmt.Printf("Step %d: %s\n", i+1, step)
		}
	}
}

// Example 6: Parallel Agent Race (First Response)
func Example6_ParallelRace() {
	ctx := context.Background()

	// Setup different speed providers (simulated)
	fastModel := model.NewOpenAIProvider("gpt-3.5-turbo", "key")
	mediumModel := model.NewOpenAIProvider("gpt-4", "key")
	slowModel := model.NewAnthropicProvider("claude-3-opus", "key")

	// Create agents
	fastAgent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "fast-responder",
		ModelName:   "gpt-3.5-turbo",
		Instruction: "Answer the question concisely",
	}, fastModel, nil)

	mediumAgent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "balanced-responder",
		ModelName:   "gpt-4",
		Instruction: "Answer the question with good detail",
	}, mediumModel, nil)

	slowAgent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "thorough-responder",
		ModelName:   "claude-3-opus",
		Instruction: "Answer the question comprehensively",
	}, slowModel, nil)

	// Create race - first to complete wins
	race := agents.NewParallel(agents.FirstAggregation).
		WithName("speed-race").
		Add(fastAgent).
		Add(mediumAgent).
		Add(slowAgent).
		WithTimeout(30 * time.Second).
		Build()

	// Execute
	startTime := time.Now()
	output, err := race.Execute(ctx, &agent.AgentInput{
		Instruction: "What is the capital of France?",
	})
	elapsed := time.Since(startTime)

	if err != nil {
		fmt.Printf("Race failed: %v\n", err)
		return
	}

	fmt.Printf("Winner's Response: %s\n", output.Result)
	fmt.Printf("Response Time: %v\n", elapsed)
	fmt.Printf("Pattern: %s\n", output.Metadata["pattern"])
}

// Example 7: Conditional Branching
func Example7_ConditionalBranching() {
	ctx := context.Background()

	// Setup
	modelProvider := model.NewOpenAIProvider("gpt-4", "your-api-key")

	// Classifier agent
	classifierAgent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "classifier",
		Instruction: "Classify the complexity as 'simple' or 'complex'. Set complexity in metadata.",
	}, modelProvider, nil)

	// Simple handler
	simpleAgent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "simple-handler",
		Instruction: "Provide a brief, straightforward answer",
	}, modelProvider, nil)

	// Complex handler
	complexAgent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "complex-handler",
		Instruction: "Provide detailed analysis with examples",
		UseReAct:    true,
	}, modelProvider, nil)

	// Conditional branching
	conditionalAgent := agents.NewConditional(func(output *agent.AgentOutput) bool {
		complexity := output.Metadata["complexity"].(string)
		return complexity == "simple"
	}).
		Then(simpleAgent).
		Else(complexAgent).
		Build()

	// Pipeline with conditional
	pipeline := agents.NewSequential().
		WithName("adaptive-pipeline").
		Add(classifierAgent).
		Add(conditionalAgent).
		WithPassOutput(true).
		Build()

	// Execute with simple question
	output1, _ := pipeline.Execute(ctx, &agent.AgentInput{
		Instruction: "What is 2 + 2?",
	})
	fmt.Printf("Simple Question Response: %s\n", output1.Result)

	// Execute with complex question
	output2, _ := pipeline.Execute(ctx, &agent.AgentInput{
		Instruction: "Explain quantum entanglement and its implications for quantum computing",
	})
	fmt.Printf("Complex Question Response: %s\n", output2.Result)
}

// Example 8: Custom Reduce Aggregation
func Example8_CustomReduce() {
	ctx := context.Background()

	// Setup
	modelProvider := model.NewOpenAIProvider("gpt-4", "your-api-key")

	// Create multiple estimator agents
	estimator1 := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "estimator-1",
		Instruction: "Estimate project duration in weeks. Return 'duration' and 'confidence' in metadata.",
	}, modelProvider, nil)

	estimator2 := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "estimator-2",
		Instruction: "Estimate project duration in weeks. Return 'duration' and 'confidence' in metadata.",
	}, modelProvider, nil)

	estimator3 := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "estimator-3",
		Instruction: "Estimate project duration in weeks. Return 'duration' and 'confidence' in metadata.",
	}, modelProvider, nil)

	// Custom reduce: weighted average by confidence
	customReduce := func(results []*agent.AgentOutput) (interface{}, error) {
		totalWeight := 0.0
		weightedSum := 0.0

		for _, result := range results {
			duration := result.Metadata["duration"].(float64)
			confidence := result.Metadata["confidence"].(float64)

			weightedSum += duration * confidence
			totalWeight += confidence
		}

		avgDuration := weightedSum / totalWeight

		return map[string]interface{}{
			"estimated_duration": avgDuration,
			"confidence_level":   totalWeight / float64(len(results)),
			"num_estimates":      len(results),
		}, nil
	}

	// Create parallel with custom reduce
	ensemble := agents.NewParallel(agents.ReduceAggregation).
		WithName("estimation-ensemble").
		Add(estimator1).
		Add(estimator2).
		Add(estimator3).
		WithReduceFunc(customReduce).
		Build()

	// Execute
	output, err := ensemble.Execute(ctx, &agent.AgentInput{
		Instruction: "Estimate duration for building an e-commerce website with React and Node.js",
	})

	if err != nil {
		fmt.Printf("Estimation failed: %v\n", err)
		return
	}

	result := output.Result.(map[string]interface{})
	fmt.Printf("Estimated Duration: %.1f weeks\n", result["estimated_duration"])
	fmt.Printf("Confidence Level: %.2f\n", result["confidence_level"])
	fmt.Printf("Number of Estimates: %d\n", result["num_estimates"])
}

// Helper function to run all examples
func RunAllExamples() {
	fmt.Println("=== Phase 3.5 Agent Types Examples ===\n")

	examples := []struct {
		name string
		fn   func()
	}{
		{"Research Pipeline", Example1_ResearchPipeline},
		{"Multi-Model Consensus", Example2_MultiModelConsensus},
		{"Iterative Refinement", Example3_IterativeRefinement},
		{"Complex Workflow", Example4_ComplexWorkflow},
		{"ReAct Pattern", Example5_ReActPattern},
		{"Parallel Race", Example6_ParallelRace},
		{"Conditional Branching", Example7_ConditionalBranching},
		{"Custom Reduce", Example8_CustomReduce},
	}

	for _, example := range examples {
		fmt.Printf("\n--- %s ---\n", example.name)
		example.fn()
	}
}
