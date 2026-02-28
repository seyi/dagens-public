"""
LangGraph Adapter

Wraps a LangGraph graph as an A2A-compatible agent.
"""

import logging
from typing import Any, Dict, List, Optional, Union

from dagens_a2a.adapter import A2AAdapter
from dagens_a2a.protocol import AgentCard, Capability

logger = logging.getLogger(__name__)


class LangGraphAdapter(A2AAdapter):
    """
    Adapter that wraps a LangGraph graph as an A2A agent.

    This adapter exposes a LangGraph StateGraph or CompiledGraph through
    the A2A protocol, allowing the dagens orchestrator to invoke it.

    Example:
        from langgraph.graph import StateGraph, END
        from dagens_a2a import A2AServer
        from dagens_a2a.adapters import LangGraphAdapter
        from typing import TypedDict

        # Define state
        class AgentState(TypedDict):
            input: str
            output: str

        # Create graph
        def process(state: AgentState) -> AgentState:
            return {"output": f"Processed: {state['input']}"}

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
    """

    def __init__(
        self,
        graph: Union["StateGraph", "CompiledGraph"],  # type: ignore
        agent_id: str,
        name: str,
        description: str = "",
        capabilities: Optional[List[Capability]] = None,
        config: Optional[Dict[str, Any]] = None,
    ):
        """
        Create a LangGraph adapter.

        Args:
            graph: The LangGraph StateGraph or CompiledGraph to wrap.
                   If StateGraph is provided, it will be compiled automatically.
            agent_id: Unique identifier for this agent
            name: Human-readable name
            description: Description of what this graph does
            capabilities: Capabilities to advertise. Required for LangGraph
                         as graphs don't have introspectable task definitions.
            config: Optional configuration to pass to graph.invoke()
        """
        self.agent_id = agent_id
        self.name = name
        self.description = description
        self._capabilities = capabilities or []
        self.config = config or {}
        self._card: Optional[AgentCard] = None

        # Compile graph if needed
        if hasattr(graph, 'compile'):
            self.graph = graph.compile()
        else:
            self.graph = graph

    def get_card(self) -> AgentCard:
        """Return the agent's capability card"""
        if self._card is not None:
            return self._card

        # Use provided capabilities or generate a default
        capabilities = self._capabilities
        if not capabilities:
            capabilities = [
                Capability(
                    name="invoke",
                    description="Invoke the LangGraph workflow",
                )
            ]

        self._card = AgentCard(
            id=self.agent_id,
            name=self.name,
            description=self.description or "LangGraph workflow agent",
            capabilities=capabilities,
        )

        return self._card

    async def invoke(self, method: str, params: Dict[str, Any]) -> Any:
        """
        Handle an invocation request.

        The graph is invoked with params as the initial state.

        Args:
            method: The capability/method being invoked (informational)
            params: Initial state passed to graph.ainvoke(params)

        Returns:
            The final state from the graph execution
        """
        logger.info(f"Invoking LangGraph '{self.name}' with method '{method}'")

        try:
            # LangGraph supports async invocation
            if hasattr(self.graph, 'ainvoke'):
                result = await self.graph.ainvoke(params, config=self.config)
            else:
                # Fall back to sync version in executor
                import asyncio
                loop = asyncio.get_event_loop()
                result = await loop.run_in_executor(
                    None,
                    lambda: self.graph.invoke(params, config=self.config)
                )

            # Result is the final state dict
            return {
                "state": result,
                "graph_name": self.name,
                "method": method,
            }

        except Exception as e:
            logger.exception(f"LangGraph execution failed: {e}")
            raise

    async def stream(self, method: str, params: Dict[str, Any]):
        """
        Stream graph execution.

        Yields intermediate states as the graph executes.

        Args:
            method: The capability/method being invoked
            params: Initial state

        Yields:
            Intermediate state updates
        """
        logger.info(f"Streaming LangGraph '{self.name}' with method '{method}'")

        try:
            if hasattr(self.graph, 'astream'):
                async for state in self.graph.astream(params, config=self.config):
                    yield {
                        "state": state,
                        "graph_name": self.name,
                        "streaming": True,
                    }
            else:
                # Fall back to non-streaming
                result = await self.invoke(method, params)
                yield result

        except Exception as e:
            logger.exception(f"LangGraph streaming failed: {e}")
            raise

    async def health_check(self) -> Dict[str, Any]:
        """Return health status"""
        # Try to get graph info
        nodes = []
        if hasattr(self.graph, 'nodes'):
            nodes = list(self.graph.nodes.keys()) if hasattr(self.graph.nodes, 'keys') else []

        return {
            "status": "healthy",
            "agent_id": self.agent_id,
            "agent_type": "langgraph",
            "graph_nodes": nodes,
        }


def create_langgraph_adapter(
    graph: Union["StateGraph", "CompiledGraph"],  # type: ignore
    agent_id: str,
    name: str,
    description: str = "",
    capabilities: Optional[List[Capability]] = None,
) -> LangGraphAdapter:
    """
    Factory function to create a LangGraph adapter.

    Args:
        graph: The LangGraph graph to wrap
        agent_id: Unique identifier
        name: Human-readable name
        description: Description of what the graph does
        capabilities: Capabilities to advertise

    Returns:
        Configured LangGraphAdapter
    """
    return LangGraphAdapter(
        graph=graph,
        agent_id=agent_id,
        name=name,
        description=description,
        capabilities=capabilities,
    )
