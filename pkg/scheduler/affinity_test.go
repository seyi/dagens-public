package scheduler

import (
	"testing"
	"time"
)

func TestAffinityMap_SetAndGet(t *testing.T) {
	am := NewAffinityMap(1*time.Hour, 5*time.Minute)
	defer am.Stop()

	// Set an affinity
	entry := am.Set("partition-1", "node-1")

	if entry.NodeID != "node-1" {
		t.Errorf("expected NodeID 'node-1', got '%s'", entry.NodeID)
	}
	if entry.HitCount != 1 {
		t.Errorf("expected HitCount 1, got %d", entry.HitCount)
	}

	// Get the affinity
	retrieved := am.Get("partition-1")
	if retrieved == nil {
		t.Fatal("expected entry, got nil")
	}
	if retrieved.NodeID != "node-1" {
		t.Errorf("expected NodeID 'node-1', got '%s'", retrieved.NodeID)
	}
}

func TestAffinityMap_GetNonExistent(t *testing.T) {
	am := NewAffinityMap(1*time.Hour, 5*time.Minute)
	defer am.Stop()

	entry := am.Get("non-existent")
	if entry != nil {
		t.Errorf("expected nil for non-existent key, got %v", entry)
	}
}

func TestAffinityMap_Touch(t *testing.T) {
	am := NewAffinityMap(1*time.Hour, 5*time.Minute)
	defer am.Stop()

	// Set an affinity
	am.Set("partition-1", "node-1")

	// Touch it multiple times
	for i := 0; i < 5; i++ {
		ok := am.Touch("partition-1")
		if !ok {
			t.Fatal("expected Touch to return true for existing entry")
		}
	}

	// Verify hit count
	entry := am.Get("partition-1")
	if entry.HitCount != 6 { // 1 from Set + 5 from Touch
		t.Errorf("expected HitCount 6, got %d", entry.HitCount)
	}
}

func TestAffinityMap_TouchNonExistent(t *testing.T) {
	am := NewAffinityMap(1*time.Hour, 5*time.Minute)
	defer am.Stop()

	ok := am.Touch("non-existent")
	if ok {
		t.Error("expected Touch to return false for non-existent entry")
	}
}

func TestAffinityMap_Delete(t *testing.T) {
	am := NewAffinityMap(1*time.Hour, 5*time.Minute)
	defer am.Stop()

	// Set and then delete
	am.Set("partition-1", "node-1")
	am.Delete("partition-1")

	entry := am.Get("partition-1")
	if entry != nil {
		t.Error("expected nil after delete")
	}
}

func TestAffinityMap_Size(t *testing.T) {
	am := NewAffinityMap(1*time.Hour, 5*time.Minute)
	defer am.Stop()

	if am.Size() != 0 {
		t.Errorf("expected size 0, got %d", am.Size())
	}

	am.Set("p1", "n1")
	am.Set("p2", "n2")
	am.Set("p3", "n3")

	if am.Size() != 3 {
		t.Errorf("expected size 3, got %d", am.Size())
	}

	am.Delete("p2")
	if am.Size() != 2 {
		t.Errorf("expected size 2 after delete, got %d", am.Size())
	}
}

func TestAffinityMap_GetAll(t *testing.T) {
	am := NewAffinityMap(1*time.Hour, 5*time.Minute)
	defer am.Stop()

	am.Set("p1", "n1")
	am.Set("p2", "n2")

	all := am.GetAll()
	if len(all) != 2 {
		t.Errorf("expected 2 entries, got %d", len(all))
	}

	if all["p1"].NodeID != "n1" {
		t.Errorf("expected p1 -> n1, got %s", all["p1"].NodeID)
	}
	if all["p2"].NodeID != "n2" {
		t.Errorf("expected p2 -> n2, got %s", all["p2"].NodeID)
	}
}

func TestAffinityMap_Expiration(t *testing.T) {
	// Use very short TTL and cleanup interval for testing
	am := NewAffinityMap(50*time.Millisecond, 20*time.Millisecond)
	defer am.Stop()

	am.Set("partition-1", "node-1")

	// Verify it exists initially
	if am.Get("partition-1") == nil {
		t.Fatal("expected entry to exist initially")
	}

	// Wait for expiration and cleanup
	time.Sleep(100 * time.Millisecond)

	// Should be expired now
	if am.Get("partition-1") != nil {
		t.Error("expected entry to be expired after TTL")
	}
}

func TestAffinityMap_TouchPreventsExpiration(t *testing.T) {
	// Use short TTL
	am := NewAffinityMap(100*time.Millisecond, 30*time.Millisecond)
	defer am.Stop()

	am.Set("partition-1", "node-1")

	// Keep touching to prevent expiration
	for i := 0; i < 5; i++ {
		time.Sleep(40 * time.Millisecond)
		am.Touch("partition-1")
	}

	// Should still exist because we kept touching it
	if am.Get("partition-1") == nil {
		t.Error("expected entry to still exist after touches")
	}
}

func TestAffinityResult(t *testing.T) {
	result := AffinityResult{
		NodeID:       "node-1",
		IsHit:        true,
		IsStale:      false,
		PartitionKey: "partition-1",
	}

	if result.NodeID != "node-1" {
		t.Errorf("expected NodeID 'node-1', got '%s'", result.NodeID)
	}
	if !result.IsHit {
		t.Error("expected IsHit to be true")
	}
	if result.IsStale {
		t.Error("expected IsStale to be false")
	}
}

func TestAffinityEntry_Fields(t *testing.T) {
	now := time.Now()
	entry := &AffinityEntry{
		NodeID:     "node-1",
		CreatedAt:  now,
		LastUsedAt: now,
		HitCount:   5,
	}

	if entry.NodeID != "node-1" {
		t.Errorf("expected NodeID 'node-1', got '%s'", entry.NodeID)
	}
	if entry.HitCount != 5 {
		t.Errorf("expected HitCount 5, got %d", entry.HitCount)
	}
	if !entry.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt mismatch")
	}
}
