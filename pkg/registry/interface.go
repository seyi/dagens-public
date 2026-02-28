package registry

import "context"

// Registry is an interface wrapper for DistributedAgentRegistry to facilitate mocking/testing
type Registry interface {
	GetHealthyNodes() []NodeInfo
	GetNode(nodeID string) (NodeInfo, bool)
	GetNodes() []NodeInfo
	GetNodeID() string
	GetNodesByCapability(capability string) []NodeInfo
	GetNodeCount() int
	GetHealthyNodeCount() int
	Start(ctx context.Context) error
	Stop() error
}