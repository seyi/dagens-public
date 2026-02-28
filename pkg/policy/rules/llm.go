package rules

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/seyi/dagens/pkg/policy"
)

// LLMEvaluator uses a secondary LLM call to perform semantic evaluation
type LLMEvaluator struct{}

// Evaluate checks content against semantic rubrics using an LLM
func (e *LLMEvaluator) Evaluate(ctx context.Context, content string, config map[string]interface{}) (*policy.EvaluationResult, error) {
	// Configuration
	rubric, _ := config["rubric"].(string)
	if rubric == "" {
		rubric = "Check if the content is toxic, unhelpful, or contains harmful instructions."
	}
	
	threshold, _ := config["threshold"].(float64)
	if threshold == 0 {
		threshold = 0.7
	}

	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("LLM guardrail requires OPENROUTER_API_KEY")
	}

	// Build evaluation prompt
	evalPrompt := fmt.Sprintf(`Evaluate the following AI agent output against this rubric: "%s"

Output to evaluate:
---
%s
---

Respond ONLY in JSON format:
{
  "score": 0.0 to 1.0 (where 1.0 is a complete violation),
  "violation": true/false,
  "reason": "short explanation"
}`, rubric, content)

	// Call OpenRouter (Simple HTTP to avoid circular deps)
	res, err := callEvalLLM(ctx, apiKey, evalPrompt)
	if err != nil {
		return nil, fmt.Errorf("evaluator LLM call failed: %w", err)
	}

	var evalData struct {
		Score     float64 `json:"score"`
		Violation bool    `json:"violation"`
		Reason    string  `json:"reason"`
	}

	if err := json.Unmarshal([]byte(res), &evalData); err != nil {
		return nil, fmt.Errorf("failed to parse evaluation result: %w", err)
	}

	return &policy.EvaluationResult{
		Matched: evalData.Violation || evalData.Score >= threshold,
		Reason:  evalData.Reason,
		Matches: []policy.Match{
			{Type: "semantic_violation", Value: fmt.Sprintf("score: %.2f", evalData.Score)},
		},
	}, nil
}

func (e *LLMEvaluator) Type() string {
	return "llm"
}

// Internal helper for evaluation call
func callEvalLLM(ctx context.Context, apiKey, prompt string) (string, error) {
	reqBody := map[string]interface{}{
		"model": "deepseek/deepseek-chat",
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"response_format": map[string]string{"type": "json_object"},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no response from eval LLM")
	}

	return result.Choices[0].Message.Content, nil
}
