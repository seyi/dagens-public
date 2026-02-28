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

// ============================================================================
// Multi-Part Content Tests
// ============================================================================

func TestMultiPartContent_GetText(t *testing.T) {
	tests := []struct {
		name     string
		content  *MultiPartContent
		expected string
	}{
		{
			name:     "nil content",
			content:  nil,
			expected: "",
		},
		{
			name:     "single text part",
			content:  NewTextContent("Hello world"),
			expected: "Hello world",
		},
		{
			name: "multiple text parts",
			content: &MultiPartContent{
				Parts: []ContentPart{
					{Text: "Hello"},
					{Text: "world"},
					{Text: "from Go"},
				},
			},
			expected: "Hello world from Go",
		},
		{
			name: "mixed parts (text and non-text)",
			content: &MultiPartContent{
				Parts: []ContentPart{
					{Text: "Start"},
					{FunctionCall: &FunctionCallPart{Name: "test"}},
					{Text: "End"},
				},
			},
			expected: "Start End",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.content.GetText()
			if result != tt.expected {
				t.Errorf("GetText() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestMultiPartContent_ExtractWords(t *testing.T) {
	content := &MultiPartContent{
		Parts: []ContentPart{
			{Text: "The quick brown fox"},
			{Text: "jumps over the lazy dog"},
		},
	}

	words := content.ExtractWords()

	expectedWords := []string{"the", "quick", "brown", "fox", "jumps", "over", "lazy", "dog"}
	for _, word := range expectedWords {
		if _, ok := words[word]; !ok {
			t.Errorf("Expected word %q not found", word)
		}
	}
}

func TestMultiPartContent_HasText(t *testing.T) {
	tests := []struct {
		name     string
		content  *MultiPartContent
		expected bool
	}{
		{
			name:     "nil content",
			content:  nil,
			expected: false,
		},
		{
			name:     "has text",
			content:  NewTextContent("Hello"),
			expected: true,
		},
		{
			name: "no text parts",
			content: &MultiPartContent{
				Parts: []ContentPart{
					{FunctionCall: &FunctionCallPart{Name: "test"}},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.content.HasText()
			if result != tt.expected {
				t.Errorf("HasText() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// ============================================================================
// State Interface Tests
// ============================================================================

func TestMapState_BasicOperations(t *testing.T) {
	state := NewMapState()

	// Test Set and Get
	err := state.Set("key1", "value1")
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	val, err := state.Get("key1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if val != "value1" {
		t.Errorf("Get() = %v, want %v", val, "value1")
	}

	// Test Has
	if !state.Has("key1") {
		t.Error("Has() = false, want true")
	}
	if state.Has("nonexistent") {
		t.Error("Has(nonexistent) = true, want false")
	}

	// Test Delete
	err = state.Delete("key1")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if state.Has("key1") {
		t.Error("Has() after Delete = true, want false")
	}
}

func TestMapState_Iterator(t *testing.T) {
	state := NewMapState()
	state.Set("a", 1)
	state.Set("b", 2)
	state.Set("c", 3)

	// Test All() iterator
	count := 0
	sum := 0
	for key, val := range state.All() {
		count++
		if v, ok := val.(int); ok {
			sum += v
		}
		_ = key // Use key
	}

	if count != 3 {
		t.Errorf("Iterator count = %d, want 3", count)
	}
	if sum != 6 {
		t.Errorf("Sum = %d, want 6", sum)
	}
}

func TestMapState_ToMap(t *testing.T) {
	state := NewMapStateFrom(map[string]interface{}{
		"x": 10,
		"y": 20,
	})

	m := state.ToMap()
	if len(m) != 2 {
		t.Errorf("ToMap() length = %d, want 2", len(m))
	}
	if m["x"] != 10 || m["y"] != 20 {
		t.Errorf("ToMap() values incorrect")
	}

	// Verify it's a copy
	m["z"] = 30
	if state.Has("z") {
		t.Error("ToMap() should return a copy, not reference")
	}
}

// ============================================================================
// Events Interface Tests
// ============================================================================

func TestEventsSlice_All(t *testing.T) {
	events := []Event{
		{ID: "1", Content: "First"},
		{ID: "2", Content: "Second"},
		{ID: "3", Content: "Third"},
	}
	slice := NewEventsSlice(events)

	// Test All() iterator
	count := 0
	for event := range slice.All() {
		count++
		if event == nil {
			t.Error("Event should not be nil")
		}
	}

	if count != 3 {
		t.Errorf("All() count = %d, want 3", count)
	}
}

func TestEventsSlice_Filter(t *testing.T) {
	events := []Event{
		{ID: "1", Type: EventTypeMessage, Content: "Message 1"},
		{ID: "2", Type: EventTypeToolCall, Content: "Tool call"},
		{ID: "3", Type: EventTypeMessage, Content: "Message 2"},
	}
	slice := NewEventsSlice(events)

	// Filter for messages only
	count := 0
	for event := range slice.Filter(func(e *Event) bool {
		return e.Type == EventTypeMessage
	}) {
		count++
		if event.Type != EventTypeMessage {
			t.Error("Filter returned wrong event type")
		}
	}

	if count != 2 {
		t.Errorf("Filter count = %d, want 2", count)
	}
}

func TestEventsSlice_Last(t *testing.T) {
	// Empty slice
	empty := NewEventsSlice([]Event{})
	if empty.Last() != nil {
		t.Error("Last() on empty slice should return nil")
	}

	// Non-empty slice
	events := []Event{
		{ID: "1"},
		{ID: "2"},
		{ID: "3"},
	}
	slice := NewEventsSlice(events)
	last := slice.Last()
	if last == nil || last.ID != "3" {
		t.Error("Last() should return the last event")
	}
}

// ============================================================================
// Enhanced Session Tests
// ============================================================================

func TestEnhancedSession_FullInterface(t *testing.T) {
	session := &Session{
		ID:     "session-123",
		UserID: "user-456",
		State: map[string]interface{}{
			"counter": 5,
		},
		EventHistory: []Event{
			{ID: "e1", Content: "Hello", Author: "user"},
			{ID: "e2", Content: "Hi there", Author: "agent"},
		},
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now(),
	}

	enhanced := NewEnhancedSession(session, "test-app")

	// Test basic getters
	if enhanced.ID() != "session-123" {
		t.Errorf("ID() = %s, want session-123", enhanced.ID())
	}
	if enhanced.AppName() != "test-app" {
		t.Errorf("AppName() = %s, want test-app", enhanced.AppName())
	}
	if enhanced.UserID() != "user-456" {
		t.Errorf("UserID() = %s, want user-456", enhanced.UserID())
	}

	// Test Events
	if enhanced.Events().Len() != 2 {
		t.Errorf("Events().Len() = %d, want 2", enhanced.Events().Len())
	}

	// Test State
	val, _ := enhanced.State().Get("counter")
	if val != 5 {
		t.Errorf("State().Get(counter) = %v, want 5", val)
	}

	// Test LastUpdateTime
	if enhanced.LastUpdateTime().IsZero() {
		t.Error("LastUpdateTime() should not be zero")
	}
}

// ============================================================================
// Enhanced Memory Service Tests
// ============================================================================

func TestEnhancedInMemoryService_AddAndSearch(t *testing.T) {
	service := NewEnhancedInMemoryService()
	ctx := context.Background()

	// Create test session
	session := &Session{
		ID:     "sess-1",
		UserID: "user-1",
		EventHistory: []Event{
			{ID: "e1", Content: "The quick brown fox", Author: "user", Timestamp: time.Now()},
			{ID: "e2", Content: "jumps over the lazy dog", Author: "agent", Timestamp: time.Now()},
		},
		UpdatedAt: time.Now(),
	}

	enhanced := NewEnhancedSession(session, "app-1")
	err := service.AddSession(ctx, enhanced)
	if err != nil {
		t.Fatalf("AddSession failed: %v", err)
	}

	// Search for existing word
	resp, err := service.Search(ctx, &MemorySearchRequest{
		Query:   "quick",
		UserID:  "user-1",
		AppName: "app-1",
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(resp.Memories) == 0 {
		t.Error("Search should find 'quick'")
	}

	// Search for another word
	resp, err = service.Search(ctx, &MemorySearchRequest{
		Query:   "lazy",
		UserID:  "user-1",
		AppName: "app-1",
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(resp.Memories) == 0 {
		t.Error("Search should find 'lazy'")
	}
}

func TestEnhancedInMemoryService_MultiPartContent(t *testing.T) {
	service := NewEnhancedInMemoryService()
	ctx := context.Background()

	// Create session with multi-part content
	session := &Session{
		ID:     "sess-multi",
		UserID: "user-multi",
		EventHistory: []Event{
			{
				ID: "e1",
				Content: &MultiPartContent{
					Parts: []ContentPart{
						{Text: "First part about Spark"},
						{Text: "Second part about distributed computing"},
					},
					Role: "model",
				},
				Author:    "agent",
				Timestamp: time.Now(),
			},
		},
		UpdatedAt: time.Now(),
	}

	enhanced := NewEnhancedSession(session, "multi-app")
	err := service.AddSession(ctx, enhanced)
	if err != nil {
		t.Fatalf("AddSession failed: %v", err)
	}

	// Search should find words from both parts
	tests := []struct {
		query    string
		expected bool
	}{
		{"spark", true},
		{"distributed", true},
		{"computing", true},
		{"nonexistent", false},
	}

	for _, tt := range tests {
		resp, _ := service.Search(ctx, &MemorySearchRequest{
			Query:   tt.query,
			UserID:  "user-multi",
			AppName: "multi-app",
		})
		found := len(resp.Memories) > 0
		if found != tt.expected {
			t.Errorf("Search(%q) found=%v, want %v", tt.query, found, tt.expected)
		}
	}
}

func TestEnhancedInMemoryService_UserAppIsolation(t *testing.T) {
	service := NewEnhancedInMemoryService()
	ctx := context.Background()

	// Add sessions for different users/apps
	sessions := []struct {
		sessionID string
		userID    string
		appName   string
		content   string
	}{
		{"s1", "user-A", "app-1", "secret data for user A app 1"},
		{"s2", "user-A", "app-2", "secret data for user A app 2"},
		{"s3", "user-B", "app-1", "secret data for user B app 1"},
	}

	for _, s := range sessions {
		session := &Session{
			ID:     s.sessionID,
			UserID: s.userID,
			EventHistory: []Event{
				{ID: "e1", Content: s.content, Author: "system", Timestamp: time.Now()},
			},
			UpdatedAt: time.Now(),
		}
		enhanced := NewEnhancedSession(session, s.appName)
		service.AddSession(ctx, enhanced)
	}

	// User A, App 1 should only find their data
	resp, _ := service.Search(ctx, &MemorySearchRequest{
		Query:   "secret",
		UserID:  "user-A",
		AppName: "app-1",
	})
	if len(resp.Memories) != 1 {
		t.Errorf("User A App 1 should find exactly 1 result, got %d", len(resp.Memories))
	}

	// User B, App 2 should find nothing (no data for this combination)
	resp, _ = service.Search(ctx, &MemorySearchRequest{
		Query:   "secret",
		UserID:  "user-B",
		AppName: "app-2",
	})
	if len(resp.Memories) != 0 {
		t.Errorf("User B App 2 should find 0 results, got %d", len(resp.Memories))
	}
}

func TestEnhancedInMemoryService_WordBasedMatching(t *testing.T) {
	service := NewEnhancedInMemoryService()
	ctx := context.Background()

	session := &Session{
		ID:     "word-test",
		UserID: "user-word",
		EventHistory: []Event{
			{ID: "e1", Content: "Apache Spark is great for big data", Author: "user", Timestamp: time.Now()},
		},
		UpdatedAt: time.Now(),
	}

	enhanced := NewEnhancedSession(session, "word-app")
	service.AddSession(ctx, enhanced)

	tests := []struct {
		query    string
		expected bool
		reason   string
	}{
		{"spark", true, "exact word match (case insensitive)"},
		{"Spark", true, "case insensitive match"},
		{"SPARK", true, "uppercase match"},
		{"spa", false, "partial word should not match"},
		{"sparky", false, "different word should not match"},
		{"big data", true, "multiple words - at least one matches"},
	}

	for _, tt := range tests {
		resp, _ := service.Search(ctx, &MemorySearchRequest{
			Query:   tt.query,
			UserID:  "user-word",
			AppName: "word-app",
		})
		found := len(resp.Memories) > 0
		if found != tt.expected {
			t.Errorf("Query %q: found=%v, want %v (%s)", tt.query, found, tt.expected, tt.reason)
		}
	}
}

func TestEnhancedInMemoryService_GetStats(t *testing.T) {
	service := NewEnhancedInMemoryService()
	ctx := context.Background()

	// Initially empty
	stats := service.GetStats()
	if stats.NumKeys != 0 || stats.NumSessions != 0 || stats.NumEntries != 0 {
		t.Error("Empty service should have zero stats")
	}

	// Add some sessions
	for i := 0; i < 3; i++ {
		session := &Session{
			ID:     "stats-sess-" + string(rune('0'+i)),
			UserID: "stats-user",
			EventHistory: []Event{
				{ID: "e1", Content: "Event 1", Timestamp: time.Now()},
				{ID: "e2", Content: "Event 2", Timestamp: time.Now()},
			},
			UpdatedAt: time.Now(),
		}
		enhanced := NewEnhancedSession(session, "stats-app")
		service.AddSession(ctx, enhanced)
	}

	stats = service.GetStats()
	if stats.NumKeys != 1 {
		t.Errorf("NumKeys = %d, want 1", stats.NumKeys)
	}
	if stats.NumSessions != 3 {
		t.Errorf("NumSessions = %d, want 3", stats.NumSessions)
	}
	if stats.NumEntries != 6 {
		t.Errorf("NumEntries = %d, want 6", stats.NumEntries)
	}
}

func TestEnhancedInMemoryService_Clear(t *testing.T) {
	service := NewEnhancedInMemoryService()
	ctx := context.Background()

	// Add session
	session := &Session{
		ID:     "clear-test",
		UserID: "clear-user",
		EventHistory: []Event{
			{ID: "e1", Content: "Test content", Timestamp: time.Now()},
		},
		UpdatedAt: time.Now(),
	}
	enhanced := NewEnhancedSession(session, "clear-app")
	service.AddSession(ctx, enhanced)

	// Verify it exists
	resp, _ := service.Search(ctx, &MemorySearchRequest{
		Query:   "test",
		UserID:  "clear-user",
		AppName: "clear-app",
	})
	if len(resp.Memories) == 0 {
		t.Error("Should find data before clear")
	}

	// Clear
	service.Clear("clear-user", "clear-app")

	// Verify it's gone
	resp, _ = service.Search(ctx, &MemorySearchRequest{
		Query:   "test",
		UserID:  "clear-user",
		AppName: "clear-app",
	})
	if len(resp.Memories) != 0 {
		t.Error("Should not find data after clear")
	}
}

// ============================================================================
// Backward Compatibility Tests
// ============================================================================

func TestToSessionInterface_Compatibility(t *testing.T) {
	session := &Session{
		ID:     "compat-sess",
		UserID: "compat-user",
		EventHistory: []Event{
			{ID: "e1", Content: "Test"},
		},
		UpdatedAt: time.Now(),
	}

	enhanced := NewEnhancedSession(session, "compat-app")
	basic := ToSessionInterface(enhanced)

	// Verify basic interface works
	if basic.ID() != "compat-sess" {
		t.Errorf("ID() = %s, want compat-sess", basic.ID())
	}
	if basic.UserID() != "compat-user" {
		t.Errorf("UserID() = %s, want compat-user", basic.UserID())
	}
	if basic.Events().Len() != 1 {
		t.Errorf("Events().Len() = %d, want 1", basic.Events().Len())
	}
}

// ============================================================================
// Word Extraction Tests (Go 1.23 SplitSeq)
// ============================================================================

func TestExtractWordsFromText(t *testing.T) {
	tests := []struct {
		text     string
		expected []string
	}{
		{"Hello World", []string{"hello", "world"}},
		{"  multiple   spaces  ", []string{"multiple", "spaces"}},
		{"MixedCase UPPER lower", []string{"mixedcase", "upper", "lower"}},
		{"", []string{}},
		{"single", []string{"single"}},
	}

	for _, tt := range tests {
		words := extractWordsFromText(tt.text)
		for _, expected := range tt.expected {
			if _, ok := words[expected]; !ok {
				t.Errorf("extractWordsFromText(%q) missing word %q", tt.text, expected)
			}
		}
		if len(words) != len(tt.expected) {
			t.Errorf("extractWordsFromText(%q) = %d words, want %d", tt.text, len(words), len(tt.expected))
		}
	}
}
