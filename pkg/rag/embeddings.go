package rag

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"sync"
)

// EmbeddingService generates vector embeddings from text
// Implementations can use OpenAI, Cohere, local models, etc.
type EmbeddingService interface {
	// Embed generates an embedding for a single text
	Embed(ctx context.Context, text string) ([]float64, error)

	// EmbedBatch generates embeddings for multiple texts efficiently
	EmbedBatch(ctx context.Context, texts []string) ([][]float64, error)

	// Dimensions returns the dimensionality of embeddings
	Dimensions() int

	// ModelName returns the name of the embedding model
	ModelName() string
}

// EmbeddingServiceConfig contains common configuration for embedding services
type EmbeddingServiceConfig struct {
	APIKey     string // API key for embedding service
	Model      string // Model name (e.g., "text-embedding-ada-002")
	BatchSize  int    // Maximum batch size
	MaxRetries int    // Maximum retry attempts
	CacheSize  int    // Number of embeddings to cache
}

// CachedEmbeddingService wraps an embedding service with caching
type CachedEmbeddingService struct {
	base      EmbeddingService
	cache     map[string][]float64
	mu        sync.RWMutex
	maxSize   int
	hits      int64
	misses    int64
	evictions int64
}

// NewCachedEmbeddingService creates a cached embedding service
func NewCachedEmbeddingService(base EmbeddingService, maxSize int) *CachedEmbeddingService {
	if maxSize <= 0 {
		maxSize = 10000 // Default cache size
	}
	return &CachedEmbeddingService{
		base:    base,
		cache:   make(map[string][]float64),
		maxSize: maxSize,
	}
}

func (c *CachedEmbeddingService) Embed(ctx context.Context, text string) ([]float64, error) {
	// Generate cache key
	key := c.cacheKey(text)

	// Check cache
	c.mu.RLock()
	if embedding, exists := c.cache[key]; exists {
		c.mu.RUnlock()
		c.mu.Lock()
		c.hits++
		c.mu.Unlock()
		return embedding, nil
	}
	c.mu.RUnlock()

	// Cache miss - generate embedding
	c.mu.Lock()
	c.misses++
	c.mu.Unlock()

	embedding, err := c.base.Embed(ctx, text)
	if err != nil {
		return nil, err
	}

	// Store in cache
	c.mu.Lock()
	defer c.mu.Unlock()

	// Evict if cache is full (simple LRU-like)
	if len(c.cache) >= c.maxSize {
		// Remove random entry (simple eviction strategy)
		for k := range c.cache {
			delete(c.cache, k)
			c.evictions++
			break
		}
	}

	c.cache[key] = embedding
	return embedding, nil
}

func (c *CachedEmbeddingService) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	embeddings := make([][]float64, len(texts))
	uncachedIndices := make([]int, 0)
	uncachedTexts := make([]string, 0)

	// Check cache for each text
	c.mu.RLock()
	for i, text := range texts {
		key := c.cacheKey(text)
		if embedding, exists := c.cache[key]; exists {
			embeddings[i] = embedding
			c.hits++
		} else {
			uncachedIndices = append(uncachedIndices, i)
			uncachedTexts = append(uncachedTexts, text)
			c.misses++
		}
	}
	c.mu.RUnlock()

	// Generate embeddings for uncached texts
	if len(uncachedTexts) > 0 {
		newEmbeddings, err := c.base.EmbedBatch(ctx, uncachedTexts)
		if err != nil {
			return nil, err
		}

		// Store new embeddings in cache and result
		c.mu.Lock()
		for i, idx := range uncachedIndices {
			embedding := newEmbeddings[i]
			embeddings[idx] = embedding
			key := c.cacheKey(uncachedTexts[i])

			// Evict if needed
			if len(c.cache) >= c.maxSize {
				for k := range c.cache {
					delete(c.cache, k)
					c.evictions++
					break
				}
			}

			c.cache[key] = embedding
		}
		c.mu.Unlock()
	}

	return embeddings, nil
}

func (c *CachedEmbeddingService) Dimensions() int {
	return c.base.Dimensions()
}

func (c *CachedEmbeddingService) ModelName() string {
	return c.base.ModelName()
}

func (c *CachedEmbeddingService) cacheKey(text string) string {
	// Use MD5 hash as cache key
	hash := md5.Sum([]byte(text))
	return hex.EncodeToString(hash[:])
}

// GetCacheStats returns cache statistics
func (c *CachedEmbeddingService) GetCacheStats() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	total := c.hits + c.misses
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(c.hits) / float64(total)
	}

	return map[string]interface{}{
		"hits":       c.hits,
		"misses":     c.misses,
		"evictions":  c.evictions,
		"cache_size": len(c.cache),
		"hit_rate":   hitRate,
	}
}

// ClearCache clears all cached embeddings
func (c *CachedEmbeddingService) ClearCache() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[string][]float64)
	c.hits = 0
	c.misses = 0
	c.evictions = 0
}

// SimpleEmbeddingService is a basic implementation for testing/demo
// Uses simple bag-of-words TF-IDF-like embeddings (NOT production quality)
type SimpleEmbeddingService struct {
	dimensions int
	vocabulary map[string]int
	mu         sync.RWMutex
}

// NewSimpleEmbeddingService creates a simple embedding service for testing
// WARNING: This is NOT suitable for production use. Use OpenAI, Cohere, or Sentence Transformers instead.
func NewSimpleEmbeddingService(dimensions int) *SimpleEmbeddingService {
	return &SimpleEmbeddingService{
		dimensions: dimensions,
		vocabulary: make(map[string]int),
	}
}

func (s *SimpleEmbeddingService) Embed(ctx context.Context, text string) ([]float64, error) {
	// Tokenize (very simple)
	tokens := s.tokenize(text)

	// Create embedding vector
	embedding := make([]float64, s.dimensions)

	// Simple hash-based embedding (for demo only)
	for _, token := range tokens {
		hash := s.hashToken(token)
		idx := hash % s.dimensions
		embedding[idx] += 1.0
	}

	// Normalize
	return s.normalize(embedding), nil
}

func (s *SimpleEmbeddingService) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	embeddings := make([][]float64, len(texts))
	for i, text := range texts {
		embedding, err := s.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		embeddings[i] = embedding
	}
	return embeddings, nil
}

func (s *SimpleEmbeddingService) Dimensions() int {
	return s.dimensions
}

func (s *SimpleEmbeddingService) ModelName() string {
	return "simple-embedding"
}

func (s *SimpleEmbeddingService) tokenize(text string) []string {
	// Very simple tokenization
	text = strings.ToLower(text)
	text = strings.ReplaceAll(text, ".", " ")
	text = strings.ReplaceAll(text, ",", " ")
	text = strings.ReplaceAll(text, "!", " ")
	text = strings.ReplaceAll(text, "?", " ")
	return strings.Fields(text)
}

func (s *SimpleEmbeddingService) hashToken(token string) int {
	hash := 0
	for _, c := range token {
		hash = (hash*31 + int(c)) & 0x7FFFFFFF
	}
	return hash
}

func (s *SimpleEmbeddingService) normalize(vec []float64) []float64 {
	// L2 normalization
	var sumSq float64
	for _, v := range vec {
		sumSq += v * v
	}

	if sumSq == 0 {
		return vec
	}

	norm := math.Sqrt(sumSq)
	normalized := make([]float64, len(vec))
	for i, v := range vec {
		normalized[i] = v / norm
	}
	return normalized
}

// MockEmbeddingService is a mock for testing
type MockEmbeddingService struct {
	dimensions int
	model      string
	embeddings map[string][]float64
}

// NewMockEmbeddingService creates a mock embedding service
func NewMockEmbeddingService(dimensions int) *MockEmbeddingService {
	return &MockEmbeddingService{
		dimensions: dimensions,
		model:      "mock-embedding",
		embeddings: make(map[string][]float64),
	}
}

func (m *MockEmbeddingService) Embed(ctx context.Context, text string) ([]float64, error) {
	// Return predefined embedding if exists
	if embedding, exists := m.embeddings[text]; exists {
		return embedding, nil
	}

	// Generate deterministic random embedding based on text
	embedding := make([]float64, m.dimensions)
	hash := 0
	for _, c := range text {
		hash = (hash*31 + int(c)) & 0x7FFFFFFF
	}

	// Use hash as seed for deterministic random
	for i := range embedding {
		hash = (hash*1103515245 + 12345) & 0x7FFFFFFF
		embedding[i] = float64(hash%1000) / 1000.0
	}

	return embedding, nil
}

func (m *MockEmbeddingService) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	embeddings := make([][]float64, len(texts))
	for i, text := range texts {
		embedding, err := m.Embed(ctx, text)
		if err != nil {
			return nil, err
		}
		embeddings[i] = embedding
	}
	return embeddings, nil
}

func (m *MockEmbeddingService) Dimensions() int {
	return m.dimensions
}

func (m *MockEmbeddingService) ModelName() string {
	return m.model
}

// SetEmbedding sets a predefined embedding for testing
func (m *MockEmbeddingService) SetEmbedding(text string, embedding []float64) error {
	if len(embedding) != m.dimensions {
		return fmt.Errorf("embedding dimension mismatch: expected %d, got %d", m.dimensions, len(embedding))
	}
	m.embeddings[text] = embedding
	return nil
}
