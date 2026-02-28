// Package runtime provides agent process lifecycle management.
package runtime

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/seyi/dagens/pkg/a2a"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// ProcessStatus represents process lifecycle state
type ProcessStatus string

const (
	ProcessStatusPending   ProcessStatus = "pending"
	ProcessStatusStarting  ProcessStatus = "starting"
	ProcessStatusRunning   ProcessStatus = "running"
	ProcessStatusHealthy   ProcessStatus = "healthy"
	ProcessStatusUnhealthy ProcessStatus = "unhealthy"
	ProcessStatusStopping  ProcessStatus = "stopping"
	ProcessStatusStopped   ProcessStatus = "stopped"
	ProcessStatusFailed    ProcessStatus = "failed"
)

// HealthStatus represents agent health
type HealthStatus string

const (
	HealthStatusUnknown   HealthStatus = "unknown"
	HealthStatusHealthy   HealthStatus = "healthy"
	HealthStatusUnhealthy HealthStatus = "unhealthy"
)

// ManagedProcess represents a running agent process
type ManagedProcess struct {
	ID              string
	Name            string
	Spec            AgentSpec
	Cmd             *exec.Cmd
	Status          ProcessStatus
	Port            int
	PID             int
	StartedAt       time.Time
	RestartCount    int
	HealthStatus    HealthStatus
	LastHealthCheck time.Time
	stdout          io.ReadCloser
	stderr          io.ReadCloser
	logFile         *os.File
	cancel          context.CancelFunc
}

// ProcessManager manages external agent processes
type ProcessManager struct {
	processes     map[string]*ManagedProcess
	registry      *a2a.DiscoveryRegistry
	healthChecker *HealthChecker
	logDir        string
	tracer        trace.Tracer
	mu            sync.RWMutex
}

// HealthChecker performs periodic health checks
type HealthChecker struct {
	client   *http.Client
	interval time.Duration
}

// NewHealthChecker creates a new health checker
func NewHealthChecker(interval time.Duration) *HealthChecker {
	return &HealthChecker{
		client: &http.Client{
			Timeout: 5 * time.Second,
		},
		interval: interval,
	}
}

// Check performs a health check against an endpoint
func (h *HealthChecker) Check(endpoint string) (HealthStatus, error) {
	resp, err := h.client.Get(endpoint + "/health")
	if err != nil {
		return HealthStatusUnhealthy, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return HealthStatusHealthy, nil
	}
	return HealthStatusUnhealthy, fmt.Errorf("health check returned %d", resp.StatusCode)
}

// NewProcessManager creates a new process manager
func NewProcessManager(registry *a2a.DiscoveryRegistry) *ProcessManager {
	return &ProcessManager{
		processes:     make(map[string]*ManagedProcess),
		registry:      registry,
		healthChecker: NewHealthChecker(10 * time.Second),
		logDir:        "/var/log/dagens/agents",
		tracer:        otel.Tracer("dagens.process"),
	}
}

// StartAgent starts an external agent process
func (pm *ProcessManager) StartAgent(ctx context.Context, spec AgentSpec) error {
	ctx, span := pm.tracer.Start(ctx, "process.start",
		trace.WithAttributes(
			attribute.String("agent.name", spec.Name),
			attribute.String("agent.type", string(spec.Type)),
		),
	)
	defer span.End()

	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Check if already running
	if existing, exists := pm.processes[spec.Name]; exists {
		if existing.Status == ProcessStatusRunning || existing.Status == ProcessStatusHealthy {
			return fmt.Errorf("agent %s is already running", spec.Name)
		}
	}

	// Validate spec
	if spec.Runtime == nil || spec.Runtime.Command == "" {
		return fmt.Errorf("agent %s: runtime.command is required", spec.Name)
	}

	// Build command
	cmd := exec.CommandContext(ctx, spec.Runtime.Command, spec.Runtime.Args...)

	if spec.Runtime.WorkDir != "" {
		cmd.Dir = spec.Runtime.WorkDir
	}

	// Set environment
	cmd.Env = os.Environ()
	for k, v := range spec.Runtime.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Capture stdout/stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Create log file
	var logFile *os.File
	if err := os.MkdirAll(pm.logDir, 0755); err == nil {
		logFile, _ = os.Create(fmt.Sprintf("%s/%s.log", pm.logDir, spec.Name))
	}
	if logFile == nil {
		// Fall back to temp if log dir doesn't exist
		logFile, _ = os.CreateTemp("", fmt.Sprintf("dagens-%s-*.log", spec.Name))
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start agent %s: %w", spec.Name, err)
	}

	// Create managed process
	processCtx, cancel := context.WithCancel(context.Background())
	mp := &ManagedProcess{
		ID:           fmt.Sprintf("%s-%d", spec.Name, time.Now().UnixNano()),
		Name:         spec.Name,
		Spec:         spec,
		Cmd:          cmd,
		Status:       ProcessStatusStarting,
		Port:         spec.Network.Port,
		PID:          cmd.Process.Pid,
		StartedAt:    time.Now(),
		HealthStatus: HealthStatusUnknown,
		stdout:       stdout,
		stderr:       stderr,
		logFile:      logFile,
		cancel:       cancel,
	}

	pm.processes[spec.Name] = mp

	// Start log capture goroutines
	go pm.captureOutput(mp, stdout, "stdout")
	go pm.captureOutput(mp, stderr, "stderr")

	// Start process monitor
	go pm.monitorProcess(processCtx, mp)

	// Wait for healthy
	if spec.Defaults.HealthCheck.Enabled {
		go pm.waitForHealthy(processCtx, mp)
	} else {
		mp.Status = ProcessStatusRunning
	}

	// Register in A2A registry
	card := &a2a.AgentCard{
		ID:         spec.Name,
		Name:       spec.Name,
		Endpoint:   fmt.Sprintf("http://localhost:%d/a2a/invoke", spec.Network.Port),
		Modalities: []a2a.Modality{a2a.ModalityText},
	}
	for _, cap := range spec.Capabilities {
		card.Capabilities = append(card.Capabilities, a2a.Capability{
			Name:        cap.Name,
			Description: cap.Description,
		})
	}
	pm.registry.Register(card)

	span.SetAttributes(attribute.Int("agent.pid", mp.PID))
	return nil
}

func (pm *ProcessManager) captureOutput(mp *ManagedProcess, reader io.ReadCloser, stream string) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		timestamp := time.Now().Format(time.RFC3339)
		logLine := fmt.Sprintf("[%s] [%s] %s\n", timestamp, stream, line)
		if mp.logFile != nil {
			mp.logFile.WriteString(logLine)
		}
	}
}

func (pm *ProcessManager) monitorProcess(ctx context.Context, mp *ManagedProcess) {
	// Wait for process to exit
	err := mp.Cmd.Wait()

	pm.mu.Lock()
	defer pm.mu.Unlock()

	if err != nil {
		mp.Status = ProcessStatusFailed
	} else {
		mp.Status = ProcessStatusStopped
	}

	// Close log file
	if mp.logFile != nil {
		mp.logFile.Close()
	}

	// Unregister from A2A
	pm.registry.Unregister(mp.Name)
}

func (pm *ProcessManager) waitForHealthy(ctx context.Context, mp *ManagedProcess) {
	endpoint := fmt.Sprintf("http://localhost:%d", mp.Port)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	timeout := time.After(30 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return
		case <-timeout:
			pm.mu.Lock()
			mp.Status = ProcessStatusUnhealthy
			pm.mu.Unlock()
			return
		case <-ticker.C:
			status, _ := pm.healthChecker.Check(endpoint)
			pm.mu.Lock()
			mp.HealthStatus = status
			mp.LastHealthCheck = time.Now()
			if status == HealthStatusHealthy {
				mp.Status = ProcessStatusHealthy
				pm.mu.Unlock()
				return
			}
			pm.mu.Unlock()
		}
	}
}

// StopAgent stops an agent process
func (pm *ProcessManager) StopAgent(ctx context.Context, name string) error {
	pm.mu.Lock()
	mp, exists := pm.processes[name]
	if !exists {
		pm.mu.Unlock()
		return fmt.Errorf("agent %s not found", name)
	}
	pm.mu.Unlock()

	mp.Status = ProcessStatusStopping
	mp.cancel()

	// Send SIGTERM
	if mp.Cmd.Process != nil {
		mp.Cmd.Process.Signal(os.Interrupt)

		// Wait with timeout
		done := make(chan error)
		go func() { done <- mp.Cmd.Wait() }()

		select {
		case <-done:
			// Process exited
		case <-time.After(10 * time.Second):
			// Force kill
			mp.Cmd.Process.Kill()
		}
	}

	pm.mu.Lock()
	mp.Status = ProcessStatusStopped
	pm.mu.Unlock()

	return nil
}

// RestartAgent restarts an agent
func (pm *ProcessManager) RestartAgent(ctx context.Context, name string) error {
	pm.mu.RLock()
	mp, exists := pm.processes[name]
	if !exists {
		pm.mu.RUnlock()
		return fmt.Errorf("agent %s not found", name)
	}
	spec := mp.Spec
	pm.mu.RUnlock()

	if err := pm.StopAgent(ctx, name); err != nil {
		return err
	}

	pm.mu.Lock()
	mp.RestartCount++
	pm.mu.Unlock()

	return pm.StartAgent(ctx, spec)
}

// GetStatus returns agent status
func (pm *ProcessManager) GetStatus(name string) (*AgentStatusInfo, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	mp, exists := pm.processes[name]
	if !exists {
		return nil, fmt.Errorf("agent %s not found", name)
	}

	return &AgentStatusInfo{
		Name:            mp.Name,
		Type:            mp.Spec.Type,
		Status:          mp.Status,
		PID:             mp.PID,
		Port:            mp.Port,
		StartedAt:       mp.StartedAt,
		RestartCount:    mp.RestartCount,
		HealthStatus:    mp.HealthStatus,
		LastHealthCheck: mp.LastHealthCheck,
	}, nil
}

// ListAgents returns all managed agents
func (pm *ProcessManager) ListAgents() []*AgentStatusInfo {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	result := make([]*AgentStatusInfo, 0, len(pm.processes))
	for _, mp := range pm.processes {
		result = append(result, &AgentStatusInfo{
			Name:         mp.Name,
			Type:         mp.Spec.Type,
			Status:       mp.Status,
			PID:          mp.PID,
			Port:         mp.Port,
			Endpoint:     fmt.Sprintf("http://localhost:%d", mp.Port),
			HealthStatus: mp.HealthStatus,
		})
	}
	return result
}

// StreamLogs streams agent logs
func (pm *ProcessManager) StreamLogs(ctx context.Context, name string, w io.Writer, follow bool) error {
	pm.mu.RLock()
	mp, exists := pm.processes[name]
	pm.mu.RUnlock()

	if !exists {
		return fmt.Errorf("agent %s not found", name)
	}

	logPath := fmt.Sprintf("%s/%s.log", pm.logDir, name)
	if mp.logFile != nil {
		logPath = mp.logFile.Name()
	}

	file, err := os.Open(logPath)
	if err != nil {
		return err
	}
	defer file.Close()

	if follow {
		// Tail -f style
		reader := bufio.NewReader(file)
		for {
			select {
			case <-ctx.Done():
				return nil
			default:
				line, err := reader.ReadString('\n')
				if err == io.EOF {
					time.Sleep(100 * time.Millisecond)
					continue
				}
				if err != nil {
					return err
				}
				w.Write([]byte(line))
			}
		}
	} else {
		_, err = io.Copy(w, file)
		return err
	}
}

// AgentStatusInfo represents current agent status
type AgentStatusInfo struct {
	Name            string
	Type            AgentType
	Status          ProcessStatus
	PID             int
	Port            int
	Endpoint        string
	StartedAt       time.Time
	RestartCount    int
	HealthStatus    HealthStatus
	LastHealthCheck time.Time
}

// Singleton instance
var processManager *ProcessManager
var pmOnce sync.Once

// GetProcessManager returns the singleton process manager
func GetProcessManager() *ProcessManager {
	pmOnce.Do(func() {
		processManager = NewProcessManager(a2a.NewDiscoveryRegistry())
	})
	return processManager
}
