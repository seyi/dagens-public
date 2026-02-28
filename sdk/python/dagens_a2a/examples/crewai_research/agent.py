"""
Example: CrewAI Research Agent

This example shows how to wrap a CrewAI crew as an A2A agent
that can be orchestrated by dagens.

Usage:
    python agent.py

Then invoke from dagens or curl:
    curl -X POST http://localhost:8081/a2a/invoke \
        -H "Content-Type: application/json" \
        -d '{
            "jsonrpc": "2.0",
            "id": "1",
            "method": "research",
            "params": {"topic": "AI agent orchestration"}
        }'
"""

import os
import logging

logging.basicConfig(level=logging.INFO)

# Check for required API key
if not os.environ.get("OPENAI_API_KEY"):
    print("Warning: OPENAI_API_KEY not set. CrewAI will fail on actual invocations.")
    print("Set it with: export OPENAI_API_KEY='your-key'")

# Import CrewAI (optional dependency)
try:
    from crewai import Agent, Task, Crew, Process
except ImportError:
    print("CrewAI not installed. Install with: pip install 'dagens-a2a[crewai]'")
    exit(1)

from dagens_a2a import A2AServer
from dagens_a2a.adapters import CrewAIAdapter


def create_research_crew() -> Crew:
    """Create a simple research crew"""

    # Define the research agent
    researcher = Agent(
        role="Senior Research Analyst",
        goal="Research topics thoroughly and provide comprehensive, accurate analysis",
        backstory="""You are an expert research analyst with over 10 years of experience
        in technical research and analysis. You have a keen eye for detail and always
        verify information from multiple sources before drawing conclusions.""",
        verbose=True,
        allow_delegation=False,
    )

    # Define the research task
    research_task = Task(
        name="research",
        description="""Research the topic: {topic}

        Provide a comprehensive analysis including:
        1. Overview of the topic
        2. Key concepts and terminology
        3. Current state and trends
        4. Best practices and recommendations
        5. Potential challenges and considerations

        Be thorough but concise. Focus on actionable insights.""",
        agent=researcher,
        expected_output="A detailed research report with key findings and recommendations",
    )

    # Create the crew
    crew = Crew(
        agents=[researcher],
        tasks=[research_task],
        process=Process.sequential,
        verbose=True,
    )

    return crew


# Create the adapter
adapter = CrewAIAdapter(
    crew=create_research_crew(),
    agent_id="research-crew",
    name="Research Crew",
    description="A CrewAI crew specialized in research and analysis",
)


def main():
    """Run the A2A server"""
    port = int(os.environ.get("PORT", 8081))

    print(f"Starting Research Crew A2A server on port {port}")
    print(f"Agent ID: {adapter.get_card().id}")
    print(f"Capabilities: {[c.name for c in adapter.get_card().capabilities]}")

    server = A2AServer(adapter, port=port)
    server.run()


if __name__ == "__main__":
    main()
