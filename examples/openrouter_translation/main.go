// Example: Using OpenRouter with Qwen model for translation
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/seyi/dagens/pkg/models"
	"github.com/seyi/dagens/pkg/models/openai"
)

func main() {
	// Get OpenRouter API key from environment
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		fmt.Println("Error: OPENROUTER_API_KEY environment variable not set")
		fmt.Println("Usage: OPENROUTER_API_KEY=your-key go run main.go")
		os.Exit(1)
	}

	// Create OpenAI-compatible provider for OpenRouter
	// OpenRouter uses OpenAI-compatible API at https://openrouter.ai/api/v1
	provider, err := openai.NewProvider(openai.Config{
		APIKey:  apiKey,
		BaseURL: "https://openrouter.ai/api/v1",
		Model:   "qwen/qwen-2.5-72b-instruct", // Qwen model on OpenRouter
		Parameters: openai.Parameters{
			Temperature:     0.3, // Low temperature for translation accuracy
			MaxOutputTokens: 256,
		},
	})
	if err != nil {
		fmt.Printf("Error creating provider: %v\n", err)
		os.Exit(1)
	}
	defer provider.Close()

	// Create the translation request
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	request := &models.ModelRequest{
		Messages: []models.Message{
			{
				Role:    models.RoleSystem,
				Content: "You are a professional translator. Translate the given text to Spanish. Only output the translation, nothing else.",
			},
			{
				Role:    models.RoleUser,
				Content: "Hello World",
			},
		},
		Temperature:     0.3,
		MaxOutputTokens: 256,
	}

	fmt.Println("=== OpenRouter Translation Demo ===")
	fmt.Printf("Model: %s\n", provider.Name())
	fmt.Printf("Input: \"Hello World\"\n")
	fmt.Printf("Target: Spanish\n")
	fmt.Println("-----------------------------------")

	// Make the API call
	response, err := provider.Chat(ctx, request)
	if err != nil {
		fmt.Printf("Error calling API: %v\n", err)
		os.Exit(1)
	}

	// Output results
	fmt.Printf("Translation: %s\n", response.Message.Content)
	fmt.Println("-----------------------------------")
	fmt.Printf("Tokens used: %d (prompt: %d, completion: %d)\n",
		response.Usage.TotalTokens,
		response.Usage.PromptTokens,
		response.Usage.CompletionTokens)
	fmt.Printf("Latency: %v\n", response.Latency)
	fmt.Printf("Finish reason: %s\n", response.FinishReason)

	fmt.Println("\n\n=== Starting Parallel Demo ===")
	runParallelDemo()
}
