// Package agents provides distributed AI agents with enhanced coordination capabilities.
// This file extends the LoadBalancerAgent with cluster awareness and distributed coordination.
package agents

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/seyi/dagens/pkg/agent"
	"github.com/seyi/dagens/pkg/registry"
	"github.com/seyi/dagens/pkg/remote"
)

// ClusterAwareLoadBalancerAgent extends LoadBalancerAgent with cluster awareness
type ClusterAwareLoadBalancerAgent struct {
	*LoadBalancerAgent
	registry           registry.Registry
	localAgentCount    int64
	remoteNodeCount    int64
	clusterCoordinator ClusterCoordinator
	remoteCounter      int64 // Dedicated counter for remote node selection
}

// ClusterAwareLoadBalancerConfig extends LoadBalancerAgentConfig with cluster settings
type ClusterAwareLoadBalancerConfig struct {
	LoadBalancerAgentConfig
	EnableClusterAwareness bool
	Registry              registry.Registry
	RemoteExecutor        *remote.RemoteExecutor
	ClusterStrategy       ClusterLoadBalanceStrategy
	MaxRemoteRetries      int
	RemoteTimeout         time.Duration
}

// ClusterLoadBalanceStrategy defines how to distribute load across the cluster
type ClusterLoadBalanceStrategy string

const (
	// ClusterLocalFirst prioritizes local agents, falls back to remote
	ClusterLocalFirst ClusterLoadBalanceStrategy = "local-first"

	// ClusterGlobal distributes across all nodes globally
	ClusterGlobal ClusterLoadBalanceStrategy = "global"

	// ClusterAffinity routes to nodes with specific capabilities
	ClusterAffinity ClusterLoadBalanceStrategy = "affinity"
)

// NewClusterAwareLoadBalancerAgent creates a cluster-aware load balancer agent
func NewClusterAwareLoadBalancerAgent(config ClusterAwareLoadBalancerConfig) *ClusterAwareLoadBalancerAgent {
	// Create base load balancer agent
	baseLBAgent := NewLoadBalancerAgent(config.LoadBalancerAgentConfig)

	clusterAgent := &ClusterAwareLoadBalancerAgent{
		LoadBalancerAgent: baseLBAgent,
		registry:          config.Registry,
		localAgentCount:   int64(len(config.Agents)),
		remoteNodeCount:   0,
	}

	if config.EnableClusterAwareness && config.Registry != nil && config.RemoteExecutor != nil {
		clusterAgent.clusterCoordinator = NewDefaultClusterCoordinator(ClusterCoordinatorConfig{
			Registry:        config.Registry,
			RemoteExecutor:  config.RemoteExecutor,
			LocalNodeID:     config.Registry.GetNodeID(),
			MaxRemoteRetries: config.MaxRemoteRetries,
			RemoteTimeout:    config.RemoteTimeout,
			Strategy:         config.ClusterStrategy,
		})
	}

	return clusterAgent
}

// Execute implements the Agent interface with cluster awareness
func (c *ClusterAwareLoadBalancerAgent) Execute(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	if !c.isClusterAware() {
		// Fall back to local load balancing if cluster awareness is disabled
		return c.LoadBalancerAgent.Execute(ctx, input)
	}

	// Determine if we should route locally or remotely based on strategy
	switch c.clusterCoordinator.GetStrategy() {
	case ClusterLocalFirst:
		return c.executeLocalFirst(ctx, input)
	case ClusterGlobal:
		return c.executeGlobal(ctx, input)
	case ClusterAffinity:
		return c.executeAffinity(ctx, input)
	default:
		return c.executeLocalFirst(ctx, input)
	}
}

// executeLocalFirst tries local agents first, then falls back to remote
func (c *ClusterAwareLoadBalancerAgent) executeLocalFirst(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// Try local execution first
	localOutput, localErr := c.LoadBalancerAgent.Execute(ctx, input)
	if localErr == nil {
		return localOutput, nil
	}

	// If local execution fails, try remote execution
	return c.executeOnRemoteNode(ctx, input)
}

// executeGlobal distributes across local and remote nodes globally
func (c *ClusterAwareLoadBalancerAgent) executeGlobal(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// Get cluster state
	nodes := c.registry.GetHealthyNodes()
	localNodeID := c.clusterCoordinator.GetLocalNodeID()

	// Calculate total capacity (local + remote)
	totalCapacity := int(c.localAgentCount)
	for _, node := range nodes {
		if node.ID != localNodeID {
			totalCapacity += 1 // Assume each remote node adds at least 1 unit of capacity
		}
	}

	if totalCapacity == 0 {
		return c.LoadBalancerAgent.Execute(ctx, input)
	}

	// Use round-robin across total capacity
	totalIndex := c.counter.Add(1)
	
	if int(totalIndex)%totalCapacity < int(c.localAgentCount) {
		// Execute locally
		return c.LoadBalancerAgent.Execute(ctx, input)
	} else {
		// Execute remotely
		return c.executeOnRemoteNode(ctx, input)
	}
}

// executeAffinity routes to nodes with specific capabilities
func (c *ClusterAwareLoadBalancerAgent) executeAffinity(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	// Extract required capabilities from input context
	requiredCaps, _ := input.Context["required_capabilities"].([]string)
	if len(requiredCaps) == 0 {
		// If no specific capabilities required, fall back to local-first
		return c.executeLocalFirst(ctx, input)
	}

	// Find nodes that have the required capabilities
	nodes := c.registry.GetNodesByCapability(requiredCaps[0]) // Use first capability as routing hint
	localNodeID := c.clusterCoordinator.GetLocalNodeID()

	// Check if local node has the capability
	hasLocalCapability := false
	for _, cap := range c.LoadBalancerAgent.Capabilities() {
		for _, requiredCap := range requiredCaps {
			if cap == requiredCap {
				hasLocalCapability = true
				break
			}
		}
		if hasLocalCapability {
			break
		}
	}

	if hasLocalCapability {
		// Try local execution first
		localOutput, localErr := c.LoadBalancerAgent.Execute(ctx, input)
		if localErr == nil {
			return localOutput, nil
		}
	}

	// If local doesn't have capability or failed, try remote nodes with capability
	for _, node := range nodes {
		if node.ID != localNodeID {
			output, err := c.executeOnRemoteNodeWithID(ctx, input, node.ID)
			if err == nil {
				return output, nil
			}
		}
	}

	// If no nodes with capability found, fall back to any remote node
	return c.executeOnRemoteNode(ctx, input)
}

// executeOnRemoteNode executes on a remote node in the cluster
func (c *ClusterAwareLoadBalancerAgent) executeOnRemoteNode(ctx context.Context, input *agent.AgentInput) (*agent.AgentOutput, error) {
	if c.clusterCoordinator == nil {
		return nil, fmt.Errorf("cluster coordinator not initialized")
	}

	// Get available remote nodes
	nodes := c.registry.GetHealthyNodes()
	localNodeID := c.clusterCoordinator.GetLocalNodeID()

	// Filter out local node
	remoteNodes := make([]registry.NodeInfo, 0)
	for _, node := range nodes {
		if node.ID != localNodeID {
			remoteNodes = append(remoteNodes, node)
		}
	}

	if len(remoteNodes) == 0 {
		// No remote nodes available, fall back to local
		return c.LoadBalancerAgent.Execute(ctx, input)
	}

	// Select a remote node (round-robin for now)
	// Use dedicated remoteCounter to avoid syncing with global counter
	nodeIndex := int(atomic.AddInt64(&c.remoteCounter, 1)) % len(remoteNodes)
	selectedNode := remoteNodes[nodeIndex]

	// Execute on remote node
	return c.clusterCoordinator.ExecuteOnNode(ctx, selectedNode.ID, input)
}

// executeOnRemoteNodeWithID executes on a specific remote node
func (c *ClusterAwareLoadBalancerAgent) executeOnRemoteNodeWithID(ctx context.Context, input *agent.AgentInput, nodeID string) (*agent.AgentOutput, error) {
	if c.clusterCoordinator == nil {
		return nil, fmt.Errorf("cluster coordinator not initialized")
	}

	return c.clusterCoordinator.ExecuteOnNode(ctx, nodeID, input)
}

// isClusterAware checks if cluster awareness is enabled
func (c *ClusterAwareLoadBalancerAgent) isClusterAware() bool {
	return c.clusterCoordinator != nil
}

// GetClusterStats returns cluster-aware statistics
func (c *ClusterAwareLoadBalancerAgent) GetClusterStats() ClusterStats {
	localStats := c.LoadBalancerAgent.GetAgentStats()
	
	clusterStats := ClusterStats{
		LocalAgentStats: localStats,
		TotalNodes:      c.registry.GetNodeCount(),
		HealthyNodes:    c.registry.GetHealthyNodeCount(),
		LocalAgentCount: len(localStats),
	}

	if c.clusterCoordinator != nil {
		clusterStats.CoordinatorStats = c.clusterCoordinator.GetStats()
	}

	return clusterStats
}

// ClusterStats holds cluster-aware statistics
type ClusterStats struct {
	LocalAgentStats []AgentStats
	TotalNodes      int
	HealthyNodes    int
	LocalAgentCount int
	CoordinatorStats CoordinatorStats
}

// ClusterCoordinator interface manages remote execution
type ClusterCoordinator interface {
	ExecuteOnNode(ctx context.Context, nodeID string, input *agent.AgentInput) (*agent.AgentOutput, error)
	GetStats() CoordinatorStats
	GetLocalNodeID() string
	GetStrategy() ClusterLoadBalanceStrategy
	SetStrategy(strategy ClusterLoadBalanceStrategy)
}

// DefaultClusterCoordinator implements ClusterCoordinator
type DefaultClusterCoordinator struct {
	registry       registry.Registry
	remoteExecutor *remote.RemoteExecutor
	localNodeID    string
	maxRemoteRetries int
	remoteTimeout  time.Duration
	strategy       ClusterLoadBalanceStrategy
	stats          CoordinatorStats
}

// CoordinatorStats holds coordinator statistics
type CoordinatorStats struct {
	RemoteExecutions    int64
	RemoteErrors        int64
	RemoteAvgLatency    time.Duration
	LastRemoteExecution time.Time
}

// ClusterCoordinatorConfig configures the cluster coordinator
type ClusterCoordinatorConfig struct {
	Registry        registry.Registry
	RemoteExecutor  *remote.RemoteExecutor
	LocalNodeID     string
	MaxRemoteRetries int
	RemoteTimeout   time.Duration
	Strategy        ClusterLoadBalanceStrategy
}

// NewDefaultClusterCoordinator creates a new default cluster coordinator
func NewDefaultClusterCoordinator(config ClusterCoordinatorConfig) *DefaultClusterCoordinator {
	if config.MaxRemoteRetries == 0 {
		config.MaxRemoteRetries = 3
	}
	if config.RemoteTimeout == 0 {
		config.RemoteTimeout = 30 * time.Second
	}
	if config.Strategy == "" {
		config.Strategy = ClusterLocalFirst
	}

	return &DefaultClusterCoordinator{
		registry:       config.Registry,
		remoteExecutor: config.RemoteExecutor,
		localNodeID:    config.LocalNodeID,
		maxRemoteRetries: config.MaxRemoteRetries,
		remoteTimeout:  config.RemoteTimeout,
		strategy:       config.Strategy,
	}
}

// ExecuteOnNode executes an agent input on a remote node
func (cc *DefaultClusterCoordinator) ExecuteOnNode(ctx context.Context, nodeID string, input *agent.AgentInput) (*agent.AgentOutput, error) {
	if cc.remoteExecutor == nil {
		return nil, fmt.Errorf("remote executor not available")
	}

	startTime := time.Now()
	
	// Use the real RemoteExecutor to perform the call via gRPC
	// We check context for agent name hint
	agentName, _ := input.Context["agent_name"].(string)
	if agentName == "" {
		agentName = "generic-agent" // Default
	}

	output, err := cc.remoteExecutor.ExecuteOnNode(ctx, nodeID, agentName, input)
	
	duration := time.Since(startTime)
	atomic.AddInt64(&cc.stats.RemoteExecutions, 1)
	cc.stats.LastRemoteExecution = time.Now()

	if err != nil {
		atomic.AddInt64(&cc.stats.RemoteErrors, 1)
		return nil, fmt.Errorf("remote execution on node %s failed: %w", nodeID, err)
	}

	// Update average latency (simplified moving average)
	oldLatency := cc.stats.RemoteAvgLatency
	if oldLatency == 0 {
		cc.stats.RemoteAvgLatency = duration
	} else {
		cc.stats.RemoteAvgLatency = (oldLatency + duration) / 2
	}

	return output, nil
}

// GetStats returns coordinator statistics
func (cc *DefaultClusterCoordinator) GetStats() CoordinatorStats {
	return cc.stats
}

func (cc *DefaultClusterCoordinator) GetLocalNodeID() string {
	return cc.localNodeID
}

func (cc *DefaultClusterCoordinator) GetStrategy() ClusterLoadBalanceStrategy {
	return cc.strategy
}

func (cc *DefaultClusterCoordinator) SetStrategy(strategy ClusterLoadBalanceStrategy) {
	cc.strategy = strategy
}

// GetNodeID returns the local node ID
func (c *ClusterAwareLoadBalancerAgent) GetNodeID() string {
	if c.clusterCoordinator != nil {
		return c.clusterCoordinator.GetLocalNodeID()
	}
	return ""
}

// GetRegistry returns the registry
func (c *ClusterAwareLoadBalancerAgent) GetRegistry() registry.Registry {
	return c.registry
}

// UpdateRemoteNodeCount updates the count of remote nodes
func (c *ClusterAwareLoadBalancerAgent) UpdateRemoteNodeCount() {
	if c.registry != nil {
		nodes := c.registry.GetNodes()
		localCount := int(c.localAgentCount)
		totalCount := len(nodes)
		remoteCount := totalCount - localCount
		if remoteCount < 0 {
			remoteCount = 0 // Safety check
		}
		atomic.StoreInt64(&c.remoteNodeCount, int64(remoteCount))
	}
}

// GetRemoteNodeCount returns the count of remote nodes
func (c *ClusterAwareLoadBalancerAgent) GetRemoteNodeCount() int64 {
	return atomic.LoadInt64(&c.remoteNodeCount)
}

// GetLocalAgentCount returns the count of local agents
func (c *ClusterAwareLoadBalancerAgent) GetLocalAgentCount() int64 {
	return c.localAgentCount
}

// SetClusterStrategy updates the cluster load balancing strategy
func (c *ClusterAwareLoadBalancerAgent) SetClusterStrategy(strategy ClusterLoadBalanceStrategy) {
	if c.clusterCoordinator != nil {
		c.clusterCoordinator.SetStrategy(strategy)
	}
}

// GetClusterStrategy returns the current cluster load balancing strategy
func (c *ClusterAwareLoadBalancerAgent) GetClusterStrategy() ClusterLoadBalanceStrategy {
	if c.clusterCoordinator != nil {
		return c.clusterCoordinator.GetStrategy()
	}
	return ClusterLocalFirst
}