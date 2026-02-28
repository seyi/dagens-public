"""
dagens-a2a: A2A Protocol Adapter SDK for Python Agent Frameworks

This SDK enables Python-based agent frameworks (CrewAI, LangGraph, etc.)
to communicate via the A2A (Agent-to-Agent) protocol with the dagens
orchestration framework.

Example usage:
    from dagens_a2a import A2AServer, CrewAIAdapter
    from crewai import Crew

    crew = Crew(agents=[...], tasks=[...])
    adapter = CrewAIAdapter(crew, agent_id="my-crew", name="Research Crew")

    server = A2AServer(adapter, port=8081)
    server.run()
"""

from dagens_a2a.protocol import (
    AgentCard,
    Capability,
    InvocationRequest,
    InvocationResponse,
    RPCError,
    Modality,
    AgentStatus,
    StreamChunk,
)
from dagens_a2a.adapter import A2AAdapter, CallableAdapter
from dagens_a2a.server import A2AServer, create_server

__version__ = "0.1.0"

__all__ = [
    # Server
    "A2AServer",
    "create_server",
    # Adapters
    "A2AAdapter",
    "CallableAdapter",
    # Protocol types
    "AgentCard",
    "Capability",
    "InvocationRequest",
    "InvocationResponse",
    "RPCError",
    "Modality",
    "AgentStatus",
    "StreamChunk",
    # Version
    "__version__",
]
