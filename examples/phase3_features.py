"""
Phase 3 Features Demonstration
===============================

This example demonstrates the advanced features from Phase 3:
1. Planning Module - Task decomposition and DAG generation
2. Artifact Management - Structured output handling
3. A2A Protocol - Agent-to-agent communication
4. Authentication & Security - RBAC and multi-tenant support

All features maintain the distributed Spark architecture.
"""

import json
import time
from spark_agents import (
    SparkAgentCoordinator,
    Planner,
    ArtifactStore,
    A2AClient,
    Authenticator,
    Authorizer,
    AgentCard,
    PlanConstraints,
    Credentials,
    Permission,
)


def demonstrate_planning():
    """Demonstrates the Planning Module"""
    print("\n=== Planning Module Demo ===")

    # Create a planner
    planner = Planner()

    # Define planning constraints
    constraints = PlanConstraints(
        max_steps=10,
        max_parallelism=4,
        timeout=300,  # 5 minutes
        preferred_pattern="hierarchical"
    )

    # Create a plan for a complex objective
    objective = "Research AI agents, summarize findings, and create a presentation"
    plan = planner.create_plan(objective, constraints)

    print(f"Plan ID: {plan.id}")
    print(f"Objective: {plan.objective}")
    print(f"Number of steps: {len(plan.steps)}")
    print(f"Estimated duration: {plan.estimated}")

    # Validate the plan
    if planner.validate_plan(plan):
        print("✓ Plan is valid")

    # Optimize for parallel execution
    optimized_plan = planner.optimize_plan(plan)
    print(f"Optimized steps: {len(optimized_plan.steps)}")

    # Print plan steps
    print("\nPlan Steps:")
    for i, step in enumerate(optimized_plan.steps, 1):
        print(f"  {i}. {step.description}")
        if step.dependencies:
            print(f"     Dependencies: {', '.join(step.dependencies)}")

    # Convert plan to DAG for execution
    dag = plan.to_dag()
    print(f"\nGenerated DAG with {len(dag.stages)} stages")

    return plan


def demonstrate_artifacts():
    """Demonstrates Artifact Management"""
    print("\n=== Artifact Management Demo ===")

    # Create artifact store
    store = ArtifactStore()

    # Create different types of artifacts

    # 1. Text artifact
    text_artifact = store.create_artifact(
        artifact_type="text",
        content="This is a research summary on AI agents...",
        agent_id="researcher-agent",
        session_id="session-123",
        tags=["research", "summary"]
    )

    print(f"Created text artifact: {text_artifact.id}")
    print(f"  Checksum: {text_artifact.checksum}")
    print(f"  Size: {text_artifact.size} bytes")

    # 2. Code artifact
    code_artifact = store.create_artifact(
        artifact_type="code",
        content="""
def analyze_data(data):
    return sum(data) / len(data)
        """,
        agent_id="code-agent",
        session_id="session-123",
        tags=["python", "analysis"],
        metadata={
            "language": "python",
            "function": "analyze_data"
        }
    )

    print(f"Created code artifact: {code_artifact.id}")

    # 3. Image artifact (simulated)
    image_artifact = store.create_artifact(
        artifact_type="image",
        content=b"<binary image data>",
        content_type="image/png",
        agent_id="viz-agent",
        session_id="session-123",
        tags=["visualization", "chart"]
    )

    print(f"Created image artifact: {image_artifact.id}")

    # List artifacts by session
    session_artifacts = store.list_artifacts(
        filters={
            "session_id": "session-123"
        }
    )

    print(f"\nFound {len(session_artifacts)} artifacts for session")

    # Search by tags
    research_artifacts = store.list_artifacts(
        filters={
            "tags": ["research"]
        }
    )

    print(f"Found {len(research_artifacts)} research artifacts")

    # Version tracking
    updated_text = store.update_artifact(
        artifact_id=text_artifact.id,
        content="Updated research summary with new findings..."
    )

    print(f"\nArtifact updated to version {updated_text.version}")

    # Retrieve previous version
    v1 = store.get_version(text_artifact.id, version=1)
    print(f"Retrieved version 1: {len(v1.content)} bytes")

    return store


def demonstrate_a2a_protocol():
    """Demonstrates Agent-to-Agent Protocol"""
    print("\n=== A2A Protocol Demo ===")

    # Create discovery registry
    from spark_agents import DiscoveryRegistry
    registry = DiscoveryRegistry()

    # Register agents with their capabilities (Agent Cards)

    # Research agent
    research_card = AgentCard(
        id="research-agent",
        name="Research Assistant",
        description="Conducts web research and summarizes findings",
        version="1.0.0",
        endpoint="http://localhost:8001/invoke",
        capabilities=[
            {
                "name": "web_research",
                "description": "Search and analyze web content",
                "input_schema": {"query": "string"},
                "output_schema": {"summary": "string", "sources": "array"}
            },
            {
                "name": "summarize",
                "description": "Summarize long documents",
                "input_schema": {"text": "string"},
                "output_schema": {"summary": "string"}
            }
        ],
        modalities=["text", "stream"],
        auth_scheme={
            "type": "bearer",
            "parameters": {}
        },
        supported_patterns=["request_response", "server_sent_events"]
    )

    registry.register(research_card)
    print(f"Registered {research_card.name}")

    # Code agent
    code_card = AgentCard(
        id="code-agent",
        name="Code Assistant",
        description="Generates and analyzes code",
        version="1.0.0",
        endpoint="http://localhost:8002/invoke",
        capabilities=[
            {
                "name": "generate_code",
                "description": "Generate code from specifications",
                "input_schema": {"spec": "string", "language": "string"},
                "output_schema": {"code": "string"}
            }
        ],
        modalities=["text"],
        auth_scheme={"type": "api_key"},
        supported_patterns=["request_response"]
    )

    registry.register(code_card)
    print(f"Registered {code_card.name}")

    # Discover agents by capability
    researchers = registry.find_by_capability("web_research")
    print(f"\nFound {len(researchers)} agents with 'web_research' capability:")
    for agent in researchers:
        print(f"  - {agent.name} ({agent.id})")

    # Create A2A client
    client = A2AClient(registry)

    # Invoke remote agent
    print("\nInvoking remote research agent...")
    result = client.invoke_agent(
        agent_id="research-agent",
        input={
            "instruction": "Research the latest developments in AI agents",
            "capability": "web_research",
            "params": {
                "query": "AI agents 2024"
            }
        }
    )

    print(f"Result: {result.result}")

    # Agent-to-agent collaboration
    print("\nAgent collaboration scenario:")
    print("1. Research agent gathers information")
    print("2. Code agent generates analysis code")
    print("3. Viz agent creates visualizations")

    # List all available agents
    all_agents = registry.list_all()
    print(f"\nTotal registered agents: {len(all_agents)}")

    return registry


def demonstrate_authentication():
    """Demonstrates Authentication & Security"""
    print("\n=== Authentication & Security Demo ===")

    # Create authenticator and authorizer
    authenticator = Authenticator()
    authorizer = Authorizer()

    # Register a user
    user = authenticator.register_user(
        username="alice",
        password="secret123",
        email="alice@example.com",
        tenant_id="tenant-1",
        roles=["agent_user", "developer"]
    )

    print(f"Registered user: {user.username}")

    # Authenticate with password
    credentials = Credentials(
        type="password",
        username="alice",
        password="secret123"
    )

    principal = authenticator.authenticate(credentials)
    print(f"Authenticated principal: {principal.username}")
    print(f"Roles: {', '.join(principal.roles)}")
    print(f"Token: {principal.token[:20]}...")

    # Create API key
    api_key = authenticator.create_api_key(
        user_id=principal.id,
        name="Production API Key",
        expires_at=None  # Never expires
    )

    print(f"\nCreated API key: {api_key.key[:20]}...")

    # Authenticate with API key
    api_credentials = Credentials(
        type="api_key",
        api_key=api_key.key
    )

    api_principal = authenticator.authenticate(api_credentials)
    print(f"Authenticated via API key: {api_principal.username}")

    # Set up RBAC permissions

    # Agent user role can run agents
    authorizer.grant_permission(
        role="agent_user",
        permission=Permission(
            resource="agents/*",
            actions=["read", "execute"],
            scope="tenant"
        )
    )

    # Developer role has more permissions
    authorizer.grant_permission(
        role="developer",
        permission=Permission(
            resource="agents/*",
            actions=["read", "write", "execute", "delete"],
            scope="tenant"
        )
    )

    authorizer.grant_permission(
        role="developer",
        permission=Permission(
            resource="artifacts/*",
            actions=["read", "write"],
            scope="tenant"
        )
    )

    print("\nPermissions configured")

    # Check authorization
    can_execute = authorizer.authorize(
        principal=principal,
        resource="agents/research-agent",
        action="execute"
    )

    print(f"Can execute agent: {can_execute}")

    can_delete = authorizer.authorize(
        principal=principal,
        resource="agents/research-agent",
        action="delete"
    )

    print(f"Can delete agent: {can_delete}")

    can_write_artifacts = authorizer.authorize(
        principal=principal,
        resource="artifacts/doc-123",
        action="write"
    )

    print(f"Can write artifacts: {can_write_artifacts}")

    # Audit logging
    from spark_agents import AuditLogger
    audit = AuditLogger()

    audit.log(
        event_type="agent.execute",
        principal_id=principal.id,
        resource="agents/research-agent",
        action="execute",
        result="success",
        metadata={
            "duration": 1.5,
            "output_size": 1024
        }
    )

    logs = audit.get_logs(limit=10)
    print(f"\nAudit logs: {len(logs)} entries")

    return principal


def demonstrate_integrated_workflow():
    """Demonstrates all Phase 3 features working together"""
    print("\n=== Integrated Workflow Demo ===")
    print("Scenario: Secure, multi-agent research and reporting workflow\n")

    # 1. Authenticate user
    print("Step 1: Authenticate user")
    authenticator = Authenticator()
    user = authenticator.register_user(
        username="researcher",
        password="pass123",
        tenant_id="research-team",
        roles=["researcher"]
    )

    principal = authenticator.authenticate(
        Credentials(type="password", username="researcher", password="pass123")
    )
    print(f"✓ Authenticated: {principal.username}\n")

    # 2. Check authorization
    print("Step 2: Check authorization")
    authorizer = Authorizer()
    authorizer.grant_permission(
        role="researcher",
        permission=Permission(
            resource="*",
            actions=["read", "write", "execute"],
            scope="tenant"
        )
    )

    if authorizer.authorize(principal, "agents/*", "execute"):
        print("✓ Authorized to execute agents\n")

    # 3. Create execution plan
    print("Step 3: Create execution plan")
    planner = Planner()
    plan = planner.create_plan(
        objective="Research AI trends, analyze data, create report",
        constraints=PlanConstraints(
            max_steps=5,
            preferred_pattern="pipeline"
        )
    )
    print(f"✓ Created plan with {len(plan.steps)} steps\n")

    # 4. Discover and invoke agents via A2A
    print("Step 4: Discover and invoke agents")
    registry = DiscoveryRegistry()

    # Register research agent
    registry.register(AgentCard(
        id="research-agent",
        name="Research Agent",
        endpoint="http://localhost:8001/invoke",
        capabilities=[{"name": "research"}]
    ))

    client = A2AClient(registry)
    print("✓ Discovered research agent\n")

    # 5. Execute and store artifacts
    print("Step 5: Execute and store artifacts")
    artifact_store = ArtifactStore()

    # Simulate agent execution
    result_artifact = artifact_store.create_artifact(
        artifact_type="text",
        content="Research findings on AI trends in 2024...",
        agent_id="research-agent",
        session_id="workflow-session",
        tags=["research", "report"],
        metadata={
            "created_by": principal.username,
            "tenant_id": principal.tenant_id
        }
    )

    print(f"✓ Created artifact: {result_artifact.id}")
    print(f"  Type: {result_artifact.type}")
    print(f"  Size: {result_artifact.size} bytes")
    print(f"  Tags: {', '.join(result_artifact.tags)}\n")

    # 6. Audit the workflow
    print("Step 6: Audit logging")
    audit = AuditLogger()
    audit.log(
        event_type="workflow.complete",
        principal_id=principal.id,
        resource="workflow/research-pipeline",
        action="execute",
        result="success",
        metadata={
            "plan_id": plan.id,
            "artifact_id": result_artifact.id,
            "duration": 45.2
        }
    )

    print("✓ Workflow completed and audited\n")

    print("=" * 60)
    print("All Phase 3 features integrated successfully!")
    print("=" * 60)


def main():
    """Run all demonstrations"""
    print("=" * 60)
    print("Spark AI Agents - Phase 3 Features Demonstration")
    print("=" * 60)

    # Individual feature demos
    demonstrate_planning()
    demonstrate_artifacts()
    demonstrate_a2a_protocol()
    demonstrate_authentication()

    # Integrated workflow
    demonstrate_integrated_workflow()

    print("\n✓ All demonstrations completed successfully!")


if __name__ == "__main__":
    main()
