// Package ipc provides interprocess communication mechanisms for agents
// using stdin/stdout as the communication channel, similar to Erlang's
// message passing model.
package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/graph"
)

// Message represents a message in the IPC system
type Message struct {
	ID       string                 `json:"id"`
	From     string                 `json:"from"`
	To       string                 `json:"to"`
	Type     string                 `json:"type"`
	Payload  map[string]interface{} `json:"payload"`
	SentAt   time.Time              `json:"sent_at"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// Message types
const (
	MessageTypeRequest  = "request"
	MessageTypeResponse = "response"
	MessageTypeError    = "error"
	MessageTypePing     = "ping"
	MessageTypePong     = "pong"
)

// ProcessAgent represents an agent running in a separate process
type ProcessAgent struct {
	ID          string
	Command     string
	Args        []string
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      io.Reader
	stderr      io.Reader
	messages    chan Message
	stop        chan struct{}
	wg          sync.WaitGroup
	messageID   int
	messageIDMu sync.Mutex
}

// NewProcessAgent creates a new agent running in a separate process
func NewProcessAgent(id, command string, args ...string) *ProcessAgent {
	return &ProcessAgent{
		ID:       id,
		Command:  command,
		Args:     args,
		messages: make(chan Message, 100), // Buffered channel for messages
		stop:     make(chan struct{}),
	}
}

// Start starts the process agent
func (pa *ProcessAgent) Start() error {
	pa.cmd = exec.Command(pa.Command, pa.Args...)

	stdin, err := pa.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	pa.stdin = stdin

	stdout, err := pa.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	pa.stdout = stdout

	stderr, err := pa.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}
	pa.stderr = stderr

	if err := pa.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start process: %w", err)
	}

	// Start message readers
	pa.wg.Add(2)
	go pa.readStdout()
	go pa.readStderr()

	return nil
}

// Stop stops the process agent
func (pa *ProcessAgent) Stop() error {
	close(pa.stop)
	
	if pa.stdin != nil {
		pa.stdin.Close()
	}
	
	if pa.cmd != nil && pa.cmd.Process != nil {
		pa.cmd.Process.Kill()
	}
	
	pa.wg.Wait()
	
	if pa.cmd != nil {
		return pa.cmd.Wait()
	}
	
	return nil
}

// readStdout reads messages from the process's stdout
func (pa *ProcessAgent) readStdout() {
	defer pa.wg.Done()
	
	scanner := bufio.NewScanner(pa.stdout)
	for scanner.Scan() {
		select {
		case <-pa.stop:
			return
		default:
		}
		
		line := scanner.Text()
		
		// Skip empty lines
		if strings.TrimSpace(line) == "" {
			continue
		}
		
		var msg Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			// Not a JSON message, could be regular output
			continue
		}
		
		// Send message to the channel
		select {
		case pa.messages <- msg:
		case <-pa.stop:
			return
		}
	}
}

// readStderr reads error output from the process's stderr
func (pa *ProcessAgent) readStderr() {
	defer pa.wg.Done()
	
	scanner := bufio.NewScanner(pa.stderr)
	for scanner.Scan() {
		select {
		case <-pa.stop:
			return
		default:
		}
		
		line := scanner.Text()
		fmt.Fprintf(os.Stderr, "Process %s stderr: %s\n", pa.ID, line)
	}
}

// SendMessage sends a message to the process agent
func (pa *ProcessAgent) SendMessage(msg Message) error {
	// Ensure message has required fields
	if msg.ID == "" {
		msg.ID = pa.generateMessageID()
	}
	msg.From = pa.ID
	msg.SentAt = time.Now()
	
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}
	
	// Add newline to separate messages
	data = append(data, '\n')
	
	_, err = pa.stdin.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write message to stdin: %w", err)
	}
	
	return nil
}

// generateMessageID generates a unique message ID
func (pa *ProcessAgent) generateMessageID() string {
	pa.messageIDMu.Lock()
	defer pa.messageIDMu.Unlock()
	
	pa.messageID++
	return fmt.Sprintf("%s-%d", pa.ID, pa.messageID)
}

// ReceiveMessage receives a message from the process agent
func (pa *ProcessAgent) ReceiveMessage(timeout time.Duration) (Message, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	
	select {
	case msg := <-pa.messages:
		return msg, nil
	case <-ctx.Done():
		return Message{}, fmt.Errorf("timeout waiting for message")
	}
}

// SendRequest sends a request and waits for a response
func (pa *ProcessAgent) SendRequest(payload map[string]interface{}) (Message, error) {
	// Create request message
	req := Message{
		Type:    MessageTypeRequest,
		To:      pa.ID, // This would be the target agent in a multi-agent setup
		Payload: payload,
	}
	
	// Send the request
	if err := pa.SendMessage(req); err != nil {
		return Message{}, fmt.Errorf("failed to send request: %w", err)
	}
	
	// Wait for response
	resp, err := pa.ReceiveMessage(30 * time.Second) // 30 second timeout
	if err != nil {
		return Message{}, fmt.Errorf("failed to receive response: %w", err)
	}
	
	if resp.Type == MessageTypeError {
		return Message{}, fmt.Errorf("received error response: %v", resp.Payload)
	}
	
	return resp, nil
}

// StdioAgentNode represents a node that communicates with an agent via stdin/stdout
type StdioAgentNode struct {
	*graph.BaseNode
	agent *ProcessAgent
}

// StdioAgentNodeConfig holds configuration for creating a StdioAgentNode
type StdioAgentNodeConfig struct {
	ID     string
	Agent  *ProcessAgent
}

// NewStdioAgentNode creates a new node that communicates with an agent via stdin/stdout
func NewStdioAgentNode(config StdioAgentNodeConfig) *StdioAgentNode {
	return &StdioAgentNode{
		BaseNode: graph.NewBaseNode(config.ID, "stdio_agent"),
		agent:    config.Agent,
	}
}

// Execute sends the current state to the agent process and updates the state with the response
func (n *StdioAgentNode) Execute(ctx context.Context, state graph.State) error {
	// Convert state to payload
	payload := make(map[string]interface{})
	
	// Add all state values to the payload
	for _, key := range state.Keys() {
		if val, exists := state.Get(key); exists {
			payload[key] = val
		}
	}
	
	// Add metadata about the execution
	payload["node_id"] = n.ID()
	payload["node_type"] = n.Type()
	
	// Send request to agent process
	resp, err := n.agent.SendRequest(payload)
	if err != nil {
		return fmt.Errorf("failed to communicate with agent process: %w", err)
	}
	
	// Update state with response payload
	for key, value := range resp.Payload {
		// Skip internal fields
		if key != "node_id" && key != "node_type" && key != "id" && key != "from" && key != "to" && key != "type" && key != "sent_at" {
			state.Set(key, value)
		}
	}
	
	return nil
}

// StdioAgentServer provides a server that listens on stdin/stdout for agent requests
type StdioAgentServer struct {
	agents map[string]agent.Agent
}

// NewStdioAgentServer creates a new stdin/stdout agent server
func NewStdioAgentServer() *StdioAgentServer {
	return &StdioAgentServer{
		agents: make(map[string]agent.Agent),
	}
}

// RegisterAgent registers an agent with the server
func (s *StdioAgentServer) RegisterAgent(id string, agent agent.Agent) {
	s.agents[id] = agent
}

// Start starts the server, listening on stdin/stdout
func (s *StdioAgentServer) Start() error {
	scanner := bufio.NewScanner(os.Stdin)

	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines
		if strings.TrimSpace(line) == "" {
			continue
		}

		// Parse the message
		var msg Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			// Send error response
			errorMsg := Message{
				ID:      "error",
				Type:    MessageTypeError,
				Payload: map[string]interface{}{"error": err.Error()},
			}
			s.sendMessage(errorMsg)
			continue
		}

		// Process the message
		response := s.processMessage(msg)

		// Send response
		s.sendMessage(response)
	}

	return scanner.Err()
}

// processMessage processes an incoming message and returns a response
func (s *StdioAgentServer) processMessage(msg Message) Message {
	switch msg.Type {
	case MessageTypeRequest:
		return s.handleRequest(msg)
	case MessageTypePing:
		return Message{
			ID:      msg.ID,
			From:    "server",
			To:      msg.From,
			Type:    MessageTypePong,
			Payload: map[string]interface{}{"timestamp": time.Now().Unix()},
			SentAt:  time.Now(),
		}
	default:
		return Message{
			ID:      msg.ID,
			From:    "server",
			To:      msg.From,
			Type:    MessageTypeError,
			Payload: map[string]interface{}{"error": fmt.Sprintf("unknown message type: %s", msg.Type)},
			SentAt:  time.Now(),
		}
	}
}

// handleRequest handles a request message
func (s *StdioAgentServer) handleRequest(msg Message) Message {
	// Extract agent ID from the request
	agentID, ok := msg.Payload["agent_id"].(string)
	if !ok {
		// Use the "to" field as the agent ID if not specified in payload
		agentID = msg.To
	}

	// Get the agent
	agentInst, exists := s.agents[agentID]
	if !exists {
		return Message{
			ID:      msg.ID,
			From:    "server",
			To:      msg.From,
			Type:    MessageTypeError,
			Payload: map[string]interface{}{"error": fmt.Sprintf("agent not found: %s", agentID)},
			SentAt:  time.Now(),
		}
	}

	// Create agent input from message payload
	input := &agent.AgentInput{
		Instruction: "",
		Context:     msg.Payload,
	}

	// Extract instruction if present in a specific field
	if instruction, ok := msg.Payload["instruction"].(string); ok {
		input.Instruction = instruction
	} else if prompt, ok := msg.Payload["prompt"].(string); ok {
		input.Instruction = prompt
	} else if query, ok := msg.Payload["query"].(string); ok {
		input.Instruction = query
	}

	// Execute the agent
	ctx := context.Background()
	output, err := agentInst.Execute(ctx, input)

	if err != nil {
		return Message{
			ID:      msg.ID,
			From:    "server",
			To:      msg.From,
			Type:    MessageTypeError,
			Payload: map[string]interface{}{"error": err.Error()},
			SentAt:  time.Now(),
		}
	}

	// Create response message
	responsePayload := make(map[string]interface{})

	// Add result to payload
	if output.Result != nil {
		responsePayload["result"] = output.Result
	}

	// Add metadata to payload
	if output.Metadata != nil {
		for k, v := range output.Metadata {
			responsePayload[k] = v
		}
	}

	// Add original request fields to help with correlation
	responsePayload["request_id"] = msg.ID
	responsePayload["agent_id"] = agentID

	return Message{
		ID:      msg.ID,
		From:    "server",
		To:      msg.From,
		Type:    MessageTypeResponse,
		Payload: responsePayload,
		SentAt:  time.Now(),
	}
}

// sendMessage sends a message to stdout
func (s *StdioAgentServer) sendMessage(msg Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		// Can't marshal the message, send an error instead
		errorMsg := Message{
			ID:      "marshal-error",
			Type:    MessageTypeError,
			Payload: map[string]interface{}{"error": err.Error()},
		}
		data, _ = json.Marshal(errorMsg) // This should not fail
	}

	// Add newline to separate messages
	data = append(data, '\n')

	os.Stdout.Write(data)
	os.Stdout.Sync() // Ensure the data is written immediately
}