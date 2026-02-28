package coordination

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

// DistributedSemaphore provides a distributed counting semaphore using etcd.
// It uses ephemeral keys tied to an etcd session and validates acquisition
// based on etcd create revisions, ensuring fairness (FIFO) and robustness
// against node crashes (lease-based cleanup).
type DistributedSemaphore struct {
	client  *clientv3.Client
	session *concurrency.Session
	key     string
	size    int
}

// NewDistributedSemaphore creates a new distributed semaphore.
// The key should be a prefix used to store permits.
// The size is the maximum number of concurrent permits allowed.
func NewDistributedSemaphore(client *clientv3.Client, session *concurrency.Session, key string, size int) *DistributedSemaphore {
	return &DistributedSemaphore{
		client:  client,
		session: session,
		key:     key,
		size:    size,
	}
}

// Acquire acquires a permit from the semaphore.
// It blocks until a permit is available or the context is cancelled.
// This implementation is fair (FIFO) and crash-safe.
func (ds *DistributedSemaphore) Acquire(ctx context.Context) error {
	// Generate a unique ID for this permit request to allow multiple permits per session
	permitID := uuid.New().String()
	permitKey := fmt.Sprintf("%s/permits/%x/%s", ds.key, ds.session.Lease(), permitID)

	// 1. Try to register our intent to acquire a permit
	// We use the lease to ensure that if we crash, our "intent" (and permit) is cleaned up.
	_, err := ds.client.Put(ctx, permitKey, "waiting", clientv3.WithLease(ds.session.Lease()))
	if err != nil {
		return fmt.Errorf("failed to put permit key: %w", err)
	}

	// Ensure cleanup if we exit with an error (e.g., context cancellation)
	// before we officially "own" the permit. 
	success := false
	defer func() {
		if !success {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			ds.client.Delete(cleanupCtx, permitKey)
		}
	}()

	for {
		// 2. Get all participants sorted by creation revision to determine our rank
		resp, err := ds.client.Get(ctx, ds.key+"/permits/", clientv3.WithPrefix(), clientv3.WithSort(clientv3.SortByCreateRevision, clientv3.SortAscend))
		if err != nil {
			return fmt.Errorf("failed to query permits: %w", err)
		}

		// Find our position
		rank := -1
		for i, kv := range resp.Kvs {
			if string(kv.Key) == permitKey {
				rank = i
				break
			}
		}

		if rank == -1 {
			// Our key was somehow deleted (e.g., session lost)
			return fmt.Errorf("permit key lost during acquisition")
		}

		// 3. Check if our rank is within the allowed size
		if rank < ds.size {
			success = true
			return nil // Acquired!
		}

		// 4. Wait for changes to the permits prefix
		// We watch from the revision of our last 'Get' to avoid missing events.
		watchCtx, watchCancel := context.WithCancel(ctx)
		watchCh := ds.client.Watch(watchCtx, ds.key+"/permits/", clientv3.WithPrefix(), clientv3.WithRev(resp.Header.Revision+1))
		
		select {
		case <-ctx.Done():
			watchCancel()
			return ctx.Err()
		case wr := <-watchCh:
			watchCancel()
			if wr.Err() != nil {
				return fmt.Errorf("watch error: %w", wr.Err())
			}
			// Something changed, retry the loop to re-check rank
		}
	}
}

// Release releases a permit back to the semaphore.
// Note: This releases *one* permit held by the session.
// Ideally, the caller should pass the permitID, but keeping the interface simple
// implies we might release *any* permit held by this session.
// However, since we don't return a handle, we will release the *oldest* permit 
// held by this session to approximate FIFO release.
func (ds *DistributedSemaphore) Release(ctx context.Context) error {
	// Find all permits held by this session
	prefix := fmt.Sprintf("%s/permits/%x/", ds.key, ds.session.Lease())
	
	resp, err := ds.client.Get(ctx, prefix, clientv3.WithPrefix(), clientv3.WithSort(clientv3.SortByCreateRevision, clientv3.SortAscend), clientv3.WithLimit(1))
	if err != nil {
		return err
	}

	if len(resp.Kvs) == 0 {
		return fmt.Errorf("no permits held by this session")
	}

	// Delete the oldest one
	_, err = ds.client.Delete(ctx, string(resp.Kvs[0].Key))
	return err
}

// GetAvailable returns the number of currently active permits.
// Note: This counts "waiting" nodes as well if they are in the queue.
// For a more accurate "remaining slots" count, we compare with size.
func (ds *DistributedSemaphore) GetAvailable(ctx context.Context) (int, error) {
	resp, err := ds.client.Get(ctx, ds.key+"/permits/", clientv3.WithPrefix(), clientv3.WithCountOnly())
	if err != nil {
		return 0, err
	}

	available := ds.size - int(resp.Count)
	if available < 0 {
		available = 0
	}
	return available, nil
}

// GetHolders returns the list of lease IDs (as hex strings) currently holding or waiting for permits.
func (ds *DistributedSemaphore) GetHolders(ctx context.Context) ([]string, error) {
	resp, err := ds.client.Get(ctx, ds.key+"/permits/", clientv3.WithPrefix(), clientv3.WithSort(clientv3.SortByCreateRevision, clientv3.SortAscend))
	if err != nil {
		return nil, err
	}

	holders := make([]string, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		// Key format: {prefix}/permits/{leaseID}/{uuid}
		keyStr := string(kv.Key)
		
		// Parse lease ID manually
		// Expected format: .../permits/12345678/uuid...
		parts := strings.Split(keyStr, "/permits/")
		if len(parts) != 2 {
			continue
		}
		
		subParts := strings.Split(parts[1], "/")
		if len(subParts) >= 1 {
			holders = append(holders, subParts[0])
		}
	}
	return holders, nil
}
