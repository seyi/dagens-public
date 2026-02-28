// Package registry provides distributed coordination and agent discovery
// using etcd for consistent shared state across the cluster.
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/coordination"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// NodeInfo represents information about a node in the cluster
type NodeInfo struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Address      string            `json:"address"`
	Port         int               `json:"port"`
	Capabilities []string          `json:"capabilities"`
	Metadata     map[string]string `json:"metadata"`
	LastSeen     time.Time         `json:"last_seen"`
	Load         float64           `json:"load"`
	Healthy      bool              `json:"healthy"`
}

// ServiceInfo represents information about a service running on a node
type ServiceInfo struct {
	NodeID      string    `json:"node_id"`
	ServiceName string    `json:"service_name"`
	Address     string    `json:"address"`
	Port        int       `json:"port"`
	LastSeen    time.Time `json:"last_seen"`
	Healthy     bool      `json:"healthy"`
}

// DistributedAgentRegistry provides distributed coordination and agent discovery
type DistributedAgentRegistry struct {
	config     RegistryConfig
	client     *clientv3.Client
	nodeID     string
	nodeInfo   NodeInfo
	session    *concurrency.Session
	leaseID    clientv3.LeaseID
	cancelFunc context.CancelFunc
	wg         sync.WaitGroup
	mu         sync.RWMutex
	nodes      map[string]NodeInfo
	services   map[string]ServiceInfo
	observers  []RegistryObserver
}

// RegistryObserver receives notifications about registry changes
type RegistryObserver interface {
	NodeAdded(node NodeInfo)
	NodeRemoved(nodeID string)
	ServiceRegistered(service ServiceInfo)
	ServiceUnregistered(serviceName string)
}

// RegistryConfig holds configuration for the distributed registry
type RegistryConfig struct {
	EtcdEndpoints      []string
	EtcdDialTimeout    time.Duration
	NodeID             string
	NodeName           string
	NodeAddress        string
	NodePort           int
	NodeCapabilities   []string
	NodeMetadata       map[string]string
	LeaseTTL           int64 // Lease TTL in seconds
	WatchTimeout       time.Duration
	HeartbeatInterval  time.Duration
}

// NewDistributedAgentRegistry creates a new distributed agent registry
func NewDistributedAgentRegistry(config RegistryConfig) (*DistributedAgentRegistry, error) {
	if config.EtcdDialTimeout == 0 {
		config.EtcdDialTimeout = 5 * time.Second
	}
	if config.LeaseTTL == 0 {
		config.LeaseTTL = 10 // 10 seconds default
	}
	if config.HeartbeatInterval == 0 {
		config.HeartbeatInterval = 3 * time.Second
	}
	if config.WatchTimeout == 0 {
		config.WatchTimeout = 30 * time.Second
	}

	// Create etcd client
	client, err := clientv3.New(clientv3.Config{
		Endpoints:   config.EtcdEndpoints,
		DialTimeout: config.EtcdDialTimeout,
		DialOptions: []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd client: %w", err)
	}

	// Create session with lease (Fail fast on startup)
	session, err := concurrency.NewSession(client, concurrency.WithTTL(int(config.LeaseTTL)))
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to create etcd session: %w", err)
	}

	// Create node info
	nodeInfo := NodeInfo{
		ID:           config.NodeID,
		Name:         config.NodeName,
		Address:      config.NodeAddress,
		Port:         config.NodePort,
		Capabilities: config.NodeCapabilities,
		Metadata:     config.NodeMetadata,
		LastSeen:     time.Now(),
		Load:         0.0, // Initial load
		Healthy:      true,
	}

	registry := &DistributedAgentRegistry{
		config:   config,
		client:   client,
		nodeID:   config.NodeID,
		nodeInfo: nodeInfo,
		session:  session,
		leaseID:  session.Lease(),
		nodes:    make(map[string]NodeInfo),
		services: make(map[string]ServiceInfo),
	}

	return registry, nil
}

// Start begins the registry operations including registration and watching
func (r *DistributedAgentRegistry) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	r.cancelFunc = cancel

	// Register this node (using the initial session)
	if err := r.registerNode(ctx); err != nil {
		return fmt.Errorf("failed to register node: %w", err)
	}

	// Start session management loop (Supervisor)
	r.wg.Add(1)
	go r.sessionManagerLoop(ctx)

	// Start watching for cluster changes
	r.wg.Add(1)
	go r.watchClusterChanges(ctx)

	return nil
}

// Stop stops the registry operations
func (r *DistributedAgentRegistry) Stop() error {
	if r.cancelFunc != nil {
		r.cancelFunc()
	}

	// Wait for goroutines to finish
	r.wg.Wait()

	// Unregister node (best effort)
	if r.client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		r.unregisterNode(ctx)
	}

	// Close session and client
	r.mu.Lock()
	if r.session != nil {
		r.session.Close()
	}
	if r.client != nil {
		r.client.Close()
	}
	r.mu.Unlock()

	return nil
}

// sessionManagerLoop manages the etcd session lifecycle, reconnecting if lost
func (r *DistributedAgentRegistry) sessionManagerLoop(ctx context.Context) {
	defer r.wg.Done()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.mu.RLock()
			session := r.session
			r.mu.RUnlock()

			if session == nil {
				r.reconnectSession(ctx)
				continue
			}

			select {
			case <-session.Done():
				// Session expired or lost
				r.reconnectSession(ctx)
			default:
				// Session is healthy
			}
		}
	}
}

// reconnectSession attempts to create a new session and register the node
func (r *DistributedAgentRegistry) reconnectSession(ctx context.Context) {
	backoff := 1 * time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Try to create session
			session, err := concurrency.NewSession(r.client, concurrency.WithTTL(int(r.config.LeaseTTL)))
			if err != nil {
				// Failed to create session, wait and retry
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
					backoff *= 2
					if backoff > maxBackoff {
						backoff = maxBackoff
					}
					continue
				}
			}

			// Success! Update state
			r.mu.Lock()
			if r.session != nil {
				r.session.Close() // Close old if exists
			}
			r.session = session
			r.leaseID = session.Lease()
			r.mu.Unlock()

			// Register node with new lease
			if err := r.registerNode(ctx); err != nil {
				// If registration fails, we'll rely on the sessionManagerLoop 
				// to eventually catch issues, or we could loop here.
				// For now, having a valid session is the most important step.
				// The node info will be put eventually if we add a retry here or if the app updates load.
			}

			return // Connected
		}
	}
}

// registerNode registers this node in the registry with an ephemeral lease
func (r *DistributedAgentRegistry) registerNode(ctx context.Context) error {
	r.mu.RLock()
	leaseID := r.leaseID
	r.mu.RUnlock()

	if leaseID == 0 {
		return fmt.Errorf("no active lease")
	}

	key := fmt.Sprintf("/dagens/nodes/%s", r.nodeID)
	value, err := json.Marshal(r.nodeInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal node info: %w", err)
	}

	// Put the node info with the lease
	_, err = r.client.Put(ctx, key, string(value), clientv3.WithLease(leaseID))
	if err != nil {
		return fmt.Errorf("failed to register node: %w", err)
	}

	// Add to local cache
	r.mu.Lock()
	r.nodes[r.nodeID] = r.nodeInfo
	r.mu.Unlock()

	return nil
}

// unregisterNode removes this node from the registry
func (r *DistributedAgentRegistry) unregisterNode(ctx context.Context) error {
	key := fmt.Sprintf("/dagens/nodes/%s", r.nodeID)
	_, err := r.client.Delete(ctx, key)
	return err
}

// watchClusterChanges watches for changes in the cluster and updates local cache
func (r *DistributedAgentRegistry) watchClusterChanges(ctx context.Context) {
	defer r.wg.Done()

	// Watch for node changes
	watchCh := r.client.Watch(ctx, "/dagens/nodes/", clientv3.WithPrefix())

	for {
		select {
		case <-ctx.Done():
			return
		case watchResp := <-watchCh:
			if watchResp.Err() != nil {
				time.Sleep(1 * time.Second)
				continue
			}
			for _, ev := range watchResp.Events {
				r.handleNodeEvent(ev)
			}
		}
	}
}

// handleNodeEvent processes a node-related event from etcd
func (r *DistributedAgentRegistry) handleNodeEvent(ev *clientv3.Event) {
	key := string(ev.Kv.Key)
	nodeID := r.extractNodeIDFromKey(key)

	r.mu.Lock()
	defer r.mu.Unlock()

	switch ev.Type {
	case clientv3.EventTypePut:
		// Node added or updated
		var nodeInfo NodeInfo
		if err := json.Unmarshal(ev.Kv.Value, &nodeInfo); err != nil {
			// Log error but continue
			return
		}
		
		_, exists := r.nodes[nodeID]
		r.nodes[nodeID] = nodeInfo

		// Notify observers
		for _, observer := range r.observers {
			if exists {
				// Node updated (we'll treat updates as additions for simplicity)
				observer.NodeAdded(nodeInfo)
			} else {
				// Node added
				observer.NodeAdded(nodeInfo)
			}
		}
	case clientv3.EventTypeDelete:
		// Node removed
		if _, exists := r.nodes[nodeID]; exists {
			delete(r.nodes, nodeID)

			// Notify observers
			for _, observer := range r.observers {
				observer.NodeRemoved(nodeID)
			}
		}
	}
}

// extractNodeIDFromKey extracts the node ID from an etcd key
func (r *DistributedAgentRegistry) extractNodeIDFromKey(key string) string {
	// Key format: /dagens/nodes/{node_id}
	prefix := "/dagens/nodes/"
	if len(key) > len(prefix) {
		return key[len(prefix):]
	}
	return ""
}

// GetNodes returns all known nodes in the cluster
func (r *DistributedAgentRegistry) GetNodes() []NodeInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	nodes := make([]NodeInfo, 0, len(r.nodes))
	for _, node := range r.nodes {
		nodes = append(nodes, node)
	}
	return nodes
}

// GetNode returns information about a specific node
func (r *DistributedAgentRegistry) GetNode(nodeID string) (NodeInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	node, exists := r.nodes[nodeID]
	return node, exists
}

// GetNodeID returns the local node ID
func (r *DistributedAgentRegistry) GetNodeID() string {
	return r.nodeID
}

// GetHealthyNodes returns only healthy nodes
func (r *DistributedAgentRegistry) GetHealthyNodes() []NodeInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	nodes := make([]NodeInfo, 0)
	for _, node := range r.nodes {
		if node.Healthy {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

// GetNodesByCapability returns nodes that have the specified capability
func (r *DistributedAgentRegistry) GetNodesByCapability(capability string) []NodeInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	nodes := make([]NodeInfo, 0)
	for _, node := range r.nodes {
		for _, cap := range node.Capabilities {
			if cap == capability {
				nodes = append(nodes, node)
				break
			}
		}
	}
	return nodes
}

// UpdateNodeLoad updates the load information for this node
func (r *DistributedAgentRegistry) UpdateNodeLoad(load float64) error {
	r.mu.Lock()
	r.nodeInfo.Load = load
	r.nodeInfo.LastSeen = time.Now()
	r.nodes[r.nodeID] = r.nodeInfo
	leaseID := r.leaseID
	r.mu.Unlock()

	if leaseID == 0 {
		return fmt.Errorf("no active lease")
	}

	// Update in etcd
	key := fmt.Sprintf("/dagens/nodes/%s", r.nodeID)
	value, err := json.Marshal(r.nodeInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal node info: %w", err)
	}

	_, err = r.client.Put(context.Background(), key, string(value), clientv3.WithLease(leaseID))
	return err
}

// RegisterService registers a service on this node
func (r *DistributedAgentRegistry) RegisterService(serviceName string, port int) error {
	r.mu.RLock()
	leaseID := r.leaseID
	r.mu.RUnlock()

	if leaseID == 0 {
		return fmt.Errorf("no active lease")
	}

	serviceInfo := ServiceInfo{
		NodeID:      r.nodeID,
		ServiceName: serviceName,
		Address:     r.nodeInfo.Address,
		Port:        port,
		LastSeen:    time.Now(),
		Healthy:     true,
	}

	key := fmt.Sprintf("/dagens/services/%s/%s", serviceName, r.nodeID)
	value, err := json.Marshal(serviceInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal service info: %w", err)
	}

	_, err = r.client.Put(context.Background(), key, string(value), clientv3.WithLease(leaseID))
	if err != nil {
		return fmt.Errorf("failed to register service: %w", err)
	}

	// Add to local cache
	r.mu.Lock()
	r.services[serviceName] = serviceInfo
	r.mu.Unlock()

	// Notify observers
	for _, observer := range r.observers {
		observer.ServiceRegistered(serviceInfo)
	}

	return nil
}

// UnregisterService removes a service registration
func (r *DistributedAgentRegistry) UnregisterService(serviceName string) error {
	key := fmt.Sprintf("/dagens/services/%s/%s", serviceName, r.nodeID)
	_, err := r.client.Delete(context.Background(), key)
	if err != nil {
		return err
	}

	// Remove from local cache
	r.mu.Lock()
	delete(r.services, serviceName)
	r.mu.Unlock()

	// Notify observers
	for _, observer := range r.observers {
		observer.ServiceUnregistered(serviceName)
	}

	return nil
}

// GetService returns information about a service
func (r *DistributedAgentRegistry) GetService(serviceName string) (ServiceInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	service, exists := r.services[serviceName]
	return service, exists
}

// GetServices returns all services registered under a name
func (r *DistributedAgentRegistry) GetServices(serviceName string) []ServiceInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	services := make([]ServiceInfo, 0)
	for _, service := range r.services {
		if service.ServiceName == serviceName {
			services = append(services, service)
		}
	}
	return services
}

// AddObserver adds a registry observer
func (r *DistributedAgentRegistry) AddObserver(observer RegistryObserver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observers = append(r.observers, observer)
}

// RemoveObserver removes a registry observer
func (r *DistributedAgentRegistry) RemoveObserver(observer RegistryObserver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	for i, obs := range r.observers {
		if obs == observer {
			r.observers = append(r.observers[:i], r.observers[i+1:]...)
			break
		}
	}
}

// GetNodeCount returns the total number of nodes in the cluster
func (r *DistributedAgentRegistry) GetNodeCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.nodes)
}

// GetHealthyNodeCount returns the number of healthy nodes in the cluster
func (r *DistributedAgentRegistry) GetHealthyNodeCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	count := 0
	for _, node := range r.nodes {
		if node.Healthy {
			count++
		}
	}
	return count
}

// NewMutex creates a new distributed mutex using the registry's session
func (r *DistributedAgentRegistry) NewMutex(key string) *coordination.DistributedMutex {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return coordination.NewDistributedMutex(r.session, key)
}

// NewBarrier creates a new distributed barrier using the registry's session
func (r *DistributedAgentRegistry) NewBarrier(key string, count int) *coordination.DistributedBarrier {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return coordination.NewDistributedBarrier(r.client, r.session, key, count)
}

// NewSemaphore creates a new distributed semaphore using the registry's session
func (r *DistributedAgentRegistry) NewSemaphore(key string, size int) *coordination.DistributedSemaphore {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return coordination.NewDistributedSemaphore(r.client, r.session, key, size)
}

// NewLeaderElector creates a new leader elector using the registry's client
func (r *DistributedAgentRegistry) NewLeaderElector(key string, callbacks coordination.LeaderCallbacks) (*coordination.LeaderElector, error) {
	// Leader elector uses its own session, but we pass client.
	// Thread safety for r.client is not an issue as it's a pointer that doesn't change, 
	// but strictly speaking we should probably be careful if we ever support client rotation.
	// For now, client is static after New.
	return coordination.NewLeaderElector(coordination.LeaderElectionConfig{
		Client:    r.client,
		Key:       key,
		Identity:  r.nodeID,
		Callbacks: callbacks,
	})
}
