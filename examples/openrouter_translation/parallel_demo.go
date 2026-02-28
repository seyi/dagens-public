// Example: Parallel translations using Dagens agents with OpenRouter/Qwen
package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/models"
	"github.com/seyi/dagens/pkg/models/openai"
)

// TranslationResult holds a translation result
type TranslationResult struct {
	Language    string
	Translation string
	Latency     time.Duration
	Tokens      int
	Error       error
}

func runParallelDemo() {
	// Get OpenRouter API key
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		fmt.Println("Error: OPENROUTER_API_KEY environment variable not set")
		os.Exit(1)
	}

	// Create OpenRouter provider with Qwen model
	provider, err := openai.NewProvider(openai.Config{
		APIKey:  apiKey,
		BaseURL: "https://openrouter.ai/api/v1",
		Model:   "qwen/qwen-2.5-72b-instruct",
		Parameters: openai.Parameters{
			Temperature:     0.3,
			MaxOutputTokens: 256,
		},
	})
	if err != nil {
		fmt.Printf("Error creating provider: %v\n", err)
		os.Exit(1)
	}
	defer provider.Close()

	// Input text to translate
	inputText := "Hello World! Welcome to the Dagens agent framework."

	// Target languages for parallel translation
	languages := []string{
		"Spanish",
		"French",
		"German",
		"Japanese",
		"Chinese (Simplified)",
		"Portuguese",
		"Italian",
		"Korean",
	}

	fmt.Println("╔════════════════════════════════════════════════════════════════╗")
	fmt.Println("║       Parallel Translation Demo - Dagens Agent Framework       ║")
	fmt.Println("╠════════════════════════════════════════════════════════════════╣")
	fmt.Printf("║ Model: %-56s ║\n", provider.Name())
	fmt.Printf("║ Input: %-56s ║\n", truncate(inputText, 56))
	fmt.Printf("║ Languages: %-52d ║\n", len(languages))
	fmt.Println("╚════════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Execute parallel translations
	startTime := time.Now()
	results := executeParallelTranslations(provider, inputText, languages)
	totalTime := time.Since(startTime)

	// Display results
	fmt.Println("┌────────────────────┬────────────────────────────────────────────┬──────────┬────────┐")
	fmt.Println("│ Language           │ Translation                                │ Latency  │ Tokens │")
	fmt.Println("├────────────────────┼────────────────────────────────────────────┼──────────┼────────┤")

	successCount := 0
	totalTokens := 0
	for _, result := range results {
		if result.Error != nil {
			fmt.Printf("│ %-18s │ %-42s │ %-8s │ %-6s │\n",
				result.Language,
				truncate(fmt.Sprintf("ERROR: %v", result.Error), 42),
				"-",
				"-")
		} else {
			successCount++
			totalTokens += result.Tokens
			fmt.Printf("│ %-18s │ %-42s │ %6dms │ %6d │\n",
				result.Language,
				truncate(result.Translation, 42),
				result.Latency.Milliseconds(),
				result.Tokens)
		}
	}

	fmt.Println("└────────────────────┴────────────────────────────────────────────┴──────────┴────────┘")
	fmt.Println()

	// Summary
	fmt.Println("┌─────────────────────────────────────────────────────────────────┐")
	fmt.Println("│                          Summary                                │")
	fmt.Println("├─────────────────────────────────────────────────────────────────┤")
	fmt.Printf("│ Total parallel execution time: %-32v │\n", totalTime.Round(time.Millisecond))
	fmt.Printf("│ Successful translations: %d/%-36d │\n", successCount, len(languages))
	fmt.Printf("│ Total tokens used: %-43d │\n", totalTokens)

	// Calculate speedup
	var sequentialTime time.Duration
	for _, r := range results {
		if r.Error == nil {
			sequentialTime += r.Latency
		}
	}
	if totalTime > 0 {
		speedup := float64(sequentialTime) / float64(totalTime)
		fmt.Printf("│ Parallel speedup: %.2fx (vs sequential %-21v) │\n", speedup, sequentialTime.Round(time.Millisecond))
	}
	fmt.Println("└─────────────────────────────────────────────────────────────────┘")
}

// executeParallelTranslations runs translations concurrently
func executeParallelTranslations(provider *openai.Provider, text string, languages []string) []TranslationResult {
	results := make([]TranslationResult, len(languages))
	var wg sync.WaitGroup

	for i, lang := range languages {
		wg.Add(1)
		go func(idx int, language string) {
			defer wg.Done()

			start := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			request := &models.ModelRequest{
				Messages: []models.Message{
					{
						Role:    models.RoleSystem,
						Content: fmt.Sprintf("You are a professional translator. Translate the given text to %s. Only output the translation, nothing else.", language),
					},
					{
						Role:    models.RoleUser,
						Content: text,
					},
				},
				Temperature:     0.3,
				MaxOutputTokens: 256,
			}

			response, err := provider.Chat(ctx, request)
			latency := time.Since(start)

			if err != nil {
				results[idx] = TranslationResult{
					Language: language,
					Error:    err,
					Latency:  latency,
				}
				return
			}

			results[idx] = TranslationResult{
				Language:    language,
				Translation: response.Message.Content,
				Latency:     latency,
				Tokens:      response.Usage.TotalTokens,
			}
		}(i, lang)
	}

	wg.Wait()
	return results
}

// truncate shortens a string to fit display
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
