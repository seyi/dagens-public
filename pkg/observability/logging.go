// Package observability provides structured JSON logging for production systems.
package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"
)

// LogLevel represents the severity of a log message
type LogLevel int

const (
	LogLevelDebug LogLevel = iota
	LogLevelInfo
	LogLevelWarn
	LogLevelError
	LogLevelFatal
)

func (l LogLevel) String() string {
	return [...]string{"DEBUG", "INFO", "WARN", "ERROR", "FATAL"}[l]
}

// ParseLogLevel parses a string into a LogLevel
func ParseLogLevel(s string) LogLevel {
	switch s {
	case "DEBUG", "debug":
		return LogLevelDebug
	case "INFO", "info":
		return LogLevelInfo
	case "WARN", "warn", "WARNING", "warning":
		return LogLevelWarn
	case "ERROR", "error":
		return LogLevelError
	case "FATAL", "fatal":
		return LogLevelFatal
	default:
		return LogLevelInfo
	}
}

// LogField represents a key-value pair in structured logging
type LogField struct {
	Key   string
	Value interface{}
}

// Field creates a new log field
func Field(key string, value interface{}) LogField {
	return LogField{Key: key, Value: value}
}

// LogEntry represents a single structured log entry
type LogEntry struct {
	Timestamp string                 `json:"timestamp"`
	Level     string                 `json:"level"`
	Message   string                 `json:"message"`
	Service   string                 `json:"service,omitempty"`
	Version   string                 `json:"version,omitempty"`
	TraceID   string                 `json:"trace_id,omitempty"`
	SpanID    string                 `json:"span_id,omitempty"`
	Caller    string                 `json:"caller,omitempty"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
}

// LoggerConfig configures the structured logger
type LoggerConfig struct {
	// Output destination (defaults to stdout)
	Output io.Writer

	// Minimum log level (defaults to Info)
	Level LogLevel

	// Service name for all log entries
	Service string

	// Version for all log entries
	Version string

	// Include caller information (file:line)
	IncludeCaller bool

	// Pretty print JSON (for development)
	PrettyPrint bool

	// Additional fields to include in all log entries
	DefaultFields map[string]interface{}
}

// DefaultLoggerConfig returns production-ready logger configuration
func DefaultLoggerConfig() LoggerConfig {
	return LoggerConfig{
		Output:        os.Stdout,
		Level:         LogLevelInfo,
		Service:       "spark-ai-agents",
		Version:       "0.1.0",
		IncludeCaller: false, // Disabled for performance in production
		PrettyPrint:   false, // Compact JSON for production
	}
}

// Logger is a structured JSON logger
type Logger struct {
	config    LoggerConfig
	mu        sync.Mutex
	encoder   *json.Encoder
	entryPool sync.Pool
}

// NewLogger creates a new structured logger
func NewLogger(config LoggerConfig) *Logger {
	if config.Output == nil {
		config.Output = os.Stdout
	}

	l := &Logger{
		config:  config,
		encoder: json.NewEncoder(config.Output),
	}

	if config.PrettyPrint {
		l.encoder.SetIndent("", "  ")
	}

	// Pool for log entries to reduce allocations
	l.entryPool = sync.Pool{
		New: func() interface{} {
			return &LogEntry{
				Fields: make(map[string]interface{}, 8),
			}
		},
	}

	return l
}

// log writes a log entry at the specified level
func (l *Logger) log(level LogLevel, message string, fields ...LogField) {
	if level < l.config.Level {
		return
	}

	// Get entry from pool
	entry := l.entryPool.Get().(*LogEntry)
	defer func() {
		// Clear fields for reuse
		for k := range entry.Fields {
			delete(entry.Fields, k)
		}
		l.entryPool.Put(entry)
	}()

	// Populate entry
	entry.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	entry.Level = level.String()
	entry.Message = message
	entry.Service = l.config.Service
	entry.Version = l.config.Version

	// Add caller info if enabled
	if l.config.IncludeCaller {
		_, file, line, ok := runtime.Caller(2)
		if ok {
			entry.Caller = fmt.Sprintf("%s:%d", file, line)
		}
	}

	// Add default fields
	for k, v := range l.config.DefaultFields {
		entry.Fields[k] = v
	}

	// Add provided fields
	for _, f := range fields {
		entry.Fields[f.Key] = f.Value
	}

	// Encode and write
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.encoder.Encode(entry); err != nil {
		// Fall back to fmt if JSON encoding fails
		fmt.Fprintf(os.Stderr, "logging error: %v, entry: %+v\n", err, entry)
	}
}

// Debug logs at debug level
func (l *Logger) Debug(message string, fields ...LogField) {
	l.log(LogLevelDebug, message, fields...)
}

// Info logs at info level
func (l *Logger) Info(message string, fields ...LogField) {
	l.log(LogLevelInfo, message, fields...)
}

// Warn logs at warn level
func (l *Logger) Warn(message string, fields ...LogField) {
	l.log(LogLevelWarn, message, fields...)
}

// Error logs at error level
func (l *Logger) Error(message string, fields ...LogField) {
	l.log(LogLevelError, message, fields...)
}

// Fatal logs at fatal level and exits
func (l *Logger) Fatal(message string, fields ...LogField) {
	l.log(LogLevelFatal, message, fields...)
	os.Exit(1)
}

// WithFields creates a child logger with additional default fields
func (l *Logger) WithFields(fields ...LogField) *Logger {
	newConfig := l.config
	newConfig.DefaultFields = make(map[string]interface{})

	// Copy existing default fields
	for k, v := range l.config.DefaultFields {
		newConfig.DefaultFields[k] = v
	}

	// Add new fields
	for _, f := range fields {
		newConfig.DefaultFields[f.Key] = f.Value
	}

	return NewLogger(newConfig)
}

// WithService creates a child logger with a different service name
func (l *Logger) WithService(service string) *Logger {
	newConfig := l.config
	newConfig.Service = service
	return NewLogger(newConfig)
}

// Context-aware logging

// ContextKey is the type for context keys
type ContextKey string

const (
	// TraceIDKey is the context key for trace IDs
	TraceIDKey ContextKey = "trace_id"
	// SpanIDKey is the context key for span IDs
	SpanIDKey ContextKey = "span_id"
	// RequestIDKey is the context key for request IDs
	RequestIDKey ContextKey = "request_id"
)

// WithContext extracts trace context and adds it to log fields
func (l *Logger) WithContext(ctx context.Context) *Logger {
	var fields []LogField

	if traceID, ok := ctx.Value(TraceIDKey).(string); ok {
		fields = append(fields, Field("trace_id", traceID))
	}

	if spanID, ok := ctx.Value(SpanIDKey).(string); ok {
		fields = append(fields, Field("span_id", spanID))
	}

	if requestID, ok := ctx.Value(RequestIDKey).(string); ok {
		fields = append(fields, Field("request_id", requestID))
	}

	if len(fields) > 0 {
		return l.WithFields(fields...)
	}

	return l
}

// DebugCtx logs at debug level with context
func (l *Logger) DebugCtx(ctx context.Context, message string, fields ...LogField) {
	l.WithContext(ctx).Debug(message, fields...)
}

// InfoCtx logs at info level with context
func (l *Logger) InfoCtx(ctx context.Context, message string, fields ...LogField) {
	l.WithContext(ctx).Info(message, fields...)
}

// WarnCtx logs at warn level with context
func (l *Logger) WarnCtx(ctx context.Context, message string, fields ...LogField) {
	l.WithContext(ctx).Warn(message, fields...)
}

// ErrorCtx logs at error level with context
func (l *Logger) ErrorCtx(ctx context.Context, message string, fields ...LogField) {
	l.WithContext(ctx).Error(message, fields...)
}

// Specialized agent logging helpers

// AgentLogger is a logger specialized for agent operations
type AgentLogger struct {
	*Logger
	agentName string
	agentID   string
}

// NewAgentLogger creates a logger for a specific agent
func NewAgentLogger(base *Logger, agentName, agentID string) *AgentLogger {
	return &AgentLogger{
		Logger:    base.WithFields(Field("agent_name", agentName), Field("agent_id", agentID)),
		agentName: agentName,
		agentID:   agentID,
	}
}

// ExecutionStarted logs the start of agent execution
func (al *AgentLogger) ExecutionStarted(taskID string, instruction string) {
	al.Info("agent_execution_started",
		Field("task_id", taskID),
		Field("instruction_preview", truncate(instruction, 100)),
	)
}

// ExecutionCompleted logs successful agent execution
func (al *AgentLogger) ExecutionCompleted(taskID string, duration time.Duration) {
	al.Info("agent_execution_completed",
		Field("task_id", taskID),
		Field("duration_ms", duration.Milliseconds()),
	)
}

// ExecutionFailed logs failed agent execution
func (al *AgentLogger) ExecutionFailed(taskID string, err error, duration time.Duration) {
	al.Error("agent_execution_failed",
		Field("task_id", taskID),
		Field("error", err.Error()),
		Field("duration_ms", duration.Milliseconds()),
	)
}

// ToolCalled logs a tool invocation
func (al *AgentLogger) ToolCalled(toolName string, duration time.Duration, err error) {
	if err != nil {
		al.Warn("agent_tool_call_failed",
			Field("tool_name", toolName),
			Field("duration_ms", duration.Milliseconds()),
			Field("error", err.Error()),
		)
	} else {
		al.Debug("agent_tool_called",
			Field("tool_name", toolName),
			Field("duration_ms", duration.Milliseconds()),
		)
	}
}

// LLMCalled logs an LLM API call
func (al *AgentLogger) LLMCalled(provider string, model string, tokens int, duration time.Duration, err error) {
	if err != nil {
		al.Warn("agent_llm_call_failed",
			Field("provider", provider),
			Field("model", model),
			Field("duration_ms", duration.Milliseconds()),
			Field("error", err.Error()),
		)
	} else {
		al.Debug("agent_llm_called",
			Field("provider", provider),
			Field("model", model),
			Field("tokens", tokens),
			Field("duration_ms", duration.Milliseconds()),
		)
	}
}

// RetryAttempt logs a retry attempt
func (al *AgentLogger) RetryAttempt(attempt int, err error, nextDelay time.Duration) {
	al.Warn("agent_retry_attempt",
		Field("attempt", attempt),
		Field("error", err.Error()),
		Field("next_delay_ms", nextDelay.Milliseconds()),
	)
}

// CircuitBreakerTripped logs circuit breaker state change
func (al *AgentLogger) CircuitBreakerTripped(state string) {
	al.Warn("agent_circuit_breaker_state",
		Field("state", state),
	)
}

// truncate truncates a string to max length
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// Global logger instance
var (
	globalLogger *Logger
	loggerOnce   sync.Once
)

// GetLogger returns the global logger instance
func GetLogger() *Logger {
	loggerOnce.Do(func() {
		config := DefaultLoggerConfig()

		// Check for environment overrides
		if level := os.Getenv("LOG_LEVEL"); level != "" {
			config.Level = ParseLogLevel(level)
		}

		if format := os.Getenv("LOG_FORMAT"); format == "pretty" {
			config.PrettyPrint = true
		}

		if service := os.Getenv("SERVICE_NAME"); service != "" {
			config.Service = service
		}

		if version := os.Getenv("SERVICE_VERSION"); version != "" {
			config.Version = version
		}

		globalLogger = NewLogger(config)
	})
	return globalLogger
}

// SetGlobalLogger sets the global logger instance
func SetGlobalLogger(logger *Logger) {
	globalLogger = logger
}

// Convenience functions using global logger

// Debug logs at debug level using global logger
func Debug(message string, fields ...LogField) {
	GetLogger().Debug(message, fields...)
}

// Info logs at info level using global logger
func Info(message string, fields ...LogField) {
	GetLogger().Info(message, fields...)
}

// Warn logs at warn level using global logger
func Warn(message string, fields ...LogField) {
	GetLogger().Warn(message, fields...)
}

// Error logs at error level using global logger
func Error(message string, fields ...LogField) {
	GetLogger().Error(message, fields...)
}

// Fatal logs at fatal level and exits using global logger
func Fatal(message string, fields ...LogField) {
	GetLogger().Fatal(message, fields...)
}

// LogRequest is middleware-style logging for HTTP requests
type RequestLogger struct {
	*Logger
}

// NewRequestLogger creates a request logger
func NewRequestLogger(base *Logger) *RequestLogger {
	return &RequestLogger{Logger: base}
}

// LogHTTPRequest logs an HTTP request
func (rl *RequestLogger) LogHTTPRequest(method, path string, statusCode int, duration time.Duration, err error) {
	fields := []LogField{
		Field("method", method),
		Field("path", path),
		Field("status_code", statusCode),
		Field("duration_ms", duration.Milliseconds()),
	}

	if err != nil {
		fields = append(fields, Field("error", err.Error()))
	}

	if statusCode >= 500 {
		rl.Error("http_request", fields...)
	} else if statusCode >= 400 {
		rl.Warn("http_request", fields...)
	} else {
		rl.Info("http_request", fields...)
	}
}

// LogGRPCRequest logs a gRPC request
func (rl *RequestLogger) LogGRPCRequest(method string, code int32, duration time.Duration, err error) {
	fields := []LogField{
		Field("method", method),
		Field("code", code),
		Field("duration_ms", duration.Milliseconds()),
	}

	if err != nil {
		fields = append(fields, Field("error", err.Error()))
	}

	if code != 0 { // Non-OK status
		rl.Warn("grpc_request", fields...)
	} else {
		rl.Info("grpc_request", fields...)
	}
}
