package scheduler

import (
	"log"
	"sync"
	"time"
)

// AffinityEntry represents a sticky mapping from a PartitionKey to a NodeID
type AffinityEntry struct {
	NodeID     string    // The worker node this partition is pinned to
	CreatedAt  time.Time // When the affinity was established
	LastUsedAt time.Time // Last time a task used this affinity
	HitCount   int64     // Number of tasks that used this affinity
}

// AffinityMap manages partition-to-node affinity mappings with TTL-based expiration
type AffinityMap struct {
	entries map[string]*AffinityEntry
	mu      sync.RWMutex
	ttl     time.Duration
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

// NewAffinityMap creates a new AffinityMap with the specified TTL and cleanup interval
func NewAffinityMap(ttl, cleanupInterval time.Duration) *AffinityMap {
	am := &AffinityMap{
		entries: make(map[string]*AffinityEntry),
		ttl:     ttl,
		stopCh:  make(chan struct{}),
	}

	// Start background cleanup goroutine
	am.wg.Add(1)
	go am.cleanupLoop(cleanupInterval)

	return am
}

// Get retrieves an affinity entry for the given partition key
// Returns nil if no entry exists
func (am *AffinityMap) Get(partitionKey string) *AffinityEntry {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return am.entries[partitionKey]
}

// Set creates or updates an affinity entry for the given partition key
func (am *AffinityMap) Set(partitionKey string, nodeID string) *AffinityEntry {
	am.mu.Lock()
	defer am.mu.Unlock()

	now := time.Now()
	entry := &AffinityEntry{
		NodeID:     nodeID,
		CreatedAt:  now,
		LastUsedAt: now,
		HitCount:   1,
	}
	am.entries[partitionKey] = entry
	return entry
}

// Touch updates the LastUsedAt timestamp and increments HitCount for an existing entry
// Returns false if the entry doesn't exist
func (am *AffinityMap) Touch(partitionKey string) bool {
	am.mu.Lock()
	defer am.mu.Unlock()

	entry, exists := am.entries[partitionKey]
	if !exists {
		return false
	}

	entry.LastUsedAt = time.Now()
	entry.HitCount++
	return true
}

// Delete removes an affinity entry for the given partition key
func (am *AffinityMap) Delete(partitionKey string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	delete(am.entries, partitionKey)
}

// Size returns the number of entries in the affinity map
func (am *AffinityMap) Size() int {
	am.mu.RLock()
	defer am.mu.RUnlock()
	return len(am.entries)
}

// GetAll returns a copy of all entries (for observability/debugging)
func (am *AffinityMap) GetAll() map[string]AffinityEntry {
	am.mu.RLock()
	defer am.mu.RUnlock()

	result := make(map[string]AffinityEntry, len(am.entries))
	for k, v := range am.entries {
		result[k] = *v // Copy the entry
	}
	return result
}

// Stop stops the background cleanup goroutine
func (am *AffinityMap) Stop() {
	close(am.stopCh)
	am.wg.Wait()
}

// cleanupLoop runs periodically to remove expired entries
func (am *AffinityMap) cleanupLoop(interval time.Duration) {
	defer am.wg.Done()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-am.stopCh:
			return
		case <-ticker.C:
			am.cleanupExpired()
		}
	}
}

// cleanupExpired removes all entries that have been idle longer than the TTL
func (am *AffinityMap) cleanupExpired() {
	am.mu.Lock()
	defer am.mu.Unlock()

	now := time.Now()
	expiredKeys := make([]string, 0)

	for partitionKey, entry := range am.entries {
		idleTime := now.Sub(entry.LastUsedAt)
		if idleTime > am.ttl {
			expiredKeys = append(expiredKeys, partitionKey)
		}
	}

	for _, key := range expiredKeys {
		delete(am.entries, key)
		log.Printf("[AFFINITY] EXPIRED: PartitionKey=%s (idle for %v)", key, am.ttl)
	}

	if len(expiredKeys) > 0 {
		log.Printf("[AFFINITY] Cleanup: removed %d expired entries, %d remaining", len(expiredKeys), len(am.entries))
	}
}

// AffinityResult represents the outcome of a node selection with affinity
type AffinityResult struct {
	NodeID       string
	IsHit        bool   // True if we used an existing affinity
	IsStale      bool   // True if we had to delete a stale affinity
	PartitionKey string // The partition key used (empty if none)
}
