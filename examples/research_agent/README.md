# Research Agent (Phase 2A)

A multi-step research workflow demonstrating Phase 2A state management with the real OpenAI provider.

## Architecture

```
User Topic
    │
    ▼
┌─────────────────┐
│   SEARCHER      │  → state["searcher.output"]
│ (provider.Chat) │  → checkpoint: after-searcher
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  SUMMARIZER     │  → state["summarizer.output"]
│ (reads state)   │  → checkpoint: after-summarizer
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│    WRITER       │  → state["writer.output"]
│ (reads state)   │  → checkpoint: after-writer
└────────┬────────┘
         │
         ▼
    Final Report
```

## What This Example Demonstrates

- **Multi-step workflow**: Searcher → Summarizer → Writer pipeline
- **State passing between agents**: Each agent reads from and writes to shared state
- **Scoped state keys**: `searcher.*`, `summarizer.*`, `writer.*`, `workflow.*`
- **Checkpoint persistence**: StateStore saves snapshots after each phase
- **Version tracking**: State version increments with each modification
- **Real OpenAI provider**: Uses `pkg/models/openai` with gpt-4o-mini

## Prerequisites

- Go 1.22+
- An OpenAI API key

## Setup

```bash
export OPENAI_API_KEY=your-key
```

## Run

```bash
go run examples/research_agent/main.go
```

## File Output

The final report is automatically saved to a timestamped Markdown file. By default, it creates `report_YYYY-MM-DD_HHMMSS.md` in the current directory.

### Configuration

**Command-line flag** (highest priority):
```bash
go run examples/research_agent/main.go -output my_report.md
# Creates: my_report_2024-12-09_143022.md
```

**Environment variable**:
```bash
export RESEARCH_AGENT_OUTPUT=reports/analysis.md
go run examples/research_agent/main.go
# Creates: reports/analysis_2024-12-09_143022.md
```

**Default** (if neither flag nor env var is set):
```bash
go run examples/research_agent/main.go
# Creates: report_2024-12-09_143022.md
```

### Output File Format

The saved file includes a metadata header (hidden in Markdown rendering):
```markdown
<!-- Research Agent Report | topic: latest advancements in AI | started: 2024-12-09T14:30:00Z | completed: 2024-12-09T14:30:22Z | duration: 3.2s | tokens: 640 -->

# AI Advancements Report
...
```

### Behavior Notes

- Timestamps are always appended to prevent overwrites
- Directories are created automatically if they don't exist
- If no extension is provided, `.md` is used
- Errors during file save are logged as warnings (workflow continues)
- The output path is stored in `state["workflow.output_path"]`

## Expected Output

```
╔══════════════════════════════════════════════════════╗
║   RESEARCH AGENT (Phase 2A): Searcher → Summarizer → Writer   ║
╚══════════════════════════════════════════════════════╝
Topic: latest advancements in AI
Initial state version: 0

──────────────────────────
Phase 1: SEARCHER
──────────────────────────
Searcher Output:
- Large language models continue to improve with GPT-4 and Claude 3
- Multimodal AI can now process text, images, and audio together
- AI agents are becoming more autonomous with tool use capabilities
- Open-source models like Llama 3 are closing the gap with proprietary ones
- AI safety and alignment research is accelerating

[duration: 1.2s, tokens: 280, state ver: 5]

──────────────────────────
Phase 2: SUMMARIZER
──────────────────────────
Summarizer Output:
AI is advancing rapidly across multiple fronts. Large language models
are becoming more capable, while multimodal systems can now handle
diverse inputs. The rise of autonomous AI agents and competitive
open-source alternatives marks a new phase in the field.

[duration: 0.9s, tokens: 150, state ver: 9]

──────────────────────────
Phase 3: WRITER
──────────────────────────
Writer Output:
# AI Advancements Report

## Executive Summary
The AI landscape is evolving rapidly with improvements in language
models, multimodal capabilities, and autonomous agents.

## Key Developments
- Enhanced LLMs with better reasoning
- Multimodal processing capabilities
- Autonomous tool-using agents

## Outlook
Expect continued rapid progress with increasing focus on safety.

[duration: 1.1s, tokens: 210, state ver: 14]

════════════════════════════════════════════════════════
WORKFLOW COMPLETE
════════════════════════════════════════════════════════
Topic: latest advancements in AI
Agents: 3 (Searcher, Summarizer, Writer)
Total duration: 3.2s
Total tokens: 640
Final state version: 14

State keys by scope:
  searcher. (4 keys): [searcher.duration_ms searcher.output searcher.status searcher.tokens_used]
  summarizer. (4 keys): [summarizer.duration_ms summarizer.output summarizer.status summarizer.tokens_used]
  writer. (4 keys): [writer.duration_ms writer.output writer.status writer.tokens_used]
  workflow. (4 keys): [workflow.completed_at workflow.id workflow.started_at workflow.status]

✓ Report saved to: report_2024-12-09_143022.md
```

## Time to Complete

This example should take **under 10 minutes** to set up and run.

## Cost Estimate

Running this example uses approximately 600-800 tokens total, costing ~$0.001 with gpt-4o-mini.

## Troubleshooting

| Error | Solution |
|-------|----------|
| `OPENAI_API_KEY environment variable is required` | Run `export OPENAI_API_KEY=your-key` |
| Network timeout | Check internet connection; retry |
| Rate limit (429) | Wait a moment and retry |
| High latency | Normal for sequential API calls; each phase waits for the previous |

## Phase 2A Features Used

- [x] State passing between agents (`state.Get`/`state.Set`)
- [x] Scoped state iteration (`KeysWithPrefix`)
- [x] Checkpoint persistence (`StateStore.Save`)
- [x] State serialization (`Snapshot`)
- [x] Version tracking (`state.Version()`)

## Next Steps

- `examples/multi_agent_demo/` - Similar workflow with checkpoint recovery demo
- `examples/state_demo/` - Full Phase 2A state management features
- `examples/hello_dagens/` - Minimal quickstart example
