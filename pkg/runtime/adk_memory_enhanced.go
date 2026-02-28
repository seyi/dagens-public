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

// Package runtime provides enhanced ADK-compatible Memory interfaces with Go 1.23 support.
//
// This file extends our memory implementation to match Google ADK Go's patterns:
// - Go 1.23 iterator support for Events (iter.Seq)
// - State interface with iterator support
// - Multi-part Content support (similar to genai.Content)
// - Full SessionInterface compatibility
//
// While maintaining our Spark distributed computing advantages:
// - Vector/semantic search with embeddings
// - Distributed partitioning
// - Rich metadata and access tracking
package runtime

import (
	"context"
	"iter"
	"strings"
	"sync"
	"time"
)

// ============================================================================
// Multi-Part Content Support (similar to genai.Content)
// ============================================================================

// ContentPart represents a single part of multi-modal content.
// Similar to Google's genai.Part but framework-agnostic.
type ContentPart struct {
	// Text content
	Text string `json:"text,omitempty"`

	// Binary data (images, audio, video)
	InlineData *BlobData `json:"inline_data,omitempty"`

	// Function/tool call
	FunctionCall *FunctionCallPart `json:"function_call,omitempty"`

	// Function/tool response
	FunctionResponse *FunctionResponsePart `json:"function_response,omitempty"`

	// Structured data (JSON, etc.)
	StructuredData interface{} `json:"structured_data,omitempty"`
}

// BlobData represents binary data with MIME type
type BlobData struct {
	MIMEType string `json:"mime_type"`
	Data     []byte `json:"data"`
}

// FunctionCallPart represents a function/tool invocation
type FunctionCallPart struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args,omitempty"`
}

// FunctionResponsePart represents a function/tool result
type FunctionResponsePart struct {
	Name     string      `json:"name"`
	Response interface{} `json:"response"`
}

// MultiPartContent represents structured multi-modal content.
// Similar to Google's genai.Content but framework-agnostic.
type MultiPartContent struct {
	Parts []ContentPart `json:"parts"`
	Role  string        `json:"role,omitempty"` // "user", "model", "function", "system"
}

// NewTextContent creates a simple text content
func NewTextContent(text string) *MultiPartContent {
	return &MultiPartContent{
		Parts: []ContentPart{{Text: text}},
		Role:  "model",
	}
}

// NewMultiPartContent creates content with multiple parts
func NewMultiPartContent(role string, parts ...ContentPart) *MultiPartContent {
	return &MultiPartContent{
		Parts: parts,
		Role:  role,
	}
}

// GetText extracts all text from content parts
func (c *MultiPartContent) GetText() string {
	if c == nil {
		return ""
	}
	var texts []string
	for _, part := range c.Parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, " ")
}

// HasText checks if content has any text parts
func (c *MultiPartContent) HasText() bool {
	if c == nil {
		return false
	}
	for _, part := range c.Parts {
		if part.Text != "" {
			return true
		}
	}
	return false
}

// ExtractWords extracts all words from all text parts (for keyword matching)
func (c *MultiPartContent) ExtractWords() map[string]struct{} {
	words := make(map[string]struct{})
	if c == nil {
		return words
	}
	for _, part := range c.Parts {
		if part.Text != "" {
			extractWordsIntoMap(part.Text, words)
		}
	}
	return words
}

// ============================================================================
// State Interface (matches Google ADK Go's session.State)
// ============================================================================

// StateInterface provides key-value state management with iterator support.
// Matches Google ADK Go's session.State interface.
type StateInterface interface {
	// Get retrieves a value by key
	Get(key string) (interface{}, error)
	// Set stores a value by key
	Set(key string, value interface{}) error
	// Delete removes a key
	Delete(key string) error
	// All returns an iterator over all key-value pairs (Go 1.23)
	All() iter.Seq2[string, interface{}]
	// Has checks if a key exists
	Has(key string) bool
}

// ReadonlyStateInterface provides read-only state access
type ReadonlyStateInterface interface {
	Get(key string) (interface{}, error)
	All() iter.Seq2[string, interface{}]
	Has(key string) bool
}

// MapState implements StateInterface using a map
type MapState struct {
	data map[string]interface{}
	mu   sync.RWMutex
}

// NewMapState creates a new map-based state
func NewMapState() *MapState {
	return &MapState{
		data: make(map[string]interface{}),
	}
}

// NewMapStateFrom creates a state from an existing map
func NewMapStateFrom(data map[string]interface{}) *MapState {
	copied := make(map[string]interface{}, len(data))
	for k, v := range data {
		copied[k] = v
	}
	return &MapState{data: copied}
}

// Get retrieves a value by key
func (s *MapState) Get(key string) (interface{}, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.data[key]
	if !ok {
		return nil, nil // Return nil, nil for missing keys (ADK pattern)
	}
	return val, nil
}

// Set stores a value by key
func (s *MapState) Set(key string, value interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
	return nil
}

// Delete removes a key
func (s *MapState) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	return nil
}

// Has checks if a key exists
func (s *MapState) Has(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.data[key]
	return ok
}

// All returns a Go 1.23 iterator over all key-value pairs
func (s *MapState) All() iter.Seq2[string, interface{}] {
	return func(yield func(string, interface{}) bool) {
		s.mu.RLock()
		defer s.mu.RUnlock()
		for k, v := range s.data {
			if !yield(k, v) {
				return
			}
		}
	}
}

// ToMap returns a copy of the underlying map
func (s *MapState) ToMap() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copied := make(map[string]interface{}, len(s.data))
	for k, v := range s.data {
		copied[k] = v
	}
	return copied
}

// ============================================================================
// Enhanced Events Interface with Go 1.23 Iterator Support
// ============================================================================

// EnhancedEventsInterface extends EventsInterface with Go 1.23 iterator support.
// Matches Google ADK Go's session.Events interface.
type EnhancedEventsInterface interface {
	// All returns a Go 1.23 iterator over all events
	All() iter.Seq[*Event]
	// Len returns the number of events
	Len() int
	// At returns the event at index i
	At(i int) *Event
	// Last returns the most recent event (nil if empty)
	Last() *Event
	// Filter returns events matching a predicate
	Filter(pred func(*Event) bool) iter.Seq[*Event]
}

// EventsSlice implements EnhancedEventsInterface for a slice of events
type EventsSlice struct {
	events []Event
}

// NewEventsSlice creates an EventsSlice from events
func NewEventsSlice(events []Event) *EventsSlice {
	return &EventsSlice{events: events}
}

// All returns a Go 1.23 iterator over all events
func (e *EventsSlice) All() iter.Seq[*Event] {
	return func(yield func(*Event) bool) {
		for i := range e.events {
			if !yield(&e.events[i]) {
				return
			}
		}
	}
}

// Len returns the number of events
func (e *EventsSlice) Len() int {
	return len(e.events)
}

// At returns the event at index i
func (e *EventsSlice) At(i int) *Event {
	if i < 0 || i >= len(e.events) {
		return nil
	}
	return &e.events[i]
}

// Last returns the most recent event
func (e *EventsSlice) Last() *Event {
	if len(e.events) == 0 {
		return nil
	}
	return &e.events[len(e.events)-1]
}

// Filter returns events matching a predicate
func (e *EventsSlice) Filter(pred func(*Event) bool) iter.Seq[*Event] {
	return func(yield func(*Event) bool) {
		for i := range e.events {
			if pred(&e.events[i]) {
				if !yield(&e.events[i]) {
					return
				}
			}
		}
	}
}

// ============================================================================
// Enhanced Session Interface (matches Google ADK Go's session.Session)
// ============================================================================

// EnhancedSessionInterface provides full Google ADK Go compatibility.
// Extends SessionInterface with State() and LastUpdateTime().
type EnhancedSessionInterface interface {
	// ID returns the session identifier
	ID() string
	// AppName returns the application name
	AppName() string
	// UserID returns the user identifier
	UserID() string
	// Events returns the events interface with iterator support
	Events() EnhancedEventsInterface
	// State returns the session state interface
	State() StateInterface
	// LastUpdateTime returns when the session was last modified
	LastUpdateTime() time.Time
}

// EnhancedSession wraps our Session with full ADK compatibility
type EnhancedSession struct {
	session *Session
	appName string
	state   *MapState
}

// NewEnhancedSession creates an enhanced session wrapper
func NewEnhancedSession(session *Session, appName string) *EnhancedSession {
	state := NewMapState()
	if session.State != nil {
		for k, v := range session.State {
			state.Set(k, v)
		}
	}
	return &EnhancedSession{
		session: session,
		appName: appName,
		state:   state,
	}
}

// ID returns the session identifier
func (s *EnhancedSession) ID() string {
	return s.session.ID
}

// AppName returns the application name
func (s *EnhancedSession) AppName() string {
	return s.appName
}

// UserID returns the user identifier
func (s *EnhancedSession) UserID() string {
	return s.session.UserID
}

// Events returns the events interface with iterator support
func (s *EnhancedSession) Events() EnhancedEventsInterface {
	return NewEventsSlice(s.session.EventHistory)
}

// State returns the session state interface
func (s *EnhancedSession) State() StateInterface {
	return s.state
}

// LastUpdateTime returns when the session was last modified
func (s *EnhancedSession) LastUpdateTime() time.Time {
	return s.session.UpdatedAt
}

// Unwrap returns the underlying Session
func (s *EnhancedSession) Unwrap() *Session {
	return s.session
}

// ============================================================================
// Enhanced Memory Service with Multi-Part Content Support
// ============================================================================

// EnhancedMemoryValue stores memory with multi-part content support
type EnhancedMemoryValue struct {
	content       *MultiPartContent
	rawContent    interface{} // Original content for backward compatibility
	author        string
	timestamp     time.Time
	words         map[string]struct{} // Precomputed words for keyword matching
	sessionID     string
	eventID       string
	metadata      map[string]interface{}
}

// EnhancedInMemoryService provides Google ADK Go compatible memory with multi-part support
type EnhancedInMemoryService struct {
	mu    sync.RWMutex
	store map[memoryKey]map[string][]EnhancedMemoryValue // key -> sessionID -> values
}

// NewEnhancedInMemoryService creates a new enhanced memory service
func NewEnhancedInMemoryService() *EnhancedInMemoryService {
	return &EnhancedInMemoryService{
		store: make(map[memoryKey]map[string][]EnhancedMemoryValue),
	}
}

// AddSession adds a session to memory with full multi-part content extraction
func (s *EnhancedInMemoryService) AddSession(ctx context.Context, sess EnhancedSessionInterface) error {
	if sess == nil {
		return nil
	}

	var values []EnhancedMemoryValue

	// Use Go 1.23 iterator to process events
	for event := range sess.Events().All() {
		if event == nil || event.Content == nil {
			continue
		}

		// Extract words from content (handles both multi-part and simple content)
		words := make(map[string]struct{})
		var multiPart *MultiPartContent

		switch content := event.Content.(type) {
		case *MultiPartContent:
			multiPart = content
			// Extract words from all text parts
			for k, v := range content.ExtractWords() {
				words[k] = v
			}
		case MultiPartContent:
			multiPart = &content
			for k, v := range content.ExtractWords() {
				words[k] = v
			}
		case string:
			multiPart = NewTextContent(content)
			extractWordsIntoMap(content, words)
		default:
			// Try to convert to string
			if text, ok := event.Content.(string); ok {
				multiPart = NewTextContent(text)
				extractWordsIntoMap(text, words)
			}
		}

		if len(words) == 0 {
			continue
		}

		values = append(values, EnhancedMemoryValue{
			content:    multiPart,
			rawContent: event.Content,
			author:     event.Author,
			timestamp:  event.Timestamp,
			words:      words,
			eventID:    event.ID,
			metadata:   event.Metadata,
		})
	}

	key := memoryKey{
		appName: sess.AppName(),
		userID:  sess.UserID(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sessMap, ok := s.store[key]
	if !ok {
		sessMap = make(map[string][]EnhancedMemoryValue)
		s.store[key] = sessMap
	}

	// Store with session ID for tracking
	sessMap[sess.ID()] = values
	return nil
}

// Search returns memory entries matching the query
func (s *EnhancedInMemoryService) Search(ctx context.Context, req *MemorySearchRequest) (*SearchResponse, error) {
	if req == nil || req.Query == "" {
		return &SearchResponse{Memories: []MemoryEntry{}}, nil
	}

	queryWords := make(map[string]struct{})
	extractWordsIntoMap(req.Query, queryWords)

	key := memoryKey{
		appName: req.AppName,
		userID:  req.UserID,
	}

	s.mu.RLock()
	sessMap, ok := s.store[key]
	s.mu.RUnlock()

	if !ok {
		return &SearchResponse{Memories: []MemoryEntry{}}, nil
	}

	var results []MemoryEntry

	for sessionID, events := range sessMap {
		for _, e := range events {
			if mapsIntersect(e.words, queryWords) {
				// Use multi-part content text if available
				var content interface{}
				if e.content != nil {
					content = e.content.GetText()
				} else {
					content = e.rawContent
				}

				results = append(results, MemoryEntry{
					Content:   content,
					Author:    e.author,
					Timestamp: e.timestamp,
					SessionID: sessionID,
					EventID:   e.eventID,
					Metadata:  e.metadata,
				})
			}
		}
	}

	return &SearchResponse{Memories: results}, nil
}

// AddSessionCompat adds a session using the basic SessionInterface
func (s *EnhancedInMemoryService) AddSessionCompat(ctx context.Context, sess SessionInterface) error {
	if sess == nil {
		return nil
	}

	var values []EnhancedMemoryValue
	events := sess.Events()

	for i := 0; i < events.Len(); i++ {
		event := events.At(i)
		if event == nil || event.Content == nil {
			continue
		}

		words := make(map[string]struct{})
		var multiPart *MultiPartContent

		switch content := event.Content.(type) {
		case *MultiPartContent:
			multiPart = content
			for k, v := range content.ExtractWords() {
				words[k] = v
			}
		case string:
			multiPart = NewTextContent(content)
			extractWordsIntoMap(content, words)
		}

		if len(words) == 0 {
			continue
		}

		values = append(values, EnhancedMemoryValue{
			content:    multiPart,
			rawContent: event.Content,
			author:     event.Author,
			timestamp:  event.Timestamp,
			words:      words,
			eventID:    event.ID,
			metadata:   event.Metadata,
		})
	}

	key := memoryKey{
		appName: sess.AppName(),
		userID:  sess.UserID(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sessMap, ok := s.store[key]
	if !ok {
		sessMap = make(map[string][]EnhancedMemoryValue)
		s.store[key] = sessMap
	}

	sessMap[sess.ID()] = values
	return nil
}

// Clear removes all memories for a user/app combination
func (s *EnhancedInMemoryService) Clear(userID, appName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.store, memoryKey{appName: appName, userID: userID})
}

// ClearAll removes all memories
func (s *EnhancedInMemoryService) ClearAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store = make(map[memoryKey]map[string][]EnhancedMemoryValue)
}

// GetStats returns memory statistics
func (s *EnhancedInMemoryService) GetStats() EnhancedMemoryStats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := EnhancedMemoryStats{
		NumKeys:     len(s.store),
		NumSessions: 0,
		NumEntries:  0,
	}

	for _, sessMap := range s.store {
		stats.NumSessions += len(sessMap)
		for _, values := range sessMap {
			stats.NumEntries += len(values)
		}
	}

	return stats
}

// EnhancedMemoryStats provides memory statistics
type EnhancedMemoryStats struct {
	NumKeys     int `json:"num_keys"`
	NumSessions int `json:"num_sessions"`
	NumEntries  int `json:"num_entries"`
}

// ============================================================================
// Helper Functions
// ============================================================================

// extractWordsIntoMap extracts lowercase words from text into a map.
// Uses Go 1.23's strings.SplitSeq for iterator-based splitting (no allocation).
func extractWordsIntoMap(text string, words map[string]struct{}) {
	// Go 1.23: Use SplitSeq for memory-efficient iteration
	for word := range strings.SplitSeq(text, " ") {
		if word != "" {
			words[strings.ToLower(word)] = struct{}{}
		}
	}
}

// extractWordsFromText extracts words and returns a new map
func extractWordsFromText(text string) map[string]struct{} {
	words := make(map[string]struct{})
	extractWordsIntoMap(text, words)
	return words
}

// WrapSessionEnhanced wraps a Session for use with EnhancedInMemoryService
func WrapSessionEnhanced(s *Session, appName string) EnhancedSessionInterface {
	return NewEnhancedSession(s, appName)
}

// ============================================================================
// Backward Compatibility Adapters
// ============================================================================

// EnhancedSessionAdapter adapts EnhancedSessionInterface to SessionInterface
type EnhancedSessionAdapter struct {
	enhanced EnhancedSessionInterface
}

// ID returns the session identifier
func (a *EnhancedSessionAdapter) ID() string {
	return a.enhanced.ID()
}

// AppName returns the application name
func (a *EnhancedSessionAdapter) AppName() string {
	return a.enhanced.AppName()
}

// UserID returns the user identifier
func (a *EnhancedSessionAdapter) UserID() string {
	return a.enhanced.UserID()
}

// Events returns the events interface (downgraded to basic interface)
func (a *EnhancedSessionAdapter) Events() EventsInterface {
	return &EnhancedEventsAdapter{enhanced: a.enhanced.Events()}
}

// EnhancedEventsAdapter adapts EnhancedEventsInterface to EventsInterface
type EnhancedEventsAdapter struct {
	enhanced EnhancedEventsInterface
}

// Len returns the number of events
func (a *EnhancedEventsAdapter) Len() int {
	return a.enhanced.Len()
}

// At returns the event at index i
func (a *EnhancedEventsAdapter) At(i int) *Event {
	return a.enhanced.At(i)
}

// ToSessionInterface converts EnhancedSessionInterface to basic SessionInterface
func ToSessionInterface(enhanced EnhancedSessionInterface) SessionInterface {
	return &EnhancedSessionAdapter{enhanced: enhanced}
}
