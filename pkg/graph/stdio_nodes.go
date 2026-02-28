package graph

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/template"
	"time"

	"golang.org/x/term"
)

// StdinNode reads from standard input and updates the state with the input.
type StdinNode struct {
	*BaseNode
	prompt      string          // Prompt to display before reading input
	stateKey    string          // Key in state where input will be stored
	trimSpace   bool            // Whether to trim whitespace from input
	defaultValue interface{}    // Default value if no input is provided
}

// StdinNodeConfig holds configuration for creating a StdinNode
type StdinNodeConfig struct {
	ID         string
	Prompt     string
	StateKey   string
	TrimSpace  bool
	DefaultValue interface{}
}

// NewStdinNode creates a new StdinNode
func NewStdinNode(config StdinNodeConfig) *StdinNode {
	if config.StateKey == "" {
		config.StateKey = "stdin_input"
	}
	
	node := &StdinNode{
		BaseNode:   NewBaseNode(config.ID, "stdin"),
		prompt:     config.Prompt,
		stateKey:   config.StateKey,
		trimSpace:  config.TrimSpace,
		defaultValue: config.DefaultValue,
	}
	
	return node
}

// Execute reads from stdin and stores the input in the state
func (n *StdinNode) Execute(ctx context.Context, state State) error {
	// Check if there's a default value and we're not in an interactive context
	if n.defaultValue != nil && !isInteractiveContext(ctx) {
		state.Set(n.stateKey, n.defaultValue)
		return nil
	}

	// Display prompt if provided
	if n.prompt != "" {
		fmt.Print(n.prompt)
	}

	// Read from stdin
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		input := scanner.Text()

		// Process input based on configuration
		if n.trimSpace {
			input = strings.TrimSpace(input)
		}

		// Use default value if input is empty and default is provided
		if input == "" && n.defaultValue != nil {
			// Convert default value to string if needed
			switch v := n.defaultValue.(type) {
			case string:
				state.Set(n.stateKey, v)
			default:
				state.Set(n.stateKey, fmt.Sprintf("%v", v))
			}
		} else {
			state.Set(n.stateKey, input)
		}
	} else {
		// Handle error or empty input
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("error reading from stdin: %w", err)
		}
		// If no input was provided but we have a default, use it
		if n.defaultValue != nil {
			state.Set(n.stateKey, n.defaultValue)
		} else {
			// Set to empty string if no default
			state.Set(n.stateKey, "")
		}
	}

	return nil
}

// isInteractiveContext checks if the context allows for interactive input
// and if stdin is connected to a terminal (TTY)
func isInteractiveContext(ctx context.Context) bool {
	// First check if context is done
	select {
	case <-ctx.Done():
		return false
	default:
	}
	// Then check if stdin is connected to a terminal
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// StdoutNode writes data from state to standard output.
type StdoutNode struct {
	*BaseNode
	template   string    // Template for output (supports key substitution)
	stateKey   string    // Key in state to output (if template not used)
	prefix     string    // Prefix to add to output
	suffix     string    // Suffix to add to output
	newline    bool      // Whether to add a newline after output
}

// StdoutNodeConfig holds configuration for creating a StdoutNode
type StdoutNodeConfig struct {
	ID       string
	Template string  // Template like "Result: {{.result_key}}"
	StateKey string  // Specific key to output
	Prefix   string
	Suffix   string
	Newline  bool
}

// NewStdoutNode creates a new StdoutNode
func NewStdoutNode(config StdoutNodeConfig) *StdoutNode {
	if config.StateKey == "" && config.Template == "" {
		config.StateKey = "output" // Default key if none provided
	}
	
	node := &StdoutNode{
		BaseNode: NewBaseNode(config.ID, "stdout"),
		template: config.Template,
		stateKey: config.StateKey,
		prefix:   config.Prefix,
		suffix:   config.Suffix,
		newline:  config.Newline,
	}
	
	return node
}

// Execute writes data from state to stdout
func (n *StdoutNode) Execute(ctx context.Context, state State) error {
	var output string
	
	if n.template != "" {
		// Use template to format output
		output = n.formatTemplate(n.template, state)
	} else if n.stateKey != "" {
		// Use specific state key
		if val, exists := state.Get(n.stateKey); exists {
			output = fmt.Sprintf("%v", val)
		} else {
			output = "" // or return an error if key doesn't exist
		}
	} else {
		// If no template or key specified, output all keys
		keys := state.Keys()
		for i, key := range keys {
			if val, exists := state.Get(key); exists {
				if i > 0 {
					output += "\n"
				}
				output += fmt.Sprintf("%s: %v", key, val)
			}
		}
	}
	
	// Apply prefix and suffix
	if n.prefix != "" {
		output = n.prefix + output
	}
	if n.suffix != "" {
		output = output + n.suffix
	}
	if n.newline {
		output += "\n"
	}
	
	// Write to stdout
	fmt.Print(output)
	
	return nil
}

// formatTemplate formats output using Go's text/template with state values
func (n *StdoutNode) formatTemplate(templateStr string, state State) string {
	// Create a template from the string
	tmpl, err := template.New("output").Parse(templateStr)
	if err != nil {
		// Fallback to simple string replacement if template parsing fails
		return n.simpleFormatTemplate(templateStr, state)
	}

	// Convert state to map for template execution
	stateMap := make(map[string]interface{})
	keys := state.Keys()
	for _, key := range keys {
		if val, exists := state.Get(key); exists {
			stateMap[key] = val
		}
	}

	// Execute template
	var buf strings.Builder
	if err := tmpl.Execute(&buf, stateMap); err != nil {
		// Fallback to simple string replacement if template execution fails
		return n.simpleFormatTemplate(templateStr, state)
	}

	return buf.String()
}

// simpleFormatTemplate provides backward compatibility with simple string replacement
func (n *StdoutNode) simpleFormatTemplate(templateStr string, state State) string {
	result := templateStr
	keys := state.Keys()
	for _, key := range keys {
		if val, exists := state.Get(key); exists {
			placeholder := fmt.Sprintf("{{.%s}}", key)
			result = strings.ReplaceAll(result, placeholder, fmt.Sprintf("%v", val))
		}
	}
	return result
}

// StdioCheckpointNode creates a checkpoint that can be resumed with stdin input.
// This combines checkpoint functionality with stdin/stdout interaction.
type StdioCheckpointNode struct {
	*BaseNode
	prompt      string
	stateKey    string
	continueKey string // State key that determines if execution should continue
	timeout     int    // Timeout in seconds (0 = no timeout)
	// Note: For true durable checkpointing that survives process restart,
	// this node would need to integrate with the HITL package checkpoint system
}

// StdioCheckpointNodeConfig holds configuration for creating a StdioCheckpointNode
type StdioCheckpointNodeConfig struct {
	ID          string
	Prompt      string
	StateKey    string
	ContinueKey string
	Timeout     int // Timeout in seconds (0 = no timeout)
}

// NewStdioCheckpointNode creates a new StdioCheckpointNode
func NewStdioCheckpointNode(config StdioCheckpointNodeConfig) *StdioCheckpointNode {
	if config.StateKey == "" {
		config.StateKey = "checkpoint_input"
	}
	if config.ContinueKey == "" {
		config.ContinueKey = "checkpoint_continue"
	}

	node := &StdioCheckpointNode{
		BaseNode:    NewBaseNode(config.ID, "stdio_checkpoint"),
		prompt:      config.Prompt,
		stateKey:    config.StateKey,
		continueKey: config.ContinueKey,
		timeout:     config.Timeout,
	}

	return node
}

// Execute handles checkpointing with stdin/stdout interaction
func (n *StdioCheckpointNode) Execute(ctx context.Context, state State) error {
	// Check if we're resuming from a previous checkpoint
	isResume, exists := state.Get("is_resume_checkpoint")
	if exists && isResume.(bool) {
		// This is a resumed execution - just continue (resume handling is for durable checkpoints)
		log.Println("Resuming from checkpoint")
		// For the graph-level checkpoint node, we just continue execution
		// Actual resume logic is handled by the HITL integration layer
		return nil
	}

	// Create a context with timeout
	var readCtx context.Context
	var cancel context.CancelFunc

	if n.timeout > 0 {
		readCtx, cancel = context.WithTimeout(ctx, time.Duration(n.timeout)*time.Second)
	} else {
		readCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	// Use the shared utility function for reading from stdin
	input, err := readFromStdinWithSignalHandling(ctx, n.prompt, readCtx)
	if err != nil {
		return err
	}

	processedInput := strings.TrimSpace(input)

	// Store input in state
	state.Set(n.stateKey, processedInput)

	// Determine if execution should continue based on input
	shouldContinue := strings.ToLower(processedInput) == "continue" ||
	                  strings.ToLower(processedInput) == "yes" ||
	                  strings.ToLower(processedInput) == "y"

	state.Set(n.continueKey, shouldContinue)

	return nil
}

// StdinReadResult represents the result of reading from stdin
type StdinReadResult struct {
	Input string
	Error error
}

// readFromStdinWithSignalHandling reads from stdin with proper signal handling
// On Linux, this uses non-blocking I/O to completely eliminate goroutine leaks.
// On other platforms, it falls back to a goroutine-based approach.
func readFromStdinWithSignalHandling(ctx context.Context, prompt string, timeoutCtx context.Context) (string, error) {
	// On Linux, use non-blocking I/O to avoid goroutine leaks entirely
	if useNonBlockingStdin() {
		// Calculate timeout duration from context
		var timeout time.Duration
		if deadline, ok := timeoutCtx.Deadline(); ok {
			timeout = time.Until(deadline)
			if timeout < 0 {
				timeout = 0
			}
		}
		return readFromStdinNonBlockingLinux(ctx, prompt, timeout)
	}

	// Fallback for non-Linux platforms
	return readFromStdinFallback(ctx, prompt, timeoutCtx)
}

// readFromStdinFallback is the fallback implementation for non-Linux platforms
// Note: This may have goroutine leak issues on timeout, but is the best we can do
// without platform-specific non-blocking I/O support.
func readFromStdinFallback(ctx context.Context, prompt string, timeoutCtx context.Context) (string, error) {
	// Display prompt
	if prompt != "" {
		fmt.Print(prompt)
	}

	// Use channels for communication
	resultChan := make(chan string, 1)
	errChan := make(chan error, 1)

	// Set up signal handling for clean interruption
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signalChan)

	// Use bufio.Reader instead of Scanner for better control
	reader := bufio.NewReader(os.Stdin)

	// Start the reader in a separate goroutine
	// Note: The goroutine may still block on ReadString if no input is provided,
	// but using channels allows the main goroutine to return on timeout/cancel
	go func() {
		line, err := reader.ReadString('\n')
		if err != nil {
			select {
			case errChan <- err:
			case <-timeoutCtx.Done():
			case <-ctx.Done():
			}
			return
		}
		// Trim the newline character
		line = strings.TrimRight(line, "\r\n")
		select {
		case resultChan <- line:
		case <-timeoutCtx.Done():
		case <-ctx.Done():
		}
	}()

	// Wait for input, timeout, signal, or context cancellation
	// Prioritize context cancellation and timeout over input
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timeoutCtx.Done():
		return "", fmt.Errorf("timeout waiting for stdin input: %w", timeoutCtx.Err())
	case input := <-resultChan:
		// Check if a signal was also received (race condition handling)
		select {
		case <-signalChan:
			return input, nil
		default:
			return input, nil
		}
	case err := <-errChan:
		// Handle EOF gracefully
		if err.Error() == "EOF" {
			return "", nil
		}
		return "", fmt.Errorf("error reading from stdin: %w", err)
	case <-signalChan:
		return "", fmt.Errorf("stdin read interrupted by signal")
	}
}

