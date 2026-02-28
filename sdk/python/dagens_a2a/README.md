# dagens-a2a

A2A (Agent-to-Agent) Protocol Adapter SDK for Python Agent Frameworks.

This SDK enables Python-based agent frameworks (CrewAI, LangGraph, etc.) to communicate via the A2A protocol with the dagens Go orchestration framework.

## Installation

```bash
# Base package
pip install dagens-a2a

# With CrewAI support
pip install "dagens-a2a[crewai]"

# With LangGraph support
pip install "dagens-a2a[langgraph]"

# All frameworks
pip install "dagens-a2a[all]"
```

## Quick Start

### CrewAI Example

```python
from crewai import Agent, Task, Crew, Process
from dagens_a2a import A2AServer
from dagens_a2a.adapters import CrewAIAdapter

# Create your CrewAI crew
researcher = Agent(
    role="Senior Research Analyst",
    goal="Research topics thoroughly and provide comprehensive analysis",
    backstory="Expert researcher with attention to detail",
    llm="gpt-4",
)

research_task = Task(
    description="Research {topic} and provide detailed analysis",
    agent=researcher,
    expected_output="Detailed research report",
)

crew = Crew(
    agents=[researcher],
    tasks=[research_task],
    process=Process.sequential,
)

# Wrap as A2A adapter
adapter = CrewAIAdapter(
    crew=crew,
    agent_id="research-crew",
    name="Research Crew",
    description="A crew specialized in research and analysis",
)

# Start A2A server
server = A2AServer(adapter, port=8081)
server.run()
```

### LangGraph Example

```python
from langgraph.graph import StateGraph, END
from dagens_a2a import A2AServer
from dagens_a2a.adapters import LangGraphAdapter
from dagens_a2a.protocol import Capability
from typing import TypedDict

# Define your state
class AgentState(TypedDict):
    input: str
    output: str

# Define your processing function
def process(state: AgentState) -> AgentState:
    return {"output": f"Processed: {state['input']}"}

# Create your graph
graph = StateGraph(AgentState)
graph.add_node("process", process)
graph.set_entry_point("process")
graph.add_edge("process", END)

# Wrap as A2A adapter
adapter = LangGraphAdapter(
    graph=graph,
    agent_id="processor",
    name="Text Processor",
    capabilities=[
        Capability(name="process", description="Process text input")
    ],
)

# Start A2A server
server = A2AServer(adapter, port=8082)
server.run()
```

### Custom Adapter

```python
from dagens_a2a import A2AAdapter, A2AServer, AgentCard, Capability
from typing import Any, Dict

class MyCustomAdapter(A2AAdapter):
    def get_card(self) -> AgentCard:
        return AgentCard(
            id="my-agent",
            name="My Custom Agent",
            description="Does something useful",
            capabilities=[
                Capability(name="do_something", description="Does the thing")
            ],
        )

    async def invoke(self, method: str, params: Dict[str, Any]) -> Any:
        if method == "do_something":
            return {"result": f"Did something with {params}"}
        raise ValueError(f"Unknown method: {method}")

adapter = MyCustomAdapter()
server = A2AServer(adapter, port=8080)
server.run()
```

## CLI Usage

```bash
# Start server from a module
dagens-a2a --adapter myagent.adapter --port 8081

# Specify the adapter attribute name
dagens-a2a --adapter myagent --attr my_adapter --port 8082

# Bind to specific host
dagens-a2a --adapter myagent.adapter --host 127.0.0.1 --port 8081
```

## API Endpoints

The A2A server exposes the following endpoints:

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/a2a/invoke` | POST | Invoke the agent with JSON-RPC 2.0 request |
| `/a2a/card` | GET | Get the agent's capability card |
| `/health` | GET | Health check endpoint |
| `/a2a/discover` | GET | Discovery endpoint |

### Invocation Request

```json
POST /a2a/invoke
{
    "jsonrpc": "2.0",
    "id": "request-123",
    "method": "research",
    "params": {
        "topic": "AI orchestration patterns"
    }
}
```

### Invocation Response

```json
{
    "jsonrpc": "2.0",
    "id": "request-123",
    "result": {
        "output": "Research findings...",
        "crew_name": "Research Crew",
        "method": "research"
    }
}
```

## Integration with dagens

Once your Python agent is running as an A2A server, register it with the dagens orchestrator:

```yaml
# agents.yaml
apiVersion: dagens.io/v1
kind: AgentManifest
metadata:
  name: my-agents

spec:
  agents:
    - name: research-crew
      type: external
      runtime:
        language: python
        command: "python -m myagent"
      network:
        port: 8081
      capabilities:
        - name: research
          description: "Research any topic"
```

Then apply the manifest:

```bash
dagens agent apply -f agents.yaml
```

## OpenTelemetry Integration

The server automatically instruments requests with OpenTelemetry when a tracer is configured:

```python
from opentelemetry import trace
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import ConsoleSpanExporter, SimpleSpanProcessor

# Configure tracing
provider = TracerProvider()
provider.add_span_processor(SimpleSpanProcessor(ConsoleSpanExporter()))
trace.set_tracer_provider(provider)

# Server will automatically use the configured tracer
server = A2AServer(adapter, port=8081)
server.run()
```

## Health Checks

Implement custom health checks by overriding the `health_check` method:

```python
from dagens_a2a.health import HealthChecker, HealthStatus, HealthCheckResult

class MyAdapter(A2AAdapter):
    def __init__(self):
        self.health_checker = HealthChecker()
        self.health_checker.add_check("llm_api", self._check_llm)

    async def _check_llm(self) -> HealthCheckResult:
        # Check LLM API connectivity
        try:
            # Your check logic here
            return HealthCheckResult(
                name="llm_api",
                status=HealthStatus.HEALTHY,
            )
        except Exception as e:
            return HealthCheckResult(
                name="llm_api",
                status=HealthStatus.UNHEALTHY,
                message=str(e),
            )

    async def health_check(self) -> dict:
        result = await self.health_checker.run_all()
        return result.to_dict()
```

## License

Apache-2.0
