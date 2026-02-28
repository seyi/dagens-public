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

package mcp

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// httpTransport implements Transport using HTTP communication
type httpTransport struct {
	client    *http.Client
	params    HTTPConnectionParams
	connected bool
	mu        sync.RWMutex
}

// NewHTTPTransport creates a new HTTP transport
func NewHTTPTransport(params HTTPConnectionParams) (Transport, error) {
	if err := params.Validate(); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	// Set default timeout
	if params.Timeout == 0 {
		params.Timeout = 300
	}

	// Create HTTP client
	transport := &http.Transport{
		MaxIdleConns:        10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	// Configure TLS if needed
	if params.TLSInsecure {
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   time.Duration(params.Timeout) * time.Second,
	}

	return &httpTransport{
		client:    client,
		params:    params,
		connected: true,
	}, nil
}

// Send sends a JSON-RPC request via HTTP POST
func (t *httpTransport) Send(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
	t.mu.RLock()
	if !t.connected {
		t.mu.RUnlock()
		return nil, fmt.Errorf("transport not connected")
	}
	t.mu.RUnlock()

	// Marshal request
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", t.params.URL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/json")

	// Add custom headers
	for key, value := range t.params.Headers {
		httpReq.Header.Set(key, value)
	}

	// Add session ID if provided
	if t.params.SessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", t.params.SessionID)
	}

	// CORS headers if enabled
	if t.params.EnableCORS {
		httpReq.Header.Set("Access-Control-Allow-Origin", "*")
		httpReq.Header.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		httpReq.Header.Set("Access-Control-Allow-Headers", "Content-Type, Mcp-Session-Id")
	}

	// Send request
	httpResp, err := t.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer httpResp.Body.Close()

	// Check status code
	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", httpResp.StatusCode, string(body))
	}

	// Read response body
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse JSON-RPC response
	var resp JSONRPCResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &resp, nil
}

// Close closes the HTTP transport
func (t *httpTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.connected = false
	t.client.CloseIdleConnections()
	return nil
}

// IsConnected returns true if the transport is connected
func (t *httpTransport) IsConnected() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.connected
}
