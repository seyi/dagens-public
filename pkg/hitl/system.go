package hitl

import (
	"context"
	"fmt"
	"time"

	"github.com/seyi/dagens/pkg/graph"
)

// SystemConfig holds the configuration for the complete HITL system
type SystemConfig struct {
	// Core settings
	ShortWaitThreshold time.Duration
	MaxConcurrentWaits int
	CallbackSecret     []byte

	// Infrastructure
	CheckpointStore  CheckpointStore
	IdempotencyStore IdempotencyStore
	ResumptionQueue  ResumptionQueue
	GraphRegistry    GraphRegistry

	// Workers
	NumResumptionWorkers int

	// Security
	SecretRotationEnabled bool
	RotationInterval      time.Duration

	// Observability
	MetricsCollector *MetricsCollector
	AlertService     *AlertService
	LoggingService   *LoggingService
	TracingService   *TracingService
}

// System represents the complete HITL system
type System struct {
	config      *SystemConfig
	workers     []*ResumptionWorker
	securityMgr *SecurityManager
	monitoring  *MonitoringService
	operational *OperationalToolService
}

// NewSystem creates a new HITL system
func NewSystem(config SystemConfig) *System {
	// Create security manager
	securityMgr := NewSecurityManager(config.CallbackSecret)

	// Create metrics collector if not provided
	if config.MetricsCollector == nil {
		config.MetricsCollector = NewMetricsCollector()
	}

	// Create other services if not provided
	if config.AlertService == nil {
		config.AlertService = NewAlertService()
	}
	if config.LoggingService == nil {
		config.LoggingService = NewLoggingService()
	}
	if config.TracingService == nil {
		config.TracingService = NewTracingService()
	}

	// Create operational tools
	operational := NewOperationalToolService(config.CheckpointStore, config.IdempotencyStore)

	// Create executor factory
	executorFactory := func(graphID, graphVersion string) ResumableExecutor {
		// Placeholder simple executor; replace with graph-aware factory once available.
		return NewSimpleResumableExecutor(graphID, graphVersion, map[string]graph.Node{})
	}

	// Create resumption workers
	var workers []*ResumptionWorker
	if config.NumResumptionWorkers == 0 {
		config.NumResumptionWorkers = 5 // Default
	}

	for i := 0; i < config.NumResumptionWorkers; i++ {
		worker := NewResumptionWorker(
			config.ResumptionQueue,
			config.CheckpointStore,
			config.IdempotencyStore,
			config.GraphRegistry,
			executorFactory,
			config.MetricsCollector.GetMetrics(),
		)
		workers = append(workers, worker)
	}

	// Create monitoring service
	monitoring := NewMonitoringService(config.MetricsCollector)

	return &System{
		config:      &config,
		workers:     workers,
		securityMgr: securityMgr,
		monitoring:  monitoring,
		operational: operational,
	}
}

// Start starts the HITL system
func (s *System) Start(ctx context.Context) error {
	// Start all resumption workers
	for _, worker := range s.workers {
		worker.Start(ctx, 1) // Each worker runs in its own goroutine
	}

	// In a real implementation, start other services as needed
	// e.g., secret rotation, monitoring, etc.

	return nil
}

// Stop stops the HITL system gracefully
func (s *System) Stop() {
	// Stop all workers
	for _, worker := range s.workers {
		worker.Stop()
	}
}

// CreateHumanNode creates a HumanNode configured for the system
func (s *System) CreateHumanNode(config HumanNodeConfig) *HumanNode {
	// Set defaults from system config if not provided
	if config.ShortWaitThreshold == 0 {
		config.ShortWaitThreshold = s.config.ShortWaitThreshold
	}
	if config.MaxConcurrentWaits == 0 {
		config.MaxConcurrentWaits = s.config.MaxConcurrentWaits
	}
	if config.CallbackSecret == nil {
		config.CallbackSecret = s.config.CallbackSecret
	}
	if config.CheckpointStore == nil {
		config.CheckpointStore = s.config.CheckpointStore
	}

	return NewHumanNode(config)
}

// CreateCallbackHandler creates a CallbackHandler configured for the system
func (s *System) CreateCallbackHandler() *CallbackHandler {
	return NewCallbackHandler(
		s.config.ResumptionQueue,
		s.config.IdempotencyStore,
		s.config.GraphRegistry,
		s.config.CallbackSecret,
		s.config.MetricsCollector.GetMetrics(),
	)
}

// GetMonitoringService returns the monitoring service
func (s *System) GetMonitoringService() *MonitoringService {
	return s.monitoring
}

// GetOperationalTools returns the operational tools
func (s *System) GetOperationalTools() *OperationalToolService {
	return s.operational
}

// ValidateStateSerialization validates that all state types can be serialized
func (s *System) ValidateStateSerialization(state graph.State) error {
	// Try to marshal it - check if it's an AdvancedState that supports marshaling
	advancedState, ok := state.(graph.AdvancedState)
	if !ok {
		return fmt.Errorf("state does not implement AdvancedState interface with Marshal/Unmarshal")
	}

	// Try to marshal it
	serialized, err := advancedState.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Try to unmarshal it back
	newState := graph.NewMemoryState()
	err = newState.Unmarshal(serialized)
	if err != nil {
		return fmt.Errorf("failed to unmarshal state: %w", err)
	}

	return nil
}

// RunHealthCheck performs a complete health check of the system
func (s *System) RunHealthCheck() map[string]interface{} {
	return s.monitoring.HealthCheck()
}
