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

//go:build integration
// +build integration

package services

import (
	"context"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/runtime"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// setupRedisContainer starts a Redis container for testing
func setupRedisContainer(t *testing.T) (string, func()) {
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections"),
	}

	redisC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("Failed to start Redis container: %v", err)
	}

	endpoint, err := redisC.Endpoint(ctx, "")
	if err != nil {
		t.Fatalf("Failed to get Redis endpoint: %v", err)
	}

	cleanup := func() {
		if err := redisC.Terminate(ctx); err != nil {
			t.Logf("Failed to terminate Redis container: %v", err)
		}
	}

	return endpoint, cleanup
}

// Test RedisSessionBackend

func TestRedisSessionBackend_BasicOperations(t *testing.T) {
	addr, cleanup := setupRedisContainer(t)
	defer cleanup()

	backend, err := NewRedisSessionBackend(RedisSessionConfig{
		Addr: addr,
		TTL:  1 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Failed to create Redis session backend: %v", err)
	}
	defer backend.Close()

	ctx := context.Background()

	// Create test session
	session := &runtime.Session{
		ID:        "test-session-1",
		UserID:    "test-user-1",
		State:     map[string]interface{}{"key": "value"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Test Put
	err = backend.Put(ctx, session)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Test Get
	retrieved, err := backend.Get(ctx, "test-session-1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if retrieved.ID != session.ID {
		t.Errorf("Expected ID %s, got %s", session.ID, retrieved.ID)
	}
	if retrieved.UserID != session.UserID {
		t.Errorf("Expected UserID %s, got %s", session.UserID, retrieved.UserID)
	}

	// Test List
	sessions, err := backend.List(ctx, "test-user-1")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(sessions) != 1 {
		t.Errorf("Expected 1 session, got %d", len(sessions))
	}

	// Test Delete
	err = backend.Delete(ctx, "test-session-1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify deletion
	_, err = backend.Get(ctx, "test-session-1")
	if err == nil {
		t.Error("Expected error when getting deleted session")
	}
}

func TestRedisSessionBackend_MultipleUsers(t *testing.T) {
	addr, cleanup := setupRedisContainer(t)
	defer cleanup()

	backend, err := NewRedisSessionBackend(RedisSessionConfig{
		Addr: addr,
		TTL:  1 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Failed to create Redis session backend: %v", err)
	}
	defer backend.Close()

	ctx := context.Background()

	// Create sessions for multiple users
	users := []string{"user-1", "user-2", "user-3"}
	for _, userID := range users {
		for i := 0; i < 3; i++ {
			session := &runtime.Session{
				ID:        userID + "-session-" + string(rune('a'+i)),
				UserID:    userID,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			}

			err := backend.Put(ctx, session)
			if err != nil {
				t.Fatalf("Put failed for user %s: %v", userID, err)
			}
		}
	}

	// Verify each user has 3 sessions
	for _, userID := range users {
		sessions, err := backend.List(ctx, userID)
		if err != nil {
			t.Fatalf("List failed for user %s: %v", userID, err)
		}

		if len(sessions) != 3 {
			t.Errorf("Expected 3 sessions for user %s, got %d", userID, len(sessions))
		}
	}
}

// Test RedisArtifactBackend

func TestRedisArtifactBackend_BasicOperations(t *testing.T) {
	addr, cleanup := setupRedisContainer(t)
	defer cleanup()

	backend, err := NewRedisArtifactBackend(RedisArtifactConfig{
		Addr: addr,
		TTL:  1 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Failed to create Redis artifact backend: %v", err)
	}
	defer backend.Close()

	ctx := context.Background()

	// Test Put
	key := "session-1/artifact.txt"
	data := []byte("test artifact data")

	err = backend.Put(ctx, key, data)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Test Get
	retrieved, err := backend.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if string(retrieved) != string(data) {
		t.Errorf("Expected %s, got %s", string(data), string(retrieved))
	}

	// Test List
	artifacts, err := backend.List(ctx, "session-1")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(artifacts) != 1 {
		t.Errorf("Expected 1 artifact, got %d", len(artifacts))
	}

	if artifacts[0] != "artifact.txt" {
		t.Errorf("Expected artifact.txt, got %s", artifacts[0])
	}

	// Test Delete
	err = backend.Delete(ctx, key)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify deletion
	_, err = backend.Get(ctx, key)
	if err == nil {
		t.Error("Expected error when getting deleted artifact")
	}
}

func TestRedisArtifactBackend_LargeData(t *testing.T) {
	addr, cleanup := setupRedisContainer(t)
	defer cleanup()

	backend, err := NewRedisArtifactBackend(RedisArtifactConfig{
		Addr: addr,
		TTL:  1 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Failed to create Redis artifact backend: %v", err)
	}
	defer backend.Close()

	ctx := context.Background()

	// Create 1MB artifact
	key := "session-1/large.bin"
	data := make([]byte, 1024*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	err = backend.Put(ctx, key, data)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Retrieve and verify
	retrieved, err := backend.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if len(retrieved) != len(data) {
		t.Errorf("Expected %d bytes, got %d", len(data), len(retrieved))
	}
}

// Test RedisMemoryBackend

func TestRedisMemoryBackend_BasicOperations(t *testing.T) {
	addr, cleanup := setupRedisContainer(t)
	defer cleanup()

	backend, err := NewRedisMemoryBackend(RedisMemoryConfig{
		Addr: addr,
		TTL:  1 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Failed to create Redis memory backend: %v", err)
	}
	defer backend.Close()

	ctx := context.Background()

	// Create test memory
	memory := &runtime.Memory{
		ID:        "mem-1",
		Content:   "Test memory",
		Embedding: []float64{0.1, 0.2, 0.3},
		Metadata:  map[string]interface{}{"user_id": "user-1"},
		CreatedAt: time.Now(),
	}

	// Test Put
	err = backend.Put(ctx, memory)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Test Get
	retrieved, err := backend.Get(ctx, "mem-1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if retrieved.ID != memory.ID {
		t.Errorf("Expected ID %s, got %s", memory.ID, retrieved.ID)
	}
	if retrieved.Content != memory.Content {
		t.Errorf("Expected content %s, got %s", memory.Content, retrieved.Content)
	}

	// Test List
	memories, err := backend.List(ctx, "user-1")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(memories) != 1 {
		t.Errorf("Expected 1 memory, got %d", len(memories))
	}

	// Test Delete
	err = backend.Delete(ctx, "mem-1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify deletion
	_, err = backend.Get(ctx, "mem-1")
	if err == nil {
		t.Error("Expected error when getting deleted memory")
	}
}

func TestRedisMemoryBackend_MultipleMemories(t *testing.T) {
	addr, cleanup := setupRedisContainer(t)
	defer cleanup()

	backend, err := NewRedisMemoryBackend(RedisMemoryConfig{
		Addr: addr,
		TTL:  1 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Failed to create Redis memory backend: %v", err)
	}
	defer backend.Close()

	ctx := context.Background()
	userID := "test-user"

	// Store multiple memories
	for i := 0; i < 5; i++ {
		memory := &runtime.Memory{
			ID:        "mem-" + string(rune('a'+i)),
			Content:   "Memory " + string(rune('A'+i)),
			Embedding: []float64{float64(i), float64(i + 1), float64(i + 2)},
			Metadata:  map[string]interface{}{"user_id": userID},
			CreatedAt: time.Now(),
		}

		err := backend.Put(ctx, memory)
		if err != nil {
			t.Fatalf("Put failed for memory %d: %v", i, err)
		}
	}

	// List all memories
	memories, err := backend.List(ctx, userID)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(memories) != 5 {
		t.Errorf("Expected 5 memories, got %d", len(memories))
	}
}

// Benchmark Redis operations

func BenchmarkRedisSession_Put(b *testing.B) {
	addr, cleanup := setupRedisContainer(&testing.T{})
	defer cleanup()

	backend, err := NewRedisSessionBackend(RedisSessionConfig{
		Addr: addr,
		TTL:  1 * time.Hour,
	})
	if err != nil {
		b.Fatalf("Failed to create backend: %v", err)
	}
	defer backend.Close()

	ctx := context.Background()
	session := &runtime.Session{
		ID:        "bench-session",
		UserID:    "bench-user",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = backend.Put(ctx, session)
	}
}

func BenchmarkRedisSession_Get(b *testing.B) {
	addr, cleanup := setupRedisContainer(&testing.T{})
	defer cleanup()

	backend, err := NewRedisSessionBackend(RedisSessionConfig{
		Addr: addr,
		TTL:  1 * time.Hour,
	})
	if err != nil {
		b.Fatalf("Failed to create backend: %v", err)
	}
	defer backend.Close()

	ctx := context.Background()
	session := &runtime.Session{
		ID:        "bench-session",
		UserID:    "bench-user",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	backend.Put(ctx, session)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = backend.Get(ctx, "bench-session")
	}
}
