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

//go:build integration
// +build integration

package database

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/mysql"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// TestPostgreSQLIntegration tests real PostgreSQL connection and queries.
func TestPostgreSQLIntegration(t *testing.T) {
	ctx := context.Background()

	// Start PostgreSQL container
	postgresContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(),
	)
	if err != nil {
		t.Fatalf("Failed to start PostgreSQL container: %v", err)
	}
	defer func() {
		if err := testcontainers.TerminateContainer(postgresContainer); err != nil {
			t.Logf("Failed to terminate container: %v", err)
		}
	}()

	// Get connection string
	connStr, err := postgresContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	// Create config
	config := &DatabaseConfig{
		Type:             DatabaseTypePostgreSQL,
		ConnectionString: connStr,
		WriteMode:        WriteModeReadOnly,
		QueryTimeout:     30 * time.Second,
		MaxResultRows:    1000,
	}

	// Create connector
	connector, err := NewSparkSQLConnector(config)
	if err != nil {
		t.Fatalf("Failed to create connector: %v", err)
	}

	// Test connection
	if err := connector.Connect(ctx); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer connector.Disconnect(ctx)

	// Verify connected
	if !connector.IsConnected() {
		t.Error("Connector should be connected")
	}

	// Test simple query
	result, err := connector.ExecuteQuery(ctx, "SELECT 1 as num, 'test' as str")
	if err != nil {
		t.Fatalf("Failed to execute query: %v", err)
	}

	if result.RowCount != 1 {
		t.Errorf("Expected 1 row, got %d", result.RowCount)
	}

	if len(result.Columns) != 2 {
		t.Errorf("Expected 2 columns, got %d", len(result.Columns))
	}

	// Check data
	if len(result.Rows) > 0 {
		row := result.Rows[0]
		if row["num"] != int64(1) {
			t.Errorf("Expected num=1, got %v", row["num"])
		}
		if row["str"] != "test" {
			t.Errorf("Expected str='test', got %v", row["str"])
		}
	}

	// Check performance metrics
	if result.ExecutionTime <= 0 {
		t.Error("Execution time should be > 0")
	}

	t.Logf("Query executed in %.3f seconds", result.ExecutionTime)
}

// TestPostgreSQLTableOperations tests table creation and queries.
func TestPostgreSQLTableOperations(t *testing.T) {
	ctx := context.Background()

	// Start PostgreSQL container
	postgresContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
	)
	if err != nil {
		t.Fatalf("Failed to start PostgreSQL container: %v", err)
	}
	defer testcontainers.TerminateContainer(postgresContainer)

	connStr, err := postgresContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	// Create connector with FULL write mode
	config := &DatabaseConfig{
		Type:             DatabaseTypePostgreSQL,
		ConnectionString: connStr,
		WriteMode:        WriteModeFull, // Allow writes
		QueryTimeout:     30 * time.Second,
		MaxResultRows:    1000,
		BlockedKeywords:  []string{}, // Allow all operations
	}

	connector, err := NewSparkSQLConnector(config)
	if err != nil {
		t.Fatalf("Failed to create connector: %v", err)
	}

	if err := connector.Connect(ctx); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer connector.Disconnect(ctx)

	// Create table
	_, err = connector.ExecuteQuery(ctx, `
		CREATE TABLE users (
			id SERIAL PRIMARY KEY,
			name VARCHAR(100),
			email VARCHAR(100),
			active BOOLEAN DEFAULT true
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	// Insert data
	_, err = connector.ExecuteQuery(ctx, `
		INSERT INTO users (name, email, active) VALUES
		('Alice', 'alice@example.com', true),
		('Bob', 'bob@example.com', true),
		('Charlie', 'charlie@example.com', false)
	`)
	if err != nil {
		t.Fatalf("Failed to insert data: %v", err)
	}

	// Query data
	result, err := connector.ExecuteQuery(ctx, "SELECT * FROM users WHERE active = true ORDER BY id")
	if err != nil {
		t.Fatalf("Failed to query users: %v", err)
	}

	if result.RowCount != 2 {
		t.Errorf("Expected 2 active users, got %d", result.RowCount)
	}

	// Check first user
	if len(result.Rows) > 0 {
		alice := result.Rows[0]
		if alice["name"] != "Alice" {
			t.Errorf("Expected name='Alice', got %v", alice["name"])
		}
		if alice["email"] != "alice@example.com" {
			t.Errorf("Expected email='alice@example.com', got %v", alice["email"])
		}
	}

	// Test metadata discovery
	tables, err := connector.GetTables(ctx, "public")
	if err != nil {
		t.Fatalf("Failed to get tables: %v", err)
	}

	if len(tables) == 0 {
		t.Error("Expected at least one table")
	}

	t.Logf("Found tables: %v", tables)

	// Test table schema
	tableInfo, err := connector.GetTableInfo(ctx, "public", "users")
	if err != nil {
		t.Fatalf("Failed to get table info: %v", err)
	}

	if tableInfo.Table != "users" {
		t.Errorf("Expected table name 'users', got %s", tableInfo.Table)
	}

	if len(tableInfo.Columns) < 4 {
		t.Errorf("Expected at least 4 columns, got %d", len(tableInfo.Columns))
	}

	t.Logf("Table schema: %d columns", len(tableInfo.Columns))
	for _, col := range tableInfo.Columns {
		t.Logf("  - %s: %s (nullable: %v)", col.Name, col.DataType, col.Nullable)
	}
}

// TestMySQLIntegration tests real MySQL connection and queries.
func TestMySQLIntegration(t *testing.T) {
	ctx := context.Background()

	// Start MySQL container
	mysqlContainer, err := mysql.Run(ctx,
		"mysql:8.0",
		mysql.WithDatabase("testdb"),
		mysql.WithUsername("testuser"),
		mysql.WithPassword("testpass"),
	)
	if err != nil {
		t.Fatalf("Failed to start MySQL container: %v", err)
	}
	defer testcontainers.TerminateContainer(mysqlContainer)

	// Get connection string
	connStr, err := mysqlContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	// Create config
	config := &DatabaseConfig{
		Type:             DatabaseTypeMySQL,
		ConnectionString: connStr,
		WriteMode:        WriteModeReadOnly,
		QueryTimeout:     30 * time.Second,
		MaxResultRows:    1000,
	}

	// Create connector
	connector, err := NewSparkSQLConnector(config)
	if err != nil {
		t.Fatalf("Failed to create connector: %v", err)
	}

	// Test connection
	if err := connector.Connect(ctx); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer connector.Disconnect(ctx)

	// Test simple query
	result, err := connector.ExecuteQuery(ctx, "SELECT 1 as num, 'test' as str")
	if err != nil {
		t.Fatalf("Failed to execute query: %v", err)
	}

	if result.RowCount != 1 {
		t.Errorf("Expected 1 row, got %d", result.RowCount)
	}

	t.Logf("MySQL query executed in %.3f seconds", result.ExecutionTime)
}

// TestSecurityControls tests WriteMode and query validation.
func TestSecurityControls(t *testing.T) {
	ctx := context.Background()

	postgresContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
	)
	if err != nil {
		t.Fatalf("Failed to start PostgreSQL container: %v", err)
	}
	defer testcontainers.TerminateContainer(postgresContainer)

	connStr, err := postgresContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	// Test BLOCKED mode
	t.Run("BlockedMode", func(t *testing.T) {
		config := &DatabaseConfig{
			Type:             DatabaseTypePostgreSQL,
			ConnectionString: connStr,
			WriteMode:        WriteModeBlocked,
			QueryTimeout:     30 * time.Second,
		}

		connector, _ := NewSparkSQLConnector(config)
		connector.Connect(ctx)
		defer connector.Disconnect(ctx)

		// SELECT should work
		_, err := connector.ExecuteQuery(ctx, "SELECT 1")
		if err != nil {
			t.Errorf("SELECT should be allowed in BLOCKED mode: %v", err)
		}

		// INSERT should fail
		_, err = connector.ExecuteQuery(ctx, "INSERT INTO test VALUES (1)")
		if err == nil {
			t.Error("INSERT should be blocked in BLOCKED mode")
		}
	})

	// Test READ_ONLY mode
	t.Run("ReadOnlyMode", func(t *testing.T) {
		config := &DatabaseConfig{
			Type:             DatabaseTypePostgreSQL,
			ConnectionString: connStr,
			WriteMode:        WriteModeReadOnly,
			QueryTimeout:     30 * time.Second,
		}

		connector, _ := NewSparkSQLConnector(config)
		connector.Connect(ctx)
		defer connector.Disconnect(ctx)

		// SELECT should work
		_, err := connector.ExecuteQuery(ctx, "SELECT 1")
		if err != nil {
			t.Errorf("SELECT should be allowed in READ_ONLY mode: %v", err)
		}

		// DROP should fail
		_, err = connector.ExecuteQuery(ctx, "DROP TABLE test")
		if err == nil {
			t.Error("DROP should be blocked in READ_ONLY mode")
		}
	})

	// Test blocked keywords
	t.Run("BlockedKeywords", func(t *testing.T) {
		config := &DatabaseConfig{
			Type:             DatabaseTypePostgreSQL,
			ConnectionString: connStr,
			WriteMode:        WriteModeFull,
			BlockedKeywords:  []string{"DROP", "TRUNCATE"},
			QueryTimeout:     30 * time.Second,
		}

		connector, _ := NewSparkSQLConnector(config)
		connector.Connect(ctx)
		defer connector.Disconnect(ctx)

		// DROP should be blocked
		_, err := connector.ExecuteQuery(ctx, "DROP TABLE test")
		if err == nil {
			t.Error("DROP should be blocked by blocked keywords")
		}

		// TRUNCATE should be blocked
		_, err = connector.ExecuteQuery(ctx, "TRUNCATE TABLE test")
		if err == nil {
			t.Error("TRUNCATE should be blocked by blocked keywords")
		}
	})
}

// TestPerformanceMetrics tests metric tracking.
func TestPerformanceMetrics(t *testing.T) {
	ctx := context.Background()

	postgresContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
	)
	if err != nil {
		t.Fatalf("Failed to start PostgreSQL container: %v", err)
	}
	defer testcontainers.TerminateContainer(postgresContainer)

	connStr, err := postgresContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	config := &DatabaseConfig{
		Type:             DatabaseTypePostgreSQL,
		ConnectionString: connStr,
		WriteMode:        WriteModeReadOnly,
		QueryTimeout:     30 * time.Second,
		MaxResultRows:    1000,
	}

	connector, _ := NewSparkSQLConnector(config)
	connector.Connect(ctx)
	defer connector.Disconnect(ctx)

	// Execute multiple queries
	for i := 0; i < 5; i++ {
		_, err := connector.ExecuteQuery(ctx, fmt.Sprintf("SELECT %d", i))
		if err != nil {
			t.Fatalf("Query %d failed: %v", i, err)
		}
	}

	// Check stats
	info := connector.GetConnectionInfo()

	queriesExecuted, ok := info["queries_executed"].(int64)
	if !ok || queriesExecuted != 5 {
		t.Errorf("Expected 5 queries executed, got %v", info["queries_executed"])
	}

	totalTime, ok := info["total_time_seconds"].(float64)
	if !ok || totalTime <= 0 {
		t.Errorf("Expected positive total time, got %v", info["total_time_seconds"])
	}

	t.Logf("Performance metrics:")
	t.Logf("  Queries executed: %v", info["queries_executed"])
	t.Logf("  Total time: %.3f seconds", info["total_time_seconds"])
	t.Logf("  Avg time per query: %.3f seconds", totalTime/float64(queriesExecuted))
}

// TestMaxResultRows tests result truncation.
func TestMaxResultRows(t *testing.T) {
	ctx := context.Background()

	postgresContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
	)
	if err != nil {
		t.Fatalf("Failed to start PostgreSQL container: %v", err)
	}
	defer testcontainers.TerminateContainer(postgresContainer)

	connStr, err := postgresContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	// Config with small MaxResultRows
	config := &DatabaseConfig{
		Type:             DatabaseTypePostgreSQL,
		ConnectionString: connStr,
		WriteMode:        WriteModeReadOnly,
		QueryTimeout:     30 * time.Second,
		MaxResultRows:    5, // Only 5 rows max
	}

	connector, _ := NewSparkSQLConnector(config)
	connector.Connect(ctx)
	defer connector.Disconnect(ctx)

	// Query that would return 10 rows
	result, err := connector.ExecuteQuery(ctx, "SELECT generate_series(1, 10) as num")
	if err != nil {
		t.Fatalf("Failed to execute query: %v", err)
	}

	// Should be truncated to 5
	if result.RowCount != 5 {
		t.Errorf("Expected 5 rows (truncated), got %d", result.RowCount)
	}

	// Should have warning
	if len(result.Warnings) == 0 {
		t.Error("Expected truncation warning")
	}

	t.Logf("Result truncated: %v", result.Warnings)
}

// TestDatabaseToolset tests the full toolset with real database.
func TestDatabaseToolset(t *testing.T) {
	ctx := context.Background()

	postgresContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
	)
	if err != nil {
		t.Fatalf("Failed to start PostgreSQL container: %v", err)
	}
	defer testcontainers.TerminateContainer(postgresContainer)

	connStr, err := postgresContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	config := &DatabaseConfig{
		Type:             DatabaseTypePostgreSQL,
		ConnectionString: connStr,
		WriteMode:        WriteModeReadOnly,
		QueryTimeout:     30 * time.Second,
		MaxResultRows:    1000,
	}

	// Create toolset
	toolset, err := NewDatabaseToolset(config)
	if err != nil {
		t.Fatalf("Failed to create toolset: %v", err)
	}

	if err := toolset.Connect(ctx); err != nil {
		t.Fatalf("Failed to connect toolset: %v", err)
	}
	defer toolset.Disconnect(ctx)

	tools := toolset.GetTools()
	if len(tools) != 7 {
		t.Errorf("Expected 7 tools, got %d", len(tools))
	}

	// Test list_databases tool
	t.Run("ListDatabases", func(t *testing.T) {
		listDbTool := tools[0]
		result, err := listDbTool.Handler(ctx, map[string]interface{}{})
		if err != nil {
			t.Fatalf("list_databases failed: %v", err)
		}

		resultMap := result.(map[string]interface{})
		databases := resultMap["databases"].([]string)
		if len(databases) == 0 {
			t.Error("Expected at least one database")
		}

		t.Logf("Found databases: %v", databases)
	})

	// Test execute_sql tool
	t.Run("ExecuteSQL", func(t *testing.T) {
		executeSQLTool := tools[4]
		result, err := executeSQLTool.Handler(ctx, map[string]interface{}{
			"query": "SELECT 1 as num, 'test' as str",
		})
		if err != nil {
			t.Fatalf("execute_sql failed: %v", err)
		}

		queryResult := result.(*QueryResult)
		if queryResult.RowCount != 1 {
			t.Errorf("Expected 1 row, got %d", queryResult.RowCount)
		}

		t.Logf("Query result: %d rows, %.3fs", queryResult.RowCount, queryResult.ExecutionTime)
	})
}

// TestStreamQuery tests streaming large result sets.
func TestStreamQuery(t *testing.T) {
	ctx := context.Background()

	postgresContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
	)
	if err != nil {
		t.Fatalf("Failed to start PostgreSQL container: %v", err)
	}
	defer testcontainers.TerminateContainer(postgresContainer)

	connStr, err := postgresContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	config := &DatabaseConfig{
		Type:             DatabaseTypePostgreSQL,
		ConnectionString: connStr,
		WriteMode:        WriteModeReadOnly,
		QueryTimeout:     30 * time.Second,
	}

	connector, _ := NewSparkSQLConnector(config)
	connector.Connect(ctx)
	defer connector.Disconnect(ctx)

	// Stream query with 100 rows
	streamConfig := &StreamConfig{
		BufferSize: 10,
		BatchSize:  20,
		MaxRows:    0, // unlimited
	}

	stream, err := connector.StreamQuery(ctx, "SELECT generate_series(1, 100) as num", streamConfig)
	if err != nil {
		t.Fatalf("StreamQuery failed: %v", err)
	}
	defer stream.Cancel()

	// Count rows
	rowCount := 0
	for row := range stream.Rows {
		rowCount++
		if rowCount == 1 {
			// Check first row
			if row["num"] != int64(1) {
				t.Errorf("Expected first row num=1, got %v", row["num"])
			}
		}
	}

	// Check for errors
	if err := <-stream.Errors; err != nil {
		t.Fatalf("Stream error: %v", err)
	}

	if rowCount != 100 {
		t.Errorf("Expected 100 rows, got %d", rowCount)
	}

	if !stream.Metadata.Completed {
		t.Error("Stream should be marked as completed")
	}

	t.Logf("Streamed %d rows in %.3fs", rowCount, stream.Metadata.CompletedAt.Sub(stream.Metadata.StartTime).Seconds())
}

// TestStreamQueryCancellation tests early cancellation of streaming.
func TestStreamQueryCancellation(t *testing.T) {
	ctx := context.Background()

	postgresContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
	)
	if err != nil {
		t.Fatalf("Failed to start PostgreSQL container: %v", err)
	}
	defer testcontainers.TerminateContainer(postgresContainer)

	connStr, err := postgresContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	config := &DatabaseConfig{
		Type:             DatabaseTypePostgreSQL,
		ConnectionString: connStr,
		WriteMode:        WriteModeReadOnly,
	}

	connector, _ := NewSparkSQLConnector(config)
	connector.Connect(ctx)
	defer connector.Disconnect(ctx)

	stream, err := connector.StreamQuery(ctx, "SELECT generate_series(1, 1000) as num", nil)
	if err != nil {
		t.Fatalf("StreamQuery failed: %v", err)
	}

	// Read only first 10 rows then cancel
	rowCount := 0
	for row := range stream.Rows {
		rowCount++
		if rowCount >= 10 {
			stream.Cancel() // Cancel early
			break
		}
		_ = row
	}

	// Should have stopped at 10
	if rowCount != 10 {
		t.Errorf("Expected to read 10 rows before cancel, got %d", rowCount)
	}

	t.Logf("Successfully cancelled stream after %d rows", rowCount)
}

// TestStreamQueryMaxRows tests MaxRows limit.
func TestStreamQueryMaxRows(t *testing.T) {
	ctx := context.Background()

	postgresContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
	)
	if err != nil {
		t.Fatalf("Failed to start PostgreSQL container: %v", err)
	}
	defer testcontainers.TerminateContainer(postgresContainer)

	connStr, err := postgresContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	config := &DatabaseConfig{
		Type:             DatabaseTypePostgreSQL,
		ConnectionString: connStr,
		WriteMode:        WriteModeReadOnly,
	}

	connector, _ := NewSparkSQLConnector(config)
	connector.Connect(ctx)
	defer connector.Disconnect(ctx)

	// Set MaxRows to 50
	streamConfig := &StreamConfig{
		MaxRows: 50,
	}

	stream, err := connector.StreamQuery(ctx, "SELECT generate_series(1, 1000) as num", streamConfig)
	if err != nil {
		t.Fatalf("StreamQuery failed: %v", err)
	}
	defer stream.Cancel()

	rowCount := 0
	for range stream.Rows {
		rowCount++
	}

	// Check for errors
	<-stream.Errors

	// Should be limited to 50
	if rowCount != 50 {
		t.Errorf("Expected 50 rows (MaxRows limit), got %d", rowCount)
	}

	t.Logf("Successfully limited stream to %d rows", rowCount)
}

// TestPaginatedQuery tests paginated query execution.
func TestPaginatedQuery(t *testing.T) {
	ctx := context.Background()

	postgresContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
	)
	if err != nil {
		t.Fatalf("Failed to start PostgreSQL container: %v", err)
	}
	defer testcontainers.TerminateContainer(postgresContainer)

	connStr, err := postgresContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	config := &DatabaseConfig{
		Type:             DatabaseTypePostgreSQL,
		ConnectionString: connStr,
		WriteMode:        WriteModeReadOnly,
	}

	connector, _ := NewSparkSQLConnector(config)
	connector.Connect(ctx)
	defer connector.Disconnect(ctx)

	// Test pagination
	paginationConfig := &PaginationConfig{
		PageSize: 25,
		Page:     1,
	}

	// First page
	page1, err := connector.ExecuteQueryPaginated(ctx, "SELECT generate_series(1, 100) as num", paginationConfig)
	if err != nil {
		t.Fatalf("ExecuteQueryPaginated failed: %v", err)
	}

	if page1.RowCount != 25 {
		t.Errorf("Expected 25 rows in page 1, got %d", page1.RowCount)
	}

	if !page1.HasNextPage {
		t.Error("Page 1 should have next page")
	}

	if page1.Page != 1 {
		t.Errorf("Expected page 1, got %d", page1.Page)
	}

	// Check first row of page 1
	if len(page1.Rows) > 0 {
		if page1.Rows[0]["num"] != int64(1) {
			t.Errorf("Expected first row of page 1 to have num=1, got %v", page1.Rows[0]["num"])
		}
	}

	// Second page
	paginationConfig.Page = 2
	page2, err := connector.ExecuteQueryPaginated(ctx, "SELECT generate_series(1, 100) as num", paginationConfig)
	if err != nil {
		t.Fatalf("ExecuteQueryPaginated page 2 failed: %v", err)
	}

	if page2.RowCount != 25 {
		t.Errorf("Expected 25 rows in page 2, got %d", page2.RowCount)
	}

	// Check first row of page 2 (should be num=26)
	if len(page2.Rows) > 0 {
		if page2.Rows[0]["num"] != int64(26) {
			t.Errorf("Expected first row of page 2 to have num=26, got %v", page2.Rows[0]["num"])
		}
	}

	// Last page (page 4)
	paginationConfig.Page = 4
	page4, err := connector.ExecuteQueryPaginated(ctx, "SELECT generate_series(1, 100) as num", paginationConfig)
	if err != nil {
		t.Fatalf("ExecuteQueryPaginated page 4 failed: %v", err)
	}

	if page4.RowCount != 25 {
		t.Errorf("Expected 25 rows in page 4, got %d", page4.RowCount)
	}

	if page4.HasNextPage {
		t.Error("Page 4 should not have next page")
	}

	t.Logf("Successfully paginated 100 rows into 4 pages of 25 rows each")
}

// TestPaginationWithTotalCount tests pagination with total count.
func TestPaginationWithTotalCount(t *testing.T) {
	ctx := context.Background()

	postgresContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
	)
	if err != nil {
		t.Fatalf("Failed to start PostgreSQL container: %v", err)
	}
	defer testcontainers.TerminateContainer(postgresContainer)

	connStr, err := postgresContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	config := &DatabaseConfig{
		Type:             DatabaseTypePostgreSQL,
		ConnectionString: connStr,
		WriteMode:        WriteModeReadOnly,
	}

	connector, _ := NewSparkSQLConnector(config)
	connector.Connect(ctx)
	defer connector.Disconnect(ctx)

	paginationConfig := &PaginationConfig{
		PageSize:          20,
		Page:              1,
		IncludeTotalCount: true,
	}

	page, err := connector.ExecuteQueryPaginated(ctx, "SELECT generate_series(1, 75) as num", paginationConfig)
	if err != nil {
		t.Fatalf("ExecuteQueryPaginated failed: %v", err)
	}

	if page.TotalRows != 75 {
		t.Errorf("Expected total rows 75, got %d", page.TotalRows)
	}

	t.Logf("Page 1 of %d total rows (%.1f pages)", page.TotalRows, float64(page.TotalRows)/float64(page.PageSize))
}
