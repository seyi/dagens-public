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

// Package services provides distributed, Spark-aware implementations of ADK service interfaces
package services

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/runtime"
)

// SessionBackend provides persistent storage for sessions
type SessionBackend interface {
	// Get retrieves a session by ID
	Get(ctx context.Context, sessionID string) (*runtime.Session, error)

	// Put stores or updates a session
	Put(ctx context.Context, session *runtime.Session) error

	// Delete removes a session
	Delete(ctx context.Context, sessionID string) error

	// List returns all sessions for a user
	List(ctx context.Context, userID string) ([]*runtime.Session, error)

	// Close cleans up backend resources
	Close() error
}

// ArtifactBackend provides persistent storage for artifacts
type ArtifactBackend interface {
	// Put stores an artifact
	Put(ctx context.Context, key string, data []byte) error

	// Get retrieves an artifact
	Get(ctx context.Context, key string) ([]byte, error)

	// Delete removes an artifact
	Delete(ctx context.Context, key string) error

	// List returns all artifact keys with given prefix
	List(ctx context.Context, prefix string) ([]string, error)

	// Close cleans up backend resources
	Close() error
}

// MemoryBackend provides persistent storage for memories
type MemoryBackend interface {
	// Put stores a memory
	Put(ctx context.Context, memory *runtime.Memory) error

	// Get retrieves a memory by ID
	Get(ctx context.Context, memoryID string) (*runtime.Memory, error)

	// Delete removes a memory
	Delete(ctx context.Context, memoryID string) error

	// List returns all memories for a user
	List(ctx context.Context, userID string) ([]*runtime.Memory, error)

	// Close cleans up backend resources
	Close() error
}

// InMemorySessionBackend provides an in-memory session backend (for testing/development)
type InMemorySessionBackend struct {
	sessions map[string]*runtime.Session
	mu       sync.RWMutex
}

// NewInMemorySessionBackend creates a new in-memory session backend
func NewInMemorySessionBackend() *InMemorySessionBackend {
	return &InMemorySessionBackend{
		sessions: make(map[string]*runtime.Session),
	}
}

func (b *InMemorySessionBackend) Get(ctx context.Context, sessionID string) (*runtime.Session, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	session, ok := b.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	// Return a copy to prevent external mutation
	sessionCopy := *session
	return &sessionCopy, nil
}

func (b *InMemorySessionBackend) Put(ctx context.Context, session *runtime.Session) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Store a copy
	sessionCopy := *session
	sessionCopy.UpdatedAt = time.Now()

	if sessionCopy.CreatedAt.IsZero() {
		sessionCopy.CreatedAt = time.Now()
	}

	b.sessions[session.ID] = &sessionCopy
	return nil
}

func (b *InMemorySessionBackend) Delete(ctx context.Context, sessionID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.sessions, sessionID)
	return nil
}

func (b *InMemorySessionBackend) List(ctx context.Context, userID string) ([]*runtime.Session, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var result []*runtime.Session
	for _, session := range b.sessions {
		if session.UserID == userID {
			sessionCopy := *session
			result = append(result, &sessionCopy)
		}
	}

	return result, nil
}

func (b *InMemorySessionBackend) Close() error {
	return nil
}

// InMemoryArtifactBackend provides an in-memory artifact backend
type InMemoryArtifactBackend struct {
	artifacts map[string][]byte
	mu        sync.RWMutex
}

// NewInMemoryArtifactBackend creates a new in-memory artifact backend
func NewInMemoryArtifactBackend() *InMemoryArtifactBackend {
	return &InMemoryArtifactBackend{
		artifacts: make(map[string][]byte),
	}
}

func (b *InMemoryArtifactBackend) Put(ctx context.Context, key string, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Store a copy
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)
	b.artifacts[key] = dataCopy

	return nil
}

func (b *InMemoryArtifactBackend) Get(ctx context.Context, key string) ([]byte, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	data, ok := b.artifacts[key]
	if !ok {
		return nil, fmt.Errorf("artifact not found: %s", key)
	}

	// Return a copy
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)
	return dataCopy, nil
}

func (b *InMemoryArtifactBackend) Delete(ctx context.Context, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.artifacts, key)
	return nil
}

func (b *InMemoryArtifactBackend) List(ctx context.Context, prefix string) ([]string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var result []string
	for key := range b.artifacts {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			result = append(result, key)
		}
	}

	return result, nil
}

func (b *InMemoryArtifactBackend) Close() error {
	return nil
}

// InMemoryMemoryBackend provides an in-memory memory backend
type InMemoryMemoryBackend struct {
	memories map[string]*runtime.Memory
	mu       sync.RWMutex
}

// NewInMemoryMemoryBackend creates a new in-memory memory backend
func NewInMemoryMemoryBackend() *InMemoryMemoryBackend {
	return &InMemoryMemoryBackend{
		memories: make(map[string]*runtime.Memory),
	}
}

func (b *InMemoryMemoryBackend) Put(ctx context.Context, memory *runtime.Memory) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Store a copy
	memoryCopy := *memory
	if memoryCopy.CreatedAt.IsZero() {
		memoryCopy.CreatedAt = time.Now()
	}

	b.memories[memory.ID] = &memoryCopy
	return nil
}

func (b *InMemoryMemoryBackend) Get(ctx context.Context, memoryID string) (*runtime.Memory, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	memory, ok := b.memories[memoryID]
	if !ok {
		return nil, fmt.Errorf("memory not found: %s", memoryID)
	}

	// Return a copy
	memoryCopy := *memory
	return &memoryCopy, nil
}

func (b *InMemoryMemoryBackend) Delete(ctx context.Context, memoryID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	delete(b.memories, memoryID)
	return nil
}

func (b *InMemoryMemoryBackend) List(ctx context.Context, userID string) ([]*runtime.Memory, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var result []*runtime.Memory
	for _, memory := range b.memories {
		// Check userID in metadata
		if memory.Metadata != nil {
			if uid, ok := memory.Metadata["user_id"].(string); ok && uid == userID {
				memoryCopy := *memory
				result = append(result, &memoryCopy)
			}
		}
	}

	return result, nil
}

func (b *InMemoryMemoryBackend) Close() error {
	return nil
}
