// Copyright 2025 Apache Spark AI Agents
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law of agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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
