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
	"os"
)

const envProviderName = "env"

// EnvProvider retrieves secrets from environment variables.
type EnvProvider struct{}

// NewEnvProvider creates a new EnvProvider.
func NewEnvProvider() *EnvProvider {
	return &EnvProvider{}
}

// GetSecret retrieves a secret from an environment variable.
func (p *EnvProvider) GetSecret(ctx context.Context, name string) (string, error) {
	val := os.Getenv(name)
	if val == "" {
		return "", fmt.Errorf("environment variable %s not found", name)
	}
	return val, nil
}

// Name returns the name of the provider.
func (p *EnvProvider) Name() string {
	return envProviderName
}
