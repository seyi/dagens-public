package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/agent"
)

// Mock implementations for testing

type MockAgentCoordinator struct {
	mu          sync.Mutex
	jobs        map[string]*MockJobStatus
	submitDelay time.Duration
}

func NewMockCoordinator() *MockAgentCoordinator {
	return &MockAgentCoordinator{
		jobs:        make(map[string]*MockJobStatus),
		submitDelay: 10 * time.Millisecond,
	}
}

func (m *MockAgentCoordinator) SubmitJob(ctx context.Context, agentID string, input *agent.AgentInput) (string, error) {
	jobID := fmt.Sprintf("job-%d", time.Now().UnixNano())

	// Simulate processing delay
	time.Sleep(m.submitDelay)

	output := &agent.AgentOutput{
		TaskID: input.TaskID,
		Result: input.Instruction, // Echo the input
		Metrics: &agent.ExecutionMetrics{
			Duration:   50 * time.Millisecond,
			TokensUsed: 100,
		},
	}

	m.mu.Lock()
	m.jobs[jobID] = &MockJobStatus{
		complete: true,
		output:   output,
	}
	m.mu.Unlock()

	return jobID, nil
}

func (m *MockAgentCoordinator) GetJobStatus(jobID string) (JobStatus, error) {
	m.mu.Lock()
	job, exists := m.jobs[jobID]
	m.mu.Unlock()
	if !exists {
		return nil, fmt.Errorf("job not found: %s", jobID)
	}
	return job, nil
}

type MockJobStatus struct {
	complete bool
	output   *agent.AgentOutput
	err      error
}

func (m *MockJobStatus) IsComplete() bool {
	return m.complete
}

func (m *MockJobStatus) GetOutput() *agent.AgentOutput {
	return m.output
}

func (m *MockJobStatus) GetError() error {
	return m.err
}

// Tests

func TestNewEvaluator(t *testing.T) {
	coordinator := NewMockCoordinator()
	evaluator := NewEvaluator(coordinator)

	if evaluator == nil {
		t.Fatal("Evaluator should not be nil")
	}

	if evaluator.coordinator == nil {
		t.Error("Coordinator not set")
	}

	if len(evaluator.validators) == 0 {
		t.Error("No default validators registered")
	}
}

func TestValidatorRegistration(t *testing.T) {
	coordinator := NewMockCoordinator()
	evaluator := NewEvaluator(coordinator)

	customValidator := &ExactMatchValidator{}
	evaluator.RegisterValidator("custom", customValidator)

	evaluator.mu.RLock()
	_, exists := evaluator.validators["custom"]
	evaluator.mu.RUnlock()

	if !exists {
		t.Error("Custom validator not registered")
	}
}

func TestExactMatchValidator(t *testing.T) {
	validator := &ExactMatchValidator{}

	t.Run("Matching Output", func(t *testing.T) {
		output := &agent.AgentOutput{
			Result: "test result",
		}

		result, err := validator.Validate(output, "test result")
		if err != nil {
			t.Fatalf("Validation error: %v", err)
		}

		if !result.Passed {
			t.Error("Validation should pass for matching output")
		}

		if result.Score != 1.0 {
			t.Errorf("Expected score 1.0, got %f", result.Score)
		}
	})

	t.Run("Non-Matching Output", func(t *testing.T) {
		output := &agent.AgentOutput{
			Result: "actual result",
		}

		result, err := validator.Validate(output, "expected result")
		if err != nil {
			t.Fatalf("Validation error: %v", err)
		}

		if result.Passed {
			t.Error("Validation should fail for non-matching output")
		}

		if result.Score != 0.0 {
			t.Errorf("Expected score 0.0, got %f", result.Score)
		}
	})

	t.Run("Nil Output", func(t *testing.T) {
		result, err := validator.Validate(nil, "expected")
		if err != nil {
			t.Fatalf("Validation error: %v", err)
		}

		if result.Passed {
			t.Error("Validation should fail for nil output")
		}
	})
}

func TestContainsValidator(t *testing.T) {
	validator := &ContainsValidator{}

	t.Run("Contains Substring", func(t *testing.T) {
		output := &agent.AgentOutput{
			Result: "This is a test result",
		}

		result, err := validator.Validate(output, "test")
		if err != nil {
			t.Fatalf("Validation error: %v", err)
		}

		if !result.Passed {
			t.Error("Validation should pass when substring is present")
		}
	})

	t.Run("Does Not Contain Substring", func(t *testing.T) {
		output := &agent.AgentOutput{
			Result: "This is a result",
		}

		result, err := validator.Validate(output, "missing")
		if err != nil {
			t.Fatalf("Validation error: %v", err)
		}

		if result.Passed {
			t.Error("Validation should fail when substring is missing")
		}
	})
}

func TestLatencyValidator(t *testing.T) {
	validator := &LatencyValidator{}

	t.Run("Within Latency Limit", func(t *testing.T) {
		output := &agent.AgentOutput{
			Result: "result",
			Metrics: &agent.ExecutionMetrics{
				Duration: 50 * time.Millisecond,
			},
		}

		result, err := validator.Validate(output, 100*time.Millisecond)
		if err != nil {
			t.Fatalf("Validation error: %v", err)
		}

		if !result.Passed {
			t.Error("Validation should pass when within latency limit")
		}
	})

	t.Run("Exceeds Latency Limit", func(t *testing.T) {
		output := &agent.AgentOutput{
			Result: "result",
			Metrics: &agent.ExecutionMetrics{
				Duration: 150 * time.Millisecond,
			},
		}

		result, err := validator.Validate(output, 100*time.Millisecond)
		if err != nil {
			t.Fatalf("Validation error: %v", err)
		}

		if result.Passed {
			t.Error("Validation should fail when exceeding latency limit")
		}
	})

	t.Run("No Metrics", func(t *testing.T) {
		output := &agent.AgentOutput{
			Result: "result",
		}

		result, err := validator.Validate(output, 100*time.Millisecond)
		if err != nil {
			t.Fatalf("Validation error: %v", err)
		}

		if result.Passed {
			t.Error("Validation should fail when metrics are missing")
		}
	})
}

func TestTokenCountValidator(t *testing.T) {
	validator := &TokenCountValidator{}

	t.Run("Within Token Limit", func(t *testing.T) {
		output := &agent.AgentOutput{
			Result: "result",
			Metrics: &agent.ExecutionMetrics{
				TokensUsed: 50,
			},
		}

		result, err := validator.Validate(output, 100)
		if err != nil {
			t.Fatalf("Validation error: %v", err)
		}

		if !result.Passed {
			t.Error("Validation should pass when within token limit")
		}
	})

	t.Run("Exceeds Token Limit", func(t *testing.T) {
		output := &agent.AgentOutput{
			Result: "result",
			Metrics: &agent.ExecutionMetrics{
				TokensUsed: 150,
			},
		}

		result, err := validator.Validate(output, 100)
		if err != nil {
			t.Fatalf("Validation error: %v", err)
		}

		if result.Passed {
			t.Error("Validation should fail when exceeding token limit")
		}
	})
}

func TestRunTestCase(t *testing.T) {
	coordinator := NewMockCoordinator()
	evaluator := NewEvaluator(coordinator)
	ctx := context.Background()

	testCase := &TestCase{
		ID:   "test-1",
		Name: "Simple Test",
		Input: &agent.AgentInput{
			Instruction: "test prompt",
		},
		ExpectedOutput: "test prompt",
		Validators: []ValidatorConfig{
			{Type: ValidatorExactMatch},
		},
	}

	result := evaluator.runTestCase(ctx, "agent-1", testCase)

	if result == nil {
		t.Fatal("Result should not be nil")
	}

	if result.TestCaseID != "test-1" {
		t.Errorf("Expected test case ID 'test-1', got '%s'", result.TestCaseID)
	}

	if result.Error != nil {
		t.Errorf("Unexpected error: %v", result.Error)
	}

	if !result.Passed {
		t.Error("Test should pass")
	}
}

func TestRunSequential(t *testing.T) {
	coordinator := NewMockCoordinator()
	evaluator := NewEvaluator(coordinator)
	ctx := context.Background()

	evalSet := &EvaluationSet{
		AgentID: "agent-1",
		TestCases: []TestCase{
			{
				ID:   "test-1",
				Name: "Test 1",
				Input: &agent.AgentInput{
					Instruction: "prompt 1",
				},
				ExpectedOutput: "prompt 1",
				Validators: []ValidatorConfig{
					{Type: ValidatorExactMatch},
				},
			},
			{
				ID:   "test-2",
				Name: "Test 2",
				Input: &agent.AgentInput{
					Instruction: "prompt 2",
				},
				ExpectedOutput: "prompt 2",
				Validators: []ValidatorConfig{
					{Type: ValidatorExactMatch},
				},
			},
		},
		Config: EvalConfig{
			Parallel: false,
		},
	}

	results := evaluator.runSequential(ctx, evalSet)

	if len(results) != 2 {
		t.Errorf("Expected 2 results, got %d", len(results))
	}

	for i, result := range results {
		if result.Error != nil {
			t.Errorf("Test %d failed with error: %v", i, result.Error)
		}
	}
}

func TestRunParallel(t *testing.T) {
	coordinator := NewMockCoordinator()
	evaluator := NewEvaluator(coordinator)
	ctx := context.Background()

	evalSet := &EvaluationSet{
		AgentID: "agent-1",
		TestCases: []TestCase{
			{
				ID:   "test-1",
				Name: "Test 1",
				Input: &agent.AgentInput{
					Instruction: "prompt 1",
				},
				ExpectedOutput: "prompt 1",
				Validators: []ValidatorConfig{
					{Type: ValidatorExactMatch},
				},
			},
			{
				ID:   "test-2",
				Name: "Test 2",
				Input: &agent.AgentInput{
					Instruction: "prompt 2",
				},
				ExpectedOutput: "prompt 2",
				Validators: []ValidatorConfig{
					{Type: ValidatorExactMatch},
				},
			},
			{
				ID:   "test-3",
				Name: "Test 3",
				Input: &agent.AgentInput{
					Instruction: "prompt 3",
				},
				ExpectedOutput: "prompt 3",
				Validators: []ValidatorConfig{
					{Type: ValidatorExactMatch},
				},
			},
		},
		Config: EvalConfig{
			Parallel:       true,
			MaxConcurrency: 2,
		},
	}

	results := evaluator.runParallel(ctx, evalSet)

	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}

	for i, result := range results {
		if result == nil {
			t.Errorf("Result %d is nil", i)
		}
	}
}

func TestRunEvaluation(t *testing.T) {
	coordinator := NewMockCoordinator()
	evaluator := NewEvaluator(coordinator)
	ctx := context.Background()

	evalSet := &EvaluationSet{
		Name:    "Test Evaluation",
		AgentID: "agent-1",
		TestCases: []TestCase{
			{
				ID:   "test-1",
				Name: "Pass Test",
				Input: &agent.AgentInput{
					Instruction: "test",
				},
				ExpectedOutput: "test",
				Validators: []ValidatorConfig{
					{Type: ValidatorExactMatch},
				},
			},
			{
				ID:   "test-2",
				Name: "Fail Test",
				Input: &agent.AgentInput{
					Instruction: "actual",
				},
				ExpectedOutput: "expected",
				Validators: []ValidatorConfig{
					{Type: ValidatorExactMatch},
				},
			},
		},
		Config: EvalConfig{
			Parallel: false,
		},
	}

	report, err := evaluator.RunEvaluation(ctx, evalSet)
	if err != nil {
		t.Fatalf("Evaluation failed: %v", err)
	}

	if report.SetName != "Test Evaluation" {
		t.Error("Report name not set correctly")
	}

	if report.TotalTests != 2 {
		t.Errorf("Expected 2 total tests, got %d", report.TotalTests)
	}

	if report.PassedTests != 1 {
		t.Errorf("Expected 1 passed test, got %d", report.PassedTests)
	}

	if report.FailedTests != 1 {
		t.Errorf("Expected 1 failed test, got %d", report.FailedTests)
	}

	if report.Summary == nil {
		t.Error("Summary not generated")
	}
}

func TestCalculateSummary(t *testing.T) {
	coordinator := NewMockCoordinator()
	evaluator := NewEvaluator(coordinator)

	report := &EvaluationReport{
		TotalTests:  3,
		PassedTests: 2,
		FailedTests: 1,
		TestResults: []*TestResult{
			{
				Passed:   true,
				Duration: 50 * time.Millisecond,
				Output: &agent.AgentOutput{
					Metrics: &agent.ExecutionMetrics{
						TokensUsed: 100,
					},
				},
				Validations: []*ValidationResult{
					{Passed: true, Score: 1.0},
				},
			},
			{
				Passed:   true,
				Duration: 100 * time.Millisecond,
				Output: &agent.AgentOutput{
					Metrics: &agent.ExecutionMetrics{
						TokensUsed: 150,
					},
				},
				Validations: []*ValidationResult{
					{Passed: true, Score: 1.0},
				},
			},
			{
				Passed:   false,
				Duration: 75 * time.Millisecond,
				Error:    fmt.Errorf("test error"),
				Output: &agent.AgentOutput{
					Metrics: &agent.ExecutionMetrics{
						TokensUsed: 50,
					},
				},
				Validations: []*ValidationResult{
					{Passed: false, Score: 0.0},
				},
			},
		},
	}

	summary := evaluator.calculateSummary(report)

	if summary == nil {
		t.Fatal("Summary should not be nil")
	}

	expectedPassRate := 2.0 / 3.0
	if summary.PassRate != expectedPassRate {
		t.Errorf("Expected pass rate %.2f, got %.2f", expectedPassRate, summary.PassRate)
	}

	if summary.TotalTokens != 300 {
		t.Errorf("Expected 300 total tokens, got %d", summary.TotalTokens)
	}

	expectedAvgTokens := 300.0 / 3.0
	if summary.AverageTokens != expectedAvgTokens {
		t.Errorf("Expected average tokens %.0f, got %.0f", expectedAvgTokens, summary.AverageTokens)
	}

	if summary.FastestTest != 50*time.Millisecond {
		t.Errorf("Expected fastest test 50ms, got %s", summary.FastestTest)
	}

	if summary.SlowestTest != 100*time.Millisecond {
		t.Errorf("Expected slowest test 100ms, got %s", summary.SlowestTest)
	}

	expectedErrorRate := 1.0 / 3.0
	if summary.ErrorRate != expectedErrorRate {
		t.Errorf("Expected error rate %.2f, got %.2f", expectedErrorRate, summary.ErrorRate)
	}
}

func TestLoadEvaluationSet(t *testing.T) {
	// Create a temporary evaluation set file
	evalSet := &EvaluationSet{
		Name:        "Test Set",
		Description: "Test evaluation set",
		AgentID:     "agent-1",
		TestCases: []TestCase{
			{
				ID:   "test-1",
				Name: "Test Case 1",
				Input: &agent.AgentInput{
					Instruction: "test prompt",
				},
				ExpectedOutput: "expected output",
				Validators: []ValidatorConfig{
					{Type: ValidatorExactMatch},
				},
			},
		},
		Config: EvalConfig{
			Parallel:       false,
			MaxConcurrency: 1,
		},
	}

	data, err := json.MarshalIndent(evalSet, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal eval set: %v", err)
	}

	tmpFile := filepath.Join(os.TempDir(), "test-evalset.json")
	defer os.Remove(tmpFile)

	err = os.WriteFile(tmpFile, data, 0644)
	if err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}

	// Load the evaluation set
	loaded, err := LoadEvaluationSet(tmpFile)
	if err != nil {
		t.Fatalf("Failed to load evaluation set: %v", err)
	}

	if loaded.Name != "Test Set" {
		t.Errorf("Expected name 'Test Set', got '%s'", loaded.Name)
	}

	if len(loaded.TestCases) != 1 {
		t.Errorf("Expected 1 test case, got %d", len(loaded.TestCases))
	}

	if loaded.TestCases[0].ID != "test-1" {
		t.Error("Test case not loaded correctly")
	}
}

func TestLoadEvaluationSetErrors(t *testing.T) {
	t.Run("File Not Found", func(t *testing.T) {
		_, err := LoadEvaluationSet("/nonexistent/file.json")
		if err == nil {
			t.Error("Expected error for nonexistent file")
		}
	})

	t.Run("Invalid JSON", func(t *testing.T) {
		tmpFile := filepath.Join(os.TempDir(), "invalid-evalset.json")
		defer os.Remove(tmpFile)

		err := os.WriteFile(tmpFile, []byte("invalid json"), 0644)
		if err != nil {
			t.Fatalf("Failed to write temp file: %v", err)
		}

		_, err = LoadEvaluationSet(tmpFile)
		if err == nil {
			t.Error("Expected error for invalid JSON")
		}
	})
}

func TestStopOnFailure(t *testing.T) {
	coordinator := NewMockCoordinator()
	evaluator := NewEvaluator(coordinator)
	ctx := context.Background()

	evalSet := &EvaluationSet{
		AgentID: "agent-1",
		TestCases: []TestCase{
			{
				ID:   "test-1",
				Name: "Pass Test",
				Input: &agent.AgentInput{
					Instruction: "test",
				},
				ExpectedOutput: "test",
				Validators: []ValidatorConfig{
					{Type: ValidatorExactMatch},
				},
			},
			{
				ID:   "test-2",
				Name: "Fail Test",
				Input: &agent.AgentInput{
					Instruction: "actual",
				},
				ExpectedOutput: "expected",
				Validators: []ValidatorConfig{
					{Type: ValidatorExactMatch},
				},
			},
			{
				ID:   "test-3",
				Name: "Should Not Run",
				Input: &agent.AgentInput{
					Instruction: "test",
				},
				ExpectedOutput: "test",
				Validators: []ValidatorConfig{
					{Type: ValidatorExactMatch},
				},
			},
		},
		Config: EvalConfig{
			Parallel:      false,
			StopOnFailure: true,
		},
	}

	results := evaluator.runSequential(ctx, evalSet)

	// Should stop after the first failure (test-2)
	if len(results) != 2 {
		t.Errorf("Expected 2 results (stopped on failure), got %d", len(results))
	}
}

func TestValidatorHelperFunctions(t *testing.T) {
	t.Run("Contains Helper", func(t *testing.T) {
		if !contains("hello world", "world") {
			t.Error("Should contain 'world'")
		}

		if contains("hello", "world") {
			t.Error("Should not contain 'world'")
		}
	})

	t.Run("FindSubstring Helper", func(t *testing.T) {
		if !findSubstring("testing", "test") {
			t.Error("Should find 'test' in 'testing'")
		}

		if findSubstring("abc", "xyz") {
			t.Error("Should not find 'xyz' in 'abc'")
		}
	})
}
