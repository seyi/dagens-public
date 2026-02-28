package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/graph"
	"github.com/seyi/dagens/pkg/registry"
	"github.com/seyi/dagens/pkg/scheduler"
	"github.com/seyi/dagens/pkg/telemetry"
)

// This example demonstrates 5 common AI Agent workflows using the Dagens distributed graph package.
// Note: In a real distributed setup, you would use the actual DistributedAgentRegistry.
// Here we use mocks to simulate the environment for demonstration purposes.

func main() {
	// 0. Initialize Telemetry (Jaeger)
	// Explicitly configure OTLP to ensure it works regardless of environment variables
	otelConfig := telemetry.TracerConfig{
		ServiceName:    "dagens-distributed-example",
		ServiceVersion: "1.0.0",
		Endpoint:       "localhost:4317",
		ExporterType:   telemetry.ExporterOTLP,
		SampleRatio:    1.0,
		Insecure:       true,
	}
	
	// We manually initialize the global collector to ensure we use the OTEL config
	_, err := telemetry.InitOTELTelemetry(otelConfig)
	if err != nil {
		log.Printf("Failed to initialize OTLP telemetry: %v. Falling back to env vars.", err)
		telemetry.InitGlobalTelemetry("dagens-distributed-example", "1.0.0")
	} else {
		// Hack to set the private global collector if possible, or just use this one
		// Since we can't easily set the private global var from outside, 
		// we will rely on InitGlobalTelemetry respecting env vars, 
		// BUT we will also set the env vars programmatically here to be double sure.
		log.Println("OTLP Telemetry configured for localhost:4317")
	}
	
	// Re-initialize global with env vars set programmatically to be safe
	os.Setenv("OTEL_EXPORTER_TYPE", "otlp")
	os.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")
	os.Setenv("OTEL_SERVICE_NAME", "dagens-distributed-example")
	
	telemetry.InitGlobalTelemetry("dagens-distributed-example", "1.0.0")
	defer telemetry.ShutdownGlobalTelemetry(context.Background())

	// 1. Setup Distributed Infrastructure (Simulated)
	sched := setupDistributedInfrastructure()
	defer sched.Stop()

	// 2. Run Examples
	runExample("1. Map-Reduce Research", buildMapReduceGraph(), sched)
	runExample("2. Reflection/Critique Loop", buildReflectionLoopGraph(), sched)
	runExample("3. Plan-and-Execute", buildPlanExecuteGraph(), sched)
	runExample("4. Fan-Out/Fan-In Verification", buildVerificationGraph(), sched)
	runExample("5. Human-in-the-Loop Approval", buildHumanApprovalGraph(), sched)

	// Ensure all traces are sent
	ctx := context.Background()
	telemetry.ForceFlushGlobalTelemetry(ctx)
	time.Sleep(2 * time.Second) // Give OTLP exporter time to drain
}

// -----------------------------------------------------------------------------
// Example 1: Map-Reduce Research
// Scenario: A Researcher breaks a topic into sub-topics, multiple Analysts process
// them in parallel, and a Writer synthesizes the results.
// -----------------------------------------------------------------------------
func buildMapReduceGraph() *graph.Graph {
	g := graph.NewGraph("map-reduce-research")

	// 1. Planner Agent (The Mapper)
	planner := graph.NewFunctionNode("planner-agent", func(ctx context.Context, s graph.State) error {
		log.Println("[Planner] Breaking 'Future of AI' into 3 sub-topics...")
		return nil
	})

	// 2. Analyst Agents (The Workers)
	analyst1 := graph.NewFunctionNode("analyst-tech", func(ctx context.Context, s graph.State) error {
		log.Println("[Analyst-1] Analyzing Technical Trends...")
		time.Sleep(100 * time.Millisecond) // Simulate work
		return nil
	})
	analyst2 := graph.NewFunctionNode("analyst-ethical", func(ctx context.Context, s graph.State) error {
		log.Println("[Analyst-2] Analyzing Ethical Implications...")
		time.Sleep(150 * time.Millisecond)
		return nil
	})
	analyst3 := graph.NewFunctionNode("analyst-economic", func(ctx context.Context, s graph.State) error {
		log.Println("[Analyst-3] Analyzing Economic Impact...")
		time.Sleep(120 * time.Millisecond)
		return nil
	})

	// Parallel Execution Block
	parallelAnalysis := graph.NewParallelNode("parallel-analysis", []graph.Node{analyst1, analyst2, analyst3})

	// 3. Writer Agent (The Reducer)
	writer := graph.NewFunctionNode("writer-agent", func(ctx context.Context, s graph.State) error {
		log.Println("[Writer] Synthesizing all 3 reports into final article.")
		return nil
	})

	g.AddNode(planner)
	g.AddNode(parallelAnalysis)
	g.AddNode(writer)

	g.SetEntry("planner-agent")
	g.AddFinish("writer-agent")

	g.AddEdge(graph.NewDirectEdge("planner-agent", "parallel-analysis"))
	g.AddEdge(graph.NewDirectEdge("parallel-analysis", "writer-agent"))

	return g
}

// -----------------------------------------------------------------------------
// Example 2: Reflection/Critique Loop
// Scenario: A Coder writes code, a Reviewer critiques it. If bad, it loops back.
// -----------------------------------------------------------------------------
func buildReflectionLoopGraph() *graph.Graph {
	g := graph.NewGraph("reflection-loop")

	// 1. Coder Agent
	coder := graph.NewFunctionNode("coder-agent", func(ctx context.Context, s graph.State) error {
		log.Println("[Coder] Writing Python script...")
		return nil
	})

	// 2. Reviewer Agent
	// Uses 'Metadata' to simulate state (e.g., quality score)
	reviewer := graph.NewFunctionNode("reviewer-agent", func(ctx context.Context, s graph.State) error {
		// Simulate random quality check
		log.Println("[Reviewer] Checking code quality...")
		// In a real app, this would check agent output
		return nil
	})

	// 3. Conditional Logic
	// We simulate a loop by using a LoopNode that wraps the Coder+Reviewer
	// Note: Our current simple V1 LoopNode wraps a SINGLE child.
	// For complex multi-node loops, we'd define the loop condition in the graph edges or
	// wrap the sub-graph in a SubGraphNode (advanced).
	// For this example, we'll implement a simple "Self-Correction" node that loops internally.

	improver := graph.NewLoopNode(graph.LoopConfig{
		ID:       "code-improvement-loop",
		MaxIters: 3,
		Condition: func(s graph.State) bool {
			// Return TRUE to continue looping (while quality < threshold)
			// Here we simulate loop running 3 times then stopping
			log.Println("   ...Code quality insufficient, retrying...")
			return true
		},
		Child: graph.NewFunctionNode("correction-step", func(ctx context.Context, s graph.State) error {
			log.Println("   [Improver] Refactoring code based on critique.")
			return nil
		}),
	})

	// 4. Final Deployer
	deployer := graph.NewFunctionNode("deployer-agent", func(ctx context.Context, s graph.State) error {
		log.Println("[Deployer] Code approved. Deploying to prod.")
		return nil
	})

	g.AddNode(coder)
	g.AddNode(reviewer)
	g.AddNode(improver)
	g.AddNode(deployer)

	g.SetEntry("coder-agent")
	g.AddFinish("deployer-agent")

	g.AddEdge(graph.NewDirectEdge("coder-agent", "reviewer-agent"))
	g.AddEdge(graph.NewDirectEdge("reviewer-agent", "code-improvement-loop"))
	g.AddEdge(graph.NewDirectEdge("code-improvement-loop", "deployer-agent"))

	return g
}

// -----------------------------------------------------------------------------
// Example 3: Plan-and-Execute
// Scenario: A Planner creates a list of tasks. A dynamic "Dispatcher" executes them.
// -----------------------------------------------------------------------------
func buildPlanExecuteGraph() *graph.Graph {
	g := graph.NewGraph("plan-and-execute")

	planner := graph.NewFunctionNode("planner", func(ctx context.Context, s graph.State) error {
		log.Println("[Planner] Created plan with 5 steps.")
		return nil
	})

	// In Dagens, the "Executor" is often the Scheduler itself.
	// But we can model a "Dispatcher" agent that iterates through the plan.
	dispatcher := graph.NewLoopNode(graph.LoopConfig{
		ID:       "task-executor",
		MaxIters: 5,
		Child: graph.NewFunctionNode("single-task-worker", func(ctx context.Context, s graph.State) error {
			log.Println("   [Worker] Executing step from plan...")
			return nil
		}),
		Condition: func(s graph.State) bool {
			return true // Always run for MaxIters (simulating 5 steps)
		},
	})

	summarizer := graph.NewFunctionNode("summarizer", func(ctx context.Context, s graph.State) error {
		log.Println("[Summarizer] Plan execution complete. Generating summary.")
		return nil
	})

	g.AddNode(planner)
	g.AddNode(dispatcher)
	g.AddNode(summarizer)

	g.SetEntry("planner")
	g.AddFinish("summarizer")

	g.AddEdge(graph.NewDirectEdge("planner", "task-executor"))
	g.AddEdge(graph.NewDirectEdge("task-executor", "summarizer"))

	return g
}

// -----------------------------------------------------------------------------
// Example 4: Fan-Out/Fan-In Verification
// Scenario: A "Claim" is checked against 3 different knowledge bases (Web, Internal, Legal).
// Only if 2/3 agree is it marked as "Verified".
// -----------------------------------------------------------------------------
func buildVerificationGraph() *graph.Graph {
	g := graph.NewGraph("fact-check-verification")

	claimDetector := graph.NewFunctionNode("claim-detector", func(ctx context.Context, s graph.State) error {
		log.Println("[Detector] Identified claim: 'The sky is green'.")
		return nil
	})

	// Fan-Out
	webSearch := graph.NewFunctionNode("verify-web", func(ctx context.Context, s graph.State) error {
		log.Println("[Web-Agent] Searching Google... Result: False")
		return nil
	})
	internalDB := graph.NewFunctionNode("verify-db", func(ctx context.Context, s graph.State) error {
		log.Println("[DB-Agent] Checking Internal Wiki... Result: False")
		return nil
	})
	legalCheck := graph.NewFunctionNode("verify-legal", func(ctx context.Context, s graph.State) error {
		log.Println("[Legal-Agent] Checking Compliance... Result: Neutral")
		return nil
	})

	parallelChecks := graph.NewParallelNode("parallel-checks", []graph.Node{webSearch, internalDB, legalCheck})

	// Fan-In (Consensus)
	consensus := graph.NewFunctionNode("consensus-agent", func(ctx context.Context, s graph.State) error {
		log.Println("[Consensus] 2/3 sources say FALSE. Marking claim as DEBUNKED.")
		return nil
	})

	g.AddNode(claimDetector)
	g.AddNode(parallelChecks)
	g.AddNode(consensus)

	g.SetEntry("claim-detector")
	g.AddFinish("consensus-agent")

	g.AddEdge(graph.NewDirectEdge("claim-detector", "parallel-checks"))
	g.AddEdge(graph.NewDirectEdge("parallel-checks", "consensus-agent"))

	return g
}

// -----------------------------------------------------------------------------
// Example 5: Human-in-the-Loop Approval
// Scenario: An agent generates content. It pauses for Human "Mock" Approval.
// If approved, it publishes. If rejected, it deletes.
// -----------------------------------------------------------------------------
func buildHumanApprovalGraph() *graph.Graph {
	g := graph.NewGraph("human-approval")

	creator := graph.NewFunctionNode("content-creator", func(ctx context.Context, s graph.State) error {
		log.Println("[Creator] Generated blog post draft.")
		return nil
	})

	// Conditional Branch
	approvalNode := graph.NewConditionalNode(graph.ConditionalConfig{
		ID: "human-review-gate",
		Condition: func(s graph.State) bool {
			// Simulate human input check
			// In real system, this might check a DB flag or wait for signal
			approved := true // Toggle this to test rejection
			if approved {
				log.Println("[Human] APPROVED the draft.")
				return true
			} else {
				log.Println("[Human] REJECTED the draft.")
				return false
			}
		},
		TrueNode: graph.NewFunctionNode("publisher", func(ctx context.Context, s graph.State) error {
			log.Println("[Publisher] Publishing to CMS...")
			return nil
		}),
		FalseNode: graph.NewFunctionNode("discarder", func(ctx context.Context, s graph.State) error {
			log.Println("[Discarder] Deleting draft.")
			return nil
		}),
	})

	g.AddNode(creator)
	g.AddNode(approvalNode)

	g.SetEntry("content-creator")
	// Both branches act as finish points in this simple topology
	// Note: ConditionalNode handles the branching logic internally via Execute()
	// So we add it as a finish candidate if it's the last explicit node in the main chain
	// But technically the True/False nodes are the real finish points.
	// For Dagens graph validation, we just mark the last reachable nodes.
	// Since True/False nodes are nested in ConditionalNode, they are executed *by* it.
	// So "human-review-gate" is effectively the last top-level node.
	g.AddFinish("human-review-gate")

	g.AddEdge(graph.NewDirectEdge("content-creator", "human-review-gate"))

	return g
}

// -----------------------------------------------------------------------------
// Infrastructure Helpers
// -----------------------------------------------------------------------------

func runExample(name string, g *graph.Graph, sched *scheduler.Scheduler) {
	fmt.Printf("\n=== Running Example: %s ===\n", name)

	// Start a span for the example
	tracer := telemetry.GetGlobalTelemetry().GetTracer()
	_, span := tracer.StartSpan(context.Background(), "example-run")
	span.SetAttribute("example.name", name)
	defer span.End()

	// Compile
	compiler := graph.NewDAGCompiler()
	job, err := compiler.Compile(g, &agent.AgentInput{Instruction: "run example"})
	if err != nil {
		log.Fatalf("Compilation failed: %v", err)
	}

	// Submit
	if err := sched.SubmitJob(job); err != nil {
		log.Fatalf("Submission failed: %v", err)
	}

	// Wait
	waitForJob(sched, job.ID)
	fmt.Printf("=== Example %s Completed ===\n", name)
}

func waitForJob(sched *scheduler.Scheduler, jobID string) {
	for {
		job, err := sched.GetJob(jobID)
		if err != nil {
			log.Printf("Job not found: %v", err)
			return
		}

		if job.Status == scheduler.JobCompleted {
			return
		}
		if job.Status == scheduler.JobFailed {
			log.Printf("Job failed!")
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// setupDistributedInfrastructure mocks the Registry and Executor for the demo
func setupDistributedInfrastructure() *scheduler.Scheduler {
	// 1. Mock Registry
	reg := &MockRegistry{
		nodes: []registry.NodeInfo{
			{ID: "worker-1", Healthy: true},
			{ID: "worker-2", Healthy: true},
			{ID: "worker-3", Healthy: true},
		},
	}

	// 2. Mock Task Executor
	// In a real app, this would be remote.NewRemoteExecutor(...)
	executor := &MockExecutor{}

	// 3. Scheduler
	sched := scheduler.NewScheduler(reg, executor)
	sched.Start()
	return sched
}

// MockRegistry implements registry.Registry
type MockRegistry struct {
	nodes []registry.NodeInfo
}

func (m *MockRegistry) GetHealthyNodes() []registry.NodeInfo { return m.nodes }
func (m *MockRegistry) GetNode(id string) (registry.NodeInfo, bool) {
	return registry.NodeInfo{ID: id, Healthy: true}, true
}
func (m *MockRegistry) GetNodeID() string { return "master" }
func (m *MockRegistry) GetNodesByCapability(c string) []registry.NodeInfo { return m.nodes }

// MockExecutor implements scheduler.TaskExecutor
type MockExecutor struct{}

func (m *MockExecutor) ExecuteOnNode(ctx context.Context, nodeID, agentName string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// Start a span to simulate remote work visibility in Jaeger
	tracer := telemetry.GetGlobalTelemetry().GetTracer()
	ctx, span := tracer.StartSpan(ctx, "remote-worker-exec")
	span.SetAttribute("worker.node_id", nodeID)
	span.SetAttribute("agent.name", agentName)
	defer span.End()

	// Simulate network latency
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(20 * time.Millisecond):
	}

	return &agent.AgentOutput{Result: "success"}, nil
}
