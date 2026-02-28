#!/usr/bin/env python3
"""
Example demonstrating LangGraph integration with the dagens A2A protocol.
This example shows how to wrap a LangGraph workflow as an A2A agent that can participate
in the distributed dagens orchestration system.
"""

import asyncio
from typing import TypedDict, List, Annotated
import operator
from langgraph.graph import StateGraph, END

# Import the dagens A2A SDK
try:
    from dagens_a2a import A2AServer
    from dagens_a2a.adapters import LangGraphAdapter
    from dagens_a2a.protocol import AgentCard, Capability
except ImportError:
    print("dagens_a2a package not available. This example requires the Python SDK.")
    print("Install with: pip install 'dagens-a2a[langgraph]'")
    exit(1)

# Import LangGraph components
try:
    import os
    # Set the OpenRouter API key for actual LLM usage
    os.environ.setdefault("OPENAI_API_KEY", "${OPENROUTER_API_KEY}")
except ImportError:
    print("LangGraph not available. Install with: pip install 'langgraph'")
    exit(1)


# Define the state structure
class AgentState(TypedDict):
    input: str
    output: str
    steps: List[str]
    current_step: str


# Define nodes for the workflow
def research_node(state: AgentState) -> AgentState:
    """Research node that processes the input."""
    query = state['input']
    steps = state.get('steps', []) + [f"Research completed for: {query}"]

    return {
        'input': query,
        'output': f"Research results for '{query}'",
        'steps': steps,
        'current_step': 'research'
    }


def analysis_node(state: AgentState) -> AgentState:
    """Analysis node that processes research results."""
    data = state['output']
    steps = state.get('steps', []) + [f"Analysis completed for: {data}"]

    return {
        'input': state['input'],
        'output': f"Analysis of '{data}'",
        'steps': steps,
        'current_step': 'analysis'
    }


def report_node(state: AgentState) -> AgentState:
    """Report node that generates final output."""
    input_topic = state['input']
    analysis_result = state['output']
    steps_list = state.get('steps', [])

    final_report = f"""
Final Report for: {input_topic}

Analysis: {analysis_result}

Steps completed:
{chr(10).join(steps_list)}

Summary: This is a simulated report based on research and analysis.
    """

    steps = steps_list + [f"Report generated for: {input_topic}"]

    return {
        'input': input_topic,
        'output': final_report,
        'steps': steps,
        'current_step': 'report'
    }


def create_langgraph_workflow():
    """Create a sample LangGraph workflow for demonstration."""

    # Create the graph
    workflow = StateGraph(AgentState)

    # Add nodes
    workflow.add_node("research", research_node)
    workflow.add_node("analysis", analysis_node)
    workflow.add_node("report", report_node)

    # Set entry point
    workflow.set_entry_point("research")

    # Define transitions
    workflow.add_edge("research", "analysis")
    workflow.add_edge("analysis", "report")
    workflow.add_edge("report", END)

    # Compile the graph
    graph = workflow.compile()

    return graph


def main():
    """Main function to demonstrate LangGraph integration."""
    
    print("Creating LangGraph workflow...")
    graph = create_langgraph_workflow()
    
    print("Creating A2A adapter for LangGraph...")
    adapter = LangGraphAdapter(
        graph=graph,
        agent_id="langgraph-workflow",
        name="LangGraph Research Workflow",
        description="A LangGraph workflow for research, analysis, and reporting",
        capabilities=[
            Capability(name="research", description="Perform research tasks"),
            Capability(name="analysis", description="Analyze data and information"),
            Capability(name="report", description="Generate reports"),
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
    
    print("LangGraph integration with A2A protocol is working correctly!")


if __name__ == "__main__":
    main()