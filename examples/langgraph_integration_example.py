#!/usr/bin/env python3
"""
Example demonstrating LangGraph integration with the dagens A2A protocol.
This example shows how to wrap a LangGraph workflow as an A2A agent that can participate
in the distributed dagens orchestration system.
"""

import asyncio
from typing import TypedDict, List
import operator
from langgraph.graph import StateGraph, END
from langchain_core.tools import tool

try:
    from dagens_a2a import A2AServer
    from dagens_a2a.adapters import LangGraphAdapter
    from dagens_a2a.protocol import AgentCard, Capability
except ImportError:
    print("dagens_a2a package not available. This example requires the Python SDK.")
    print("Install with: pip install dagens-a2a")
    exit(1)


# Define the state structure
class AgentState(TypedDict):
    input: str
    output: str
    steps: List[str]
    current_step: str


# Define tools for the graph
@tool
def search_web(query: str) -> str:
    """Search the web for information about the given query."""
    return f"Search results for '{query}': This is simulated search output"


@tool
def analyze_data(data: str) -> str:
    """Analyze the provided data and return insights."""
    return f"Analysis of '{data}': This is simulated analysis output"


# Define the nodes
def research_node(state: AgentState) -> AgentState:
    """Research node that performs web search."""
    query = state['input']
    result = search_web(query)
    steps = state.get('steps', []) + [f"Research completed: {result}"]

    return {
        'input': query,
        'output': result,
        'steps': steps,
        'current_step': 'research'
    }


def analysis_node(state: AgentState) -> AgentState:
    """Analysis node that processes research results."""
    data = state['output']
    result = analyze_data(data)
    steps = state.get('steps', []) + [f"Analysis completed: {result}"]

    return {
        'input': state['input'],
        'output': result,
        'steps': steps,
        'current_step': 'analysis'
    }


def report_node(state: AgentState) -> AgentState:
    """Report node that generates final output."""
    input_topic = state['input']
    steps_str = "\n".join(state.get('steps', []))

    final_report = f"""
Final Report for: {input_topic}

Steps completed:
{steps_str}

Summary: This is a simulated report based on research and analysis.
    """

    steps = state.get('steps', []) + [f"Report generated: {len(final_report)} chars"]

    return {
        'input': input_topic,
        'output': final_report,
        'steps': steps,
        'current_step': 'report'
    }


def create_langgraph_workflow():
    """Create a LangGraph workflow for demonstration."""
    
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
    """Main function to start the A2A server with the LangGraph adapter."""
    
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
    
    print("Starting A2A server on port 8082...")
    server = A2AServer(adapter, port=8082)
    
    try:
        print("Server started. Press Ctrl+C to stop.")
        server.run()
    except KeyboardInterrupt:
        print("\nShutting down server...")
        server.shutdown()


if __name__ == "__main__":
    main()