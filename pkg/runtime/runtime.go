// Copyright 2025 Apache Spark AI Agents
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package runtime provides ADK-inspired runtime orchestration for distributed agent execution.
//
// This package implements ADK's Runtime/Runner pattern while preserving Spark's
// distributed computing model. Key concepts:
//
// - AgentRuntime: Orchestrates agent execution with event coordination
// - InvocationContext: Rich execution context with state management
// - Event: First-class events with pause/resume semantics
// - StreamingRuntime: Real-time event streaming for UX
//
// The runtime is designed to work within Spark tasks, providing intra-task
// orchestration while Spark handles inter-task coordination.
package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/agent"
)

// EventType classifies different kinds of events
type EventType int

const (
	EventTypeMessage      EventType = iota // LLM message
	EventTypeToolCall                      // Tool invocation
	EventTypeToolResult                    // Tool response
	EventTypeStateChange                   // State update
	EventTypeCheckpoint                    // Spark checkpoint point
	EventTypeError                         // Error occurred
)

func (t EventType) String() string {
	switch t {
	case EventTypeMessage:
		return "message"
	case EventTypeToolCall:
		return "tool_call"
	case EventTypeToolResult:
		return "tool_result"
	case EventTypeStateChange:
		return "state_change"
	case EventTypeCheckpoint:
		return "checkpoint"
	case EventTypeError:
		return "error"
	default:
		return "unknown"
	}
}

// Event represents an atomic state change or communication during execution.
// Inspired by ADK's event model with Spark-specific additions.
type Event struct {
	ID        string                 `json:"id"`
	Type      EventType              `json:"type"`
	Content   interface{}            `json:"content"`
	Partial   bool                   `json:"partial"`   // ADK: streaming vs committed
	Author    string                 `json:"author"`    // Agent that produced event
	Timestamp time.Time              `json:"timestamp"`

	// State changes (ADK pattern)
	StateDelta    map[string]interface{} `json:"state_delta,omitempty"`

	// Artifact changes (ADK pattern)
	ArtifactDelta *ArtifactDelta `json:"artifact_delta,omitempty"`

	// Spark-specific fields
	PartitionID string   `json:"partition_id,omitempty"`
	Lineage     []string `json:"lineage,omitempty"` // Parent event IDs (like RDD lineage)

	// Metadata
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// ArtifactDelta represents file/artifact changes
type ArtifactDelta struct {
	Created []string `json:"created,omitempty"`
	Updated []string `json:"updated,omitempty"`
	Deleted []string `json:"deleted,omitempty"`
}

// StateDelta represents a state change
type StateDelta struct {
	Key       string      `json:"key"`
	OldValue  interface{} `json:"old_value"`
	NewValue  interface{} `json:"new_value"`
	Timestamp time.Time   `json:"timestamp"`
}

// InvocationContext provides rich execution context for agents.
// Inspired by ADK's InvocationContext with Spark-aware extensions.
type InvocationContext struct {
	context.Context

	// Identity
	SessionID     string `json:"session_id"`
	InvocationID  string `json:"invocation_id"`
	UserID        string `json:"user_id,omitempty"`

	// Input
	Input *agent.AgentInput `json:"input"`

	// State management (ADK pattern)
	State       map[string]interface{} `json:"state"`        // Persistent state
	TempState   map[string]interface{} `json:"temp_state"`   // Temporary (task-local)
	StateDeltas []StateDelta           `json:"state_deltas"` // Uncommitted changes

	// Spark-specific
	PartitionID   string `json:"partition_id"`
	TaskAttemptID string `json:"task_attempt_id,omitempty"`

	// Execution tracking
	EventHistory []Event   `json:"event_history"`
	CurrentTurn  int       `json:"current_turn"`
	StartTime    time.Time `json:"start_time"`

	// ADK-compatible flow control
	AgentName     string `json:"agent_name,omitempty"`      // Current agent name (for billing/logging)
	EndInvocation bool   `json:"end_invocation,omitempty"` // Signal to end invocation early

	// LLM call tracking (ADK pattern)
	LLMCallCount int `json:"llm_call_count"` // Number of LLM calls in this invocation

	// CFC (Continuous Function Calling) support
	LiveRequestQueue interface{} `json:"-"` // *LiveRequestQueue when in CFC mode

	// Run configuration
	RunConfig interface{} `json:"-"` // *RunConfig for this invocation

	// Services (will be populated)
	Services *RuntimeServices `json:"-"`

	// Synchronization
	mu sync.RWMutex
}

// NewInvocationContext creates a new invocation context
func NewInvocationContext(ctx context.Context, input *agent.AgentInput) *InvocationContext {
	return &InvocationContext{
		Context:      ctx,
		SessionID:    generateID("session"),
		InvocationID: generateID("invocation"),
		Input:        input,
		State:        make(map[string]interface{}),
		TempState:    make(map[string]interface{}),
		StateDeltas:  make([]StateDelta, 0),
		EventHistory: make([]Event, 0),
		StartTime:    time.Now(),
	}
}

// Get retrieves a value from context state with dirty read support.
// Implements ADK's state access pattern:
// 1. Check temp state (prefixed with "temp:")
// 2. Check uncommitted deltas (dirty reads)
// 3. Check committed state
func (c *InvocationContext) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Check temporary state (task-local)
	if strings.HasPrefix(key, "temp:") {
		val, ok := c.TempState[key]
		return val, ok
	}

	// Check uncommitted deltas (dirty reads)
	for i := len(c.StateDeltas) - 1; i >= 0; i-- {
		if c.StateDeltas[i].Key == key {
			return c.StateDeltas[i].NewValue, true
		}
	}

	// Check committed state
	val, ok := c.State[key]
	return val, ok
}

// Set updates a value in context state.
// Temporary keys (prefixed "temp:") go to TempState.
// Other keys are recorded as uncommitted deltas.
func (c *InvocationContext) Set(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if strings.HasPrefix(key, "temp:") {
		c.TempState[key] = value
	} else {
		// Get old value
		oldValue, _ := c.State[key]

		// Record as uncommitted delta
		c.StateDeltas = append(c.StateDeltas, StateDelta{
			Key:       key,
			OldValue:  oldValue,
			NewValue:  value,
			Timestamp: time.Now(),
		})
	}
}

// CommitStateDeltas commits all pending state changes
func (c *InvocationContext) CommitStateDeltas() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, delta := range c.StateDeltas {
		c.State[delta.Key] = delta.NewValue
	}

	// Clear deltas after commit
	c.StateDeltas = make([]StateDelta, 0)
}

// ClearTempState removes all temporary state
func (c *InvocationContext) ClearTempState() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.TempState = make(map[string]interface{})
}

// IncrementLLMCallCount increments the LLM call counter and checks the limit.
// Returns an error if the limit is exceeded (ADK pattern).
func (c *InvocationContext) IncrementLLMCallCount() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.LLMCallCount++

	// Check limit from RunConfig if set
	if c.RunConfig != nil {
		if rc, ok := c.RunConfig.(*RunConfig); ok && rc.MaxLLMCalls > 0 {
			if c.LLMCallCount > rc.MaxLLMCalls {
				return fmt.Errorf("LLM call limit exceeded: %d > %d", c.LLMCallCount, rc.MaxLLMCalls)
			}
		}
	}
	return nil
}

// GetLLMCallCount returns the current LLM call count
func (c *InvocationContext) GetLLMCallCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.LLMCallCount
}

// AddEvent adds an event to the history
func (c *InvocationContext) AddEvent(event Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.EventHistory = append(c.EventHistory, event)
}

// RuntimeServices provides backend services for the runtime
type RuntimeServices struct {
	SessionService  SessionService
	ArtifactService ArtifactService
	MemoryService   MemoryService
}

// SessionService manages session persistence
type SessionService interface {
	GetSession(ctx context.Context, sessionID string) (*Session, error)
	UpdateSession(ctx context.Context, session *Session) error
	ListSessions(ctx context.Context, userID string) ([]*Session, error)
}

// ArtifactService manages artifact storage
type ArtifactService interface {
	SaveArtifact(ctx context.Context, sessionID, name string, data []byte) error
	GetArtifact(ctx context.Context, sessionID, name string) ([]byte, error)
	ListArtifacts(ctx context.Context, sessionID string) ([]string, error)
}

// MemoryService manages long-term memory
type MemoryService interface {
	Store(ctx context.Context, userID string, memory *Memory) error
	Query(ctx context.Context, userID string, query string) ([]*Memory, error)
}

// Session represents a user session
type Session struct {
	ID           string                 `json:"id"`
	UserID       string                 `json:"user_id"`
	State        map[string]interface{} `json:"state"`
	EventHistory []Event                `json:"event_history"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
}

// Memory represents a long-term memory entry
type Memory struct {
	ID        string                 `json:"id"`
	Content   string                 `json:"content"`
	Embedding []float64              `json:"embedding,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

// AgentRuntime orchestrates agent execution with event coordination.
// Inspired by ADK's Runner pattern, adapted for Spark distributed execution.
type AgentRuntime struct {
	agent       agent.Agent
	services    *RuntimeServices
	partitionID string

	// Configuration
	maxEvents      int
	enableMetrics  bool
	enableCheckpoint bool

	// State
	eventLog []Event
	mu       sync.RWMutex
}

// NewAgentRuntime creates a new agent runtime
func NewAgentRuntime(ag agent.Agent, services *RuntimeServices) *AgentRuntime {
	return &AgentRuntime{
		agent:            ag,
		services:         services,
		maxEvents:        1000,
		enableMetrics:    true,
		enableCheckpoint: true,
		eventLog:         make([]Event, 0),
	}
}

// WithPartitionID sets the Spark partition ID
func (r *AgentRuntime) WithPartitionID(id string) *AgentRuntime {
	r.partitionID = id
	return r
}

// WithMaxEvents sets maximum events to keep in memory
func (r *AgentRuntime) WithMaxEvents(max int) *AgentRuntime {
	r.maxEvents = max
	return r
}

// Run executes the agent with event coordination.
// This is the core orchestration method, implementing ADK's execution model:
// 1. Initialize invocation context
// 2. Execute agent
// 3. Process events (pause points)
// 4. Commit state changes
// 5. Continue until completion
func (r *AgentRuntime) Run(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// Create invocation context
	invCtx := NewInvocationContext(ctx, input)
	invCtx.PartitionID = r.partitionID
	invCtx.Services = r.services

	// Execute agent
	output, err := r.agent.Execute(invCtx, input)
	if err != nil {
		return nil, fmt.Errorf("agent execution failed: %w", err)
	}

	// Process any state deltas
	invCtx.CommitStateDeltas()

	// Clean up temporary state
	invCtx.ClearTempState()

	// Copy events from InvocationContext to runtime log
	r.mu.Lock()
	for _, event := range invCtx.EventHistory {
		r.eventLog = append(r.eventLog, event)

		// Trim if too many events
		if len(r.eventLog) > r.maxEvents {
			r.eventLog = r.eventLog[len(r.eventLog)-r.maxEvents:]
		}
	}
	r.mu.Unlock()

	// Record final state in output
	if output.Metadata == nil {
		output.Metadata = make(map[string]interface{})
	}
	output.Metadata["invocation_context"] = invCtx
	output.Metadata["event_count"] = len(invCtx.EventHistory)
	output.Metadata["state_delta_count"] = len(invCtx.StateDeltas)

	return output, nil
}

// processEvent processes a single event (pause point in execution)
func (r *AgentRuntime) processEvent(ctx *InvocationContext, event Event) error {
	// Add to context history
	ctx.AddEvent(event)

	// Add to runtime log
	r.mu.Lock()
	r.eventLog = append(r.eventLog, event)

	// Trim if too many events
	if len(r.eventLog) > r.maxEvents {
		r.eventLog = r.eventLog[len(r.eventLog)-r.maxEvents:]
	}
	r.mu.Unlock()

	// Process state deltas
	if event.StateDelta != nil {
		for key, value := range event.StateDelta {
			ctx.Set(key, value)
		}
	}

	// Commit if not partial
	if !event.Partial {
		ctx.CommitStateDeltas()
	}

	// Handle checkpoint events
	if event.Type == EventTypeCheckpoint && r.enableCheckpoint {
		if err := r.checkpoint(ctx); err != nil {
			return fmt.Errorf("checkpoint failed: %w", err)
		}
	}

	return nil
}

// checkpoint creates a checkpoint (Spark-style)
func (r *AgentRuntime) checkpoint(ctx *InvocationContext) error {
	if r.services == nil || r.services.SessionService == nil {
		return nil // No service configured
	}

	// Save session state
	session := &Session{
		ID:           ctx.SessionID,
		UserID:       ctx.UserID,
		State:        ctx.State,
		EventHistory: ctx.EventHistory,
		UpdatedAt:    time.Now(),
	}

	return r.services.SessionService.UpdateSession(ctx, session)
}

// GetEventLog returns the event log
func (r *AgentRuntime) GetEventLog() []Event {
	r.mu.RLock()
	defer r.mu.RUnlock()

	events := make([]Event, len(r.eventLog))
	copy(events, r.eventLog)
	return events
}

// Helper function to generate IDs
func generateID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}
