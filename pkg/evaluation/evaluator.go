// Package evaluation provides agent evaluation and testing framework
// Inspired by ADK's evaluation system with .evalset.json files
package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/agent"
)

// EvaluationSet represents a collection of test cases for agent evaluation
type EvaluationSet struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	AgentID     string     `json:"agent_id"`
	TestCases   []TestCase `json:"test_cases"`
	Config      EvalConfig `json:"config"`
}

// TestCase represents a single evaluation test case
type TestCase struct {
	ID              string                 `json:"id"`
	Name            string                 `json:"name"`
	Input           *agent.AgentInput      `json:"input"`
	ExpectedOutput  interface{}            `json:"expected_output"`
	Validators      []ValidatorConfig      `json:"validators"`
	Timeout         time.Duration          `json:"timeout"`
	Tags            []string               `json:"tags"`
	Metadata        map[string]interface{} `json:"metadata"`
}

// ValidatorConfig configures a validator
type ValidatorConfig struct {
	Type       ValidatorType      `json:"type"`
	Parameters map[string]interface{} `json:"parameters"`
}

// ValidatorType defines the type of validation
type ValidatorType string

const (
	ValidatorExactMatch    ValidatorType = "exact_match"
	ValidatorContains      ValidatorType = "contains"
	ValidatorRegex         ValidatorType = "regex"
	ValidatorCustom        ValidatorType = "custom"
	ValidatorSemantic      ValidatorType = "semantic"
	ValidatorLatency       ValidatorType = "latency"
	ValidatorTokenCount    ValidatorType = "token_count"
)

// EvalConfig holds evaluation configuration
type EvalConfig struct {
	Parallel        bool          `json:"parallel"`
	MaxConcurrency  int           `json:"max_concurrency"`
	StopOnFailure   bool          `json:"stop_on_failure"`
	RetryFailed     int           `json:"retry_failed"`
	Timeout         time.Duration `json:"timeout"`
}

// Validator validates agent outputs
type Validator interface {
	Validate(output *agent.AgentOutput, expected interface{}) (*ValidationResult, error)
	Name() string
}

// ValidationResult represents the result of a validation
type ValidationResult struct {
	Passed   bool
	Message  string
	Score    float64 // 0.0 to 1.0
	Details  map[string]interface{}
}

// TestResult represents the result of a single test case
type TestResult struct {
	TestCaseID      string
	TestCaseName    string
	Passed          bool
	Output          *agent.AgentOutput
	Validations     []*ValidationResult
	Duration        time.Duration
	Error           error
	Timestamp       time.Time
}

// EvaluationReport represents the complete evaluation results
type EvaluationReport struct {
	SetName         string
	TotalTests      int
	PassedTests     int
	FailedTests     int
	SkippedTests    int
	TotalDuration   time.Duration
	AverageLatency  time.Duration
	TestResults     []*TestResult
	Summary         *EvaluationSummary
	Timestamp       time.Time
}

// EvaluationSummary provides aggregate statistics
type EvaluationSummary struct {
	PassRate        float64
	AverageScore    float64
	TotalTokens     int
	AverageTokens   float64
	FastestTest     time.Duration
	SlowestTest     time.Duration
	ErrorRate       float64
}

// Evaluator runs evaluation sets against agents
type Evaluator struct {
	coordinator AgentCoordinator
	validators  map[ValidatorType]Validator
	mu          sync.RWMutex
}

// AgentCoordinator interface for submitting jobs
type AgentCoordinator interface {
	SubmitJob(ctx context.Context, agentID string, input *agent.AgentInput) (string, error)
	GetJobStatus(jobID string) (JobStatus, error)
}

// JobStatus represents job execution status
type JobStatus interface {
	IsComplete() bool
	GetOutput() *agent.AgentOutput
	GetError() error
}

// NewEvaluator creates a new evaluator
func NewEvaluator(coordinator AgentCoordinator) *Evaluator {
	eval := &Evaluator{
		coordinator: coordinator,
		validators:  make(map[ValidatorType]Validator),
	}

	// Register built-in validators
	eval.RegisterValidator(ValidatorExactMatch, &ExactMatchValidator{})
	eval.RegisterValidator(ValidatorContains, &ContainsValidator{})
	eval.RegisterValidator(ValidatorLatency, &LatencyValidator{})
	eval.RegisterValidator(ValidatorTokenCount, &TokenCountValidator{})

	return eval
}

// RegisterValidator registers a custom validator
func (e *Evaluator) RegisterValidator(vType ValidatorType, validator Validator) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.validators[vType] = validator
}

// LoadEvaluationSet loads an evaluation set from a JSON file
func LoadEvaluationSet(filepath string) (*EvaluationSet, error) {
	data, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var evalSet EvaluationSet
	if err := json.Unmarshal(data, &evalSet); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return &evalSet, nil
}

// RunEvaluation executes an evaluation set
func (e *Evaluator) RunEvaluation(ctx context.Context, evalSet *EvaluationSet) (*EvaluationReport, error) {
	report := &EvaluationReport{
		SetName:     evalSet.Name,
		TotalTests:  len(evalSet.TestCases),
		TestResults: make([]*TestResult, 0),
		Timestamp:   time.Now(),
	}

	startTime := time.Now()

	if evalSet.Config.Parallel {
		// Parallel execution
		report.TestResults = e.runParallel(ctx, evalSet)
	} else {
		// Sequential execution
		report.TestResults = e.runSequential(ctx, evalSet)
	}

	report.TotalDuration = time.Since(startTime)

	// Calculate summary
	report.Summary = e.calculateSummary(report)

	// Count results
	for _, result := range report.TestResults {
		if result.Passed {
			report.PassedTests++
		} else {
			report.FailedTests++
		}
	}

	return report, nil
}

// runSequential runs test cases sequentially
func (e *Evaluator) runSequential(ctx context.Context, evalSet *EvaluationSet) []*TestResult {
	results := make([]*TestResult, 0)

	for _, testCase := range evalSet.TestCases {
		result := e.runTestCase(ctx, evalSet.AgentID, &testCase)
		results = append(results, result)

		if evalSet.Config.StopOnFailure && !result.Passed {
			break
		}
	}

	return results
}

// runParallel runs test cases in parallel
func (e *Evaluator) runParallel(ctx context.Context, evalSet *EvaluationSet) []*TestResult {
	var wg sync.WaitGroup
	results := make([]*TestResult, len(evalSet.TestCases))

	maxConcurrency := evalSet.Config.MaxConcurrency
	if maxConcurrency == 0 {
		maxConcurrency = 10
	}

	semaphore := make(chan struct{}, maxConcurrency)

	for i, testCase := range evalSet.TestCases {
		wg.Add(1)
		go func(idx int, tc TestCase) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			results[idx] = e.runTestCase(ctx, evalSet.AgentID, &tc)
		}(i, testCase)
	}

	wg.Wait()
	return results
}

// runTestCase executes a single test case
func (e *Evaluator) runTestCase(ctx context.Context, agentID string, testCase *TestCase) *TestResult {
	result := &TestResult{
		TestCaseID:   testCase.ID,
		TestCaseName: testCase.Name,
		Timestamp:    time.Now(),
		Validations:  make([]*ValidationResult, 0),
	}

	startTime := time.Now()

	// Set timeout
	testCtx := ctx
	if testCase.Timeout > 0 {
		var cancel context.CancelFunc
		testCtx, cancel = context.WithTimeout(ctx, testCase.Timeout)
		defer cancel()
	}

	// Submit job
	jobID, err := e.coordinator.SubmitJob(testCtx, agentID, testCase.Input)
	if err != nil {
		result.Error = err
		result.Duration = time.Since(startTime)
		return result
	}

	// Wait for completion (simplified polling)
	// In production, use event-based waiting
	for {
		select {
		case <-testCtx.Done():
			result.Error = testCtx.Err()
			result.Duration = time.Since(startTime)
			return result
		default:
			status, err := e.coordinator.GetJobStatus(jobID)
			if err != nil {
				result.Error = err
				result.Duration = time.Since(startTime)
				return result
			}

			if status.IsComplete() {
				result.Duration = time.Since(startTime)
				result.Output = status.GetOutput()
				result.Error = status.GetError()

				if result.Error == nil {
					// Run validations
					result.Passed = e.runValidations(result, testCase)
				}

				return result
			}

			time.Sleep(100 * time.Millisecond)
		}
	}
}

// runValidations runs all validators for a test case
func (e *Evaluator) runValidations(result *TestResult, testCase *TestCase) bool {
	allPassed := true

	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, validatorCfg := range testCase.Validators {
		validator, exists := e.validators[validatorCfg.Type]
		if !exists {
			continue
		}

		validation, err := validator.Validate(result.Output, testCase.ExpectedOutput)
		if err != nil {
			validation = &ValidationResult{
				Passed:  false,
				Message: fmt.Sprintf("Validator error: %s", err.Error()),
			}
		}

		result.Validations = append(result.Validations, validation)

		if !validation.Passed {
			allPassed = false
		}
	}

	return allPassed
}

// calculateSummary generates evaluation summary statistics
func (e *Evaluator) calculateSummary(report *EvaluationReport) *EvaluationSummary {
	summary := &EvaluationSummary{}

	if report.TotalTests == 0 {
		return summary
	}

	summary.PassRate = float64(report.PassedTests) / float64(report.TotalTests)

	var totalScore float64
	var totalTokens int
	var totalDuration time.Duration
	var fastest, slowest time.Duration
	errorCount := 0

	for i, result := range report.TestResults {
		if i == 0 {
			fastest = result.Duration
			slowest = result.Duration
		}

		if result.Duration < fastest {
			fastest = result.Duration
		}
		if result.Duration > slowest {
			slowest = result.Duration
		}

		totalDuration += result.Duration

		if result.Error != nil {
			errorCount++
		}

		if result.Output != nil && result.Output.Metrics != nil {
			totalTokens += result.Output.Metrics.TokensUsed
		}

		for _, validation := range result.Validations {
			totalScore += validation.Score
		}
	}

	summary.AverageScore = totalScore / float64(len(report.TestResults))
	summary.TotalTokens = totalTokens
	summary.AverageTokens = float64(totalTokens) / float64(report.TotalTests)
	summary.FastestTest = fastest
	summary.SlowestTest = slowest
	summary.ErrorRate = float64(errorCount) / float64(report.TotalTests)

	return summary
}

// PrintReport prints a formatted evaluation report
func PrintReport(report *EvaluationReport) {
	separator := "============================================================"
	fmt.Println("\n" + separator)
	fmt.Printf("Evaluation Report: %s\n", report.SetName)
	fmt.Println(separator)

	fmt.Printf("\nTotal Tests: %d\n", report.TotalTests)
	fmt.Printf("Passed: %d (%.1f%%)\n", report.PassedTests, report.Summary.PassRate*100)
	fmt.Printf("Failed: %d\n", report.FailedTests)
	fmt.Printf("Total Duration: %s\n", report.TotalDuration)

	fmt.Println("\nSummary:")
	fmt.Printf("  Pass Rate: %.1f%%\n", report.Summary.PassRate*100)
	fmt.Printf("  Average Score: %.2f\n", report.Summary.AverageScore)
	fmt.Printf("  Average Tokens: %.0f\n", report.Summary.AverageTokens)
	fmt.Printf("  Fastest Test: %s\n", report.Summary.FastestTest)
	fmt.Printf("  Slowest Test: %s\n", report.Summary.SlowestTest)
	fmt.Printf("  Error Rate: %.1f%%\n", report.Summary.ErrorRate*100)

	fmt.Println("\nTest Results:")
	for _, result := range report.TestResults {
		status := "✓ PASS"
		if !result.Passed {
			status = "✗ FAIL"
		}

		fmt.Printf("  %s %s (%s)\n", status, result.TestCaseName, result.Duration)

		if result.Error != nil {
			fmt.Printf("    Error: %s\n", result.Error.Error())
		}

		for _, validation := range result.Validations {
			if !validation.Passed {
				fmt.Printf("    Validation failed: %s\n", validation.Message)
			}
		}
	}

	fmt.Println("\n" + separator + "\n")
}

// Built-in Validators

// ExactMatchValidator checks for exact match
type ExactMatchValidator struct{}

func (v *ExactMatchValidator) Name() string { return "exact_match" }

func (v *ExactMatchValidator) Validate(output *agent.AgentOutput, expected interface{}) (*ValidationResult, error) {
	if output == nil {
		return &ValidationResult{
			Passed:  false,
			Message: "Output is nil",
			Score:   0.0,
		}, nil
	}

	result := fmt.Sprintf("%v", output.Result)
	expectedStr := fmt.Sprintf("%v", expected)

	passed := result == expectedStr

	return &ValidationResult{
		Passed:  passed,
		Message: fmt.Sprintf("Expected '%s', got '%s'", expectedStr, result),
		Score:   map[bool]float64{true: 1.0, false: 0.0}[passed],
	}, nil
}

// ContainsValidator checks if output contains expected substring
type ContainsValidator struct{}

func (v *ContainsValidator) Name() string { return "contains" }

func (v *ContainsValidator) Validate(output *agent.AgentOutput, expected interface{}) (*ValidationResult, error) {
	if output == nil {
		return &ValidationResult{
			Passed:  false,
			Message: "Output is nil",
			Score:   0.0,
		}, nil
	}

	result := fmt.Sprintf("%v", output.Result)
	expectedStr := fmt.Sprintf("%v", expected)

	passed := contains(result, expectedStr)

	return &ValidationResult{
		Passed:  passed,
		Message: fmt.Sprintf("Expected to contain '%s'", expectedStr),
		Score:   map[bool]float64{true: 1.0, false: 0.0}[passed],
	}, nil
}

// LatencyValidator checks execution latency
type LatencyValidator struct{}

func (v *LatencyValidator) Name() string { return "latency" }

func (v *LatencyValidator) Validate(output *agent.AgentOutput, expected interface{}) (*ValidationResult, error) {
	if output == nil || output.Metrics == nil {
		return &ValidationResult{
			Passed:  false,
			Message: "No metrics available",
			Score:   0.0,
		}, nil
	}

	maxLatency, ok := expected.(time.Duration)
	if !ok {
		return nil, fmt.Errorf("expected duration, got %T", expected)
	}

	passed := output.Metrics.Duration <= maxLatency

	return &ValidationResult{
		Passed:  passed,
		Message: fmt.Sprintf("Latency: %s (max: %s)", output.Metrics.Duration, maxLatency),
		Score:   map[bool]float64{true: 1.0, false: 0.0}[passed],
		Details: map[string]interface{}{
			"actual": output.Metrics.Duration,
			"max":    maxLatency,
		},
	}, nil
}

// TokenCountValidator checks token usage
type TokenCountValidator struct{}

func (v *TokenCountValidator) Name() string { return "token_count" }

func (v *TokenCountValidator) Validate(output *agent.AgentOutput, expected interface{}) (*ValidationResult, error) {
	if output == nil || output.Metrics == nil {
		return &ValidationResult{
			Passed:  false,
			Message: "No metrics available",
			Score:   0.0,
		}, nil
	}

	maxTokens, ok := expected.(int)
	if !ok {
		return nil, fmt.Errorf("expected int, got %T", expected)
	}

	passed := output.Metrics.TokensUsed <= maxTokens

	return &ValidationResult{
		Passed:  passed,
		Message: fmt.Sprintf("Tokens: %d (max: %d)", output.Metrics.TokensUsed, maxTokens),
		Score:   map[bool]float64{true: 1.0, false: 0.0}[passed],
		Details: map[string]interface{}{
			"actual": output.Metrics.TokensUsed,
			"max":    maxTokens,
		},
	}, nil
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
