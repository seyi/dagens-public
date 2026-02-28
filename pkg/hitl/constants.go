package hitl

import (
	"errors"
	"time"
)

// System state key prefixes - all underscore-prefixed keys are reserved
const (
	StateKeyHumanPendingFmt = "_hitl_pending:%s" // Format: node ID
	StateKeyHumanRequestID  = "_hitl_request_id" // Internal use only
	StateKeyHumanTimeout    = "_hitl_timeout"    // Internal use only
	StateKeyReservedPrefix  = "_"                // All _ keys are system-reserved
)

// System-wide timeout and retry constants
const (
	ProcessingLockTTL     = 5 * time.Minute // Lock duration for callback processing
	MaxResumptionAttempts = 3                // Max retry attempts for resumption
)

// Errors
var (
	ErrHumanInteractionPending = errors.New("human interaction pending: checkpoint required")
	ErrHumanTimeout            = errors.New("human response timeout exceeded")
	ErrCheckpointNotFound      = errors.New("checkpoint not found")
	ErrInvalidSignature        = errors.New("invalid callback signature")
	ErrGraphVersionMismatch    = errors.New("graph version mismatch: cannot resume")
	ErrServiceOverloaded       = errors.New("resumption queue is full")
)
