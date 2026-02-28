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
	"strings"
)

// DatabaseConnector provides vendor-neutral database access through Spark SQL.
// All database operations are executed via Spark for distributed processing.
type DatabaseConnector interface {
	// Connect establishes connection to the database
	Connect(ctx context.Context) error

	// Disconnect closes the database connection
	Disconnect(ctx context.Context) error

	// IsConnected returns true if currently connected
	IsConnected() bool

	// ExecuteQuery executes a SQL query and returns results
	ExecuteQuery(ctx context.Context, query string) (*QueryResult, error)

	// GetDatabases lists all accessible databases/datasets
	GetDatabases(ctx context.Context) ([]string, error)

	// GetDatabaseInfo retrieves metadata about a specific database
	GetDatabaseInfo(ctx context.Context, database string) (*DatabaseInfo, error)

	// GetTables lists all tables in a database
	GetTables(ctx context.Context, database string) ([]string, error)

	// GetTableInfo retrieves schema and metadata for a table
	GetTableInfo(ctx context.Context, database, table string) (*TableInfo, error)

	// ValidateQuery checks if a query is allowed based on WriteMode and security settings
	ValidateQuery(query string) error

	// GetConnectionInfo returns connection details (for debugging)
	GetConnectionInfo() map[string]interface{}
}

// SparkSQLConnector implements DatabaseConnector using Spark SQL.
// Provides distributed query execution across multiple database types.
type SparkSQLConnector struct {
	config      *DatabaseConfig
	session     *SparkSession
	sparkConfig map[string]string
}

// NewSparkSQLConnector creates a new Spark SQL database connector.
func NewSparkSQLConnector(config *DatabaseConfig) (*SparkSQLConnector, error) {
	if config == nil {
		return nil, fmt.Errorf("database config cannot be nil")
	}

	if config.ConnectionString == "" && config.Type != DatabaseTypeSpark {
		return nil, fmt.Errorf("connection string is required for database type %s", config.Type)
	}

	// Create Spark session
	session, err := NewSparkSession(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Spark session: %w", err)
	}

	connector := &SparkSQLConnector{
		config:      config,
		session:     session,
		sparkConfig: make(map[string]string),
	}

	// Build Spark configuration based on database type
	if err := connector.buildSparkConfig(); err != nil {
		return nil, fmt.Errorf("failed to build Spark config: %w", err)
	}

	return connector, nil
}

// buildSparkConfig constructs Spark SQL configuration for the database type.
func (s *SparkSQLConnector) buildSparkConfig() error {
	switch s.config.Type {
	case DatabaseTypeBigQuery:
		return s.buildBigQueryConfig()
	case DatabaseTypePostgreSQL:
		return s.buildPostgreSQLConfig()
	case DatabaseTypeMySQL:
		return s.buildMySQLConfig()
	case DatabaseTypeSnowflake:
		return s.buildSnowflakeConfig()
	case DatabaseTypeRedshift:
		return s.buildRedshiftConfig()
	case DatabaseTypeSQLServer:
		return s.buildSQLServerConfig()
	case DatabaseTypeSpark:
		return s.buildSparkNativeConfig()
	default:
		return fmt.Errorf("unsupported database type: %s", s.config.Type)
	}
}

// buildBigQueryConfig configures Spark for BigQuery access.
// Uses spark-bigquery-connector: https://github.com/GoogleCloudDataproc/spark-bigquery-connector
func (s *SparkSQLConnector) buildBigQueryConfig() error {
	s.sparkConfig["spark.sql.catalog.bigquery"] = "com.google.cloud.spark.bigquery.v2.BigQueryConnector"

	if s.config.BigQueryProject != "" {
		s.sparkConfig["spark.datasource.bigquery.project"] = s.config.BigQueryProject
	}

	if s.config.BigQueryDataset != "" {
		s.sparkConfig["spark.datasource.bigquery.dataset"] = s.config.BigQueryDataset
	}

	if s.config.BigQueryMaterializationProject != "" {
		s.sparkConfig["spark.datasource.bigquery.materializationProject"] = s.config.BigQueryMaterializationProject
	}

	if s.config.BigQueryMaterializationDataset != "" {
		s.sparkConfig["spark.datasource.bigquery.materializationDataset"] = s.config.BigQueryMaterializationDataset
	}

	// Enable pushdown for better performance
	s.sparkConfig["spark.datasource.bigquery.pushAllFilters"] = "true"

	return nil
}

// buildPostgreSQLConfig configures Spark for PostgreSQL access via JDBC.
func (s *SparkSQLConnector) buildPostgreSQLConfig() error {
	s.sparkConfig["spark.jars.packages"] = "org.postgresql:postgresql:42.6.0"

	if s.config.JDBCDriver == "" {
		s.config.JDBCDriver = "org.postgresql.Driver"
	}

	// Parse connection string to extract components
	// Format: postgresql://host:port/database
	if strings.HasPrefix(s.config.ConnectionString, "postgresql://") {
		// Spark JDBC URL format: jdbc:postgresql://host:port/database
		jdbcURL := strings.Replace(s.config.ConnectionString, "postgresql://", "jdbc:postgresql://", 1)
		s.sparkConfig["spark.jdbc.url"] = jdbcURL
	}

	return nil
}

// buildMySQLConfig configures Spark for MySQL access via JDBC.
func (s *SparkSQLConnector) buildMySQLConfig() error {
	s.sparkConfig["spark.jars.packages"] = "mysql:mysql-connector-java:8.0.33"

	if s.config.JDBCDriver == "" {
		s.config.JDBCDriver = "com.mysql.cj.jdbc.Driver"
	}

	if strings.HasPrefix(s.config.ConnectionString, "mysql://") {
		jdbcURL := strings.Replace(s.config.ConnectionString, "mysql://", "jdbc:mysql://", 1)
		s.sparkConfig["spark.jdbc.url"] = jdbcURL
	}

	return nil
}

// buildSnowflakeConfig configures Spark for Snowflake access.
// Uses snowflake-spark-connector: https://github.com/snowflakedb/spark-snowflake
func (s *SparkSQLConnector) buildSnowflakeConfig() error {
	s.sparkConfig["spark.jars.packages"] = "net.snowflake:spark-snowflake_2.12:2.11.0-spark_3.3"

	// Snowflake connection format: snowflake://account.region.cloud/database/schema
	s.sparkConfig["sfURL"] = s.config.ConnectionString

	return nil
}

// buildRedshiftConfig configures Spark for Redshift access via JDBC.
func (s *SparkSQLConnector) buildRedshiftConfig() error {
	s.sparkConfig["spark.jars.packages"] = "com.amazon.redshift:redshift-jdbc42:2.1.0.16"

	if s.config.JDBCDriver == "" {
		s.config.JDBCDriver = "com.amazon.redshift.jdbc.Driver"
	}

	if strings.HasPrefix(s.config.ConnectionString, "redshift://") {
		jdbcURL := strings.Replace(s.config.ConnectionString, "redshift://", "jdbc:redshift://", 1)
		s.sparkConfig["spark.jdbc.url"] = jdbcURL
	}

	return nil
}

// buildSQLServerConfig configures Spark for SQL Server access via JDBC.
func (s *SparkSQLConnector) buildSQLServerConfig() error {
	s.sparkConfig["spark.jars.packages"] = "com.microsoft.sqlserver:mssql-jdbc:12.2.0.jre11"

	if s.config.JDBCDriver == "" {
		s.config.JDBCDriver = "com.microsoft.sqlserver.jdbc.SQLServerDriver"
	}

	if strings.HasPrefix(s.config.ConnectionString, "sqlserver://") {
		jdbcURL := strings.Replace(s.config.ConnectionString, "sqlserver://", "jdbc:sqlserver://", 1)
		s.sparkConfig["spark.jdbc.url"] = jdbcURL
	}

	return nil
}

// buildSparkNativeConfig configures for Spark native sources (Hive, Delta, Iceberg).
func (s *SparkSQLConnector) buildSparkNativeConfig() error {
	// Enable Hive support
	s.sparkConfig["spark.sql.catalogImplementation"] = "hive"

	// Enable Delta Lake if available
	s.sparkConfig["spark.sql.extensions"] = "io.delta.sql.DeltaSparkSessionExtension"
	s.sparkConfig["spark.sql.catalog.spark_catalog"] = "org.apache.spark.sql.delta.catalog.DeltaCatalog"

	return nil
}

// Connect establishes connection to the database.
// For Spark SQL, this means initializing the Spark session with appropriate config.
func (s *SparkSQLConnector) Connect(ctx context.Context) error {
	// Delegate to Spark session
	return s.session.Connect(ctx)
}

// Disconnect closes the database connection.
func (s *SparkSQLConnector) Disconnect(ctx context.Context) error {
	// Delegate to Spark session
	return s.session.Disconnect(ctx)
}

// IsConnected returns true if currently connected.
func (s *SparkSQLConnector) IsConnected() bool {
	return s.session.IsConnected()
}

// ExecuteQuery executes a SQL query and returns results.
// Query is executed through Spark SQL for distributed processing.
func (s *SparkSQLConnector) ExecuteQuery(ctx context.Context, query string) (*QueryResult, error) {
	// Validate query based on security settings
	if err := s.ValidateQuery(query); err != nil {
		return nil, fmt.Errorf("query validation failed: %w", err)
	}

	// Execute through Spark session (REAL implementation)
	return s.session.ExecuteQuery(ctx, query)
}

// ValidateQuery checks if a query is allowed based on WriteMode and security settings.
func (s *SparkSQLConnector) ValidateQuery(query string) error {
	queryUpper := strings.ToUpper(strings.TrimSpace(query))

	// Check blocked keywords
	for _, keyword := range s.config.BlockedKeywords {
		if strings.Contains(queryUpper, strings.ToUpper(keyword)) {
			return fmt.Errorf("query contains blocked keyword: %s", keyword)
		}
	}

	// Check WriteMode restrictions
	switch s.config.WriteMode {
	case WriteModeBlocked:
		// Only allow SELECT queries
		if !strings.HasPrefix(queryUpper, "SELECT") &&
			!strings.HasPrefix(queryUpper, "SHOW") &&
			!strings.HasPrefix(queryUpper, "DESCRIBE") &&
			!strings.HasPrefix(queryUpper, "EXPLAIN") {
			return fmt.Errorf("write mode BLOCKED: only SELECT/SHOW/DESCRIBE queries allowed")
		}

	case WriteModeReadOnly:
		// Block write operations
		writeKeywords := []string{"INSERT", "UPDATE", "DELETE", "DROP", "CREATE", "ALTER", "TRUNCATE"}
		for _, keyword := range writeKeywords {
			if strings.Contains(queryUpper, keyword) {
				return fmt.Errorf("write mode READ_ONLY: %s operations not allowed", keyword)
			}
		}

	case WriteModeFull:
		// All operations allowed
	}

	// Check if WHERE clause is required for SELECT
	if s.config.RequireWhereClause && strings.HasPrefix(queryUpper, "SELECT") {
		if !strings.Contains(queryUpper, "WHERE") && !strings.Contains(queryUpper, "LIMIT") {
			return fmt.Errorf("WHERE clause or LIMIT required for SELECT queries")
		}
	}

	return nil
}

// GetDatabases lists all accessible databases/datasets.
func (s *SparkSQLConnector) GetDatabases(ctx context.Context) ([]string, error) {
	// Execute through Spark session (REAL implementation)
	databases, err := s.session.GetDatabases(ctx)
	if err != nil {
		return nil, err
	}

	// Apply AllowedDatabases filter if configured
	if len(s.config.AllowedDatabases) > 0 {
		filtered := []string{}
		for _, db := range databases {
			for _, allowed := range s.config.AllowedDatabases {
				if db == allowed {
					filtered = append(filtered, db)
					break
				}
			}
		}
		return filtered, nil
	}

	return databases, nil
}

// GetDatabaseInfo retrieves metadata about a specific database.
func (s *SparkSQLConnector) GetDatabaseInfo(ctx context.Context, database string) (*DatabaseInfo, error) {
	// Check if database is allowed
	if len(s.config.AllowedDatabases) > 0 {
		allowed := false
		for _, db := range s.config.AllowedDatabases {
			if db == database {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("access to database %s not allowed", database)
		}
	}

	// For now, return basic info (could query DESCRIBE DATABASE in future)
	info := &DatabaseInfo{
		Name:               database,
		FullyQualifiedName: database,
		Type:               s.config.Type,
		Metadata:           make(map[string]interface{}),
	}

	return info, nil
}

// GetTables lists all tables in a database.
func (s *SparkSQLConnector) GetTables(ctx context.Context, database string) ([]string, error) {
	// Execute through Spark session (REAL implementation)
	tables, err := s.session.GetTables(ctx, database)
	if err != nil {
		return nil, err
	}

	// Apply AllowedTables filter if configured
	if len(s.config.AllowedTables) > 0 {
		filtered := []string{}
		for _, table := range tables {
			fullName := fmt.Sprintf("%s.%s", database, table)
			for _, allowed := range s.config.AllowedTables {
				if table == allowed || fullName == allowed {
					filtered = append(filtered, table)
					break
				}
			}
		}
		return filtered, nil
	}

	return tables, nil
}

// GetTableInfo retrieves schema and metadata for a table.
func (s *SparkSQLConnector) GetTableInfo(ctx context.Context, database, table string) (*TableInfo, error) {
	// Execute through Spark session (REAL implementation)
	return s.session.GetTableSchema(ctx, database, table)
}

// GetConnectionInfo returns connection details for debugging.
func (s *SparkSQLConnector) GetConnectionInfo() map[string]interface{} {
	stats := s.session.GetStats()
	return map[string]interface{}{
		"type":               s.config.Type,
		"connection_string":  s.config.ConnectionString,
		"database":           s.config.Database,
		"connected":          s.session.IsConnected(),
		"write_mode":         s.config.WriteMode,
		"spark_config":       s.sparkConfig,
		"queries_executed":   stats.QueriesExecuted,
		"total_bytes":        stats.TotalBytes,
		"total_time_seconds": stats.TotalTime.Seconds(),
		"cache_hits":         stats.CacheHits,
		"cache_misses":       stats.CacheMisses,
	}
}

// StreamQuery executes a SQL query and streams results via channel.
// Memory-efficient for large result sets.
func (s *SparkSQLConnector) StreamQuery(ctx context.Context, query string, config *StreamConfig) (*QueryResultStream, error) {
	// Validate query based on security settings
	if err := s.ValidateQuery(query); err != nil {
		return nil, fmt.Errorf("query validation failed: %w", err)
	}

	// Execute through Spark session
	return s.session.StreamQuery(ctx, query, config)
}

// ExecuteQueryPaginated executes a SQL query with pagination.
// Alternative to streaming for discrete pages.
func (s *SparkSQLConnector) ExecuteQueryPaginated(ctx context.Context, query string, config *PaginationConfig) (*QueryResultPage, error) {
	// Validate query based on security settings
	if err := s.ValidateQuery(query); err != nil {
		return nil, fmt.Errorf("query validation failed: %w", err)
	}

	// Execute through Spark session
	return s.session.ExecuteQueryPaginated(ctx, query, config)
}
