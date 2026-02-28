package coordination

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/seyi/dagens/pkg/observability"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

// DistributedBarrier provides a distributed barrier synchronization primitive using etcd.
// It uses lease-bound keys to ensure liveness: if a participant crashes, they are
// automatically removed from the count.
type DistributedBarrier struct {
	client             *clientv3.Client
	session            *concurrency.Session
	key                string
	count              int
	participantCounter int64 // Counter for unique participant IDs per Wait() call
}

// NewDistributedBarrier creates a new distributed barrier.
func NewDistributedBarrier(client *clientv3.Client, session *concurrency.Session, key string, count int) *DistributedBarrier {
	return &DistributedBarrier{
		client:             client,
		session:            session,
		key:                key,
		count:              count,
		participantCounter: 0,
	}
}

// Wait blocks until 'count' participants have joined the barrier.
// It is crash-safe: dead participants (expired leases) are not counted.
// It supports timeouts via the provided context.
func (db *DistributedBarrier) Wait(ctx context.Context) (err error) {
	metrics := observability.GetMetrics()
	logger := observability.GetLogger()
	waitStart := time.Now()
	defer func() {
		status := "success"
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				status = "timeout"
			} else {
				status = "error"
			}
		}
		metrics.RecordBarrierWait(db.key, status, time.Since(waitStart))
		logger.Debug("barrier_wait_finished",
			observability.Field("barrier", db.key),
			observability.Field("status", status),
			observability.Field("duration_ms", time.Since(waitStart).Milliseconds()),
		)
	}()

	generation, err := db.getOrCreateGeneration(ctx)
	if err != nil {
		return fmt.Errorf("failed to get barrier generation: %w", err)
	}

	// Generate unique participant ID per Wait() call.
	// Format: <leaseID>-<counter> ensures uniqueness both:
	// - Across nodes (different lease IDs)
	// - Within a node (atomic counter for multiple goroutines with same session)
	participantNum := atomic.AddInt64(&db.participantCounter, 1)
	participantKey := fmt.Sprintf("%s/participants/%s/%x-%d", db.key, generation, db.session.Lease(), participantNum)
	participantsPrefix := fmt.Sprintf("%s/participants/%s/", db.key, generation)
	releaseKey := fmt.Sprintf("%s/released/%s", db.key, generation)

	// 1. Register presence with a lease.
	// If this node crashes, the lease expires, the key is removed, and the count drops.
	_, err = db.client.Put(ctx, participantKey, "waiting", clientv3.WithLease(db.session.Lease()))
	if err != nil {
		return fmt.Errorf("failed to register participant: %w", err)
	}
	logger.Debug("barrier_wait_registered",
		observability.Field("barrier", db.key),
		observability.Field("generation", generation),
		observability.Field("participant", participantKey),
		observability.Field("target_count", db.count),
	)

	// Ensure cleanup on exit (normal return or timeout/cancel)
	defer func() {
		// Use a detached context for cleanup to ensure it runs even if ctx is cancelled
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		db.client.Delete(cleanupCtx, participantKey)
	}()

	// 2. Use watch-based synchronization with periodic polling fallback.
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	watchCh := db.client.Watch(ctx, db.key, clientv3.WithPrefix())

	for {
		// Check if barrier is already released first.
		resp, err := db.client.Get(ctx, releaseKey)
		if err != nil {
			return fmt.Errorf("failed to check release status: %w", err)
		}
		if len(resp.Kvs) > 0 {
			return nil
		}

		// Count active participants for this generation only.
		countResp, err := db.client.Get(ctx, participantsPrefix,
			clientv3.WithPrefix(), clientv3.WithCountOnly())
		if err != nil {
			return fmt.Errorf("failed to count participants: %w", err)
		}

		if countResp.Count >= int64(db.count) {
			// Trip barrier atomically. Do NOT set a lease on release key.
			tripStart := time.Now()
			txnResp, err := db.client.Txn(ctx).
				If(clientv3.Compare(clientv3.CreateRevision(releaseKey), "=", 0)).
				Then(clientv3.OpPut(releaseKey, fmt.Sprintf("%d", time.Now().UnixNano()))).
				Commit()
			tripDuration := time.Since(tripStart)
			metrics.RecordBarrierTripLatency(db.key, tripDuration)
			if err != nil {
				metrics.RecordBarrierTrip(db.key, "error")
				logger.Warn("barrier_trip_failed",
					observability.Field("barrier", db.key),
					observability.Field("generation", generation),
					observability.Field("participants", countResp.Count),
					observability.Field("error", err.Error()),
					observability.Field("duration_ms", tripDuration.Milliseconds()),
				)
				return fmt.Errorf("failed to trip barrier: %w", err)
			}
			if txnResp.Succeeded {
				metrics.RecordBarrierTrip(db.key, "success")
				logger.Debug("barrier_tripped",
					observability.Field("barrier", db.key),
					observability.Field("generation", generation),
					observability.Field("participants", countResp.Count),
					observability.Field("duration_ms", tripDuration.Milliseconds()),
				)
			} else {
				metrics.RecordBarrierTrip(db.key, "already_released")
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case watchResp := <-watchCh:
			if watchResp.Canceled && watchResp.Err() != nil {
				return fmt.Errorf("barrier watch canceled: %w", watchResp.Err())
			}
		case <-ticker.C:
		}
	}
}

func (db *DistributedBarrier) getOrCreateGeneration(ctx context.Context) (string, error) {
	generationKey := db.key + "/generation"
	newGeneration := fmt.Sprintf("%d", time.Now().UnixNano())

	txnResp, err := db.client.Txn(ctx).
		If(clientv3.Compare(clientv3.CreateRevision(generationKey), "=", 0)).
		Then(clientv3.OpPut(generationKey, newGeneration), clientv3.OpGet(generationKey)).
		Else(clientv3.OpGet(generationKey)).
		Commit()
	if err != nil {
		return "", err
	}
	if txnResp.Succeeded {
		observability.GetMetrics().RecordBarrierGenerationCreated(db.key)
		observability.GetLogger().Debug("barrier_generation_created",
			observability.Field("barrier", db.key),
			observability.Field("generation", newGeneration),
		)
	}

	getResp := txnResp.Responses[len(txnResp.Responses)-1].GetResponseRange()
	if len(getResp.Kvs) == 0 {
		return "", fmt.Errorf("generation key missing after transaction")
	}
	return string(getResp.Kvs[0].Value), nil
}

// Reset resets the barrier for future use.
// This is dangerous if participants are still waiting; use with caution.
func (db *DistributedBarrier) Reset(ctx context.Context) error {
	// Delete release key and all participants
	_, err := db.client.Delete(ctx, db.key, clientv3.WithPrefix())
	return err
}
