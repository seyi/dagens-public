package openai

import "strings"

// ModelPricing captures pricing, capability, and context metadata for an OpenAI model.
type ModelPricing struct {
	PromptCostPer1K     float64
	CompletionCostPer1K float64
	ContextWindow       int
	MaxOutputTokens     int
	SupportsVision      bool
	SupportsJSONMode    bool
}

var pricingTable = map[string]ModelPricing{
	"gpt-4o": {
		PromptCostPer1K:     0.005,
		CompletionCostPer1K: 0.015,
		ContextWindow:       128000,
		MaxOutputTokens:     4096,
		SupportsVision:      true,
		SupportsJSONMode:    true,
	},
	"gpt-4o-2024-05-13": {
		PromptCostPer1K:     0.005,
		CompletionCostPer1K: 0.015,
		ContextWindow:       128000,
		MaxOutputTokens:     4096,
		SupportsVision:      true,
		SupportsJSONMode:    true,
	},
	"gpt-4o-mini": {
		PromptCostPer1K:     0.00015,
		CompletionCostPer1K: 0.0006,
		ContextWindow:       128000,
		MaxOutputTokens:     16384,
		SupportsVision:      true,
		SupportsJSONMode:    true,
	},
	"gpt-4-turbo": {
		PromptCostPer1K:     0.01,
		CompletionCostPer1K: 0.03,
		ContextWindow:       128000,
		MaxOutputTokens:     4096,
		SupportsVision:      true,
		SupportsJSONMode:    true,
	},
	"gpt-4-turbo-2024-04-09": {
		PromptCostPer1K:     0.01,
		CompletionCostPer1K: 0.03,
		ContextWindow:       128000,
		MaxOutputTokens:     4096,
		SupportsVision:      true,
		SupportsJSONMode:    true,
	},
	"gpt-3.5-turbo": {
		PromptCostPer1K:     0.001,
		CompletionCostPer1K: 0.002,
		ContextWindow:       16000,
		MaxOutputTokens:     4096,
		SupportsVision:      false,
		SupportsJSONMode:    true,
	},
	"gpt-3.5-turbo-0125": {
		PromptCostPer1K:     0.001,
		CompletionCostPer1K: 0.002,
		ContextWindow:       16000,
		MaxOutputTokens:     4096,
		SupportsVision:      false,
		SupportsJSONMode:    true,
	},
}

// LookupPricing returns pricing metadata for the supplied model name.
func LookupPricing(model string) (ModelPricing, bool) {
	if model == "" {
		return ModelPricing{}, false
	}

	normalized := strings.ToLower(model)
	if pricing, ok := pricingTable[normalized]; ok {
		return pricing, true
	}
	return ModelPricing{}, false
}

// EstimateCost calculates the USD estimate for the supplied token usage.
func EstimateCost(model string, promptTokens, completionTokens int) float64 {
	pricing, ok := LookupPricing(model)
	if !ok {
		return 0
	}

	promptCost := (float64(promptTokens) / 1000.0) * pricing.PromptCostPer1K
	completionCost := (float64(completionTokens) / 1000.0) * pricing.CompletionCostPer1K
	return promptCost + completionCost
}

// EstimateCostWithCache calculates cost considering context caching (cached tokens are 50% cheaper).
func EstimateCostWithCache(model string, promptTokens, completionTokens, cachedTokens int) float64 {
	pricing, ok := LookupPricing(model)
	if !ok {
		return 0
	}

	// Cached tokens cost 50% of regular prompt tokens
	uncachedPromptTokens := promptTokens - cachedTokens
	uncachedCost := (float64(uncachedPromptTokens) / 1000.0) * pricing.PromptCostPer1K
	cachedCost := (float64(cachedTokens) / 1000.0) * (pricing.PromptCostPer1K * 0.5)
	completionCost := (float64(completionTokens) / 1000.0) * pricing.CompletionCostPer1K

	return uncachedCost + cachedCost + completionCost
}
