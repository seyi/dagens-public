package graph

// Edge represents a connection between two nodes in the graph.
type Edge interface {
	// From returns the source node ID.
	From() string

	// To returns the target node ID.
	To() string

	// Type returns the edge type (e.g., "direct", "conditional").
	Type() string

	// ShouldTraverse determines if this edge should be followed based on state.
	// Returns true if the edge should be traversed.
	ShouldTraverse(state State) bool

	// Metadata returns edge-specific metadata.
	Metadata() map[string]interface{}
}

// DirectEdge is an unconditional edge that always traverses.
type DirectEdge struct {
	from     string
	to       string
	metadata map[string]interface{}
}

// NewDirectEdge creates a new direct edge.
func NewDirectEdge(from, to string) *DirectEdge {
	return &DirectEdge{
		from:     from,
		to:       to,
		metadata: make(map[string]interface{}),
	}
}

// From returns the source node ID.
func (e *DirectEdge) From() string {
	return e.from
}

// To returns the target node ID.
func (e *DirectEdge) To() string {
	return e.to
}

// Type returns "direct".
func (e *DirectEdge) Type() string {
	return "direct"
}

// ShouldTraverse always returns true for direct edges.
func (e *DirectEdge) ShouldTraverse(state State) bool {
	return true
}

// Metadata returns the edge metadata.
func (e *DirectEdge) Metadata() map[string]interface{} {
	return e.metadata
}

// SetMetadata sets a metadata value.
func (e *DirectEdge) SetMetadata(key string, value interface{}) {
	e.metadata[key] = value
}

// ConditionalEdge is an edge that only traverses if a condition is met.
type ConditionalEdge struct {
	from      string
	to        string
	condition func(state State) bool
	metadata  map[string]interface{}
}

// ConditionalEdgeConfig holds configuration for conditional edges.
type ConditionalEdgeConfig struct {
	From      string
	To        string
	Condition func(state State) bool
	Metadata  map[string]interface{}
}

// NewConditionalEdge creates a new conditional edge.
func NewConditionalEdge(cfg ConditionalEdgeConfig) *ConditionalEdge {
	metadata := cfg.Metadata
	if metadata == nil {
		metadata = make(map[string]interface{})
	}

	return &ConditionalEdge{
		from:      cfg.From,
		to:        cfg.To,
		condition: cfg.Condition,
		metadata:  metadata,
	}
}

// From returns the source node ID.
func (e *ConditionalEdge) From() string {
	return e.from
}

// To returns the target node ID.
func (e *ConditionalEdge) To() string {
	return e.to
}

// Type returns "conditional".
func (e *ConditionalEdge) Type() string {
	return "conditional"
}

// ShouldTraverse evaluates the condition.
func (e *ConditionalEdge) ShouldTraverse(state State) bool {
	if e.condition == nil {
		return true
	}
	return e.condition(state)
}

// Metadata returns the edge metadata.
func (e *ConditionalEdge) Metadata() map[string]interface{} {
	return e.metadata
}

// SetMetadata sets a metadata value.
func (e *ConditionalEdge) SetMetadata(key string, value interface{}) {
	e.metadata[key] = value
}

// DynamicEdge selects the target node dynamically based on state.
type DynamicEdge struct {
	from     string
	selector func(state State) string // Returns target node ID
	metadata map[string]interface{}
}

// DynamicEdgeConfig holds configuration for dynamic edges.
type DynamicEdgeConfig struct {
	From     string
	Selector func(state State) string
	Metadata map[string]interface{}
}

// NewDynamicEdge creates a new dynamic edge.
func NewDynamicEdge(cfg DynamicEdgeConfig) *DynamicEdge {
	metadata := cfg.Metadata
	if metadata == nil {
		metadata = make(map[string]interface{})
	}

	return &DynamicEdge{
		from:     cfg.From,
		selector: cfg.Selector,
		metadata: metadata,
	}
}

// From returns the source node ID.
func (e *DynamicEdge) From() string {
	return e.from
}

// To returns the dynamically selected target node ID.
// Note: This evaluates the selector with the current state.
func (e *DynamicEdge) To() string {
	// For interface compatibility, return empty string
	// Actual target is determined at runtime via selector
	return ""
}

// ToWithState returns the target node ID based on current state.
func (e *DynamicEdge) ToWithState(state State) string {
	if e.selector == nil {
		return ""
	}
	return e.selector(state)
}

// Type returns "dynamic".
func (e *DynamicEdge) Type() string {
	return "dynamic"
}

// ShouldTraverse always returns true (target selection handles logic).
func (e *DynamicEdge) ShouldTraverse(state State) bool {
	// Dynamic edges always traverse, but to different targets
	return e.ToWithState(state) != ""
}

// Metadata returns the edge metadata.
func (e *DynamicEdge) Metadata() map[string]interface{} {
	return e.metadata
}

// SetMetadata sets a metadata value.
func (e *DynamicEdge) SetMetadata(key string, value interface{}) {
	e.metadata[key] = value
}
