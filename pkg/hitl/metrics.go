package hitl

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
	ResumptionWorkerBusy  gauge   // NEW: Track worker utilization
	ResumptionQueueLength gauge   // NEW: Track queue length
	ResumptionRetries     counter // NEW: Track retry attempts

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

func (m *HITLMetrics) IncCheckpointsMovedToDLQ() {
	if m != nil && m.CheckpointsMovedToDLQ != nil {
		m.CheckpointsMovedToDLQ.Inc()
	}
}
