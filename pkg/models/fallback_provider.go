// Package models provides a fallback provider that tries multiple providers
package models

import (
	"context"
	"fmt"
	"log"
)

// FallbackProvider tries multiple providers in order until one succeeds
type FallbackProvider struct {
	*BaseProvider
	providers []ModelProvider
}

// NewFallbackProvider creates a new fallback provider
func NewFallbackProvider(id string, providers ...ModelProvider) *FallbackProvider {
	if len(providers) == 0 {
		panic("fallback provider requires at least one provider")
	}

	// Use first provider's info for base
	first := providers[0]
	return &FallbackProvider{
		BaseProvider: NewBaseProvider(id, "fallback", first.Name()),
		providers:    providers,
	}
}

// Chat tries each provider in order until one succeeds
func (f *FallbackProvider) Chat(ctx context.Context, req *ModelRequest) (*ModelResponse, error) {
	var lastErr error

	for i, provider := range f.providers {
		resp, err := provider.Chat(ctx, req)
		if err == nil {
			if i > 0 {
				log.Printf("Fallback succeeded with provider %d (%s)", i, provider.ID())
			}
			return resp, nil
		}

		log.Printf("Provider %s failed: %v, trying next", provider.ID(), err)
		lastErr = err
	}

	return nil, fmt.Errorf("all providers failed, last error: %w", lastErr)
}

// Stream tries each provider until one succeeds
func (f *FallbackProvider) Stream(ctx context.Context, req *ModelRequest, handler StreamHandler) error {
	var lastErr error

	for i, provider := range f.providers {
		err := provider.Stream(ctx, req, handler)
		if err == nil {
			if i > 0 {
				log.Printf("Fallback stream succeeded with provider %d (%s)", i, provider.ID())
			}
			return nil
		}

		log.Printf("Provider %s stream failed: %v, trying next", provider.ID(), err)
		lastErr = err
	}

	return fmt.Errorf("all providers failed for streaming, last error: %w", lastErr)
}

// SupportsTools returns true if any provider supports tools
func (f *FallbackProvider) SupportsTools() bool {
	for _, p := range f.providers {
		if p.SupportsTools() {
			return true
		}
	}
	return false
}

// SupportsVision returns true if any provider supports vision
func (f *FallbackProvider) SupportsVision() bool {
	for _, p := range f.providers {
		if p.SupportsVision() {
			return true
		}
	}
	return false
}

// SupportsStreaming returns true if any provider supports streaming
func (f *FallbackProvider) SupportsStreaming() bool {
	for _, p := range f.providers {
		if p.SupportsStreaming() {
			return true
		}
	}
	return false
}

// SupportsJSONMode returns true if any provider supports JSON mode
func (f *FallbackProvider) SupportsJSONMode() bool {
	for _, p := range f.providers {
		if p.SupportsJSONMode() {
			return true
		}
	}
	return false
}

// ContextWindow returns the maximum context window across all providers
func (f *FallbackProvider) ContextWindow() int {
	max := 0
	for _, p := range f.providers {
		if window := p.ContextWindow(); window > max {
			max = window
		}
	}
	return max
}

// MaxOutputTokens returns the maximum output tokens across all providers
func (f *FallbackProvider) MaxOutputTokens() int {
	max := 0
	for _, p := range f.providers {
		if tokens := p.MaxOutputTokens(); tokens > max {
			max = tokens
		}
	}
	return max
}

// HealthCheck checks health of all providers
func (f *FallbackProvider) HealthCheck(ctx context.Context) error {
	var errs []error
	available := 0

	for _, provider := range f.providers {
		if err := provider.HealthCheck(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", provider.ID(), err))
		} else {
			available++
		}
	}

	if available == 0 {
		return fmt.Errorf("no providers available: %v", errs)
	}

	return nil
}

// Close closes all providers
func (f *FallbackProvider) Close() error {
	var errs []error
	for _, provider := range f.providers {
		if err := provider.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing providers: %v", errs)
	}

	return nil
}
