// Copyright 2025 Apache Spark AI Agents
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law of agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tools

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func requireGVisorToolEnvironment(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("runsc"); err != nil {
		t.Skipf("skipping gVisor tool test: runsc not available: %v", err)
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		t.Skipf("skipping gVisor tool test: sudo not available: %v", err)
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
		t.Skipf("skipping gVisor tool test: host cannot run sudo runsc: %v (%s)", err, strings.TrimSpace(string(output)))
	}

	t.Fatalf("gVisor tool environment check failed: %v (%s)", err, strings.TrimSpace(string(output)))
}

func TestCodeExecutionTool_GVisor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping gvisor test in short mode")
	}

	requireGVisorToolEnvironment(t)

	config := DefaultCodeExecutionConfig()
	config.SandboxType = "gvisor"
	config.AllowedLanguages = []string{"go"}

	tool := CodeExecutionTool(config)

	params := map[string]interface{}{
		"language": "go",
		"code": `
package main
import "fmt"
func main() {
    fmt.Println("Hello from gVisor sandbox via tool!")
}
`,
	}

	result, err := tool.Handler(context.Background(), params)
	if err != nil {
		t.Fatalf("handler returned an error: %v", err)
	}

	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("handler did not return a map")
	}

	if success, _ := resultMap["success"].(bool); !success {
		t.Errorf("execution was not successful: %v", resultMap["stderr"])
	}

	stdout, _ := resultMap["stdout"].(string)
	if !strings.Contains(stdout, "Hello from gVisor sandbox via tool!") {
		t.Errorf("unexpected stdout: got %q", stdout)
	}
}
