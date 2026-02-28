//go:build ignore

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
	"fmt"
	"log"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/agents"
	"github.com/seyi/dagens/pkg/model"
	"github.com/seyi/dagens/pkg/tools"
	"github.com/seyi/dagens/pkg/tools/mcp"
)

// Example1_BasicMCPToolUsage demonstrates basic MCP tool integration
func Example1_BasicMCPToolUsage() {
	ctx := context.Background()

	// Create MCP toolset for AgentQL (web scraping tool)
	// This matches ADK's MCPToolset pattern
	agentqlToolset, err := mcp.NewMCPToolset(mcp.StdioConnectionParams{
		Command: "npx",
		Args:    []string{"-y", "agentql-mcp"},
		Env: map[string]string{
			"AGENTQL_API_KEY": "your-api-key-here",
		},
		Timeout: 300,
	})

	if err != nil {
		log.Fatalf("Failed to create MCP toolset: %v", err)
	}

	// Connect and discover tools
	if err := agentqlToolset.Connect(ctx); err != nil {
		log.Fatalf("Failed to connect to MCP server: %v", err)
	}
	defer agentqlToolset.Disconnect()

	// Register tools in our tool registry
	toolRegistry := tools.NewToolRegistry()
	if err := agentqlToolset.RegisterTools(toolRegistry); err != nil {
		log.Fatalf("Failed to register tools: %v", err)
	}

	// Create agent with MCP tools
	modelProvider := model.NewAnthropicProvider("claude-3-5-sonnet", "your-api-key")

	scraper := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "web-scraper",
		ModelName:   "claude-3-5-sonnet",
		Instruction: "Extract structured data from websites using AgentQL",
		Tools:       []string{"extract-web-data"}, // Tool discovered from MCP
		Temperature: 0.7,
	}, modelProvider, toolRegistry)

	// Execute agent - uses third-party MCP tool!
	output, err := scraper.Execute(ctx, &agent.AgentInput{
		Instruction: "Extract product names and prices from https://example.com/products",
	})

	if err != nil {
		log.Fatalf("Agent execution failed: %v", err)
	}

	fmt.Printf("Extracted data: %v\n", output.Result)

	// Get metrics
	metrics := agentqlToolset.GetMetrics()
	for _, m := range metrics {
		fmt.Printf("MCP Tool Metrics: %s took %.2fms (partition: %s)\n",
			m.ToolName, m.DurationMs, m.PartitionID)
	}
}

// Example2_MultipleThirdPartyTools demonstrates using multiple MCP servers
func Example2_MultipleThirdPartyTools() {
	ctx := context.Background()

	// Create tool registry
	toolRegistry := tools.NewToolRegistry()

	// Add AgentQL for web scraping
	agentqlTools, err := mcp.NewMCPToolset(mcp.StdioConnectionParams{
		Command: "npx",
		Args:    []string{"-y", "agentql-mcp"},
		Env:     map[string]string{"AGENTQL_API_KEY": "key1"},
	})
	if err == nil {
		agentqlTools.Connect(ctx)
		defer agentqlTools.Disconnect()
		agentqlTools.RegisterTools(toolRegistry)
	}

	// Add Brave Search for web search
	braveTools, err := mcp.NewMCPToolset(mcp.StdioConnectionParams{
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-brave-search"},
		Env:     map[string]string{"BRAVE_API_KEY": "key2"},
	})
	if err == nil {
		braveTools.Connect(ctx)
		defer braveTools.Disconnect()
		braveTools.RegisterTools(toolRegistry)
	}

	// Add Puppeteer for browser automation
	puppeteerTools, err := mcp.NewMCPToolset(mcp.StdioConnectionParams{
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-puppeteer"},
	})
	if err == nil {
		puppeteerTools.Connect(ctx)
		defer puppeteerTools.Disconnect()
		puppeteerTools.RegisterTools(toolRegistry)
	}

	// Create agent with access to ALL third-party tools
	modelProvider := model.NewAnthropicProvider("claude-3-5-sonnet", "your-api-key")

	researcher := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "research-agent",
		ModelName:   "claude-3-5-sonnet",
		Instruction: "Research topics using web search, scraping, and automation",
		Tools:       toolRegistry.ListToolNames(), // All MCP tools!
		Temperature: 0.7,
	}, modelProvider, toolRegistry)

	// Execute complex research task using multiple tools
	output, err := researcher.Execute(ctx, &agent.AgentInput{
		Instruction: "Research the latest AI frameworks, search the web, and extract key features",
	})

	if err != nil {
		log.Fatalf("Research failed: %v", err)
	}

	fmt.Printf("Research results: %v\n", output.Result)
}

// Example3_DistributedMCPExecution demonstrates distributed MCP tool execution
func Example3_DistributedMCPExecution() {
	ctx := context.Background()

	// Create distributed MCP toolset manager
	// This enables tool execution across multiple Spark partitions
	distributedConfig := &mcp.DistributedMCPConfig{
		PartitionStrategy:  "round-robin",
		MaxConcurrentCalls: 10,
		EnableCaching:      false,
		EnableMetrics:      true,
	}

	params := mcp.StdioConnectionParams{
		Command: "npx",
		Args:    []string{"-y", "agentql-mcp"},
		Env:     map[string]string{"AGENTQL_API_KEY": "your-key"},
	}

	distributedToolset := mcp.NewDistributedMCPToolset(params, distributedConfig)
	defer distributedToolset.Close()

	// Simulate distributed execution across partitions
	partitions := []string{"partition-0", "partition-1", "partition-2"}

	for _, partitionID := range partitions {
		// Get or create toolset for this partition
		toolset, err := distributedToolset.GetOrCreateToolset(ctx, partitionID)
		if err != nil {
			log.Printf("Failed to create toolset for %s: %v", partitionID, err)
			continue
		}

		// Register tools for this partition
		registry := tools.NewToolRegistry()
		toolset.RegisterTools(registry)

		// Create agent for this partition
		modelProvider := model.NewAnthropicProvider("claude-3-5-sonnet", "your-api-key")

		agent := agents.NewLlmAgent(agents.LlmAgentConfig{
			Name:        fmt.Sprintf("scraper-%s", partitionID),
			Instruction: "Extract data from websites",
			Tools:       registry.ListToolNames(),
		}, modelProvider, registry)

		// Execute on this partition
		go func(pid string, ag agent.Agent) {
			output, err := ag.Execute(ctx, &agent.AgentInput{
				Instruction: fmt.Sprintf("Process data for %s", pid),
			})
			if err != nil {
				log.Printf("%s failed: %v", pid, err)
			} else {
				log.Printf("%s succeeded: %v", pid, output.Result)
			}
		}(partitionID, agent)
	}

	// Get aggregated metrics from all partitions
	allMetrics := distributedToolset.GetAggregatedMetrics()
	fmt.Printf("Total tool calls across partitions: %d\n", len(allMetrics))

	// Analyze metrics by partition
	metricsPerPartition := make(map[string]int)
	for _, m := range allMetrics {
		metricsPerPartition[m.PartitionID]++
	}

	for pid, count := range metricsPerPartition {
		fmt.Printf("Partition %s: %d tool calls\n", pid, count)
	}
}

// Example4_HTTPTransport demonstrates using HTTP-based MCP servers
func Example4_HTTPTransport() {
	ctx := context.Background()

	// Create MCP toolset with HTTP transport for remote servers
	httpToolset, err := mcp.NewMCPToolset(mcp.HTTPConnectionParams{
		URL: "https://mcp-server.example.com/api",
		Headers: map[string]string{
			"Authorization": "Bearer your-token",
		},
		Timeout:  300,
		SessionID: "session-123", // Optional: stateful connections
		EnableCORS: true,
	})

	if err != nil {
		log.Fatalf("Failed to create HTTP MCP toolset: %v", err)
	}

	// Connect and discover tools
	if err := httpToolset.Connect(ctx); err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer httpToolset.Disconnect()

	// Register and use tools
	toolRegistry := tools.NewToolRegistry()
	httpToolset.RegisterTools(toolRegistry)

	fmt.Printf("Discovered %d tools from HTTP MCP server\n", len(httpToolset.GetTools()))
}

// Example5_AgentHierarchyWithMCP demonstrates using MCP tools in agent hierarchies
func Example5_AgentHierarchyWithMCP() {
	ctx := context.Background()

	// Set up MCP tools
	toolRegistry := tools.NewToolRegistry()

	agentqlTools, _ := mcp.NewMCPToolset(mcp.StdioConnectionParams{
		Command: "npx",
		Args:    []string{"-y", "agentql-mcp"},
		Env:     map[string]string{"AGENTQL_API_KEY": "key"},
	})
	agentqlTools.Connect(ctx)
	defer agentqlTools.Disconnect()
	agentqlTools.RegisterTools(toolRegistry)

	// Create model provider
	modelProvider := model.NewAnthropicProvider("claude-3-5-sonnet", "your-api-key")

	// Create specialist agent with MCP tools
	scraper := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "scraper-specialist",
		Instruction: "Extract data from websites",
		Tools:       []string{"extract-web-data"},
	}, modelProvider, toolRegistry)

	// Create analyzer agent
	analyzer := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "data-analyzer",
		Instruction: "Analyze extracted data",
	}, modelProvider, toolRegistry)

	// Create coordinator with sub-agents
	coordinator := agent.NewAgent(agent.AgentConfig{
		Name:      "research-coordinator",
		SubAgents: []agent.Agent{scraper, analyzer},
	})

	// Create hierarchical tool registry
	coordinatorTools := agents.CreateHierarchicalToolRegistry(
		coordinator.(*agent.BaseAgent),
	)

	// Create coordinator LLM agent
	coordinatorAgent := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name: "coordinator",
		Instruction: `Coordinate research tasks.
Available agents:
- scraper-specialist: Extract data using AgentQL (third-party MCP tool)
- data-analyzer: Analyze extracted data

Use transfer_to_agent to delegate.`,
		Tools: coordinatorTools.ListToolNames(),
	}, modelProvider, coordinatorTools)

	// Execute - coordinator delegates to scraper which uses MCP tools!
	output, err := coordinatorAgent.Execute(ctx, &agent.AgentInput{
		Instruction: "Research product prices from e-commerce sites",
	})

	if err != nil {
		log.Fatalf("Execution failed: %v", err)
	}

	fmt.Printf("Coordinated research result: %v\n", output.Result)
}

// Example6_MCPToolsWithWorkflows demonstrates MCP tools in workflow agents
func Example6_MCPToolsWithWorkflows() {
	ctx := context.Background()

	// Set up MCP tools
	toolRegistry := tools.NewToolRegistry()

	searchTools, _ := mcp.NewMCPToolset(mcp.StdioConnectionParams{
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-brave-search"},
		Env:     map[string]string{"BRAVE_API_KEY": "key"},
	})
	searchTools.Connect(ctx)
	defer searchTools.Disconnect()
	searchTools.RegisterTools(toolRegistry)

	modelProvider := model.NewAnthropicProvider("claude-3-5-sonnet", "your-api-key")

	// Create sequential workflow with MCP tools
	searcher := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "searcher",
		Instruction: "Search the web for information",
		Tools:       []string{"brave_web_search"}, // MCP tool
	}, modelProvider, toolRegistry)

	summarizer := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "summarizer",
		Instruction: "Summarize search results",
	}, modelProvider, toolRegistry)

	formatter := agents.NewLlmAgent(agents.LlmAgentConfig{
		Name:        "formatter",
		Instruction: "Format as structured report",
	}, modelProvider, toolRegistry)

	// Build sequential pipeline
	pipeline := agents.NewSequential().
		WithName("research-pipeline").
		Add(searcher).    // Uses MCP tool
		Add(summarizer).
		Add(formatter).
		WithPassOutput(true).
		Build()

	// Execute pipeline
	output, err := pipeline.Execute(ctx, &agent.AgentInput{
		Instruction: "Research quantum computing breakthroughs",
	})

	if err != nil {
		log.Fatalf("Pipeline failed: %v", err)
	}

	fmt.Printf("Research report:\n%s\n", output.Result)
	fmt.Printf("Pipeline execution time: %.2f seconds\n",
		output.Metadata["execution_time"].(float64))
}
