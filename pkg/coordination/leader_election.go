// Package coordination provides distributed coordination primitives like leader election
package coordination

import (
	"context"
	"fmt"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
)

// LeaderElector manages leader election for distributed coordination
type LeaderElector struct {
	client     *clientv3.Client
	session    *concurrency.Session
	key        string
	identity   string
	callbacks  LeaderCallbacks
	mu         sync.RWMutex
	isLeader   bool
	cancelFunc context.CancelFunc
	wg         sync.WaitGroup
}

// LeaderCallbacks defines callbacks for leader election events
type LeaderCallbacks struct {
	OnElected    func() // Called when this instance becomes leader
	OnRevoked    func() // Called when leadership is lost
	OnAttempting func() // Called when attempting to gain leadership
}

// LeaderElectionConfig configures leader election
type LeaderElectionConfig struct {
	Client    *clientv3.Client
	SessionTTL  int // Session TTL in seconds
	Key         string // Etcd key for leader election
	Identity    string // Unique identity for this instance
	Callbacks   LeaderCallbacks
}

// NewLeaderElector creates a new leader elector
func NewLeaderElector(config LeaderElectionConfig) (*LeaderElector, error) {
	if config.SessionTTL == 0 {
		config.SessionTTL = 10 // Default 10 seconds
	}
	if config.Key == "" {
		config.Key = "/dagens/leader/election"
	}
	if config.Identity == "" {
		return nil, fmt.Errorf("identity is required for leader election")
	}

	// Create session with TTL
	session, err := concurrency.NewSession(config.Client, concurrency.WithTTL(config.SessionTTL))
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd session: %w", err)
	}

	elector := &LeaderElector{
		client:    config.Client,
		session:   session,
		key:       config.Key,
		identity:  config.Identity,
		callbacks: config.Callbacks,
	}

	return elector, nil
}

// Start begins the leader election process
func (le *LeaderElector) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	le.cancelFunc = cancel

	// Start the election loop
	le.wg.Add(1)
	go le.electionLoop(ctx)

	return nil
}

// Stop stops the leader election process
func (le *LeaderElector) Stop() {
	if le.cancelFunc != nil {
		le.cancelFunc()
	}
	le.wg.Wait()
	
	if le.session != nil {
		le.session.Close()
	}
}

// IsLeader returns whether this instance is currently the leader
func (le *LeaderElector) IsLeader() bool {
	le.mu.RLock()
	defer le.mu.RUnlock()
	return le.isLeader
}

// GetLeaderIdentity returns the current leader's identity
func (le *LeaderElector) GetLeaderIdentity() (string, error) {
	resp, err := le.client.Get(context.Background(), le.key)
	if err != nil {
		return "", fmt.Errorf("failed to get leader identity: %w", err)
	}

	if len(resp.Kvs) == 0 {
		return "", fmt.Errorf("no leader currently elected")
	}

	return string(resp.Kvs[0].Value), nil
}

// electionLoop runs the continuous leader election process
func (le *LeaderElector) electionLoop(ctx context.Context) {
	defer le.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Attempt to gain leadership
			if le.callbacks.OnAttempting != nil {
				le.callbacks.OnAttempting()
			}

			// Create a new election instance
			election := concurrency.NewElection(le.session, le.key)

			// Campaign to become leader
			err := election.Campaign(ctx, le.identity)
			if err != nil {
				// Could be context cancellation or etcd error
				if ctx.Err() != nil {
					return
				}
				// Log error and continue to try again
				continue
			}

			// Successfully became leader
			le.setLeaderStatus(true)

			if le.callbacks.OnElected != nil {
				le.callbacks.OnElected()
			}

			// Monitor leadership - this blocks until leadership is lost
			le.monitorLeadership(ctx, election)

			// Leadership was lost
			le.setLeaderStatus(false)

			if le.callbacks.OnRevoked != nil {
				le.callbacks.OnRevoked()
			}

			// Add a small delay before attempting to regain leadership
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
				// Continue to next election attempt
			}
		}
	}
}

// monitorLeadership monitors the leadership status
func (le *LeaderElector) monitorLeadership(ctx context.Context, election *concurrency.Election) {
	// Create a context for monitoring that's independent of the main context
	monitorCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Watch for leadership changes
	watchChan := election.Observe(monitorCtx)

	for {
		select {
		case <-ctx.Done():
			// Main context cancelled, stop monitoring
			return
		case _, ok := <-watchChan:
			if !ok {
				// Channel closed, leadership likely lost
				return
			}
			// Leadership status is still active
		}
	}
}

// setLeaderStatus updates the internal leader status
func (le *LeaderElector) setLeaderStatus(isLeader bool) {
	le.mu.Lock()
	defer le.mu.Unlock()
	le.isLeader = isLeader
}

// LeaderTaskRunner runs tasks that should only execute on the leader node
type LeaderTaskRunner struct {
	elector *LeaderElector
	tasks   []LeaderTask
	wg      sync.WaitGroup
}

// LeaderTask defines a task that runs only on the leader
type LeaderTask struct {
	Name        string
	Function    func(context.Context) error
	Interval    time.Duration
	RunUntil    func() bool // Function that returns true when task should stop
}

// NewLeaderTaskRunner creates a new leader task runner
func NewLeaderTaskRunner(elector *LeaderElector) *LeaderTaskRunner {
	return &LeaderTaskRunner{
		elector: elector,
		tasks:   make([]LeaderTask, 0),
	}
}

// AddTask adds a task that runs only on the leader
func (ltr *LeaderTaskRunner) AddTask(task LeaderTask) {
	if task.Interval == 0 {
		task.Interval = 30 * time.Second // Default interval
	}
	if task.RunUntil == nil {
		// Default: run until context is cancelled
		task.RunUntil = func() bool { return false }
	}
	ltr.tasks = append(ltr.tasks, task)
}

// Start starts running leader tasks
func (ltr *LeaderTaskRunner) Start(ctx context.Context) error {
	for _, task := range ltr.tasks {
		task := task // Capture for closure
		ltr.wg.Add(1)
		go ltr.runTask(ctx, task)
	}
	return nil
}

// Stop stops all leader tasks
func (ltr *LeaderTaskRunner) Stop() {
	ltr.wg.Wait()
}

// runTask runs a single leader task continuously
func (ltr *LeaderTaskRunner) runTask(ctx context.Context, task LeaderTask) {
	defer ltr.wg.Done()

	ticker := time.NewTicker(task.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !ltr.elector.IsLeader() {
				continue // Only run on leader
			}

			if task.RunUntil != nil && task.RunUntil() {
				return // Task should stop
			}

			// Run the task
			err := task.Function(ctx)
			if err != nil {
				// Log error but continue running
			}
		}
	}
}