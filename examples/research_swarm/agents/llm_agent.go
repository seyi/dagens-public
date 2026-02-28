package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	coreagent "github.com/seyi/dagens/pkg/agent"
	coreagents "github.com/seyi/dagens/pkg/agents"
	"github.com/seyi/dagens/pkg/model"
)

type llmResponse struct {
	Summary string   `json:"summary"`
	Trends  []string `json:"trends"`
}

// LLMResearchAgent wraps the internal LlmAgent from pkg/agents.
type LLMResearchAgent struct {
	name   string
	source string
	runner *coreagents.LlmAgent
}

func (a *LLMResearchAgent) Name() string {
	return a.name
}

func (a *LLMResearchAgent) Run(ctx context.Context, query string) (Finding, error) {
	fmt.Printf("[%s] starting research on: %s\n", a.name, query)

	instruction := strings.TrimSpace(`
Return a compact JSON object only:
{"summary":"...","trends":["trend1","trend2","trend3"]}

Constraints:
- summary: max 35 words
- trends: exactly 3 short phrases
- no markdown fences
`) + "\n\nQuery: " + query

	start := time.Now()
	output, err := a.runner.Execute(ctx, &coreagent.AgentInput{
		TaskID:      fmt.Sprintf("%s-%d", a.name, time.Now().UnixNano()),
		Instruction: instruction,
		MaxRetries:  1,
		Timeout:     40 * time.Second,
		Context:     map[string]interface{}{"query": query, "source": a.source},
	})
	if err != nil {
		return Finding{}, err
	}

	raw := fmt.Sprint(output.Result)
	parsed := parseLLMResponse(raw)
	if len(parsed.Trends) == 0 {
		parsed.Trends = []string{"multi-agent orchestration", "stateful workflows", "evaluation loops"}
	}
	if parsed.Summary == "" {
		parsed.Summary = strings.TrimSpace(raw)
	}

	fmt.Printf("[%s] completed in %s\n", a.name, time.Since(start).Round(time.Millisecond))
	return Finding{
		Agent:   a.name,
		Source:  a.source,
		Summary: parsed.Summary,
		Trends:  parsed.Trends,
	}, nil
}

func newLLMResearchAgent(name, source, roleInstruction string, provider model.ModelProvider) *LLMResearchAgent {
	runner := coreagents.NewLlmAgent(coreagents.LlmAgentConfig{
		Name:        name,
		Instruction: roleInstruction,
		Temperature: 0.2,
		UseReAct:    false,
	}, provider, nil)

	return &LLMResearchAgent{
		name:   name,
		source: source,
		runner: runner,
	}
}

func parseLLMResponse(raw string) llmResponse {
	clean := strings.TrimSpace(raw)
	clean = strings.TrimPrefix(clean, "```json")
	clean = strings.TrimPrefix(clean, "```")
	clean = strings.TrimSuffix(clean, "```")
	clean = strings.TrimSpace(clean)

	start := strings.Index(clean, "{")
	end := strings.LastIndex(clean, "}")
	if start >= 0 && end > start {
		clean = clean[start : end+1]
	}

	var out llmResponse
	if err := json.Unmarshal([]byte(clean), &out); err != nil {
		return llmResponse{Summary: strings.TrimSpace(raw)}
	}

	for i := range out.Trends {
		out.Trends[i] = strings.TrimSpace(out.Trends[i])
	}
	if len(out.Trends) > 3 {
		out.Trends = out.Trends[:3]
	}
	return out
}
