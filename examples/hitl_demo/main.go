package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/seyi/dagens/pkg/graph"
	"github.com/seyi/dagens/pkg/hitl"
)

// SimpleNode is a basic node that performs an operation and stores results in state
type SimpleNode struct {
	*graph.BaseNode
	operation string
}

func NewSimpleNode(id, operation string) *SimpleNode {
	return &SimpleNode{
		BaseNode:  graph.NewBaseNode(id, "simple"),
		operation: operation,
	}
}

func (n *SimpleNode) Execute(ctx context.Context, state graph.State) error {
	log.Printf("Executing node %s with operation: %s", n.ID(), n.operation)

	switch n.operation {
	case "start":
		state.Set("start_time", time.Now().Unix())
		state.Set("data", "initial_data")
		log.Printf("Set initial data in state")
	case "process":
		if data, exists := state.Get("data"); exists {
			if str, ok := data.(string); ok {
				processed := fmt.Sprintf("processed_%s", str)
				state.Set("data", processed)
				log.Printf("Processed data: %s", processed)
			}
		}
	case "end":
		if data, exists := state.Get("data"); exists {
			log.Printf("Final data: %s", data)
		}
		if response, exists := state.Get("human_response"); exists {
			log.Printf("Human response received: %s", response)
		}
		if responseText, exists := state.Get("human_response_text"); exists {
			log.Printf("Human response text: %s", responseText)
		}
	}

	return nil
}

// CustomResumableExecutor implements the ResumableExecutor interface for our demo
type CustomResumableExecutor struct {
	graphID      string
	graphVersion string
	nodes        map[string]graph.Node
	currentNode  string
	state        graph.State
}

func NewCustomResumableExecutor(graphID, graphVersion string, nodes map[string]graph.Node) *CustomResumableExecutor {
	return &CustomResumableExecutor{
		graphID:      graphID,
		graphVersion: graphVersion,
		nodes:        nodes,
		currentNode:  "",
	}
}

func (c *CustomResumableExecutor) ExecuteCurrent(state graph.State) (graph.State, error) {
	// In a real implementation, this would execute the current node in the graph
	// For this demo, we'll just return the state unchanged
	c.state = state
	return state, nil
}

func (c *CustomResumableExecutor) ResumeFromNode(nodeID string, state graph.State) error {
	// In a real implementation, this would continue the graph execution from the given node
	// For this demo, let's execute the "end" node after the human response
	log.Printf("Resuming execution from node: %s", nodeID)
	
	if endNode, exists := c.nodes["end"]; exists {
		ctx := context.Background()
		if err := endNode.Execute(ctx, state); err != nil {
			return fmt.Errorf("failed to execute end node: %w", err)
		}
	}
	
	return nil
}

func (c *CustomResumableExecutor) CurrentNodeID() string {
	return c.currentNode
}

// SimpleIdempotencyStore implements the IdempotencyStore interface for the demo
type SimpleIdempotencyStore struct {
	store map[string]bool
}

func (s *SimpleIdempotencyStore) Exists(key string) (bool, error) {
	_, exists := s.store[key]
	return exists, nil
}

func (s *SimpleIdempotencyStore) Set(key string, ttl time.Duration) error {
	s.store[key] = true
	return nil
}

func (s *SimpleIdempotencyStore) SetNX(key string, ttl time.Duration) (bool, error) {
	if _, exists := s.store[key]; exists {
		return false, nil
	}
	s.store[key] = true
	return true, nil
}

func (s *SimpleIdempotencyStore) Delete(key string) error {
	delete(s.store, key)
	return nil
}

func main() {
	log.Println("Starting HITL (Human-in-the-Loop) Demo")

	// Create a graph with nodes
	graphBuilder := graph.NewGraph("hitl-demo")
	
	// Add nodes to the graph
	startNode := NewSimpleNode("start", "start")
	graphBuilder.AddNode(startNode)
	
	// Create a human response manager to use with the human node
	responseManager := hitl.NewHumanResponseManager()
	
	// Create a HumanNode with a longer timeout to trigger checkpointing
	humanNode := hitl.NewHumanNode(hitl.HumanNodeConfig{
		ID:                 "human_input",
		Prompt:             "Please review the data and select an option or provide feedback",
		Options:            []string{"Approve", "Reject", "Request Changes"},
		Timeout:            30 * time.Second, // Longer timeout (> 5 seconds threshold) to trigger checkpointing
		ShortWaitThreshold: 5 * time.Second,  // Short threshold to force checkpointing
		MaxConcurrentWaits: 1000,
		ResponseManager:    responseManager,
		CheckpointStore:    hitl.NewRedisCheckpointStore(), // Using in-memory for demo
		CallbackSecret:     []byte("demo-secret-key"),      // In production, use a secure key
	})
	graphBuilder.AddNode(humanNode)
	
	endNode := NewSimpleNode("end", "end")
	graphBuilder.AddNode(endNode)

	// Set up graph structure (start -> human -> end)
	// Add edges to connect the nodes
	graphBuilder.AddEdge(graph.NewDirectEdge("start", "human_input"))
	graphBuilder.AddEdge(graph.NewDirectEdge("human_input", "end"))
	graphBuilder.SetEntry("start")
	graphBuilder.AddFinish("end")

	// Validate the graph
	if err := graphBuilder.Validate(); err != nil {
		log.Fatalf("Invalid graph: %v", err)
	}

	log.Printf("Graph created with %d nodes", graphBuilder.NodeCount())

	// Create initial state
	initialState := graph.NewMemoryState()
	initialState.Set("graph_id", "hitl-demo-1")
	initialState.Set("user_id", "demo-user")

	// Execute the start node manually to initialize the state
	log.Println("Executing start node...")
	ctx := context.Background()
	if err := startNode.Execute(ctx, initialState); err != nil {
		log.Printf("Error executing start node: %v", err)
	}

	// Set up the HITL components
	checkpointStore := hitl.NewRedisCheckpointStore() // Using in-memory implementation
	graphRegistry := hitl.NewSimpleGraphRegistry()
	
	// Register the graph
	graphRegistry.RegisterGraph(hitl.GraphDefinition{
		ID:      "hitl-demo",
		Version: "v1.0.0",
	})

	// Create executor
	nodes := map[string]graph.Node{
		"start":       startNode,
		"human_input": humanNode,
		"end":         endNode,
	}
	_ = NewCustomResumableExecutor("hitl-demo", "v1.0.0", nodes)

	// Execute the human node - this will trigger checkpointing since timeout > threshold
	log.Println("Executing human node (this will trigger checkpointing)...")
	
	// Serialize the current state for the checkpoint
	stateData, err := initialState.Marshal()
	if err != nil {
		log.Printf("Error serializing state: %v", err)
		return
	}

	// Execute the human node - this will create a checkpoint since timeout > threshold
	if err := humanNode.Execute(ctx, initialState); err != nil {
		log.Printf("Human node execution result: %v", err)
		
		if err == hitl.ErrHumanInteractionPending {
			log.Println("Human interaction pending - checkpoint created")
			
			// Simulate human response after a delay
			go func() {
				time.Sleep(3 * time.Second) // Wait before delivering response
				
				// Create a human response
				response := &hitl.HumanResponse{
					SelectedOption: "Approve",
					FreeformText:   "This looks good to me",
					Payload:        map[string]interface{}{"reviewer": "demo-user", "timestamp": time.Now().Unix()},
					Timestamp:      time.Now(),
				}
				
				// Create a simple idempotency store for the demo
				idempotencyStore := &SimpleIdempotencyStore{
					store: make(map[string]bool),
				}
				
				// Get the request ID from the state (it should be stored there during execution)
				// For this demo, we'll create a mock checkpoint with the actual serialized state
				mockRequestID := "demo-request-checkpoint-789"
				
				// Create a proper checkpoint with the serialized state
				mockCheckpoint := &hitl.ExecutionCheckpoint{
					GraphID:      "hitl-demo",
					GraphVersion: "v1.0.0",
					NodeID:       "human_input",
					StateData:    stateData, // Use the actual serialized state
					RequestID:    mockRequestID,
					CreatedAt:    time.Now(),
					ExpiresAt:    time.Now().Add(24 * time.Hour),
				}
				
				// Store the checkpoint
				if err := checkpointStore.Create(mockCheckpoint); err != nil {
					log.Printf("Error creating checkpoint: %v", err)
					return
				}
				
				// Resume the graph execution with the human response
				log.Println("Resuming graph with human response...")
				if err := hitl.ResumeGraphFromCheckpoint(
					mockRequestID,
					response,
					checkpointStore,
					idempotencyStore,
					graphRegistry,
					func(graphID, graphVersion string) hitl.ResumableExecutor {
						return NewCustomResumableExecutor(graphID, graphVersion, nodes)
					},
				); err != nil {
					log.Printf("Error resuming graph: %v", err)
				} else {
					log.Println("Graph execution resumed successfully")
				}
			}()
		} else {
			log.Printf("Human node execution failed: %v", err)
		}
	} else {
		log.Println("Human node executed without pausing")
	}

	// Keep the program running to see the results
	time.Sleep(20 * time.Second)
	log.Println("Demo completed")
}