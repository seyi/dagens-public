#!/usr/bin/env python3
"""
Simple example demonstrating basic agent creation and execution.

This example shows how to:
1. Initialize the SparkAgentCoordinator
2. Create and register agents
3. Submit jobs
4. Check job status
"""

from spark_agents import SparkAgentCoordinator, Agent

def main():
    print("=== Spark AI Agents - Simple Example ===\n")

    # Initialize coordinator with 4 executor workers
    print("Initializing coordinator...")
    coordinator = SparkAgentCoordinator(num_executors=4, tasks_per_executor=2)

    # Create a simple research agent
    print("Creating research agent...")
    researcher = Agent(
        name="Researcher",
        description="AI agent specialized in information gathering and analysis",
        capabilities=["search", "analyze", "summarize"],
        partition="research-partition"
    )

    # Register the agent
    print(f"Registering agent: {researcher.name}")
    agent_id = coordinator.register_agent(researcher)
    print(f"  → Agent ID: {agent_id}\n")

    # Submit a job
    instruction = "Research distributed AI agent systems and summarize key concepts"
    print(f"Submitting job with instruction:")
    print(f"  '{instruction}'")

    job_id = coordinator.submit_job(
        agent_id=agent_id,
        instruction=instruction,
        model="gpt-4",
        max_retries=3,
        timeout_ms=300000
    )
    print(f"  → Job ID: {job_id}\n")

    # Check job status
    print("Checking job status...")
    status = coordinator.get_job_status(job_id)
    print(f"  → {status}\n")

    # Get coordinator metrics
    print("Coordinator metrics:")
    metrics = coordinator.get_metrics()
    print(f"  → {metrics}\n")

    # Shutdown
    print("Shutting down coordinator...")
    coordinator.shutdown()
    print("  → Done!\n")


if __name__ == "__main__":
    main()
