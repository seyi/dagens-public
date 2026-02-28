package manifest

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseAgentManifest_Python(t *testing.T) {
	yaml := `
apiVersion: dagens.io/v1
kind: Agent
metadata:
  name: research-agent
  labels:
    framework: crewai
spec:
  runtime:
    type: python
    python:
      framework: crewai
      entryPoint: agents/research.py
      venvPath: .venv
      requirements: requirements.txt
  capabilities:
    - research
    - web_search
  communication:
    port: 8081
    streaming: true
  healthCheck:
    endpoint: /health
    interval: 30s
  env:
    - name: OPENAI_API_KEY
      secretRef:
        name: api-keys
        key: openai
`

	parser := NewParser("")
	manifest, err := parser.ParseAgentManifestBytes([]byte(yaml))

	assert.NoError(t, err)
	assert.Equal(t, "dagens.io/v1", manifest.APIVersion)
	assert.Equal(t, "Agent", manifest.Kind)
	assert.Equal(t, "research-agent", manifest.Metadata.Name)
	assert.Equal(t, "crewai", manifest.Metadata.Labels["framework"])

	assert.Equal(t, RuntimeTypePython, manifest.Spec.Runtime.Type)
	assert.Equal(t, "crewai", manifest.Spec.Runtime.Python.Framework)
	assert.Equal(t, "agents/research.py", manifest.Spec.Runtime.Python.EntryPoint)
	assert.Equal(t, ".venv", manifest.Spec.Runtime.Python.VenvPath)

	assert.Contains(t, manifest.Spec.Capabilities, "research")
	assert.Contains(t, manifest.Spec.Capabilities, "web_search")

	assert.Equal(t, 8081, manifest.Spec.Communication.Port)
	assert.True(t, manifest.Spec.Communication.Streaming)

	assert.Equal(t, "/health", manifest.Spec.HealthCheck.Endpoint)
	assert.Equal(t, "30s", manifest.Spec.HealthCheck.Interval)

	assert.Len(t, manifest.Spec.Env, 1)
	assert.Equal(t, "OPENAI_API_KEY", manifest.Spec.Env[0].Name)
	assert.Equal(t, "api-keys", manifest.Spec.Env[0].SecretRef.Name)
}

func TestParseAgentManifest_Docker(t *testing.T) {
	yaml := `
apiVersion: dagens.io/v1
kind: Agent
metadata:
  name: docker-agent
spec:
  runtime:
    type: docker
    docker:
      image: myorg/agent:latest
      pullPolicy: IfNotPresent
      ports:
        - containerPort: 8080
          hostPort: 9090
      volumes:
        - hostPath: /data
          containerPath: /app/data
  capabilities:
    - process
`

	parser := NewParser("")
	manifest, err := parser.ParseAgentManifestBytes([]byte(yaml))

	assert.NoError(t, err)
	assert.Equal(t, RuntimeTypeDocker, manifest.Spec.Runtime.Type)
	assert.Equal(t, "myorg/agent:latest", manifest.Spec.Runtime.Docker.Image)
	assert.Equal(t, "IfNotPresent", manifest.Spec.Runtime.Docker.PullPolicy)
	assert.Len(t, manifest.Spec.Runtime.Docker.Ports, 1)
	assert.Equal(t, 8080, manifest.Spec.Runtime.Docker.Ports[0].ContainerPort)
	assert.Len(t, manifest.Spec.Runtime.Docker.Volumes, 1)
}

func TestParseAgentManifest_External(t *testing.T) {
	yaml := `
apiVersion: dagens.io/v1
kind: Agent
metadata:
  name: external-agent
spec:
  runtime:
    type: external
    external:
      endpoint: http://external-service:8080/a2a
      auth:
        type: bearer
        token: ${EXTERNAL_TOKEN}
  capabilities:
    - external_service
`

	parser := NewParser("")
	manifest, err := parser.ParseAgentManifestBytes([]byte(yaml))

	assert.NoError(t, err)
	assert.Equal(t, RuntimeTypeExternal, manifest.Spec.Runtime.Type)
	assert.Equal(t, "http://external-service:8080/a2a", manifest.Spec.Runtime.External.Endpoint)
	assert.Equal(t, "bearer", manifest.Spec.Runtime.External.Auth.Type)
}

func TestParseAgentManifest_Go(t *testing.T) {
	yaml := `
apiVersion: dagens.io/v1
kind: Agent
metadata:
  name: go-agent
spec:
  runtime:
    type: go
    go:
      package: github.com/myorg/agents/coordinator
      buildTags:
        - production
`

	parser := NewParser("")
	manifest, err := parser.ParseAgentManifestBytes([]byte(yaml))

	assert.NoError(t, err)
	assert.Equal(t, RuntimeTypeGo, manifest.Spec.Runtime.Type)
	assert.Equal(t, "github.com/myorg/agents/coordinator", manifest.Spec.Runtime.Go.Package)
	assert.Contains(t, manifest.Spec.Runtime.Go.BuildTags, "production")
}

func TestParseAgentManifest_ValidationErrors(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		expectErr string
	}{
		{
			name: "missing apiVersion",
			yaml: `
kind: Agent
metadata:
  name: test
spec:
  runtime:
    type: python
    python:
      entryPoint: main.py
`,
			expectErr: "apiVersion is required",
		},
		{
			name: "wrong kind",
			yaml: `
apiVersion: dagens.io/v1
kind: WrongKind
metadata:
  name: test
spec:
  runtime:
    type: python
    python:
      entryPoint: main.py
`,
			expectErr: "kind must be 'Agent'",
		},
		{
			name: "missing name",
			yaml: `
apiVersion: dagens.io/v1
kind: Agent
metadata:
  labels:
    foo: bar
spec:
  runtime:
    type: python
    python:
      entryPoint: main.py
`,
			expectErr: "metadata.name is required",
		},
		{
			name: "missing runtime type",
			yaml: `
apiVersion: dagens.io/v1
kind: Agent
metadata:
  name: test
spec:
  runtime:
    python:
      entryPoint: main.py
`,
			expectErr: "spec.runtime.type is required",
		},
		{
			name: "missing python config",
			yaml: `
apiVersion: dagens.io/v1
kind: Agent
metadata:
  name: test
spec:
  runtime:
    type: python
`,
			expectErr: "spec.runtime.python is required",
		},
		{
			name: "missing docker image",
			yaml: `
apiVersion: dagens.io/v1
kind: Agent
metadata:
  name: test
spec:
  runtime:
    type: docker
    docker:
      pullPolicy: Always
`,
			expectErr: "spec.runtime.docker.image is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewParser("")
			_, err := parser.ParseAgentManifestBytes([]byte(tt.yaml))

			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectErr)
		})
	}
}

func TestParseOrchestrationManifest(t *testing.T) {
	yaml := `
apiVersion: dagens.io/v1
kind: Orchestration
metadata:
  name: travel-booking
  namespace: production
spec:
  entryPoint: coordinator
  agents:
    - name: coordinator
      manifest: agents/coordinator.yaml
    - name: flight-agent
      manifest: agents/flight.yaml
    - name: hotel-agent
      manifest: agents/hotel.yaml
  transfers:
    - from: coordinator
      to:
        - flight-agent
        - hotel-agent
      condition: user requests booking
  settings:
    maxTransferDepth: 5
    timeout: 5m
    retryPolicy:
      maxRetries: 3
      backoff: exponential
`

	parser := NewParser("")
	manifest, err := parser.ParseOrchestrationManifestBytes([]byte(yaml))

	assert.NoError(t, err)
	assert.Equal(t, "dagens.io/v1", manifest.APIVersion)
	assert.Equal(t, "Orchestration", manifest.Kind)
	assert.Equal(t, "travel-booking", manifest.Metadata.Name)
	assert.Equal(t, "production", manifest.Metadata.Namespace)

	assert.Equal(t, "coordinator", manifest.Spec.EntryPoint)
	assert.Len(t, manifest.Spec.Agents, 3)

	assert.Len(t, manifest.Spec.Transfers, 1)
	assert.Equal(t, "coordinator", manifest.Spec.Transfers[0].From)
	assert.Contains(t, manifest.Spec.Transfers[0].To, "flight-agent")

	assert.Equal(t, 5, manifest.Spec.Settings.MaxTransferDepth)
	assert.Equal(t, "5m", manifest.Spec.Settings.Timeout)
	assert.Equal(t, 3, manifest.Spec.Settings.RetryPolicy.MaxRetries)
}

func TestParseOrchestrationManifest_ValidationErrors(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		expectErr string
	}{
		{
			name: "missing entryPoint",
			yaml: `
apiVersion: dagens.io/v1
kind: Orchestration
metadata:
  name: test
spec:
  agents:
    - name: agent1
`,
			expectErr: "spec.entryPoint is required",
		},
		{
			name: "empty agents",
			yaml: `
apiVersion: dagens.io/v1
kind: Orchestration
metadata:
  name: test
spec:
  entryPoint: coordinator
  agents: []
`,
			expectErr: "spec.agents must have at least one agent",
		},
		{
			name: "entryPoint not in agents",
			yaml: `
apiVersion: dagens.io/v1
kind: Orchestration
metadata:
  name: test
spec:
  entryPoint: missing-agent
  agents:
    - name: agent1
`,
			expectErr: "entryPoint 'missing-agent' not found in agents list",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := NewParser("")
			_, err := parser.ParseOrchestrationManifestBytes([]byte(tt.yaml))

			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectErr)
		})
	}
}

func TestParseAgentManifestReader(t *testing.T) {
	yaml := `
apiVersion: dagens.io/v1
kind: Agent
metadata:
  name: reader-test
spec:
  runtime:
    type: external
    external:
      endpoint: http://localhost:8080
`

	parser := NewParser("")
	manifest, err := parser.ParseAgentManifestReader(strings.NewReader(yaml))

	assert.NoError(t, err)
	assert.Equal(t, "reader-test", manifest.Metadata.Name)
}

func TestAgentManifestToYAML(t *testing.T) {
	manifest := &AgentManifest{
		APIVersion: "dagens.io/v1",
		Kind:       "Agent",
		Metadata: AgentMetadata{
			Name: "test-agent",
			Labels: map[string]string{
				"env": "test",
			},
		},
		Spec: AgentSpec{
			Runtime: RuntimeSpec{
				Type: RuntimeTypePython,
				Python: &PythonRuntimeSpec{
					Framework:  "crewai",
					EntryPoint: "main.py",
				},
			},
			Capabilities: []string{"test"},
		},
	}

	yamlBytes, err := AgentManifestToYAML(manifest)
	assert.NoError(t, err)

	yamlStr := string(yamlBytes)
	assert.Contains(t, yamlStr, "apiVersion: dagens.io/v1")
	assert.Contains(t, yamlStr, "kind: Agent")
	assert.Contains(t, yamlStr, "name: test-agent")
	assert.Contains(t, yamlStr, "framework: crewai")
}

func TestRuntimeTypes(t *testing.T) {
	assert.Equal(t, RuntimeType("python"), RuntimeTypePython)
	assert.Equal(t, RuntimeType("go"), RuntimeTypeGo)
	assert.Equal(t, RuntimeType("docker"), RuntimeTypeDocker)
	assert.Equal(t, RuntimeType("external"), RuntimeTypeExternal)
}

func TestAgentPhases(t *testing.T) {
	phases := []AgentPhase{
		AgentPhasePending,
		AgentPhaseStarting,
		AgentPhaseRunning,
		AgentPhaseHealthy,
		AgentPhaseUnhealthy,
		AgentPhaseStopping,
		AgentPhaseStopped,
		AgentPhaseFailed,
	}

	for _, phase := range phases {
		assert.NotEmpty(t, string(phase))
	}
}
