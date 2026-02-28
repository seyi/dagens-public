package hitl

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/seyi/dagens/pkg/graph"
)

// HumanNode implements a node that can request human input either by blocking briefly
// or by checkpointing for longer waits.
type HumanNode struct {
	*graph.BaseNode
	prompt             string
	options            []string
	timeout            time.Duration
	shortWaitThreshold time.Duration // e.g., 5 minutes
	maxConcurrentWaits int           // Memory protection (e.g., 1000)

	responseMgr     *HumanResponseManager
	checkpointStore CheckpointStore
	callbackSecret  []byte // For HMAC signing

	// Metrics
	activeBlockingWaits atomic.Int32
}

// HumanNodeConfig holds configuration for creating a HumanNode
type HumanNodeConfig struct {
	ID                 string
	Prompt             string
	Options            []string
	Timeout            time.Duration
	ShortWaitThreshold time.Duration
	MaxConcurrentWaits int
	ResponseManager    *HumanResponseManager
	CheckpointStore    CheckpointStore
	CallbackSecret     []byte
}

// NewHumanNode creates a new HumanNode
func NewHumanNode(config HumanNodeConfig) *HumanNode {
	if config.ShortWaitThreshold == 0 {
		config.ShortWaitThreshold = 5 * time.Minute // Default 5 minutes
	}
	if config.MaxConcurrentWaits == 0 {
		config.MaxConcurrentWaits = 1000 // Default 1000 concurrent waits
	}

	node := &HumanNode{
		BaseNode:           graph.NewBaseNode(config.ID, "human"),
		prompt:             config.Prompt,
		options:            config.Options,
		timeout:            config.Timeout,
		shortWaitThreshold: config.ShortWaitThreshold,
		maxConcurrentWaits: config.MaxConcurrentWaits,
		responseMgr:        config.ResponseManager,
		checkpointStore:    config.CheckpointStore,
		callbackSecret:     config.CallbackSecret,
	}

	return node
}

// Execute implements the Node interface
func (n *HumanNode) Execute(ctx context.Context, state graph.State) error {
	// Check for pre-injected response (resumption path)
	pendingKey := fmt.Sprintf(StateKeyHumanPendingFmt, n.ID())
	if respData, exists := state.Get(pendingKey); exists {
		// Type assertion with safety check
		respBytes, ok := respData.([]byte)
		if !ok {
			return fmt.Errorf("invalid pending response type: expected []byte, got %T", respData)
		}

		var resp HumanResponse
		if err := json.Unmarshal(respBytes, &resp); err != nil {
			return fmt.Errorf("unmarshal human response: %w", err)
		}

		// Process response and update state
		if err := n.processResponse(state, &resp); err != nil {
			return fmt.Errorf("process human response: %w", err)
		}

		// Clean up pending key
		state.Delete(pendingKey)
		return nil // Allow graph to continue
	}

	// First-time execution: Create request
	reqID := generateRequestID()
	req := &HumanRequest{
		RequestID:   reqID,
		AgentName:   n.ID(),
		Instruction: n.prompt,
		Options:     n.options,
		CallbackURL: n.buildCallbackURL(reqID),
		Timeout:     n.timeout,
		Timestamp:   time.Now(),
	}

	// DECISION POINT: Short wait or checkpoint?
	if n.timeout > 0 && n.timeout <= n.shortWaitThreshold {
		return n.blockWithTimeout(ctx, state, req)
	}

	return n.checkpointAndReturn(ctx, state, req)
}

// blockWithTimeout: For short waits (< 5 minutes)
func (n *HumanNode) blockWithTimeout(ctx context.Context, state graph.State, req *HumanRequest) error {
	// Protect against resource exhaustion
	current := n.activeBlockingWaits.Add(1)
	defer n.activeBlockingWaits.Add(-1)

	if current > int32(n.maxConcurrentWaits) {
		return fmt.Errorf("max concurrent blocking waits reached (%d), use checkpoint mode",
			n.maxConcurrentWaits)
	}

	respChan := make(chan *HumanResponse, 1)
	errChan := make(chan error, 1)

	// Register callback handler
	n.responseMgr.RegisterCallback(req.RequestID, func(resp *HumanResponse) {
		respChan <- resp
	})
	defer n.responseMgr.Unregister(req.RequestID)

	// Send request notification (webhook, email, UI, etc.)
	if err := n.responseMgr.NotifyHuman(req); err != nil {
		return fmt.Errorf("notify human: %w", err)
	}

	// Wait for response with timeout
	select {
	case resp := <-respChan:
		return n.processResponse(state, resp)
	case <-time.After(n.timeout):
		return ErrHumanTimeout
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errChan:
		return err
	}
}

// checkpointAndReturn: For long waits (≥ 5 minutes)
func (n *HumanNode) checkpointAndReturn(ctx context.Context, state graph.State, req *HumanRequest) error {
	// Store request ID in state for checkpoint creation
	state.Set(StateKeyHumanRequestID, req.RequestID)
	state.Set(StateKeyHumanTimeout, n.timeout.String())

	// Return special error to trigger checkpoint
	// Orchestrator will handle checkpoint creation with proper transaction boundary
	return ErrHumanInteractionPending
}

// buildCallbackURL: Generate HMAC-signed callback URL
func (n *HumanNode) buildCallbackURL(requestID string) string {
	timestamp := time.Now().Unix()
	payload := fmt.Sprintf("%s:%d", requestID, timestamp)
	signature := n.signHMAC(payload)

	// Use a configurable base URL instead of empty string placeholder
	// In a real implementation, this should come from configuration
	baseURL := getBaseURL() // Function to retrieve base URL from config
	return fmt.Sprintf("%s/api/human-callback?req=%s&ts=%d&sig=%s",
		baseURL, requestID, timestamp, signature)
}

// getBaseURL retrieves the base URL from configuration
// This is a placeholder - in a real implementation, this would read from a config file or environment variable
func getBaseURL() string {
	// In production, retrieve from environment variable or config file
	baseURL := os.Getenv("BASE_CALLBACK_URL")
	if baseURL == "" {
		// Default to localhost for development, but this should be configured properly in production
		baseURL = "http://localhost:8080"
	}
	return baseURL
}

func (n *HumanNode) signHMAC(payload string) string {
	h := hmac.New(sha256.New, n.callbackSecret)
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}

func (n *HumanNode) processResponse(state graph.State, resp *HumanResponse) error {
	// Store response in state for downstream nodes
	state.Set("human_response", resp.SelectedOption)
	state.Set("human_response_text", resp.FreeformText)
	state.Set("human_response_timestamp", resp.Timestamp)
	return nil
}

// Helper function to generate a unique request ID
func generateRequestID() string {
	return fmt.Sprintf("hitl-%d", time.Now().UnixNano())
}

// HumanRequest represents a request sent to a human
type HumanRequest struct {
	RequestID   string                 `json:"request_id"`
	AgentName   string                 `json:"agent_name"`
	Instruction string                 `json:"instruction"`
	Options     []string               `json:"options,omitempty"`
	CallbackURL string                 `json:"callback_url,omitempty"`
	Timeout     time.Duration          `json:"timeout"`
	Timestamp   time.Time              `json:"timestamp"`
	Context     map[string]interface{} `json:"context,omitempty"`
}

// HumanResponseManager manages human responses (simplified interface)
type HumanResponseManager struct {
	callbacks map[string]func(*HumanResponse)
	mutex     sync.RWMutex // Protect access to callbacks map
}

// NewHumanResponseManager creates a new response manager
func NewHumanResponseManager() *HumanResponseManager {
	return &HumanResponseManager{
		callbacks: make(map[string]func(*HumanResponse)),
		mutex:     sync.RWMutex{},
	}
}

// RegisterCallback registers a callback for a request ID
func (m *HumanResponseManager) RegisterCallback(requestID string, callback func(*HumanResponse)) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.callbacks[requestID] = callback
}

// Unregister removes a callback
func (m *HumanResponseManager) Unregister(requestID string) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	delete(m.callbacks, requestID)
}

// NotifyHuman sends the request to the human
func (m *HumanResponseManager) NotifyHuman(req *HumanRequest) error {
	// This would typically send the request to a UI, email, or other notification system
	// For now, this is a placeholder
	return nil
}

// DeliverResponse delivers a response to the appropriate callback
func (m *HumanResponseManager) DeliverResponse(requestID string, response *HumanResponse) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if callback, exists := m.callbacks[requestID]; exists {
		callback(response)
		// Clean up the callback after use
		delete(m.callbacks, requestID)
	}
}
