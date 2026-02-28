# Stdio HITL (Human-in-the-Loop) Demo

This example demonstrates Human-in-the-Loop functionality using stdin/stdout for interaction with the dagens framework.

## Overview

This demo shows how to:
1. Create nodes that read from stdin and write to stdout
2. Integrate human feedback into graph execution
3. Use conditional logic based on human input
4. Display state information to users
5. Implement durable checkpointing that survives process restart
6. Provide secure out-of-band resume functionality

## Architecture

The examples create workflows with different checkpointing strategies:

### Basic Demo (`main.go`)
- `start` node: Initializes the workflow state
- `process` node: Performs initial processing
- `display_state` node: Outputs current state to stdout
- `get_input` node: Reads human input from stdin
- `validate_input` node: Processes the human feedback
- `decision` node: Makes conditional decisions based on input
- `final_display` node: Shows final results

### Durable Demo (`durable_demo.go`)
- Uses the HITL package for true durable checkpointing
- Survives process restarts
- Includes signal handling and goroutine leak prevention
- Implements proper resume functionality

### Full Lifecycle Demo (`full_lifecycle_demo.go`)
- Demonstrates complete checkpoint/resume lifecycle
- Shows workflow pause and resume with security validation
- Simulates process restart scenario

### Resume Command (`cmd/resume.go`)
- Standalone command-line tool to resume workflows
- Uses HMAC signatures for security
- Can be run independently to continue paused workflows

## Running the Demos

```bash
cd examples/stdio_hitl

# Basic demo (direct input)
go run main.go

# Advanced demo with durable checkpointing
go run durable_demo.go

# Full lifecycle demo showing complete workflow
go run full_lifecycle_demo.go

# Or use the Makefile:
make demo             # runs basic demo
make durable-demo     # runs durable demo
make lifecycle-demo   # runs full lifecycle demo

# To build the resume command:
cd cmd
go build resume.go
# Then use: ./resume -request-id <id> -response <response> -secret <secret>
```

## Key Features Demonstrated

- **StdinNode**: Reads user input and stores it in the graph state
- **StdoutNode**: Outputs state data to standard output with templating support
- **StdioCheckpointNode**: Handles stdin with durable checkpointing capability
- **Conditional Logic**: Changes workflow path based on human input
- **State Management**: Maintains and updates state throughout execution
- **Signal Handling**: Clean interruption of stdin reads (Ctrl+C)
- **Goroutine Leak Prevention**: Proper cleanup of scanner goroutines
- **Durable Checkpointing**: Survives process restarts with HITL integration
- **Proper Resume**: State restoration after checkpoint resume
- **Security**: HMAC-based request validation for resume operations
- **ResponseManager Integration**: Proper integration with HITL notification system

## How It Works

### Basic Demo
1. The workflow starts and processes initial data
2. Current state is displayed to the user via stdout
3. The workflow waits for user input via stdin
4. Based on the input, the workflow takes different paths
5. Final results are displayed to the user

### Durable Demo
1. The workflow starts and processes initial data
2. For long waits (>5 seconds), creates durable checkpoint
3. State is serialized and stored durably
4. Instructions are provided to resume via out-of-band mechanism
5. Workflow can be resumed using the standalone resume command

### Full Lifecycle Demo
1. Simulates the complete workflow: start → process → checkpoint
2. Demonstrates how checkpoint is created and stored
3. Shows how to resume with proper security validation
4. Validates the complete end-to-end process

## Resume Command Usage

After a workflow is paused, users can resume it using the standalone command:

```bash
# Calculate signature and resume
./cmd/resume -request-id "request123" -response "continue" -secret "demo-secret-key"

# Or provide pre-calculated signature
./cmd/resume -request-id "request123" -response "continue" -signature "hmac-signature"
```

## Input Options

- Type "continue" or "yes" to approve the processing
- Type anything else to proceed with "reviewed" status
- Use Ctrl+C to cleanly interrupt the process

## Advanced Features

The implementation includes several important fixes and improvements:
1. **No Goroutine Leaks**: Proper cleanup of scanner goroutines using done channels
2. **Signal Handling**: Clean interruption with OS signal support
3. **Durable Checkpointing**: Integration with HITL system for process restart survival
4. **Proper Resume**: State restoration after checkpoint resume
5. **Security**: HMAC-based request validation to prevent unauthorized resume
6. **Out-of-Band Resumption**: Decoupled resume mechanism that works after process restart
7. **ResponseManager Integration**: Proper use of HITL notification system
8. **Race Condition Handling**: Improved signal/input handling to prevent race conditions
9. **State Serialization**: Generic approach that works with any State implementation