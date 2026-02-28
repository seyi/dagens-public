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
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/runtime"
)

// DistributedSessionService provides distributed, Spark-aware session management
// Implements runtime.SessionService interface with partition-aware caching
type DistributedSessionService struct {
	// Backend storage
	backend SessionBackend

	// Partition-local caches (one per partition)
	caches map[int]*SessionCache
	mu     sync.RWMutex

	// Partitioning strategy
	partitioner   PartitionStrategy
	numPartitions int

	// Configuration
	enableCache bool
	cacheTTL    time.Duration
	maxCacheSize int
}

// SessionCache provides partition-local caching
type SessionCache struct {
	sessions map[string]*CachedSession
	mu       sync.RWMutex
	maxSize  int
}

// CachedSession wraps a session with cache metadata
type CachedSession struct {
	Session   *runtime.Session
	CachedAt  time.Time
	AccessedAt time.Time
}

// DistributedSessionConfig configures the distributed session service
type DistributedSessionConfig struct {
	Backend      SessionBackend
	NumPartitions int
	EnableCache  bool
	CacheTTL     time.Duration
	MaxCacheSize int  // Per partition
}

// NewDistributedSessionService creates a new distributed session service
func NewDistributedSessionService(config DistributedSessionConfig) *DistributedSessionService {
	if config.Backend == nil {
		config.Backend = NewInMemorySessionBackend()
	}

	if config.NumPartitions <= 0 {
		config.NumPartitions = 1
	}

	if config.CacheTTL == 0 {
		config.CacheTTL = 5 * time.Minute
	}

	if config.MaxCacheSize == 0 {
		config.MaxCacheSize = 100
	}

	service := &DistributedSessionService{
		backend:       config.Backend,
		caches:        make(map[int]*SessionCache),
		partitioner:   NewHashPartitionStrategy(),
		numPartitions: config.NumPartitions,
		enableCache:   config.EnableCache,
		cacheTTL:      config.CacheTTL,
		maxCacheSize:  config.MaxCacheSize,
	}

	// Initialize partition caches
	for i := 0; i < config.NumPartitions; i++ {
		service.caches[i] = &SessionCache{
			sessions: make(map[string]*CachedSession),
			maxSize:  config.MaxCacheSize,
		}
	}

	return service
}

// GetSession retrieves a session by ID (ADK-compatible interface)
func (s *DistributedSessionService) GetSession(ctx context.Context, sessionID string) (*runtime.Session, error) {
	// Determine partition
	partitionID := s.partitioner.GetPartition(sessionID, s.numPartitions)

	// Try cache first (if enabled)
	if s.enableCache {
		if cached := s.getFromCache(partitionID, sessionID); cached != nil {
			return cached, nil
		}
	}

	// Load from backend
	session, err := s.backend.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	// Update cache
	if s.enableCache {
		s.putInCache(partitionID, session)
	}

	return session, nil
}

// UpdateSession stores or updates a session (ADK-compatible interface)
func (s *DistributedSessionService) UpdateSession(ctx context.Context, session *runtime.Session) error {
	// Determine partition
	partitionID := s.partitioner.GetPartition(session.ID, s.numPartitions)

	// Update backend
	if err := s.backend.Put(ctx, session); err != nil {
		return err
	}

	// Update cache
	if s.enableCache {
		s.putInCache(partitionID, session)
	}

	return nil
}

// ListSessions returns all sessions for a user (ADK-compatible interface)
// Note: This requires cross-partition aggregation and is more expensive
func (s *DistributedSessionService) ListSessions(ctx context.Context, userID string) ([]*runtime.Session, error) {
	// List from backend (aggregates across all partitions)
	return s.backend.List(ctx, userID)
}

// getFromCache retrieves a session from the partition cache
func (s *DistributedSessionService) getFromCache(partitionID int, sessionID string) *runtime.Session {
	s.mu.RLock()
	cache, ok := s.caches[partitionID]
	s.mu.RUnlock()

	if !ok {
		return nil
	}

	cache.mu.RLock()
	defer cache.mu.RUnlock()

	cached, ok := cache.sessions[sessionID]
	if !ok {
		return nil
	}

	// Check if expired
	if time.Since(cached.CachedAt) > s.cacheTTL {
		return nil
	}

	// Update access time (for LRU)
	cached.AccessedAt = time.Now()

	return cached.Session
}

// putInCache stores a session in the partition cache
func (s *DistributedSessionService) putInCache(partitionID int, session *runtime.Session) {
	s.mu.RLock()
	cache, ok := s.caches[partitionID]
	s.mu.RUnlock()

	if !ok {
		return
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()

	// Evict if cache is full (simple LRU)
	if len(cache.sessions) >= cache.maxSize {
		s.evictOldest(cache)
	}

	cache.sessions[session.ID] = &CachedSession{
		Session:    session,
		CachedAt:   time.Now(),
		AccessedAt: time.Now(),
	}
}

// evictOldest removes the least recently accessed session from cache
func (s *DistributedSessionService) evictOldest(cache *SessionCache) {
	var oldestID string
	var oldestTime time.Time

	for id, cached := range cache.sessions {
		if oldestID == "" || cached.AccessedAt.Before(oldestTime) {
			oldestID = id
			oldestTime = cached.AccessedAt
		}
	}

	if oldestID != "" {
		delete(cache.sessions, oldestID)
	}
}

// InvalidateCache invalidates cache entries for a session across all partitions
func (s *DistributedSessionService) InvalidateCache(sessionID string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, cache := range s.caches {
		cache.mu.Lock()
		delete(cache.sessions, sessionID)
		cache.mu.Unlock()
	}
}

// GetCacheStats returns cache statistics for monitoring
func (s *DistributedSessionService) GetCacheStats() map[int]SessionCacheStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := make(map[int]SessionCacheStats)

	for partitionID, cache := range s.caches {
		cache.mu.RLock()
		stats[partitionID] = SessionCacheStats{
			PartitionID: partitionID,
			NumSessions: len(cache.sessions),
			MaxSize:     cache.maxSize,
		}
		cache.mu.RUnlock()
	}

	return stats
}

// SessionCacheStats provides cache statistics
type SessionCacheStats struct {
	PartitionID int
	NumSessions int
	MaxSize     int
}

// Close cleans up resources
func (s *DistributedSessionService) Close() error {
	return s.backend.Close()
}
