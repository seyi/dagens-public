// Package a2a provides Server-Sent Events (SSE) support for A2A streaming
package a2a

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// SSEEvent represents a Server-Sent Event
type SSEEvent struct {
	ID    string      `json:"id,omitempty"`
	Event string      `json:"event,omitempty"`
	Data  interface{} `json:"data,omitempty"`
	Retry int         `json:"retry,omitempty"`
}

// SSEReader reads Server-Sent Events from an io.Reader
type SSEReader struct {
	reader *bufio.Reader
}

// NewSSEReader creates a new SSE reader
func NewSSEReader(r io.Reader) *SSEReader {
	return &SSEReader{
		reader: bufio.NewReader(r),
	}
}

// ReadEvent reads the next SSE event from the stream
// Returns nil, nil for empty events (keep-alive)
func (r *SSEReader) ReadEvent() (*SSEEvent, error) {
	event := &SSEEvent{}
	var dataLines []string

	for {
		line, err := r.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				// Process any remaining data before EOF
				if len(dataLines) > 0 {
					event.Data = parseSSEData(strings.Join(dataLines, "\n"))
					return event, nil
				}
				return nil, fmt.Errorf("EOF")
			}
			return nil, err
		}

		// Remove trailing newline/carriage return
		line = strings.TrimRight(line, "\r\n")

		// Empty line signals end of event
		if line == "" {
			if len(dataLines) == 0 && event.Event == "" && event.ID == "" {
				// Empty event (keep-alive), continue reading
				continue
			}
			// Parse accumulated data
			if len(dataLines) > 0 {
				event.Data = parseSSEData(strings.Join(dataLines, "\n"))
			}
			return event, nil
		}

		// Parse SSE field
		if strings.HasPrefix(line, ":") {
			// Comment line, ignore
			continue
		}

		colonIdx := strings.Index(line, ":")
		if colonIdx == -1 {
			// Field with no value
			continue
		}

		field := line[:colonIdx]
		value := strings.TrimPrefix(line[colonIdx+1:], " ")

		switch field {
		case "event":
			event.Event = value
		case "data":
			dataLines = append(dataLines, value)
		case "id":
			event.ID = value
		case "retry":
			// Parse retry value (milliseconds)
			var retry int
			fmt.Sscanf(value, "%d", &retry)
			event.Retry = retry
		}
	}
}

// parseSSEData attempts to parse data as JSON, falls back to string
func parseSSEData(data string) interface{} {
	// Try to parse as JSON object
	var jsonObj map[string]interface{}
	if err := json.Unmarshal([]byte(data), &jsonObj); err == nil {
		return jsonObj
	}

	// Try to parse as JSON array
	var jsonArr []interface{}
	if err := json.Unmarshal([]byte(data), &jsonArr); err == nil {
		return jsonArr
	}

	// Return as string
	return data
}

// SSEWriter writes Server-Sent Events to an http.ResponseWriter
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

// NewSSEWriter creates a new SSE writer
func NewSSEWriter(w http.ResponseWriter) (*SSEWriter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming not supported")
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	return &SSEWriter{
		w:       w,
		flusher: flusher,
	}, nil
}

// WriteEvent writes an SSE event to the stream
func (w *SSEWriter) WriteEvent(event *SSEEvent) error {
	var buf bytes.Buffer

	if event.ID != "" {
		fmt.Fprintf(&buf, "id: %s\n", event.ID)
	}

	if event.Event != "" {
		fmt.Fprintf(&buf, "event: %s\n", event.Event)
	}

	if event.Data != nil {
		// Marshal data to JSON if it's not a string
		var dataStr string
		switch v := event.Data.(type) {
		case string:
			dataStr = v
		default:
			jsonData, err := json.Marshal(v)
			if err != nil {
				return fmt.Errorf("failed to marshal event data: %w", err)
			}
			dataStr = string(jsonData)
		}

		// Split data by newlines and prefix each line with "data: "
		for _, line := range strings.Split(dataStr, "\n") {
			fmt.Fprintf(&buf, "data: %s\n", line)
		}
	}

	if event.Retry > 0 {
		fmt.Fprintf(&buf, "retry: %d\n", event.Retry)
	}

	// End event with blank line
	buf.WriteString("\n")

	_, err := w.w.Write(buf.Bytes())
	if err != nil {
		return err
	}

	w.flusher.Flush()
	return nil
}

// WriteData writes a simple data-only event
func (w *SSEWriter) WriteData(data interface{}) error {
	return w.WriteEvent(&SSEEvent{Data: data})
}

// WriteComment writes an SSE comment (for keep-alive)
func (w *SSEWriter) WriteComment(comment string) error {
	_, err := fmt.Fprintf(w.w, ": %s\n\n", comment)
	if err != nil {
		return err
	}
	w.flusher.Flush()
	return nil
}

// Close writes a done event and closes the stream
func (w *SSEWriter) Close() error {
	return w.WriteEvent(&SSEEvent{
		Event: "done",
		Data:  map[string]interface{}{"status": "complete"},
	})
}

// SSEStreamHandler handles SSE streaming for A2A server
type SSEStreamHandler struct {
	onRequest func(ctx context.Context, req *InvocationRequest) (<-chan interface{}, error)
}

// NewSSEStreamHandler creates a new SSE stream handler
func NewSSEStreamHandler(handler func(ctx context.Context, req *InvocationRequest) (<-chan interface{}, error)) *SSEStreamHandler {
	return &SSEStreamHandler{
		onRequest: handler,
	}
}

// ServeHTTP implements http.Handler for SSE streaming
func (h *SSEStreamHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only accept POST requests
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request
	var req InvocationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Create SSE writer
	writer, err := NewSSEWriter(w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Create context with cancellation
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Start streaming
	dataChan, err := h.onRequest(ctx, &req)
	if err != nil {
		writer.WriteEvent(&SSEEvent{
			Event: "error",
			Data: map[string]interface{}{
				"error": err.Error(),
			},
		})
		return
	}

	// Send initial event
	writer.WriteEvent(&SSEEvent{
		Event: "start",
		Data: map[string]interface{}{
			"request_id": req.ID,
			"timestamp":  time.Now().Format(time.RFC3339),
		},
	})

	// Stream data chunks
	sequence := 0
	for data := range dataChan {
		select {
		case <-ctx.Done():
			return
		default:
			sequence++
			writer.WriteEvent(&SSEEvent{
				ID:    fmt.Sprintf("%s-%d", req.ID, sequence),
				Event: "data",
				Data:  data,
			})
		}
	}

	// Send completion event
	writer.Close()
}

// StreamingAgent interface for agents that support streaming
type StreamingAgent interface {
	ExecuteStream(ctx context.Context, input *AgentStreamInput) (<-chan *AgentStreamOutput, error)
}

// AgentStreamInput represents input for streaming agent execution
type AgentStreamInput struct {
	Instruction string                 `json:"instruction"`
	Context     map[string]interface{} `json:"context,omitempty"`
	TaskID      string                 `json:"task_id,omitempty"`
}

// AgentStreamOutput represents a chunk of streaming output
type AgentStreamOutput struct {
	Type      StreamOutputType       `json:"type"`
	Content   string                 `json:"content,omitempty"`
	Data      interface{}            `json:"data,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

// StreamOutputType defines types of stream output
type StreamOutputType string

const (
	StreamOutputText      StreamOutputType = "text"
	StreamOutputThinking  StreamOutputType = "thinking"
	StreamOutputToolCall  StreamOutputType = "tool_call"
	StreamOutputToolResult StreamOutputType = "tool_result"
	StreamOutputComplete  StreamOutputType = "complete"
	StreamOutputError     StreamOutputType = "error"
)
