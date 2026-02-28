package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client provides typed helpers for calling agent server endpoints.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new test client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// HealthResponse represents the /health endpoint response.
type HealthResponse struct {
	Status string `json:"status"`
}

// Agent represents an agent in the list response.
type Agent struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
}

// AgentsResponse represents the /api/v1/agents endpoint response.
type AgentsResponse struct {
	Agents []Agent `json:"agents"`
}

// ExecuteRequest represents a request to /api/v1/agents/execute.
type ExecuteRequest struct {
	AgentID     string                 `json:"agent_id"`
	Instruction string                 `json:"-"` // Mapped to Input for JSON
	Input       string                 `json:"input,omitempty"`
	Context     map[string]interface{} `json:"context,omitempty"`
}

// ToServerRequest converts the test request to server format.
func (r *ExecuteRequest) ToServerRequest() map[string]interface{} {
	input := r.Input
	if input == "" && r.Instruction != "" {
		input = r.Instruction
	}
	return map[string]interface{}{
		"agent_id": r.AgentID,
		"input":    input,
		"context":  r.Context,
	}
}

// ExecuteResponse represents the response from /api/v1/agents/execute.
type ExecuteResponse struct {
	Output     string                 `json:"output"`
	Metadata   map[string]interface{} `json:"metadata"`
	Success    bool                   `json:"success"`
	DurationMS int64                  `json:"duration_ms"`
	Error      string                 `json:"error,omitempty"`
}

// BatchExecuteRequest represents a request to /api/v1/agents/batch_execute.
// Note: The server expects a single agent_id with multiple inputs.
type BatchExecuteRequest struct {
	AgentID  string                 `json:"agent_id"`
	Inputs   []string               `json:"inputs"`
	Context  map[string]string      `json:"context,omitempty"`
	Requests []ExecuteRequest       `json:"-"` // For test convenience, converted to Inputs
}

// ToServerRequest converts to server format.
func (r *BatchExecuteRequest) ToServerRequest() map[string]interface{} {
	// If Requests is provided, extract inputs (assumes same agent for batch)
	if len(r.Requests) > 0 {
		inputs := make([]string, len(r.Requests))
		agentID := r.Requests[0].AgentID
		for i, req := range r.Requests {
			if req.Input != "" {
				inputs[i] = req.Input
			} else {
				inputs[i] = req.Instruction
			}
		}
		return map[string]interface{}{
			"agent_id": agentID,
			"inputs":   inputs,
			"context":  r.Context,
		}
	}
	return map[string]interface{}{
		"agent_id": r.AgentID,
		"inputs":   r.Inputs,
		"context":  r.Context,
	}
}

// BatchExecuteResponse represents the response from /api/v1/agents/batch_execute.
type BatchExecuteResponse struct {
	Results []ExecuteResponse `json:"results"`
}

// ErrorResponse represents an error response from the server.
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// Health calls GET /health and returns the response.
func (c *Client) Health() (*HealthResponse, int, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/health")
	if err != nil {
		return nil, 0, fmt.Errorf("health request failed: %w", err)
	}
	defer resp.Body.Close()

	var result HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, resp.StatusCode, nil
}

// ListAgents calls GET /api/v1/agents and returns the list of agents.
func (c *Client) ListAgents() (*AgentsResponse, int, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/api/v1/agents")
	if err != nil {
		return nil, 0, fmt.Errorf("list agents request failed: %w", err)
	}
	defer resp.Body.Close()

	var result AgentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, resp.StatusCode, nil
}

// Execute calls POST /api/v1/agents/execute with the given request.
func (c *Client) Execute(req *ExecuteRequest) (*ExecuteResponse, int, error) {
	body, err := json.Marshal(req.ToServerRequest())
	if err != nil {
		return nil, 0, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := c.httpClient.Post(
		c.baseURL+"/api/v1/agents/execute",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, 0, fmt.Errorf("execute request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response: %w", err)
	}

	var result ExecuteResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		// Try to parse as error response
		var errResp ErrorResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != "" {
			return &ExecuteResponse{Error: errResp.Error}, resp.StatusCode, nil
		}
		return nil, resp.StatusCode, fmt.Errorf("failed to decode response: %w (body: %s)", err, string(respBody))
	}

	return &result, resp.StatusCode, nil
}

// BatchExecute calls POST /api/v1/agents/batch_execute with the given requests.
func (c *Client) BatchExecute(req *BatchExecuteRequest) (*BatchExecuteResponse, int, error) {
	body, err := json.Marshal(req.ToServerRequest())
	if err != nil {
		return nil, 0, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := c.httpClient.Post(
		c.baseURL+"/api/v1/agents/batch_execute",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, 0, fmt.Errorf("batch execute request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read body first to handle non-JSON error responses
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response: %w", err)
	}

	// For error status codes, the server may return plain text
	if resp.StatusCode >= 400 {
		return &BatchExecuteResponse{}, resp.StatusCode, nil
	}

	var result BatchExecuteResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to decode response: %w (body: %s)", err, string(respBody))
	}

	return &result, resp.StatusCode, nil
}

// Stream calls POST /api/v1/agents/stream and returns the raw response.
// Note: Streaming is not fully implemented; this returns status only.
func (c *Client) Stream(req *ExecuteRequest) (int, string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return 0, "", fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := c.httpClient.Post(
		c.baseURL+"/api/v1/agents/stream",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		return 0, "", fmt.Errorf("stream request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", fmt.Errorf("failed to read response: %w", err)
	}

	return resp.StatusCode, string(respBody), nil
}
