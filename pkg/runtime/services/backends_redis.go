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
	"encoding/json"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/runtime"
	"github.com/redis/go-redis/v9"
)

// RedisSessionBackend provides Redis-backed session storage
type RedisSessionBackend struct {
	client *redis.Client
	ttl    time.Duration
}

// RedisSessionConfig configures Redis session backend
type RedisSessionConfig struct {
	Addr         string        // Redis address (e.g., "localhost:6379")
	Password     string        // Redis password
	DB           int           // Redis database (0-15)
	PoolSize     int           // Max connections (default: 10)
	MinIdleConns int           // Min idle connections (default: 5)
	TTL          time.Duration // Session TTL (default: 24h)
}

// NewRedisSessionBackend creates a new Redis session backend
func NewRedisSessionBackend(config RedisSessionConfig) (*RedisSessionBackend, error) {
	if config.PoolSize == 0 {
		config.PoolSize = 10
	}
	if config.MinIdleConns == 0 {
		config.MinIdleConns = 5
	}
	if config.TTL == 0 {
		config.TTL = 24 * time.Hour
	}

	client := redis.NewClient(&redis.Options{
		Addr:            config.Addr,
		Password:        config.Password,
		DB:              config.DB,
		PoolSize:        config.PoolSize,
		MinIdleConns:    config.MinIdleConns,
		MaxRetries:      3,
		PoolTimeout:     4 * time.Second,
		ConnMaxIdleTime: 5 * time.Minute,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &RedisSessionBackend{
		client: client,
		ttl:    config.TTL,
	}, nil
}

func (b *RedisSessionBackend) Get(ctx context.Context, sessionID string) (*runtime.Session, error) {
	key := fmt.Sprintf("session:%s", sessionID)

	data, err := b.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}

	var session runtime.Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, fmt.Errorf("failed to deserialize session: %w", err)
	}

	return &session, nil
}

func (b *RedisSessionBackend) Put(ctx context.Context, session *runtime.Session) error {
	key := fmt.Sprintf("session:%s", session.ID)
	userKey := fmt.Sprintf("session:user:%s", session.UserID)

	// Update timestamp
	session.UpdatedAt = time.Now()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = time.Now()
	}

	// Serialize session
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("failed to serialize session: %w", err)
	}

	// Use pipeline for atomic operation
	pipe := b.client.Pipeline()

	// Store session with TTL
	pipe.Set(ctx, key, data, b.ttl)

	// Add to user's session set
	pipe.SAdd(ctx, userKey, session.ID)
	pipe.Expire(ctx, userKey, b.ttl)

	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to put session: %w", err)
	}

	return nil
}

func (b *RedisSessionBackend) Delete(ctx context.Context, sessionID string) error {
	key := fmt.Sprintf("session:%s", sessionID)

	// Get session to find userID
	session, err := b.Get(ctx, sessionID)
	if err != nil {
		// Session doesn't exist, consider it deleted
		return nil
	}

	userKey := fmt.Sprintf("session:user:%s", session.UserID)

	// Use pipeline for atomic operation
	pipe := b.client.Pipeline()

	pipe.Del(ctx, key)
	pipe.SRem(ctx, userKey, sessionID)

	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	return nil
}

func (b *RedisSessionBackend) List(ctx context.Context, userID string) ([]*runtime.Session, error) {
	userKey := fmt.Sprintf("session:user:%s", userID)

	// Get all session IDs for user
	sessionIDs, err := b.client.SMembers(ctx, userKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}

	if len(sessionIDs) == 0 {
		return []*runtime.Session{}, nil
	}

	// Build keys for MGET
	keys := make([]string, len(sessionIDs))
	for i, id := range sessionIDs {
		keys[i] = fmt.Sprintf("session:%s", id)
	}

	// Fetch all sessions in one round-trip
	results, err := b.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch sessions: %w", err)
	}

	sessions := make([]*runtime.Session, 0, len(results))
	for _, result := range results {
		if result == nil {
			continue // Session expired or deleted
		}

		data, ok := result.(string)
		if !ok {
			continue
		}

		var session runtime.Session
		if err := json.Unmarshal([]byte(data), &session); err != nil {
			continue // Skip invalid session
		}

		sessions = append(sessions, &session)
	}

	return sessions, nil
}

func (b *RedisSessionBackend) Close() error {
	return b.client.Close()
}

// RedisArtifactBackend provides Redis-backed artifact storage
type RedisArtifactBackend struct {
	client *redis.Client
	ttl    time.Duration
}

// RedisArtifactConfig configures Redis artifact backend
type RedisArtifactConfig struct {
	Addr         string        // Redis address
	Password     string        // Redis password
	DB           int           // Redis database
	PoolSize     int           // Max connections
	MinIdleConns int           // Min idle connections
	TTL          time.Duration // Artifact TTL (default: 7 days)
}

// NewRedisArtifactBackend creates a new Redis artifact backend
func NewRedisArtifactBackend(config RedisArtifactConfig) (*RedisArtifactBackend, error) {
	if config.PoolSize == 0 {
		config.PoolSize = 10
	}
	if config.MinIdleConns == 0 {
		config.MinIdleConns = 5
	}
	if config.TTL == 0 {
		config.TTL = 7 * 24 * time.Hour
	}

	client := redis.NewClient(&redis.Options{
		Addr:            config.Addr,
		Password:        config.Password,
		DB:              config.DB,
		PoolSize:        config.PoolSize,
		MinIdleConns:    config.MinIdleConns,
		MaxRetries:      3,
		PoolTimeout:     4 * time.Second,
		ConnMaxIdleTime: 5 * time.Minute,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &RedisArtifactBackend{
		client: client,
		ttl:    config.TTL,
	}, nil
}

func (b *RedisArtifactBackend) Get(ctx context.Context, key string) ([]byte, error) {
	artifactKey := fmt.Sprintf("artifact:%s", key)

	data, err := b.client.Get(ctx, artifactKey).Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("artifact not found: %s", key)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get artifact: %w", err)
	}

	return data, nil
}

func (b *RedisArtifactBackend) Put(ctx context.Context, key string, data []byte) error {
	artifactKey := fmt.Sprintf("artifact:%s", key)

	// Extract session ID from key (format: session_id/name)
	sessionID := extractSessionID(key)
	sessionKey := fmt.Sprintf("artifact:session:%s", sessionID)

	// Use pipeline for atomic operation
	pipe := b.client.Pipeline()

	// Store artifact with TTL
	pipe.Set(ctx, artifactKey, data, b.ttl)

	// Add to session's artifact set
	artifactName := extractArtifactName(key)
	pipe.SAdd(ctx, sessionKey, artifactName)
	pipe.Expire(ctx, sessionKey, b.ttl)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to put artifact: %w", err)
	}

	return nil
}

func (b *RedisArtifactBackend) Delete(ctx context.Context, key string) error {
	artifactKey := fmt.Sprintf("artifact:%s", key)

	sessionID := extractSessionID(key)
	sessionKey := fmt.Sprintf("artifact:session:%s", sessionID)
	artifactName := extractArtifactName(key)

	// Use pipeline for atomic operation
	pipe := b.client.Pipeline()

	pipe.Del(ctx, artifactKey)
	pipe.SRem(ctx, sessionKey, artifactName)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete artifact: %w", err)
	}

	return nil
}

func (b *RedisArtifactBackend) List(ctx context.Context, prefix string) ([]string, error) {
	sessionID := prefix
	sessionKey := fmt.Sprintf("artifact:session:%s", sessionID)

	// Get all artifact names for session
	names, err := b.client.SMembers(ctx, sessionKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to list artifacts: %w", err)
	}

	return names, nil
}

func (b *RedisArtifactBackend) Close() error {
	return b.client.Close()
}

// RedisMemoryBackend provides Redis-backed memory storage (basic version without vector search)
type RedisMemoryBackend struct {
	client *redis.Client
	ttl    time.Duration
}

// RedisMemoryConfig configures Redis memory backend
type RedisMemoryConfig struct {
	Addr         string        // Redis address
	Password     string        // Redis password
	DB           int           // Redis database
	PoolSize     int           // Max connections
	MinIdleConns int           // Min idle connections
	TTL          time.Duration // Memory TTL (default: 30 days)
}

// NewRedisMemoryBackend creates a new Redis memory backend
func NewRedisMemoryBackend(config RedisMemoryConfig) (*RedisMemoryBackend, error) {
	if config.PoolSize == 0 {
		config.PoolSize = 10
	}
	if config.MinIdleConns == 0 {
		config.MinIdleConns = 5
	}
	if config.TTL == 0 {
		config.TTL = 30 * 24 * time.Hour
	}

	client := redis.NewClient(&redis.Options{
		Addr:            config.Addr,
		Password:        config.Password,
		DB:              config.DB,
		PoolSize:        config.PoolSize,
		MinIdleConns:    config.MinIdleConns,
		MaxRetries:      3,
		PoolTimeout:     4 * time.Second,
		ConnMaxIdleTime: 5 * time.Minute,
	})

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &RedisMemoryBackend{
		client: client,
		ttl:    config.TTL,
	}, nil
}

func (b *RedisMemoryBackend) Put(ctx context.Context, memory *runtime.Memory) error {
	key := fmt.Sprintf("memory:%s", memory.ID)

	// Extract userID from metadata
	userID := ""
	if memory.Metadata != nil {
		if uid, ok := memory.Metadata["user_id"].(string); ok {
			userID = uid
		}
	}

	userKey := fmt.Sprintf("memory:user:%s", userID)

	// Update timestamp
	if memory.CreatedAt.IsZero() {
		memory.CreatedAt = time.Now()
	}

	// Serialize memory
	data, err := json.Marshal(memory)
	if err != nil {
		return fmt.Errorf("failed to serialize memory: %w", err)
	}

	// Use pipeline for atomic operation
	pipe := b.client.Pipeline()

	// Store memory with TTL
	pipe.Set(ctx, key, data, b.ttl)

	// Add to user's memory set
	if userID != "" {
		pipe.SAdd(ctx, userKey, memory.ID)
		pipe.Expire(ctx, userKey, b.ttl)
	}

	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to put memory: %w", err)
	}

	return nil
}

func (b *RedisMemoryBackend) Get(ctx context.Context, memoryID string) (*runtime.Memory, error) {
	key := fmt.Sprintf("memory:%s", memoryID)

	data, err := b.client.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return nil, fmt.Errorf("memory not found: %s", memoryID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get memory: %w", err)
	}

	var memory runtime.Memory
	if err := json.Unmarshal(data, &memory); err != nil {
		return nil, fmt.Errorf("failed to deserialize memory: %w", err)
	}

	return &memory, nil
}

func (b *RedisMemoryBackend) Delete(ctx context.Context, memoryID string) error {
	key := fmt.Sprintf("memory:%s", memoryID)

	// Get memory to find userID
	memory, err := b.Get(ctx, memoryID)
	if err != nil {
		// Memory doesn't exist, consider it deleted
		return nil
	}

	// Extract userID from metadata
	userID := ""
	if memory.Metadata != nil {
		if uid, ok := memory.Metadata["user_id"].(string); ok {
			userID = uid
		}
	}

	// Use pipeline for atomic operation
	pipe := b.client.Pipeline()

	pipe.Del(ctx, key)

	if userID != "" {
		userKey := fmt.Sprintf("memory:user:%s", userID)
		pipe.SRem(ctx, userKey, memoryID)
	}

	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete memory: %w", err)
	}

	return nil
}

func (b *RedisMemoryBackend) List(ctx context.Context, userID string) ([]*runtime.Memory, error) {
	userKey := fmt.Sprintf("memory:user:%s", userID)

	// Get all memory IDs for user
	memoryIDs, err := b.client.SMembers(ctx, userKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to list memories: %w", err)
	}

	if len(memoryIDs) == 0 {
		return []*runtime.Memory{}, nil
	}

	// Build keys for MGET
	keys := make([]string, len(memoryIDs))
	for i, id := range memoryIDs {
		keys[i] = fmt.Sprintf("memory:%s", id)
	}

	// Fetch all memories in one round-trip
	results, err := b.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch memories: %w", err)
	}

	memories := make([]*runtime.Memory, 0, len(results))
	for _, result := range results {
		if result == nil {
			continue // Memory expired or deleted
		}

		data, ok := result.(string)
		if !ok {
			continue
		}

		var memory runtime.Memory
		if err := json.Unmarshal([]byte(data), &memory); err != nil {
			continue // Skip invalid memory
		}

		memories = append(memories, &memory)
	}

	return memories, nil
}

func (b *RedisMemoryBackend) Close() error {
	return b.client.Close()
}

// Helper functions

func extractSessionID(key string) string {
	// Key format: "sessionID/artifactName"
	for i := 0; i < len(key); i++ {
		if key[i] == '/' {
			return key[:i]
		}
	}
	return key
}

func extractArtifactName(key string) string {
	// Key format: "sessionID/artifactName"
	for i := 0; i < len(key); i++ {
		if key[i] == '/' {
			if i+1 < len(key) {
				return key[i+1:]
			}
			return ""
		}
	}
	return key
}
