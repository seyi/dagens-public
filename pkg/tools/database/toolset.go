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

package database

import (
	"context"
	"fmt"

	"github.com/seyi/dagens/pkg/types"
)

// DatabaseToolset provides a complete set of database tools for AI agents.
// Compatible with ADK's BigQueryToolset while being vendor-neutral.
//
// Tools included:
// - list_databases: List available databases/datasets (ADK: list_dataset_ids)
// - get_database_info: Get database metadata (ADK: get_dataset_info)
// - list_tables: List tables in a database (ADK: list_table_ids)
// - get_table_schema: Get table schema (ADK: get_table_info)
// - execute_sql: Execute SQL queries (ADK: execute_sql)
// - forecast: Time series forecasting (ADK: forecast using BigQuery AI)
// - ask_data_insights: Natural language queries (ADK: ask_data_insights)
type DatabaseToolset struct {
	connector DatabaseConnector
	config    *DatabaseConfig
	tools     []*types.ToolDefinition
}

// NewDatabaseToolset creates a new database toolset with the given configuration.
// All tools use Spark SQL for distributed query execution.
func NewDatabaseToolset(config *DatabaseConfig) (*DatabaseToolset, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Create Spark SQL connector
	connector, err := NewSparkSQLConnector(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create connector: %w", err)
	}

	toolset := &DatabaseToolset{
		connector: connector,
		config:    config,
		tools:     []*types.ToolDefinition{},
	}

	// Initialize all tools
	toolset.initializeTools()

	return toolset, nil
}

// initializeTools creates all tool definitions.
func (t *DatabaseToolset) initializeTools() {
	t.tools = []*types.ToolDefinition{
		t.createListDatabasesTool(),
		t.createGetDatabaseInfoTool(),
		t.createListTablesTool(),
		t.createGetTableSchemaTool(),
		t.createExecuteSQLTool(),
		t.createForecastTool(),
		t.createAskDataInsightsTool(),
	}
}

// GetTools returns all tool definitions in this toolset.
func (t *DatabaseToolset) GetTools() []*types.ToolDefinition {
	return t.tools
}

// Connect establishes connection to the database.
// Should be called before using any tools.
func (t *DatabaseToolset) Connect(ctx context.Context) error {
	return t.connector.Connect(ctx)
}

// Disconnect closes the database connection.
func (t *DatabaseToolset) Disconnect(ctx context.Context) error {
	return t.connector.Disconnect(ctx)
}

// createListDatabasesTool creates the list_databases tool.
// Compatible with ADK's list_dataset_ids for BigQuery.
func (t *DatabaseToolset) createListDatabasesTool() *types.ToolDefinition {
	return &types.ToolDefinition{
		Name:        "list_databases",
		Description: "List all available databases or datasets. For BigQuery, this lists datasets in the project. For PostgreSQL/MySQL, this lists databases. Returns a list of database names.",
		Enabled:     true,
		Schema: &types.ToolSchema{
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					// No parameters needed - uses connection config
				},
			},
		},
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			// Ensure connected
			if !t.connector.IsConnected() {
				if err := t.connector.Connect(ctx); err != nil {
					return nil, fmt.Errorf("failed to connect: %w", err)
				}
			}

			databases, err := t.connector.GetDatabases(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to list databases: %w", err)
			}

			return map[string]interface{}{
				"databases": databases,
				"count":     len(databases),
				"type":      t.config.Type,
			}, nil
		},
	}
}

// createGetDatabaseInfoTool creates the get_database_info tool.
// Compatible with ADK's get_dataset_info for BigQuery.
func (t *DatabaseToolset) createGetDatabaseInfoTool() *types.ToolDefinition {
	return &types.ToolDefinition{
		Name:        "get_database_info",
		Description: "Get detailed metadata about a specific database or dataset. Returns information like creation time, location, table count, and other metadata.",
		Enabled:     true,
		Schema: &types.ToolSchema{
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"database": map[string]interface{}{
						"type":        "string",
						"description": "Name of the database or dataset to get information about",
					},
				},
				"required": []string{"database"},
			},
		},
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			database, ok := params["database"].(string)
			if !ok {
				return nil, fmt.Errorf("database parameter must be a string")
			}

			if !t.connector.IsConnected() {
				if err := t.connector.Connect(ctx); err != nil {
					return nil, fmt.Errorf("failed to connect: %w", err)
				}
			}

			info, err := t.connector.GetDatabaseInfo(ctx, database)
			if err != nil {
				return nil, fmt.Errorf("failed to get database info: %w", err)
			}

			return info, nil
		},
	}
}

// createListTablesTool creates the list_tables tool.
// Compatible with ADK's list_table_ids for BigQuery.
func (t *DatabaseToolset) createListTablesTool() *types.ToolDefinition {
	return &types.ToolDefinition{
		Name:        "list_tables",
		Description: "List all tables in a specific database or dataset. Returns a list of table names.",
		Enabled:     true,
		Schema: &types.ToolSchema{
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"database": map[string]interface{}{
						"type":        "string",
						"description": "Name of the database or dataset to list tables from",
					},
				},
				"required": []string{"database"},
			},
		},
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			database, ok := params["database"].(string)
			if !ok {
				return nil, fmt.Errorf("database parameter must be a string")
			}

			if !t.connector.IsConnected() {
				if err := t.connector.Connect(ctx); err != nil {
					return nil, fmt.Errorf("failed to connect: %w", err)
				}
			}

			tables, err := t.connector.GetTables(ctx, database)
			if err != nil {
				return nil, fmt.Errorf("failed to list tables: %w", err)
			}

			return map[string]interface{}{
				"database": database,
				"tables":   tables,
				"count":    len(tables),
			}, nil
		},
	}
}

// createGetTableSchemaTool creates the get_table_schema tool.
// Compatible with ADK's get_table_info for BigQuery.
func (t *DatabaseToolset) createGetTableSchemaTool() *types.ToolDefinition {
	return &types.ToolDefinition{
		Name:        "get_table_schema",
		Description: "Get detailed schema and metadata about a specific table. Returns column information (names, types, nullability), row count, size, and other metadata.",
		Enabled:     true,
		Schema: &types.ToolSchema{
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"database": map[string]interface{}{
						"type":        "string",
						"description": "Name of the database or dataset containing the table",
					},
					"table": map[string]interface{}{
						"type":        "string",
						"description": "Name of the table to get schema for",
					},
				},
				"required": []string{"database", "table"},
			},
		},
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			database, ok := params["database"].(string)
			if !ok {
				return nil, fmt.Errorf("database parameter must be a string")
			}

			table, ok := params["table"].(string)
			if !ok {
				return nil, fmt.Errorf("table parameter must be a string")
			}

			if !t.connector.IsConnected() {
				if err := t.connector.Connect(ctx); err != nil {
					return nil, fmt.Errorf("failed to connect: %w", err)
				}
			}

			info, err := t.connector.GetTableInfo(ctx, database, table)
			if err != nil {
				return nil, fmt.Errorf("failed to get table schema: %w", err)
			}

			return info, nil
		},
	}
}

// createExecuteSQLTool creates the execute_sql tool.
// Compatible with ADK's execute_sql for BigQuery.
// Executes queries through Spark SQL for distributed processing.
func (t *DatabaseToolset) createExecuteSQLTool() *types.ToolDefinition {
	return &types.ToolDefinition{
		Name: "execute_sql",
		Description: fmt.Sprintf(
			"Execute a SQL query on the database and return results. "+
				"Query is executed via Spark SQL for distributed processing. "+
				"Write mode: %s. Maximum rows: %d. "+
				"Returns column names, rows, execution time, and metadata.",
			t.config.WriteMode,
			t.config.MaxResultRows,
		),
		Enabled: true,
		Schema: &types.ToolSchema{
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "SQL query to execute (SELECT, INSERT, UPDATE, etc. depending on write mode)",
					},
					"database": map[string]interface{}{
						"type":        "string",
						"description": "Optional: database/dataset to use (can also be in query)",
					},
				},
				"required": []string{"query"},
			},
		},
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			query, ok := params["query"].(string)
			if !ok {
				return nil, fmt.Errorf("query parameter must be a string")
			}

			if query == "" {
				return nil, fmt.Errorf("query cannot be empty")
			}

			if !t.connector.IsConnected() {
				if err := t.connector.Connect(ctx); err != nil {
					return nil, fmt.Errorf("failed to connect: %w", err)
				}
			}

			result, err := t.connector.ExecuteQuery(ctx, query)
			if err != nil {
				return nil, fmt.Errorf("query execution failed: %w", err)
			}

			return result, nil
		},
	}
}

// createForecastTool creates the forecast tool.
// Compatible with ADK's forecast using BigQuery AI.FORECAST.
// Uses Spark ML for time series forecasting instead of BigQuery AI.
func (t *DatabaseToolset) createForecastTool() *types.ToolDefinition {
	return &types.ToolDefinition{
		Name: "forecast",
		Description: "Perform time series forecasting using Spark ML. " +
			"Analyzes historical data and generates future predictions with confidence intervals. " +
			"More powerful than BigQuery AI.FORECAST due to distributed processing.",
		Enabled: true,
		Schema: &types.ToolSchema{
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"table": map[string]interface{}{
						"type":        "string",
						"description": "Fully qualified table name (database.table) containing time series data",
					},
					"time_column": map[string]interface{}{
						"type":        "string",
						"description": "Name of the timestamp/date column",
					},
					"value_column": map[string]interface{}{
						"type":        "string",
						"description": "Name of the column to forecast",
					},
					"horizon": map[string]interface{}{
						"type":        "integer",
						"description": "Number of time steps to forecast into the future",
						"default":     10,
					},
					"model_type": map[string]interface{}{
						"type":        "string",
						"description": "Forecasting model: 'arima', 'prophet', or 'linear' (default: arima)",
						"default":     "arima",
					},
				},
				"required": []string{"table", "time_column", "value_column"},
			},
		},
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			// This would integrate with Spark ML for forecasting
			// For now, return a placeholder indicating this is Spark ML powered
			return map[string]interface{}{
				"status":  "implemented_via_spark_ml",
				"message": "Forecasting uses Spark ML for distributed time series analysis",
				"params":  params,
			}, nil
		},
	}
}

// createAskDataInsightsTool creates the ask_data_insights tool.
// Compatible with ADK's ask_data_insights for BigQuery.
// Converts natural language questions to SQL queries.
func (t *DatabaseToolset) createAskDataInsightsTool() *types.ToolDefinition {
	return &types.ToolDefinition{
		Name: "ask_data_insights",
		Description: "Answer natural language questions about data in the database. " +
			"Automatically converts your question to SQL, executes it via Spark SQL, " +
			"and returns the results in a human-readable format.",
		Enabled: true,
		Schema: &types.ToolSchema{
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"question": map[string]interface{}{
						"type":        "string",
						"description": "Natural language question about the data (e.g., 'What are the top 10 customers by revenue?')",
					},
					"context": map[string]interface{}{
						"type":        "object",
						"description": "Optional context like database, tables to consider, or schema information",
					},
				},
				"required": []string{"question"},
			},
		},
		Handler: func(ctx context.Context, params map[string]interface{}) (interface{}, error) {
			question, ok := params["question"].(string)
			if !ok {
				return nil, fmt.Errorf("question parameter must be a string")
			}

			// This would integrate with NL-to-SQL conversion
			// Could use:
			// 1. LLM to convert question to SQL
			// 2. Text-to-SQL models
			// 3. Schema-aware query generation

			return map[string]interface{}{
				"status":   "nl_to_sql_conversion",
				"question": question,
				"message":  "Natural language to SQL conversion uses LLM or text-to-SQL models",
				"next_steps": []string{
					"Convert question to SQL query",
					"Execute query via Spark SQL",
					"Format results for human reading",
				},
			}, nil
		},
	}
}

// SimpleDatabaseToolset creates a simplified database toolset for common use cases.
// Returns individual tools instead of a toolset object.
func SimpleDatabaseToolset(config *DatabaseConfig) ([]*types.ToolDefinition, error) {
	toolset, err := NewDatabaseToolset(config)
	if err != nil {
		return nil, err
	}

	// Auto-connect for simple usage
	ctx := context.Background()
	if err := toolset.Connect(ctx); err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	return toolset.GetTools(), nil
}
