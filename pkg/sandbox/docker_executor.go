package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// DockerExecutor executes code in a restricted Docker container.
// It provides a more secure and generic alternative to basic os/exec.
type DockerExecutor struct {
	workDir string
	image   string
}

// NewDockerExecutor creates a new Docker executor.
func NewDockerExecutor(workDir string, image string) (*DockerExecutor, error) {
	if workDir == "" {
		workDir = filepath.Join(os.TempDir(), "dagens-sandbox-docker")
	}
	if image == "" {
		image = "alpine:latest" // Default to a minimal image
	}
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create work directory: %w", err)
	}
	return &DockerExecutor{workDir: workDir, image: image}, nil
}

// Execute runs the provided command in a restricted Docker container.
func (e *DockerExecutor) Execute(ctx context.Context, cmd []string, policy *Policy) (string, string, error) {
	dockerArgs := []string{"run", "--rm"}

	// 1. Basic Security Hardening
	dockerArgs = append(dockerArgs, "--cap-drop", "ALL")        // Drop all linux capabilities
	dockerArgs = append(dockerArgs, "--security-opt", "no-new-privileges") // Prevent privilege escalation
	dockerArgs = append(dockerArgs, "--user", "1000:1000")      // Run as non-root user

	// 2. Resource Constraints
	if policy != nil && policy.Resources != nil {
		if policy.Resources.Cpus > 0 {
			dockerArgs = append(dockerArgs, "--cpus", fmt.Sprintf("%f", policy.Resources.Cpus))
		}
		if policy.Resources.Memory > 0 {
			dockerArgs = append(dockerArgs, "--memory", fmt.Sprintf("%dm", policy.Resources.Memory))
		}
	}

	// 3. Network Isolation
	if policy != nil && policy.Network != nil {
		if !policy.Network.Enabled {
			dockerArgs = append(dockerArgs, "--network", "none")
		} else {
			// If enabled, we could potentially restrict hosts here if using a custom network
			// For now, we just allow standard bridge network if explicitly enabled
		}
	} else {
		dockerArgs = append(dockerArgs, "--network", "none") // Default to no network for safety
	}

	// 4. Filesystem Mounts
	if policy != nil {
		for _, m := range policy.Mounts {
			mode := "rw"
			if m.ReadOnly {
				mode = "ro"
			}
			dockerArgs = append(dockerArgs, "-v", fmt.Sprintf("%s:%s:%s", m.Source, m.Destination, mode))
		}
	}

	// 5. Container ID for tracking
	containerName := fmt.Sprintf("dagens-exec-%d", time.Now().UnixNano())
	dockerArgs = append(dockerArgs, "--name", containerName)

	// 6. Image and Command
	dockerArgs = append(dockerArgs, e.image)
	dockerArgs = append(dockerArgs, cmd...)

	// Execute via Docker CLI
	execCmd := exec.CommandContext(ctx, "docker", dockerArgs...)
	output, err := execCmd.CombinedOutput()

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			// Cleanup if timed out
			exec.Command("docker", "kill", containerName).Run()
			return "", "", context.DeadlineExceeded
		}
		return "", string(output), fmt.Errorf("docker execution failed: %w", err)
	}

	return string(output), "", nil
}

// ExecuteGo runs Go code by mounting it into a golang container, compiling and executing it.
func (e *DockerExecutor) ExecuteGo(ctx context.Context, code string, policy *Policy) (string, string, error) {
	// Create a temporary directory for the code
	tempDir, err := os.MkdirTemp(e.workDir, "go-exec-")
	if err != nil {
		return "", "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Write the code
	codePath := filepath.Join(tempDir, "main.go")
	if err := os.WriteFile(codePath, []byte(code), 0644); err != nil {
		return "", "", fmt.Errorf("failed to write code: %w", err)
	}

	// We use a multi-step approach: 
	// 1. Mount the code into a golang container to build it.
	// 2. Run the resulting binary in a restricted alpine container.
	
	buildArgs := []string{
		"run", "--rm",
		"-v", fmt.Sprintf("%s:/app", tempDir),
		"-w", "/app",
		"-e", "CGO_ENABLED=0",
		"golang:alpine",
		"go", "build", "-o", "main", "main.go",
	}

	buildCmd := exec.CommandContext(ctx, "docker", buildArgs...)
	if out, err := buildCmd.CombinedOutput(); err != nil {
		return "", string(out), fmt.Errorf("failed to build go code: %w", err)
	}

	// Now execute the binary using the generic Execute method
	// We mount the tempDir to /app and run /app/main
	binaryPolicy := *policy
	binaryPolicy.Mounts = append(binaryPolicy.Mounts, Mount{
		Source:      tempDir,
		Destination: "/app",
		ReadOnly:    true,
	})

	e.image = "alpine:latest" // Ensure we use alpine for execution
	return e.Execute(ctx, []string{"/app/main"}, &binaryPolicy)
}
