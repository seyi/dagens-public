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

package runtime

import (
	"context"
	"fmt"

	"github.com/seyi/dagens/pkg/agent"
)

// StreamingRuntime provides real-time event streaming.
// Inspired by ADK's streaming support with partial events.
//
// This enables UI feedback while agent is thinking/working,
// separating display concerns from state persistence.
type StreamingRuntime struct {
	*AgentRuntime
	bufferSize int
}

// NewStreamingRuntime creates a new streaming runtime
func NewStreamingRuntime(ag agent.Agent, services *RuntimeServices) *StreamingRuntime {
	return &StreamingRuntime{
		AgentRuntime: NewAgentRuntime(ag, services),
		bufferSize:   100,
	}
}

// WithBufferSize sets the event channel buffer size
func (s *StreamingRuntime) WithBufferSize(size int) *StreamingRuntime {
	s.bufferSize = size
	return s
}

// RunStreaming executes the agent and streams events in real-time.
//
// Returns a channel that emits events as they occur. Partial events
// are streamed immediately for UI display, while final events trigger
// state commits.
//
// Example:
//   events := runtime.RunStreaming(ctx, input)
//   for event := range events {
//       if event.Partial {
//           displayToUI(event.Content)  // Real-time feedback
//       } else {
//           commitToDatabase(event.StateDelta)  // Persist state
//       }
//   }
func (s *StreamingRuntime) RunStreaming(ctx context.Context, input *agent.AgentInput) <-chan StreamEvent {
	output := make(chan StreamEvent, s.bufferSize)

	go func() {
		defer close(output)

		// Create invocation context
		invCtx := NewInvocationContext(ctx, input)
		invCtx.PartitionID = s.partitionID
		invCtx.Services = s.services

		// Check if agent supports streaming
		// For now, simulate streaming by breaking execution into steps
		// Real implementation would use generator pattern

		// Emit start event
		output <- StreamEvent{
			Event: Event{
				ID:        generateID("event"),
				Type:      EventTypeMessage,
				Content:   "Starting execution...",
				Partial:   true,
				Author:    s.agent.Name(),
				Timestamp: invCtx.StartTime,
			},
			Progress: 0.0,
		}

		// Execute agent (this would be streaming in real implementation)
		result, err := s.agent.Execute(invCtx, input)
		if err != nil {
			output <- StreamEvent{
				Event: Event{
					ID:      generateID("event"),
					Type:    EventTypeError,
					Content: err.Error(),
					Partial: false,
				},
				Error: err,
			}
			return
		}

		// Process events from context
		for i, event := range invCtx.EventHistory {
			progress := float64(i+1) / float64(len(invCtx.EventHistory))

			output <- StreamEvent{
				Event:    event,
				Progress: progress,
			}

			// Process event (state commits, checkpoints)
			if err := s.processEvent(invCtx, event); err != nil {
				output <- StreamEvent{
					Event: Event{
						ID:      generateID("event"),
						Type:    EventTypeError,
						Content: fmt.Sprintf("Event processing failed: %v", err),
					},
					Error: err,
				}
				return
			}
		}

		// Emit final event
		output <- StreamEvent{
			Event: Event{
				ID:        generateID("event"),
				Type:      EventTypeMessage,
				Content:   result.Result,
				Partial:   false,
				Author:    s.agent.Name(),
				StateDelta: map[string]interface{}{
					"final_result": result.Result,
				},
			},
			Progress: 1.0,
			Final:    result,
		}

		// Clean up
		invCtx.ClearTempState()
	}()

	return output
}

// StreamEvent wraps an Event with additional streaming metadata
type StreamEvent struct {
	Event    Event                `json:"event"`
	Progress float64              `json:"progress"` // 0.0 to 1.0
	Error    error                `json:"error,omitempty"`
	Final    *agent.AgentOutput   `json:"final,omitempty"` // Present on last event
}

// EventGenerator is an interface for agents that support native streaming.
// Agents implementing this can yield events incrementally.
type EventGenerator interface {
	// GenerateEvents returns a channel of events
	GenerateEvents(ctx *InvocationContext) <-chan Event
}

// RunWithGenerator executes an agent that implements EventGenerator.
// This provides true streaming where the agent yields events as they occur.
func (s *StreamingRuntime) RunWithGenerator(ctx context.Context, input *agent.AgentInput) <-chan StreamEvent {
	output := make(chan StreamEvent, s.bufferSize)

	// Check if agent implements EventGenerator
	generator, ok := s.agent.(EventGenerator)
	if !ok {
		// Fall back to non-streaming execution
		close(output)
		return output
	}

	go func() {
		defer close(output)

		invCtx := NewInvocationContext(ctx, input)
		invCtx.PartitionID = s.partitionID
		invCtx.Services = s.services

		eventCount := 0

		// Process events as they're generated
		for event := range generator.GenerateEvents(invCtx) {
			eventCount++

			// Add to context
			invCtx.AddEvent(event)

			// Emit to output
			output <- StreamEvent{
				Event:    event,
				Progress: -1.0, // Unknown total progress
			}

			// Process event (commits, checkpoints)
			if err := s.processEvent(invCtx, event); err != nil {
				output <- StreamEvent{
					Event: Event{
						Type:    EventTypeError,
						Content: err.Error(),
					},
					Error: err,
				}
				return
			}
		}

		// Build final output
		finalOutput := &agent.AgentOutput{
			TaskID:   input.TaskID,
			Metadata: make(map[string]interface{}),
		}
		finalOutput.Metadata["event_count"] = eventCount
		finalOutput.Metadata["invocation_context"] = invCtx

		// Get last non-partial event as result
		for i := len(invCtx.EventHistory) - 1; i >= 0; i-- {
			if !invCtx.EventHistory[i].Partial {
				finalOutput.Result = invCtx.EventHistory[i].Content
				break
			}
		}

		// Emit final marker
		output <- StreamEvent{
			Event: Event{
				Type:    EventTypeMessage,
				Content: "Execution complete",
				Partial: false,
			},
			Progress: 1.0,
			Final:    finalOutput,
		}
	}()

	return output
}

// BatchEvents groups events into batches for efficiency.
// Useful for reducing network overhead when streaming to remote clients.
func (s *StreamingRuntime) BatchEvents(events <-chan StreamEvent, batchSize int) <-chan []StreamEvent {
	batches := make(chan []StreamEvent)

	go func() {
		defer close(batches)

		batch := make([]StreamEvent, 0, batchSize)

		for event := range events {
			batch = append(batch, event)

			// Send batch when full or on final event
			if len(batch) >= batchSize || event.Final != nil {
				batchCopy := make([]StreamEvent, len(batch))
				copy(batchCopy, batch)
				batches <- batchCopy
				batch = batch[:0]
			}
		}

		// Send remaining events
		if len(batch) > 0 {
			batches <- batch
		}
	}()

	return batches
}

// FilterEvents filters events based on a predicate
func FilterEvents(events <-chan StreamEvent, predicate func(StreamEvent) bool) <-chan StreamEvent {
	filtered := make(chan StreamEvent)

	go func() {
		defer close(filtered)

		for event := range events {
			if predicate(event) {
				filtered <- event
			}
		}
	}()

	return filtered
}

// OnlyFinalEvents filters to only non-partial events
func OnlyFinalEvents(events <-chan StreamEvent) <-chan StreamEvent {
	return FilterEvents(events, func(e StreamEvent) bool {
		return !e.Event.Partial
	})
}

// OnlyPartialEvents filters to only partial events (for UI streaming)
func OnlyPartialEvents(events <-chan StreamEvent) <-chan StreamEvent {
	return FilterEvents(events, func(e StreamEvent) bool {
		return e.Event.Partial
	})
}
