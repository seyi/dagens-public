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

package runtime

import (
	"context"
	"testing"
	"time"
)

func TestInMemoryADKMemoryService_AddSession(t *testing.T) {
	service := NewInMemoryADKMemoryService()
	ctx := context.Background()

	// Create a session with events
	session := &Session{
		ID:     "session-1",
		UserID: "user-1",
		State:  make(map[string]interface{}),
		EventHistory: []Event{
			{
				ID:        "event-1",
				Type:      EventTypeMessage,
				Author:    "user-1",
				Content:   "The quick brown fox",
				Timestamp: time.Now().Add(-time.Minute),
			},
			{
				ID:        "event-2",
				Type:      EventTypeMessage,
				Author:    "agent-1",
				Content:   "jumps over the lazy dog",
				Timestamp: time.Now(),
			},
		},
	}

	// Add session
	err := service.AddSession(ctx, session)
	if err != nil {
		t.Fatalf("AddSession failed: %v", err)
	}

	// Search for "fox"
	resp, err := service.SearchForUser(ctx, "user-1", "fox")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(resp.Memories) != 1 {
		t.Errorf("Expected 1 result, got %d", len(resp.Memories))
	}

	if resp.Memories[0].Author != "user-1" {
		t.Errorf("Expected author 'user-1', got %s", resp.Memories[0].Author)
	}
}

func TestInMemoryADKMemoryService_Search(t *testing.T) {
	service := NewInMemoryADKMemoryService()
	ctx := context.Background()

	// Add sessions for two users
	session1 := &Session{
		ID:     "session-1",
		UserID: "user-1",
		EventHistory: []Event{
			{ID: "e1", Content: "Content for user1", Author: "user-1", Timestamp: time.Now()},
		},
	}
	session2 := &Session{
		ID:     "session-2",
		UserID: "user-2",
		EventHistory: []Event{
			{ID: "e2", Content: "Content for user2", Author: "user-2", Timestamp: time.Now()},
		},
	}

	_ = service.AddSession(ctx, session1)
	_ = service.AddSession(ctx, session2)

	// Search across all users
	resp, err := service.Search(ctx, "Content")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(resp.Memories) != 2 {
		t.Errorf("Expected 2 results, got %d", len(resp.Memories))
	}

	// Search for user1 only
	resp1, err := service.SearchForUser(ctx, "user-1", "Content")
	if err != nil {
		t.Fatalf("SearchForUser failed: %v", err)
	}

	if len(resp1.Memories) != 1 {
		t.Errorf("Expected 1 result for user-1, got %d", len(resp1.Memories))
	}
}

func TestInMemoryADKMemoryService_SearchCaseInsensitive(t *testing.T) {
	service := NewInMemoryADKMemoryService()
	ctx := context.Background()

	session := &Session{
		ID:     "session-1",
		UserID: "user-1",
		EventHistory: []Event{
			{ID: "e1", Content: "The Quick Brown FOX", Author: "user", Timestamp: time.Now()},
		},
	}

	_ = service.AddSession(ctx, session)

	// Search with different cases
	tests := []struct {
		query    string
		expected int
	}{
		{"fox", 1},
		{"FOX", 1},
		{"Fox", 1},
		{"quick", 1},
		{"BROWN", 1},
		{"elephant", 0},
	}

	for _, tc := range tests {
		resp, err := service.Search(ctx, tc.query)
		if err != nil {
			t.Fatalf("Search(%q) failed: %v", tc.query, err)
		}

		if len(resp.Memories) != tc.expected {
			t.Errorf("Search(%q): expected %d results, got %d", tc.query, tc.expected, len(resp.Memories))
		}
	}
}

func TestInMemoryADKMemoryService_EmptyQuery(t *testing.T) {
	service := NewInMemoryADKMemoryService()
	ctx := context.Background()

	session := &Session{
		ID:     "session-1",
		UserID: "user-1",
		EventHistory: []Event{
			{ID: "e1", Content: "Some content", Author: "user", Timestamp: time.Now()},
		},
	}

	_ = service.AddSession(ctx, session)

	// Empty query should return empty results
	resp, err := service.Search(ctx, "")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(resp.Memories) != 0 {
		t.Errorf("Expected 0 results for empty query, got %d", len(resp.Memories))
	}
}

func TestInMemoryADKMemoryService_Clear(t *testing.T) {
	service := NewInMemoryADKMemoryService()
	ctx := context.Background()

	session := &Session{
		ID:     "session-1",
		UserID: "user-1",
		EventHistory: []Event{
			{ID: "e1", Content: "Test content", Author: "user", Timestamp: time.Now()},
		},
	}

	_ = service.AddSession(ctx, session)

	// Verify content exists
	resp, _ := service.SearchForUser(ctx, "user-1", "content")
	if len(resp.Memories) != 1 {
		t.Errorf("Expected 1 result before clear")
	}

	// Clear user memories
	service.Clear("user-1")

	// Verify content is gone
	resp, _ = service.SearchForUser(ctx, "user-1", "content")
	if len(resp.Memories) != 0 {
		t.Errorf("Expected 0 results after clear, got %d", len(resp.Memories))
	}
}

func TestInMemoryADKMemoryService_NilSession(t *testing.T) {
	service := NewInMemoryADKMemoryService()
	ctx := context.Background()

	// Adding nil session should not fail
	err := service.AddSession(ctx, nil)
	if err != nil {
		t.Errorf("AddSession(nil) should not return error, got %v", err)
	}
}

func TestADKMemoryService_WithBackend(t *testing.T) {
	// Create a mock backend
	backend := &mockMemoryBackend{
		memories: make(map[string][]*Memory),
	}

	service := NewADKMemoryService(ADKMemoryConfig{
		Backend:   backend,
		UserID:    "user-1",
		AppName:   "test-app",
		SessionID: "session-1",
	})

	ctx := context.Background()

	// Create session
	session := &Session{
		ID:     "session-1",
		UserID: "user-1",
		EventHistory: []Event{
			{ID: "e1", Content: "Hello world", Author: "user", Timestamp: time.Now()},
		},
	}

	// Add session
	err := service.AddSession(ctx, session)
	if err != nil {
		t.Fatalf("AddSession failed: %v", err)
	}

	// Backend should have received the memory
	if len(backend.memories["user-1"]) != 1 {
		t.Errorf("Expected 1 memory in backend, got %d", len(backend.memories["user-1"]))
	}
}

func TestToolContextWithMemory_SearchMemory(t *testing.T) {
	memory := NewInMemoryADKMemoryService()
	ctx := context.Background()

	// Add some memories
	session := &Session{
		ID:     "session-1",
		UserID: "user-1",
		EventHistory: []Event{
			{ID: "e1", Content: "Remember this important fact", Author: "user", Timestamp: time.Now()},
		},
	}
	_ = memory.AddSession(ctx, session)

	// Create tool context with memory
	toolCtx := &ToolContext{
		InvocationContext: &InvocationContext{
			SessionID: "session-1",
			UserID:    "user-1",
		},
		ToolName: "test-tool",
	}

	toolCtxWithMemory := NewToolContextWithMemory(toolCtx, memory)

	// Search memory from tool context
	resp, err := toolCtxWithMemory.SearchMemory(ctx, "important")
	if err != nil {
		t.Fatalf("SearchMemory failed: %v", err)
	}

	if len(resp.Memories) != 1 {
		t.Errorf("Expected 1 result, got %d", len(resp.Memories))
	}
}

func TestInvocationContextWithMemory(t *testing.T) {
	memory := NewInMemoryADKMemoryService()
	ctx := context.Background()

	invCtx := &InvocationContext{
		SessionID: "session-1",
		UserID:    "user-1",
	}

	invCtxWithMemory := NewInvocationContextWithMemory(invCtx, memory)

	// Verify memory is accessible
	if invCtxWithMemory.Memory() == nil {
		t.Error("Expected Memory() to return non-nil")
	}

	// Add a session through the memory
	session := &Session{
		ID:     "session-1",
		UserID: "user-1",
		EventHistory: []Event{
			{ID: "e1", Content: "Context memory test", Author: "user", Timestamp: time.Now()},
		},
	}
	_ = invCtxWithMemory.Memory().AddSession(ctx, session)

	// Search
	resp, err := invCtxWithMemory.Memory().Search(ctx, "memory test")
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(resp.Memories) != 1 {
		t.Errorf("Expected 1 result, got %d", len(resp.Memories))
	}
}

// mockMemoryBackend for testing
type mockMemoryBackend struct {
	memories map[string][]*Memory
}

func (m *mockMemoryBackend) Store(ctx context.Context, userID string, memory *Memory) error {
	if m.memories[userID] == nil {
		m.memories[userID] = make([]*Memory, 0)
	}
	m.memories[userID] = append(m.memories[userID], memory)
	return nil
}

func (m *mockMemoryBackend) Query(ctx context.Context, userID string, query string) ([]*Memory, error) {
	return m.memories[userID], nil
}

// ============================================================================
// Tests for Google ADK Go Compatible Memory Service
// ============================================================================

func TestInMemoryServiceCompat_AddSessionAndSearch(t *testing.T) {
	service := NewInMemoryServiceCompat()
	ctx := context.Background()

	// Create a test session
	session := &testSessionCompat{
		id:      "session-1",
		appName: "test-app",
		userID:  "user-1",
		events: []Event{
			{ID: "e1", Content: "The quick brown fox", Author: "user-1", Timestamp: time.Now().Add(-time.Minute)},
			{ID: "e2", Content: "jumps over the lazy dog", Author: "agent-1", Timestamp: time.Now()},
		},
	}

	// Add session
	err := service.AddSession(ctx, session)
	if err != nil {
		t.Fatalf("AddSession failed: %v", err)
	}

	// Search for "fox"
	resp, err := service.Search(ctx, &MemorySearchRequest{
		Query:   "fox",
		UserID:  "user-1",
		AppName: "test-app",
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}

	if len(resp.Memories) != 1 {
		t.Errorf("Expected 1 result, got %d", len(resp.Memories))
	}
}

func TestInMemoryServiceCompat_WordBasedMatching(t *testing.T) {
	service := NewInMemoryServiceCompat()
	ctx := context.Background()

	session := &testSessionCompat{
		id:      "session-1",
		appName: "app1",
		userID:  "user1",
		events: []Event{
			{ID: "e1", Content: "The Quick brown fox", Author: "user1", Timestamp: time.Now()},
			{ID: "e2", Content: "hello world", Author: "bot", Timestamp: time.Now()},
		},
	}

	_ = service.AddSession(ctx, session)

	tests := []struct {
		query    string
		expected int
	}{
		{"quick hello", 2}, // Matches both (word-based OR)
		{"fox", 1},         // Matches first
		{"world", 1},       // Matches second
		{"elephant", 0},    // No match
		{"", 0},            // Empty query
	}

	for _, tc := range tests {
		resp, err := service.Search(ctx, &MemorySearchRequest{
			Query:   tc.query,
			UserID:  "user1",
			AppName: "app1",
		})
		if err != nil {
			t.Fatalf("Search(%q) failed: %v", tc.query, err)
		}

		if len(resp.Memories) != tc.expected {
			t.Errorf("Search(%q): expected %d results, got %d", tc.query, tc.expected, len(resp.Memories))
		}
	}
}

func TestInMemoryServiceCompat_UserIsolation(t *testing.T) {
	service := NewInMemoryServiceCompat()
	ctx := context.Background()

	// Add sessions for two users
	session1 := &testSessionCompat{
		id:      "session-1",
		appName: "app1",
		userID:  "user1",
		events: []Event{
			{ID: "e1", Content: "Content for user1", Author: "user1", Timestamp: time.Now()},
		},
	}
	session2 := &testSessionCompat{
		id:      "session-2",
		appName: "app1",
		userID:  "user2",
		events: []Event{
			{ID: "e2", Content: "Content for user2", Author: "user2", Timestamp: time.Now()},
		},
	}

	_ = service.AddSession(ctx, session1)
	_ = service.AddSession(ctx, session2)

	// User1 search should only find user1's content
	resp1, _ := service.Search(ctx, &MemorySearchRequest{
		Query:   "Content",
		UserID:  "user1",
		AppName: "app1",
	})
	if len(resp1.Memories) != 1 {
		t.Errorf("Expected 1 result for user1, got %d", len(resp1.Memories))
	}

	// Different app should find nothing
	resp3, _ := service.Search(ctx, &MemorySearchRequest{
		Query:   "Content",
		UserID:  "user1",
		AppName: "other_app",
	})
	if len(resp3.Memories) != 0 {
		t.Errorf("Expected 0 results for other_app, got %d", len(resp3.Memories))
	}
}

func TestWrapSession(t *testing.T) {
	session := &Session{
		ID:     "session-1",
		UserID: "user-1",
		EventHistory: []Event{
			{ID: "e1", Content: "Test content", Author: "user", Timestamp: time.Now()},
		},
	}

	wrapped := WrapSession(session)

	if wrapped.ID() != "session-1" {
		t.Errorf("ID() = %s, want session-1", wrapped.ID())
	}
	if wrapped.UserID() != "user-1" {
		t.Errorf("UserID() = %s, want user-1", wrapped.UserID())
	}
	if wrapped.Events().Len() != 1 {
		t.Errorf("Events().Len() = %d, want 1", wrapped.Events().Len())
	}
}

// testSessionCompat implements SessionInterface for testing
type testSessionCompat struct {
	id      string
	appName string
	userID  string
	events  []Event
}

func (s *testSessionCompat) ID() string      { return s.id }
func (s *testSessionCompat) AppName() string { return s.appName }
func (s *testSessionCompat) UserID() string  { return s.userID }
func (s *testSessionCompat) Events() EventsInterface {
	return &testEventsCompat{events: s.events}
}

type testEventsCompat struct {
	events []Event
}

func (e *testEventsCompat) Len() int        { return len(e.events) }
func (e *testEventsCompat) At(i int) *Event { return &e.events[i] }
