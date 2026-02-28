// Package manifest provides YAML manifest parsing and validation
package manifest

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Parser parses agent and orchestration manifests
type Parser struct {
	basePath string
}

// NewParser creates a new manifest parser
func NewParser(basePath string) *Parser {
	return &Parser{
		basePath: basePath,
	}
}

// ParseAgentManifest parses an agent manifest from a file
func (p *Parser) ParseAgentManifest(path string) (*AgentManifest, error) {
	fullPath := p.resolvePath(path)

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	return p.ParseAgentManifestBytes(data)
}

// ParseAgentManifestBytes parses an agent manifest from bytes
func (p *Parser) ParseAgentManifestBytes(data []byte) (*AgentManifest, error) {
	var manifest AgentManifest

	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	if err := p.validateAgentManifest(&manifest); err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}

	return &manifest, nil
}

// ParseAgentManifestReader parses an agent manifest from a reader
func (p *Parser) ParseAgentManifestReader(r io.Reader) (*AgentManifest, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read: %w", err)
	}

	return p.ParseAgentManifestBytes(data)
}

// ParseOrchestrationManifest parses an orchestration manifest from a file
func (p *Parser) ParseOrchestrationManifest(path string) (*OrchestrationManifest, error) {
	fullPath := p.resolvePath(path)

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	return p.ParseOrchestrationManifestBytes(data)
}

// ParseOrchestrationManifestBytes parses an orchestration manifest from bytes
func (p *Parser) ParseOrchestrationManifestBytes(data []byte) (*OrchestrationManifest, error) {
	var manifest OrchestrationManifest

	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	if err := p.validateOrchestrationManifest(&manifest); err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}

	return &manifest, nil
}

// ParseAny parses either an agent or orchestration manifest
func (p *Parser) ParseAny(path string) (interface{}, error) {
	fullPath := p.resolvePath(path)

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	// First, parse to determine kind
	var base struct {
		Kind string `yaml:"kind"`
	}

	if err := yaml.Unmarshal(data, &base); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	switch base.Kind {
	case "Agent":
		return p.ParseAgentManifestBytes(data)
	case "Orchestration":
		return p.ParseOrchestrationManifestBytes(data)
	default:
		return nil, fmt.Errorf("unknown manifest kind: %s", base.Kind)
	}
}

// ParseDirectory parses all manifests in a directory
func (p *Parser) ParseDirectory(dir string) ([]*AgentManifest, []*OrchestrationManifest, error) {
	fullPath := p.resolvePath(dir)

	var agents []*AgentManifest
	var orchestrations []*OrchestrationManifest

	err := filepath.Walk(fullPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		// Only process YAML files
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".yaml" && ext != ".yml" {
			return nil
		}

		manifest, err := p.ParseAny(path)
		if err != nil {
			return fmt.Errorf("failed to parse %s: %w", path, err)
		}

		switch m := manifest.(type) {
		case *AgentManifest:
			agents = append(agents, m)
		case *OrchestrationManifest:
			orchestrations = append(orchestrations, m)
		}

		return nil
	})

	if err != nil {
		return nil, nil, err
	}

	return agents, orchestrations, nil
}

// validateAgentManifest validates an agent manifest
func (p *Parser) validateAgentManifest(m *AgentManifest) error {
	if m.APIVersion == "" {
		return fmt.Errorf("apiVersion is required")
	}

	if m.Kind != "Agent" {
		return fmt.Errorf("kind must be 'Agent', got '%s'", m.Kind)
	}

	if m.Metadata.Name == "" {
		return fmt.Errorf("metadata.name is required")
	}

	if m.Spec.Runtime.Type == "" {
		return fmt.Errorf("spec.runtime.type is required")
	}

	// Validate runtime-specific config
	switch m.Spec.Runtime.Type {
	case RuntimeTypePython:
		if m.Spec.Runtime.Python == nil {
			return fmt.Errorf("spec.runtime.python is required for python runtime")
		}
		if m.Spec.Runtime.Python.EntryPoint == "" {
			return fmt.Errorf("spec.runtime.python.entryPoint is required")
		}

	case RuntimeTypeGo:
		if m.Spec.Runtime.Go == nil {
			return fmt.Errorf("spec.runtime.go is required for go runtime")
		}
		if m.Spec.Runtime.Go.Package == "" && m.Spec.Runtime.Go.Binary == "" {
			return fmt.Errorf("spec.runtime.go.package or spec.runtime.go.binary is required")
		}

	case RuntimeTypeDocker:
		if m.Spec.Runtime.Docker == nil {
			return fmt.Errorf("spec.runtime.docker is required for docker runtime")
		}
		if m.Spec.Runtime.Docker.Image == "" {
			return fmt.Errorf("spec.runtime.docker.image is required")
		}

	case RuntimeTypeExternal:
		if m.Spec.Runtime.External == nil {
			return fmt.Errorf("spec.runtime.external is required for external runtime")
		}
		if m.Spec.Runtime.External.Endpoint == "" {
			return fmt.Errorf("spec.runtime.external.endpoint is required")
		}

	default:
		return fmt.Errorf("unknown runtime type: %s", m.Spec.Runtime.Type)
	}

	return nil
}

// validateOrchestrationManifest validates an orchestration manifest
func (p *Parser) validateOrchestrationManifest(m *OrchestrationManifest) error {
	if m.APIVersion == "" {
		return fmt.Errorf("apiVersion is required")
	}

	if m.Kind != "Orchestration" {
		return fmt.Errorf("kind must be 'Orchestration', got '%s'", m.Kind)
	}

	if m.Metadata.Name == "" {
		return fmt.Errorf("metadata.name is required")
	}

	if m.Spec.EntryPoint == "" {
		return fmt.Errorf("spec.entryPoint is required")
	}

	if len(m.Spec.Agents) == 0 {
		return fmt.Errorf("spec.agents must have at least one agent")
	}

	// Verify entry point is in agents list
	found := false
	for _, agent := range m.Spec.Agents {
		if agent.Name == m.Spec.EntryPoint {
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("entryPoint '%s' not found in agents list", m.Spec.EntryPoint)
	}

	return nil
}

// resolvePath resolves a path relative to the base path
func (p *Parser) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}

	if p.basePath == "" {
		return path
	}

	return filepath.Join(p.basePath, path)
}

// WriteAgentManifest writes an agent manifest to a file
func WriteAgentManifest(manifest *AgentManifest, path string) error {
	data, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

// WriteOrchestrationManifest writes an orchestration manifest to a file
func WriteOrchestrationManifest(manifest *OrchestrationManifest, path string) error {
	data, err := yaml.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

// AgentManifestToYAML converts an agent manifest to YAML bytes
func AgentManifestToYAML(manifest *AgentManifest) ([]byte, error) {
	return yaml.Marshal(manifest)
}

// OrchestrationManifestToYAML converts an orchestration manifest to YAML bytes
func OrchestrationManifestToYAML(manifest *OrchestrationManifest) ([]byte, error) {
	return yaml.Marshal(manifest)
}
