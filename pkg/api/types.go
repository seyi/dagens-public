package api

// JobSubmissionRequest represents the payload to submit a new job
type JobSubmissionRequest struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Nodes       []NodeDefinition `json:"nodes"`
	Edges       []EdgeDefinition `json:"edges"`
	EntryNode   string           `json:"entry_node"`
	FinishNodes []string         `json:"finish_nodes"`
	Input       JobInput         `json:"input"`
	// SessionID enables sticky scheduling. When provided, all tasks in this job
	// will be routed to the same worker as previous jobs with this SessionID,
	// preserving in-memory context on that worker.
	SessionID string `json:"session_id,omitempty"`
}

// NodeDefinition defines a node in the graph
type NodeDefinition struct {
	ID       string                 `json:"id"`
	Type     string                 `json:"type"` // "function", "agent", "policy", "parallel", "loop", "conditional"
	Name     string                 `json:"name,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
	// For "agent" type nodes
	AgentConfig *AgentNodeConfig `json:"agent_config,omitempty"`
	// For "policy" type nodes (guardrails)
	PolicyConfig *PolicyNodeConfig `json:"policy_config,omitempty"`
	// For "parallel" nodes, children are referenced by edge relationships in the flat list,
	// OR we can allow nesting. Flat list with edges is usually cleaner for APIs.
	// However, Dagens ParallelNode struct expects `[]Node` children.
	// To keep JSON simple, we might use a "Children" list of IDs for structural nodes.
	Children []string `json:"children,omitempty"`
}

// PolicyNodeConfig specific configuration for policy/guardrail nodes
type PolicyNodeConfig struct {
	Rules            []PolicyRuleDefinition `json:"rules"`
	FailOpen         bool                   `json:"fail_open,omitempty"`
	StopOnFirstMatch bool                   `json:"stop_on_first_match,omitempty"`
}

// PolicyRuleDefinition defines a single policy rule
type PolicyRuleDefinition struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name,omitempty"`
	Type     string                 `json:"type"`   // "pii", "content", "length", "regex"
	Action   string                 `json:"action"` // "pass", "block", "redact", "warn"
	Severity string                 `json:"severity,omitempty"`
	Enabled  bool                   `json:"enabled"`
	Config   map[string]interface{} `json:"config,omitempty"`
}

// AgentNodeConfig specific configuration for agent nodes
type AgentNodeConfig struct {
	Model       string   `json:"model,omitempty"`
	Instruction string   `json:"instruction,omitempty"`
	Tools       []string `json:"tools,omitempty"`
}

// EdgeDefinition defines a connection between nodes
type EdgeDefinition struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// JobInput defines the initial input for the job
type JobInput struct {
	Instruction string                 `json:"instruction"`
	Data        map[string]interface{} `json:"data,omitempty"`
}

// JobResponse represents the standard response for job operations
type JobResponse struct {
	JobID     string `json:"job_id"`
	Status    string `json:"status"`
	SubmittedAt string `json:"submitted_at"`
	Message   string `json:"message,omitempty"`
}
