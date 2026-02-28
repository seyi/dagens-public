"""
Base A2A Adapter Interface

Defines the abstract base class that all framework adapters must implement.
"""

from abc import ABC, abstractmethod
from typing import Any, Dict

from dagens_a2a.protocol import AgentCard


class A2AAdapter(ABC):
    """
    Abstract base class for A2A adapters.

    Framework-specific adapters (CrewAI, LangGraph, etc.) should inherit
    from this class and implement the required methods.

    Example:
        class MyAdapter(A2AAdapter):
            def get_card(self) -> AgentCard:
                return AgentCard(
                    id="my-agent",
                    name="My Agent",
                    capabilities=[...]
                )

            async def invoke(self, method: str, params: dict) -> Any:
                # Handle the invocation
                return {"result": "success"}
    """

    @abstractmethod
    def get_card(self) -> AgentCard:
        """
        Return the agent's capability card.

        The card describes what this agent can do and how to interact with it.
        This is called once during server startup and cached.

        Returns:
            AgentCard: The agent's capability declaration
        """
        pass

    @abstractmethod
    async def invoke(self, method: str, params: Dict[str, Any]) -> Any:
        """
        Handle an invocation request.

        This is the main entry point for agent execution. The method parameter
        typically maps to a capability name, and params contains the input data.

        Args:
            method: The capability/method being invoked
            params: Input parameters for the invocation

        Returns:
            Any: The result of the invocation (will be JSON-serialized)

        Raises:
            Exception: Any exception will be converted to an RPC error response
        """
        pass

    async def health_check(self) -> Dict[str, Any]:
        """
        Return health status.

        Override this method to provide custom health check logic.
        The default implementation returns a simple healthy status.

        Returns:
            Dict with at least a "status" key ("healthy" or "unhealthy")
        """
        return {
            "status": "healthy",
            "agent_id": self.get_card().id,
        }

    async def on_startup(self) -> None:
        """
        Called when the server starts.

        Override this to perform initialization tasks like loading models,
        establishing connections, etc.
        """
        pass

    async def on_shutdown(self) -> None:
        """
        Called when the server stops.

        Override this to perform cleanup tasks like closing connections,
        saving state, etc.
        """
        pass


class CallableAdapter(A2AAdapter):
    """
    Simple adapter that wraps a callable function.

    Useful for quick prototyping or simple agents that don't need
    a full framework.

    Example:
        async def my_handler(method: str, params: dict) -> dict:
            return {"result": params.get("input", "") + " processed"}

        adapter = CallableAdapter(
            handler=my_handler,
            agent_id="simple-agent",
            name="Simple Agent",
            capabilities=["process"]
        )
    """

    def __init__(
        self,
        handler: callable,
        agent_id: str,
        name: str,
        description: str = "",
        capabilities: list = None,
    ):
        """
        Create a callable adapter.

        Args:
            handler: Async callable that handles invocations
            agent_id: Unique identifier for the agent
            name: Human-readable name
            description: Description of what the agent does
            capabilities: List of capability names (strings)
        """
        self.handler = handler
        self.agent_id = agent_id
        self.name = name
        self.description = description
        self._capabilities = capabilities or []

    def get_card(self) -> AgentCard:
        from dagens_a2a.protocol import Capability

        return AgentCard(
            id=self.agent_id,
            name=self.name,
            description=self.description,
            capabilities=[
                Capability(name=cap, description="")
                for cap in self._capabilities
            ],
        )

    async def invoke(self, method: str, params: Dict[str, Any]) -> Any:
        import asyncio

        if asyncio.iscoroutinefunction(self.handler):
            return await self.handler(method, params)
        return self.handler(method, params)
