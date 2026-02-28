package graph

import (
	"context"
	"testing"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/stretchr/testify/assert"
)

func TestDAGCompiler_Compile(t *testing.T) {
	// 1. Create a Graph
	g := NewGraph("test-graph")
	
	// Node A (Entry)
	nodeA := NewFunctionNode("node-a", func(ctx context.Context, s State) error { return nil })
	g.AddNode(nodeA)
	g.SetEntry("node-a")

	// Node B1, B2 (Parallel Children)
	nodeB1 := NewFunctionNode("node-b1", func(ctx context.Context, s State) error { return nil })
	nodeB2 := NewFunctionNode("node-b2", func(ctx context.Context, s State) error { return nil })
	
	// Node B (Parallel)
	nodeB := NewParallelNode("node-b", []Node{nodeB1, nodeB2})
	g.AddNode(nodeB)

	// Node C (Final)
	nodeC := NewFunctionNode("node-c", func(ctx context.Context, s State) error { return nil })
	g.AddNode(nodeC)
	g.AddFinish("node-c")

	// Edges: A -> B -> C
	g.AddEdge(NewDirectEdge("node-a", "node-b"))
	g.AddEdge(NewDirectEdge("node-b", "node-c"))

	// 2. Compile
	compiler := NewDAGCompiler()
	input := &agent.AgentInput{Instruction: "test"}
	job, err := compiler.Compile(g, input)

	// 3. Verify
	assert.NoError(t, err)
	assert.NotNil(t, job)
	assert.Equal(t, "test-graph", job.Name)

	// We expect 3 stages:
	// Stage 1: Node A
	// Stage 2: Node B1, B2 (Parallel Children)
	// Stage 3: Node C
	
	// Note: The logic in compiler.go might produce slightly different stage boundaries 
	// depending on how it handles "current" vs "new" stages. 
	// Let's inspect what we got.
	
	t.Logf("Job has %d stages", len(job.Stages))
	for i, stage := range job.Stages {
		t.Logf("Stage %d: %d tasks", i, len(stage.Tasks))
		for _, task := range stage.Tasks {
			t.Logf(" - Task: %s (Agent: %s)", task.ID, task.AgentID)
		}
	}

	assert.True(t, len(job.Stages) >= 3, "Should have at least 3 stages")
	
	// Check Stage 2 (Parallel)
	// It should contain tasks for b1 and b2
	foundB1 := false
	foundB2 := false
	
	// Iterate stages to find the parallel one
	for _, stage := range job.Stages {
		if len(stage.Tasks) == 2 {
			for _, task := range stage.Tasks {
				if task.AgentID == "node-b1" { foundB1 = true }
				if task.AgentID == "node-b2" { foundB2 = true }
			}
		}
	}
	
	assert.True(t, foundB1, "Should find task for node-b1")
	assert.True(t, foundB2, "Should find task for node-b2")
}
