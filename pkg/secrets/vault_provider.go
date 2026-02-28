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

	vault "github.com/hashicorp/vault/api"
)

const vaultProviderName = "vault"

// VaultConfig configures the Vault provider.
type VaultConfig struct {
	Addr      string // Vault address, e.g., "http://127.0.0.1:8200"
	Token     string // Vault token
	Namespace string // Vault namespace (optional)
}

// VaultProvider retrieves secrets from HashiCorp Vault.
type VaultProvider struct {
	client *vault.Client
	config VaultConfig
}

// NewVaultProvider creates a new VaultProvider.
func NewVaultProvider(config VaultConfig) (*VaultProvider, error) {
	vaultConfig := vault.DefaultConfig()
	if config.Addr != "" {
		vaultConfig.Address = config.Addr
	}

	client, err := vault.NewClient(vaultConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Vault client: %w", err)
	}

	client.SetToken(config.Token)
	if config.Namespace != "" {
		client.SetNamespace(config.Namespace)
	}

	return &VaultProvider{
		client: client,
		config: config,
	}, nil
}

// GetSecret retrieves a secret from Vault.
// The name parameter is used as the key within the Vault secret path.
// The path parameter (from Config struct) will be used as the base path for the secret.
func (p *VaultProvider) GetSecret(ctx context.Context, name string) (string, error) {
	if p.config.Addr == "" || p.config.Token == "" {
		return "", fmt.Errorf("Vault address and token must be configured")
	}

	// Assume KV-V2 secret engine for now
	secretPath := fmt.Sprintf("kv/data/%s", name)
	if p.config.Namespace != "" {
		secretPath = fmt.Sprintf("%s/%s", p.config.Namespace, secretPath)
	}

	secret, err := p.client.Logical().ReadWithContext(ctx, secretPath)
	if err != nil {
		return "", fmt.Errorf("failed to read secret from Vault at %s: %w", secretPath, err)
	}

	if secret == nil || secret.Data == nil {
		return "", fmt.Errorf("secret not found at %s or has no data", secretPath)
	}

	// Assuming the secret value is under a key named 'value' in the data map
	// For KV-V2, data is nested under 'data' key
	data, ok := secret.Data["data"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("Vault secret at %s does not contain a 'data' map", secretPath)
	}

	value, ok := data["value"].(string)
	if !ok {
		return "", fmt.Errorf("Vault secret at %s does not contain a 'value' key", secretPath)
	}

	return value, nil
}

// Name returns the name of the provider.
func (p *VaultProvider) Name() string {
	return vaultProviderName
}
