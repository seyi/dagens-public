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

// Distributed Services Examples
// Demonstrates Phase 3: ADK-compatible services with Spark distribution
package main

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/seyi/dagens/pkg/runtime"
	"github.com/seyi/dagens/pkg/runtime/services"
)

func main() {
	fmt.Println("=== Distributed Services Examples ===")
	fmt.Println()

	// Example 1: Service Bundle Creation
	fmt.Println("Example 1: Service Bundle Creation")
	example1_ServiceBundleCreation()
	fmt.Println()

	// Example 2: Session Management
	fmt.Println("Example 2: Session Management")
	example2_SessionManagement()
	fmt.Println()

	// Example 3: Artifact Storage
	fmt.Println("Example 3: Artifact Storage")
	example3_ArtifactStorage()
	fmt.Println()

	// Example 4: Vector Memory Search
	fmt.Println("Example 4: Vector Memory Search")
	example4_VectorMemorySearch()
	fmt.Println()

	// Example 5: Spark Task Coordination
	fmt.Println("Example 5: Spark Task Coordination")
	example5_SparkTaskCoordination()
	fmt.Println()

	// Example 6: Multi-Partition Simulation
	fmt.Println("Example 6: Multi-Partition Simulation")
	example6_MultiPartitionSimulation()
	fmt.Println()

	// Example 7: Service Statistics
	fmt.Println("Example 7: Service Statistics")
	example7_ServiceStatistics()
	fmt.Println()

	fmt.Println("=== All Examples Complete! ===")
}

// Example 1: Creating a service bundle
func example1_ServiceBundleCreation() {
	// Create in-memory service bundle for development
	bundle := services.NewInMemoryServiceBundle(4) // 4 partitions
	defer bundle.Close()

	fmt.Printf("Created service bundle with %d partitions\n", 4)
	fmt.Println("Services initialized:")
	fmt.Printf("  - SessionService: %T\n", bundle.SessionService)
	fmt.Printf("  - ArtifactService: %T\n", bundle.ArtifactService)
	fmt.Printf("  - MemoryService: %T\n", bundle.MemoryService)

	// Health check
	ctx := context.Background()
	if err := bundle.HealthCheck(ctx); err != nil {
		fmt.Printf("Health check failed: %v\n", err)
	} else {
		fmt.Println("Health check passed!")
	}
}

// Example 2: Session management with partition awareness
func example2_SessionManagement() {
	bundle := services.NewInMemoryServiceBundle(4)
	defer bundle.Close()

	ctx := context.Background()

	// Create multiple sessions for different users
	users := []string{"alice", "bob", "charlie"}

	for _, userID := range users {
		session := &runtime.Session{
			ID:        fmt.Sprintf("session-%s-%d", userID, time.Now().Unix()),
			UserID:    userID,
			State:     map[string]interface{}{"initialized": true},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		// Store session
		if err := bundle.SessionService.UpdateSession(ctx, session); err != nil {
			fmt.Printf("Failed to store session: %v\n", err)
			continue
		}

		fmt.Printf("Stored session for user %s: %s\n", userID, session.ID)

		// Retrieve session
		retrieved, err := bundle.SessionService.GetSession(ctx, session.ID)
		if err != nil {
			fmt.Printf("Failed to retrieve session: %v\n", err)
			continue
		}

		fmt.Printf("  Retrieved session: initialized=%v\n", retrieved.State["initialized"])
	}

	// List sessions for alice
	aliceSessions, _ := bundle.SessionService.ListSessions(ctx, "alice")
	fmt.Printf("\nAlice has %d session(s)\n", len(aliceSessions))
}

// Example 3: Artifact storage with size-aware caching
func example3_ArtifactStorage() {
	bundle := services.NewInMemoryServiceBundle(2)
	defer bundle.Close()

	ctx := context.Background()
	sessionID := "artifact-demo-session"

	// Store various artifacts
	artifacts := map[string][]byte{
		"config.json":    []byte(`{"model": "gpt-4", "temperature": 0.7}`),
		"prompt.txt":     []byte("You are a helpful AI assistant."),
		"data.csv":       []byte("name,age,city\nAlice,30,NYC\nBob,25,SF"),
		"image_meta.txt": []byte("Image size: 1024x768, Format: PNG"),
	}

	for name, data := range artifacts {
		if err := bundle.ArtifactService.SaveArtifact(ctx, sessionID, name, data); err != nil {
			fmt.Printf("Failed to save artifact %s: %v\n", name, err)
			continue
		}

		fmt.Printf("Saved artifact: %s (%d bytes)\n", name, len(data))
	}

	// List all artifacts
	artifactNames, _ := bundle.ArtifactService.ListArtifacts(ctx, sessionID)
	fmt.Printf("\nTotal artifacts: %d\n", len(artifactNames))

	// Retrieve specific artifact
	configData, _ := bundle.ArtifactService.GetArtifact(ctx, sessionID, "config.json")
	fmt.Printf("Retrieved config.json: %s\n", string(configData))

	// Delete artifact
	bundle.ArtifactService.DeleteArtifact(ctx, sessionID, "image_meta.txt")
	fmt.Println("Deleted image_meta.txt")
}

// Example 4: Vector memory search
func example4_VectorMemorySearch() {
	// Create bundle with custom embedding dimension
	bundle := services.NewServiceBundle(services.ServiceBundleConfig{
		NumPartitions:        1,
		SessionBackend:       services.NewInMemorySessionBackend(),
		SessionCacheEnabled:  true,
		ArtifactBackend:      services.NewInMemoryArtifactBackend(),
		ArtifactCacheEnabled: true,
		MemoryBackend:        services.NewInMemoryMemoryBackend(),
		MemoryCacheEnabled:   true,
		EmbeddingDim:         3, // Use 3D embeddings for this example
	})
	defer bundle.Close()

	ctx := context.Background()
	userID := "demo-user"

	// Store memories with embeddings
	memories := []struct {
		id        string
		content   string
		embedding []float64
	}{
		{
			id:        "mem-1",
			content:   "Python is a programming language",
			embedding: []float64{0.9, 0.1, 0.0}, // Tech-related
		},
		{
			id:        "mem-2",
			content:   "JavaScript is used for web development",
			embedding: []float64{0.85, 0.15, 0.0}, // Tech-related, similar to Python
		},
		{
			id:        "mem-3",
			content:   "Pasta is a delicious Italian dish",
			embedding: []float64{0.0, 0.0, 1.0}, // Food-related, very different
		},
	}

	for _, mem := range memories {
		memory := &runtime.Memory{
			ID:        mem.id,
			Content:   mem.content,
			Embedding: normalizeVector(mem.embedding),
			Metadata:  map[string]interface{}{"user_id": userID},
			CreatedAt: time.Now(),
		}

		if err := bundle.MemoryService.Store(ctx, memory); err != nil {
			fmt.Printf("Failed to store memory: %v\n", err)
			continue
		}

		fmt.Printf("Stored memory: %s\n", mem.content)
	}

	// Query for programming-related memories
	queryEmbedding := normalizeVector([]float64{1.0, 0.0, 0.0}) // Tech query
	results, _ := bundle.MemoryService.Query(ctx, userID, queryEmbedding, 2)

	fmt.Printf("\nTop 2 memories for tech query:\n")
	for i, mem := range results {
		fmt.Printf("  %d. %s\n", i+1, mem.Content)
	}
}

// Example 5: Spark task coordination
func example5_SparkTaskCoordination() {
	bundle := services.NewInMemoryServiceBundle(4)
	defer bundle.Close()

	// Simulate Spark task
	taskCtx := &services.SparkTaskContext{
		PartitionID:   0,
		TaskAttemptID: "task_0_0",
		StageID:       1,
		SessionID:     "coordinator-session",
	}

	coordinator := services.NewServiceCoordinator(bundle, taskCtx)
	ctx := context.Background()

	// Create session for task
	session := &runtime.Session{
		ID:        "coordinator-session",
		UserID:    "task-user",
		State:     map[string]interface{}{"stage": 1, "status": "running"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := coordinator.UpdateSessionForTask(ctx, session); err != nil {
		fmt.Printf("Failed to update session: %v\n", err)
		return
	}

	fmt.Printf("Task %s initialized session\n", taskCtx.TaskAttemptID)

	// Save artifacts from task
	taskResults := []byte(fmt.Sprintf("Results from partition %d", taskCtx.PartitionID))
	coordinator.SaveArtifactForTask(ctx, "results.txt", taskResults)

	fmt.Printf("Saved task results: %d bytes\n", len(taskResults))

	// Get partition statistics
	stats := coordinator.GetPartitionStats()
	fmt.Printf("Partition %d stats:\n", stats.PartitionID)
	fmt.Printf("  Sessions cached: %d\n", stats.SessionCacheStats.NumSessions)
	fmt.Printf("  Artifacts cached: %d\n", stats.ArtifactCacheStats.NumArtifacts)
}

// Example 6: Multi-partition simulation (Spark pattern)
func example6_MultiPartitionSimulation() {
	numPartitions := 4
	bundle := services.NewInMemoryServiceBundle(numPartitions)
	defer bundle.Close()

	ctx := context.Background()

	fmt.Printf("Simulating Spark job with %d partitions\n", numPartitions)

	// Simulate parallel tasks (one per partition)
	for partitionID := 0; partitionID < numPartitions; partitionID++ {
		// Each partition processes different data
		sessionID := fmt.Sprintf("partition-%d-session", partitionID)

		session := &runtime.Session{
			ID:        sessionID,
			UserID:    fmt.Sprintf("partition-%d-user", partitionID),
			State:     map[string]interface{}{"partition": partitionID, "processed": true},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		bundle.SessionService.UpdateSession(ctx, session)

		// Store partition results
		results := []byte(fmt.Sprintf("Partition %d results", partitionID))
		bundle.ArtifactService.SaveArtifact(ctx, sessionID, "output.txt", results)

		fmt.Printf("  Partition %d completed: session=%s, results=%d bytes\n",
			partitionID, sessionID, len(results))
	}

	// Aggregate statistics across all partitions
	stats := bundle.GetStats()
	totalSessions := 0
	totalArtifacts := 0

	for partID := 0; partID < numPartitions; partID++ {
		totalSessions += stats.SessionCacheStats[partID].NumSessions
		totalArtifacts += stats.ArtifactCacheStats[partID].NumArtifacts
	}

	fmt.Printf("\nAggregated results:\n")
	fmt.Printf("  Total sessions: %d\n", totalSessions)
	fmt.Printf("  Total artifacts: %d\n", totalArtifacts)
}

// Example 7: Service statistics and monitoring
func example7_ServiceStatistics() {
	bundle := services.NewInMemoryServiceBundle(4)
	defer bundle.Close()

	ctx := context.Background()

	// Create some data
	for i := 0; i < 10; i++ {
		session := &runtime.Session{
			ID:        fmt.Sprintf("session-%d", i),
			UserID:    fmt.Sprintf("user-%d", i%3), // 3 users
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			State:     map[string]interface{}{"index": i},
		}

		bundle.SessionService.UpdateSession(ctx, session)

		// Store artifacts
		data := []byte(fmt.Sprintf("data-%d", i))
		bundle.ArtifactService.SaveArtifact(ctx, session.ID, "data.txt", data)

		// Store memories
		embedding := make([]float64, 1536)
		for j := range embedding {
			embedding[j] = rand.Float64()
		}

		memory := &runtime.Memory{
			ID:        fmt.Sprintf("memory-%d", i),
			Content:   fmt.Sprintf("Memory %d", i),
			Embedding: normalizeVector(embedding),
			Metadata:  map[string]interface{}{"user_id": fmt.Sprintf("user-%d", i%3)},
			CreatedAt: time.Now(),
		}

		bundle.MemoryService.Store(ctx, memory)
	}

	// Get statistics
	stats := bundle.GetStats()

	fmt.Printf("Service Statistics (%d partitions):\n", stats.NumPartitions)
	fmt.Println()

	// Session statistics
	fmt.Println("Session Cache Statistics:")
	for partID := 0; partID < 4; partID++ {
		sessionStats := stats.SessionCacheStats[partID]
		fmt.Printf("  Partition %d: %d sessions (max: %d)\n",
			sessionStats.PartitionID,
			sessionStats.NumSessions,
			sessionStats.MaxSize)
	}
	fmt.Println()

	// Artifact statistics
	fmt.Println("Artifact Cache Statistics:")
	for partID := 0; partID < 4; partID++ {
		artifactStats := stats.ArtifactCacheStats[partID]
		fmt.Printf("  Partition %d: %d artifacts, %.2f%% full (%d/%d bytes)\n",
			artifactStats.PartitionID,
			artifactStats.NumArtifacts,
			artifactStats.UtilizationPct,
			artifactStats.TotalSize,
			artifactStats.MaxSize)
	}
	fmt.Println()

	// Memory statistics
	fmt.Println("Memory Index Statistics:")
	for partID := 0; partID < 4; partID++ {
		memoryStats := stats.MemoryIndexStats[partID]
		fmt.Printf("  Partition %d: %d memories indexed\n",
			memoryStats.PartitionID,
			memoryStats.NumMemories)
	}
}

// Helper function to normalize vectors
func normalizeVector(v []float64) []float64 {
	var magnitude float64
	for _, val := range v {
		magnitude += val * val
	}
	magnitude = 1.0 / (magnitude + 1e-10) // Avoid division by zero

	normalized := make([]float64, len(v))
	for i, val := range v {
		normalized[i] = val * magnitude
	}

	return normalized
}
