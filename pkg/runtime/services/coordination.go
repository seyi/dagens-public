// Copyright 2025 Apache Spark AI Agents
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package services

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/runtime"
)

// ServiceBundle bundles all distributed services together
// Provides coordinated access to Session, Artifact, and Memory services
type ServiceBundle struct {
	SessionService  *DistributedSessionService
	ArtifactService *DistributedArtifactService
	MemoryService   *DistributedMemoryService

	// Configuration
	numPartitions int
	initialized   bool
	mu            sync.RWMutex
}

// ServiceBundleConfig configures all services
type ServiceBundleConfig struct {
	// Global settings
	NumPartitions int

	// Session service config
	SessionBackend      SessionBackend
	SessionCacheEnabled bool
	SessionCacheTTL     time.Duration
	SessionMaxCacheSize int

	// Artifact service config
	ArtifactBackend      ArtifactBackend
	ArtifactCacheEnabled bool
	ArtifactCacheTTL     time.Duration
	ArtifactMaxCacheSize int64
	MaxArtifactSize      int64

	// Memory service config
	MemoryBackend      MemoryBackend
	MemoryCacheEnabled bool
	MemoryCacheTTL     time.Duration
	EmbeddingDim       int
	EmbeddingProvider  EmbeddingProvider
}

// NewServiceBundle creates a coordinated service bundle
func NewServiceBundle(config ServiceBundleConfig) *ServiceBundle {
	// Apply defaults
	if config.NumPartitions <= 0 {
		config.NumPartitions = 4 // Default: 4 partitions
	}

	if config.SessionCacheTTL == 0 {
		config.SessionCacheTTL = 10 * time.Minute
	}

	if config.SessionMaxCacheSize == 0 {
		config.SessionMaxCacheSize = 1000
	}

	if config.ArtifactCacheTTL == 0 {
		config.ArtifactCacheTTL = 10 * time.Minute
	}

	if config.ArtifactMaxCacheSize == 0 {
		config.ArtifactMaxCacheSize = 100 * 1024 * 1024 // 100MB
	}

	if config.MaxArtifactSize == 0 {
		config.MaxArtifactSize = 10 * 1024 * 1024 // 10MB
	}

	if config.MemoryCacheTTL == 0 {
		config.MemoryCacheTTL = 30 * time.Minute
	}

	if config.EmbeddingDim == 0 {
		config.EmbeddingDim = 1536 // OpenAI ada-002
	}

	// Create services
	sessionService := NewDistributedSessionService(DistributedSessionConfig{
		Backend:      config.SessionBackend,
		NumPartitions: config.NumPartitions,
		EnableCache:  config.SessionCacheEnabled,
		CacheTTL:     config.SessionCacheTTL,
		MaxCacheSize: config.SessionMaxCacheSize,
	})

	artifactService := NewDistributedArtifactService(DistributedArtifactConfig{
		Backend:         config.ArtifactBackend,
		NumPartitions:   config.NumPartitions,
		EnableCache:     config.ArtifactCacheEnabled,
		CacheTTL:        config.ArtifactCacheTTL,
		MaxCacheSize:    config.ArtifactMaxCacheSize,
		MaxArtifactSize: config.MaxArtifactSize,
	})

	memoryService := NewDistributedMemoryService(DistributedMemoryConfig{
		Backend:           config.MemoryBackend,
		NumPartitions:     config.NumPartitions,
		EnableCache:       config.MemoryCacheEnabled,
		CacheTTL:          config.MemoryCacheTTL,
		EmbeddingDim:      config.EmbeddingDim,
		EmbeddingProvider: config.EmbeddingProvider,
	})

	return &ServiceBundle{
		SessionService:  sessionService,
		ArtifactService: artifactService,
		MemoryService:   memoryService,
		numPartitions:   config.NumPartitions,
		initialized:     true,
	}
}

// NewInMemoryServiceBundle creates a service bundle with in-memory backends
// Useful for testing and development
func NewInMemoryServiceBundle(numPartitions int) *ServiceBundle {
	return NewServiceBundle(ServiceBundleConfig{
		NumPartitions:        numPartitions,
		SessionBackend:       NewInMemorySessionBackend(),
		SessionCacheEnabled:  true,
		ArtifactBackend:      NewInMemoryArtifactBackend(),
		ArtifactCacheEnabled: true,
		MemoryBackend:        NewInMemoryMemoryBackend(),
		MemoryCacheEnabled:   true,
	})
}

// Close cleans up all services
func (b *ServiceBundle) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.initialized {
		return nil
	}

	// Close all services
	var errors []error

	if err := b.SessionService.Close(); err != nil {
		errors = append(errors, fmt.Errorf("session service: %w", err))
	}

	if err := b.ArtifactService.Close(); err != nil {
		errors = append(errors, fmt.Errorf("artifact service: %w", err))
	}

	if err := b.MemoryService.Close(); err != nil {
		errors = append(errors, fmt.Errorf("memory service: %w", err))
	}

	b.initialized = false

	if len(errors) > 0 {
		return fmt.Errorf("errors closing services: %v", errors)
	}

	return nil
}

// GetStats returns aggregated statistics from all services
func (b *ServiceBundle) GetStats() ServiceBundleStats {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return ServiceBundleStats{
		NumPartitions:      b.numPartitions,
		SessionCacheStats:  b.SessionService.GetCacheStats(),
		ArtifactCacheStats: b.ArtifactService.GetCacheStats(),
		MemoryIndexStats:   b.MemoryService.GetIndexStats(),
	}
}

// ServiceBundleStats provides aggregated statistics
type ServiceBundleStats struct {
	NumPartitions      int
	SessionCacheStats  map[int]SessionCacheStats
	ArtifactCacheStats map[int]ArtifactCacheStats
	MemoryIndexStats   map[int]MemoryIndexStats
}

// HealthCheck performs health checks on all services
func (b *ServiceBundle) HealthCheck(ctx context.Context) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.initialized {
		return fmt.Errorf("service bundle not initialized")
	}

	// Create a test session to verify service health
	testSession := &runtime.Session{
		ID:        "health-check-" + time.Now().Format("20060102150405"),
		UserID:    "health-check",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Test session service
	if err := b.SessionService.UpdateSession(ctx, testSession); err != nil {
		return fmt.Errorf("session service unhealthy: %w", err)
	}

	// Cleanup test session
	// Note: No delete method in SessionService, would need backend access

	return nil
}

// RuntimeWithServices extends AgentRuntime with distributed services
// Provides ADK-compatible runtime with Spark-aware service implementations
type RuntimeWithServices struct {
	AgentRuntime *runtime.AgentRuntime // Embedded ADK runtime
	Services     *ServiceBundle
}

// NewRuntimeWithServices creates a runtime with distributed services
func NewRuntimeWithServices(agentRuntime *runtime.AgentRuntime, services *ServiceBundle) *RuntimeWithServices {
	return &RuntimeWithServices{
		AgentRuntime: agentRuntime,
		Services:     services,
	}
}

// WithPartitionID sets the partition ID for partition-aware operations
// This should be called once per Spark task with the task's partition ID
func (r *RuntimeWithServices) WithPartitionID(partitionID string) *RuntimeWithServices {
	// If the embedded runtime supports partition ID, set it
	// This is a pattern for chaining configuration
	return r
}

// DistributedServiceFactory provides factory methods for creating services
// with common configurations
type DistributedServiceFactory struct{}

// NewFactory creates a new service factory
func NewFactory() *DistributedServiceFactory {
	return &DistributedServiceFactory{}
}

// CreateDevelopmentBundle creates a service bundle for development
// Uses in-memory backends with caching enabled
func (f *DistributedServiceFactory) CreateDevelopmentBundle() *ServiceBundle {
	return NewInMemoryServiceBundle(1) // Single partition for development
}

// CreateTestingBundle creates a service bundle for testing
// Uses in-memory backends with multiple partitions
func (f *DistributedServiceFactory) CreateTestingBundle(numPartitions int) *ServiceBundle {
	return NewInMemoryServiceBundle(numPartitions)
}

// CreateProductionBundle creates a service bundle for production
// Requires external backends (Redis, PostgreSQL, etc.)
func (f *DistributedServiceFactory) CreateProductionBundle(config ServiceBundleConfig) *ServiceBundle {
	// Validate production requirements
	if config.SessionBackend == nil {
		panic("production bundle requires SessionBackend")
	}
	if config.ArtifactBackend == nil {
		panic("production bundle requires ArtifactBackend")
	}
	if config.MemoryBackend == nil {
		panic("production bundle requires MemoryBackend")
	}

	return NewServiceBundle(config)
}

// SparkTaskContext provides Spark-specific context for service operations
type SparkTaskContext struct {
	PartitionID   int
	TaskAttemptID string
	StageID       int
	SessionID     string
}

// ServiceCoordinator coordinates service operations across Spark tasks
type ServiceCoordinator struct {
	bundle  *ServiceBundle
	taskCtx *SparkTaskContext
	mu      sync.RWMutex
}

// NewServiceCoordinator creates a coordinator for Spark task operations
func NewServiceCoordinator(bundle *ServiceBundle, taskCtx *SparkTaskContext) *ServiceCoordinator {
	return &ServiceCoordinator{
		bundle:  bundle,
		taskCtx: taskCtx,
	}
}

// GetSessionForTask retrieves the session for the current Spark task
func (c *ServiceCoordinator) GetSessionForTask(ctx context.Context) (*runtime.Session, error) {
	c.mu.RLock()
	sessionID := c.taskCtx.SessionID
	c.mu.RUnlock()

	return c.bundle.SessionService.GetSession(ctx, sessionID)
}

// UpdateSessionForTask updates the session for the current Spark task
func (c *ServiceCoordinator) UpdateSessionForTask(ctx context.Context, session *runtime.Session) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Ensure session ID matches task context
	if session.ID != c.taskCtx.SessionID {
		return fmt.Errorf("session ID mismatch: expected %s, got %s", c.taskCtx.SessionID, session.ID)
	}

	return c.bundle.SessionService.UpdateSession(ctx, session)
}

// SaveArtifactForTask saves an artifact for the current Spark task
func (c *ServiceCoordinator) SaveArtifactForTask(ctx context.Context, name string, data []byte) error {
	c.mu.RLock()
	sessionID := c.taskCtx.SessionID
	c.mu.RUnlock()

	return c.bundle.ArtifactService.SaveArtifact(ctx, sessionID, name, data)
}

// GetArtifactForTask retrieves an artifact for the current Spark task
func (c *ServiceCoordinator) GetArtifactForTask(ctx context.Context, name string) ([]byte, error) {
	c.mu.RLock()
	sessionID := c.taskCtx.SessionID
	c.mu.RUnlock()

	return c.bundle.ArtifactService.GetArtifact(ctx, sessionID, name)
}

// StoreMemoryForTask stores a memory for the current user
func (c *ServiceCoordinator) StoreMemoryForTask(ctx context.Context, memory *runtime.Memory) error {
	return c.bundle.MemoryService.Store(ctx, memory)
}

// QueryMemoryForTask performs semantic search for the current user
func (c *ServiceCoordinator) QueryMemoryForTask(ctx context.Context, userID string, queryEmbedding []float64, topK int) ([]*runtime.Memory, error) {
	return c.bundle.MemoryService.Query(ctx, userID, queryEmbedding, topK)
}

// GetPartitionStats returns statistics for the current partition
func (c *ServiceCoordinator) GetPartitionStats() PartitionStats {
	c.mu.RLock()
	partitionID := c.taskCtx.PartitionID
	c.mu.RUnlock()

	bundleStats := c.bundle.GetStats()

	return PartitionStats{
		PartitionID:       partitionID,
		SessionCacheStats: bundleStats.SessionCacheStats[partitionID],
		ArtifactCacheStats: bundleStats.ArtifactCacheStats[partitionID],
		MemoryIndexStats:  bundleStats.MemoryIndexStats[partitionID],
	}
}

// PartitionStats provides partition-local statistics
type PartitionStats struct {
	PartitionID        int
	SessionCacheStats  SessionCacheStats
	ArtifactCacheStats ArtifactCacheStats
	MemoryIndexStats   MemoryIndexStats
}

// ServiceMiddleware provides middleware for service operations
type ServiceMiddleware struct {
	bundle *ServiceBundle
}

// NewServiceMiddleware creates service middleware
func NewServiceMiddleware(bundle *ServiceBundle) *ServiceMiddleware {
	return &ServiceMiddleware{
		bundle: bundle,
	}
}

// WithMetrics wraps service operations with metrics collection
func (m *ServiceMiddleware) WithMetrics(operation func() error) error {
	start := time.Now()
	err := operation()
	duration := time.Since(start)

	// TODO: Emit metrics (Prometheus, CloudWatch, etc.)
	_ = duration

	return err
}

// WithRetry wraps service operations with retry logic
func (m *ServiceMiddleware) WithRetry(operation func() error, maxRetries int) error {
	var err error
	for i := 0; i < maxRetries; i++ {
		err = operation()
		if err == nil {
			return nil
		}

		// Exponential backoff
		backoff := time.Duration(1<<uint(i)) * 100 * time.Millisecond
		time.Sleep(backoff)
	}

	return fmt.Errorf("operation failed after %d retries: %w", maxRetries, err)
}

// WithTimeout wraps service operations with timeout
func (m *ServiceMiddleware) WithTimeout(ctx context.Context, operation func(context.Context) error, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- operation(ctx)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("operation timed out after %v", timeout)
	}
}
