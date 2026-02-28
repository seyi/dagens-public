package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLogger_JSONOutput(t *testing.T) {
	buf := &bytes.Buffer{}
	config := LoggerConfig{
		Output:  buf,
		Level:   LogLevelDebug,
		Service: "test-service",
		Version: "1.0.0",
	}

	logger := NewLogger(config)
	logger.Info("test message", Field("key", "value"))

	// Parse JSON output
	var entry LogEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if entry.Level != "INFO" {
		t.Errorf("Expected level INFO, got %s", entry.Level)
	}

	if entry.Message != "test message" {
		t.Errorf("Expected message 'test message', got %s", entry.Message)
	}

	if entry.Service != "test-service" {
		t.Errorf("Expected service 'test-service', got %s", entry.Service)
	}

	if entry.Fields["key"] != "value" {
		t.Errorf("Expected field key=value, got %v", entry.Fields["key"])
	}
}

func TestLogger_LogLevels(t *testing.T) {
	// Test: logger configured at minLevel, calling method at callLevel
	// Should log if callLevel >= minLevel
	tests := []struct {
		name      string
		minLevel  LogLevel // Logger's minimum level
		callLevel LogLevel // Level of log call being made
		shouldLog bool
	}{
		{"debug call at debug min", LogLevelDebug, LogLevelDebug, true},
		{"debug call at info min", LogLevelInfo, LogLevelDebug, false},
		{"info call at info min", LogLevelInfo, LogLevelInfo, true},
		{"info call at warn min", LogLevelWarn, LogLevelInfo, false},
		{"warn call at info min", LogLevelInfo, LogLevelWarn, true},
		{"error call at warn min", LogLevelWarn, LogLevelError, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			logger := NewLogger(LoggerConfig{
				Output: buf,
				Level:  tt.minLevel,
			})

			switch tt.callLevel {
			case LogLevelDebug:
				logger.Debug("test")
			case LogLevelInfo:
				logger.Info("test")
			case LogLevelWarn:
				logger.Warn("test")
			case LogLevelError:
				logger.Error("test")
			}

			hasOutput := buf.Len() > 0
			if hasOutput != tt.shouldLog {
				t.Errorf("Expected shouldLog=%v, got output=%v", tt.shouldLog, hasOutput)
			}
		})
	}
}

func TestLogger_WithFields(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewLogger(LoggerConfig{
		Output: buf,
		Level:  LogLevelDebug,
	})

	childLogger := logger.WithFields(Field("component", "test"))
	childLogger.Info("message")

	var entry LogEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if entry.Fields["component"] != "test" {
		t.Errorf("Expected field component=test, got %v", entry.Fields["component"])
	}
}

func TestLogger_WithContext(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := NewLogger(LoggerConfig{
		Output: buf,
		Level:  LogLevelDebug,
	})

	ctx := context.WithValue(context.Background(), TraceIDKey, "trace-123")
	ctx = context.WithValue(ctx, SpanIDKey, "span-456")

	logger.InfoCtx(ctx, "contextual message")

	var entry LogEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if entry.Fields["trace_id"] != "trace-123" {
		t.Errorf("Expected trace_id=trace-123, got %v", entry.Fields["trace_id"])
	}

	if entry.Fields["span_id"] != "span-456" {
		t.Errorf("Expected span_id=span-456, got %v", entry.Fields["span_id"])
	}
}

func TestAgentLogger(t *testing.T) {
	buf := &bytes.Buffer{}
	baseLogger := NewLogger(LoggerConfig{
		Output: buf,
		Level:  LogLevelDebug,
	})

	agentLogger := NewAgentLogger(baseLogger, "test-agent", "agent-123")
	agentLogger.ExecutionStarted("task-1", "Process this data")

	var entry LogEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if entry.Message != "agent_execution_started" {
		t.Errorf("Expected message 'agent_execution_started', got %s", entry.Message)
	}

	if entry.Fields["agent_name"] != "test-agent" {
		t.Errorf("Expected agent_name=test-agent, got %v", entry.Fields["agent_name"])
	}

	if entry.Fields["task_id"] != "task-1" {
		t.Errorf("Expected task_id=task-1, got %v", entry.Fields["task_id"])
	}
}

func TestAgentLogger_ExecutionCompleted(t *testing.T) {
	buf := &bytes.Buffer{}
	baseLogger := NewLogger(LoggerConfig{
		Output: buf,
		Level:  LogLevelDebug,
	})

	agentLogger := NewAgentLogger(baseLogger, "test-agent", "agent-123")
	agentLogger.ExecutionCompleted("task-1", 150*time.Millisecond)

	var entry LogEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if entry.Message != "agent_execution_completed" {
		t.Errorf("Expected message 'agent_execution_completed', got %s", entry.Message)
	}

	durationMS, ok := entry.Fields["duration_ms"].(float64)
	if !ok || durationMS != 150 {
		t.Errorf("Expected duration_ms=150, got %v", entry.Fields["duration_ms"])
	}
}

func TestAgentLogger_ExecutionFailed(t *testing.T) {
	buf := &bytes.Buffer{}
	baseLogger := NewLogger(LoggerConfig{
		Output: buf,
		Level:  LogLevelDebug,
	})

	agentLogger := NewAgentLogger(baseLogger, "test-agent", "agent-123")
	agentLogger.ExecutionFailed("task-1", context.DeadlineExceeded, 5*time.Second)

	var entry LogEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if entry.Level != "ERROR" {
		t.Errorf("Expected level ERROR, got %s", entry.Level)
	}

	if entry.Fields["error"] != "context deadline exceeded" {
		t.Errorf("Expected error message, got %v", entry.Fields["error"])
	}
}

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected LogLevel
	}{
		{"DEBUG", LogLevelDebug},
		{"debug", LogLevelDebug},
		{"INFO", LogLevelInfo},
		{"info", LogLevelInfo},
		{"WARN", LogLevelWarn},
		{"warn", LogLevelWarn},
		{"WARNING", LogLevelWarn},
		{"warning", LogLevelWarn},
		{"ERROR", LogLevelError},
		{"error", LogLevelError},
		{"FATAL", LogLevelFatal},
		{"fatal", LogLevelFatal},
		{"unknown", LogLevelInfo}, // Default
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := ParseLogLevel(tt.input)
			if result != tt.expected {
				t.Errorf("ParseLogLevel(%s) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestRequestLogger(t *testing.T) {
	buf := &bytes.Buffer{}
	baseLogger := NewLogger(LoggerConfig{
		Output: buf,
		Level:  LogLevelDebug,
	})

	reqLogger := NewRequestLogger(baseLogger)
	reqLogger.LogHTTPRequest("GET", "/api/v1/agents", 200, 50*time.Millisecond, nil)

	var entry LogEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if entry.Message != "http_request" {
		t.Errorf("Expected message 'http_request', got %s", entry.Message)
	}

	if entry.Fields["method"] != "GET" {
		t.Errorf("Expected method=GET, got %v", entry.Fields["method"])
	}

	if entry.Fields["path"] != "/api/v1/agents" {
		t.Errorf("Expected path=/api/v1/agents, got %v", entry.Fields["path"])
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"short", 10, "short"},
		{"exactly10x", 10, "exactly10x"},
		{"this is a long string", 10, "this is a ..."},
	}

	for _, tt := range tests {
		result := truncate(tt.input, tt.maxLen)
		if result != tt.expected {
			t.Errorf("truncate(%q, %d) = %q, expected %q", tt.input, tt.maxLen, result, tt.expected)
		}
	}
}

func TestLogger_PrettyPrint(t *testing.T) {
	buf := &bytes.Buffer{}
	config := LoggerConfig{
		Output:      buf,
		Level:       LogLevelDebug,
		PrettyPrint: true,
	}

	logger := NewLogger(config)
	logger.Info("test message")

	// Pretty printed JSON should have newlines
	output := buf.String()
	if !strings.Contains(output, "\n") {
		t.Error("Expected pretty printed output with newlines")
	}
}

func TestLogLevelString(t *testing.T) {
	tests := []struct {
		level    LogLevel
		expected string
	}{
		{LogLevelDebug, "DEBUG"},
		{LogLevelInfo, "INFO"},
		{LogLevelWarn, "WARN"},
		{LogLevelError, "ERROR"},
		{LogLevelFatal, "FATAL"},
	}

	for _, tt := range tests {
		if tt.level.String() != tt.expected {
			t.Errorf("LogLevel.String() = %s, expected %s", tt.level.String(), tt.expected)
		}
	}
}
