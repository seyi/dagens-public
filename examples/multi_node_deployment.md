# Multi-Node Dagens Deployment Example

## Overview
This document provides an example of how to set up a multi-node Dagens deployment with distributed coordination capabilities. This addresses the distributed coordination gap identified in the scale readiness assessment.

## Architecture

### Components
- **Agent Registry**: etcd-based registry for discovering agents across nodes
- **Load Balancer**: Cluster-aware load balancer that can route to agents on any node
- **State Management**: Distributed state management using Redis
- **Message Queue**: RabbitMQ/Redis Streams for inter-node communication
- **Observability**: Centralized monitoring and tracing

```
┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   Node 1        │    │   Node 2        │    │   Node 3        │
│                 │    │                 │    │                 │
│ ┌─────────────┐ │    │ ┌─────────────┐ │    │ ┌─────────────┐ │
│ │ Agent Pool  │ │    │ │ Agent Pool  │ │    │ │ Agent Pool  │ │
│ │ (5 agents)  │ │    │ │ (5 agents)  │ │    │ │ (5 agents)  │ │
│ └─────────────┘ │    │ └─────────────┘ │    │ └─────────────┘ │
│                 │    │                 │    │                 │
│ ┌─────────────┐ │    │ ┌─────────────┐ │    │ ┌─────────────┐ │
│ │ Load Balancer││◄───┼─┤ Load Balancer││◄───┼─┤ Load Balancer││
│ │ (Cluster-Aware)│    │ │ (Cluster-Aware)│    │ │ (Cluster-Aware)│
│ └─────────────┘ │    │ └─────────────┘ │    │ └─────────────┘ │
└─────────────────┘    └─────────────────┘    └─────────────────┘
         │                       │                       │
         └───────────────────────┼───────────────────────┘
                                 │
                    ┌─────────────────────────┐
                    │   Shared Infrastructure│
                    │                        │
                    │ • etcd (Agent Registry)│
                    │ • Redis (State)        │
                    │ • RabbitMQ (Queue)     │
                    │ • Jaeger (Tracing)     │
                    │ • Prometheus (Metrics) │
                    └─────────────────────────┘
```

## Implementation

### 1. Agent Registry Service

#### etcd-based Agent Registry
```go
// pkg/registry/agent_registry.go
package registry

import (
    "context"
    "encoding/json"
    "fmt"
    "time"

    clientv3 "go.etcd.io/etcd/client/v3"
    "go.uber.org/zap"
)

type AgentInfo struct {
    ID          string            `json:"id"`
    Name        string            `json:"name"`
    Address     string            `json:"address"`
    Port        int               `json:"port"`
    Capabilities []string         `json:"capabilities"`
    Status      string            `json:"status"` // healthy, unhealthy, draining
    LastSeen    time.Time         `json:"last_seen"`
    Metadata    map[string]string `json:"metadata"`
}

type AgentRegistry struct {
    client *clientv3.Client
    logger *zap.Logger
    ttl    int64 // TTL in seconds for agent registration
}

func NewAgentRegistry(endpoints []string, logger *zap.Logger) (*AgentRegistry, error) {
    client, err := clientv3.New(clientv3.Config{
        Endpoints:   endpoints,
        DialTimeout: 5 * time.Second,
    })
    if err != nil {
        return nil, fmt.Errorf("failed to create etcd client: %w", err)
    }

    return &AgentRegistry{
        client: client,
        logger: logger,
        ttl:    30, // 30 second TTL for agent health checks
    }, nil
}

func (r *AgentRegistry) RegisterAgent(ctx context.Context, agentInfo AgentInfo) error {
    agentInfo.LastSeen = time.Now()
    
    key := fmt.Sprintf("/dagens/agents/%s", agentInfo.ID)
    value, err := json.Marshal(agentInfo)
    if err != nil {
        return fmt.Errorf("failed to marshal agent info: %w", err)
    }

    lease, err := r.client.Grant(ctx, r.ttl)
    if err != nil {
        return fmt.Errorf("failed to create lease: %w", err)
    }

    _, err = r.client.Put(ctx, key, string(value), clientv3.WithLease(lease.ID))
    if err != nil {
        return fmt.Errorf("failed to register agent: %w", err)
    }

    // Keep the lease alive
    r.client.KeepAlive(ctx, lease.ID)

    r.logger.Info("Agent registered", 
        zap.String("agent_id", agentInfo.ID),
        zap.String("address", agentInfo.Address))

    return nil
}

func (r *AgentRegistry) DeregisterAgent(ctx context.Context, agentID string) error {
    key := fmt.Sprintf("/dagens/agents/%s", agentID)
    
    _, err := r.client.Delete(ctx, key)
    if err != nil {
        return fmt.Errorf("failed to deregister agent: %w", err)
    }

    r.logger.Info("Agent deregistered", zap.String("agent_id", agentID))
    return nil
}

func (r *AgentRegistry) GetAgent(ctx context.Context, agentID string) (*AgentInfo, error) {
    key := fmt.Sprintf("/dagens/agents/%s", agentID)
    
    resp, err := r.client.Get(ctx, key)
    if err != nil {
        return nil, fmt.Errorf("failed to get agent: %w", err)
    }

    if len(resp.Kvs) == 0 {
        return nil, fmt.Errorf("agent not found: %s", agentID)
    }

    var agentInfo AgentInfo
    if err := json.Unmarshal(resp.Kvs[0].Value, &agentInfo); err != nil {
        return nil, fmt.Errorf("failed to unmarshal agent info: %w", err)
    }

    return &agentInfo, nil
}

func (r *AgentRegistry) ListAgents(ctx context.Context) ([]AgentInfo, error) {
    resp, err := r.client.Get(ctx, "/dagens/agents/", clientv3.WithPrefix())
    if err != nil {
        return nil, fmt.Errorf("failed to list agents: %w", err)
    }

    agents := make([]AgentInfo, 0, len(resp.Kvs))
    for _, kv := range resp.Kvs {
        var agentInfo AgentInfo
        if err := json.Unmarshal(kv.Value, &agentInfo); err != nil {
            r.logger.Error("Failed to unmarshal agent info", 
                zap.String("key", string(kv.Key)), 
                zap.Error(err))
            continue
        }
        agents = append(agents, agentInfo)
    }

    return agents, nil
}

func (r *AgentRegistry) WatchAgents(ctx context.Context) clientv3.WatchChan {
    return r.client.Watch(ctx, "/dagens/agents/", clientv3.WithPrefix())
}
```

### 2. Cluster-Aware Load Balancer

#### Distributed Load Balancer Agent
```go
// pkg/agents/cluster_loadbalancer.go
package agents

import (
    "context"
    "fmt"
    "math/rand"
    "sync/atomic"
    "time"

    "github.com/seyi/dagens/pkg/agent"
    "github.com/seyi/dagens/pkg/registry"
)

type ClusterLoadBalancerAgent struct {
    *agent.BaseAgent
    registry       *registry.AgentRegistry
    strategy       LoadBalanceStrategy
    counter        int64
    lastAgentList  []registry.AgentInfo
    lastUpdate     time.Time
    updateInterval time.Duration
}

type ClusterLoadBalancerConfig struct {
    Name           string
    Strategy       LoadBalanceStrategy
    Registry       *registry.AgentRegistry
    UpdateInterval time.Duration
    Dependencies   []agent.Agent
}

func NewClusterLoadBalancerAgent(config ClusterLoadBalancerConfig) *ClusterLoadBalancerAgent {
    if config.UpdateInterval == 0 {
        config.UpdateInterval = 30 * time.Second
    }

    lbAgent := &ClusterLoadBalancerAgent{
        registry:       config.Registry,
        strategy:       config.Strategy,
        updateInterval: config.UpdateInterval,
    }

    executor := &clusterLoadBalancerExecutor{
        lbAgent: lbAgent,
    }

    baseAgent := agent.NewAgent(agent.AgentConfig{
        Name:         config.Name,
        Executor:     executor,
        Dependencies: config.Dependencies,
    })

    lbAgent.BaseAgent = baseAgent
    return lbAgent
}

type clusterLoadBalancerExecutor struct {
    lbAgent *ClusterLoadBalancerAgent
}

func (e *clusterLoadBalancerExecutor) Execute(ctx context.Context, _ agent.Agent, input *agent.AgentInput) (*agent.AgentOutput, error) {
    // Get available agents from registry
    agents, err := e.lbAgent.getAvailableAgents(ctx)
    if err != nil {
        return nil, fmt.Errorf("failed to get available agents: %w", err)
    }

    if len(agents) == 0 {
        return nil, fmt.Errorf("no available agents in cluster")
    }

    // Select agent based on strategy
    selectedAgent, err := e.selectAgent(agents)
    if err != nil {
        return nil, fmt.Errorf("failed to select agent: %w", err)
    }

    // Execute on remote agent
    // This would involve making an HTTP/gRPC call to the selected agent
    output, err := e.executeOnRemoteAgent(ctx, selectedAgent, input)
    if err != nil {
        return nil, fmt.Errorf("failed to execute on remote agent: %w", err)
    }

    // Add cluster routing metadata
    if output.Metadata == nil {
        output.Metadata = make(map[string]interface{})
    }
    output.Metadata["pattern"] = "ClusterLoadBalancer"
    output.Metadata["selected_agent"] = selectedAgent.ID
    output.Metadata["selected_node"] = selectedAgent.Address
    output.Metadata["strategy"] = string(e.lbAgent.strategy)

    return output, nil
}

func (e *clusterLoadBalancerExecutor) getAvailableAgents(ctx context.Context) ([]registry.AgentInfo, error) {
    // Update agent list if needed
    if time.Since(e.lbAgent.lastUpdate) > e.lbAgent.updateInterval {
        agents, err := e.lbAgent.registry.ListAgents(ctx)
        if err != nil {
            return nil, err
        }
        
        // Filter healthy agents
        healthyAgents := make([]registry.AgentInfo, 0)
        for _, agent := range agents {
            if agent.Status == "healthy" {
                healthyAgents = append(healthyAgents, agent)
            }
        }
        
        e.lbAgent.lastAgentList = healthyAgents
        e.lbAgent.lastUpdate = time.Now()
    }

    return e.lbAgent.lastAgentList, nil
}

func (e *clusterLoadBalancerExecutor) selectAgent(agents []registry.AgentInfo) (registry.AgentInfo, error) {
    if len(agents) == 0 {
        return registry.AgentInfo{}, fmt.Errorf("no agents available")
    }

    var idx int
    switch e.lbAgent.strategy {
    case RoundRobin:
        counter := atomic.AddInt64(&e.lbAgent.counter, 1)
        idx = int(counter-1) % len(agents)
    case Random:
        idx = rand.Intn(len(agents))
    case LeastLoaded:
        // For now, just select randomly - in a real implementation,
        // this would query each agent's load metrics
        idx = rand.Intn(len(agents))
    default:
        return registry.AgentInfo{}, fmt.Errorf("unknown strategy: %s", e.lbAgent.strategy)
    }

    return agents[idx], nil
}

func (e *clusterLoadBalancerExecutor) executeOnRemoteAgent(ctx context.Context, agentInfo registry.AgentInfo, input *agent.AgentInput) (*agent.AgentOutput, error) {
    // This would implement the actual remote execution logic
    // using HTTP/gRPC to call the remote agent
    // For this example, we'll return a mock response
    return &agent.AgentOutput{
        Result: fmt.Sprintf("Executed on agent %s at %s:%d", agentInfo.Name, agentInfo.Address, agentInfo.Port),
    }, nil
}
```

### 3. Kubernetes Deployment for Multi-Node Setup

#### etcd Cluster Configuration
```yaml
# etcd-cluster.yaml
apiVersion: v1
kind: Service
metadata:
  name: etcd-client
  namespace: dagens
spec:
  ports:
  - name: etcd-client
    port: 2379
    protocol: TCP
    targetPort: 2379
  selector:
    app: etcd
---
apiVersion: v1
kind: Service
metadata:
  name: etcd-headless
  namespace: dagens
  annotations:
    service.alpha.kubernetes.io/tolerate-unready-endpoints: "true"
spec:
  ports:
  - port: 2380
    name: etcd-peer
  - port: 2379
    name: etcd-client
  clusterIP: None
  selector:
    app: etcd
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: etcd
  namespace: dagens
spec:
  serviceName: etcd-headless
  replicas: 3
  selector:
    matchLabels:
      app: etcd
  template:
    metadata:
      labels:
        app: etcd
    spec:
      containers:
      - name: etcd
        image: quay.io/coreos/etcd:v3.5.0
        command:
        - /usr/local/bin/etcd
        args:
        - --name
        - $(POD_NAME)
        - --data-dir
        - /var/lib/etcd
        - --advertise-client-urls
        - http://$(POD_IP):2379
        - --listen-client-urls
        - http://0.0.0.0:2379
        - --initial-advertise-peer-urls
        - http://$(POD_NAME).etcd-headless.dagens.svc.cluster.local:2380
        - --listen-peer-urls
        - http://0.0.0.0:2380
        - --initial-cluster
        - etcd-0=http://etcd-0.etcd-headless.dagens.svc.cluster.local:2380,etcd-1=http://etcd-1.etcd-headless.dagens.svc.cluster.local:2380,etcd-2=http://etcd-2.etcd-headless.dagens.svc.cluster.local:2380
        - --initial-cluster-token
        - dagens-etcd-cluster
        - --initial-cluster-state
        - new
        env:
        - name: POD_NAME
          valueFrom:
            fieldRef:
              fieldPath: metadata.name
        - name: POD_IP
          valueFrom:
            fieldRef:
              fieldPath: status.podIP
        ports:
        - containerPort: 2379
          name: client
        - containerPort: 2380
          name: peer
        volumeMounts:
        - name: etcd-data
          mountPath: /var/lib/etcd
        readinessProbe:
          httpGet:
            path: /health
            port: 2378
          initialDelaySeconds: 5
          timeoutSeconds: 5
      volumes:
      - name: etcd-data
        emptyDir: {}
```

#### Multi-Node Agent Deployment
```yaml
# cluster-agent-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: dagens-cluster-agent
  namespace: dagens
spec:
  replicas: 3
  selector:
    matchLabels:
      app: dagens-cluster-agent
  template:
    metadata:
      labels:
        app: dagens-cluster-agent
    spec:
      containers:
      - name: agent
        image: your-registry/dagens:latest
        command: ["/dagens", "cluster-agent"]
        ports:
        - containerPort: 8080
        env:
        - name: AGENT_REGISTRY_ENDPOINTS
          value: "etcd.dagens.svc.cluster.local:2379"
        - name: AGENT_ID
          valueFrom:
            fieldRef:
              fieldPath: metadata.name
        - name: AGENT_ADDRESS
          valueFrom:
            fieldRef:
              fieldPath: status.podIP
        - name: AGENT_PORT
          value: "8080"
        - name: OTEL_EXPORTER_OTLP_ENDPOINT
          value: "jaeger-collector.dagens:4317"
        resources:
          requests:
            memory: "256Mi"
            cpu: "200m"
          limits:
            memory: "512Mi"
            cpu: "500m"
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 30
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /ready
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 5
---
apiVersion: v1
kind: Service
metadata:
  name: dagens-cluster-agent-service
  namespace: dagens
spec:
  selector:
    app: dagens-cluster-agent
  ports:
    - protocol: TCP
      port: 8080
      targetPort: 8080
  type: ClusterIP
```

### 4. Example Usage

#### Initializing Cluster-Aware Agent
```go
// examples/cluster/main.go
package main

import (
    "context"
    "log"
    "time"

    "github.com/seyi/dagens/pkg/agents"
    "github.com/seyi/dagens/pkg/registry"
    "go.uber.org/zap"
)

func main() {
    // Initialize logger
    logger, _ := zap.NewProduction()
    defer logger.Sync()

    // Initialize etcd registry
    registry, err := registry.NewAgentRegistry(
        []string{"etcd.dagens.svc.cluster.local:2379"}, 
        logger,
    )
    if err != nil {
        log.Fatal("Failed to create registry: ", err)
    }

    // Register this agent with the cluster
    agentInfo := registry.AgentInfo{
        ID:          "agent-1",
        Name:        "example-agent",
        Address:     "10.0.0.1", // This would be the pod IP in Kubernetes
        Port:        8080,
        Capabilities: []string{"llm", "reasoning", "tool-use"},
        Status:      "healthy",
        Metadata:    map[string]string{"region": "us-west-1"},
    }

    ctx := context.Background()
    if err := registry.RegisterAgent(ctx, agentInfo); err != nil {
        log.Fatal("Failed to register agent: ", err)
    }

    // Create cluster-aware load balancer
    lb := agents.NewClusterLoadBalancerAgent(agents.ClusterLoadBalancerConfig{
        Name:     "cluster-lb",
        Strategy: agents.RoundRobin,
        Registry: registry,
    })

    // The load balancer will now route to agents across the cluster
    log.Println("Cluster-aware load balancer initialized")
    log.Println("Agents will be discovered from the registry")
    
    // Keep the agent running
    select {}
}
```

## Benefits of Multi-Node Deployment

### 1. High Availability
- Agents distributed across multiple nodes
- Failure of one node doesn't affect the entire system
- Automatic failover to healthy agents

### 2. Scalability
- Horizontal scaling by adding more nodes
- Load distribution across the cluster
- Resource utilization optimization

### 3. Fault Tolerance
- Agent registry maintains cluster state
- Automatic detection of node failures
- Graceful degradation when nodes are unavailable

### 4. Geographic Distribution
- Agents can be deployed in different regions
- Reduced latency for geographically distributed users
- Regional failover capabilities

## Next Steps

### Implementation Priority
1. **Agent Registry**: Implement etcd-based registry (completed in example)
2. **Cluster Load Balancer**: Implement cluster-aware load balancing (completed in example)
3. **Health Monitoring**: Add health checks and auto-healing
4. **Service Discovery**: Integrate with Kubernetes service discovery
5. **Load Metrics**: Implement actual load-based routing

This multi-node deployment example addresses the distributed coordination gap by providing a framework for cluster-aware agent management and load balancing across multiple nodes.