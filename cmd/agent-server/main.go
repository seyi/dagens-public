// Copyright 2025 Apache Spark AI Agents
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/grpc"
)


// Sample agents for testing

// EchoAgent echoes input back
type EchoAgent struct {
	id string
}

func (a *EchoAgent) ID() string {
	return a.id
}

func (a *EchoAgent) Name() string        { return a.id }
func (a *EchoAgent) Description() string { return "Echoes input back" }
func (a *EchoAgent) Capabilities() []string { return []string{"echo"} }
func (a *EchoAgent) Dependencies() []agent.Agent { return []agent.Agent{} }
func (a *EchoAgent) Partition() string   { return "" }

func (a *EchoAgent) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	return &agent.AgentOutput{
		Result: fmt.Sprintf("Echo: %s", input.Instruction),
		Metadata: map[string]interface{}{
			"agent_type": "echo",
		},
	}, nil
}

// SummarizerAgent provides mock summarization
type SummarizerAgent struct {
	id string
}

func (a *SummarizerAgent) ID() string {
	return a.id
}

func (a *SummarizerAgent) Name() string        { return a.id }
func (a *SummarizerAgent) Description() string { return "Summarizes text" }
func (a *SummarizerAgent) Capabilities() []string { return []string{"summarize"} }
func (a *SummarizerAgent) Dependencies() []agent.Agent { return []agent.Agent{} }
func (a *SummarizerAgent) Partition() string   { return "" }

func (a *SummarizerAgent) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	words := strings.Fields(input.Instruction)
	wordCount := len(words)

	summary := fmt.Sprintf("Summary: %d words", wordCount)
	if wordCount > 0 {
		summary += fmt.Sprintf(", starts with '%s'", words[0])
	}

	return &agent.AgentOutput{
		Result: summary,
		Metadata: map[string]interface{}{
			"agent_type": "summarizer",
			"word_count": wordCount,
		},
	}, nil
}

// ClassifierAgent provides mock classification
type ClassifierAgent struct {
	id string
}

func (a *ClassifierAgent) ID() string {
	return a.id
}

func (a *ClassifierAgent) Name() string        { return a.id }
func (a *ClassifierAgent) Description() string { return "Classifies text" }
func (a *ClassifierAgent) Capabilities() []string { return []string{"classify"} }
func (a *ClassifierAgent) Dependencies() []agent.Agent { return []agent.Agent{} }
func (a *ClassifierAgent) Partition() string   { return "" }

func (a *ClassifierAgent) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// Simple mock classification based on keywords
	text := strings.ToLower(input.Instruction)

	category := "neutral"
	confidence := 0.5

	if strings.Contains(text, "good") || strings.Contains(text, "great") || strings.Contains(text, "excellent") {
		category = "positive"
		confidence = 0.85
	} else if strings.Contains(text, "bad") || strings.Contains(text, "poor") || strings.Contains(text, "terrible") {
		category = "negative"
		confidence = 0.85
	}

	return &agent.AgentOutput{
		Result: category,
		Metadata: map[string]interface{}{
			"agent_type": "classifier",
			"confidence": confidence,
			"category":   category,
		},
	}, nil
}

// SentimentAgent provides sentiment analysis
type SentimentAgent struct {
	id string
}

func (a *SentimentAgent) ID() string {
	return a.id
}

func (a *SentimentAgent) Name() string        { return a.id }
func (a *SentimentAgent) Description() string { return "Analyzes sentiment" }
func (a *SentimentAgent) Capabilities() []string { return []string{"sentiment"} }
func (a *SentimentAgent) Dependencies() []agent.Agent { return []agent.Agent{} }
func (a *SentimentAgent) Partition() string   { return "" }

func (a *SentimentAgent) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	text := strings.ToLower(input.Instruction)

	// Count positive and negative words
	positiveWords := []string{"good", "great", "excellent", "amazing", "wonderful", "love", "best"}
	negativeWords := []string{"bad", "poor", "terrible", "awful", "hate", "worst", "horrible"}

	positiveCount := 0
	negativeCount := 0

	for _, word := range positiveWords {
		if strings.Contains(text, word) {
			positiveCount++
		}
	}

	for _, word := range negativeWords {
		if strings.Contains(text, word) {
			negativeCount++
		}
	}

	sentiment := "neutral"
	score := 0.5

	if positiveCount > negativeCount {
		sentiment = "positive"
		score = 0.5 + float64(positiveCount)*0.1
		if score > 1.0 {
			score = 1.0
		}
	} else if negativeCount > positiveCount {
		sentiment = "negative"
		score = 0.5 - float64(negativeCount)*0.1
		if score < 0.0 {
			score = 0.0
		}
	}

	return &agent.AgentOutput{
		Result: sentiment,
		Metadata: map[string]interface{}{
			"agent_type":      "sentiment",
			"score":           score,
			"positive_words":  positiveCount,
			"negative_words":  negativeCount,
		},
	}, nil
}

func main() {
	// Parse flags
	addr := flag.String("addr", ":8080", "HTTP server address")
	flag.Parse()

	// Create HTTP server (no runtime needed for simple HTTP mode)
	server := grpc.NewAgentHTTPServer(nil)

	// Register sample agents
	log.Println("Registering agents...")

	server.RegisterAgent("echo", &EchoAgent{id: "echo"})
	log.Println("  ✓ Registered 'echo' agent")

	server.RegisterAgent("summarizer", &SummarizerAgent{id: "summarizer"})
	log.Println("  ✓ Registered 'summarizer' agent")

	server.RegisterAgent("classifier", &ClassifierAgent{id: "classifier"})
	log.Println("  ✓ Registered 'classifier' agent")

	server.RegisterAgent("sentiment", &SentimentAgent{id: "sentiment"})
	log.Println("  ✓ Registered 'sentiment' agent")

	// Setup graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Start server in goroutine
	go func() {
		log.Printf("Starting agent HTTP server on %s...", *addr)
		log.Printf("Available agents: echo, summarizer, classifier, sentiment")
		log.Printf("Health check: http://localhost%s/health", *addr)
		log.Printf("List agents: http://localhost%s/api/v1/agents", *addr)

		if err := server.Start(*addr); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Wait for interrupt
	<-stop
	log.Println("\nShutting down server...")
}
