package hitl

import (
	"sync/atomic"
	"time"
)

// HITLMetrics defines the metrics for the Human-in-the-Loop system.
type HITLMetrics struct {
	// Checkpoint operations
	CheckpointCreations       counter
	CheckpointSizeBytes       histogram
	CheckpointCreationLatency histogram

	// Human response tracking
	HumanResponseTime      histogram // Time from request to callback
	ActiveWaitingWorkflows gauge     // Current paused workflows
	BlockingWaitsActive    gauge     // Current goroutines waiting

	// Callback handler
	CallbacksReceived        counter
	CallbacksSuccessful      counter
	CallbacksEnqueued        counter // NEW: Track enqueuing success
	CallbackDuplicates       counter
	CallbackSecurityFailures counter
	CallbackLatency          histogram

	// Resumption worker metrics
	ResumptionWorkerBusy    gauge   // NEW: Track worker utilization
	ResumptionQueueLength   gauge   // NEW: Track queue length
	ResumptionRetries       counter // NEW: Track retry attempts
	ResumptionDequeueErrors counter

	// Failures
	ResumeFailures             counter
	GraphVersionMismatches     counter
	StateSerializationFailures counter
	CheckpointOrphans          gauge

	// NEW: DLQ and failure handling metrics
	CheckpointsMovedToDLQ counter
	DLQSize               gauge

	// Resource usage
	CheckpointStorageMB  gauge
	IdempotencyStoreSize gauge

	activeWaitingValue atomic.Int64
	dlqSizeValue       atomic.Int64
}

// Counter interface for counting metrics
type counter interface {
	Inc()
	Add(float64)
}

// Histogram interface for histogram metrics
type histogram interface {
	Observe(float64)
}

// Gauge interface for gauge metrics
type gauge interface {
	Set(float64)
	Inc()
	Dec()
	Add(float64)
}

// Helper methods for metrics
func (m *HITLMetrics) IncCallbacksReceived() {
	if m != nil && m.CallbacksReceived != nil {
		m.CallbacksReceived.Inc()
	}
}

func (m *HITLMetrics) IncCallbacksSuccessful() {
	if m != nil && m.CallbacksSuccessful != nil {
		m.CallbacksSuccessful.Inc()
	}
}

func (m *HITLMetrics) IncCallbacksEnqueued() {
	if m != nil && m.CallbacksEnqueued != nil {
		m.CallbacksEnqueued.Inc()
	}
}

func (m *HITLMetrics) IncCallbackDuplicates() {
	if m != nil && m.CallbackDuplicates != nil {
		m.CallbackDuplicates.Inc()
	}
}

func (m *HITLMetrics) IncCallbackSecurityFailures() {
	if m != nil && m.CallbackSecurityFailures != nil {
		m.CallbackSecurityFailures.Inc()
	}
}

func (m *HITLMetrics) IncResumeFailures() {
	if m != nil && m.ResumeFailures != nil {
		m.ResumeFailures.Inc()
	}
}

func (m *HITLMetrics) IncGraphVersionMismatches() {
	if m != nil && m.GraphVersionMismatches != nil {
		m.GraphVersionMismatches.Inc()
	}
}

func (m *HITLMetrics) IncResumptionRetries() {
	if m != nil && m.ResumptionRetries != nil {
		m.ResumptionRetries.Inc()
	}
}

func (m *HITLMetrics) IncResumptionDequeueErrors() {
	if m != nil && m.ResumptionDequeueErrors != nil {
		m.ResumptionDequeueErrors.Inc()
	}
}

func (m *HITLMetrics) IncCheckpointsMovedToDLQ() {
	if m != nil && m.CheckpointsMovedToDLQ != nil {
		m.CheckpointsMovedToDLQ.Inc()
	}
}

func (m *HITLMetrics) ObserveCallbackLatency(d time.Duration) {
	if m != nil && m.CallbackLatency != nil {
		m.CallbackLatency.Observe(d.Seconds())
	}
}

func (m *HITLMetrics) IncResumptionWorkerBusy() {
	if m != nil && m.ResumptionWorkerBusy != nil {
		m.ResumptionWorkerBusy.Inc()
	}
}

func (m *HITLMetrics) DecResumptionWorkerBusy() {
	if m != nil && m.ResumptionWorkerBusy != nil {
		m.ResumptionWorkerBusy.Dec()
	}
}

func (m *HITLMetrics) IncCheckpointCreations() {
	if m != nil && m.CheckpointCreations != nil {
		m.CheckpointCreations.Inc()
	}
}

func (m *HITLMetrics) ObserveCheckpointCreationLatency(d time.Duration) {
	if m != nil && m.CheckpointCreationLatency != nil {
		m.CheckpointCreationLatency.Observe(d.Seconds())
	}
}

func (m *HITLMetrics) ObserveCheckpointSizeBytes(size int) {
	if m != nil && m.CheckpointSizeBytes != nil {
		m.CheckpointSizeBytes.Observe(float64(size))
	}
}

func (m *HITLMetrics) ObserveHumanResponseTime(d time.Duration) {
	if m != nil && m.HumanResponseTime != nil {
		m.HumanResponseTime.Observe(d.Seconds())
	}
}

func (m *HITLMetrics) IncActiveWaitingWorkflows() {
	if m == nil {
		return
	}
	v := m.activeWaitingValue.Add(1)
	if m.ActiveWaitingWorkflows != nil {
		m.ActiveWaitingWorkflows.Set(float64(v))
	}
}

func (m *HITLMetrics) DecActiveWaitingWorkflows() {
	if m == nil {
		return
	}
	for {
		current := m.activeWaitingValue.Load()
		if current <= 0 {
			if m.ActiveWaitingWorkflows != nil {
				m.ActiveWaitingWorkflows.Set(0)
			}
			return
		}
		next := current - 1
		if m.activeWaitingValue.CompareAndSwap(current, next) {
			if m.ActiveWaitingWorkflows != nil {
				m.ActiveWaitingWorkflows.Set(float64(next))
			}
			return
		}
	}
}

func (m *HITLMetrics) IncBlockingWaitsActive() {
	if m != nil && m.BlockingWaitsActive != nil {
		m.BlockingWaitsActive.Inc()
	}
}

func (m *HITLMetrics) DecBlockingWaitsActive() {
	if m != nil && m.BlockingWaitsActive != nil {
		m.BlockingWaitsActive.Dec()
	}
}

func (m *HITLMetrics) SetResumptionQueueLength(v float64) {
	if m != nil && m.ResumptionQueueLength != nil {
		m.ResumptionQueueLength.Set(v)
	}
}

func (m *HITLMetrics) IncStateSerializationFailures() {
	if m != nil && m.StateSerializationFailures != nil {
		m.StateSerializationFailures.Inc()
	}
}

func (m *HITLMetrics) IncDLQSize() {
	if m == nil {
		return
	}
	v := m.dlqSizeValue.Add(1)
	if m.DLQSize != nil {
		m.DLQSize.Set(float64(v))
	}
}

func (m *HITLMetrics) DecDLQSize() {
	if m == nil {
		return
	}
	for {
		current := m.dlqSizeValue.Load()
		if current <= 0 {
			if m.DLQSize != nil {
				m.DLQSize.Set(0)
			}
			return
		}
		next := current - 1
		if m.dlqSizeValue.CompareAndSwap(current, next) {
			if m.DLQSize != nil {
				m.DLQSize.Set(float64(next))
			}
			return
		}
	}
}

func (m *HITLMetrics) SetDLQSize(v float64) {
	if m != nil && m.DLQSize != nil {
		m.DLQSize.Set(v)
	}
	if m != nil {
		if v <= 0 {
			m.dlqSizeValue.Store(0)
		} else {
			m.dlqSizeValue.Store(int64(v))
		}
	}
}
