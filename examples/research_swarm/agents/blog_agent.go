package agents

import "github.com/seyi/dagens/pkg/model"

func NewBlogAgent(provider model.ModelProvider) ResearchAgent {
	return newLLMResearchAgent(
		"blog-agent",
		"industry-blogs",
		"You are an industry blog analysis agent. Focus on practical architecture shifts and adoption patterns.",
		provider,
	)
}
