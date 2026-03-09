package hitl

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/seyi/dagens/pkg/telemetry"
)

// CallbackHandler validates incoming human responses and enqueues them for processing.
type CallbackHandler struct {
	queue            ResumptionQueue
	idempotencyStore IdempotencyStore
	graphRegistry    GraphRegistry
	callbackSecret   []byte
	metrics          *HITLMetrics
}

// NewCallbackHandler creates a new, lightweight callback handler.
func NewCallbackHandler(
	queue ResumptionQueue,
	idempotencyStore IdempotencyStore,
	graphRegistry GraphRegistry,
	callbackSecret []byte,
	metrics *HITLMetrics,
) *CallbackHandler {
	return &CallbackHandler{
		queue:            queue,
		idempotencyStore: idempotencyStore,
		graphRegistry:    graphRegistry,
		callbackSecret:   callbackSecret,
		metrics:          metrics,
	}
}

// HandleHumanCallback is the HTTP handler for human responses. It validates,
// acquires a lock, enqueues the job, and returns immediately.
func (h *CallbackHandler) HandleHumanCallback(
	w http.ResponseWriter,
	r *http.Request,
	requestID string,
	timestamp int64,
	signature string,
	response *HumanResponse,
) {
	start := time.Now()
	defer h.metrics.ObserveCallbackLatency(time.Since(start))

	tracer := telemetry.GetGlobalTelemetry().GetTracer()
	ctx, span := tracer.StartSpan(r.Context(), "hitl.callback.handle")
	defer span.End()
	logger := hitlLogger()

	traceID := span.TraceID()
	spanID := span.SpanID()
	traceParent := r.Header.Get("traceparent")

	h.metrics.IncCallbacksReceived()
	if timestamp > 0 {
		age := time.Since(time.Unix(timestamp, 0))
		if age > 0 {
			h.metrics.ObserveHumanResponseTime(age)
		}
	}

	// 1. SECURITY: Verify HMAC signature
	expectedPayload := fmt.Sprintf("%s:%d", requestID, timestamp)
	if !h.verifyHMAC(expectedPayload, signature) {
		h.metrics.IncCallbackSecurityFailures()
		logger.Warn("hitl callback rejected: invalid signature", safeLogFields(map[string]interface{}{
			"operation":  "callback.verify_hmac",
			"request_id": requestID,
			"trace_id":   traceID,
			"span_id":    spanID,
		}))
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	// 2. SECURITY: Check timestamp freshness (prevent replay attacks)
	if time.Now().Unix()-timestamp > 300 { // 5 minute window
		h.metrics.IncCallbackSecurityFailures()
		logger.Warn("hitl callback rejected: stale timestamp", safeLogFields(map[string]interface{}{
			"operation":  "callback.verify_timestamp",
			"request_id": requestID,
			"timestamp":  timestamp,
			"trace_id":   traceID,
			"span_id":    spanID,
		}))
		http.Error(w, "callback timestamp too old", http.StatusForbidden)
		return
	}

	// 3. IDEMPOTENCY: Check if already fully processed.
	finalIdempotencyKey := fmt.Sprintf("callback-done:%s", requestID)
	if processed, _ := h.idempotencyStore.Exists(finalIdempotencyKey); processed {
		h.metrics.IncCallbackDuplicates()
		logger.Info("hitl callback duplicate: already completed", safeLogFields(map[string]interface{}{
			"operation":  "callback.idempotency.completed",
			"request_id": requestID,
			"trace_id":   traceID,
			"span_id":    spanID,
		}))
		w.WriteHeader(http.StatusOK) // Already handled, return success.
		return
	}

	// 4. IDEMPOTENCY: Attempt to acquire a short-lived processing lock.
	processingKey := fmt.Sprintf("processing:%s", requestID)
	wasSet, err := h.idempotencyStore.SetNX(processingKey, ProcessingLockTTL)
	if err != nil {
		logger.Warn("hitl callback idempotency lock failure", safeLogFields(map[string]interface{}{
			"operation":  "callback.idempotency.lock",
			"request_id": requestID,
			"error":      err.Error(),
			"trace_id":   traceID,
			"span_id":    spanID,
		}))
		http.Error(w, "internal server error during idempotency check", http.StatusInternalServerError)
		return
	}
	if !wasSet {
		// Another handler is already processing this request. This is expected during retries.
		h.metrics.IncCallbackDuplicates()
		logger.Info("hitl callback duplicate: in-flight processing", safeLogFields(map[string]interface{}{
			"operation":  "callback.idempotency.in_flight",
			"request_id": requestID,
			"trace_id":   traceID,
			"span_id":    spanID,
		}))
		w.WriteHeader(http.StatusAccepted) // Acknowledge that it's being processed.
		return
	}

	// 5. Enqueue for async resumption
	job := &ResumptionJob{
		RequestID:   requestID,
		Timestamp:   timestamp,
		Signature:   signature,
		TraceID:     traceID,
		TraceParent: traceParent,
		Response:    response,
	}

	if err := h.queue.Enqueue(ctx, job); err != nil {
		// If enqueuing fails, we must release the lock so another attempt can succeed.
		h.idempotencyStore.Delete(processingKey)
		logger.Warn("hitl callback enqueue failure", safeLogFields(map[string]interface{}{
			"operation":  "callback.enqueue",
			"request_id": requestID,
			"error":      err.Error(),
			"trace_id":   traceID,
			"span_id":    spanID,
		}))
		if errors.Is(err, ErrServiceOverloaded) {
			http.Error(w, "service overloaded: queue full", http.StatusServiceUnavailable)
		} else {
			http.Error(w, "internal server error: failed to enqueue job", http.StatusInternalServerError)
		}
		return
	}

	h.metrics.IncCallbacksEnqueued()
	reconcileQueueLengthMetric(ctx, h.queue, h.metrics)
	logger.Info("hitl callback accepted and enqueued", safeLogFields(map[string]interface{}{
		"operation":  "callback.enqueue",
		"request_id": requestID,
		"job_id":     job.JobID,
		"trace_id":   traceID,
		"span_id":    spanID,
	}))

	// 6. Return success to the client immediately.
	w.WriteHeader(http.StatusAccepted)
}

func reconcileQueueLengthMetric(ctx context.Context, queue ResumptionQueue, metrics *HITLMetrics) {
	if provider, ok := queue.(interface {
		QueueLength(context.Context) (int64, error)
	}); ok {
		if qlen, err := provider.QueueLength(ctx); err == nil {
			metrics.SetResumptionQueueLength(float64(qlen))
		}
	}
}

func (h *CallbackHandler) verifyHMAC(payload, signature string) bool {
	expected := hmac.New(sha256.New, h.callbackSecret)
	expected.Write([]byte(payload))
	expectedHex := hex.EncodeToString(expected.Sum(nil))
	return hmac.Equal([]byte(expectedHex), []byte(signature))
}
