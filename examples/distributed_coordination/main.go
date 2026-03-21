package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/registry"
)

// HeavyComputeAgent simulates a resource-intensive agent
type HeavyComputeAgent struct {
	id   string
	name string
}

func (a *HeavyComputeAgent) ID() string             { return a.id }
func (a *HeavyComputeAgent) Name() string           { return a.name }
func (a *HeavyComputeAgent) Description() string    { return "Simulates heavy computation" }
func (a *HeavyComputeAgent) Capabilities() []string { return []string{"heavy-compute"} }
func (a *HeavyComputeAgent) Dependencies() []agent.Agent { return nil }
func (a *HeavyComputeAgent) Partition() string      { return "" }

func (a *HeavyComputeAgent) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	log.Printf("[%s] Starting heavy computation: %s", a.name, input.Instruction)
	
	// Simulate work
	select {
	case <-time.After(2 * time.Second):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	log.Printf("[%s] Finished computation", a.name)
	return &agent.AgentOutput{
		Result: fmt.Sprintf("Computed: %s", input.Instruction),
		Metadata: map[string]interface{}{
			"processor": a.name,
		},
	}, nil
}

func main() {
	// 1. Configuration
	etcdEndpoints := os.Getenv("ETCD_ENDPOINTS")
	if etcdEndpoints == "" {
		log.Println("ETCD_ENDPOINTS not set. Please run:")
		log.Println("  docker run -p 2379:2379 -p 2380:2380 --name etcd quay.io/coreos/etcd:v3.5.0 /usr/local/bin/etcd --advertise-client-urls http://0.0.0.0:2379 --listen-client-urls http://0.0.0.0:2379")
		log.Println("Then set ETCD_ENDPOINTS=localhost:2379")
		os.Exit(1)
	}

	log.Println("Starting Distributed Coordination Example...")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 2. Start Worker Node (Node A)
	workerReg, err := registry.NewDistributedAgentRegistry(registry.RegistryConfig{
		EtcdEndpoints:    []string{etcdEndpoints},
		NodeID:           "worker-node-a",
		NodeName:         "Worker A",
		NodeAddress:      "localhost",
		NodePort:         50051,
		NodeCapabilities: []string{"heavy-compute"},
		LeaseTTL:         10,
	})
	if err != nil {
		log.Fatalf("Failed to create worker registry: %v", err)
	}
	if err := workerReg.Start(ctx); err != nil {
		log.Fatalf("Failed to start worker registry: %v", err)
	}
	defer workerReg.Stop()
	log.Println("✅ Worker Node A started and registered")

	// 3. Start Orchestrator Node (Node B)
	orchReg, err := registry.NewDistributedAgentRegistry(registry.RegistryConfig{
		EtcdEndpoints:    []string{etcdEndpoints},
		NodeID:           "orch-node-b",
		NodeName:         "Orchestrator B",
		NodeAddress:      "localhost",
		NodePort:         50052,
		LeaseTTL:         10,
	})
	if err != nil {
		log.Fatalf("Failed to create orchestrator registry: %v", err)
	}
	if err := orchReg.Start(ctx); err != nil {
		log.Fatalf("Failed to start orchestrator registry: %v", err)
	}
	defer orchReg.Stop()
	log.Println("✅ Orchestrator Node B started")

	// 4. Distributed Mutex Example
	// Coordinate access to a shared resource (e.g., logging to a file)
	log.Println("\n--- Distributed Mutex Example ---")
	sharedResourceMutex := orchReg.NewMutex("/dagens/resources/shared-log")
	
	var wg sync.WaitGroup
	wg.Add(2)

	// Simulate two concurrent processes trying to access the resource
	go func(id string) {
		defer wg.Done()
		log.Printf("[%s] Requesting lock...", id)
		
		if err := sharedResourceMutex.Lock(ctx); err != nil {
			log.Printf("[%s] Failed to acquire lock: %v", id, err)
			return
		}
		defer sharedResourceMutex.Unlock(ctx)

		log.Printf("[%s] 🔒 Lock acquired! Doing critical work...", id)
		time.Sleep(1 * time.Second) // Hold lock
		log.Printf("[%s] 🔓 Releasing lock", id)
	}("Process-1")

	go func(id string) {
		defer wg.Done()
		time.Sleep(100 * time.Millisecond) // Slight delay
		log.Printf("[%s] Requesting lock...", id)
		
		if err := sharedResourceMutex.Lock(ctx); err != nil {
			log.Printf("[%s] Failed to acquire lock: %v", id, err)
			return
		}
		defer sharedResourceMutex.Unlock(ctx)

		log.Printf("[%s] 🔒 Lock acquired! Doing critical work...", id)
		time.Sleep(1 * time.Second)
		log.Printf("[%s] 🔓 Releasing lock", id)
	}("Process-2")

	wg.Wait()

	// 5. Distributed Semaphore Example
	// Limit concurrent "heavy" tasks across the cluster
	log.Println("\n--- Distributed Semaphore Example ---")
	const maxConcurrency = 2
	sem := orchReg.NewSemaphore("/dagens/quotas/heavy-compute", maxConcurrency)
	
	// Create a heavy agent
	heavyAgent := &HeavyComputeAgent{id: "heavy-1", name: "HeavyAgent"}

	// Launch 5 concurrent tasks, but only 'maxConcurrency' should run at once
	tasks := []string{"Task A", "Task B", "Task C", "Task D", "Task E"}
	var taskWg sync.WaitGroup

	for _, taskName := range tasks {
		taskWg.Add(1)
		go func(task string) {
			defer taskWg.Done()

			log.Printf("[%s] Requesting execution permit...", task)
			if err := sem.Acquire(ctx); err != nil {
				log.Printf("[%s] Failed to acquire permit: %v", task, err)
				return
			}
			// Important: Release permit when done
			defer sem.Release(ctx)

			log.Printf("[%s] 🟢 Permit granted! Executing...", task)
			
			// Execute the agent
			_, err := heavyAgent.Execute(ctx, &agent.AgentInput{Instruction: task})
			if err != nil {
				log.Printf("[%s] Execution failed: %v", task, err)
			}
		}(taskName)
		
		// Stagger slightly
		time.Sleep(200 * time.Millisecond)
	}

	taskWg.Wait()
	log.Println("All tasks completed.")

	// 6. Service Discovery
	log.Println("\n--- Service Discovery Example ---")
	nodes := orchReg.GetNodesByCapability("heavy-compute")
	log.Printf("Found %d nodes with 'heavy-compute' capability:", len(nodes))
	for _, n := range nodes {
		log.Printf(" - %s (%s:%d)", n.Name, n.Address, n.Port)
	}

	// Keep alive until signal
	log.Println("\nExample complete. Press Ctrl+C to exit.")
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Println("Shutting down...")
}
