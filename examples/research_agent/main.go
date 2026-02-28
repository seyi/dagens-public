// Package main demonstrates a multi-step research workflow using Phase 2A state management.
//
// This example creates a pipeline: Searcher → Summarizer → Writer
// Each agent stores its output in shared state and checkpoints are saved after each phase.
//
// Usage:
//
//	export OPENAI_API_KEY=your-key
//	go run examples/research_agent/main.go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/seyi/dagens/pkg/graph"
	"github.com/seyi/dagens/pkg/models"
	"github.com/seyi/dagens/pkg/models/openai"
)

func main() {
	fmt.Println("╔══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║   RESEARCH AGENT (Phase 2A): Searcher → Summarizer → Writer      ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Parse command-line flags
	outputFlag := flag.String("output", "", "Output file path (e.g., report.md). Uses RESEARCH_AGENT_OUTPUT env var or defaults to report.md")
	flag.Parse()

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable is required")
	}

	// Create OpenAI provider
	provider, err := openai.NewProvider(openai.Config{
		Model:  "gpt-4o-mini",
		APIKey: apiKey,
		Parameters: openai.Parameters{
			Temperature:     0.7,
			MaxOutputTokens: 600,
		},
	})
	if err != nil {
		log.Fatalf("Failed to create provider: %v", err)
	}
	defer provider.Close()

	ctx := context.Background()
	state := graph.NewMemoryState()
	store := graph.NewMemoryStateStore()

	// Workflow initialization
	topic := "latest advancements in AI"
	state.Set("workflow.id", "research-001")
	state.Set("workflow.topic", topic)
	state.Set("workflow.started_at", time.Now().Format(time.RFC3339))
	state.Set("workflow.status", "running")

	fmt.Printf("Topic: %s\n", topic)
	fmt.Printf("Initial state version: %d\n\n", state.Version())

	// ========================================
	// Phase 1: Searcher
	// ========================================
	fmt.Println("──────────────────────────────────────────────────────────────────")
	fmt.Println("Phase 1: SEARCHER")
	fmt.Println("──────────────────────────────────────────────────────────────────")

	searchPrompt := fmt.Sprintf(`You are a research assistant. Research the topic below and provide 4-6 concise bullet points.

Topic: %s

Format:
- [point]
- [point]
- [point]`, topic)

	searchStart := time.Now()
	searchResp, err := provider.Chat(ctx, &models.ModelRequest{
		Messages: []models.Message{
			{Role: models.RoleUser, Content: searchPrompt},
		},
	})
	if err != nil {
		log.Fatalf("Searcher failed: %v", err)
	}
	searchDur := time.Since(searchStart)

	state.Set("searcher.output", searchResp.Message.Content)
	state.Set("searcher.status", "completed")
	state.Set("searcher.duration_ms", searchDur.Milliseconds())
	state.Set("searcher.tokens_used", searchResp.Usage.TotalTokens)

	fmt.Printf("Searcher Output:\n%s\n\n", searchResp.Message.Content)
	fmt.Printf("[duration: %v, tokens: %d, state ver: %d]\n", searchDur, searchResp.Usage.TotalTokens, state.Version())

	// Checkpoint after searcher
	store.Save(ctx, "after-searcher", state.Snapshot())
	fmt.Println("[Checkpoint saved: after-searcher]")

	// ========================================
	// Phase 2: Summarizer
	// ========================================
	fmt.Println("\n──────────────────────────────────────────────────────────────────")
	fmt.Println("Phase 2: SUMMARIZER")
	fmt.Println("──────────────────────────────────────────────────────────────────")

	searcherOutput, _ := state.Get("searcher.output")
	summarizePrompt := fmt.Sprintf(`You are a summarization expert. Summarize the research below into a single paragraph (3-4 sentences).

Research:
%s`, searcherOutput)

	sumStart := time.Now()
	sumResp, err := provider.Chat(ctx, &models.ModelRequest{
		Messages: []models.Message{
			{Role: models.RoleUser, Content: summarizePrompt},
		},
	})
	if err != nil {
		log.Fatalf("Summarizer failed: %v", err)
	}
	sumDur := time.Since(sumStart)

	state.Set("summarizer.output", sumResp.Message.Content)
	state.Set("summarizer.status", "completed")
	state.Set("summarizer.duration_ms", sumDur.Milliseconds())
	state.Set("summarizer.tokens_used", sumResp.Usage.TotalTokens)

	fmt.Printf("Summarizer Output:\n%s\n\n", sumResp.Message.Content)
	fmt.Printf("[duration: %v, tokens: %d, state ver: %d]\n", sumDur, sumResp.Usage.TotalTokens, state.Version())

	// Checkpoint after summarizer
	store.Save(ctx, "after-summarizer", state.Snapshot())
	fmt.Println("[Checkpoint saved: after-summarizer]")

	// ========================================
	// Phase 3: Writer
	// ========================================
	fmt.Println("\n──────────────────────────────────────────────────────────────────")
	fmt.Println("Phase 3: WRITER")
	fmt.Println("──────────────────────────────────────────────────────────────────")

	summary, _ := state.Get("summarizer.output")
	writerPrompt := fmt.Sprintf(`You are a technical writer. Using the summary below, produce a short report with:
1) Title
2) Executive Summary (2-3 sentences)
3) Key Developments (3 bullet points)
4) Outlook (1-2 sentences)

Summary:
%s`, summary)

	writeStart := time.Now()
	writeResp, err := provider.Chat(ctx, &models.ModelRequest{
		Messages: []models.Message{
			{Role: models.RoleUser, Content: writerPrompt},
		},
	})
	if err != nil {
		log.Fatalf("Writer failed: %v", err)
	}
	writeDur := time.Since(writeStart)

	state.Set("writer.output", writeResp.Message.Content)
	state.Set("writer.status", "completed")
	state.Set("writer.duration_ms", writeDur.Milliseconds())
	state.Set("writer.tokens_used", writeResp.Usage.TotalTokens)
	state.Set("workflow.status", "completed")
	state.Set("workflow.completed_at", time.Now().Format(time.RFC3339))

	fmt.Printf("Writer Output:\n%s\n\n", writeResp.Message.Content)
	fmt.Printf("[duration: %v, tokens: %d, state ver: %d]\n", writeDur, writeResp.Usage.TotalTokens, state.Version())

	// Final checkpoint
	store.Save(ctx, "after-writer", state.Snapshot())
	fmt.Println("[Checkpoint saved: after-writer]")

	// ========================================
	// Save Report to File
	// ========================================
	totalTokens := searchResp.Usage.TotalTokens + sumResp.Usage.TotalTokens + writeResp.Usage.TotalTokens
	totalDuration := searchDur + sumDur + writeDur

	reportContent, ok := state.Get("writer.output")
	if ok {
		// Resolve output path: flag > env var > default
		var outputPath string
		if *outputFlag != "" {
			outputPath = *outputFlag
		} else if env := os.Getenv("RESEARCH_AGENT_OUTPUT"); env != "" {
			outputPath = env
		} else {
			outputPath = "report.md"
		}

		// Handle directory-only paths (e.g., "reports/")
		if strings.HasSuffix(outputPath, "/") || strings.HasSuffix(outputPath, string(os.PathSeparator)) {
			outputPath = filepath.Join(outputPath, "report.md")
		}

		// Add timestamp to filename to prevent overwrites
		ts := time.Now().Format("2006-01-02_150405")
		ext := filepath.Ext(outputPath)
		if ext == "" {
			ext = ".md"
			outputPath = outputPath + ext
		}
		base := strings.TrimSuffix(outputPath, ext)
		timestampedPath := fmt.Sprintf("%s_%s%s", base, ts, ext)

		// Create output directory if needed
		dir := filepath.Dir(timestampedPath)
		if dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				log.Printf("Warning: Failed to create output directory %s: %v", dir, err)
				state.Set("workflow.output_error", err.Error())
			}
		}

		// Compose report with metadata header
		startedAt, _ := state.Get("workflow.started_at")
		completedAt, _ := state.Get("workflow.completed_at")
		header := fmt.Sprintf(
			"<!-- Research Agent Report | topic: %s | started: %v | completed: %v | duration: %v | tokens: %d -->\n\n",
			topic, startedAt, completedAt, totalDuration, totalTokens,
		)
		fullContent := header + reportContent.(string)

		// Write to file
		if err := os.WriteFile(timestampedPath, []byte(fullContent), 0644); err != nil {
			log.Printf("Warning: Failed to write report to %s: %v", timestampedPath, err)
			state.Set("workflow.output_error", err.Error())
		} else {
			fmt.Printf("\n✓ Report saved to: %s\n", timestampedPath)
			state.Set("workflow.output_path", timestampedPath)
		}
	}

	// ========================================
	// Summary
	// ========================================

	fmt.Println("\n════════════════════════════════════════════════════════════════════")
	fmt.Println("                         WORKFLOW COMPLETE")
	fmt.Println("════════════════════════════════════════════════════════════════════")
	fmt.Printf("Topic: %s\n", topic)
	fmt.Printf("Agents: 3 (Searcher, Summarizer, Writer)\n")
	fmt.Printf("Total duration: %v\n", totalDuration)
	fmt.Printf("Total tokens: %d\n", totalTokens)
	fmt.Printf("Final state version: %d\n", state.Version())

	// Show keys by scope
	fmt.Println("\nState keys by scope:")
	for _, scope := range []string{"searcher.", "summarizer.", "writer.", "workflow."} {
		keys := state.KeysWithPrefix(scope)
		fmt.Printf("  %s (%d keys): %v\n", scope, len(keys), keys)
	}

	// Show available checkpoints
	checkpoints, _ := store.List(ctx)
	fmt.Printf("\nAvailable checkpoints: %v\n", checkpoints)

	fmt.Println("\nPhase 2A Features Demonstrated:")
	fmt.Println("  [x] State passing between agents")
	fmt.Println("  [x] Scoped state keys (searcher.*, summarizer.*, writer.*, workflow.*)")
	fmt.Println("  [x] Checkpoint persistence (StateStore)")
	fmt.Println("  [x] Version tracking")
	fmt.Println("  [x] Real OpenAI provider integration")
	fmt.Println("  [x] File output with metadata (DX-203)")
}
