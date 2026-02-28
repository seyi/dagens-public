# Dagens Sandbox

This document describes the design of the sandbox configuration API.

## 1. Goals

-   Provide a flexible way to configure sandbox policies on a per-tool basis.
-   Support fine-grained control over network access, filesystem mounts, and resource limits.
-   Be easy to understand and use.

## 2. Configuration Schema

The sandbox configuration will be defined by the `Policy` struct in the `pkg/sandbox` package.

```go
// pkg/sandbox/policy.go

package sandbox

// Policy defines the security policy for a sandboxed tool.
type Policy struct {
    // Network specifies the network policy.
    Network *NetworkPolicy `json:"network,omitempty"`

    // Mounts specifies the filesystem mounts.
    Mounts []Mount `json:"mounts,omitempty"`

    // Resources specifies the resource limits.
    Resources *ResourcePolicy `json:"resources,omitempty"`
}

// NetworkPolicy defines the network access policy.
type NetworkPolicy struct {
    // Enabled enables or disables network access.
    Enabled bool `json:"enabled"`

    // AllowedHosts is a list of allowed outbound hosts. If empty, all hosts are allowed.
    AllowedHosts []string `json:"allowedHosts,omitempty"`
}

// Mount defines a filesystem mount.
type Mount struct {
    // Source is the path on the host.
    Source string `json:"source"`

    // Destination is the path in the sandbox.
    Destination string `json:"destination"`

    // ReadOnly specifies if the mount should be read-only.
    ReadOnly bool `json:"readOnly"`
}

// ResourcePolicy defines the resource limits.
type ResourcePolicy struct {
    // Cpus specifies the number of CPUs.
    Cpus float64 `json:"cpus,omitempty"`

    // Memory specifies the memory limit in megabytes.
    Memory int64 `json:"memory,omitempty"`
}
```

## 3. Example Usage

The following is an example of how a tool's sandbox policy could be defined in a configuration file:

```yaml
tools:
  - name: "file-writer"
    sandbox:
      mounts:
        - source: "/tmp/data"
          destination: "/data"
          readOnly: false
      resources:
        memory: 128
  - name: "api-caller"
    sandbox:
      network:
        enabled: true
        allowedHosts:
          - "api.example.com"
```
