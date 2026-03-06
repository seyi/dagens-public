# Spark AI Agents Architecture

This document provides a deep dive into the architecture of Spark AI Agents, explaining how it leverages Apache Spark's distributed computing concepts for AI agent orchestration.

## Table of Contents

1. [Overview](#overview)
2. [Core Components](#core-components)
3. [Execution Model](#execution-model)
4. [Fault Tolerance](#fault-tolerance)
5. [Scheduling Strategy](#scheduling-strategy)
6. [Communication Layer](#communication-layer)
7. [State Management](#state-management)
8. [Comparison with Spark](#comparison-with-spark)

## Overview

Spark AI Agents applies Apache Spark's proven distributed computing patterns to AI agent execution. The key insight is that AI agent workflows, like data processing workflows, form Directed Acyclic Graphs (DAGs) and benefit from:

- **Parallel execution** of independent tasks
- **Fault tolerance** through lineage tracking
- **Locality-aware scheduling** for performance
- **Resource elasticity** for scalability

## Core Components

### 1. Agent Orchestrator (DAGScheduler)

**Location**: `pkg/scheduler/dag_scheduler.go`

The Agent Orchestrator is responsible for high-level workflow coordination, analogous to Spark's DAGScheduler.

**Responsibilities**:
- Build DAG from agent dependencies
- Break DAG into stages at shuffle boundaries
- Track stage completion and trigger subsequent stages
- Handle stage failures and retries
- Maintain job metadata

**Key Concepts**:
```
Agent DAG → Stages → Tasks
```

**Stage Boundaries**:
- Created at agent dependencies (like shuffle boundaries in Spark)
- Agents with no dependencies can execute in parallel (same stage)
- Stages execute in topological order

**Example DAG**:
```
ReportGenerator
       ↓
   Analyzer ←── Visualizer
       ↓
DataCollector

Stages:
Stage 2: [DataCollector]
Stage 1: [Analyzer, Visualizer] (parallel)
Stage 0: [ReportGenerator]
```

### 2. Task Scheduler

**Location**: `pkg/scheduler/task_scheduler.go`

The Task Scheduler handles low-level task assignment to executors, similar to Spark's TaskScheduler.

**Responsibilities**:
- Assign tasks to available executors
- Implement locality-aware scheduling
- Handle task failures and retries
- Track executor health
- Manage task queue

**Locality Levels** (in order of preference):
1. **PROCESS_LOCAL**: Task runs on executor with cached data/agent state
2. **NODE_LOCAL**: Task runs on same node
3. **RACK_LOCAL**: Task runs in same rack/region
4. **ANY**: Task runs on any available executor

**Delay Scheduling**:
- Wait up to `maxLocalityWaitTime` for better locality
- Prevents suboptimal scheduling due to temporary unavailability
- Balances locality with job latency

### 3. Executor Pool

**Location**: `pkg/executor/executor.go`

Executors are worker processes that run agent tasks, analogous to Spark Executors.

**Responsibilities**:
- Execute agent tasks in thread pool
- Cache agent definitions and state
- Send heartbeats to scheduler
- Report task completion/failure
- Manage local resources

**Executor Properties**:
- **Partition**: Logical partition for locality
- **Node**: Physical node identifier
- **Rack**: Rack/region for multi-level locality
- **MaxTasks**: Concurrency limit

**Heartbeat Mechanism**:
- Periodic health signals to coordinator
- Report metrics (CPU, memory, tasks)
- Receive kill signals for failed tasks
- Enable executor failure detection

### 4. Agent Abstraction

**Location**: `pkg/agent/agent.go`

Agents are the core abstraction, similar to RDDs in Spark.

**Agent Properties**:
1. **Capabilities**: List of tools the agent can use
2. **Dependencies**: Other agents this depends on (DAG edges)
3. **Partition**: Locality hint for scheduling
4. **Executor**: Logic for executing the agent

**Agent Task**:
- Represents a unit of work for an agent
- Contains input, state, and execution metadata
- Tracks attempts and retry state

**Agent Stage**:
- Groups tasks that can execute in parallel
- Represents a level in the DAG
- Contains dependency information

### 5. State Manager

**Location**: `pkg/state/checkpoint.go`

Manages agent state, checkpointing, and lineage tracking.

**Components**:
- **CheckpointManager**: Saves/restores agent state
- **LineageTracker**: Records execution dependencies
- **Storage**: Pluggable backend (memory, file, S3)

**Checkpointing Strategy**:
- Checkpoint at stage boundaries
- Truncate lineage to prevent long recomputation chains
- Support both reliable (HDFS) and local checkpointing
- Configurable checkpoint frequency

**Lineage Tracking**:
```
LineageNode {
    AgentID: "analyzer"
    Input: {...}
    Output: {...}
    Dependencies: [data_collector_node]
}
```

Enables recomputation on failure without re-executing entire DAG.

## Execution Model

### Job Submission Flow

```
1. User submits root agent + input
   ↓
2. DAGScheduler builds DAG from dependencies
   ↓
3. DAG is broken into stages
   ↓
4. Tasks are created for each stage
   ↓
5. Tasks submitted to TaskScheduler
   ↓
6. TaskScheduler assigns to executors (locality-aware)
   ↓
7. Executors run tasks
   ↓
8. Results propagate through DAG
   ↓
9. Job completes
```

### Stage Execution

Stages execute in reverse topological order (deepest dependencies first):

```python
# Example agent hierarchy
root_agent
  ├─ agent_a
  │   └─ agent_c
  └─ agent_b
      └─ agent_c

# Stages (reverse topological order)
Stage 2: [agent_c]           # Deepest, no dependencies
Stage 1: [agent_a, agent_b]  # Depend on agent_c (parallel)
Stage 0: [root_agent]        # Depends on agent_a and agent_b
```

### Task Execution

Within an executor:

```
1. Receive task from scheduler
   ↓
2. Get/load agent definition
   ↓
3. Setup execution context
   ↓
4. Execute agent with input
   ↓
5. Collect output and metrics
   ↓
6. Send result to scheduler
```

## Fault Tolerance

### Multi-Level Fault Tolerance

#### 1. Task-Level Recovery

**Automatic Retry**:
- Tasks automatically retry on failure
- Configurable max retries (default: 3)
- Exponential backoff between retries

**Lineage-Based Recomputation**:
- On failure, recompute from parent tasks
- No need to rerun entire job
- Efficient recovery for transient failures

#### 2. Executor-Level Recovery

**Executor Failure Detection**:
- Heartbeat mechanism detects failed executors
- Health tracker maintains exclusion list
- Automatic task reassignment to healthy executors

**Graceful Degradation**:
- System continues with remaining executors
- Tasks redistribute across healthy workers
- Configurable failure thresholds

#### 3. Stage-Level Recovery

**Stage Retry**:
- Failed stages retry from checkpoint
- Dependent stages wait for completion
- Stage boundaries provide natural recovery points

**Checkpoint-Based Recovery**:
- Checkpoint state at stage boundaries
- Truncate lineage to prevent long recomputation
- Reliable storage for durability

### Failure Scenarios

| Failure Type | Detection | Recovery Strategy |
|--------------|-----------|-------------------|
| Task timeout | Executor timeout | Retry on same/different executor |
| Executor crash | Heartbeat miss | Rerun all tasks on executor |
| Stage failure | All tasks fail | Retry stage from checkpoint |
| Network partition | RPC timeout | Retry with backoff |
| Data loss | Checksum/missing | Recompute from lineage |

## Scheduling Strategy

### Locality-Aware Scheduling

The scheduler prioritizes data locality to minimize network transfer and maximize cache hits.

**Locality Algorithm**:
```go
for each locality_level in [PROCESS_LOCAL, NODE_LOCAL, RACK_LOCAL, ANY]:
    if time_since_submission < maxLocalityWaitTime:
        if locality_level == RACK_LOCAL or NODE_LOCAL:
            continue  // Wait for better locality

    executor = find_executor_for_locality(task, locality_level)
    if executor != nil:
        launch_task(executor, task)
        return
```

**Delay Scheduling**:
- Wait up to 3 seconds for PROCESS_LOCAL placement
- Prevents head-of-line blocking
- Balances locality with latency

### Fair Scheduling

**FIFO Mode** (default):
- First-in-first-out job scheduling
- Simple and predictable
- Good for batch workloads

**Fair Mode** (optional):
- Time-share resources across jobs
- Prevents large jobs from monopolizing cluster
- Good for multi-tenant scenarios

### Resource Allocation

**Static Allocation**:
- Fixed number of executors
- Simple configuration
- Predictable resource usage

**Dynamic Allocation** (future):
- Add executors based on pending tasks
- Remove idle executors
- Optimal resource utilization

## Communication Layer

### Protocol Buffers Schema

**Location**: `pkg/rpc/protocol.proto`

Defines messages for distributed communication:
- `RegisterExecutor`: Executor registration
- `LaunchTask`: Task assignment
- `TaskStatusUpdate`: Task completion/failure
- `Heartbeat`: Health signals
- `SubmitJob`: Job submission
- `GetJobStatus`: Status queries

### RPC Patterns

**One-Way Messages**:
- Fire-and-forget for async operations
- Examples: LaunchTask, Heartbeat

**Request-Response**:
- Synchronous queries
- Examples: SubmitJob, GetJobStatus

**Event-Driven**:
- Async event processing
- Examples: TaskCompleted, StageFailed

### Future: Distributed RPC

**Current**: In-memory (single-process)
**Planned**: gRPC-based distribution
- Netty-based async communication
- Connection pooling
- TLS encryption
- Authentication

## State Management

### Checkpoint Types

**Reliable Checkpointing**:
- Saved to distributed storage (HDFS, S3)
- Survives executor/node failures
- Higher latency but more durable

**Local Checkpointing**:
- Saved to executor local storage
- Faster but less durable
- Good for transient failures

### Lineage vs. Checkpointing Trade-off

**Short Lineage**:
- Fast recomputation
- No checkpointing needed
- Low storage overhead

**Long Lineage**:
- Slow recomputation
- Checkpoint periodically
- Higher storage, faster recovery

**Strategy**:
- Checkpoint every N stages (configurable)
- Truncate lineage after checkpoint
- Balance storage vs. recomputation cost

## Comparison with Spark

### Similarities

| Concept | Spark | Spark AI Agents |
|---------|-------|-----------------|
| Core Abstraction | RDD | Agent |
| High-Level Scheduler | DAGScheduler | Agent Orchestrator |
| Low-Level Scheduler | TaskScheduler | Task Scheduler |
| Worker Process | Executor | Agent Worker |
| Fault Recovery | Lineage | Execution Lineage |
| State Persistence | Checkpoint | Checkpoint |
| Locality | Data Locality | Agent/State Locality |
| Parallelism | Partitions | Stages |

### Differences

| Aspect | Spark | Spark AI Agents |
|--------|-------|-----------------|
| Computation | Data transformations | AI agent execution |
| Dependencies | Data dependencies | Agent dependencies |
| Shuffle | Data redistribution | Agent communication |
| Caching | RDD data | Agent state |
| Input | Datasets | Instructions/prompts |
| Output | Transformed data | Agent outputs |

### Why Spark's Model Fits AI Agents

1. **DAG Workflows**: AI agent tasks naturally form DAGs
2. **Fault Tolerance**: Long-running AI tasks need resilience
3. **Resource Management**: Expensive AI models benefit from efficient scheduling
4. **Scalability**: Complex workflows require distributed execution
5. **Composability**: Agents compose hierarchically like RDD transformations

## Implementation Notes

### Language Choice: Go

**Why Go**:
- High performance (compiled, low overhead)
- Excellent concurrency (goroutines, channels)
- Strong typing for reliability
- Easy C interop for Python bindings
- Growing AI/ML ecosystem

### Python Bindings via CGO

**Architecture**:
```
Python API → ctypes → CGO → Go Core
```

**Advantages**:
- Python's ease of use
- Go's performance
- Single binary deployment
- No separate processes

### Future Improvements

1. **Full gRPC Distribution**: Multi-node deployment
2. **Advanced Checkpointing**: S3, HDFS, distributed file systems
3. **UI Dashboard**: Like Spark UI for monitoring
4. **Kubernetes Integration**: Native K8s executor management
5. **GPU Support**: Locality-aware GPU scheduling
6. **Streaming Agents**: Continuous agent execution
7. **Cost Optimization**: Model selection, caching strategies

## References

1. [Apache Spark Architecture](https://spark.apache.org/docs/latest/)
2. [Spark DAGScheduler](https://github.com/apache/spark/blob/master/core/src/main/scala/org/apache/spark/scheduler/DAGScheduler.scala)
3. [Resilient Distributed Datasets Paper](https://www.usenix.org/system/files/conference/nsdi12/nsdi12-final138.pdf)
4. [Zen MCP Server](https://github.com/seyi/zen-mcp-server)
5. [ADK Python](https://github.com/seyi/adk-python)
