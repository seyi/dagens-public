# Hello Dagens (Phase 2A)

A minimal "Hello World" example using the real OpenAI provider and Phase 2A state management.

## What This Example Demonstrates

- Creating an OpenAI provider using `pkg/models/openai`
- Basic state management with `MemoryState`
- Storing user input and assistant response in state
- Scoped key retrieval with `KeysWithPrefix`

## Prerequisites

- Go 1.22+
- An OpenAI API key

## Setup

```bash
export OPENAI_API_KEY=your-key
```

## Run

```bash
go run examples/hello_dagens/main.go
```

## Expected Output

```
╔════════════════════════════════════════════════════╗
║        HELLO DAGENS - Phase 2A Quick Start         ║
╚════════════════════════════════════════════════════╝

Provider: openai:gpt-4o-mini (context window: 128000 tokens)
Initial state version: 0

User: Say hello to Dagens in one short sentence.
Assistant: Hello Dagens, great to meet you!

Tokens used: 21
Final state version: 3

─────────────────────────────────────────────────────
State Management Demo
─────────────────────────────────────────────────────
All keys: [user.question assistant.answer assistant.tokens_total]
User scope keys: [user.question]
Assistant scope keys: [assistant.answer assistant.tokens_total]

✓ Hello Dagens complete!
```

## Time to Complete

This example should take **under 5 minutes** to set up and run.

## Troubleshooting

| Error | Solution |
|-------|----------|
| `OPENAI_API_KEY environment variable is required` | Run `export OPENAI_API_KEY=your-key` |
| Network timeout | Check internet connection; retry |
| Rate limit (429) | Wait a moment and retry |

## Next Steps

After completing this example, try:
- `examples/multi_agent_demo/` - Multi-agent workflow with checkpointing
- `examples/state_demo/` - Full Phase 2A state management features
- `examples/research_agent/` - Three-step research pipeline
