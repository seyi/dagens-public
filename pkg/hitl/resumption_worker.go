package hitl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/telemetry"
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
	cancel           context.CancelFunc
	stopOnce         sync.Once
	wg               sync.WaitGroup
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
	workerCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	w.stopOnce = sync.Once{}
	logger := hitlLogger()
	for i := 0; i < numWorkers; i++ {
		w.wg.Add(1)
		go w.work(workerCtx)
	}
	logger.Info("started hitl resumption workers", safeLogFields(map[string]interface{}{
		"operation":   "worker.start",
		"num_workers": numWorkers,
	}))
}

// Stop gracefully shuts down the worker.
func (w *ResumptionWorker) Stop() {
	w.stopOnce.Do(func() {
		if w.cancel != nil {
			w.cancel()
		}
		if w.shutdown != nil {
			close(w.shutdown)
		}
	})
	w.wg.Wait()
	hitlLogger().Info("shutting down hitl resumption workers", safeLogFields(map[string]interface{}{
		"operation": "worker.stop",
	}))
}

func (w *ResumptionWorker) work(ctx context.Context) {
	defer w.wg.Done()
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
				w.metrics.IncResumptionDequeueErrors()
				hitlLogger().Warn("error dequeuing hitl resumption job", safeLogFields(map[string]interface{}{
					"operation": "worker.dequeue",
					"error":     err.Error(),
				}))
				time.Sleep(1 * time.Second) // Avoid fast spin on persistent error
				continue
			}
			reconcileQueueLengthMetric(ctx, w.queue, w.metrics)
			if err := w.processJob(ctx, job); err != nil {
				hitlLogger().Warn("failed to process hitl job", safeLogFields(map[string]interface{}{
					"operation":  "worker.process_job",
					"request_id": job.RequestID,
					"job_id":     job.JobID,
					"trace_id":   job.TraceID,
					"error":      err.Error(),
				}))
			}
		}
	}
}

// processJob contains the core logic for resuming a workflow.
func (w *ResumptionWorker) processJob(ctx context.Context, job *ResumptionJob) error {
	tracer := telemetry.GetGlobalTelemetry().GetTracer()
	spanCtx, span := tracer.StartSpan(ctx, "hitl.worker.process_job")
	defer span.End()
	traceID := span.TraceID()

	if job.TraceID == "" {
		job.TraceID = traceID
	}
	if job.TraceParent != "" {
		span.SetAttribute("traceparent", job.TraceParent)
	}
	span.SetAttribute("request_id", job.RequestID)
	span.SetAttribute("job_id", job.JobID)

	w.metrics.IncResumptionWorkerBusy()
	defer w.metrics.DecResumptionWorkerBusy()
	logger := hitlLogger()

	// 1. Load checkpoint
	loadCtx, loadSpan := tracer.StartSpan(spanCtx, "hitl.checkpoint_store.get")
	loadSpan.SetAttribute("request_id", job.RequestID)
	cp, err := w.checkpointStore.GetByRequestID(job.RequestID)
	if err != nil {
		loadSpan.SetStatus(telemetry.StatusError, err.Error())
	} else {
		loadSpan.SetStatus(telemetry.StatusOK, "checkpoint loaded")
	}
	loadSpan.End()
	_ = loadCtx
	if err != nil {
		if errors.Is(err, ErrCheckpointNotFound) {
			// This can happen if the job was already processed but the ACK to the queue failed.
			// The processing lock will be gone, but the final idempotency key should exist.
			// We intentionally do not decrement ActiveWaitingWorkflows here because this path can
			// represent an already-terminal workflow where the decrement was already applied.
			logger.Info("checkpoint not found during resume; likely already processed", safeLogFields(map[string]interface{}{
				"operation":  "worker.load_checkpoint",
				"request_id": job.RequestID,
				"job_id":     job.JobID,
				"trace_id":   traceID,
			}))
			w.queue.Ack(spanCtx, job.JobID)
			return nil // Acknowledge message from queue
		}
		// A different error might be transient. We can't proceed without the checkpoint.
		return fmt.Errorf("could not load checkpoint: %w", err) // Job will be re-queued by broker
	}

	// 2. VALIDATION: Verify graph version compatibility
	currentGraph, err := w.graphRegistry.GetGraph(cp.GraphID)
	if err != nil {
		w.moveToDLQ(spanCtx, tracer, cp, fmt.Sprintf("failed to get current graph definition: %v", err), traceID)
		w.queue.Ack(spanCtx, job.JobID)
		return nil
	}
	if currentGraph.Version != cp.GraphVersion {
		err := fmt.Errorf("%w: checkpoint=%s, current=%s", ErrGraphVersionMismatch, cp.GraphVersion, currentGraph.Version)
		w.metrics.IncGraphVersionMismatches()
		logger.Warn("hitl graph version mismatch during resume", safeLogFields(map[string]interface{}{
			"operation":          "worker.validate_graph",
			"request_id":         job.RequestID,
			"job_id":             job.JobID,
			"checkpoint_version": cp.GraphVersion,
			"current_version":    currentGraph.Version,
			"trace_id":           traceID,
		}))
		w.moveToDLQ(spanCtx, tracer, cp, err.Error(), traceID)
		w.queue.Ack(spanCtx, job.JobID)
		return nil // Permanent failure, ACK message
	}

	// 3. Deserialize state and inject response
	state, err := DeserializeState(cp.StateData)
	if err != nil {
		w.metrics.IncStateSerializationFailures()
		w.moveToDLQ(spanCtx, tracer, cp, fmt.Sprintf("state deserialization failed: %v", err), traceID)
		w.queue.Ack(spanCtx, job.JobID)
		return nil
	}
	respData, err := json.Marshal(job.Response)
	if err != nil {
		w.metrics.IncStateSerializationFailures()
		w.moveToDLQ(spanCtx, tracer, cp, fmt.Sprintf("response serialization failed: %v", err), traceID)
		w.queue.Ack(spanCtx, job.JobID)
		return nil
	}
	pendingKey := fmt.Sprintf(StateKeyHumanPendingFmt, cp.NodeID)
	state.Set(pendingKey, respData)

	// 4. Mark as processed BEFORE execution.
	// This is the final idempotency key that prevents any reprocessing.
	finalIdempotencyKey := fmt.Sprintf("callback-done:%s", job.RequestID)
	idemCtx, idemSpan := tracer.StartSpan(spanCtx, "hitl.idempotency_store.set")
	idemSpan.SetAttribute("idempotency_key", finalIdempotencyKey)
	if err := w.idempotencyStore.Set(finalIdempotencyKey, 24*time.Hour); err != nil {
		idemSpan.SetStatus(telemetry.StatusError, err.Error())
		idemSpan.End()
		return fmt.Errorf("failed to set final idempotency key: %w", err)
	}
	idemSpan.SetStatus(telemetry.StatusOK, "idempotency set")
	idemSpan.End()
	_ = idemCtx

	// 5. Resume execution (graph-native when executable graph registry is available)
	result, err := resumeFromCheckpoint(spanCtx, cp, state, w.graphRegistry, w.executorFactory)
	if err != nil {
		w.metrics.IncResumeFailures()
		// Check if error is transient or permanent
		if isRetryable(err) && cp.FailureCount < MaxResumptionAttempts {
			// Do not decrement ActiveWaitingWorkflows on retryable failures:
			// the workflow remains pending and will be retried.
			w.metrics.IncResumptionRetries()
			recordCtx, recordSpan := tracer.StartSpan(spanCtx, "hitl.checkpoint_store.record_failure")
			recordSpan.SetAttribute("request_id", job.RequestID)
			_, recErr := w.checkpointStore.RecordFailure(job.RequestID, err)
			if recErr != nil {
				recordSpan.SetStatus(telemetry.StatusError, recErr.Error())
				logger.Error("failed to record resumption failure", safeLogFields(map[string]interface{}{
					"operation":  "worker.record_failure",
					"request_id": job.RequestID,
					"job_id":     job.JobID,
					"trace_id":   traceID,
					"error":      recErr.Error(),
				}))
			} else {
				recordSpan.SetStatus(telemetry.StatusOK, "failure recorded")
			}
			recordSpan.End()
			_ = recordCtx
			return fmt.Errorf("resumption failed with retryable error: %w", err) // Re-queue job
		}
		// Permanent failure or max retries exceeded
		w.moveToDLQ(spanCtx, tracer, cp, fmt.Sprintf("resumption failed permanently: %v", err), traceID)
		w.queue.Ack(spanCtx, job.JobID)
		return nil
	}

	if result != nil && result.IsPaused() {
		nextCP, persistErr := persistNestedPauseCheckpoint(spanCtx, w.checkpointStore, cp, state, result.Paused)
		if persistErr != nil {
			w.metrics.IncResumeFailures()
			w.moveToDLQ(spanCtx, tracer, cp, fmt.Sprintf("failed to persist nested pause checkpoint: %v", persistErr), traceID)
			w.queue.Ack(spanCtx, job.JobID)
			return nil
		}

		w.metrics.IncCheckpointCreations()
		w.metrics.ObserveCheckpointSizeBytes(len(nextCP.StateData))
		logger.Info("hitl resume yielded nested pause checkpoint", safeLogFields(map[string]interface{}{
			"operation":        "worker.resume.nested_pause",
			"request_id":       job.RequestID,
			"job_id":           job.JobID,
			"next_request_id":  nextCP.RequestID,
			"next_node_id":     nextCP.NodeID,
			"checkpoint_graph": nextCP.GraphID,
			"trace_id":         traceID,
		}))

		w.metrics.IncCallbacksSuccessful()
		if err := w.queue.Ack(spanCtx, job.JobID); err != nil {
			logger.Warn("hitl nested pause ack failed", safeLogFields(map[string]interface{}{
				"operation":  "worker.ack.nested_pause",
				"job_id":     job.JobID,
				"request_id": job.RequestID,
				"trace_id":   traceID,
				"error":      err.Error(),
			}))
		}
		return nil
	}

	// 6. Success: Clean up checkpoint
	deleteCtx, deleteSpan := tracer.StartSpan(spanCtx, "hitl.checkpoint_store.delete")
	deleteSpan.SetAttribute("request_id", job.RequestID)
	if err := w.checkpointStore.Delete(job.RequestID); err != nil {
		deleteSpan.SetStatus(telemetry.StatusError, err.Error())
		logger.Warn("failed to delete processed checkpoint", safeLogFields(map[string]interface{}{
			"operation":  "worker.cleanup_checkpoint",
			"request_id": job.RequestID,
			"job_id":     job.JobID,
			"trace_id":   traceID,
			"error":      err.Error(),
		}))
	} else {
		deleteSpan.SetStatus(telemetry.StatusOK, "checkpoint deleted")
	}
	deleteSpan.End()
	_ = deleteCtx
	w.metrics.DecActiveWaitingWorkflows()

	// 7. Clean up the processing lock explicitly
	processingKey := fmt.Sprintf("processing:%s", job.RequestID)
	w.idempotencyStore.Delete(processingKey)

	w.metrics.IncCallbacksSuccessful()
	// Acknowledge successful processing
	if err := w.queue.Ack(spanCtx, job.JobID); err != nil {
		logger.Warn("hitl resume ack failed", safeLogFields(map[string]interface{}{
			"operation":  "worker.ack",
			"job_id":     job.JobID,
			"request_id": job.RequestID,
			"trace_id":   traceID,
			"error":      err.Error(),
		}))
	}
	return nil
}

func (w *ResumptionWorker) moveToDLQ(ctx context.Context, tracer telemetry.Tracer, cp *ExecutionCheckpoint, reason string, traceID string) {
	dlqCtx, dlqSpan := tracer.StartSpan(ctx, "hitl.checkpoint_store.move_to_dlq")
	dlqSpan.SetAttribute("request_id", cp.RequestID)
	dlqSpan.SetAttribute("reason", reason)
	logger := hitlLogger()
	logger.Warn("moving checkpoint to hitl DLQ", safeLogFields(map[string]interface{}{
		"operation":  "worker.move_to_dlq",
		"request_id": cp.RequestID,
		"reason":     reason,
		"trace_id":   traceID,
	}))
	if err := w.checkpointStore.MoveToCheckpointDLQ(cp.RequestID, reason); err != nil {
		dlqSpan.SetStatus(telemetry.StatusError, err.Error())
		dlqSpan.End()
		logger.Error("failed to move checkpoint to hitl DLQ", safeLogFields(map[string]interface{}{
			"operation":  "worker.move_to_dlq",
			"request_id": cp.RequestID,
			"trace_id":   traceID,
			"error":      err.Error(),
		}))
		return
	}
	dlqSpan.SetStatus(telemetry.StatusOK, "checkpoint moved to dlq")
	dlqSpan.End()
	_ = dlqCtx
	w.metrics.IncCheckpointsMovedToDLQ()
	w.metrics.IncDLQSize()
	w.metrics.DecActiveWaitingWorkflows()
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
