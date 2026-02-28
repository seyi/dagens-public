// Package models provides a registry for managing multiple LLM providers
package models

import (
	"fmt"
	"sync"
)

// ProviderFactory is a function that creates a new provider instance from config
type ProviderFactory func(config map[string]interface{}) (ModelProvider, error)

// ProviderRegistry manages available LLM providers
type ProviderRegistry struct {
	factories map[string]ProviderFactory
	providers map[string]ModelProvider
	mu        sync.RWMutex
}

// NewProviderRegistry creates a new provider registry
func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{
		factories: make(map[string]ProviderFactory),
		providers: make(map[string]ModelProvider),
	}
}

// Register registers a provider factory for a given type
func (r *ProviderRegistry) Register(providerType string, factory ProviderFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[providerType] = factory
}

// Create creates a new provider instance from configuration
// Config format:
// {
//   "type": "openai",
//   "model": "gpt-4-turbo",
//   "api_key_env": "OPENAI_API_KEY",
//   "parameters": {...}
// }
func (r *ProviderRegistry) Create(config map[string]interface{}) (ModelProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	providerType, ok := config["type"].(string)
	if !ok {
		return nil, fmt.Errorf("provider type not specified in config")
	}

	factory, exists := r.factories[providerType]
	if !exists {
		return nil, fmt.Errorf("unknown provider type: %s", providerType)
	}

	return factory(config)
}

// CreateAndRegister creates a provider and registers it by ID
func (r *ProviderRegistry) CreateAndRegister(id string, config map[string]interface{}) (ModelProvider, error) {
	provider, err := r.Create(config)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.providers[id] = provider
	r.mu.Unlock()

	return provider, nil
}

// Get retrieves a registered provider by ID
func (r *ProviderRegistry) Get(id string) (ModelProvider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	provider, exists := r.providers[id]
	if !exists {
		return nil, fmt.Errorf("provider not found: %s", id)
	}

	return provider, nil
}

// List returns all registered provider IDs
func (r *ProviderRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]string, 0, len(r.providers))
	for id := range r.providers {
		ids = append(ids, id)
	}
	return ids
}

// ListTypes returns all registered provider types
func (r *ProviderRegistry) ListTypes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	types := make([]string, 0, len(r.factories))
	for pType := range r.factories {
		types = append(types, pType)
	}
	return types
}

// Remove removes a provider by ID
func (r *ProviderRegistry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	provider, exists := r.providers[id]
	if !exists {
		return fmt.Errorf("provider not found: %s", id)
	}

	// Close the provider
	if err := provider.Close(); err != nil {
		return fmt.Errorf("failed to close provider: %w", err)
	}

	delete(r.providers, id)
	return nil
}

// Close closes all registered providers
func (r *ProviderRegistry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var errs []error
	for id, provider := range r.providers {
		if err := provider.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close provider %s: %w", id, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing providers: %v", errs)
	}

	return nil
}

// Global registry instance
var globalRegistry = NewProviderRegistry()

// RegisterProvider registers a provider factory globally
func RegisterProvider(providerType string, factory ProviderFactory) {
	globalRegistry.Register(providerType, factory)
}

// CreateProvider creates a provider from config using global registry
func CreateProvider(config map[string]interface{}) (ModelProvider, error) {
	return globalRegistry.Create(config)
}

// GetProvider retrieves a provider from global registry
func GetProvider(id string) (ModelProvider, error) {
	return globalRegistry.Get(id)
}
