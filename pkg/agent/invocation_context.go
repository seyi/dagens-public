package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// InvocationContext tracks the execution state during agent invocations,
// including transfer history for LLM-driven agent delegation.
// Designed for Spark distributed environments with partition-aware tracking.
type InvocationContext struct {
	// Execution context
	Ctx context.Context

	// Input provided to the agent
	Input *AgentInput

	// Current agent executing (changes with transfers)
	CurrentAgent Agent

	// Root agent that started the invocation chain
	RootAgent Agent

	// Transfer history for observability and debugging
	TransferHistory []Transfer

	// Maximum number of transfers allowed (prevents infinite loops)
	MaxTransfers int

	// Current partition (for distributed execution tracking)
	CurrentPartition string

	// Node ID for distributed scenarios
	NodeID string

	// Metadata for custom tracking
	Metadata map[string]interface{}

	// Thread-safe access
	mu sync.RWMutex
}

// Transfer represents a single agent transfer event
type Transfer struct {
	// Source agent name
	FromAgent string

	// Source agent ID
	FromAgentID string

	// Source partition
	FromPartition string

	// Target agent name
	ToAgent string

	// Target agent ID
	ToAgentID string

	// Target partition
	ToPartition string

	// Reason for transfer (optional, from LLM)
	Reason string

	// Transfer timestamp
	Timestamp time.Time

	// Transfer type (local or remote)
	TransferType TransferType

	// Node ID where transfer originated
	NodeID string
}

// TransferType indicates whether transfer is local or cross-partition
type TransferType string

const (
	// TransferTypeLocal indicates transfer within same partition/node
	TransferTypeLocal TransferType = "local"

	// TransferTypeRemote indicates cross-partition transfer
	TransferTypeRemote TransferType = "remote"

	// TransferTypeCrossNode indicates cross-node transfer in distributed setup
	TransferTypeCrossNode TransferType = "cross_node"
)

// NewInvocationContext creates a new invocation context
func NewInvocationContext(ctx context.Context, input *AgentInput, rootAgent Agent) *InvocationContext {
	ic := &InvocationContext{
		Ctx:              ctx,
		Input:            input,
		CurrentAgent:     rootAgent,
		RootAgent:        rootAgent,
		TransferHistory:  []Transfer{},
		MaxTransfers:     10, // Default limit
		CurrentPartition: rootAgent.Partition(),
		NodeID:           "", // Will be set in distributed scenarios
		Metadata:         make(map[string]interface{}),
	}

	return ic
}

// TransferTo transfers execution to a target agent
// Returns error if max transfers exceeded or target not found
func (ic *InvocationContext) TransferTo(targetAgent Agent, reason string) error {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	// Check transfer limit
	if len(ic.TransferHistory) >= ic.MaxTransfers {
		return fmt.Errorf("maximum transfers (%d) exceeded - possible infinite loop", ic.MaxTransfers)
	}

	// Determine transfer type
	transferType := ic.determineTransferType(ic.CurrentAgent, targetAgent)

	// Create transfer record
	transfer := Transfer{
		FromAgent:     ic.CurrentAgent.Name(),
		FromAgentID:   ic.CurrentAgent.ID(),
		FromPartition: ic.CurrentAgent.Partition(),
		ToAgent:       targetAgent.Name(),
		ToAgentID:     targetAgent.ID(),
		ToPartition:   targetAgent.Partition(),
		Reason:        reason,
		Timestamp:     time.Now(),
		TransferType:  transferType,
		NodeID:        ic.NodeID,
	}

	// Add to history
	ic.TransferHistory = append(ic.TransferHistory, transfer)

	// Update current agent and partition
	ic.CurrentAgent = targetAgent
	ic.CurrentPartition = targetAgent.Partition()

	return nil
}

// determineTransferType determines if transfer is local or remote
func (ic *InvocationContext) determineTransferType(from, to Agent) TransferType {
	// Same partition = local
	if from.Partition() == to.Partition() {
		return TransferTypeLocal
	}

	// Different partition but same node ID (if set)
	if ic.NodeID != "" {
		// In a real distributed system, we'd check if target is on same node
		// For now, assume different partition = remote
		return TransferTypeRemote
	}

	return TransferTypeRemote
}

// GetTransferCount returns the number of transfers that have occurred
func (ic *InvocationContext) GetTransferCount() int {
	ic.mu.RLock()
	defer ic.mu.RUnlock()
	return len(ic.TransferHistory)
}

// GetTransferPath returns the path of agent names in transfer order
func (ic *InvocationContext) GetTransferPath() []string {
	ic.mu.RLock()
	defer ic.mu.RUnlock()

	if len(ic.TransferHistory) == 0 {
		return []string{ic.RootAgent.Name()}
	}

	path := []string{ic.RootAgent.Name()}
	for _, transfer := range ic.TransferHistory {
		path = append(path, transfer.ToAgent)
	}

	return path
}

// GetLastTransfer returns the most recent transfer, or nil if none
func (ic *InvocationContext) GetLastTransfer() *Transfer {
	ic.mu.RLock()
	defer ic.mu.RUnlock()

	if len(ic.TransferHistory) == 0 {
		return nil
	}

	return &ic.TransferHistory[len(ic.TransferHistory)-1]
}

// HasTransferredAcrossPartitions returns true if any cross-partition transfer occurred
func (ic *InvocationContext) HasTransferredAcrossPartitions() bool {
	ic.mu.RLock()
	defer ic.mu.RUnlock()

	for _, transfer := range ic.TransferHistory {
		if transfer.TransferType == TransferTypeRemote || transfer.TransferType == TransferTypeCrossNode {
			return true
		}
	}

	return false
}

// GetPartitionTransferCount returns count of cross-partition transfers
func (ic *InvocationContext) GetPartitionTransferCount() int {
	ic.mu.RLock()
	defer ic.mu.RUnlock()

	count := 0
	for _, transfer := range ic.TransferHistory {
		if transfer.TransferType == TransferTypeRemote || transfer.TransferType == TransferTypeCrossNode {
			count++
		}
	}

	return count
}

// SetNodeID sets the node ID for distributed tracking
func (ic *InvocationContext) SetNodeID(nodeID string) {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	ic.NodeID = nodeID
}

// SetMaxTransfers updates the maximum allowed transfers
func (ic *InvocationContext) SetMaxTransfers(max int) {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	ic.MaxTransfers = max
}

// --- Serialization for Distributed Execution ---

// SerializableInvocationContext is the JSON-serializable version
// Used for cross-partition/cross-node communication in Spark environments
type SerializableInvocationContext struct {
	InputJSON        []byte              `json:"input_json"`
	CurrentAgentID   string              `json:"current_agent_id"`
	CurrentAgentName string              `json:"current_agent_name"`
	RootAgentID      string              `json:"root_agent_id"`
	RootAgentName    string              `json:"root_agent_name"`
	TransferHistory  []Transfer          `json:"transfer_history"`
	MaxTransfers     int                 `json:"max_transfers"`
	CurrentPartition string              `json:"current_partition"`
	NodeID           string              `json:"node_id"`
	Metadata         map[string]interface{} `json:"metadata"`
}

// Serialize converts InvocationContext to serializable format
// for transmission across Spark partitions/nodes
func (ic *InvocationContext) Serialize() (*SerializableInvocationContext, error) {
	ic.mu.RLock()
	defer ic.mu.RUnlock()

	// Serialize input
	inputJSON, err := json.Marshal(ic.Input)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize input: %w", err)
	}

	return &SerializableInvocationContext{
		InputJSON:        inputJSON,
		CurrentAgentID:   ic.CurrentAgent.ID(),
		CurrentAgentName: ic.CurrentAgent.Name(),
		RootAgentID:      ic.RootAgent.ID(),
		RootAgentName:    ic.RootAgent.Name(),
		TransferHistory:  ic.TransferHistory,
		MaxTransfers:     ic.MaxTransfers,
		CurrentPartition: ic.CurrentPartition,
		NodeID:           ic.NodeID,
		Metadata:         ic.Metadata,
	}, nil
}

// Deserialize reconstructs InvocationContext from serializable format
// Note: Requires access to agent registry to resolve agent IDs
func (sic *SerializableInvocationContext) Deserialize(
	ctx context.Context,
	agentResolver func(id string) (Agent, error),
) (*InvocationContext, error) {
	// Deserialize input
	var input AgentInput
	if err := json.Unmarshal(sic.InputJSON, &input); err != nil {
		return nil, fmt.Errorf("failed to deserialize input: %w", err)
	}

	// Resolve agents
	currentAgent, err := agentResolver(sic.CurrentAgentID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve current agent %s: %w", sic.CurrentAgentID, err)
	}

	rootAgent, err := agentResolver(sic.RootAgentID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve root agent %s: %w", sic.RootAgentID, err)
	}

	return &InvocationContext{
		Ctx:              ctx,
		Input:            &input,
		CurrentAgent:     currentAgent,
		RootAgent:        rootAgent,
		TransferHistory:  sic.TransferHistory,
		MaxTransfers:     sic.MaxTransfers,
		CurrentPartition: sic.CurrentPartition,
		NodeID:           sic.NodeID,
		Metadata:         sic.Metadata,
	}, nil
}

// ToJSON converts to JSON string for debugging/logging
func (ic *InvocationContext) ToJSON() (string, error) {
	ic.mu.RLock()
	defer ic.mu.RUnlock()

	data := map[string]interface{}{
		"current_agent":    ic.CurrentAgent.Name(),
		"root_agent":       ic.RootAgent.Name(),
		"transfer_count":   len(ic.TransferHistory),
		"transfer_path":    ic.GetTransferPath(),
		"current_partition": ic.CurrentPartition,
		"node_id":          ic.NodeID,
		"max_transfers":    ic.MaxTransfers,
	}

	jsonBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", err
	}

	return string(jsonBytes), nil
}

// Clone creates a deep copy of the invocation context
// Useful for branching execution paths or parallel transfers
func (ic *InvocationContext) Clone() *InvocationContext {
	ic.mu.RLock()
	defer ic.mu.RUnlock()

	// Deep copy transfer history
	historyClone := make([]Transfer, len(ic.TransferHistory))
	copy(historyClone, ic.TransferHistory)

	// Deep copy metadata
	metadataClone := make(map[string]interface{})
	for k, v := range ic.Metadata {
		metadataClone[k] = v
	}

	// Clone input context
	var inputClone *AgentInput
	if ic.Input != nil {
		inputContextClone := make(map[string]interface{})
		for k, v := range ic.Input.Context {
			inputContextClone[k] = v
		}

		inputClone = &AgentInput{
			TaskID:      ic.Input.TaskID,
			Instruction: ic.Input.Instruction,
			Context:     inputContextClone,
			Tools:       ic.Input.Tools,
			Model:       ic.Input.Model,
			MaxRetries:  ic.Input.MaxRetries,
			Timeout:     ic.Input.Timeout,
		}
	}

	return &InvocationContext{
		Ctx:              ic.Ctx,
		Input:            inputClone,
		CurrentAgent:     ic.CurrentAgent,
		RootAgent:        ic.RootAgent,
		TransferHistory:  historyClone,
		MaxTransfers:     ic.MaxTransfers,
		CurrentPartition: ic.CurrentPartition,
		NodeID:           ic.NodeID,
		Metadata:         metadataClone,
	}
}
