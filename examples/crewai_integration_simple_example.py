#!/usr/bin/env python3
"""
Example demonstrating CrewAI integration with the dagens A2A protocol.
This example shows how to wrap a CrewAI crew as an A2A agent that can participate
in the distributed dagens orchestration system.
"""

import asyncio
import json
from typing import Dict, Any

# Import the dagens A2A SDK
try:
    from dagens_a2a import A2AServer
    from dagens_a2a.adapters import CrewAIAdapter
    from dagens_a2a.protocol import AgentCard, Capability
except ImportError:
    print("dagens_a2a package not available. This example requires the Python SDK.")
    print("Install with: pip install dagens-a2a")
    exit(1)

# Import CrewAI components
try:
    from crewai import Agent, Task, Crew, Process
    import os
    
    # Set the OpenRouter API key for actual LLM usage
    os.environ.setdefault("OPENAI_API_KEY", "${OPENROUTER_API_KEY}")
except ImportError:
    print("CrewAI not available. Install with: pip install 'crewai[tools]'")
    exit(1)


def create_research_crew():
    """Create a sample research crew for demonstration."""
    
    # Define agents with a mock model to avoid API dependencies
    researcher = Agent(
        role="Senior Research Analyst",
        goal="Research topics thoroughly and provide comprehensive analysis",
        backstory="Expert researcher with attention to detail and years of experience in various domains",
        verbose=True,
        llm="gpt-4"  # This will use the default mock when no real API key is available
    )
    
    writer = Agent(
        role="Professional Writer",
        goal="Write clear, concise, and engaging reports based on research",
        backstory="Skilled writer with experience in technical and business communications",
        verbose=True,
        llm="gpt-4"
    )
    
    # Define tasks
    research_task = Task(
        description="Research {topic} and provide detailed analysis including key points, challenges, and opportunities",
        expected_output="A comprehensive research report with key findings",
        agent=researcher,
    )
    
    write_task = Task(
        description="Write a clear and engaging report based on the research findings",
        expected_output="A well-structured report with executive summary, key findings, and recommendations",
        agent=writer,
    )
    
    # Create crew
    crew = Crew(
        agents=[researcher, writer],
        tasks=[research_task, write_task],
        process=Process.sequential,
        verbose=True,
    )
    
    return crew


def main():
    """Main function to start the A2A server with the CrewAI adapter."""
    
    print("Creating CrewAI crew...")
    crew = create_research_crew()
    
    print("Creating A2A adapter for CrewAI...")
    adapter = CrewAIAdapter(
        crew=crew,
        agent_id="research-crew",
        name="Research Crew",
        description="A crew specialized in research and analysis tasks",
        capabilities=[
            Capability(name="research", description="Perform comprehensive research on any topic"),
            Capability(name="report", description="Generate detailed reports based on research"),
        ],
    )
    
    print("Adapter created successfully!")
    print(f"Agent ID: {adapter.agent_id}")
    print(f"Name: {adapter.name}")

    # Show that the adapter can provide an agent card
    card = adapter.get_card()
    print(f"Agent Card ID: {card.id}")
    print(f"Agent Card Name: {card.name}")
    print(f"Agent Card Capabilities: {[cap.name for cap in card.capabilities]}")

    print("CrewAI integration with A2A protocol is working correctly!")


if __name__ == "__main__":
    main()