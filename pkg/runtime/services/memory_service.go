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
	"sort"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/runtime"
)

// DistributedMemoryService provides distributed, Spark-aware memory storage with vector search
// Implements runtime.MemoryService interface with partition-aware vector indices
type DistributedMemoryService struct {
	// Backend storage
	backend MemoryBackend

	// Partition-local vector indices (one per partition)
	indices map[int]*VectorIndex
	mu      sync.RWMutex

	// Partitioning strategy (partition by userID)
	partitioner   PartitionStrategy
	numPartitions int

	// Configuration
	enableCache       bool
	cacheTTL          time.Duration
	embeddingDim      int                // Expected embedding dimension
	embeddingProvider EmbeddingProvider  // Optional: generates embeddings from text
}

// VectorIndex provides partition-local vector search
type VectorIndex struct {
	memories   map[string]*IndexedMemory
	embeddings [][]float64 // Parallel array for fast vector search
	memoryIDs  []string    // Maps embedding index to memory ID
	mu         sync.RWMutex
}

// IndexedMemory wraps memory with vector search metadata
type IndexedMemory struct {
	Memory     *runtime.Memory
	Embedding  []float64
	CachedAt   time.Time
	AccessedAt time.Time
}

// EmbeddingProvider generates embeddings from text (optional)
type EmbeddingProvider interface {
	GenerateEmbedding(ctx context.Context, text string) ([]float64, error)
}

// DistributedMemoryConfig configures the distributed memory service
type DistributedMemoryConfig struct {
	Backend           MemoryBackend
	NumPartitions     int
	EnableCache       bool
	CacheTTL          time.Duration
	EmbeddingDim      int               // Expected embedding dimension (e.g., 1536 for OpenAI)
	EmbeddingProvider EmbeddingProvider // Optional: auto-generate embeddings
}

// NewDistributedMemoryService creates a new distributed memory service
func NewDistributedMemoryService(config DistributedMemoryConfig) *DistributedMemoryService {
	if config.Backend == nil {
		config.Backend = NewInMemoryMemoryBackend()
	}

	if config.NumPartitions <= 0 {
		config.NumPartitions = 1
	}

	if config.CacheTTL == 0 {
		config.CacheTTL = 30 * time.Minute
	}

	if config.EmbeddingDim == 0 {
		config.EmbeddingDim = 1536 // Default: OpenAI ada-002 dimension
	}

	service := &DistributedMemoryService{
		backend:           config.Backend,
		indices:           make(map[int]*VectorIndex),
		partitioner:       NewHashPartitionStrategy(),
		numPartitions:     config.NumPartitions,
		enableCache:       config.EnableCache,
		cacheTTL:          config.CacheTTL,
		embeddingDim:      config.EmbeddingDim,
		embeddingProvider: config.EmbeddingProvider,
	}

	// Initialize partition indices
	for i := 0; i < config.NumPartitions; i++ {
		service.indices[i] = &VectorIndex{
			memories:   make(map[string]*IndexedMemory),
			embeddings: make([][]float64, 0),
			memoryIDs:  make([]string, 0),
		}
	}

	return service
}

// Store saves a memory (ADK-compatible interface)
func (s *DistributedMemoryService) Store(ctx context.Context, memory *runtime.Memory) error {
	// Validate embedding
	if memory.Embedding == nil || len(memory.Embedding) == 0 {
		// Auto-generate embedding if provider available
		if s.embeddingProvider != nil && memory.Content != "" {
			embedding, err := s.embeddingProvider.GenerateEmbedding(ctx, memory.Content)
			if err != nil {
				return fmt.Errorf("failed to generate embedding: %w", err)
			}
			memory.Embedding = embedding
		} else {
			return fmt.Errorf("memory must have embedding or content for auto-generation")
		}
	}

	// Validate embedding dimension
	if len(memory.Embedding) != s.embeddingDim {
		return fmt.Errorf("embedding dimension mismatch: expected %d, got %d", s.embeddingDim, len(memory.Embedding))
	}

	// Normalize embedding for cosine similarity
	normalizedEmbedding := normalizeVector(memory.Embedding)

	// Extract userID from metadata
	userID := ""
	if memory.Metadata != nil {
		if uid, ok := memory.Metadata["user_id"].(string); ok {
			userID = uid
		}
	}

	// Determine partition (by userID for affinity)
	partitionID := 0
	if userID != "" {
		partitionID = s.partitioner.GetPartition(userID, s.numPartitions)
	}

	// Save to backend
	if err := s.backend.Put(ctx, memory); err != nil {
		return err
	}

	// Update index (if enabled)
	if s.enableCache {
		s.addToIndex(partitionID, memory, normalizedEmbedding)
	}

	return nil
}

// Query performs semantic search across memories (ADK-compatible interface)
func (s *DistributedMemoryService) Query(ctx context.Context, userID string, queryEmbedding []float64, topK int) ([]*runtime.Memory, error) {
	// Validate query embedding
	if len(queryEmbedding) != s.embeddingDim {
		return nil, fmt.Errorf("query embedding dimension mismatch: expected %d, got %d", s.embeddingDim, len(queryEmbedding))
	}

	// Normalize query embedding
	normalizedQuery := normalizeVector(queryEmbedding)

	// Determine user's primary partition
	primaryPartition := s.partitioner.GetPartition(userID, s.numPartitions)

	// Try cache first (partition-local search)
	if s.enableCache {
		if results := s.searchIndex(primaryPartition, userID, normalizedQuery, topK); len(results) > 0 {
			return results, nil
		}
	}

	// Fallback: Load from backend and search
	// In production, this would be a distributed query across all partitions
	memories, err := s.backend.List(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Score all memories
	type scoredMemory struct {
		memory *runtime.Memory
		score  float64
	}

	scored := make([]scoredMemory, 0, len(memories))
	for _, mem := range memories {
		if len(mem.Embedding) != s.embeddingDim {
			continue // Skip invalid embeddings
		}

		normalizedMemEmbedding := normalizeVector(mem.Embedding)
		score := cosineSimilarity(normalizedQuery, normalizedMemEmbedding)

		scored = append(scored, scoredMemory{
			memory: mem,
			score:  score,
		})
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Take top K
	if topK > len(scored) {
		topK = len(scored)
	}

	results := make([]*runtime.Memory, topK)
	for i := 0; i < topK; i++ {
		results[i] = scored[i].memory

		// Update index cache
		if s.enableCache {
			s.addToIndex(primaryPartition, scored[i].memory, normalizeVector(scored[i].memory.Embedding))
		}
	}

	return results, nil
}

// Delete removes a memory (ADK-compatible interface)
func (s *DistributedMemoryService) Delete(ctx context.Context, memoryID string) error {
	// Delete from backend (backend should handle finding it)
	if err := s.backend.Delete(ctx, memoryID); err != nil {
		return err
	}

	// Remove from all indices (since we don't know which partition)
	if s.enableCache {
		s.removeFromAllIndices(memoryID)
	}

	return nil
}

// List returns all memories for a user (ADK-compatible interface)
func (s *DistributedMemoryService) List(ctx context.Context, userID string) ([]*runtime.Memory, error) {
	return s.backend.List(ctx, userID)
}

// addToIndex adds a memory to the partition-local vector index
func (s *DistributedMemoryService) addToIndex(partitionID int, memory *runtime.Memory, normalizedEmbedding []float64) {
	s.mu.RLock()
	index, ok := s.indices[partitionID]
	s.mu.RUnlock()

	if !ok {
		return
	}

	index.mu.Lock()
	defer index.mu.Unlock()

	// Add to index
	indexed := &IndexedMemory{
		Memory:     memory,
		Embedding:  normalizedEmbedding,
		CachedAt:   time.Now(),
		AccessedAt: time.Now(),
	}

	// Update or insert
	if _, exists := index.memories[memory.ID]; !exists {
		// New memory: add to parallel arrays
		index.embeddings = append(index.embeddings, normalizedEmbedding)
		index.memoryIDs = append(index.memoryIDs, memory.ID)
	} else {
		// Update existing: find and replace in parallel arrays
		for i, id := range index.memoryIDs {
			if id == memory.ID {
				index.embeddings[i] = normalizedEmbedding
				break
			}
		}
	}

	index.memories[memory.ID] = indexed
}

// searchIndex performs partition-local vector search
func (s *DistributedMemoryService) searchIndex(partitionID int, userID string, queryEmbedding []float64, topK int) []*runtime.Memory {
	s.mu.RLock()
	index, ok := s.indices[partitionID]
	s.mu.RUnlock()

	if !ok {
		return nil
	}

	index.mu.RLock()
	defer index.mu.RUnlock()

	// Score all memories for this user
	type scoredMemory struct {
		memory *runtime.Memory
		score  float64
	}

	scored := make([]scoredMemory, 0)

	for i, memID := range index.memoryIDs {
		indexed := index.memories[memID]

		// Filter by userID from metadata
		memUserID := ""
		if indexed.Memory.Metadata != nil {
			if uid, ok := indexed.Memory.Metadata["user_id"].(string); ok {
				memUserID = uid
			}
		}

		if memUserID != userID {
			continue
		}

		// Check expiration
		if time.Since(indexed.CachedAt) > s.cacheTTL {
			continue
		}

		// Compute similarity
		score := cosineSimilarity(queryEmbedding, index.embeddings[i])

		scored = append(scored, scoredMemory{
			memory: indexed.Memory,
			score:  score,
		})

		// Update access time
		indexed.AccessedAt = time.Now()
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Take top K
	if topK > len(scored) {
		topK = len(scored)
	}

	if topK == 0 {
		return nil
	}

	results := make([]*runtime.Memory, topK)
	for i := 0; i < topK; i++ {
		results[i] = scored[i].memory
	}

	return results
}

// removeFromAllIndices removes a memory from all partition indices
func (s *DistributedMemoryService) removeFromAllIndices(memoryID string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, index := range s.indices {
		index.mu.Lock()

		// Remove from map
		delete(index.memories, memoryID)

		// Remove from parallel arrays
		for i, id := range index.memoryIDs {
			if id == memoryID {
				// Remove from embeddings and memoryIDs
				index.embeddings = append(index.embeddings[:i], index.embeddings[i+1:]...)
				index.memoryIDs = append(index.memoryIDs[:i], index.memoryIDs[i+1:]...)
				break
			}
		}

		index.mu.Unlock()
	}
}

// GetIndexStats returns index statistics for monitoring
func (s *DistributedMemoryService) GetIndexStats() map[int]MemoryIndexStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := make(map[int]MemoryIndexStats)

	for partitionID, index := range s.indices {
		index.mu.RLock()
		stats[partitionID] = MemoryIndexStats{
			PartitionID:   partitionID,
			NumMemories:   len(index.memories),
			NumEmbeddings: len(index.embeddings),
		}
		index.mu.RUnlock()
	}

	return stats
}

// MemoryIndexStats provides index statistics
type MemoryIndexStats struct {
	PartitionID   int
	NumMemories   int
	NumEmbeddings int
}

// Close cleans up resources
func (s *DistributedMemoryService) Close() error {
	return s.backend.Close()
}

// Vector math utilities

// normalizeVector normalizes a vector to unit length
func normalizeVector(v []float64) []float64 {
	magnitude := 0.0
	for _, val := range v {
		magnitude += val * val
	}
	magnitude = math.Sqrt(magnitude)

	if magnitude == 0 {
		return v // Avoid division by zero
	}

	normalized := make([]float64, len(v))
	for i, val := range v {
		normalized[i] = val / magnitude
	}

	return normalized
}

// cosineSimilarity computes cosine similarity between two vectors
// Assumes vectors are already normalized
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0.0
	}

	dotProduct := 0.0
	for i := range a {
		dotProduct += a[i] * b[i]
	}

	return dotProduct
}

// dotProduct computes dot product of two vectors
func dotProduct(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0.0
	}

	sum := 0.0
	for i := range a {
		sum += a[i] * b[i]
	}

	return sum
}

// magnitude computes the magnitude (L2 norm) of a vector
func magnitude(v []float64) float64 {
	sum := 0.0
	for _, val := range v {
		sum += val * val
	}
	return math.Sqrt(sum)
}

// euclideanDistance computes Euclidean distance between two vectors
func euclideanDistance(a, b []float64) float64 {
	if len(a) != len(b) {
		return math.Inf(1)
	}

	sum := 0.0
	for i := range a {
		diff := a[i] - b[i]
		sum += diff * diff
	}

	return math.Sqrt(sum)
}
