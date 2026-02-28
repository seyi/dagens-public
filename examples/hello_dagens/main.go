// Package main demonstrates a minimal "Hello World" using the real OpenAI provider.
//
// This is the simplest possible Dagens agent demonstrating:
// - Real OpenAI provider from pkg/models/openai
// - Basic state management with MemoryState
// - Single LLM call with response handling
//
// Usage:
//
//	export OPENAI_API_KEY=your-key
//	go run examples/hello_dagens/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/seyi/dagens/pkg/graph"
	"github.com/seyi/dagens/pkg/models"
	"github.com/seyi/dagens/pkg/models/openai"
)

func main() {
	fmt.Println("╔════════════════════════════════════════════════════╗")
	fmt.Println("║        HELLO DAGENS - Phase 2A Quick Start         ║")
	fmt.Println("╚════════════════════════════════════════════════════╝")
	fmt.Println()

	// 1) Read API key
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable is required")
	}

	// 2) Create OpenAI provider (Phase 2A pattern)
	provider, err := openai.NewProvider(openai.Config{
		Model:  "gpt-4o-mini",
		APIKey: apiKey,
		Parameters: openai.Parameters{
			Temperature: 0.7,
		},
	})
	if err != nil {
		log.Fatalf("Failed to create provider: %v", err)
	}
	defer provider.Close()

	fmt.Printf("Provider: %s (context window: %d tokens)\n", provider.Name(), provider.ContextWindow())

	// 3) Basic state setup
	state := graph.NewMemoryState()
	fmt.Printf("Initial state version: %d\n\n", state.Version())

	// 4) Ask a simple question
	userQuestion := "Say hello to Dagens in one short sentence."
	state.Set("user.question", userQuestion)

	fmt.Printf("User: %s\n", userQuestion)

	ctx := context.Background()
	resp, err := provider.Chat(ctx, &models.ModelRequest{
		Messages: []models.Message{
			{Role: models.RoleUser, Content: userQuestion},
		},
	})
	if err != nil {
		log.Fatalf("Chat failed: %v", err)
	}

	// 5) Store response and metrics in state
	state.Set("assistant.answer", resp.Message.Content)
	state.Set("assistant.tokens_total", resp.Usage.TotalTokens)

	// 6) Print result and state info
	answer, _ := state.Get("assistant.answer")
	tokens, _ := state.Get("assistant.tokens_total")

	fmt.Printf("Assistant: %s\n", answer)
	fmt.Printf("\nTokens used: %v\n", tokens)
	fmt.Printf("Final state version: %d\n", state.Version())

	// 7) Demonstrate state features
	fmt.Println("\n─────────────────────────────────────────────────────")
	fmt.Println("State Management Demo")
	fmt.Println("─────────────────────────────────────────────────────")

	// Show all keys
	fmt.Printf("All keys: %v\n", state.Keys())

	// Show user-scoped keys
	userKeys := state.KeysWithPrefix("user.")
	fmt.Printf("User scope keys: %v\n", userKeys)

	// Show assistant-scoped keys
	assistantKeys := state.KeysWithPrefix("assistant.")
	fmt.Printf("Assistant scope keys: %v\n", assistantKeys)

	fmt.Println("\n✓ Hello Dagens complete!")
}
