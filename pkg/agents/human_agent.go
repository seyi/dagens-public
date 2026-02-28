package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/events"
)

// HumanAgent implements the Human-in-the-Loop pattern
// Allows pausing agent execution to request human input, approval, or decision-making
// Supports distributed execution with partition awareness and event tracking
type HumanAgent struct {
	*agent.BaseAgent
	responseManager *HumanResponseManager
	callbackURL     string
	timeout         time.Duration
	eventBus        events.EventBus
	requestFormat   RequestFormat
}

// HumanAgentConfig configures a human-in-the-loop agent
type HumanAgentConfig struct {
	Name             string
	Description      string
	CallbackURL      string            // URL for external callback system
	Timeout          time.Duration     // Max wait time for human response
	DefaultResponse  interface{}       // Fallback if timeout occurs
	EventBus         events.EventBus
	RequestFormat    RequestFormat     // How to format requests for humans
	Partition        string            // Spark partition assignment
	ResponseManager  *HumanResponseManager // Optional shared manager
}

// RequestFormat defines how human requests are formatted
type RequestFormat int

const (
	FormatJSON RequestFormat = iota
	FormatPlainText
	FormatHTML
	FormatMarkdown
)

// HumanRequest represents a request sent to a human
type HumanRequest struct {
	RequestID   string                 `json:"request_id"`
	AgentName   string                 `json:"agent_name"`
	Partition   string                 `json:"partition"`
	Instruction string                 `json:"instruction"`
	Context     map[string]interface{} `json:"context"`
	Options     []string               `json:"options,omitempty"`
	CallbackURL string                 `json:"callback_url,omitempty"`
	Timeout     time.Duration          `json:"timeout"`
	Timestamp   time.Time              `json:"timestamp"`
}

// HumanResponse represents a human's response to a request
type HumanResponse struct {
	RequestID string                 `json:"request_id"`
	Response  interface{}            `json:"response"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	Error     error                  `json:"error,omitempty"`
}

// HumanResponseManager manages pending human requests and responses
// Thread-safe for distributed environments
type HumanResponseManager struct {
	mu               sync.RWMutex
	pendingRequests  map[string]chan *HumanResponse
	requestMetadata  map[string]*HumanRequest
	callbackHandler  CallbackHandler
	externalNotifier ExternalNotifier
}

// CallbackHandler processes incoming human responses
type CallbackHandler func(response *HumanResponse) error

// ExternalNotifier sends requests to external systems (UI, approval systems, etc.)
type ExternalNotifier func(request *HumanRequest) error

// NewHumanResponseManager creates a new response manager
func NewHumanResponseManager() *HumanResponseManager {
	return &HumanResponseManager{
		pendingRequests: make(map[string]chan *HumanResponse),
		requestMetadata: make(map[string]*HumanRequest),
	}
}

// RegisterRequest registers a new pending request
func (m *HumanResponseManager) RegisterRequest(request *HumanRequest) chan *HumanResponse {
	m.mu.Lock()
	defer m.mu.Unlock()

	responseChan := make(chan *HumanResponse, 1)
	m.pendingRequests[request.RequestID] = responseChan
	m.requestMetadata[request.RequestID] = request

	return responseChan
}

// DeliverResponse delivers a response for a pending request
func (m *HumanResponseManager) DeliverResponse(response *HumanResponse) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	responseChan, exists := m.pendingRequests[response.RequestID]
	if !exists {
		return fmt.Errorf("no pending request with ID: %s", response.RequestID)
	}

	// Send response (non-blocking)
	select {
	case responseChan <- response:
		// Clean up
		delete(m.pendingRequests, response.RequestID)
		delete(m.requestMetadata, response.RequestID)
		return nil
	default:
		return fmt.Errorf("response channel full for request: %s", response.RequestID)
	}
}

// GetPendingRequest retrieves metadata for a pending request
func (m *HumanResponseManager) GetPendingRequest(requestID string) (*HumanRequest, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	request, exists := m.requestMetadata[requestID]
	return request, exists
}

// ListPendingRequests returns all pending request IDs
func (m *HumanResponseManager) ListPendingRequests() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	requests := make([]string, 0, len(m.pendingRequests))
	for requestID := range m.pendingRequests {
		requests = append(requests, requestID)
	}
	return requests
}

// SetCallbackHandler sets a handler for processing responses
func (m *HumanResponseManager) SetCallbackHandler(handler CallbackHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callbackHandler = handler
}

// SetExternalNotifier sets a notifier for sending requests to external systems
func (m *HumanResponseManager) SetExternalNotifier(notifier ExternalNotifier) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.externalNotifier = notifier
}

// NotifyExternal sends a request to the external system
func (m *HumanResponseManager) NotifyExternal(request *HumanRequest) error {
	m.mu.RLock()
	notifier := m.externalNotifier
	m.mu.RUnlock()

	if notifier != nil {
		return notifier(request)
	}
	return nil
}

// NewHumanAgent creates a new human-in-the-loop agent
func NewHumanAgent(config HumanAgentConfig) *HumanAgent {
	if config.Timeout == 0 {
		config.Timeout = 5 * time.Minute // Default 5 minute timeout
	}

	if config.ResponseManager == nil {
		config.ResponseManager = NewHumanResponseManager()
	}

	humanAgent := &HumanAgent{
		responseManager: config.ResponseManager,
		callbackURL:     config.CallbackURL,
		timeout:         config.Timeout,
		eventBus:        config.EventBus,
		requestFormat:   config.RequestFormat,
	}

	// Create executor
	executor := &humanExecutor{
		humanAgent:      humanAgent,
		defaultResponse: config.DefaultResponse,
	}

	// Create base agent
	baseAgent := agent.NewAgent(agent.AgentConfig{
		Name:        config.Name,
		Description: config.Description,
		Executor:    executor,
		Partition:   config.Partition,
	})

	humanAgent.BaseAgent = baseAgent
	return humanAgent
}

// humanExecutor implements AgentExecutor for human-in-the-loop execution
type humanExecutor struct {
	humanAgent      *HumanAgent
	defaultResponse interface{}
}

func (e *humanExecutor) Execute(ctx context.Context, ag agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
	startTime := time.Now()

	// Create human request
	request := &HumanRequest{
		RequestID:   generateRequestID(),
		AgentName:   e.humanAgent.Name(),
		Partition:   e.humanAgent.Partition(),
		Instruction: input.Instruction,
		Context:     input.Context,
		CallbackURL: e.humanAgent.callbackURL,
		Timeout:     e.humanAgent.timeout,
		Timestamp:   time.Now(),
	}

	// Extract options if provided
	if options, ok := input.Context["options"].([]string); ok {
		request.Options = options
	}

	// Publish request event
	if e.humanAgent.eventBus != nil {
		e.humanAgent.eventBus.Publish(&events.BaseEvent{
			EventType: events.EventType("human.request_sent"),
			EventTime: time.Now(),
			Agent:     request.AgentName,
			EventData: map[string]interface{}{
				"request_id": request.RequestID,
				"agent_name": request.AgentName,
				"partition":  request.Partition,
				"timeout":    request.Timeout.String(),
			},
		})
	}

	// Register request and get response channel
	responseChan := e.humanAgent.responseManager.RegisterRequest(request)

	// Notify external system
	if err := e.humanAgent.responseManager.NotifyExternal(request); err != nil {
		// Log error but continue (external notification is optional)
		if e.humanAgent.eventBus != nil {
			e.humanAgent.eventBus.Publish(&events.BaseEvent{
				EventType: events.EventType("human.notification_failed"),
				EventTime: time.Now(),
				Agent:     request.AgentName,
				EventData: map[string]interface{}{
					"request_id": request.RequestID,
					"error":      err.Error(),
				},
			})
		}
	}

	// Wait for response with timeout
	var response *HumanResponse
	select {
	case response = <-responseChan:
		// Human response received
		if response.Error != nil {
			return nil, fmt.Errorf("human response error: %w", response.Error)
		}

	case <-time.After(e.humanAgent.timeout):
		// Timeout occurred
		if e.humanAgent.eventBus != nil {
			e.humanAgent.eventBus.Publish(&events.BaseEvent{
				EventType: events.EventType("human.timeout"),
				EventTime: time.Now(),
				Agent:     request.AgentName,
				EventData: map[string]interface{}{
					"request_id": request.RequestID,
					"timeout":    e.humanAgent.timeout.String(),
				},
			})
		}

		if e.defaultResponse != nil {
			response = &HumanResponse{
				RequestID: request.RequestID,
				Response:  e.defaultResponse,
				Metadata: map[string]interface{}{
					"timeout": true,
					"reason":  "human response timeout",
				},
				Timestamp: time.Now(),
			}
		} else {
			return nil, fmt.Errorf("human response timeout after %v", e.humanAgent.timeout)
		}

	case <-ctx.Done():
		// Context cancelled
		return nil, ctx.Err()
	}

	// Publish response event
	if e.humanAgent.eventBus != nil {
		e.humanAgent.eventBus.Publish(&events.BaseEvent{
			EventType: events.EventType("human.response_received"),
			EventTime: time.Now(),
			Agent:     request.AgentName,
			EventData: map[string]interface{}{
				"request_id":   response.RequestID,
				"duration":     time.Since(startTime).String(),
				"has_metadata": len(response.Metadata) > 0,
			},
		})
	}

	return &agent.AgentOutput{
		Result: response.Response,
		Metadata: map[string]interface{}{
			"request_id":     request.RequestID,
			"human_metadata": response.Metadata,
			"execution_time": time.Since(startTime).Seconds(),
			"partition":      request.Partition,
			"timed_out":      response.Metadata["timeout"] == true,
		},
	}, nil
}

// RespondToRequest is a convenience method to deliver a response
func (h *HumanAgent) RespondToRequest(requestID string, response interface{}) error {
	humanResponse := &HumanResponse{
		RequestID: requestID,
		Response:  response,
		Timestamp: time.Now(),
	}
	return h.responseManager.DeliverResponse(humanResponse)
}

// GetResponseManager returns the response manager
func (h *HumanAgent) GetResponseManager() *HumanResponseManager {
	return h.responseManager
}

// FormatRequest formats a human request based on the configured format
func (h *HumanAgent) FormatRequest(request *HumanRequest) (string, error) {
	switch h.requestFormat {
	case FormatJSON:
		bytes, err := json.MarshalIndent(request, "", "  ")
		if err != nil {
			return "", err
		}
		return string(bytes), nil

	case FormatPlainText:
		text := fmt.Sprintf("Request: %s\n", request.Instruction)
		if len(request.Options) > 0 {
			text += "\nOptions:\n"
			for i, option := range request.Options {
				text += fmt.Sprintf("%d. %s\n", i+1, option)
			}
		}
		if len(request.Context) > 0 {
			text += fmt.Sprintf("\nContext: %v\n", request.Context)
		}
		text += fmt.Sprintf("\nRequest ID: %s\n", request.RequestID)
		text += fmt.Sprintf("Timeout: %v\n", request.Timeout)
		return text, nil

	case FormatMarkdown:
		md := fmt.Sprintf("## Human Input Required\n\n")
		md += fmt.Sprintf("**Request:** %s\n\n", request.Instruction)
		if len(request.Options) > 0 {
			md += "**Options:**\n"
			for i, option := range request.Options {
				md += fmt.Sprintf("%d. %s\n", i+1, option)
			}
			md += "\n"
		}
		if len(request.Context) > 0 {
			md += fmt.Sprintf("**Context:** `%v`\n\n", request.Context)
		}
		md += fmt.Sprintf("**Request ID:** `%s`\n", request.RequestID)
		md += fmt.Sprintf("**Timeout:** %v\n", request.Timeout)
		return md, nil

	case FormatHTML:
		html := fmt.Sprintf("<div class='human-request'>\n")
		html += fmt.Sprintf("<h2>Human Input Required</h2>\n")
		html += fmt.Sprintf("<p><strong>Request:</strong> %s</p>\n", request.Instruction)
		if len(request.Options) > 0 {
			html += "<p><strong>Options:</strong></p><ol>\n"
			for _, option := range request.Options {
				html += fmt.Sprintf("<li>%s</li>\n", option)
			}
			html += "</ol>\n"
		}
		html += fmt.Sprintf("<p><strong>Request ID:</strong> %s</p>\n", request.RequestID)
		html += fmt.Sprintf("<p><strong>Timeout:</strong> %v</p>\n", request.Timeout)
		html += "</div>\n"
		return html, nil

	default:
		return "", fmt.Errorf("unknown request format: %d", h.requestFormat)
	}
}

// generateRequestID generates a unique request ID
func generateRequestID() string {
	return fmt.Sprintf("human-req-%d", time.Now().UnixNano())
}

// CreateHumanApprovalTool creates a tool for requesting human approval
func CreateHumanApprovalTool(responseManager *HumanResponseManager, timeout time.Duration) *HumanApprovalTool {
	return &HumanApprovalTool{
		responseManager: responseManager,
		timeout:         timeout,
	}
}

// HumanApprovalTool wraps human approval as a callable tool
type HumanApprovalTool struct {
	responseManager *HumanResponseManager
	timeout         time.Duration
}

// Execute implements the tool handler for human approval
func (t *HumanApprovalTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	instruction, ok := params["instruction"].(string)
	if !ok {
		return nil, fmt.Errorf("missing instruction parameter")
	}

	request := &HumanRequest{
		RequestID:   generateRequestID(),
		AgentName:   "approval_tool",
		Instruction: instruction,
		Context:     params,
		Timeout:     t.timeout,
		Timestamp:   time.Now(),
	}

	// Extract options if provided
	if options, ok := params["options"].([]string); ok {
		request.Options = options
	}

	responseChan := t.responseManager.RegisterRequest(request)
	t.responseManager.NotifyExternal(request)

	// Wait for response
	select {
	case response := <-responseChan:
		if response.Error != nil {
			return nil, response.Error
		}
		return response.Response, nil
	case <-time.After(t.timeout):
		return nil, fmt.Errorf("approval timeout after %v", t.timeout)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
