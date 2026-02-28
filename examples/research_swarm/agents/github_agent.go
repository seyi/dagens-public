package agents

import "github.com/seyi/dagens/pkg/model"

func NewGitHubAgent(provider model.ModelProvider) ResearchAgent {
	return newLLMResearchAgent(
		"github-agent",
		"github",
		"You are a GitHub analysis agent. Focus on active repositories, architecture patterns, and tooling trends.",
		provider,
	)
}
