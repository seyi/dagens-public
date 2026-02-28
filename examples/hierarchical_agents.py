#!/usr/bin/env python3
"""
Hierarchical agent example demonstrating agent dependencies.

This example shows how to:
1. Create multiple specialized agents
2. Define dependencies between agents (DAG)
3. Execute hierarchical workflows
4. Leverage Spark's DAG scheduling
"""

from spark_agents import SparkAgentCoordinator, Agent


def main():
    print("=== Spark AI Agents - Hierarchical Example ===\n")

    # Initialize coordinator
    print("Initializing coordinator with 8 executors...")
    coordinator = SparkAgentCoordinator(num_executors=8, tasks_per_executor=2)

    # Create specialized agents for different tasks
    print("\nCreating specialized agents...\n")

    # Data collection agent
    data_collector = Agent(
        name="DataCollector",
        description="Collects data from various sources",
        capabilities=["search", "fetch", "extract"],
        partition="data-partition"
    )
    print(f"  1. {data_collector.name}: {data_collector.description}")

    # Analysis agent (depends on data collector)
    analyzer = Agent(
        name="Analyzer",
        description="Analyzes collected data for insights",
        capabilities=["analyze", "classify", "pattern_detection"],
        dependencies=[data_collector],
        partition="compute-partition"
    )
    print(f"  2. {analyzer.name}: {analyzer.description}")
    print(f"     Dependencies: {[d.name for d in analyzer.dependencies]}")

    # Visualization agent (depends on analyzer)
    visualizer = Agent(
        name="Visualizer",
        description="Creates visualizations from analysis results",
        capabilities=["plot", "chart", "diagram"],
        dependencies=[analyzer],
        partition="viz-partition"
    )
    print(f"  3. {visualizer.name}: {visualizer.description}")
    print(f"     Dependencies: {[d.name for d in visualizer.dependencies]}")

    # Report generator (depends on both analyzer and visualizer)
    report_generator = Agent(
        name="ReportGenerator",
        description="Generates comprehensive reports",
        capabilities=["format", "write", "export"],
        dependencies=[analyzer, visualizer],
        partition="output-partition"
    )
    print(f"  4. {report_generator.name}: {report_generator.description}")
    print(f"     Dependencies: {[d.name for d in report_generator.dependencies]}")

    # Register all agents
    print("\nRegistering agents with coordinator...")
    agents = [data_collector, analyzer, visualizer, report_generator]
    agent_ids = {}

    for agent in agents:
        agent_id = coordinator.register_agent(agent)
        agent_ids[agent.name] = agent_id
        print(f"  → {agent.name}: {agent_id}")

    # Submit job for the top-level agent (report generator)
    # This will automatically schedule all dependent agents in a DAG
    print("\nSubmitting hierarchical workflow...")
    instruction = (
        "Generate a comprehensive report on distributed AI systems "
        "including data collection, analysis, and visualizations"
    )
    print(f"Instruction: '{instruction}'")

    job_id = coordinator.submit_job(
        agent_id=agent_ids["ReportGenerator"],
        instruction=instruction,
        model="gpt-4",
        max_retries=2,
        timeout_ms=600000  # 10 minutes
    )
    print(f"  → Job ID: {job_id}")

    # The DAG scheduler will execute agents in order:
    # Stage 3: DataCollector (deepest dependency)
    # Stage 2: Analyzer (depends on DataCollector)
    # Stage 1: Visualizer (depends on Analyzer)
    # Stage 0: ReportGenerator (depends on Analyzer and Visualizer)

    print("\nExpected execution order (DAG scheduling):")
    print("  Stage 3: DataCollector")
    print("  Stage 2: Analyzer")
    print("  Stage 1: Visualizer")
    print("  Stage 0: ReportGenerator")

    # Check status
    print("\nChecking job status...")
    status = coordinator.get_job_status(job_id)
    print(f"  → {status}")

    # Get metrics
    print("\nCoordinator metrics:")
    metrics = coordinator.get_metrics()
    print(f"  → {metrics}")

    # Shutdown
    print("\nShutting down coordinator...")
    coordinator.shutdown()
    print("  → Done!\n")


if __name__ == "__main__":
    main()
