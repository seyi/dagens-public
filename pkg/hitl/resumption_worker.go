package hitl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// ResumptionWorker pulls jobs from a queue and resumes graph execution.
type ResumptionWorker struct {
	queue            ResumptionQueue
	checkpointStore  CheckpointStore
	idempotencyStore IdempotencyStore
	graphRegistry    GraphRegistry
	executorFactory  func(graphID, graphVersion string) ResumableExecutor
	metrics          *HITLMetrics
	shutdown         chan struct{}
}

// NewResumptionWorker creates and initializes a new worker.
func NewResumptionWorker(
	queue ResumptionQueue,
	checkpointStore CheckpointStore,
	idempotencyStore IdempotencyStore,
	graphRegistry GraphRegistry,
	executorFactory func(graphID, graphVersion string) ResumableExecutor,
	metrics *HITLMetrics,
) *ResumptionWorker {
	return &ResumptionWorker{
		queue:            queue,
		checkpointStore:  checkpointStore,
		idempotencyStore: idempotencyStore,
		graphRegistry:    graphRegistry,
		executorFactory:  executorFactory,
		metrics:          metrics,
	}
}

// Start launches the worker loop in a new goroutine.
func (w *ResumptionWorker) Start(ctx context.Context, numWorkers int) {
	w.shutdown = make(chan struct{})
	for i := 0; i < numWorkers; i++ {
		go w.work(ctx)
	}
	log.Printf("started %d resumption workers", numWorkers)
}

// Stop gracefully shuts down the worker.
func (w *ResumptionWorker) Stop() {
	close(w.shutdown)
	log.Println("shutting down resumption workers")
}

func (w *ResumptionWorker) work(ctx context.Context) {
	for {
		select {
		case <-w.shutdown:
			return
		case <-ctx.Done():
			return
		default:
			job, err := w.queue.Dequeue(ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				log.Printf("error dequeuing resumption job: %v", err)
				time.Sleep(1 * time.Second) // Avoid fast spin on persistent error
				continue
			}
			if err := w.processJob(ctx, job); err != nil {
				log.Printf("failed to process job for request ID %s: %v", job.RequestID, err)
			}
		}
	}
}

// processJob contains the core logic for resuming a workflow.
func (w *ResumptionWorker) processJob(ctx context.Context, job *ResumptionJob) error {
	// 1. Load checkpoint
	cp, err := w.checkpointStore.GetByRequestID(job.RequestID)
	if err != nil {
		if errors.Is(err, ErrCheckpointNotFound) {
			// This can happen if the job was already processed but the ACK to the queue failed.
			// The processing lock will be gone, but the final idempotency key should exist.
			log.Printf("checkpoint not found for request %s, likely already processed.", job.RequestID)
			w.queue.Ack(ctx, job.JobID)
			return nil // Acknowledge message from queue
		}
		// A different error might be transient. We can't proceed without the checkpoint.
		return fmt.Errorf("could not load checkpoint: %w", err) // Job will be re-queued by broker
	}

	// 2. VALIDATION: Verify graph version compatibility
	currentGraph, err := w.graphRegistry.GetGraph(cp.GraphID)
	if err != nil {
		w.moveToDLQ(cp, fmt.Sprintf("failed to get current graph definition: %v", err))
		w.queue.Ack(ctx, job.JobID)
		return nil
	}
	if currentGraph.Version != cp.GraphVersion {
		err := fmt.Errorf("%w: checkpoint=%s, current=%s", ErrGraphVersionMismatch, cp.GraphVersion, currentGraph.Version)
		w.metrics.IncGraphVersionMismatches()
		w.moveToDLQ(cp, err.Error())
		w.queue.Ack(ctx, job.JobID)
		return nil // Permanent failure, ACK message
	}

	// 3. Deserialize state and inject response
	state, err := DeserializeState(cp.StateData)
	if err != nil {
		w.moveToDLQ(cp, fmt.Sprintf("state deserialization failed: %v", err))
		w.queue.Ack(ctx, job.JobID)
		return nil
	}
	respData, err := json.Marshal(job.Response)
	if err != nil {
		w.moveToDLQ(cp, fmt.Sprintf("response serialization failed: %v", err))
		w.queue.Ack(ctx, job.JobID)
		return nil
	}
	pendingKey := fmt.Sprintf(StateKeyHumanPendingFmt, cp.NodeID)
	state.Set(pendingKey, respData)

	// 4. Mark as processed BEFORE execution.
	// This is the final idempotency key that prevents any reprocessing.
	finalIdempotencyKey := fmt.Sprintf("callback-done:%s", job.RequestID)
	if err := w.idempotencyStore.Set(finalIdempotencyKey, 24*time.Hour); err != nil {
		return fmt.Errorf("failed to set final idempotency key: %w", err)
	}

	// 5. Resume execution
	executor := w.executorFactory(cp.GraphID, cp.GraphVersion)
	if err := executor.ResumeFromNode(cp.NodeID, state); err != nil {
		w.metrics.IncResumeFailures()
		// Check if error is transient or permanent
		if isRetryable(err) && cp.FailureCount < MaxResumptionAttempts {
			if _, recErr := w.checkpointStore.RecordFailure(job.RequestID, err); recErr != nil {
				log.Printf("CRITICAL: failed to record resumption failure for %s: %v", job.RequestID, recErr)
			}
			return fmt.Errorf("resumption failed with retryable error: %w", err) // Re-queue job
		}
		// Permanent failure or max retries exceeded
		w.moveToDLQ(cp, fmt.Sprintf("resumption failed permanently: %v", err))
		w.queue.Ack(ctx, job.JobID)
		return nil
	}

	// 6. Success: Clean up checkpoint
	if err := w.checkpointStore.Delete(job.RequestID); err != nil {
		log.Printf("WARN: failed to delete processed checkpoint %s: %v", job.RequestID, err)
	}

	// 7. Clean up the processing lock explicitly
	processingKey := fmt.Sprintf("processing:%s", job.RequestID)
	w.idempotencyStore.Delete(processingKey)

	w.metrics.IncCallbacksSuccessful()
	// Acknowledge successful processing
	if err := w.queue.Ack(ctx, job.JobID); err != nil {
		log.Printf("WARN: failed to ack job %s: %v", job.JobID, err)
	}
	return nil
}

func (w *ResumptionWorker) moveToDLQ(cp *ExecutionCheckpoint, reason string) {
	log.Printf("moving checkpoint %s to DLQ. Reason: %s", cp.RequestID, reason)
	if err := w.checkpointStore.MoveToCheckpointDLQ(cp.RequestID, reason); err != nil {
		log.Printf("CRITICAL: failed to move checkpoint %s to DLQ: %v", cp.RequestID, err)
	}
}

func isRetryable(err error) bool {
	// Logic to determine if a graph execution error is transient (e.g., database timeout)
	// vs permanent (e.g., invalid state).
	// This is a simplified example - implement based on your specific error types
	if err == nil {
		return false
	}

	errorMsg := err.Error()
	return strings.Contains(errorMsg, "timeout") ||
		strings.Contains(errorMsg, "connection refused") ||
		strings.Contains(errorMsg, "database is locked")
}
