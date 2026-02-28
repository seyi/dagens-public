package tools

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Helper to check if Docker is available
func dockerAvailable() bool {
	cmd := exec.Command("docker", "version")
	return cmd.Run() == nil
}

// TestCodeExecutionToolBasicPython tests basic Python code execution
func TestCodeExecutionToolBasicPython(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available, skipping code execution tests")
	}

	tool := PythonOnlyCodeExecutionTool()
	ctx := context.Background()

	params := map[string]interface{}{
		"code":     "print('Hello from Python')",
		"language": "python",
	}

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("Code execution failed: %v", err)
	}

	resultMap := result.(map[string]interface{})
	if !resultMap["success"].(bool) {
		t.Errorf("Expected success=true, got false. Stderr: %v", resultMap["stderr"])
	}

	stdout := resultMap["stdout"].(string)
	if !strings.Contains(stdout, "Hello from Python") {
		t.Errorf("Expected output to contain 'Hello from Python', got: %s", stdout)
	}
}

// TestCodeExecutionToolMath tests mathematical calculations
func TestCodeExecutionToolMath(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	tool := PythonOnlyCodeExecutionTool()
	ctx := context.Background()

	// Calculate Fibonacci numbers
	code := `
def fibonacci(n):
    if n <= 1:
        return n
    a, b = 0, 1
    for _ in range(2, n + 1):
        a, b = b, a + b
    return b

result = [fibonacci(i) for i in range(10)]
print(result)
`

	params := map[string]interface{}{
		"code":     code,
		"language": "python",
	}

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("Code execution failed: %v", err)
	}

	resultMap := result.(map[string]interface{})
	if !resultMap["success"].(bool) {
		t.Errorf("Expected success=true. Stderr: %v", resultMap["stderr"])
	}

	stdout := resultMap["stdout"].(string)
	if !strings.Contains(stdout, "[0, 1, 1, 2, 3, 5, 8, 13, 21, 34]") {
		t.Errorf("Expected Fibonacci sequence, got: %s", stdout)
	}
}

// TestCodeExecutionToolTimeout tests timeout enforcement
func TestCodeExecutionToolTimeout(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	config := DefaultCodeExecutionConfig()
	config.Timeout = 2 * time.Second
	config.AllowedLanguages = []string{"python"}
	tool := CodeExecutionTool(config)

	ctx := context.Background()

	// Code that runs indefinitely
	code := `
import time
while True:
    time.sleep(1)
`

	params := map[string]interface{}{
		"code":     code,
		"language": "python",
	}

	result, err := tool.Handler(ctx, params)
	if err != nil {
		// Timeout is expected
		t.Logf("Expected timeout error: %v", err)
	}

	resultMap := result.(map[string]interface{})
	if resultMap["success"].(bool) {
		t.Error("Expected success=false for timeout")
	}

	// Should have timeout error
	stderr := resultMap["stderr"].(string)
	if !strings.Contains(strings.ToLower(stderr), "timeout") {
		t.Errorf("Expected timeout message in stderr, got: %s", stderr)
	}
}

// TestCodeExecutionToolSyntaxError tests error handling
func TestCodeExecutionToolSyntaxError(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	tool := PythonOnlyCodeExecutionTool()
	ctx := context.Background()

	// Invalid Python syntax
	params := map[string]interface{}{
		"code":     "print('missing closing quote)",
		"language": "python",
	}

	result, _ := tool.Handler(ctx, params)

	resultMap := result.(map[string]interface{})
	if resultMap["success"].(bool) {
		t.Error("Expected success=false for syntax error")
	}

	stderr := resultMap["stderr"].(string)
	if stderr == "" {
		t.Error("Expected error message in stderr")
	}
}

// TestCodeExecutionToolLanguageValidation tests language validation
func TestCodeExecutionToolLanguageValidation(t *testing.T) {
	tool := PythonOnlyCodeExecutionTool()
	ctx := context.Background()

	params := map[string]interface{}{
		"code":     "console.log('test')",
		"language": "javascript", // Not allowed in PythonOnly tool
	}

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Error("Expected error for disallowed language")
	}

	if !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("Expected 'not allowed' error, got: %v", err)
	}
}

// TestCodeExecutionToolMissingParameters tests parameter validation
func TestCodeExecutionToolMissingParameters(t *testing.T) {
	tool := PythonOnlyCodeExecutionTool()
	ctx := context.Background()

	// Missing code parameter
	params := map[string]interface{}{
		"language": "python",
	}

	_, err := tool.Handler(ctx, params)
	if err == nil {
		t.Error("Expected error for missing code parameter")
	}

	// Missing language parameter
	params = map[string]interface{}{
		"code": "print('test')",
	}

	_, err = tool.Handler(ctx, params)
	if err == nil {
		t.Error("Expected error for missing language parameter")
	}
}

// TestCodeExecutionToolDataAnalysis tests data analysis use case
func TestCodeExecutionToolDataAnalysis(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	tool := PythonOnlyCodeExecutionTool()
	ctx := context.Background()

	code := `
import statistics

data = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]
mean = statistics.mean(data)
median = statistics.median(data)
stdev = statistics.stdev(data)

print(f"Mean: {mean}")
print(f"Median: {median}")
print(f"Std Dev: {stdev:.2f}")
`

	params := map[string]interface{}{
		"code":     code,
		"language": "python",
	}

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("Code execution failed: %v", err)
	}

	resultMap := result.(map[string]interface{})
	if !resultMap["success"].(bool) {
		t.Errorf("Expected success=true. Stderr: %v", resultMap["stderr"])
	}

	stdout := resultMap["stdout"].(string)
	if !strings.Contains(stdout, "Mean: 5.5") {
		t.Errorf("Expected mean calculation, got: %s", stdout)
	}
	if !strings.Contains(stdout, "Median: 5.5") {
		t.Errorf("Expected median calculation, got: %s", stdout)
	}
}

// TestCodeExecutionToolCustomTimeout tests custom timeout parameter
func TestCodeExecutionToolCustomTimeout(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	tool := PythonOnlyCodeExecutionTool()
	ctx := context.Background()

	// Code that sleeps for 1 second
	code := `
import time
time.sleep(1)
print('Done')
`

	params := map[string]interface{}{
		"code":     code,
		"language": "python",
		"timeout":  float64(2), // 2 seconds - should succeed
	}

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("Code execution failed: %v", err)
	}

	resultMap := result.(map[string]interface{})
	if !resultMap["success"].(bool) {
		t.Errorf("Expected success=true with sufficient timeout. Stderr: %v", resultMap["stderr"])
	}
}

// TestCodeExecutionToolJavaScript tests JavaScript execution
func TestCodeExecutionToolJavaScript(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	config := DefaultCodeExecutionConfig()
	config.AllowedLanguages = []string{"javascript"}
	tool := CodeExecutionTool(config)

	ctx := context.Background()

	code := `
const fibonacci = (n) => {
    if (n <= 1) return n;
    let a = 0, b = 1;
    for (let i = 2; i <= n; i++) {
        [a, b] = [b, a + b];
    }
    return b;
};

const result = Array.from({length: 10}, (_, i) => fibonacci(i));
console.log(JSON.stringify(result));
`

	params := map[string]interface{}{
		"code":     code,
		"language": "javascript",
	}

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("Code execution failed: %v", err)
	}

	resultMap := result.(map[string]interface{})
	if !resultMap["success"].(bool) {
		t.Errorf("Expected success=true. Stderr: %v", resultMap["stderr"])
	}

	stdout := resultMap["stdout"].(string)
	if !strings.Contains(stdout, "[0,1,1,2,3,5,8,13,21,34]") {
		t.Errorf("Expected Fibonacci sequence, got: %s", stdout)
	}
}

// TestCodeExecutionToolMultiLanguage tests multiple language support
func TestCodeExecutionToolMultiLanguage(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	tool := SimpleCodeExecutionTool() // Supports python, javascript, go

	tests := []struct {
		name     string
		language string
		code     string
		expected string
	}{
		{
			name:     "Python",
			language: "python",
			code:     "print('Python works')",
			expected: "Python works",
		},
		{
			name:     "JavaScript",
			language: "javascript",
			code:     "console.log('JavaScript works')",
			expected: "JavaScript works",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			params := map[string]interface{}{
				"code":     tt.code,
				"language": tt.language,
			}

			result, err := tool.Handler(ctx, params)
			if err != nil {
				t.Fatalf("Code execution failed for %s: %v", tt.language, err)
			}

			resultMap := result.(map[string]interface{})
			if !resultMap["success"].(bool) {
				t.Errorf("Expected success for %s. Stderr: %v", tt.language, resultMap["stderr"])
			}

			stdout := resultMap["stdout"].(string)
			if !strings.Contains(stdout, tt.expected) {
				t.Errorf("Expected output to contain '%s', got: %s", tt.expected, stdout)
			}
		})
	}
}

// TestCodeExecutionToolExecutionTime tests execution time tracking
func TestCodeExecutionToolExecutionTime(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("Docker not available")
	}

	tool := PythonOnlyCodeExecutionTool()
	ctx := context.Background()

	code := `
import time
time.sleep(0.5)
print('Done')
`

	params := map[string]interface{}{
		"code":     code,
		"language": "python",
	}

	result, err := tool.Handler(ctx, params)
	if err != nil {
		t.Fatalf("Code execution failed: %v", err)
	}

	resultMap := result.(map[string]interface{})
	execTime := resultMap["execution_time"].(float64)

	if execTime < 0.5 {
		t.Errorf("Expected execution time >= 0.5s, got: %f", execTime)
	}

	if execTime > 5.0 {
		t.Errorf("Execution time unexpectedly long: %f", execTime)
	}
}

// TestDefaultCodeExecutionConfig tests default configuration
func TestDefaultCodeExecutionConfig(t *testing.T) {
	config := DefaultCodeExecutionConfig()

	if config.Timeout != 30*time.Second {
		t.Errorf("Expected timeout 30s, got %v", config.Timeout)
	}

	if config.MaxMemoryMB != 256 {
		t.Errorf("Expected max memory 256MB, got %d", config.MaxMemoryMB)
	}

	if len(config.AllowedLanguages) != 3 {
		t.Errorf("Expected 3 allowed languages, got %d", len(config.AllowedLanguages))
	}

	if config.AllowNetwork {
		t.Error("Expected network disabled by default")
	}


}

// TestCodeExecutionToolRegistry tests tool registration
func TestCodeExecutionToolRegistry(t *testing.T) {
	registry := NewToolRegistry()
	config := DefaultCodeExecutionConfig()

	err := RegisterCodeExecutionTools(registry, config)
	if err != nil {
		t.Fatalf("Failed to register code execution tool: %v", err)
	}

	tool, err := registry.Get("execute_code")
	if err != nil {
		t.Fatalf("Failed to get execute_code tool: %v", err)
	}

	if tool.Name != "execute_code" {
		t.Errorf("Expected tool name 'execute_code', got %s", tool.Name)
	}

	if !tool.Enabled {
		t.Error("Expected tool to be enabled")
	}
}

// BenchmarkCodeExecutionSimple benchmarks simple code execution
func BenchmarkCodeExecutionSimple(b *testing.B) {
	if !dockerAvailable() {
		b.Skip("Docker not available")
	}

	tool := PythonOnlyCodeExecutionTool()
	ctx := context.Background()

	params := map[string]interface{}{
		"code":     "print(2 + 2)",
		"language": "python",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = tool.Handler(ctx, params)
	}
}
