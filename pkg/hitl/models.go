package hitl

import "time"

// ExecutionCheckpoint holds the state of a paused graph execution.
type ExecutionCheckpoint struct {
	GraphID      string    `json:"graph_id"`
	GraphVersion string    `json:"graph_version"`
	NodeID       string    `json:"node_id"`
	StateData    []byte    `json:"state_data"`
	RequestID    string    `json:"request_id"`
	JobID        string    `json:"job_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	TraceID      string    `json:"trace_id,omitempty"`
	FailureCount int       `json:"failure_count"`
	LastError    string    `json:"last_error"`
	LastAttempt  time.Time `json:"last_attempt"`
}

// HumanResponse represents the data received from the human-in-the-loop callback.
type HumanResponse struct {
	SelectedOption string                 `json:"selected_option"`
	FreeformText   string                 `json:"freeform_text"`
	Payload        map[string]interface{} `json:"payload"`
	Timestamp      time.Time              `json:"timestamp"`
}

// ResumptionJob defines the payload that is enqueued for asynchronous processing
// by a ResumptionWorker.
type ResumptionJob struct {
	JobID       string
	RequestID   string
	Timestamp   int64
	Signature   string
	TraceID     string
	TraceParent string
	Response    *HumanResponse
}
