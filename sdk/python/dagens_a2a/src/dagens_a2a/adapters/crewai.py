"""
CrewAI Adapter

Wraps a CrewAI Crew as an A2A-compatible agent.
"""

import logging
from typing import Any, Dict, List, Optional

from dagens_a2a.adapter import A2AAdapter
from dagens_a2a.protocol import AgentCard, Capability

logger = logging.getLogger(__name__)


class CrewAIAdapter(A2AAdapter):
    """
    Adapter that wraps a CrewAI Crew as an A2A agent.

    This adapter exposes a CrewAI crew through the A2A protocol,
    allowing the dagens orchestrator to invoke it.

    Example:
        from crewai import Agent, Task, Crew, Process
        from dagens_a2a import A2AServer
        from dagens_a2a.adapters import CrewAIAdapter

        # Create your crew
        researcher = Agent(
            role="Senior Research Analyst",
            goal="Research topics thoroughly",
            backstory="Expert researcher",
            llm="gpt-4",
        )

        research_task = Task(
            description="Research {topic}",
            agent=researcher,
            expected_output="Research report",
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
            description="A crew specialized in research",
        )

        # Start A2A server
        server = A2AServer(adapter, port=8081)
        server.run()
    """

    def __init__(
        self,
        crew: "Crew",  # type: ignore  # CrewAI may not be installed
        agent_id: str,
        name: str,
        description: str = "",
        capabilities: Optional[List[Capability]] = None,
    ):
        """
        Create a CrewAI adapter.

        Args:
            crew: The CrewAI Crew instance to wrap
            agent_id: Unique identifier for this agent
            name: Human-readable name
            description: Description of what this crew does
            capabilities: Optional explicit capabilities. If not provided,
                         capabilities are extracted from the crew's tasks.
        """
        self.crew = crew
        self.agent_id = agent_id
        self.name = name
        self.description = description
        self._explicit_capabilities = capabilities
        self._card: Optional[AgentCard] = None

    def get_card(self) -> AgentCard:
        """Return the agent's capability card"""
        if self._card is not None:
            return self._card

        # Extract capabilities from crew tasks if not explicitly provided
        if self._explicit_capabilities:
            capabilities = self._explicit_capabilities
        else:
            capabilities = self._extract_capabilities()

        self._card = AgentCard(
            id=self.agent_id,
            name=self.name,
            description=self.description or self._generate_description(),
            capabilities=capabilities,
        )

        return self._card

    def _extract_capabilities(self) -> List[Capability]:
        """Extract capabilities from CrewAI tasks"""
        capabilities = []

        # Try to extract from tasks
        if hasattr(self.crew, 'tasks') and self.crew.tasks:
            for i, task in enumerate(self.crew.tasks):
                task_name = getattr(task, 'name', None) or f"task_{i}"
                task_desc = getattr(task, 'description', '') or ''

                # Clean up task name (remove template variables)
                task_name = task_name.replace('{', '').replace('}', '')

                capabilities.append(Capability(
                    name=task_name,
                    description=task_desc[:200] if task_desc else "",
                ))

        # If no tasks found, create a generic capability
        if not capabilities:
            capabilities.append(Capability(
                name="execute",
                description="Execute the crew with provided inputs",
            ))

        return capabilities

    def _generate_description(self) -> str:
        """Generate description from crew agents"""
        if hasattr(self.crew, 'agents') and self.crew.agents:
            roles = [getattr(a, 'role', 'agent') for a in self.crew.agents]
            return f"CrewAI crew with: {', '.join(roles)}"
        return "CrewAI crew"

    async def invoke(self, method: str, params: Dict[str, Any]) -> Any:
        """
        Handle an invocation request.

        The crew is executed with the params as inputs. The method parameter
        is currently ignored as CrewAI crews typically have a single entry point.

        Args:
            method: The capability/method being invoked (informational)
            params: Input parameters passed to crew.kickoff(inputs=params)

        Returns:
            Dict containing the crew output and metadata
        """
        logger.info(f"Invoking CrewAI crew '{self.name}' with method '{method}'")

        try:
            # CrewAI's kickoff can be sync or async
            import asyncio

            if hasattr(self.crew, 'kickoff_async'):
                # Use async version if available
                result = await self.crew.kickoff_async(inputs=params)
            elif asyncio.iscoroutinefunction(self.crew.kickoff):
                result = await self.crew.kickoff(inputs=params)
            else:
                # Run sync version in executor to not block
                loop = asyncio.get_event_loop()
                result = await loop.run_in_executor(
                    None,
                    lambda: self.crew.kickoff(inputs=params)
                )

            # Extract output - CrewAI returns different types depending on version
            if hasattr(result, 'raw'):
                output = result.raw
            elif hasattr(result, 'output'):
                output = result.output
            else:
                output = str(result)

            # Build response with metadata
            response = {
                "output": output,
                "crew_name": self.name,
                "method": method,
            }

            # Include task outputs if available
            if hasattr(result, 'tasks_output'):
                response["tasks"] = [
                    {
                        "task": getattr(t, 'name', 'unknown'),
                        "output": str(getattr(t, 'raw', t)),
                    }
                    for t in result.tasks_output
                ]

            return response

        except Exception as e:
            logger.exception(f"CrewAI execution failed: {e}")
            raise

    async def health_check(self) -> Dict[str, Any]:
        """Return health status"""
        return {
            "status": "healthy",
            "agent_id": self.agent_id,
            "agent_type": "crewai",
            "crew_agents": len(getattr(self.crew, 'agents', [])),
            "crew_tasks": len(getattr(self.crew, 'tasks', [])),
        }


def create_crewai_adapter(
    crew: "Crew",  # type: ignore
    agent_id: str,
    name: str,
    description: str = "",
) -> CrewAIAdapter:
    """
    Factory function to create a CrewAI adapter.

    Args:
        crew: The CrewAI Crew to wrap
        agent_id: Unique identifier
        name: Human-readable name
        description: Description of what the crew does

    Returns:
        Configured CrewAIAdapter
    """
    return CrewAIAdapter(
        crew=crew,
        agent_id=agent_id,
        name=name,
        description=description,
    )
