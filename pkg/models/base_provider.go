// Package models provides base implementation for common provider functionality
package models

import (
	"sync"
	"time"
)

// BaseProvider provides common functionality for all providers
type BaseProvider struct {
	id           string
	providerType string
	modelName    string

	// Status tracking
	status         ProviderStatus
	statusMu       sync.RWMutex
	latencySum     time.Duration
	requestCount   int64
}

// NewBaseProvider creates a new base provider
func NewBaseProvider(id, providerType, modelName string) *BaseProvider {
	return &BaseProvider{
		id:           id,
		providerType: providerType,
		modelName:    modelName,
		status: ProviderStatus{
			Available:   true,
			LastChecked: time.Now(),
		},
	}
}

// ID returns the unique instance ID
func (b *BaseProvider) ID() string {
	return b.id
}

// Type returns the provider type
func (b *BaseProvider) Type() string {
	return b.providerType
}

// Name returns the model name
func (b *BaseProvider) Name() string {
	return b.modelName
}

// GetStatus returns the current provider status
func (b *BaseProvider) GetStatus() ProviderStatus {
	b.statusMu.RLock()
	defer b.statusMu.RUnlock()
	status := b.status
	return status
}

// RecordRequest records metrics for a request
func (b *BaseProvider) RecordRequest(latency time.Duration, err error) {
	b.statusMu.Lock()
	defer b.statusMu.Unlock()

	b.requestCount++
	b.latencySum += latency

	if err != nil {
		b.status.ErrorCount++
		b.status.LastError = err
		// Consider provider unavailable after repeated errors
		if b.status.ErrorCount >= 5 {
			b.status.Available = false
		}
	} else {
		// Reset error count on success
		b.status.ErrorCount = 0
		b.status.Available = true
	}

	// Update average latency
	if b.requestCount > 0 {
		b.status.AverageLatency = b.latencySum / time.Duration(b.requestCount)
	}

	b.status.LastChecked = time.Now()
}

// MarkAvailable marks the provider as available
func (b *BaseProvider) MarkAvailable() {
	b.statusMu.Lock()
	defer b.statusMu.Unlock()
	b.status.Available = true
	b.status.ErrorCount = 0
	b.status.LastError = nil
}

// MarkUnavailable marks the provider as unavailable
func (b *BaseProvider) MarkUnavailable(err error) {
	b.statusMu.Lock()
	defer b.statusMu.Unlock()
	b.status.Available = false
	b.status.LastError = err
	b.status.LastChecked = time.Now()
}

// Close performs cleanup (default implementation does nothing)
func (b *BaseProvider) Close() error {
	return nil
}

// Helper function to create a simple system message
func SystemMessage(content string) Message {
	return Message{
		Role:    RoleSystem,
		Content: content,
	}
}

// Helper function to create a user message
func UserMessage(content string) Message {
	return Message{
		Role:    RoleUser,
		Content: content,
	}
}

// Helper function to create an assistant message
func AssistantMessage(content string) Message {
	return Message{
		Role:    RoleAssistant,
		Content: content,
	}
}

// Helper function to create a tool message
func ToolMessage(toolCallID, content string) Message {
	return Message{
		Role:       RoleTool,
		ToolCallID: toolCallID,
		Content:    content,
	}
}
