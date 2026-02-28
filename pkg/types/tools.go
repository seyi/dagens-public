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

package types

import "context"

// ToolDefinition defines a tool that an agent can use. It includes metadata
// about the tool, its input/output schema, and the handler function that
// implements its logic.
type ToolDefinition struct {
	// Name is the unique name of the tool.
	Name string
	// Description is a human-readable explanation of what the tool does.
	Description string
	// Schema defines the input and output parameters for the tool.
	Schema *ToolSchema
	// Handler is the function that executes the tool's logic.
	Handler ToolHandler
	// Enabled indicates whether the tool is currently available for use.
	Enabled bool
}

// ToolSchema defines the input and output schemas for a tool, typically as
// JSON schemas.
type ToolSchema struct {
	// InputSchema defines the structure of the tool's input parameters.
	InputSchema map[string]interface{}
	// OutputSchema defines the structure of the tool's output.
	OutputSchema map[string]interface{}
}

// ToolHandler is a function that executes a tool's logic with a given context
// and parameters.
type ToolHandler func(ctx context.Context, params map[string]interface{}) (interface{}, error)
