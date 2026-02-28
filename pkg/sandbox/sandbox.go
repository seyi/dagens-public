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

package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Executor is the interface for executing code in a sandbox.
type Executor interface {
	// Execute runs a command with the given policy.
	Execute(ctx context.Context, cmd []string, policy *Policy) (string, string, error)
	// ExecuteGo runs Go code with the given policy.
	ExecuteGo(ctx context.Context, code string, policy *Policy) (string, string, error)
}

// NewExecutor creates the best available executor for the environment.
func NewExecutor(workDir string) (Executor, error) {
	// Check if docker is available
	_, err := exec.LookPath("docker")
	if err == nil {
		return NewDockerExecutor(workDir, "alpine:latest")
	}

	// Fallback to gVisor if docker is not available (though runsc check failed earlier)
	_, err = exec.LookPath("runsc")
	if err == nil {
		return NewGVisorExecutor(workDir)
	}

	return nil, fmt.Errorf("no suitable sandbox executor found (docker or runsc required)")
}

// OCI Spec subset for config.json
type OCISpec struct {
	OCIVersion string    `json:"ociVersion"`
	Root       Root      `json:"root"`
	Process    Process   `json:"process"`
	Linux      *Linux    `json:"linux,omitempty"`
	Mounts     []OCIMount `json:"mounts,omitempty"`
}

type Root struct {
	Path     string `json:"path"`
	Readonly bool   `json:"readonly"`
}

type Process struct {
	Args []string `json:"args"`
	Cwd  string   `json:"cwd"`
}

type Linux struct {
	Resources *LinuxResources `json:"resources,omitempty"`
}

type LinuxResources struct {
	CPU    *LinuxCPU    `json:"cpu,omitempty"`
	Memory *LinuxMemory `json:"memory,omitempty"`
}

type LinuxCPU struct {
	Quota  *int64 `json:"quota,omitempty"`
	Period *uint64 `json:"period,omitempty"`
}

type LinuxMemory struct {
	Limit *int64 `json:"limit,omitempty"`
}

type OCIMount struct {
	Destination string   `json:"destination"`
	Type        string   `json:"type"`
	Source      string   `json:"source"`
	Options     []string `json:"options"`
}

// GVisorExecutor executes code in a gVisor sandbox.
type GVisorExecutor struct {
	workDir string
}

// NewGVisorExecutor creates a new gVisor executor.
func NewGVisorExecutor(workDir string) (*GVisorExecutor, error) {
	if workDir == "" {
		workDir = filepath.Join(os.TempDir(), "dagens-sandbox")
	}
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create work directory: %w", err)
	}
	return &GVisorExecutor{workDir: workDir}, nil
}

// Execute runs the provided command in a gVisor sandbox with the given policy.
func (e *GVisorExecutor) Execute(ctx context.Context, cmd []string, policy *Policy) (string, string, error) {
	// This would require a more complex setup to handle arbitrary commands
	// For now, we'll maintain the existing logic for Go code in ExecuteGo
	return "", "", fmt.Errorf("generic command execution not yet implemented for gVisor")
}

// ExecuteGo runs the provided Go code in a gVisor sandbox with the given policy.
func (e *GVisorExecutor) ExecuteGo(ctx context.Context, code string, policy *Policy) (string, string, error) {
	// Create a temporary directory for the OCI bundle
	bundleDir, err := os.MkdirTemp(e.workDir, "runsc-bundle-")
	if err != nil {
		return "", "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(bundleDir)

	// Create the rootfs directory
	rootfsDir := filepath.Join(bundleDir, "rootfs")
	if err := os.Mkdir(rootfsDir, 0755); err != nil {
		return "", "", fmt.Errorf("failed to create rootfs dir: %w", err)
	}

	// Write the Go code to a file in the rootfs
	codePath := filepath.Join(rootfsDir, "main.go")
	if err := os.WriteFile(codePath, []byte(code), 0644); err != nil {
		return "", "", fmt.Errorf("failed to write code file: %w", err)
	}

	// Build the Go program
	buildCmd := exec.Command("go", "build", "-o", filepath.Join(rootfsDir, "main"), codePath)
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := buildCmd.CombinedOutput(); err != nil {
		return "", string(output), fmt.Errorf("failed to build go program: %w", err)
	}

	// Create the config.json
	spec := e.createOCISpec(policy)
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal OCI spec: %w", err)
	}
	configPath := filepath.Join(bundleDir, "config.json")
	if err := os.WriteFile(configPath, specJSON, 0644); err != nil {
		return "", "", fmt.Errorf("failed to write config.json: %w", err)
	}

	// Run the application with runsc
	containerID := fmt.Sprintf("dagens-sandbox-%d", time.Now().UnixNano())
	runscCmd := exec.CommandContext(ctx, "sudo", "runsc", "run", containerID)
	runscCmd.Dir = bundleDir
	output, err := runscCmd.CombinedOutput()
	if err != nil {
		return "", string(output), fmt.Errorf("runsc command failed: %w", err)
	}

	return string(output), "", nil
}

func (e *GVisorExecutor) createOCISpec(policy *Policy) *OCISpec {
	spec := &OCISpec{
		OCIVersion: "1.0.2-d",
		Root:       Root{Path: "rootfs", Readonly: true},
		Process: Process{
			Args: []string{"/main"},
			Cwd:  "/",
		},
	}

	if policy == nil {
		return spec
	}

	// Apply resource limits
	if policy.Resources != nil {
		spec.Linux = &Linux{Resources: &LinuxResources{}}
		if policy.Resources.Cpus > 0 {
			period := uint64(100000)
			quota := int64(policy.Resources.Cpus * float64(period))
			spec.Linux.Resources.CPU = &LinuxCPU{
				Quota:  &quota,
				Period: &period,
			}
		}
		if policy.Resources.Memory > 0 {
			limit := policy.Resources.Memory * 1024 * 1024
			spec.Linux.Resources.Memory = &LinuxMemory{
				Limit: &limit,
			}
		}
	}

	// Apply mounts
	for _, mount := range policy.Mounts {
		options := []string{"bind"}
		if mount.ReadOnly {
			options = append(options, "ro")
		}
		spec.Mounts = append(spec.Mounts, OCIMount{
			Destination: mount.Destination,
			Type:        "bind",
			Source:      mount.Source,
			Options:     options,
		})
	}

	// Note: Network policy is not directly configured in the OCI spec in this basic form.
	// It's typically managed by the container runtime (e.g., Docker, containerd) when
	// creating the network namespace. For a simple `runsc run`, network is disabled by default.
	// Implementing network policies would require more advanced setup.

	return spec
}