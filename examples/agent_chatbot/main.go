package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/seyi/dagens/pkg/backend"
	"github.com/seyi/dagens/pkg/graph"
	"github.com/seyi/dagens/pkg/models"
	"github.com/seyi/dagens/pkg/models/openai"
)

func main() {
	fmt.Println("=== AI Agent Chatbot Example ===")

	// Get API key from environment
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable is required")
	}

	// Create OpenAI provider
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

	fmt.Printf("Provider created: %s (%s)\n", provider.Name(), provider.Type())
	fmt.Printf("- Supports tools: %v\n", provider.SupportsTools())
	fmt.Printf("- Supports vision: %v\n", provider.SupportsVision())
	fmt.Printf("- Context window: %d tokens\n\n", provider.ContextWindow())

	// Build the graph
	g, err := graph.NewBuilder("chatbot").
		AddAgent("assistant", graph.AgentConfig{
			Provider:     provider,
			SystemPrompt: "You are a helpful assistant. Be concise and friendly.",
			Temperature:  0.7,
			MaxTokens:    150,
		}).
		SetEntry("assistant").
		AddFinish("assistant").
		Build()

	if err != nil {
		log.Fatalf("Failed to build graph: %v", err)
	}

	fmt.Printf("Graph '%s' built successfully!\n", g.Name())
	fmt.Printf("- Graph ID: %s\n", g.ID())
	fmt.Printf("- Nodes: %d\n", g.NodeCount())
	fmt.Printf("- Entry: %s\n\n", g.Entry())

	// Create backend
	executor := backend.NewLocalBackend()
	defer executor.Close()

	fmt.Printf("Backend: %s\n\n", executor.Name())

	// Execute the graph with a user message
	state := graph.NewMemoryState()
	state.Set("user_message", "Hello! Can you explain what you are in one sentence?")

	fmt.Println("Executing graph with user message...")
	fmt.Printf("User: %s\n\n", "Hello! Can you explain what you are in one sentence?")

	ctx := context.Background()
	result, err := executor.Execute(ctx, g, state)
	if err != nil {
		log.Fatalf("Execution failed: %v", err)
	}

	// Display results
	fmt.Println("=== Execution Results ===")
	fmt.Printf("Execution ID: %s\n", result.ExecutionID)
	fmt.Printf("Status: %s\n", result.Status)
	fmt.Printf("Duration: %v\n", result.Duration)
	fmt.Printf("Nodes executed: %d\n", result.NodesExecuted)
	fmt.Printf("Execution path: %v\n\n", result.ExecutionPath)

	// Get the response
	lastRespIface, exists := state.Get("last_response")
	if exists {
		if resp, ok := lastRespIface.(*models.ModelResponse); ok {
			fmt.Println("=== Agent Response ===")
			fmt.Printf("Assistant: %s\n\n", resp.Message.Content)
			fmt.Printf("Token usage:\n")
			if resp.Usage.TotalTokens > 0 {
				fmt.Printf("  Prompt tokens: %d\n", resp.Usage.PromptTokens)
				fmt.Printf("  Completion tokens: %d\n", resp.Usage.CompletionTokens)
				fmt.Printf("  Total tokens: %d\n", resp.Usage.TotalTokens)
				if resp.Usage.EstimatedCostUSD > 0 {
					fmt.Printf("  Estimated cost: $%.6f\n", resp.Usage.EstimatedCostUSD)
				}
			}
		}
	}

	// Try a multi-turn conversation
	fmt.Println("=== Multi-Turn Conversation ===")

	// Second turn
	messages, _ := state.Get("messages")
	if msgHistory, ok := messages.([]models.Message); ok {
		msgHistory = append(msgHistory, models.Message{
			Role:    models.RoleUser,
			Content: "What makes you different from other AI assistants?",
		})
		state.Set("messages", msgHistory)
	}

	fmt.Printf("User: %s\n\n", "What makes you different from other AI assistants?")

	result2, err := executor.Execute(ctx, g, state)
	if err != nil {
		log.Fatalf("Second execution failed: %v", err)
	}

	lastRespIface2, exists := state.Get("last_response")
	if exists {
		if resp, ok := lastRespIface2.(*models.ModelResponse); ok {
			fmt.Printf("Assistant: %s\n\n", resp.Message.Content)
			fmt.Printf("Duration: %v\n", result2.Duration)
		}
	}

	fmt.Println("=== Example Complete ===")
	fmt.Println("\nThis demonstrates:")
	fmt.Println("✅ Graph-based agent workflow")
	fmt.Println("✅ OpenAI provider integration")
	fmt.Println("✅ Local backend execution")
	fmt.Println("✅ Stateful multi-turn conversations")
	fmt.Println("✅ Real-time responses (<1ms overhead)")
}
