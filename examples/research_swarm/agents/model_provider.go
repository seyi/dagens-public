package agents

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/seyi/dagens/pkg/model"
	"github.com/seyi/dagens/pkg/models"
	openai "github.com/seyi/dagens/pkg/models/openai"
)

const (
	openRouterBaseURL = "https://openrouter.ai/api/v1"
)

// BuildDemoModelProviderFromEnv builds a model.ModelProvider used by internal LlmAgents.
// It prefers real LLM credentials; if absent, it falls back to a deterministic mock provider
// unless RESEARCH_SWARM_REQUIRE_LLM=true.
func BuildDemoModelProviderFromEnv() (model.ModelProvider, func() error, bool, error) {
	if apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); apiKey != "" {
		modelName := envOrDefault("RESEARCH_SWARM_MODEL", "gpt-4o-mini")
		provider, err := openai.NewProvider(openai.Config{Model: modelName, APIKey: apiKey})
		if err != nil {
			return nil, nil, false, err
		}
		return &chatModelAdapter{provider: provider}, provider.Close, true, nil
	}

	if apiKey := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")); apiKey != "" {
		modelName := envOrDefault("RESEARCH_SWARM_MODEL", "qwen/qwen3.5-plus-02-15")
		baseURL := envOrDefault("OPENROUTER_BASE_URL", openRouterBaseURL)
		provider, err := openai.NewProvider(openai.Config{Model: modelName, APIKey: apiKey, BaseURL: baseURL})
		if err != nil {
			return nil, nil, false, err
		}
		return &chatModelAdapter{provider: provider}, provider.Close, true, nil
	}

	if strings.EqualFold(strings.TrimSpace(os.Getenv("RESEARCH_SWARM_REQUIRE_LLM")), "true") {
		return nil, nil, false, fmt.Errorf("no API key found: set OPENAI_API_KEY or OPENROUTER_API_KEY")
	}

	return &mockModelProvider{}, func() error { return nil }, false, nil
}

type chatModelAdapter struct {
	provider models.ModelProvider
}

func (a *chatModelAdapter) Name() string {
	return a.provider.Name()
}

func (a *chatModelAdapter) Generate(ctx context.Context, input *model.ModelInput) (*model.ModelOutput, error) {
	req := &models.ModelRequest{
		Messages: []models.Message{
			{Role: models.RoleUser, Content: input.Prompt},
		},
		Temperature:     input.Temperature,
		MaxOutputTokens: 500,
	}
	resp, err := a.provider.Chat(ctx, req)
	if err != nil {
		return nil, err
	}

	return &model.ModelOutput{
		Text:         resp.Message.Content,
		TokensUsed:   resp.Usage.TotalTokens,
		FinishReason: resp.FinishReason,
	}, nil
}

type mockModelProvider struct{}

func (m *mockModelProvider) Name() string {
	return "mock-research-model"
}

func (m *mockModelProvider) Generate(_ context.Context, input *model.ModelInput) (*model.ModelOutput, error) {
	prompt := strings.ToLower(input.Prompt)

	resp := `{"summary":"Research indicates stronger coordination, memory, and evaluation patterns.","trends":["modular planning","stateful orchestration","self-verification loops"]}`
	if strings.Contains(prompt, "github") {
		resp = `{"summary":"Repository activity centers on orchestration frameworks and observability-first pipelines.","trends":["workflow observability","stateful orchestration","modular planning"]}`
	}
	if strings.Contains(prompt, "academic") {
		resp = `{"summary":"Academic work emphasizes planner modularity, memory depth, and reliability checks.","trends":["modular planning","long-context memory","self-verification loops"]}`
	}
	if strings.Contains(prompt, "blog") || strings.Contains(prompt, "industry") {
		resp = `{"summary":"Practitioner write-ups focus on execution controls and policy-aware agent operations.","trends":["human-in-the-loop","stateful orchestration","self-verification loops"]}`
	}

	return &model.ModelOutput{Text: resp, TokensUsed: 120, FinishReason: "stop"}, nil
}

func envOrDefault(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}
