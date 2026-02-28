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

package secrets

import (
	"context"
	"fmt"
)

// SecretProvider is an interface for retrieving secrets.
type SecretProvider interface {
	// GetSecret retrieves a secret by its name or key.
	GetSecret(ctx context.Context, name string) (string, error)
	// Name returns the name of the secret provider (e.g., "env", "vault").
	Name() string
}

// Config represents a generic configuration for a secret.
type Config struct {
	// Name is the name of the secret (e.g., "OPENAI_API_KEY").
	Name string
	// Provider is the name of the secret provider to use (e.g., "env", "vault").
	Provider string
	// Path is an optional path or key for the secret within the provider (e.g., "secret/data/api_keys").
	Path string
}

// Manager manages multiple secret providers.
type Manager struct {
	providers map[string]SecretProvider
}

// NewManager creates a new secret manager.
func NewManager() *Manager {
	return &Manager{
		providers: make(map[string]SecretProvider),
	}
}

// RegisterProvider registers a new secret provider with the manager.
func (m *Manager) RegisterProvider(provider SecretProvider) {
	m.providers[provider.Name()] = provider
}

// GetSecret retrieves a secret using the specified configuration.
func (m *Manager) GetSecret(ctx context.Context, config Config) (string, error) {
	provider, ok := m.providers[config.Provider]
	if !ok {
		return "", fmt.Errorf("unknown secret provider: %s", config.Provider)
	}

	return provider.GetSecret(ctx, config.Name)
}
