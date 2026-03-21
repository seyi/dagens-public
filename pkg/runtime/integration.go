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

// Integration helpers for wiring agents with the Runtime

package runtime

import (
	"context"
	"fmt"
	"time"
)

type invocationContextKey struct{}

var runtimeInvocationContextKey = invocationContextKey{}

// IsRuntimeContext checks if context is an InvocationContext
func IsRuntimeContext(ctx context.Context) bool {
	return GetInvocationContext(ctx) != nil
}

// GetInvocationContext extracts InvocationContext from context
// Returns nil if context is not an InvocationContext
func GetInvocationContext(ctx context.Context) *InvocationContext {
	invCtx, ok := ctx.(*InvocationContext)
	if ok {
		return invCtx
	}
	invCtx, _ = ctx.Value(runtimeInvocationContextKey).(*InvocationContext)
	return invCtx
}

func withInvocationContext(ctx context.Context, invCtx *InvocationContext) context.Context {
	return context.WithValue(ctx, runtimeInvocationContextKey, invCtx)
}

// EmitEvent emits an event if context is runtime-aware
// This is a helper for agents to emit events without directly depending on runtime
func EmitEvent(ctx context.Context, eventType EventType, content interface{}, partial bool) {
	invCtx := GetInvocationContext(ctx)
	if invCtx == nil {
		return // Not runtime-aware, skip event emission
	}

	event := Event{
		ID:          generateID("event"),
		Type:        eventType,
		Content:     content,
		Partial:     partial,
		Author:      "agent", // Can be overridden
		Timestamp:   time.Now(),
		PartitionID: invCtx.PartitionID,
	}

	invCtx.AddEvent(event)
}

// EmitEventWithDelta emits an event with state changes
func EmitEventWithDelta(ctx context.Context, eventType EventType, content interface{}, partial bool, stateDelta map[string]interface{}) {
	invCtx := GetInvocationContext(ctx)
	if invCtx == nil {
		return
	}

	event := Event{
		ID:          generateID("event"),
		Type:        eventType,
		Content:     content,
		Partial:     partial,
		StateDelta:  stateDelta,
		Author:      "agent",
		Timestamp:   time.Now(),
		PartitionID: invCtx.PartitionID,
	}

	invCtx.AddEvent(event)

	// Apply state delta
	if stateDelta != nil && !partial {
		for key, value := range stateDelta {
			invCtx.Set(key, value)
		}
	}
}

// EmitToolCall emits a tool call event
func EmitToolCall(ctx context.Context, toolName string, arguments interface{}) {
	EmitEvent(ctx, EventTypeToolCall, map[string]interface{}{
		"tool":      toolName,
		"arguments": arguments,
	}, false)
}

// EmitToolResult emits a tool result event
func EmitToolResult(ctx context.Context, toolName string, result interface{}, err error) {
	content := map[string]interface{}{
		"tool":   toolName,
		"result": result,
	}

	eventType := EventTypeToolResult
	if err != nil {
		content["error"] = err.Error()
		eventType = EventTypeError
	}

	EmitEvent(ctx, eventType, content, false)
}

// EmitMessage emits a message event (partial or final)
func EmitMessage(ctx context.Context, message string, partial bool) {
	EmitEvent(ctx, EventTypeMessage, message, partial)
}

// EmitCheckpoint emits a checkpoint event
func EmitCheckpoint(ctx context.Context, metadata map[string]interface{}) {
	EmitEvent(ctx, EventTypeCheckpoint, metadata, false)
}

// GetState retrieves state from InvocationContext
// Returns nil if context is not runtime-aware
func GetState(ctx context.Context, key string) (interface{}, bool) {
	invCtx := GetInvocationContext(ctx)
	if invCtx == nil {
		return nil, false
	}
	return invCtx.Get(key)
}

// SetState sets state in InvocationContext
// No-op if context is not runtime-aware
func SetState(ctx context.Context, key string, value interface{}) {
	invCtx := GetInvocationContext(ctx)
	if invCtx == nil {
		return
	}
	invCtx.Set(key, value)
}

// SetTempState sets temporary state (auto-cleaned after invocation)
func SetTempState(ctx context.Context, key string, value interface{}) {
	SetState(ctx, fmt.Sprintf("temp:%s", key), value)
}

// GetTempState retrieves temporary state
func GetTempState(ctx context.Context, key string) (interface{}, bool) {
	return GetState(ctx, fmt.Sprintf("temp:%s", key))
}

// EmitStateChange emits a state change event
func EmitStateChange(ctx context.Context, key string, oldValue, newValue interface{}) {
	EmitEventWithDelta(ctx, EventTypeStateChange, map[string]interface{}{
		"key":       key,
		"old_value": oldValue,
		"new_value": newValue,
	}, false, map[string]interface{}{
		key: newValue,
	})
}
