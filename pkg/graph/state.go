package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"math/rand"
)

// initialStateVersion is the starting version for new states.
const initialStateVersion int64 = 1

// ============================================
// Interface Segregation (Task 4)
// ============================================

// StateReader provides read-only access to state data.
type StateReader interface {
	// Get retrieves a value from the state.
	// Returns a safe copy for mutable types.
	Get(key string) (interface{}, bool)

	// Keys returns all keys in the state.
	Keys() []string

	// Metadata returns state-level metadata (deep copy).
	Metadata() map[string]interface{}
}

// StateWriter provides write access to state data.
type StateWriter interface {
	// Set stores a value in the state.
	Set(key string, value interface{})

	// Delete removes a value from the state.
	Delete(key string)
}

// StateSnapshotter provides snapshot and restore capabilities.
type StateSnapshotter interface {
	// Clone creates a deep copy of the state.
	Clone() State

	// Snapshot creates a snapshot of the current state.
	Snapshot() *StateSnapshot

	// Restore restores state from a snapshot.
	Restore(snapshot *StateSnapshot)
}

// StateObservable provides observation capabilities.
type StateObservable interface {
	// Watch subscribes to state change events.
	// Returns nil if observation is disabled.
	Watch(ctx context.Context) (<-chan StateEvent, func())

	// Unwatch removes a watcher by observer ID.
	Unwatch(observerID string)
}

// State represents the execution state passed between nodes.
// It composes all state-related interfaces for full functionality.
// This maintains backward compatibility with existing code.
type State interface {
	StateReader
	StateWriter
	StateSnapshotter
	StateObservable
}

// AdvancedState extends State with Phase 2A capabilities.
type AdvancedState interface {
	State

	// Marshal serializes the state to bytes.
	Marshal() ([]byte, error)

	// Unmarshal deserializes bytes into the state.
	Unmarshal(data []byte) error

	// Iterate calls the provided function for each key-value pair.
	Iterate(fn func(key string, value any) bool)

	// KeysWithPrefix returns sorted keys matching the prefix.
	KeysWithPrefix(prefix string) []string

	// Subtree returns a new State containing only keys with the given prefix,
	// with the prefix stripped.
	Subtree(prefix string) *State

	// DeletePrefix removes all keys matching the prefix and returns count.
	DeletePrefix(prefix string) int

	// Version returns the current state version.
	Version() uint64
}

// TypedStateReader provides generic type-safe read access.
type TypedStateReader interface {
	StateReader
	// GetTyped retrieves a value with type safety.
	// Returns the zero value and false if key doesn't exist or type doesn't match.
	GetTyped(key string, target interface{}) bool
}

// TypedStateWriter provides generic type-safe write access.
type TypedStateWriter interface {
	StateWriter
	// SetTyped stores a value with compile-time type checking via generics.
	SetTyped(key string, value interface{})
}

// ============================================
// Copy-on-Write Value Container (Task 1)
// ============================================

// cowValue wraps a stored value with copy-on-write semantics.
// Immutable values (primitives) can be returned directly.
// Mutable values are copied on read to ensure isolation.
type cowValue struct {
	value     interface{}
	immutable bool      // true if value is a primitive that doesn't need copying
	version   uint64    // version when this value was set (for future COW optimization)
	frozen    bool      // true if this value has been shared and must be copied before mutation
}

// isImmutableType returns true if the type is immutable (primitives).
func isImmutableType(v interface{}) bool {
	if v == nil {
		return true
	}
	switch v.(type) {
	case string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64, uintptr,
		float32, float64, complex64, complex128,
		json.Number:
		return true
	default:
		return false
	}
}

// newCOWValue creates a new COW value container.
// For mutable types, the value is deep copied on storage.
func newCOWValue(v interface{}, version uint64) *cowValue {
	immutable := isImmutableType(v)
	if immutable {
		return &cowValue{
			value:     v,
			immutable: true,
			version:   version,
			frozen:    false,
		}
	}
	// Deep copy mutable values on write
	return &cowValue{
		value:     deepCopyValue(v),
		immutable: false,
		version:   version,
		frozen:    false,
	}
}

// read returns the value, copying if necessary for mutable types.
func (c *cowValue) read() interface{} {
	if c.immutable {
		return c.value
	}
	// Mark as frozen since we're sharing it
	c.frozen = true
	// Return a copy to prevent external mutation
	return deepCopyValue(c.value)
}

// readDirect returns the value without copying (for internal use only).
// Caller must ensure the value is not mutated.
func (c *cowValue) readDirect() interface{} {
	return c.value
}

// ============================================
// MemoryState Implementation
// ============================================

// MemoryState is an in-memory implementation of State and AdvancedState.
// It uses Copy-on-Write semantics for efficient storage of immutable values.
type MemoryState struct {
	data     map[string]*cowValue
	metadata map[string]*cowValue
	mu       sync.RWMutex
	version  uint64

	// observerHub is lazily initialized. Access must be synchronized via s.mu.
	observers  map[string]*stateObserver
	observerMu sync.RWMutex
	closed     bool

	// stats for monitoring
	stats stateStats
}

// stateStats tracks operational statistics.
type stateStats struct {
	gets      atomic.Uint64
	sets      atomic.Uint64
	copies    atomic.Uint64
	cacheHits atomic.Uint64 // immutable values returned without copy
}

// NewState creates a new in-memory state.
func NewState() *MemoryState {
	return &MemoryState{
		data:     make(map[string]*cowValue),
		metadata: make(map[string]*cowValue),
		version:  uint64(initialStateVersion),
	}
}

// NewMemoryState creates a new in-memory state (alias for NewState).
func NewMemoryState() *MemoryState {
	return NewState()
}

// Get retrieves a value from the state.
// Immutable values (primitives) are returned directly.
// Mutable values are deep copied to prevent external mutation.
func (s *MemoryState) Get(key string) (interface{}, bool) {
	s.stats.gets.Add(1)

	s.mu.RLock()
	cow, exists := s.data[key]
	s.mu.RUnlock()

	if !exists {
		return nil, false
	}

	if cow.immutable {
		s.stats.cacheHits.Add(1)
		return cow.value, true
	}

	s.stats.copies.Add(1)
	return cow.read(), true
}

// GetTyped retrieves a value with type safety using reflection.
// The target must be a pointer to the expected type.
// Returns true if the key exists and the type matches.
func (s *MemoryState) GetTyped(key string, target interface{}) bool {
	if target == nil {
		return false
	}

	targetVal := reflect.ValueOf(target)
	if targetVal.Kind() != reflect.Ptr || targetVal.IsNil() {
		return false
	}

	val, exists := s.Get(key)
	if !exists {
		return false
	}

	valReflect := reflect.ValueOf(val)
	targetElem := targetVal.Elem()

	// Check type compatibility
	if !valReflect.Type().AssignableTo(targetElem.Type()) {
		// Try conversion for numeric types
		if valReflect.Type().ConvertibleTo(targetElem.Type()) {
			targetElem.Set(valReflect.Convert(targetElem.Type()))
			return true
		}
		return false
	}

	targetElem.Set(valReflect)
	return true
}

// Set stores a value in the state.
// Immutable values are stored directly; mutable values are deep copied.
func (s *MemoryState) Set(key string, value interface{}) {
	s.stats.sets.Add(1)

	// Create COW value (handles copying for mutable types)
	s.mu.Lock()
	s.version++
	cow := newCOWValue(value, s.version)
	s.data[key] = cow

	// Publish event while holding lock
	s.publishEventLocked(StateEvent{
		Type:      StateEventSet,
		Key:       key,
		Value:     cow.readDirect(), // Use direct read for event
		Version:   s.version,
		Timestamp: time.Now().UTC(),
	})
	s.mu.Unlock()
}

// SetTyped stores a value with the same semantics as Set.
func (s *MemoryState) SetTyped(key string, value interface{}) {
	s.Set(key, value)
}

// Delete removes a value from the state.
func (s *MemoryState) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.data[key]; exists {
		delete(s.data, key)
		s.version++

		s.publishEventLocked(StateEvent{
			Type:      StateEventDelete,
			Key:       key,
			Version:   s.version,
			Timestamp: time.Now().UTC(),
		})
	}
}

// Keys returns all keys in the state.
func (s *MemoryState) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	return keys
}

// Clone creates a deep copy of the state.
func (s *MemoryState) Clone() State {
	s.mu.RLock()
	defer s.mu.RUnlock()

	clone := &MemoryState{
		data:     make(map[string]*cowValue, len(s.data)),
		metadata: make(map[string]*cowValue, len(s.metadata)),
		version:  s.version,
	}

	// Deep copy all COW values
	for k, cow := range s.data {
		if cow.immutable {
			// Immutable values can be shared
			clone.data[k] = &cowValue{
				value:     cow.value,
				immutable: true,
				version:   cow.version,
				frozen:    true,
			}
		} else {
			// Mutable values must be copied
			clone.data[k] = &cowValue{
				value:     deepCopyValue(cow.value),
				immutable: false,
				version:   cow.version,
				frozen:    false,
			}
		}
	}

	for k, cow := range s.metadata {
		if cow.immutable {
			clone.metadata[k] = &cowValue{
				value:     cow.value,
				immutable: true,
				version:   cow.version,
				frozen:    true,
			}
		} else {
			clone.metadata[k] = &cowValue{
				value:     deepCopyValue(cow.value),
				immutable: false,
				version:   cow.version,
				frozen:    false,
			}
		}
	}

	return clone
}

// Snapshot creates a snapshot of the current state.
func (s *MemoryState) Snapshot() *StateSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data := make(map[string]interface{}, len(s.data))
	for k, cow := range s.data {
		if cow.immutable {
			data[k] = cow.value
		} else {
			data[k] = deepCopyValue(cow.value)
		}
	}

	metadata := make(map[string]interface{}, len(s.metadata))
	for k, cow := range s.metadata {
		if cow.immutable {
			metadata[k] = cow.value
		} else {
			metadata[k] = deepCopyValue(cow.value)
		}
	}

	return &StateSnapshot{
		Version:   int64(s.version),
		Timestamp: time.Now().UTC(),
		Data:      data,
		Metadata:  metadata,
	}
}

// Restore restores state from a snapshot.
func (s *MemoryState) Restore(snapshot *StateSnapshot) {
	if snapshot == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Clear existing data
	s.data = make(map[string]*cowValue, len(snapshot.Data))
	s.metadata = make(map[string]*cowValue, len(snapshot.Metadata))

	// Restore with COW wrapping
	for k, v := range snapshot.Data {
		s.data[k] = newCOWValue(v, uint64(snapshot.Version))
	}

	for k, v := range snapshot.Metadata {
		s.metadata[k] = newCOWValue(v, uint64(snapshot.Version))
	}

	s.version = uint64(snapshot.Version)
	if s.version == 0 {
		s.version = uint64(initialStateVersion)
	}

	s.publishEventLocked(StateEvent{
		Type:      StateEventRestore,
		Version:   s.version,
		Timestamp: time.Now().UTC(),
	})
}

// Metadata returns a copy of all metadata
func (s *MemoryState) Metadata() map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.metadata == nil {
		return make(map[string]interface{})
	}

	// Deep copy metadata
	result := make(map[string]interface{}, len(s.metadata))
	for k, cow := range s.metadata {
		if cow.immutable {
			result[k] = cow.value
		} else {
			result[k] = deepCopyValue(cow.value)
		}
	}
	return result
}

// SetMetadata sets a metadata value
func (s *MemoryState) SetMetadata(key string, value interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Copy-on-write for metadata
	s.version++
	cow := newCOWValue(value, s.version)
	s.metadata[key] = cow

	// Publish metadata change event
	s.publishEventLocked(StateEvent{
		Type:      StateEventSet,
		Key:       key,
		Value:     cow.readDirect(),
		Version:   s.version,
		Timestamp: time.Now().UTC(),
	})
}

// GetMetadata retrieves a metadata value
func (s *MemoryState) GetMetadata(key string) (interface{}, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.metadata == nil {
		return nil, false
	}

	cow, exists := s.metadata[key]
	if !exists {
		return nil, false
	}
	
	if cow.immutable {
		return cow.value, true
	}
	return cow.read(), true
}

// Marshal serializes the state to JSON
func (s *MemoryState) Marshal() ([]byte, error) {
	snapshot := s.Snapshot()
	return json.Marshal(snapshot)
}

// Unmarshal deserializes JSON into the state
func (s *MemoryState) Unmarshal(data []byte) error {
	if len(data) == 0 {
		return errors.New("graph: empty state payload")
	}

	var snapshot StateSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return fmt.Errorf("graph: unmarshal state: %w", err)
	}

	s.Restore(&snapshot)
	return nil
}

// MarshalJSON implements json.Marshaler interface.
func (s *MemoryState) MarshalJSON() ([]byte, error) {
	return s.Marshal()
}

// UnmarshalJSON implements json.Unmarshaler interface.
func (s *MemoryState) UnmarshalJSON(data []byte) error {
	return s.Unmarshal(data)
}

// Iterate calls the provided function for each key-value pair
func (s *MemoryState) Iterate(fn func(key string, value any) bool) {
	s.mu.RLock()
	// Take a snapshot of keys
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	s.mu.RUnlock()

	// Sort keys for deterministic iteration
	sort.Strings(keys)

	for _, key := range keys {
		s.mu.RLock()
		cow, exists := s.data[key]
		s.mu.RUnlock()

		if !exists {
			continue // Key was deleted during iteration
		}

		var valueCopy interface{}
		if cow.immutable {
			valueCopy = cow.value
		} else {
			valueCopy = cow.read()
		}
		
		if !fn(key, valueCopy) {
			break
		}
	}
}

// KeysWithPrefix returns all keys that start with the given prefix
func (s *MemoryState) KeysWithPrefix(prefix string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var keys []string
	for k := range s.data {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}

	sort.Strings(keys)
	return keys
}

// Subtree returns a new State containing only keys with the given prefix
func (s *MemoryState) Subtree(prefix string) *MemoryState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	subtree := NewState()

	for k, cow := range s.data {
		if strings.HasPrefix(k, prefix) {
			newKey := strings.TrimPrefix(k, prefix)
			// Preserve exact-match empty keys, and only trim separators when the
			// caller provided a separator-terminated prefix.
			if strings.HasSuffix(prefix, ".") || strings.HasSuffix(prefix, "/") {
				newKey = strings.TrimPrefix(newKey, ".")
				newKey = strings.TrimPrefix(newKey, "/")
			}
			// We can share immutable COWs, but must copy mutable ones for safety
			// unless we implement a shared backing store logic
			subtree.data[newKey] = newCOWValue(cow.read(), s.version)
		}
	}

	return subtree
}

// DeletePrefix removes all keys with the given prefix
func (s *MemoryState) DeletePrefix(prefix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Find keys to delete
	keysToDelete := make([]string, 0)
	for k := range s.data {
		if strings.HasPrefix(k, prefix) {
			keysToDelete = append(keysToDelete, k)
		}
	}

	if len(keysToDelete) == 0 {
		return 0
	}

	for _, k := range keysToDelete {
		delete(s.data, k)
	}
	s.version++

	// Publish events for each deleted key
	for _, key := range keysToDelete {
		s.publishEventLocked(StateEvent{
			Type:      StateEventDelete,
			Key:       key,
			Version:   s.version,
			Timestamp: time.Now().UTC(),
		})
	}

	return len(keysToDelete)
}

// Version returns the current version of the state
func (s *MemoryState) Version() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version
}

// Watch registers an observer for state changes
func (s *MemoryState) Watch(ctx context.Context) (<-chan StateEvent, func()) {
	s.mu.Lock()
	s.ensureObserverHubLocked()
	s.mu.Unlock()

	// Create buffered channel to prevent blocking
	eventCh := make(chan StateEvent, 100)

	// Create observer
	observer := &stateObserver{
		id:      generateObserverID(),
		eventCh: eventCh,
		ctx:     ctx,
	}

	// Register observer
	s.observerMu.Lock()
	s.observers[observer.id] = observer
	s.observerMu.Unlock()

	// Create unsubscribe function
	unsubscribe := func() {
		s.Unwatch(observer.id)
	}

	// Handle context cancellation
	go func() {
		<-ctx.Done()
		unsubscribe()
	}()

	return eventCh, unsubscribe
}

// stateObserver represents a registered observer
type stateObserver struct {
	id      string
	eventCh chan StateEvent
	ctx     context.Context
}

// generateObserverID creates a unique observer ID
func generateObserverID() string {
	return fmt.Sprintf("observer_%d_%d", time.Now().UnixNano(), rand.Int63())
}

// Unwatch removes an observer by ID
func (s *MemoryState) Unwatch(observerID string) {
	s.observerMu.Lock()
	defer s.observerMu.Unlock()

	if observer, exists := s.observers[observerID]; exists {
		close(observer.eventCh)
		delete(s.observers, observerID)
	}
}

// publishEventLocked publishes an event (caller must hold s.mu)
func (s *MemoryState) publishEventLocked(event StateEvent) {
	s.observerMu.RLock()
	defer s.observerMu.RUnlock()

	s.publishEventToObservers(event)
}

// publishEventToObservers sends event to all observers (caller must hold observerMu)
func (s *MemoryState) publishEventToObservers(event StateEvent) {
	for _, observer := range s.observers {
		select {
		case <-observer.ctx.Done():
			// Observer context cancelled, skip
			continue
		case observer.eventCh <- event:
			// Event sent successfully
		default:
			// Channel full, drop event
		}
	}
}

// ensureObserverHubLocked initializes the observer map if needed (caller must hold s.mu)
func (s *MemoryState) ensureObserverHubLocked() {
	if s.observers == nil {
		s.observers = make(map[string]*stateObserver)
	}
}

// Close cleans up the state and closes all observer channels
func (s *MemoryState) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.observerMu.Lock()
	defer s.observerMu.Unlock()

	// Close all observer channels
	for id, observer := range s.observers {
		close(observer.eventCh)
		delete(s.observers, id)
	}

	// Clear data
	s.data = nil
	s.metadata = nil
	s.closed = true

	return nil
}

// IsClosed returns whether the state has been closed
func (s *MemoryState) IsClosed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed
}

// ============================================================================
// Deep Copy Utilities
// ============================================================================

// deepCopyMap creates a deep copy of a map
func deepCopyMap(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}

	result := make(map[string]interface{}, len(m))
	for k, v := range m {
		result[k] = deepCopyValue(v)
	}
	return result
}

// deepCopyValue creates a deep copy of a value
func deepCopyValue(v any) any {
	if v == nil {
		return nil
	}

	switch val := v.(type) {
	case string, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, bool:
		// Primitive types are immutable, return as-is
		return val

	case []byte:
		// Copy byte slices
		cp := make([]byte, len(val))
		copy(cp, val)
		return cp

	case map[string]interface{}:
		return deepCopyMap(val)

	case map[string]string:
		cp := make(map[string]string, len(val))
		for k, v := range val {
			cp[k] = v
		}
		return cp

	case map[string]int:
		cp := make(map[string]int, len(val))
		for k, v := range val {
			cp[k] = v
		}
		return cp

	case []interface{}:
		return deepCopySlice(val)

	case []map[string]interface{}:
		cp := make([]map[string]interface{}, len(val))
		for i, item := range val {
			cp[i] = deepCopyMap(item)
		}
		return cp

	case []string:
		cp := make([]string, len(val))
		copy(cp, val)
		return cp

	case []int:
		cp := make([]int, len(val))
		copy(cp, val)
		return cp

	case []float64:
		cp := make([]float64, len(val))
		copy(cp, val)
		return cp

	case time.Time:
		return val // time.Time is immutable

	case *time.Time:
		if val == nil {
			return nil
		}
		cp := *val
		return &cp

	case DeepCopyable:
		// If the value implements DeepCopyable, use its method
		return val.DeepCopy()

	case json.RawMessage:
		cp := make(json.RawMessage, len(val))
		copy(cp, val)
		return cp

	default:
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Struct:
			if isReflectivelyCopyableType(rv.Type()) {
				cp := reflect.New(rv.Type()).Elem()
				cp.Set(rv)
				return cp.Interface()
			}
		case reflect.Ptr:
			if rv.IsNil() {
				return v
			}
			if rv.Elem().Kind() == reflect.Struct && isReflectivelyCopyableType(rv.Elem().Type()) {
				cp := reflect.New(rv.Elem().Type())
				cp.Elem().Set(rv.Elem())
				return cp.Interface()
			}
		}
		// For unknown types, try JSON round-trip as fallback
		return jsonDeepCopy(val)
	}
}

func isReflectivelyCopyableType(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128,
		reflect.String:
		return true
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if !isReflectivelyCopyableType(t.Field(i).Type) {
				return false
			}
		}
		return true
	case reflect.Array:
		return isReflectivelyCopyableType(t.Elem())
	case reflect.Ptr:
		return isReflectivelyCopyableType(t.Elem())
	default:
		return false
	}
}

// DeepCopyable interface for types that can deep copy themselves
type DeepCopyable interface {
	DeepCopy() any
}

// deepCopySlice creates a deep copy of a slice
func deepCopySlice(s []interface{}) []interface{} {
	if s == nil {
		return nil
	}

	result := make([]interface{}, len(s))
	for i, v := range s {
		result[i] = deepCopyValue(v)
	}
	return result
}

// jsonDeepCopy uses JSON marshaling/unmarshaling for deep copy
// This is a fallback for complex types.
// Panics if the value cannot be JSON-serialized to prevent isolation breaches.
func jsonDeepCopy(v any) any {
	data, err := json.Marshal(v)
	if err != nil {
		// Fail-fast: panic instead of returning the original pointer
		// This enforces the JSON-serializability contract and prevents data corruption
		panic(fmt.Sprintf(
			"state: cannot deep copy non-JSON-serializable type %T. "+
				"State values must be JSON-compatible (maps, slices, primitives, "+
				"or structs with exported fields). "+
				"Common issues: unexported struct fields, channels, functions, sync.Mutex. "+
				"Fix: use only exported fields or implement json.Marshaler/json.Unmarshaler",
			v,
		))
	}

	var result interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		// Fail-fast: panic instead of returning the original pointer
		panic(fmt.Sprintf(
			"state: cannot deep copy non-JSON-serializable type %T. "+
				"State values must be JSON-compatible (maps, slices, primitives, "+
				"or structs with exported fields). "+
				"Common issues: unexported struct fields, channels, functions, sync.Mutex. "+
				"Fix: use only exported fields or implement json.Marshaler/json.Unmarshaler",
			v,
		))
	}

	return result
}

// ============================================================================
// StateHistory - Tracks state changes over time
// ============================================================================

// StateHistory maintains a history of state snapshots
type StateHistory struct {
	mu          sync.RWMutex
	snapshots   []*StateSnapshot
	maxSize     int
}

// NewStateHistory creates a new state history tracker
func NewStateHistory(maxSize int) *StateHistory {
	if maxSize <= 0 {
		maxSize = 100 // Default max size
	}
	return &StateHistory{
		snapshots: make([]*StateSnapshot, 0, maxSize),
		maxSize:   maxSize,
	}
}

// Record adds a new state snapshot to the history
func (h *StateHistory) Record(snapshot *StateSnapshot) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// If at capacity, remove oldest entry
	if len(h.snapshots) >= h.maxSize {
		h.snapshots = h.snapshots[1:]
	}

	h.snapshots = append(h.snapshots, snapshot.Clone())
}

// Get retrieves a snapshot at a specific index
func (h *StateHistory) Get(index int) (*StateSnapshot, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if index < 0 || index >= len(h.snapshots) {
		return nil, false
	}
	return h.snapshots[index].Clone(), true
}

// Latest returns the most recent state snapshot
func (h *StateHistory) Latest() (*StateSnapshot, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.snapshots) == 0 {
		return nil, false
	}
	return h.snapshots[len(h.snapshots)-1].Clone(), true
}

// Clear removes all entries from the history
func (h *StateHistory) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.snapshots = make([]*StateSnapshot, 0, h.maxSize)
}

// Count returns the number of entries in the history
func (h *StateHistory) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return len(h.snapshots)
}

// All returns a copy of all history entries
func (h *StateHistory) All() []*StateSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make([]*StateSnapshot, len(h.snapshots))
	copy(result, h.snapshots)
	return result
}

// ============================================
// StateSnapshot
// ============================================

// StateSnapshot represents a point-in-time snapshot of state.
type StateSnapshot struct {
	Version   int64                  `json:"version"`
	Timestamp time.Time              `json:"timestamp"`
	Data      map[string]interface{} `json:"data"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// Clone creates a deep copy of the snapshot.
func (s *StateSnapshot) Clone() *StateSnapshot {
	if s == nil {
		return nil
	}
	return &StateSnapshot{
		Version:   s.Version,
		Timestamp: s.Timestamp,
		Data:      deepCopyMap(s.Data),
		Metadata:  deepCopyMap(s.Metadata),
	}
}

// ============================================
// Events
// ============================================

type StateEventType string

const (
	StateEventSet     StateEventType = "set"
	StateEventDelete  StateEventType = "delete"
	StateEventRestore StateEventType = "restore"
)

type StateEvent struct {

	Type      StateEventType `json:"type"`

	Key       string         `json:"key,omitempty"`

	Value     interface{}    `json:"value,omitempty"`

	Version   uint64         `json:"version"`

	Timestamp time.Time      `json:"timestamp"`

}



// StateObserver provides a channel for receiving events and a Stop method.

type StateObserver struct {

	Events <-chan StateEvent

	stop   func()

	id     string

}



// Stop unregisters the observer and closes its channel.

func (o *StateObserver) Stop() {

	if o != nil && o.stop != nil {

		o.stop()

	}

}
