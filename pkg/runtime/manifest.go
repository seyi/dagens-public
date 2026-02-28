// Package runtime provides YAML manifest loading for agent definitions.
package runtime

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// AgentManifest represents the root YAML document
type AgentManifest struct {
	APIVersion string       `yaml:"apiVersion"`
	Kind       string       `yaml:"kind"`
	Metadata   ManifestMeta `yaml:"metadata"`
	Spec       ManifestSpec `yaml:"spec"`
}

// ManifestMeta contains manifest metadata
type ManifestMeta struct {
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
}

// ManifestSpec contains the manifest specification
type ManifestSpec struct {
	Defaults AgentDefaults `yaml:"defaults"`
	Agents   []AgentSpec   `yaml:"agents"`
}

// AgentDefaults contains default configuration for agents
type AgentDefaults struct {
	HealthCheck HealthCheckConfig `yaml:"healthCheck"`
	Retry       RetryConfig       `yaml:"retry"`
}

// HealthCheckConfig configures health checking
type HealthCheckConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Interval time.Duration `yaml:"interval"`
	Timeout  time.Duration `yaml:"timeout"`
	Path     string        `yaml:"path"`
}

// RetryConfig configures retry behavior
type RetryConfig struct {
	MaxAttempts  int           `yaml:"maxAttempts"`
	Backoff      string        `yaml:"backoff"` // "exponential" or "linear"
	InitialDelay time.Duration `yaml:"initialDelay"`
}

// AgentSpec defines an individual agent
type AgentSpec struct {
	Name         string            `yaml:"name"`
	Type         AgentType         `yaml:"type"` // native, external, remote
	Runtime      *RuntimeSpec      `yaml:"runtime,omitempty"`
	Network      NetworkSpec       `yaml:"network,omitempty"`
	Endpoint     string            `yaml:"endpoint,omitempty"` // For remote agents
	Capabilities []CapabilitySpec  `yaml:"capabilities"`
	DependsOn    []string          `yaml:"dependsOn,omitempty"`
	Config       map[string]any    `yaml:"config,omitempty"`
	Resources    *ResourceSpec     `yaml:"resources,omitempty"`
	Auth         *AuthSpec         `yaml:"auth,omitempty"`
	Defaults     AgentDefaults     `yaml:"defaults,omitempty"` // Merged from manifest defaults
}

// AgentType defines the type of agent
type AgentType string

const (
	AgentTypeNative   AgentType = "native"
	AgentTypeExternal AgentType = "external"
	AgentTypeRemote   AgentType = "remote"
)

// RuntimeSpec configures how to run an external agent
type RuntimeSpec struct {
	Language string            `yaml:"language"`
	Command  string            `yaml:"command"`
	Args     []string          `yaml:"args,omitempty"`
	WorkDir  string            `yaml:"workDir,omitempty"`
	Env      map[string]string `yaml:"env,omitempty"`
}

// NetworkSpec configures agent networking
type NetworkSpec struct {
	Port     int    `yaml:"port"`
	Host     string `yaml:"host"`
	Protocol string `yaml:"protocol"`
}

// CapabilitySpec defines an agent capability
type CapabilitySpec struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description,omitempty"`
	InputSchema map[string]any `yaml:"inputSchema,omitempty"`
}

// ResourceSpec defines resource limits
type ResourceSpec struct {
	Memory string `yaml:"memory"` // e.g., "512Mi"
	CPU    string `yaml:"cpu"`    // e.g., "500m"
}

// AuthSpec configures authentication
type AuthSpec struct {
	Type     string `yaml:"type"` // mtls, bearer, api_key
	CertFile string `yaml:"certFile,omitempty"`
	KeyFile  string `yaml:"keyFile,omitempty"`
	CAFile   string `yaml:"caFile,omitempty"`
	Token    string `yaml:"token,omitempty"`
}

// ManifestLoader loads and validates YAML manifests
type ManifestLoader struct {
	envResolver EnvResolver
}

// EnvResolver resolves environment variable references
type EnvResolver interface {
	Resolve(value string) string
	ResolveAll(content string) string
}

// OSEnvResolver resolves ${VAR} patterns from OS environment
type OSEnvResolver struct{}

// Resolve resolves a single environment variable
func (r *OSEnvResolver) Resolve(value string) string {
	return os.ExpandEnv(value)
}

// ResolveAll resolves all environment variables in content
func (r *OSEnvResolver) ResolveAll(content string) string {
	return os.ExpandEnv(content)
}

// NewManifestLoader creates a new manifest loader
func NewManifestLoader() *ManifestLoader {
	return &ManifestLoader{
		envResolver: &OSEnvResolver{},
	}
}

// Load parses a manifest file
func (l *ManifestLoader) Load(path string) (*AgentManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	// Resolve environment variables
	resolved := l.envResolver.ResolveAll(string(data))

	var manifest AgentManifest
	if err := yaml.Unmarshal([]byte(resolved), &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	// Validate
	if err := l.validate(&manifest); err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}

	// Merge defaults into agent specs
	l.mergeDefaults(&manifest)

	return &manifest, nil
}

func (l *ManifestLoader) validate(m *AgentManifest) error {
	if m.APIVersion != "dagens.io/v1" {
		return fmt.Errorf("unsupported apiVersion: %s", m.APIVersion)
	}
	if m.Kind != "AgentManifest" && m.Kind != "OrchestrationFlow" {
		return fmt.Errorf("unsupported kind: %s", m.Kind)
	}
	for _, agent := range m.Spec.Agents {
		if err := l.validateAgent(&agent); err != nil {
			return fmt.Errorf("agent %s: %w", agent.Name, err)
		}
	}
	return nil
}

func (l *ManifestLoader) validateAgent(a *AgentSpec) error {
	if a.Name == "" {
		return fmt.Errorf("name is required")
	}
	switch a.Type {
	case AgentTypeExternal:
		if a.Runtime == nil || a.Runtime.Command == "" {
			return fmt.Errorf("external agents require runtime.command")
		}
	case AgentTypeRemote:
		if a.Endpoint == "" {
			return fmt.Errorf("remote agents require endpoint")
		}
	case AgentTypeNative:
		// Native agents are in-process, no external config needed
	default:
		return fmt.Errorf("invalid type: %s", a.Type)
	}
	return nil
}

func (l *ManifestLoader) mergeDefaults(m *AgentManifest) {
	for i := range m.Spec.Agents {
		agent := &m.Spec.Agents[i]

		// Merge health check defaults
		if !agent.Defaults.HealthCheck.Enabled && m.Spec.Defaults.HealthCheck.Enabled {
			agent.Defaults.HealthCheck = m.Spec.Defaults.HealthCheck
		}

		// Merge retry defaults
		if agent.Defaults.Retry.MaxAttempts == 0 && m.Spec.Defaults.Retry.MaxAttempts > 0 {
			agent.Defaults.Retry = m.Spec.Defaults.Retry
		}
	}
}

// OrchestrationFlow represents a workflow definition
type OrchestrationFlow struct {
	APIVersion string       `yaml:"apiVersion"`
	Kind       string       `yaml:"kind"`
	Metadata   ManifestMeta `yaml:"metadata"`
	Spec       FlowSpec     `yaml:"spec"`
}

// FlowSpec defines the flow specification
type FlowSpec struct {
	Entrypoint string         `yaml:"entrypoint"`
	Input      map[string]any `yaml:"input"`
	Nodes      []FlowNode     `yaml:"nodes"`
}

// FlowNode defines a node in the flow graph
type FlowNode struct {
	ID         string              `yaml:"id"`
	Type       FlowNodeType        `yaml:"type"` // input, output, condition, agent
	Agent      string              `yaml:"agent,omitempty"`
	Action     string              `yaml:"action,omitempty"`
	Input      map[string]any      `yaml:"input,omitempty"`
	Next       []string            `yaml:"next,omitempty"`
	Timeout    time.Duration       `yaml:"timeout,omitempty"`
	Retry      *RetryConfig        `yaml:"retry,omitempty"`
	Expression string              `yaml:"expression,omitempty"` // For conditions
	Branches   map[string][]string `yaml:"branches,omitempty"`
	Output     map[string]any      `yaml:"output,omitempty"` // For output nodes
}

// FlowNodeType defines the type of flow node
type FlowNodeType string

const (
	NodeTypeInput     FlowNodeType = "input"
	NodeTypeOutput    FlowNodeType = "output"
	NodeTypeCondition FlowNodeType = "condition"
	NodeTypeAgent     FlowNodeType = "agent"
	NodeTypeParallel  FlowNodeType = "parallel"
)

// LoadFlow loads an orchestration flow from a file
func (l *ManifestLoader) LoadFlow(path string) (*OrchestrationFlow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read flow: %w", err)
	}

	// Resolve environment variables
	resolved := l.envResolver.ResolveAll(string(data))

	var flow OrchestrationFlow
	if err := yaml.Unmarshal([]byte(resolved), &flow); err != nil {
		return nil, fmt.Errorf("failed to parse flow: %w", err)
	}

	// Validate
	if flow.APIVersion != "dagens.io/v1" {
		return nil, fmt.Errorf("unsupported apiVersion: %s", flow.APIVersion)
	}
	if flow.Kind != "OrchestrationFlow" {
		return nil, fmt.Errorf("unsupported kind: %s", flow.Kind)
	}

	return &flow, nil
}
