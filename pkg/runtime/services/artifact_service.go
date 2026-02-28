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
	"sync"
	"time"
)

// DistributedArtifactService provides distributed, Spark-aware artifact storage
// Implements runtime.ArtifactService interface with partition-aware caching
type DistributedArtifactService struct {
	// Backend storage
	backend ArtifactBackend

	// Partition-local caches (one per partition)
	caches map[int]*ArtifactCache
	mu     sync.RWMutex

	// Partitioning strategy (partition by sessionID)
	partitioner   PartitionStrategy
	numPartitions int

	// Configuration
	enableCache    bool
	cacheTTL       time.Duration
	maxCacheSize   int64 // Bytes per partition
	maxArtifactSize int64 // Max artifact size to cache
}

// ArtifactCache provides partition-local caching with LRU eviction
type ArtifactCache struct {
	artifacts    map[string]*CachedArtifact
	totalSize    int64
	maxSize      int64
	mu           sync.RWMutex
}

// CachedArtifact wraps artifact data with cache metadata
type CachedArtifact struct {
	Data       []byte
	Size       int64
	CachedAt   time.Time
	AccessedAt time.Time
}

// DistributedArtifactConfig configures the distributed artifact service
type DistributedArtifactConfig struct {
	Backend         ArtifactBackend
	NumPartitions   int
	EnableCache     bool
	CacheTTL        time.Duration
	MaxCacheSize    int64 // Per partition in bytes
	MaxArtifactSize int64 // Max artifact size to cache
}

// NewDistributedArtifactService creates a new distributed artifact service
func NewDistributedArtifactService(config DistributedArtifactConfig) *DistributedArtifactService {
	if config.Backend == nil {
		config.Backend = NewInMemoryArtifactBackend()
	}

	if config.NumPartitions <= 0 {
		config.NumPartitions = 1
	}

	if config.CacheTTL == 0 {
		config.CacheTTL = 10 * time.Minute
	}

	if config.MaxCacheSize == 0 {
		config.MaxCacheSize = 100 * 1024 * 1024 // 100MB per partition
	}

	if config.MaxArtifactSize == 0 {
		config.MaxArtifactSize = 10 * 1024 * 1024 // 10MB
	}

	service := &DistributedArtifactService{
		backend:         config.Backend,
		caches:          make(map[int]*ArtifactCache),
		partitioner:     NewHashPartitionStrategy(),
		numPartitions:   config.NumPartitions,
		enableCache:     config.EnableCache,
		cacheTTL:        config.CacheTTL,
		maxCacheSize:    config.MaxCacheSize,
		maxArtifactSize: config.MaxArtifactSize,
	}

	// Initialize partition caches
	for i := 0; i < config.NumPartitions; i++ {
		service.caches[i] = &ArtifactCache{
			artifacts: make(map[string]*CachedArtifact),
			maxSize:   config.MaxCacheSize,
		}
	}

	return service
}

// SaveArtifact stores an artifact (ADK-compatible interface)
func (s *DistributedArtifactService) SaveArtifact(ctx context.Context, sessionID, name string, data []byte) error {
	key := s.makeKey(sessionID, name)

	// Determine partition
	partitionID := s.partitioner.GetPartition(sessionID, s.numPartitions)

	// Save to backend
	if err := s.backend.Put(ctx, key, data); err != nil {
		return err
	}

	// Update cache (if artifact is small enough)
	if s.enableCache && int64(len(data)) <= s.maxArtifactSize {
		s.putInCache(partitionID, key, data)
	}

	return nil
}

// GetArtifact retrieves an artifact (ADK-compatible interface)
func (s *DistributedArtifactService) GetArtifact(ctx context.Context, sessionID, name string) ([]byte, error) {
	key := s.makeKey(sessionID, name)

	// Determine partition
	partitionID := s.partitioner.GetPartition(sessionID, s.numPartitions)

	// Try cache first (if enabled)
	if s.enableCache {
		if cached := s.getFromCache(partitionID, key); cached != nil {
			return cached, nil
		}
	}

	// Load from backend
	data, err := s.backend.Get(ctx, key)
	if err != nil {
		return nil, err
	}

	// Update cache (if artifact is small enough)
	if s.enableCache && int64(len(data)) <= s.maxArtifactSize {
		s.putInCache(partitionID, key, data)
	}

	return data, nil
}

// ListArtifacts returns all artifact names for a session (ADK-compatible interface)
func (s *DistributedArtifactService) ListArtifacts(ctx context.Context, sessionID string) ([]string, error) {
	prefix := s.makeKey(sessionID, "")

	keys, err := s.backend.List(ctx, prefix)
	if err != nil {
		return nil, err
	}

	// Strip prefix from keys to get artifact names
	names := make([]string, 0, len(keys))
	prefixLen := len(prefix)

	for _, key := range keys {
		if len(key) > prefixLen {
			name := key[prefixLen:]
			names = append(names, name)
		}
	}

	return names, nil
}

// DeleteArtifact removes an artifact (extension of ADK interface)
func (s *DistributedArtifactService) DeleteArtifact(ctx context.Context, sessionID, name string) error {
	key := s.makeKey(sessionID, name)

	// Determine partition
	partitionID := s.partitioner.GetPartition(sessionID, s.numPartitions)

	// Delete from backend
	if err := s.backend.Delete(ctx, key); err != nil {
		return err
	}

	// Remove from cache
	if s.enableCache {
		s.removeFromCache(partitionID, key)
	}

	return nil
}

// makeKey creates a storage key from sessionID and artifact name
func (s *DistributedArtifactService) makeKey(sessionID, name string) string {
	if name == "" {
		return fmt.Sprintf("artifacts/%s/", sessionID)
	}
	return fmt.Sprintf("artifacts/%s/%s", sessionID, name)
}

// getFromCache retrieves an artifact from the partition cache
func (s *DistributedArtifactService) getFromCache(partitionID int, key string) []byte {
	s.mu.RLock()
	cache, ok := s.caches[partitionID]
	s.mu.RUnlock()

	if !ok {
		return nil
	}

	cache.mu.RLock()
	defer cache.mu.RUnlock()

	cached, ok := cache.artifacts[key]
	if !ok {
		return nil
	}

	// Check if expired
	if time.Since(cached.CachedAt) > s.cacheTTL {
		return nil
	}

	// Update access time (for LRU)
	cached.AccessedAt = time.Now()

	// Return copy
	dataCopy := make([]byte, len(cached.Data))
	copy(dataCopy, cached.Data)
	return dataCopy
}

// putInCache stores an artifact in the partition cache
func (s *DistributedArtifactService) putInCache(partitionID int, key string, data []byte) {
	s.mu.RLock()
	cache, ok := s.caches[partitionID]
	s.mu.RUnlock()

	if !ok {
		return
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()

	dataSize := int64(len(data))

	// Evict until there's space
	for cache.totalSize+dataSize > cache.maxSize && len(cache.artifacts) > 0 {
		s.evictOldestFromCache(cache)
	}

	// Don't cache if still too large
	if cache.totalSize+dataSize > cache.maxSize {
		return
	}

	// Store copy
	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	cache.artifacts[key] = &CachedArtifact{
		Data:       dataCopy,
		Size:       dataSize,
		CachedAt:   time.Now(),
		AccessedAt: time.Now(),
	}

	cache.totalSize += dataSize
}

// removeFromCache removes an artifact from cache
func (s *DistributedArtifactService) removeFromCache(partitionID int, key string) {
	s.mu.RLock()
	cache, ok := s.caches[partitionID]
	s.mu.RUnlock()

	if !ok {
		return
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()

	if cached, ok := cache.artifacts[key]; ok {
		cache.totalSize -= cached.Size
		delete(cache.artifacts, key)
	}
}

// evictOldestFromCache removes the least recently accessed artifact
func (s *DistributedArtifactService) evictOldestFromCache(cache *ArtifactCache) {
	var oldestKey string
	var oldestTime time.Time

	for key, cached := range cache.artifacts {
		if oldestKey == "" || cached.AccessedAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = cached.AccessedAt
		}
	}

	if oldestKey != "" {
		cached := cache.artifacts[oldestKey]
		cache.totalSize -= cached.Size
		delete(cache.artifacts, oldestKey)
	}
}

// GetCacheStats returns cache statistics for monitoring
func (s *DistributedArtifactService) GetCacheStats() map[int]ArtifactCacheStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := make(map[int]ArtifactCacheStats)

	for partitionID, cache := range s.caches {
		cache.mu.RLock()
		stats[partitionID] = ArtifactCacheStats{
			PartitionID:    partitionID,
			NumArtifacts:   len(cache.artifacts),
			TotalSize:      cache.totalSize,
			MaxSize:        cache.maxSize,
			UtilizationPct: float64(cache.totalSize) / float64(cache.maxSize) * 100,
		}
		cache.mu.RUnlock()
	}

	return stats
}

// ArtifactCacheStats provides cache statistics
type ArtifactCacheStats struct {
	PartitionID    int
	NumArtifacts   int
	TotalSize      int64
	MaxSize        int64
	UtilizationPct float64
}

// Close cleans up resources
func (s *DistributedArtifactService) Close() error {
	return s.backend.Close()
}
