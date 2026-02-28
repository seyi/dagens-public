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
	"os"
	"testing"
)

func TestEnvProvider_GetSecret(t *testing.T) {
	provider := NewEnvProvider()
	ctx := context.Background()

	// Test case 1: Existing environment variable
	os.Setenv("TEST_SECRET", "my-secret-value")
	defer os.Unsetenv("TEST_SECRET")

	secret, err := provider.GetSecret(ctx, "TEST_SECRET")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secret != "my-secret-value" {
		t.Errorf("expected 'my-secret-value', got '%s'", secret)
	}

	// Test case 2: Non-existent environment variable
	_, err = provider.GetSecret(ctx, "NON_EXISTENT_SECRET")
	if err == nil {
		t.Fatal("expected an error for non-existent secret, got nil")
	}
}

func TestEnvProvider_Name(t *testing.T) {
	provider := NewEnvProvider()
	if provider.Name() != "env" {
		t.Errorf("expected name 'env', got '%s'", provider.Name())
	}
}

func TestManager_GetSecret(t *testing.T) {
	manager := NewManager()
	provider := NewEnvProvider()
	manager.RegisterProvider(provider)

	ctx := context.Background()
	config := Config{
		Name:     "MANAGER_TEST_SECRET",
		Provider: "env",
	}
	os.Setenv("MANAGER_TEST_SECRET", "manager-secret-value")
	defer os.Unsetenv("MANAGER_TEST_SECRET")

	secret, err := manager.GetSecret(ctx, config)
	if err != nil {
		t.Fatalf("unexpected error from manager: %v", err)
	}
	if secret != "manager-secret-value" {
		t.Errorf("expected 'manager-secret-value', got '%s'", secret)
	}

	// Test unknown provider
	config.Provider = "unknown"
	_, err = manager.GetSecret(ctx, config)
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
}
