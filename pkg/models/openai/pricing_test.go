package openai

import (
	"testing"
)

func TestLookupPricing(t *testing.T) {
	tests := []struct {
		name          string
		model         string
		expectFound   bool
		expectVision  bool
		expectJSON    bool
		expectContext int
	}{
		{
			name:          "gpt-4o",
			model:         "gpt-4o",
			expectFound:   true,
			expectVision:  true,
			expectJSON:    true,
			expectContext: 128000,
		},
		{
			name:          "gpt-4o case insensitive",
			model:         "GPT-4O",
			expectFound:   true,
			expectVision:  true,
			expectJSON:    true,
			expectContext: 128000,
		},
		{
			name:          "gpt-4o-mini",
			model:         "gpt-4o-mini",
			expectFound:   true,
			expectVision:  true,
			expectJSON:    true,
			expectContext: 128000,
		},
		{
			name:          "gpt-3.5-turbo",
			model:         "gpt-3.5-turbo",
			expectFound:   true,
			expectVision:  false,
			expectJSON:    true,
			expectContext: 16000,
		},
		{
			name:        "unknown model",
			model:       "gpt-99-ultra",
			expectFound: false,
		},
		{
			name:        "empty model",
			model:       "",
			expectFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pricing, found := LookupPricing(tt.model)

			if found != tt.expectFound {
				t.Errorf("LookupPricing(%q) found = %v, want %v", tt.model, found, tt.expectFound)
			}

			if tt.expectFound {
				if pricing.SupportsVision != tt.expectVision {
					t.Errorf("model %q SupportsVision = %v, want %v", tt.model, pricing.SupportsVision, tt.expectVision)
				}
				if pricing.SupportsJSONMode != tt.expectJSON {
					t.Errorf("model %q SupportsJSONMode = %v, want %v", tt.model, pricing.SupportsJSONMode, tt.expectJSON)
				}
				if pricing.ContextWindow != tt.expectContext {
					t.Errorf("model %q ContextWindow = %d, want %d", tt.model, pricing.ContextWindow, tt.expectContext)
				}
				if pricing.PromptCostPer1K <= 0 {
					t.Errorf("model %q has invalid PromptCostPer1K: %f", tt.model, pricing.PromptCostPer1K)
				}
				if pricing.CompletionCostPer1K <= 0 {
					t.Errorf("model %q has invalid CompletionCostPer1K: %f", tt.model, pricing.CompletionCostPer1K)
				}
			}
		})
	}
}

func TestEstimateCost(t *testing.T) {
	tests := []struct {
		name             string
		model            string
		promptTokens     int
		completionTokens int
		expectedCost     float64
		tolerance        float64
	}{
		{
			name:             "gpt-4o basic",
			model:            "gpt-4o",
			promptTokens:     1000,
			completionTokens: 500,
			expectedCost:     0.005*1 + 0.015*0.5, // $0.005 + $0.0075 = $0.0125
			tolerance:        0.0001,
		},
		{
			name:             "gpt-4o-mini cheaper",
			model:            "gpt-4o-mini",
			promptTokens:     10000,
			completionTokens: 5000,
			expectedCost:     0.00015*10 + 0.0006*5, // $0.0015 + $0.003 = $0.0045
			tolerance:        0.0001,
		},
		{
			name:             "gpt-3.5-turbo",
			model:            "gpt-3.5-turbo",
			promptTokens:     5000,
			completionTokens: 2000,
			expectedCost:     0.001*5 + 0.002*2, // $0.005 + $0.004 = $0.009
			tolerance:        0.0001,
		},
		{
			name:             "unknown model returns zero",
			model:            "gpt-unknown",
			promptTokens:     1000,
			completionTokens: 500,
			expectedCost:     0,
			tolerance:        0.0001,
		},
		{
			name:             "zero tokens",
			model:            "gpt-4o",
			promptTokens:     0,
			completionTokens: 0,
			expectedCost:     0,
			tolerance:        0.0001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost := EstimateCost(tt.model, tt.promptTokens, tt.completionTokens)

			diff := cost - tt.expectedCost
			if diff < 0 {
				diff = -diff
			}

			if diff > tt.tolerance {
				t.Errorf("EstimateCost(%q, %d, %d) = %f, want %f (diff: %f)",
					tt.model, tt.promptTokens, tt.completionTokens, cost, tt.expectedCost, diff)
			}
		})
	}
}

func TestEstimateCostWithCache(t *testing.T) {
	tests := []struct {
		name             string
		model            string
		promptTokens     int
		completionTokens int
		cachedTokens     int
		expectedCost     float64
		tolerance        float64
	}{
		{
			name:             "gpt-4o with 50% cached",
			model:            "gpt-4o",
			promptTokens:     1000,
			completionTokens: 500,
			cachedTokens:     500,
			// Uncached: 500 * 0.005/1000 = 0.0025
			// Cached: 500 * 0.005/1000 * 0.5 = 0.00125
			// Completion: 500 * 0.015/1000 = 0.0075
			// Total: 0.0025 + 0.00125 + 0.0075 = 0.01125
			expectedCost: 0.01125,
			tolerance:    0.0001,
		},
		{
			name:             "gpt-4o with 100% cached",
			model:            "gpt-4o",
			promptTokens:     1000,
			completionTokens: 500,
			cachedTokens:     1000,
			// Uncached: 0
			// Cached: 1000 * 0.005/1000 * 0.5 = 0.0025
			// Completion: 500 * 0.015/1000 = 0.0075
			// Total: 0.0025 + 0.0075 = 0.01
			expectedCost: 0.01,
			tolerance:    0.0001,
		},
		{
			name:             "gpt-4o with no cache",
			model:            "gpt-4o",
			promptTokens:     1000,
			completionTokens: 500,
			cachedTokens:     0,
			// Should match regular EstimateCost
			expectedCost: 0.0125,
			tolerance:    0.0001,
		},
		{
			name:             "gpt-4o-mini with cache",
			model:            "gpt-4o-mini",
			promptTokens:     10000,
			completionTokens: 5000,
			cachedTokens:     8000,
			// Uncached: 2000 * 0.00015/1000 = 0.0003
			// Cached: 8000 * 0.00015/1000 * 0.5 = 0.0006
			// Completion: 5000 * 0.0006/1000 = 0.003
			// Total: 0.0003 + 0.0006 + 0.003 = 0.0039
			expectedCost: 0.0039,
			tolerance:    0.0001,
		},
		{
			name:             "unknown model returns zero",
			model:            "gpt-unknown",
			promptTokens:     1000,
			completionTokens: 500,
			cachedTokens:     500,
			expectedCost:     0,
			tolerance:        0.0001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost := EstimateCostWithCache(tt.model, tt.promptTokens, tt.completionTokens, tt.cachedTokens)

			diff := cost - tt.expectedCost
			if diff < 0 {
				diff = -diff
			}

			if diff > tt.tolerance {
				t.Errorf("EstimateCostWithCache(%q, %d, %d, %d) = %f, want %f (diff: %f)",
					tt.model, tt.promptTokens, tt.completionTokens, tt.cachedTokens,
					cost, tt.expectedCost, diff)
			}
		})
	}
}

// TestCacheSavings verifies that cached tokens are indeed 50% cheaper
func TestCacheSavings(t *testing.T) {
	model := "gpt-4o"
	promptTokens := 1000
	completionTokens := 500
	cachedTokens := 1000

	// Cost without cache
	noCacheCost := EstimateCost(model, promptTokens, completionTokens)

	// Cost with full cache
	fullCacheCost := EstimateCostWithCache(model, promptTokens, completionTokens, cachedTokens)

	// The difference should be 50% of the prompt cost
	pricing, _ := LookupPricing(model)
	expectedSavings := (float64(promptTokens) / 1000.0) * pricing.PromptCostPer1K * 0.5

	actualSavings := noCacheCost - fullCacheCost

	if actualSavings < expectedSavings-0.0001 || actualSavings > expectedSavings+0.0001 {
		t.Errorf("Cache savings = %f, want %f (diff: %f)",
			actualSavings, expectedSavings, actualSavings-expectedSavings)
	}
}
