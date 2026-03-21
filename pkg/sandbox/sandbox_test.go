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
	"os/exec"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func requireGVisorTestEnvironment(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("runsc"); err != nil {
		t.Skipf("skipping gVisor test: runsc not available: %v", err)
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		t.Skipf("skipping gVisor test: sudo not available: %v", err)
	}

	cmd := exec.Command("sudo", "-n", "runsc", "--version")
	output, err := cmd.CombinedOutput()
	if err == nil {
		return
	}

	text := strings.ToLower(string(output))
	if strings.Contains(text, "no new privileges") ||
		strings.Contains(text, "a password is required") ||
		strings.Contains(text, "permission denied") {
		t.Skipf("skipping gVisor test: sandbox host cannot run sudo runsc: %v (%s)", err, strings.TrimSpace(string(output)))
	}

	t.Fatalf("gVisor test environment check failed: %v (%s)", err, strings.TrimSpace(string(output)))
}

func TestGVisorExecutor_SimpleExecution(t *testing.T) {
	requireGVisorTestEnvironment(t)

	executor, err := NewGVisorExecutor("")
	if err != nil {
		t.Fatalf("failed to create executor: %v", err)
	}

	code := `
package main
import "fmt"
func main() {
    fmt.Println("Hello from gVisor!")
}
`
	stdout, stderr, err := executor.ExecuteGo(context.Background(), code, nil)
	if err != nil {
		t.Fatalf("execution failed: %v\nStderr: %s", err, stderr)
	}

	if !strings.Contains(stdout, "Hello from gVisor!") {
		t.Errorf("unexpected output: got %q", stdout)
	}
}

func TestGVisorExecutor_ReadOnlyRootFS(t *testing.T) {
	requireGVisorTestEnvironment(t)

	executor, err := NewGVisorExecutor("")
	if err != nil {
		t.Fatalf("failed to create executor: %v", err)
	}

	code := `
package main
import "os"
func main() {
    if err := os.WriteFile("/test.txt", []byte("hello"), 0644); err != nil {
        panic(err)
    }
}
`
	_, stderr, err := executor.ExecuteGo(context.Background(), code, nil)
	if err == nil {
		t.Fatal("execution succeeded but should have failed")
	}

	if !strings.Contains(stderr, "read-only file system") {
		t.Errorf("expected read-only file system error, but got: %s", stderr)
	}
}

func TestGVisorExecutor_NoNetwork(t *testing.T) {
	requireGVisorTestEnvironment(t)

	executor, err := NewGVisorExecutor("")
	if err != nil {
		t.Fatalf("failed to create executor: %v", err)
	}

	code := `
package main
import "net"
func main() {
    if _, err := net.Dial("tcp", "google.com:80"); err != nil {
        panic(err)
    }
}
`
	_, stderr, err := executor.ExecuteGo(context.Background(), code, nil)
	if err == nil {
		t.Fatal("execution succeeded but should have failed")
	}

	if !strings.Contains(stderr, "cannot assign requested address") && !strings.Contains(stderr, "network is unreachable") {
		t.Errorf("expected network error, but got: %s", stderr)
	}
}

func TestGVisorExecutor_WithMount(t *testing.T) {
	requireGVisorTestEnvironment(t)

	executor, err := NewGVisorExecutor("")
	if err != nil {
		t.Fatalf("failed to create executor: %v", err)
	}

	// Create a temporary directory on the host to mount
	hostDir, err := os.MkdirTemp("", "gvisor-mount-test")
	if err != nil {
		t.Fatalf("failed to create temp dir for mount: %v", err)
	}
	defer os.RemoveAll(hostDir)

	// Write a file to the host directory
	hostFilePath := filepath.Join(hostDir, "test.txt")
	if err := os.WriteFile(hostFilePath, []byte("hello from host"), 0644); err != nil {
		t.Fatalf("failed to write to host file: %v", err)
	}

	policy := &Policy{
		Mounts: []Mount{
			{
				Source:      hostDir,
				Destination: "/data",
				ReadOnly:    true,
			},
		},
	}

	code := `
package main
import (
    "fmt"
    "os"
)
func main() {
    data, err := os.ReadFile("/data/test.txt")
    if err != nil {
        panic(err)
    }
    fmt.Println(string(data))
}
`
	stdout, stderr, err := executor.ExecuteGo(context.Background(), code, policy)
	if err != nil {
		t.Fatalf("execution failed: %v\nStderr: %s", err, stderr)
	}

	if !strings.Contains(stdout, "hello from host") {
		t.Errorf("unexpected output: got %q", stdout)
	}
}
