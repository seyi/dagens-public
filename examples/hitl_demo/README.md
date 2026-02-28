# HITL (Human-in-the-Loop) Demo

This example demonstrates the Human-in-the-Loop (HITL) functionality integrated with the graph abstraction in the Dagens framework.

## Overview

This demo shows how to:
1. Create a graph with nodes including a HumanNode
2. Execute the graph until it reaches a human interaction point
3. Pause execution and wait for human input using checkpointing
4. Resume execution after receiving human response

## Architecture

The HITL system implements a hybrid approach:
- **Short waits** (< 5 seconds): Synchronous blocking with timeout
- **Long waits** (≥ 5 seconds): Checkpoint persistence for resumption

## Components

- **HumanNode**: A special node type that requests human input
- **CheckpointStore**: Persists execution state when waiting for human input
- **CallbackHandler**: Processes human responses and resumes execution
- **ResumptionWorker**: Asynchronously resumes graph execution from checkpoints

## How It Works

1. The graph executes normally until it reaches the HumanNode
2. If the timeout is longer than the threshold, the HumanNode checkpoints the execution state
3. The execution state is saved and the workflow pauses
4. Human responses are received and processed
5. The system resumes execution from the checkpoint with the human response

## Running the Demo

```bash
cd examples/hitl_demo
go run main.go
```

## Key Features Demonstrated

- Graph-based workflow with human interaction points
- State persistence and resumption
- Error handling for human timeouts
- Idempotency for callback processing
- Security with HMAC-protected callbacks

## Implementation Details

The demo creates a simple workflow:
- `start` node: Sets initial data in the state
- `human_input` node: Requests human input and checkpoints if timeout is long
- `end` node: Processes the final result after human input

The HumanNode is configured with:
- A 30-second timeout (longer than 5-second threshold)
- A 5-second threshold to force checkpointing behavior
- Options for human to select from: "Approve", "Reject", "Request Changes"

After the checkpoint is created, the demo simulates a human response after 3 seconds and resumes the graph execution.

## Output

When you run the demo, you'll see output like:
```
2025/12/20 11:14:37 Starting HITL (Human-in-the-Loop) Demo
2025/12/20 11:14:37 Graph created with 3 nodes
2025/12/20 11:14:37 Executing start node...
2025/12/20 11:14:37 Executing node start with operation: start
2025/12/20 11:14:37 Set initial data in state
2025/12/20 11:14:37 Executing human node (this will trigger checkpointing)...
2025/12/20 11:14:37 Human node execution result: human interaction pending: checkpoint required
2025/12/20 11:14:37 Human interaction pending - checkpoint created
2025/12/20 11:14:40 Resuming graph with human response...
2025/12/20 11:14:40 Resuming execution from node: human_input
2025/12/20 11:14:40 Executing node end with operation: end
2025/12/20 11:14:40 Final data: initial_data
2025/12/20 11:14:40 Graph execution resumed successfully
```

This shows the complete HITL workflow: execution → checkpoint → human response → resume → completion.