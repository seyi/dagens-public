// Package memory provides long-term memory for agents
// Inspired by ADK's memory persistence for context across invocations
package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemoryStore is the interface for agent memory persistence
type MemoryStore interface {
	// Store saves a memory entry
	Store(ctx context.Context, agentID string, key string, value interface{}, metadata map[string]interface{}) error

	// Retrieve gets a memory entry by key
	Retrieve(ctx context.Context, agentID string, key string) (*MemoryEntry, error)

	// Search finds memory entries matching a query
	Search(ctx context.Context, agentID string, query *MemoryQuery) ([]*MemoryEntry, error)

	// Update updates an existing memory entry
	Update(ctx context.Context, agentID string, key string, value interface{}) error

	// Delete removes a memory entry
	Delete(ctx context.Context, agentID string, key string) error

	// List returns all memory entries for an agent
	List(ctx context.Context, agentID string, limit int, offset int) ([]*MemoryEntry, error)

	// Clear removes all memories for an agent
	Clear(ctx context.Context, agentID string) error
}

// MemoryEntry represents a single memory item
type MemoryEntry struct {
	ID        string
	AgentID   string
	Key       string
	Value     interface{}
	Timestamp time.Time
	AccessCount int
	LastAccess time.Time
	Metadata  map[string]interface{}
	Tags      []string
	Importance float64 // 0.0 to 1.0
}

// MemoryQuery represents a memory search query
type MemoryQuery struct {
	Text       string
	Tags       []string
	TimeRange  *TimeRange
	MinImportance float64
	Limit      int
	Offset     int
	SortBy     SortOrder
}

// TimeRange represents a time range for queries
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// SortOrder defines memory sorting
type SortOrder string

const (
	SortByTime       SortOrder = "time"
	SortByAccess     SortOrder = "access"
	SortByImportance SortOrder = "importance"
)

// InMemoryStore implements in-memory storage
type InMemoryStore struct {
	memories map[string]map[string]*MemoryEntry // agentID -> key -> entry
	mu       sync.RWMutex
}

func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		memories: make(map[string]map[string]*MemoryEntry),
	}
}

func (s *InMemoryStore) Store(ctx context.Context, agentID string, key string, value interface{}, metadata map[string]interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.memories[agentID] == nil {
		s.memories[agentID] = make(map[string]*MemoryEntry)
	}

	entry := &MemoryEntry{
		ID:         uuid.New().String(),
		AgentID:    agentID,
		Key:        key,
		Value:      value,
		Timestamp:  time.Now(),
		AccessCount: 0,
		LastAccess: time.Now(),
		Metadata:   metadata,
		Tags:       []string{},
		Importance: 0.5, // Default importance
	}

	if metadata != nil {
		if tags, ok := metadata["tags"].([]string); ok {
			entry.Tags = tags
		}
		if importance, ok := metadata["importance"].(float64); ok {
			entry.Importance = importance
		}
	}

	s.memories[agentID][key] = entry
	return nil
}

func (s *InMemoryStore) Retrieve(ctx context.Context, agentID string, key string) (*MemoryEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	agentMem, exists := s.memories[agentID]
	if !exists {
		return nil, fmt.Errorf("no memories for agent %s", agentID)
	}

	entry, exists := agentMem[key]
	if !exists {
		return nil, fmt.Errorf("memory key %s not found", key)
	}

	// Update access tracking
	entry.AccessCount++
	entry.LastAccess = time.Now()

	return entry, nil
}

func (s *InMemoryStore) Search(ctx context.Context, agentID string, query *MemoryQuery) ([]*MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agentMem, exists := s.memories[agentID]
	if !exists {
		return []*MemoryEntry{}, nil
	}

	results := make([]*MemoryEntry, 0)

	for _, entry := range agentMem {
		if s.matchesQuery(entry, query) {
			results = append(results, entry)
		}
	}

	// Sort results
	s.sortResults(results, query.SortBy)

	// Apply pagination
	start := query.Offset
	end := start + query.Limit
	if end > len(results) || query.Limit == 0 {
		end = len(results)
	}
	if start > len(results) {
		start = len(results)
	}

	return results[start:end], nil
}

func (s *InMemoryStore) matchesQuery(entry *MemoryEntry, query *MemoryQuery) bool {
	// Check importance
	if entry.Importance < query.MinImportance {
		return false
	}

	// Check time range
	if query.TimeRange != nil {
		if entry.Timestamp.Before(query.TimeRange.Start) || entry.Timestamp.After(query.TimeRange.End) {
			return false
		}
	}

	// Check tags
	if len(query.Tags) > 0 {
		hasTag := false
		for _, queryTag := range query.Tags {
			for _, entryTag := range entry.Tags {
				if entryTag == queryTag {
					hasTag = true
					break
				}
			}
			if hasTag {
				break
			}
		}
		if !hasTag {
			return false
		}
	}

	// Text search (simple substring match)
	// In a real implementation, this would use semantic search
	if query.Text != "" {
		if valueStr, ok := entry.Value.(string); ok {
			// Simple contains check
			// TODO: Implement semantic search with vector embeddings
			if !contains(valueStr, query.Text) {
				return false
			}
		}
	}

	return true
}

func (s *InMemoryStore) sortResults(results []*MemoryEntry, sortBy SortOrder) {
	// Simple bubble sort for now
	// In production, use sort.Slice
	n := len(results)
	for i := 0; i < n-1; i++ {
		for j := 0; j < n-i-1; j++ {
			shouldSwap := false

			switch sortBy {
			case SortByTime:
				shouldSwap = results[j].Timestamp.Before(results[j+1].Timestamp)
			case SortByAccess:
				shouldSwap = results[j].LastAccess.Before(results[j+1].LastAccess)
			case SortByImportance:
				shouldSwap = results[j].Importance < results[j+1].Importance
			}

			if shouldSwap {
				results[j], results[j+1] = results[j+1], results[j]
			}
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func (s *InMemoryStore) Update(ctx context.Context, agentID string, key string, value interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	agentMem, exists := s.memories[agentID]
	if !exists {
		return fmt.Errorf("no memories for agent %s", agentID)
	}

	entry, exists := agentMem[key]
	if !exists {
		return fmt.Errorf("memory key %s not found", key)
	}

	entry.Value = value
	entry.Timestamp = time.Now()
	entry.AccessCount++

	return nil
}

func (s *InMemoryStore) Delete(ctx context.Context, agentID string, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	agentMem, exists := s.memories[agentID]
	if !exists {
		return fmt.Errorf("no memories for agent %s", agentID)
	}

	delete(agentMem, key)
	return nil
}

func (s *InMemoryStore) List(ctx context.Context, agentID string, limit int, offset int) ([]*MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	agentMem, exists := s.memories[agentID]
	if !exists {
		return []*MemoryEntry{}, nil
	}

	entries := make([]*MemoryEntry, 0, len(agentMem))
	for _, entry := range agentMem {
		entries = append(entries, entry)
	}

	// Sort by timestamp
	s.sortResults(entries, SortByTime)

	// Apply pagination
	start := offset
	end := start + limit
	if end > len(entries) || limit == 0 {
		end = len(entries)
	}
	if start > len(entries) {
		start = len(entries)
	}

	return entries[start:end], nil
}

func (s *InMemoryStore) Clear(ctx context.Context, agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.memories, agentID)
	return nil
}

// VectorMemoryStore implements semantic search using vector embeddings
type VectorMemoryStore struct {
	baseStore    MemoryStore
	embeddings   map[string][]float64 // entryID -> embedding vector
	embeddingFn  EmbeddingFunction
	mu           sync.RWMutex
}

// EmbeddingFunction generates vector embeddings from text
type EmbeddingFunction func(text string) ([]float64, error)

func NewVectorMemoryStore(baseStore MemoryStore, embeddingFn EmbeddingFunction) *VectorMemoryStore {
	return &VectorMemoryStore{
		baseStore:   baseStore,
		embeddings:  make(map[string][]float64),
		embeddingFn: embeddingFn,
	}
}

func (v *VectorMemoryStore) Store(ctx context.Context, agentID string, key string, value interface{}, metadata map[string]interface{}) error {
	// Store in base
	if err := v.baseStore.Store(ctx, agentID, key, value, metadata); err != nil {
		return err
	}

	// Generate embedding
	if valueStr, ok := value.(string); ok {
		embedding, err := v.embeddingFn(valueStr)
		if err == nil {
			v.mu.Lock()
			v.embeddings[key] = embedding
			v.mu.Unlock()
		}
	}

	return nil
}

func (v *VectorMemoryStore) Retrieve(ctx context.Context, agentID string, key string) (*MemoryEntry, error) {
	return v.baseStore.Retrieve(ctx, agentID, key)
}

func (v *VectorMemoryStore) Search(ctx context.Context, agentID string, query *MemoryQuery) ([]*MemoryEntry, error) {
	// If text query, use semantic search
	if query.Text != "" && v.embeddingFn != nil {
		return v.semanticSearch(ctx, agentID, query)
	}

	// Otherwise, use base store
	return v.baseStore.Search(ctx, agentID, query)
}

func (v *VectorMemoryStore) semanticSearch(ctx context.Context, agentID string, query *MemoryQuery) ([]*MemoryEntry, error) {
	// Generate query embedding
	queryEmbedding, err := v.embeddingFn(query.Text)
	if err != nil {
		return nil, err
	}

	// Get all memories
	allMemories, err := v.baseStore.List(ctx, agentID, 0, 0)
	if err != nil {
		return nil, err
	}

	// Calculate similarities
	type scoredEntry struct {
		entry *MemoryEntry
		score float64
	}

	scored := make([]scoredEntry, 0)

	v.mu.RLock()
	for _, entry := range allMemories {
		if embedding, exists := v.embeddings[entry.Key]; exists {
			similarity := cosineSimilarity(queryEmbedding, embedding)
			scored = append(scored, scoredEntry{entry, similarity})
		}
	}
	v.mu.RUnlock()

	// Sort by similarity
	for i := 0; i < len(scored)-1; i++ {
		for j := 0; j < len(scored)-i-1; j++ {
			if scored[j].score < scored[j+1].score {
				scored[j], scored[j+1] = scored[j+1], scored[j]
			}
		}
	}

	// Extract entries
	limit := query.Limit
	if limit == 0 || limit > len(scored) {
		limit = len(scored)
	}

	results := make([]*MemoryEntry, limit)
	for i := 0; i < limit; i++ {
		results[i] = scored[i].entry
	}

	return results, nil
}

func (v *VectorMemoryStore) Update(ctx context.Context, agentID string, key string, value interface{}) error {
	// Update base
	if err := v.baseStore.Update(ctx, agentID, key, value); err != nil {
		return err
	}

	// Update embedding
	if valueStr, ok := value.(string); ok {
		embedding, err := v.embeddingFn(valueStr)
		if err == nil {
			v.mu.Lock()
			v.embeddings[key] = embedding
			v.mu.Unlock()
		}
	}

	return nil
}

func (v *VectorMemoryStore) Delete(ctx context.Context, agentID string, key string) error {
	v.mu.Lock()
	delete(v.embeddings, key)
	v.mu.Unlock()

	return v.baseStore.Delete(ctx, agentID, key)
}

func (v *VectorMemoryStore) List(ctx context.Context, agentID string, limit int, offset int) ([]*MemoryEntry, error) {
	return v.baseStore.List(ctx, agentID, limit, offset)
}

func (v *VectorMemoryStore) Clear(ctx context.Context, agentID string) error {
	// Clear embeddings
	entries, _ := v.baseStore.List(ctx, agentID, 0, 0)

	v.mu.Lock()
	for _, entry := range entries {
		delete(v.embeddings, entry.Key)
	}
	v.mu.Unlock()

	return v.baseStore.Clear(ctx, agentID)
}

// cosineSimilarity calculates cosine similarity between two vectors
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct, normA, normB float64

	for i := 0; i < len(a); i++ {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (sqrt(normA) * sqrt(normB))
}

// Simple square root implementation
func sqrt(x float64) float64 {
	if x == 0 {
		return 0
	}
	z := x
	for i := 0; i < 10; i++ {
		z -= (z*z - x) / (2 * z)
	}
	return z
}
