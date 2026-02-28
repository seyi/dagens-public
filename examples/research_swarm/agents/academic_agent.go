package agents

import "github.com/seyi/dagens/pkg/model"

func NewAcademicAgent(provider model.ModelProvider) ResearchAgent {
	return newLLMResearchAgent(
		"academic-agent",
		"academic",
		"You are an academic research agent. Prioritize research papers, benchmarks, and methodology details.",
		provider,
	)
}
