# Scaling and Dispatch Clarification

This document answers three architecture questions with code-backed evidence:

1. Is etcd required for production scale?
2. Can API server static registry mode run indefinitely?
3. Is scheduling push-style or pull-style?

## TL;DR

- **etcd is not strictly required for basic runtime**, but it is the mechanism for **true dynamic cluster awareness** (registration, health, watch/reconnect).
- **Static registry mode can run indefinitely** as a fixed-topology deployment.
- **Dispatch is push-style**: scheduler selects nodes and sends execution RPCs; workers do not pull from a central job queue.

## 1) etcd and Production Scaling

In `cmd/api_server`, registry mode is selected by `ETCD_ENDPOINTS`.

Code reference: [cmd/api_server/main.go](/data/repos/dagens/cmd/api_server/main.go:55)

```go
etcdEndpoints := os.Getenv("ETCD_ENDPOINTS")
if etcdEndpoints != "" {
    distReg, err := registry.NewDistributedAgentRegistry(...)
    ...
    if err := distReg.Start(context.Background()); err != nil { ... }
    reg = distReg
} else {
    reg = &MockRegistry{nodes: []registry.NodeInfo{...}}
}
```

Distributed registry behavior includes:

- node registration
- cluster watch loop
- session lifecycle management/reconnect
- health-filtered node view

Code references:

- [pkg/registry/distributed_registry.go](/data/repos/dagens/pkg/registry/distributed_registry.go:141)
- [pkg/registry/distributed_registry.go](/data/repos/dagens/pkg/registry/distributed_registry.go:190)
- [pkg/registry/distributed_registry.go](/data/repos/dagens/pkg/registry/distributed_registry.go:409)

```go
func (r *DistributedAgentRegistry) Start(ctx context.Context) error {
    if err := r.registerNode(ctx); err != nil { ... }
    go r.sessionManagerLoop(ctx)
    go r.watchClusterChanges(ctx)
}

func (r *DistributedAgentRegistry) GetHealthyNodes() []NodeInfo {
    for _, node := range r.nodes {
        if node.Healthy { ... }
    }
}
```

Interpretation:

- For fixed, manually controlled topologies, static mode is viable.
- For elastic/failure-aware orchestration across changing nodes, etcd-backed registry is the intended path.

## 2) Static Registry Mode Indefinitely?

Yes. The API server has an explicit static `MockRegistry` fallback with predefined workers.

Code reference: [cmd/api_server/main.go](/data/repos/dagens/cmd/api_server/main.go:79)

```go
log.Println("Using Mock Registry (standalone mode)")
reg = &MockRegistry{
    nodes: []registry.NodeInfo{
        {ID: "worker-1", Address: "worker-1", Port: 50051, Healthy: true},
        {ID: "worker-2", Address: "worker-2", Port: 50051, Healthy: true},
    },
}
```

This means:

- It can continue operating without etcd.
- Topology is static/manual; dynamic discovery and distributed coordination are not active.

## 3) Push vs Pull Dispatch

Scheduling is **push-style**.

The scheduler owns a central in-process job queue and then dispatches tasks to selected nodes via `ExecuteOnNode`.

Code references:

- [pkg/scheduler/scheduler.go](/data/repos/dagens/pkg/scheduler/scheduler.go:27)
- [pkg/scheduler/scheduler.go](/data/repos/dagens/pkg/scheduler/scheduler.go:85)
- [pkg/scheduler/scheduler.go](/data/repos/dagens/pkg/scheduler/scheduler.go:136)
- [pkg/scheduler/scheduler.go](/data/repos/dagens/pkg/scheduler/scheduler.go:262)

```go
type Scheduler struct {
    jobQueue chan *Job
    ...
}

func (s *Scheduler) SubmitJob(job *Job) error {
    s.jobQueue <- job
}

func (s *Scheduler) run() {
    for {
        case job := <-s.jobQueue:
            s.executeJob(job)
    }
}

output, err := s.executor.ExecuteOnNode(taskCtx, node.ID, task.AgentName, task.Input)
```

Remote execution path confirms active RPC invocation to workers:

Code reference: [pkg/remote/remote_execution.go](/data/repos/dagens/pkg/remote/remote_execution.go:181)

```go
func (re *RemoteExecutor) ExecuteOnNode(...) {
    node, _ := re.registry.GetNode(nodeID)
    conn, _ := re.connPool.GetConnection(addr)
    output, err := re.ExecuteRemote(execCtx, request, conn)
}
```

Workers expose gRPC service endpoints and wait for incoming requests; they do not poll a job queue from API:

Code references:

- [cmd/worker/main.go](/data/repos/dagens/cmd/worker/main.go:103)
- [pkg/remote/grpc_remote_execution.go](/data/repos/dagens/pkg/remote/grpc_remote_execution.go:33)

```go
s := grpc.NewServer()
pb.RegisterRemoteExecutionServiceServer(s, grpcHandler)
s.Serve(lis)
```

## Recommended Positioning Language

For architecture docs/README:

- **Accurate today**: "Central push scheduler with optional distributed registry (etcd) for dynamic cluster awareness."
- **Static mode wording**: "Supports fixed-topology operation without etcd."
- **Cluster-aware wording**: "Becomes a true dynamic cluster-aware orchestrator when etcd-backed registry is enabled."
