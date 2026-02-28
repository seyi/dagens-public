// Package manifest provides YAML-based agent and orchestration definitions
// Inspired by Kubernetes-style declarative configuration
package manifest

import (
	"time"
)

// AgentManifest defines an agent in YAML format
type AgentManifest struct {
	APIVersion string            `yaml:"apiVersion" json:"apiVersion"`
	Kind       string            `yaml:"kind" json:"kind"`
	Metadata   AgentMetadata     `yaml:"metadata" json:"metadata"`
	Spec       AgentSpec         `yaml:"spec" json:"spec"`
	Status     *AgentStatus      `yaml:"status,omitempty" json:"status,omitempty"`
}

// AgentMetadata contains agent identification information
type AgentMetadata struct {
	Name        string            `yaml:"name" json:"name"`
	Namespace   string            `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

// AgentSpec defines the agent's configuration
type AgentSpec struct {
	// Runtime configuration
	Runtime      RuntimeSpec      `yaml:"runtime" json:"runtime"`

	// Agent capabilities
	Capabilities []string         `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`

	// Communication settings
	Communication CommunicationSpec `yaml:"communication,omitempty" json:"communication,omitempty"`

	// Resources and limits
	Resources    ResourceSpec      `yaml:"resources,omitempty" json:"resources,omitempty"`

	// Health check configuration
	HealthCheck  *HealthCheckSpec  `yaml:"healthCheck,omitempty" json:"healthCheck,omitempty"`

	// Environment variables
	Env          []EnvVar          `yaml:"env,omitempty" json:"env,omitempty"`

	// Tools available to the agent
	Tools        []ToolRef         `yaml:"tools,omitempty" json:"tools,omitempty"`

	// Dependencies on other agents
	DependsOn    []string          `yaml:"dependsOn,omitempty" json:"dependsOn,omitempty"`
}

// RuntimeSpec defines the agent's runtime configuration
type RuntimeSpec struct {
	// Type of agent runtime: python, go, docker, external
	Type        RuntimeType       `yaml:"type" json:"type"`

	// For Python agents (CrewAI, LangGraph, etc.)
	Python      *PythonRuntimeSpec `yaml:"python,omitempty" json:"python,omitempty"`

	// For Go agents (native dagens agents)
	Go          *GoRuntimeSpec     `yaml:"go,omitempty" json:"go,omitempty"`

	// For Docker-based agents
	Docker      *DockerRuntimeSpec `yaml:"docker,omitempty" json:"docker,omitempty"`

	// For external agents (already running)
	External    *ExternalRuntimeSpec `yaml:"external,omitempty" json:"external,omitempty"`
}

// RuntimeType defines the type of agent runtime
type RuntimeType string

const (
	RuntimeTypePython   RuntimeType = "python"
	RuntimeTypeGo       RuntimeType = "go"
	RuntimeTypeDocker   RuntimeType = "docker"
	RuntimeTypeExternal RuntimeType = "external"
)

// PythonRuntimeSpec defines Python agent configuration
type PythonRuntimeSpec struct {
	// Framework: crewai, langgraph, autogen, custom
	Framework   string   `yaml:"framework" json:"framework"`

	// Entry point file
	EntryPoint  string   `yaml:"entryPoint" json:"entryPoint"`

	// Working directory
	WorkDir     string   `yaml:"workDir,omitempty" json:"workDir,omitempty"`

	// Python version
	Version     string   `yaml:"version,omitempty" json:"version,omitempty"`

	// Virtual environment path
	VenvPath    string   `yaml:"venvPath,omitempty" json:"venvPath,omitempty"`

	// Requirements file
	Requirements string  `yaml:"requirements,omitempty" json:"requirements,omitempty"`

	// Additional Python arguments
	Args        []string `yaml:"args,omitempty" json:"args,omitempty"`
}

// GoRuntimeSpec defines Go agent configuration
type GoRuntimeSpec struct {
	// Package path
	Package     string   `yaml:"package" json:"package"`

	// Build tags
	BuildTags   []string `yaml:"buildTags,omitempty" json:"buildTags,omitempty"`

	// Binary path (if pre-built)
	Binary      string   `yaml:"binary,omitempty" json:"binary,omitempty"`
}

// DockerRuntimeSpec defines Docker agent configuration
type DockerRuntimeSpec struct {
	// Docker image
	Image       string   `yaml:"image" json:"image"`

	// Pull policy: Always, IfNotPresent, Never
	PullPolicy  string   `yaml:"pullPolicy,omitempty" json:"pullPolicy,omitempty"`

	// Command to run
	Command     []string `yaml:"command,omitempty" json:"command,omitempty"`

	// Container arguments
	Args        []string `yaml:"args,omitempty" json:"args,omitempty"`

	// Port mappings
	Ports       []PortSpec `yaml:"ports,omitempty" json:"ports,omitempty"`

	// Volume mounts
	Volumes     []VolumeSpec `yaml:"volumes,omitempty" json:"volumes,omitempty"`

	// Network mode
	Network     string   `yaml:"network,omitempty" json:"network,omitempty"`
}

// ExternalRuntimeSpec defines external agent configuration
type ExternalRuntimeSpec struct {
	// Endpoint URL
	Endpoint    string   `yaml:"endpoint" json:"endpoint"`

	// Authentication
	Auth        *AuthSpec `yaml:"auth,omitempty" json:"auth,omitempty"`
}

// CommunicationSpec defines communication settings
type CommunicationSpec struct {
	// Protocol: http, grpc
	Protocol    string   `yaml:"protocol,omitempty" json:"protocol,omitempty"`

	// Port to listen on
	Port        int      `yaml:"port,omitempty" json:"port,omitempty"`

	// Supported modalities
	Modalities  []string `yaml:"modalities,omitempty" json:"modalities,omitempty"`

	// Streaming support
	Streaming   bool     `yaml:"streaming,omitempty" json:"streaming,omitempty"`

	// TLS configuration
	TLS         *TLSSpec `yaml:"tls,omitempty" json:"tls,omitempty"`
}

// ResourceSpec defines resource limits
type ResourceSpec struct {
	// CPU limit (e.g., "100m", "1")
	CPULimit    string   `yaml:"cpuLimit,omitempty" json:"cpuLimit,omitempty"`

	// Memory limit (e.g., "128Mi", "1Gi")
	MemoryLimit string   `yaml:"memoryLimit,omitempty" json:"memoryLimit,omitempty"`

	// Timeout for operations
	Timeout     string   `yaml:"timeout,omitempty" json:"timeout,omitempty"`

	// Maximum concurrent requests
	MaxConcurrent int    `yaml:"maxConcurrent,omitempty" json:"maxConcurrent,omitempty"`
}

// HealthCheckSpec defines health check configuration
type HealthCheckSpec struct {
	// Endpoint to check
	Endpoint    string        `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`

	// Check interval
	Interval    string        `yaml:"interval,omitempty" json:"interval,omitempty"`

	// Timeout for health check
	Timeout     string        `yaml:"timeout,omitempty" json:"timeout,omitempty"`

	// Number of failures before unhealthy
	FailureThreshold int      `yaml:"failureThreshold,omitempty" json:"failureThreshold,omitempty"`

	// Number of successes before healthy
	SuccessThreshold int      `yaml:"successThreshold,omitempty" json:"successThreshold,omitempty"`

	// Initial delay before first check
	InitialDelay string       `yaml:"initialDelay,omitempty" json:"initialDelay,omitempty"`
}

// EnvVar defines an environment variable
type EnvVar struct {
	Name  string `yaml:"name" json:"name"`
	Value string `yaml:"value,omitempty" json:"value,omitempty"`

	// Reference to secret
	SecretRef *SecretRef `yaml:"secretRef,omitempty" json:"secretRef,omitempty"`
}

// SecretRef references a secret
type SecretRef struct {
	Name string `yaml:"name" json:"name"`
	Key  string `yaml:"key" json:"key"`
}

// ToolRef references a tool
type ToolRef struct {
	Name    string `yaml:"name" json:"name"`

	// MCP server reference
	MCP     *MCPToolRef `yaml:"mcp,omitempty" json:"mcp,omitempty"`

	// Built-in tool type
	BuiltIn string      `yaml:"builtIn,omitempty" json:"builtIn,omitempty"`
}

// MCPToolRef references an MCP server
type MCPToolRef struct {
	Server  string   `yaml:"server" json:"server"`
	Tools   []string `yaml:"tools,omitempty" json:"tools,omitempty"`
}

// PortSpec defines a port mapping
type PortSpec struct {
	ContainerPort int    `yaml:"containerPort" json:"containerPort"`
	HostPort      int    `yaml:"hostPort,omitempty" json:"hostPort,omitempty"`
	Protocol      string `yaml:"protocol,omitempty" json:"protocol,omitempty"`
}

// VolumeSpec defines a volume mount
type VolumeSpec struct {
	HostPath      string `yaml:"hostPath" json:"hostPath"`
	ContainerPath string `yaml:"containerPath" json:"containerPath"`
	ReadOnly      bool   `yaml:"readOnly,omitempty" json:"readOnly,omitempty"`
}

// AuthSpec defines authentication configuration
type AuthSpec struct {
	Type   string `yaml:"type" json:"type"` // bearer, basic, api_key, mtls
	Token  string `yaml:"token,omitempty" json:"token,omitempty"`
	Header string `yaml:"header,omitempty" json:"header,omitempty"`
}

// TLSSpec defines TLS configuration
type TLSSpec struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`
	CertFile string `yaml:"certFile,omitempty" json:"certFile,omitempty"`
	KeyFile  string `yaml:"keyFile,omitempty" json:"keyFile,omitempty"`
	CAFile   string `yaml:"caFile,omitempty" json:"caFile,omitempty"`
}

// AgentStatus represents the current status of an agent
type AgentStatus struct {
	Phase       AgentPhase `yaml:"phase" json:"phase"`
	Ready       bool       `yaml:"ready" json:"ready"`
	Endpoint    string     `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	LastUpdated time.Time  `yaml:"lastUpdated,omitempty" json:"lastUpdated,omitempty"`
	Message     string     `yaml:"message,omitempty" json:"message,omitempty"`
}

// AgentPhase represents the lifecycle phase of an agent
type AgentPhase string

const (
	AgentPhasePending     AgentPhase = "Pending"
	AgentPhaseStarting    AgentPhase = "Starting"
	AgentPhaseRunning     AgentPhase = "Running"
	AgentPhaseHealthy     AgentPhase = "Healthy"
	AgentPhaseUnhealthy   AgentPhase = "Unhealthy"
	AgentPhaseStopping    AgentPhase = "Stopping"
	AgentPhaseStopped     AgentPhase = "Stopped"
	AgentPhaseFailed      AgentPhase = "Failed"
)

// OrchestrationManifest defines an orchestration flow
type OrchestrationManifest struct {
	APIVersion string                `yaml:"apiVersion" json:"apiVersion"`
	Kind       string                `yaml:"kind" json:"kind"`
	Metadata   AgentMetadata         `yaml:"metadata" json:"metadata"`
	Spec       OrchestrationSpec     `yaml:"spec" json:"spec"`
}

// OrchestrationSpec defines the orchestration configuration
type OrchestrationSpec struct {
	// Entry point agent
	EntryPoint string          `yaml:"entryPoint" json:"entryPoint"`

	// Agent references
	Agents     []AgentRef      `yaml:"agents" json:"agents"`

	// Transfer rules
	Transfers  []TransferRule  `yaml:"transfers,omitempty" json:"transfers,omitempty"`

	// Global settings
	Settings   OrchestratorSettings `yaml:"settings,omitempty" json:"settings,omitempty"`
}

// AgentRef references an agent
type AgentRef struct {
	Name      string `yaml:"name" json:"name"`
	Manifest  string `yaml:"manifest,omitempty" json:"manifest,omitempty"`
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`
}

// TransferRule defines agent transfer rules
type TransferRule struct {
	From      string   `yaml:"from" json:"from"`
	To        []string `yaml:"to" json:"to"`
	Condition string   `yaml:"condition,omitempty" json:"condition,omitempty"`
}

// OrchestratorSettings defines global orchestration settings
type OrchestratorSettings struct {
	// Maximum depth of agent transfers
	MaxTransferDepth int    `yaml:"maxTransferDepth,omitempty" json:"maxTransferDepth,omitempty"`

	// Global timeout
	Timeout          string `yaml:"timeout,omitempty" json:"timeout,omitempty"`

	// Retry policy
	RetryPolicy      *RetryPolicy `yaml:"retryPolicy,omitempty" json:"retryPolicy,omitempty"`
}

// RetryPolicy defines retry behavior
type RetryPolicy struct {
	MaxRetries int    `yaml:"maxRetries" json:"maxRetries"`
	Backoff    string `yaml:"backoff,omitempty" json:"backoff,omitempty"`
}
