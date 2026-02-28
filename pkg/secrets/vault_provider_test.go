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
	"log"
	"os/exec"
	"strings"
	"testing"
	"time"

	vault "github.com/hashicorp/vault/api"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestVaultProvider_GetSecret(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker not available, skipping Vault integration test.")
	}
	if testing.Short() {
		t.Skip("skipping Vault integration test in short mode.")
	}

	ctx := context.Background()

	// Start Vault in a Docker container
	log.Println("Starting Vault container...")
	req := testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "vault:1.15",
			ExposedPorts: []string{"8200/tcp"},
			Env: map[string]string{
				"VAULT_DEV_ROOT_TOKEN_ID": "myroot",
				"VAULT_DEV_LISTEN_ADDRESS": "0.0.0.0:8200",
			},
			WaitingFor: wait.ForLog("Development mode should NOT be used in production").WithStartupTimeout(10 * time.Second),
		},
		Started: true,
	}
	vaultContainer, err := testcontainers.GenericContainer(ctx, req)
	if err != nil {
		t.Fatalf("failed to start Vault container: %v", err)
	}
	defer vaultContainer.Terminate(ctx)

	log.Println("Vault container started.")

	hostIP, err := vaultContainer.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get host IP: %v", err)
	}

	httpPort, err := vaultContainer.MappedPort(ctx, "8200/tcp")
	if err != nil {
		t.Fatalf("failed to get mapped port: %v", err)
	}

	vaultAddr := fmt.Sprintf("http://%s:%s", hostIP, httpPort.Port())
	vaultToken := "myroot"

	// Initialize Vault client for setup
	clientConfig := vault.DefaultConfig()
	clientConfig.Address = vaultAddr
	setupClient, err := vault.NewClient(clientConfig)
	if err != nil {
		t.Fatalf("failed to create setup Vault client: %v", err)
	}
	setupClient.SetToken(vaultToken)

	// Enable KV-V2 secret engine
	log.Println("Enabling KV-V2 secret engine...")
	err = setupClient.Sys().Mount("kv", &vault.MountInput{
		Type:    "kv",
		Options: map[string]string{"version": "2"},
	})
	if err != nil && !strings.Contains(err.Error(), "path is already in use") {
		t.Fatalf("failed to enable KV-V2 secret engine: %v", err)
	}
	log.Println("KV-V2 secret engine enabled.")

	// Write a secret to Vault
	secretPath := "my-app/api-key"
	secretData := map[string]interface{}{
		"data": map[string]interface{}{
			"value": "vault-secret-value",
		},
	}
	log.Printf("Writing secret to Vault at %s...", secretPath)
	_, err = setupClient.KVv2("kv").Put(ctx, secretPath, secretData)
	if err != nil {
		t.Fatalf("failed to write secret to Vault: %v", err)
	}
	log.Println("Secret written to Vault.")

	// Create VaultProvider
	vaultProviderConfig := VaultConfig{
		Addr:  vaultAddr,
		Token: vaultToken,
	}
	provider, err := NewVaultProvider(vaultProviderConfig)
	if err != nil {
		t.Fatalf("failed to create Vault provider: %v", err)
	}

	// Test case 1: Retrieve existing secret
	secret, err := provider.GetSecret(ctx, "my-app/api-key")
	if err != nil {
		t.Fatalf("unexpected error retrieving secret: %v", err)
	}
	if secret != "vault-secret-value" {
		t.Errorf("expected 'vault-secret-value', got '%s'", secret)
	}

	// Test case 2: Retrieve non-existent secret
	_, err = provider.GetSecret(ctx, "my-app/non-existent-key")
	if err == nil {
		t.Fatal("expected an error for non-existent secret, got nil")
	}
	if !strings.Contains(err.Error(), "secret not found") {
		t.Errorf("expected 'secret not found' error, got: %v", err)
	}
}
