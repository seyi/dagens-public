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

package services

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/runtime"
)

// Test DistributedSessionService

func TestSessionService_BasicOperations(t *testing.T) {
	service := NewDistributedSessionService(DistributedSessionConfig{
		NumPartitions: 4,
		EnableCache:   true,
		CacheTTL:      5 * time.Minute,
		MaxCacheSize:  100,
	})
	defer service.Close()

	ctx := context.Background()

	// Create test session
	session := &runtime.Session{
		ID:        "session-1",
		UserID:    "user-1",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		State:     map[string]interface{}{"test": "data"},
	}

	// Test UpdateSession
	err := service.UpdateSession(ctx, session)
	if err != nil {
		t.Fatalf("UpdateSession failed: %v", err)
	}

	// Test GetSession
	retrieved, err := service.GetSession(ctx, "session-1")
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}

	if retrieved.ID != session.ID {
		t.Errorf("Expected session ID %s, got %s", session.ID, retrieved.ID)
	}

	if retrieved.UserID != session.UserID {
		t.Errorf("Expected user ID %s, got %s", session.UserID, retrieved.UserID)
	}

	// Test ListSessions
	sessions, err := service.ListSessions(ctx, "user-1")
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}

	if len(sessions) != 1 {
		t.Errorf("Expected 1 session, got %d", len(sessions))
	}
}

func TestSessionService_PartitionAwareness(t *testing.T) {
	service := NewDistributedSessionService(DistributedSessionConfig{
		NumPartitions: 4,
		EnableCache:   true,
	})
	defer service.Close()

	ctx := context.Background()

	// Create sessions for multiple users
	for i := 0; i < 10; i++ {
		session := &runtime.Session{
			ID:        fmt.Sprintf("session-%d", i),
			UserID:    fmt.Sprintf("user-%d", i),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		err := service.UpdateSession(ctx, session)
		if err != nil {
			t.Fatalf("UpdateSession failed for session %d: %v", i, err)
		}
	}

	// Verify cache distribution
	stats := service.GetCacheStats()
	if len(stats) != 4 {
		t.Errorf("Expected 4 partition stats, got %d", len(stats))
	}

	totalCached := 0
	for _, stat := range stats {
		totalCached += stat.NumSessions
	}

	if totalCached != 10 {
		t.Errorf("Expected 10 total cached sessions, got %d", totalCached)
	}
}

func TestSessionService_CacheEviction(t *testing.T) {
	service := NewDistributedSessionService(DistributedSessionConfig{
		NumPartitions: 1,
		EnableCache:   true,
		MaxCacheSize:  3, // Small cache for testing eviction
	})
	defer service.Close()

	ctx := context.Background()

	// Add more sessions than cache can hold
	for i := 0; i < 5; i++ {
		session := &runtime.Session{
			ID:        fmt.Sprintf("session-%d", i),
			UserID:    "user-1",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		err := service.UpdateSession(ctx, session)
		if err != nil {
			t.Fatalf("UpdateSession failed: %v", err)
		}

		time.Sleep(10 * time.Millisecond) // Ensure different access times
	}

	// Check cache size
	stats := service.GetCacheStats()
	cacheSize := stats[0].NumSessions

	if cacheSize > 3 {
		t.Errorf("Cache exceeded max size: expected <= 3, got %d", cacheSize)
	}
}

// Test DistributedArtifactService

func TestArtifactService_BasicOperations(t *testing.T) {
	service := NewDistributedArtifactService(DistributedArtifactConfig{
		NumPartitions: 4,
		EnableCache:   true,
	})
	defer service.Close()

	ctx := context.Background()
	sessionID := "session-1"
	artifactName := "test.txt"
	artifactData := []byte("Hello, World!")

	// Test SaveArtifact
	err := service.SaveArtifact(ctx, sessionID, artifactName, artifactData)
	if err != nil {
		t.Fatalf("SaveArtifact failed: %v", err)
	}

	// Test GetArtifact
	retrieved, err := service.GetArtifact(ctx, sessionID, artifactName)
	if err != nil {
		t.Fatalf("GetArtifact failed: %v", err)
	}

	if string(retrieved) != string(artifactData) {
		t.Errorf("Expected %s, got %s", string(artifactData), string(retrieved))
	}

	// Test ListArtifacts
	artifacts, err := service.ListArtifacts(ctx, sessionID)
	if err != nil {
		t.Fatalf("ListArtifacts failed: %v", err)
	}

	if len(artifacts) != 1 {
		t.Errorf("Expected 1 artifact, got %d", len(artifacts))
	}

	if artifacts[0] != artifactName {
		t.Errorf("Expected artifact name %s, got %s", artifactName, artifacts[0])
	}

	// Test DeleteArtifact
	err = service.DeleteArtifact(ctx, sessionID, artifactName)
	if err != nil {
		t.Fatalf("DeleteArtifact failed: %v", err)
	}

	// Verify deletion
	_, err = service.GetArtifact(ctx, sessionID, artifactName)
	if err == nil {
		t.Error("Expected error when getting deleted artifact")
	}
}

func TestArtifactService_SizeBasedCaching(t *testing.T) {
	service := NewDistributedArtifactService(DistributedArtifactConfig{
		NumPartitions:   1,
		EnableCache:     true,
		MaxCacheSize:    1024,        // 1KB total
		MaxArtifactSize: 512,         // Only cache artifacts <= 512 bytes
	})
	defer service.Close()

	ctx := context.Background()
	sessionID := "session-1"

	// Save small artifact (should be cached)
	smallData := make([]byte, 256)
	err := service.SaveArtifact(ctx, sessionID, "small.bin", smallData)
	if err != nil {
		t.Fatalf("SaveArtifact failed: %v", err)
	}

	// Save large artifact (should NOT be cached)
	largeData := make([]byte, 1024)
	err = service.SaveArtifact(ctx, sessionID, "large.bin", largeData)
	if err != nil {
		t.Fatalf("SaveArtifact failed: %v", err)
	}

	// Check cache stats
	stats := service.GetCacheStats()
	partitionStats := stats[0]

	// Only small artifact should be in cache
	if partitionStats.NumArtifacts != 1 {
		t.Errorf("Expected 1 cached artifact, got %d", partitionStats.NumArtifacts)
	}

	if partitionStats.TotalSize != 256 {
		t.Errorf("Expected cache size 256, got %d", partitionStats.TotalSize)
	}
}

func TestArtifactService_CacheUtilization(t *testing.T) {
	service := NewDistributedArtifactService(DistributedArtifactConfig{
		NumPartitions:   1,
		EnableCache:     true,
		MaxCacheSize:    1000,
		MaxArtifactSize: 500,
	})
	defer service.Close()

	ctx := context.Background()
	sessionID := "session-1"

	// Fill cache to 50%
	data := make([]byte, 500)
	err := service.SaveArtifact(ctx, sessionID, "artifact.bin", data)
	if err != nil {
		t.Fatalf("SaveArtifact failed: %v", err)
	}

	// Check utilization
	stats := service.GetCacheStats()
	utilization := stats[0].UtilizationPct

	expectedUtilization := 50.0
	if math.Abs(utilization-expectedUtilization) > 0.1 {
		t.Errorf("Expected utilization ~%.1f%%, got %.1f%%", expectedUtilization, utilization)
	}
}

// Test DistributedMemoryService

func TestMemoryService_BasicOperations(t *testing.T) {
	service := NewDistributedMemoryService(DistributedMemoryConfig{
		NumPartitions: 4,
		EnableCache:   true,
		EmbeddingDim:  3, // Small dimension for testing
	})
	defer service.Close()

	ctx := context.Background()

	// Create test memory with embedding
	memory := &runtime.Memory{
		ID:        "mem-1",
		Content:   "Test memory content",
		Embedding: []float64{1.0, 0.0, 0.0},
		Metadata:  map[string]interface{}{"user_id": "user-1"},
		CreatedAt: time.Now(),
	}

	// Test Store
	err := service.Store(ctx, memory)
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	// Test List
	memories, err := service.List(ctx, "user-1")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(memories) != 1 {
		t.Errorf("Expected 1 memory, got %d", len(memories))
	}

	// Test Delete
	err = service.Delete(ctx, "mem-1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify deletion
	memories, err = service.List(ctx, "user-1")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(memories) != 0 {
		t.Errorf("Expected 0 memories after deletion, got %d", len(memories))
	}
}

func TestMemoryService_VectorSearch(t *testing.T) {
	service := NewDistributedMemoryService(DistributedMemoryConfig{
		NumPartitions: 1,
		EnableCache:   true,
		EmbeddingDim:  3,
	})
	defer service.Close()

	ctx := context.Background()
	userID := "user-1"

	// Store multiple memories with different embeddings
	memories := []*runtime.Memory{
		{
			ID:        "mem-1",
			Content:   "Memory about cats",
			Embedding: []float64{1.0, 0.0, 0.0}, // Close to query
			Metadata:  map[string]interface{}{"user_id": userID},
			CreatedAt: time.Now(),
		},
		{
			ID:        "mem-2",
			Content:   "Memory about dogs",
			Embedding: []float64{0.9, 0.1, 0.0}, // Also close to query
			Metadata:  map[string]interface{}{"user_id": userID},
			CreatedAt: time.Now(),
		},
		{
			ID:        "mem-3",
			Content:   "Memory about birds",
			Embedding: []float64{0.0, 0.0, 1.0}, // Far from query
			Metadata:  map[string]interface{}{"user_id": userID},
			CreatedAt: time.Now(),
		},
	}

	for _, mem := range memories {
		err := service.Store(ctx, mem)
		if err != nil {
			t.Fatalf("Store failed: %v", err)
		}
	}

	// Query for similar memories
	queryEmbedding := []float64{1.0, 0.0, 0.0}
	results, err := service.Query(ctx, userID, queryEmbedding, 2)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("Expected 2 results, got %d", len(results))
	}

	// Results should be ordered by similarity
	if results[0].ID != "mem-1" {
		t.Errorf("Expected mem-1 as top result, got %s", results[0].ID)
	}

	if results[1].ID != "mem-2" {
		t.Errorf("Expected mem-2 as second result, got %s", results[1].ID)
	}
}

func TestMemoryService_CosineSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a        []float64
		b        []float64
		expected float64
	}{
		{
			name:     "identical vectors",
			a:        []float64{1.0, 0.0, 0.0},
			b:        []float64{1.0, 0.0, 0.0},
			expected: 1.0,
		},
		{
			name:     "orthogonal vectors",
			a:        []float64{1.0, 0.0, 0.0},
			b:        []float64{0.0, 1.0, 0.0},
			expected: 0.0,
		},
		{
			name:     "opposite vectors",
			a:        []float64{1.0, 0.0, 0.0},
			b:        []float64{-1.0, 0.0, 0.0},
			expected: -1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Normalize vectors
			normA := normalizeVector(tt.a)
			normB := normalizeVector(tt.b)

			similarity := cosineSimilarity(normA, normB)

			if math.Abs(similarity-tt.expected) > 0.001 {
				t.Errorf("Expected similarity %.3f, got %.3f", tt.expected, similarity)
			}
		})
	}
}

func TestMemoryService_EmbeddingValidation(t *testing.T) {
	service := NewDistributedMemoryService(DistributedMemoryConfig{
		NumPartitions: 1,
		EmbeddingDim:  3,
	})
	defer service.Close()

	ctx := context.Background()

	// Test with wrong dimension
	memory := &runtime.Memory{
		ID:        "mem-1",
		Content:   "Test",
		Embedding: []float64{1.0, 0.0}, // Wrong dimension (2 instead of 3)
		Metadata:  map[string]interface{}{"user_id": "user-1"},
		CreatedAt: time.Now(),
	}

	err := service.Store(ctx, memory)
	if err == nil {
		t.Error("Expected error for wrong embedding dimension")
	}

	// Test with missing embedding
	memory2 := &runtime.Memory{
		ID:        "mem-2",
		Content:   "Test",
		Embedding: nil,
		Metadata:  map[string]interface{}{"user_id": "user-1"},
		CreatedAt: time.Now(),
	}

	err = service.Store(ctx, memory2)
	if err == nil {
		t.Error("Expected error for missing embedding")
	}
}

// Test ServiceBundle

func TestServiceBundle_Creation(t *testing.T) {
	bundle := NewInMemoryServiceBundle(4)
	defer bundle.Close()

	if bundle.SessionService == nil {
		t.Error("Expected SessionService to be initialized")
	}

	if bundle.ArtifactService == nil {
		t.Error("Expected ArtifactService to be initialized")
	}

	if bundle.MemoryService == nil {
		t.Error("Expected MemoryService to be initialized")
	}

	if bundle.numPartitions != 4 {
		t.Errorf("Expected 4 partitions, got %d", bundle.numPartitions)
	}
}

func TestServiceBundle_HealthCheck(t *testing.T) {
	bundle := NewInMemoryServiceBundle(2)
	defer bundle.Close()

	ctx := context.Background()

	err := bundle.HealthCheck(ctx)
	if err != nil {
		t.Errorf("HealthCheck failed: %v", err)
	}
}

func TestServiceBundle_GetStats(t *testing.T) {
	bundle := NewInMemoryServiceBundle(4)
	defer bundle.Close()

	ctx := context.Background()

	// Add some data
	session := &runtime.Session{
		ID:        "session-1",
		UserID:    "user-1",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err := bundle.SessionService.UpdateSession(ctx, session)
	if err != nil {
		t.Fatalf("UpdateSession failed: %v", err)
	}

	// Get stats
	stats := bundle.GetStats()

	if stats.NumPartitions != 4 {
		t.Errorf("Expected 4 partitions, got %d", stats.NumPartitions)
	}

	if len(stats.SessionCacheStats) != 4 {
		t.Errorf("Expected 4 session cache stats, got %d", len(stats.SessionCacheStats))
	}
}

// Test ServiceCoordinator

func TestServiceCoordinator_TaskOperations(t *testing.T) {
	bundle := NewInMemoryServiceBundle(4)
	defer bundle.Close()

	taskCtx := &SparkTaskContext{
		PartitionID:   0,
		TaskAttemptID: "task-0",
		StageID:       1,
		SessionID:     "session-1",
	}

	coordinator := NewServiceCoordinator(bundle, taskCtx)
	ctx := context.Background()

	// Create session
	session := &runtime.Session{
		ID:        "session-1",
		UserID:    "user-1",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err := coordinator.UpdateSessionForTask(ctx, session)
	if err != nil {
		t.Fatalf("UpdateSessionForTask failed: %v", err)
	}

	// Retrieve session
	retrieved, err := coordinator.GetSessionForTask(ctx)
	if err != nil {
		t.Fatalf("GetSessionForTask failed: %v", err)
	}

	if retrieved.ID != session.ID {
		t.Errorf("Expected session ID %s, got %s", session.ID, retrieved.ID)
	}

	// Save artifact
	artifactData := []byte("test data")
	err = coordinator.SaveArtifactForTask(ctx, "test.txt", artifactData)
	if err != nil {
		t.Fatalf("SaveArtifactForTask failed: %v", err)
	}

	// Retrieve artifact
	retrievedData, err := coordinator.GetArtifactForTask(ctx, "test.txt")
	if err != nil {
		t.Fatalf("GetArtifactForTask failed: %v", err)
	}

	if string(retrievedData) != string(artifactData) {
		t.Errorf("Expected %s, got %s", string(artifactData), string(retrievedData))
	}

	// Get partition stats
	stats := coordinator.GetPartitionStats()
	if stats.PartitionID != 0 {
		t.Errorf("Expected partition ID 0, got %d", stats.PartitionID)
	}
}

// Test ServiceMiddleware

func TestServiceMiddleware_WithRetry(t *testing.T) {
	bundle := NewInMemoryServiceBundle(1)
	defer bundle.Close()

	middleware := NewServiceMiddleware(bundle)

	attempts := 0
	operation := func() error {
		attempts++
		if attempts < 3 {
			return fmt.Errorf("temporary error")
		}
		return nil
	}

	err := middleware.WithRetry(operation, 5)
	if err != nil {
		t.Fatalf("WithRetry failed: %v", err)
	}

	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}
}

func TestServiceMiddleware_WithTimeout(t *testing.T) {
	bundle := NewInMemoryServiceBundle(1)
	defer bundle.Close()

	middleware := NewServiceMiddleware(bundle)
	ctx := context.Background()

	// Operation that completes quickly
	fastOp := func(ctx context.Context) error {
		return nil
	}

	err := middleware.WithTimeout(ctx, fastOp, 100*time.Millisecond)
	if err != nil {
		t.Errorf("Fast operation should not timeout: %v", err)
	}

	// Operation that takes too long
	slowOp := func(ctx context.Context) error {
		time.Sleep(200 * time.Millisecond)
		return nil
	}

	err = middleware.WithTimeout(ctx, slowOp, 50*time.Millisecond)
	if err == nil {
		t.Error("Expected timeout error for slow operation")
	}
}

// Test DistributedServiceFactory

func TestServiceFactory_CreateBundles(t *testing.T) {
	factory := NewFactory()

	// Test development bundle
	devBundle := factory.CreateDevelopmentBundle()
	defer devBundle.Close()

	if devBundle.numPartitions != 1 {
		t.Errorf("Development bundle should have 1 partition, got %d", devBundle.numPartitions)
	}

	// Test testing bundle
	testBundle := factory.CreateTestingBundle(8)
	defer testBundle.Close()

	if testBundle.numPartitions != 8 {
		t.Errorf("Testing bundle should have 8 partitions, got %d", testBundle.numPartitions)
	}
}

// Benchmarks

func BenchmarkSessionService_GetSession(b *testing.B) {
	service := NewDistributedSessionService(DistributedSessionConfig{
		NumPartitions: 4,
		EnableCache:   true,
	})
	defer service.Close()

	ctx := context.Background()
	session := &runtime.Session{
		ID:        "bench-session",
		UserID:    "bench-user",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	service.UpdateSession(ctx, session)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = service.GetSession(ctx, "bench-session")
	}
}

func BenchmarkMemoryService_VectorSearch(b *testing.B) {
	service := NewDistributedMemoryService(DistributedMemoryConfig{
		NumPartitions: 1,
		EnableCache:   true,
		EmbeddingDim:  1536, // Realistic dimension
	})
	defer service.Close()

	ctx := context.Background()
	userID := "bench-user"

	// Create realistic embeddings
	for i := 0; i < 100; i++ {
		embedding := make([]float64, 1536)
		for j := range embedding {
			embedding[j] = float64(i+j) / 1000.0
		}

		memory := &runtime.Memory{
			ID:        fmt.Sprintf("mem-%d", i),
			Content:   fmt.Sprintf("Memory %d", i),
			Embedding: embedding,
			Metadata:  map[string]interface{}{"user_id": userID},
			CreatedAt: time.Now(),
		}

		service.Store(ctx, memory)
	}

	queryEmbedding := make([]float64, 1536)
	for i := range queryEmbedding {
		queryEmbedding[i] = 0.5
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = service.Query(ctx, userID, queryEmbedding, 10)
	}
}
