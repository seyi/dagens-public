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

// Package database provides vendor-neutral database tools for AI agents.
// Leverages Spark SQL for distributed query execution across multiple database types.
package database

import (
	"time"
)

// WriteMode controls what write operations are allowed on the database.
// Mirrors ADK's BigQueryToolConfig.WriteMode for compatibility.
type WriteMode string

const (
	// WriteModeBlocked prevents all write operations (safest for agent usage)
	WriteModeBlocked WriteMode = "BLOCKED"

	// WriteModeReadOnly allows SELECT queries only
	WriteModeReadOnly WriteMode = "READ_ONLY"

	// WriteModeFull allows all operations including INSERT, UPDATE, DELETE, CREATE, DROP
	// Use with extreme caution in agent contexts
	WriteModeFull WriteMode = "FULL"
)

// DatabaseType represents supported database types.
// All types are accessed through Spark SQL for unified interface.
type DatabaseType string

const (
	// DatabaseTypeBigQuery - Google BigQuery via Spark BigQuery connector
	DatabaseTypeBigQuery DatabaseType = "bigquery"

	// DatabaseTypePostgreSQL - PostgreSQL via Spark JDBC
	DatabaseTypePostgreSQL DatabaseType = "postgresql"

	// DatabaseTypeMySQL - MySQL via Spark JDBC
	DatabaseTypeMySQL DatabaseType = "mysql"

	// DatabaseTypeSnowflake - Snowflake via Spark connector
	DatabaseTypeSnowflake DatabaseType = "snowflake"

	// DatabaseTypeRedshift - Amazon Redshift via Spark JDBC
	DatabaseTypeRedshift DatabaseType = "redshift"

	// DatabaseTypeDatabricks - Databricks SQL via connector
	DatabaseTypeDatabricks DatabaseType = "databricks"

	// DatabaseTypeSQLServer - Microsoft SQL Server via Spark JDBC
	DatabaseTypeSQLServer DatabaseType = "sqlserver"

	// DatabaseTypeOracle - Oracle via Spark JDBC
	DatabaseTypeOracle DatabaseType = "oracle"

	// DatabaseTypeSpark - Spark SQL native (Hive, Delta Lake, Iceberg, etc.)
	DatabaseTypeSpark DatabaseType = "spark"
)

// DatabaseConfig configures database access for the toolset.
// Supports multiple database types through Spark SQL abstraction.
type DatabaseConfig struct {
	// Type specifies the database type (bigquery, postgresql, snowflake, etc.)
	Type DatabaseType

	// ConnectionString is the database connection string.
	// Format depends on database type:
	// - BigQuery: "bigquery://project/dataset"
	// - PostgreSQL: "postgresql://host:port/database"
	// - Snowflake: "snowflake://account/database/schema"
	ConnectionString string

	// Database name (optional, can be in connection string)
	Database string

	// Schema/Dataset name (optional, can be in connection string)
	Schema string

	// Credentials for authentication.
	// Type depends on database:
	// - BigQuery: *google.Credentials or ADC
	// - PostgreSQL: username/password in connection string
	// - Snowflake: SnowflakeCredentials
	Credentials interface{}

	// WriteMode controls allowed operations (BLOCKED, READ_ONLY, FULL)
	WriteMode WriteMode

	// --- Spark-specific configuration ---

	// SparkMaster is the Spark master URL (e.g., "local[*]", "spark://host:port")
	// If empty, uses existing Spark session or creates local session
	SparkMaster string

	// SparkAppName is the application name for the Spark session
	SparkAppName string

	// EnableCaching enables Spark SQL query result caching for performance
	EnableCaching bool

	// PartitionCount is the number of partitions for distributed queries
	// 0 means use Spark defaults
	PartitionCount int

	// QueryTimeout is the maximum time allowed for query execution
	QueryTimeout time.Duration

	// MaxResultRows limits the number of rows returned from queries
	// Prevents memory issues from large result sets
	MaxResultRows int

	// --- JDBC-specific configuration (PostgreSQL, MySQL, etc.) ---

	// JDBCDriver is the JDBC driver class name
	// e.g., "org.postgresql.Driver", "com.mysql.jdbc.Driver"
	JDBCDriver string

	// JDBCProperties are additional JDBC connection properties
	JDBCProperties map[string]string

	// --- BigQuery-specific configuration ---

	// BigQueryProject is the GCP project ID (for BigQuery)
	BigQueryProject string

	// BigQueryDataset is the default dataset (for BigQuery)
	BigQueryDataset string

	// BigQueryMaterializationProject is the project for query materialization
	BigQueryMaterializationProject string

	// BigQueryMaterializationDataset is the dataset for temp tables
	BigQueryMaterializationDataset string

	// --- Security and validation ---

	// AllowedDatabases restricts which databases agents can access
	// Empty means all databases accessible with credentials
	AllowedDatabases []string

	// AllowedTables restricts which tables agents can query
	// Format: "database.table" or "table" for current database
	// Empty means all tables allowed
	AllowedTables []string

	// BlockedKeywords prevents queries containing these SQL keywords
	// e.g., ["DROP", "TRUNCATE"] to prevent destructive operations
	BlockedKeywords []string

	// RequireWhereClause forces all SELECT queries to have WHERE clause
	// Prevents accidental full table scans
	RequireWhereClause bool
}

// DefaultDatabaseConfig returns a safe default configuration.
func DefaultDatabaseConfig(dbType DatabaseType, connectionString string) *DatabaseConfig {
	return &DatabaseConfig{
		Type:               dbType,
		ConnectionString:   connectionString,
		WriteMode:          WriteModeReadOnly, // Safe default
		SparkMaster:        "local[*]",        // Local Spark for testing
		SparkAppName:       "spark-ai-agents-db",
		EnableCaching:      true,
		PartitionCount:     4,
		QueryTimeout:       5 * time.Minute,
		MaxResultRows:      10000,              // Prevent memory issues
		BlockedKeywords:    []string{"DROP", "TRUNCATE", "DELETE"}, // Safe defaults
		RequireWhereClause: false,              // Can be strict if needed
	}
}

// TableInfo contains metadata about a database table.
// Compatible with ADK's get_table_info response.
type TableInfo struct {
	// Database/Dataset name
	Database string `json:"database"`

	// Schema name (for databases that support schemas)
	Schema string `json:"schema,omitempty"`

	// Table name
	Table string `json:"table"`

	// Fully qualified name (e.g., "project.dataset.table")
	FullyQualifiedName string `json:"fully_qualified_name"`

	// Table type (TABLE, VIEW, EXTERNAL, etc.)
	Type string `json:"type"`

	// Column information
	Columns []ColumnInfo `json:"columns"`

	// Row count (if available, -1 if unknown)
	RowCount int64 `json:"row_count"`

	// Size in bytes (if available, -1 if unknown)
	SizeBytes int64 `json:"size_bytes,omitempty"`

	// Creation timestamp
	CreatedAt *time.Time `json:"created_at,omitempty"`

	// Last modified timestamp
	ModifiedAt *time.Time `json:"modified_at,omitempty"`

	// Description/comment
	Description string `json:"description,omitempty"`

	// Additional metadata (database-specific)
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// ColumnInfo contains metadata about a table column.
type ColumnInfo struct {
	// Column name
	Name string `json:"name"`

	// Data type (SQL type string)
	DataType string `json:"data_type"`

	// Is nullable
	Nullable bool `json:"nullable"`

	// Is primary key
	IsPrimaryKey bool `json:"is_primary_key,omitempty"`

	// Default value
	DefaultValue *string `json:"default_value,omitempty"`

	// Description/comment
	Description string `json:"description,omitempty"`

	// Column position (ordinal)
	Position int `json:"position"`
}

// DatabaseInfo contains metadata about a database/dataset.
// Compatible with ADK's get_dataset_info response.
type DatabaseInfo struct {
	// Database/Dataset name
	Name string `json:"name"`

	// Fully qualified name (e.g., "project.dataset")
	FullyQualifiedName string `json:"fully_qualified_name"`

	// Database type
	Type DatabaseType `json:"type"`

	// Creation timestamp
	CreatedAt *time.Time `json:"created_at,omitempty"`

	// Last modified timestamp
	ModifiedAt *time.Time `json:"modified_at,omitempty"`

	// Description
	Description string `json:"description,omitempty"`

	// Location/region
	Location string `json:"location,omitempty"`

	// Table count
	TableCount int `json:"table_count,omitempty"`

	// Additional metadata
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// QueryResult represents the result of a SQL query execution.
type QueryResult struct {
	// Column names
	Columns []string `json:"columns"`

	// Row data (each row is a map of column name to value)
	Rows []map[string]interface{} `json:"rows"`

	// Number of rows returned
	RowCount int `json:"row_count"`

	// Execution time in seconds
	ExecutionTime float64 `json:"execution_time"`

	// Bytes processed (if available)
	BytesProcessed int64 `json:"bytes_processed,omitempty"`

	// Was result cached (Spark cache)
	Cached bool `json:"cached,omitempty"`

	// Query that was executed (sanitized)
	Query string `json:"query,omitempty"`

	// Any warnings
	Warnings []string `json:"warnings,omitempty"`
}

// ForecastResult represents the result of a time series forecast.
// Compatible with ADK's BigQuery AI.FORECAST result.
type ForecastResult struct {
	// Forecasted values
	Forecast []ForecastPoint `json:"forecast"`

	// Model metrics
	Metrics map[string]float64 `json:"metrics,omitempty"`

	// Model type used
	ModelType string `json:"model_type"`

	// Confidence intervals (if available)
	ConfidenceIntervals []ConfidenceInterval `json:"confidence_intervals,omitempty"`
}

// ForecastPoint represents a single forecasted data point.
type ForecastPoint struct {
	// Timestamp or index
	Timestamp interface{} `json:"timestamp"`

	// Forecasted value
	Value float64 `json:"value"`

	// Lower confidence bound
	LowerBound *float64 `json:"lower_bound,omitempty"`

	// Upper confidence bound
	UpperBound *float64 `json:"upper_bound,omitempty"`
}

// ConfidenceInterval represents a confidence interval for forecasts.
type ConfidenceInterval struct {
	// Confidence level (e.g., 0.95 for 95%)
	Level float64 `json:"level"`

	// Lower bound series
	Lower []float64 `json:"lower"`

	// Upper bound series
	Upper []float64 `json:"upper"`
}
