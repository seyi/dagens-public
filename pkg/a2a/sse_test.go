package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/stretchr/testify/assert"
)

func TestSSEReader_ReadEvent(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected *SSEEvent
		wantErr  bool
	}{
		{
			name:  "simple data event",
			input: "data: hello world\n\n",
			expected: &SSEEvent{
				Data: "hello world",
			},
		},
		{
			name:  "event with type",
			input: "event: message\ndata: hello\n\n",
			expected: &SSEEvent{
				Event: "message",
				Data:  "hello",
			},
		},
		{
			name:  "event with id",
			input: "id: 123\ndata: test\n\n",
			expected: &SSEEvent{
				ID:   "123",
				Data: "test",
			},
		},
		{
			name:  "json data",
			input: `data: {"key": "value"}` + "\n\n",
			expected: &SSEEvent{
				Data: map[string]interface{}{"key": "value"},
			},
		},
		{
			name:  "multi-line data",
			input: "data: line1\ndata: line2\n\n",
			expected: &SSEEvent{
				Data: "line1\nline2",
			},
		},
		{
			name:  "event with retry",
			input: "retry: 3000\ndata: test\n\n",
			expected: &SSEEvent{
				Data:  "test",
				Retry: 3000,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := NewSSEReader(strings.NewReader(tt.input))
			event, err := reader.ReadEvent()

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tt.expected.Event, event.Event)
			assert.Equal(t, tt.expected.ID, event.ID)
			assert.Equal(t, tt.expected.Retry, event.Retry)

			// Compare data (may be string or map)
			if expectedMap, ok := tt.expected.Data.(map[string]interface{}); ok {
				actualMap, ok := event.Data.(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, expectedMap, actualMap)
			} else {
				assert.Equal(t, tt.expected.Data, event.Data)
			}
		})
	}
}

func TestSSEReader_MultipleEvents(t *testing.T) {
	input := "event: start\ndata: first\n\nevent: data\ndata: second\n\nevent: done\ndata: third\n\n"

	reader := NewSSEReader(strings.NewReader(input))

	// Read first event
	event1, err := reader.ReadEvent()
	assert.NoError(t, err)
	assert.Equal(t, "start", event1.Event)
	assert.Equal(t, "first", event1.Data)

	// Read second event
	event2, err := reader.ReadEvent()
	assert.NoError(t, err)
	assert.Equal(t, "data", event2.Event)
	assert.Equal(t, "second", event2.Data)

	// Read third event
	event3, err := reader.ReadEvent()
	assert.NoError(t, err)
	assert.Equal(t, "done", event3.Event)
	assert.Equal(t, "third", event3.Data)

	// Read EOF
	_, err = reader.ReadEvent()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "EOF")
}

func TestSSEReader_Comments(t *testing.T) {
	input := ": this is a comment\ndata: actual data\n\n"

	reader := NewSSEReader(strings.NewReader(input))
	event, err := reader.ReadEvent()

	assert.NoError(t, err)
	assert.Equal(t, "actual data", event.Data)
}

func TestSSEWriter_WriteEvent(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer, err := NewSSEWriter(recorder)
	assert.NoError(t, err)

	err = writer.WriteEvent(&SSEEvent{
		ID:    "123",
		Event: "message",
		Data:  map[string]interface{}{"key": "value"},
	})
	assert.NoError(t, err)

	result := recorder.Body.String()
	assert.Contains(t, result, "id: 123")
	assert.Contains(t, result, "event: message")
	assert.Contains(t, result, "data:")
	assert.Contains(t, result, "key")
}

func TestSSEWriter_WriteData(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer, err := NewSSEWriter(recorder)
	assert.NoError(t, err)

	err = writer.WriteData("simple text")
	assert.NoError(t, err)

	result := recorder.Body.String()
	assert.Contains(t, result, "data: simple text")
}

func TestSSEWriter_WriteComment(t *testing.T) {
	recorder := httptest.NewRecorder()
	writer, err := NewSSEWriter(recorder)
	assert.NoError(t, err)

	err = writer.WriteComment("keep-alive")
	assert.NoError(t, err)

	result := recorder.Body.String()
	assert.Contains(t, result, ": keep-alive")
}

func TestSSEWriter_Headers(t *testing.T) {
	recorder := httptest.NewRecorder()
	_, err := NewSSEWriter(recorder)
	assert.NoError(t, err)

	assert.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	assert.Equal(t, "no-cache", recorder.Header().Get("Cache-Control"))
	assert.Equal(t, "keep-alive", recorder.Header().Get("Connection"))
}

func TestSSEStreamHandler(t *testing.T) {
	// Create a handler that streams 3 messages
	handler := NewSSEStreamHandler(func(ctx context.Context, req *InvocationRequest) (<-chan interface{}, error) {
		ch := make(chan interface{})
		go func() {
			defer close(ch)
			for i := 1; i <= 3; i++ {
				select {
				case <-ctx.Done():
					return
				case ch <- map[string]interface{}{"message": i}:
				}
			}
		}()
		return ch, nil
	})

	// Create request
	reqBody := &InvocationRequest{
		JSONRPC: "2.0",
		ID:      "test-123",
		Method:  "stream",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/stream", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusOK, recorder.Code)

	// Parse the SSE response
	result := recorder.Body.String()
	assert.Contains(t, result, "event: start")
	assert.Contains(t, result, "event: data")
	assert.Contains(t, result, "event: done")
}

func TestSSEStreamHandler_MethodNotAllowed(t *testing.T) {
	handler := NewSSEStreamHandler(nil)

	req := httptest.NewRequest("GET", "/stream", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusMethodNotAllowed, recorder.Code)
}

func TestHTTPClient_StreamInvocation(t *testing.T) {
	// Create a mock SSE server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writer, err := NewSSEWriter(w)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Send events
		writer.WriteEvent(&SSEEvent{Event: "start", Data: "starting"})
		writer.WriteEvent(&SSEEvent{Event: "data", Data: map[string]interface{}{"content": "chunk1"}})
		writer.WriteEvent(&SSEEvent{Event: "data", Data: map[string]interface{}{"content": "chunk2"}})
		writer.WriteEvent(&SSEEvent{Event: "done", Data: "complete"})
	}))
	defer mockServer.Close()

	registry := NewDiscoveryRegistry()

	// Register agent - note the endpoint should not include /stream
	baseURL := strings.TrimSuffix(mockServer.URL, "/stream")
	card := &AgentCard{
		ID:       "stream-agent",
		Name:     "Stream Agent",
		Endpoint: baseURL,
	}
	registry.Register(card)

	client := NewHTTPA2AClient(registry)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := &agent.AgentInput{
		Instruction: "Stream test",
		TaskID:      "task-stream",
	}

	chunks, err := client.StreamInvocation(ctx, "stream-agent", input)
	assert.NoError(t, err)

	// Collect all chunks
	var receivedChunks []*StreamChunk
	for chunk := range chunks {
		receivedChunks = append(receivedChunks, chunk)
		if chunk.Final {
			break
		}
	}

	assert.Greater(t, len(receivedChunks), 0)
}

func TestAgentStreamOutput(t *testing.T) {
	output := &AgentStreamOutput{
		Type:      StreamOutputText,
		Content:   "Hello world",
		Timestamp: time.Now(),
	}

	assert.Equal(t, StreamOutputText, output.Type)
	assert.Equal(t, "Hello world", output.Content)
}

func TestStreamOutputTypes(t *testing.T) {
	types := []StreamOutputType{
		StreamOutputText,
		StreamOutputThinking,
		StreamOutputToolCall,
		StreamOutputToolResult,
		StreamOutputComplete,
		StreamOutputError,
	}

	for _, typ := range types {
		assert.NotEmpty(t, string(typ))
	}
}

// Mock pipe reader for testing streaming
type mockPipeReader struct {
	reader io.Reader
	closed bool
}

func (m *mockPipeReader) Read(p []byte) (n int, err error) {
	if m.closed {
		return 0, io.EOF
	}
	return m.reader.Read(p)
}

func (m *mockPipeReader) Close() error {
	m.closed = true
	return nil
}
