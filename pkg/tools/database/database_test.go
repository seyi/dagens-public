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
	"testing"
	"time"
)

func TestDefaultDatabaseConfig(t *testing.T) {
	config := DefaultDatabaseConfig(DatabaseTypePostgreSQL, "postgresql://localhost:5432/testdb")

	if config.Type != DatabaseTypePostgreSQL {
		t.Errorf("Expected type PostgreSQL, got %s", config.Type)
	}

	if config.WriteMode != WriteModeReadOnly {
		t.Errorf("Expected READ_ONLY write mode, got %s", config.WriteMode)
	}

	if config.MaxResultRows != 10000 {
		t.Errorf("Expected MaxResultRows 10000, got %d", config.MaxResultRows)
	}

	if len(config.BlockedKeywords) == 0 {
		t.Error("Expected default blocked keywords")
	}
}

func TestSparkSQLConnector_BigQuery(t *testing.T) {
	config := &DatabaseConfig{
		Type:             DatabaseTypeBigQuery,
		ConnectionString: "bigquery://my-project/my_dataset",
		BigQueryProject:  "my-project",
		BigQueryDataset:  "my_dataset",
		WriteMode:        WriteModeReadOnly,
	}

	connector, err := NewSparkSQLConnector(config)
	if err != nil {
		t.Fatalf("Failed to create BigQuery connector: %v", err)
	}

	if connector.config.Type != DatabaseTypeBigQuery {
		t.Errorf("Expected BigQuery type, got %s", connector.config.Type)
	}

	// Check Spark config
	if connector.sparkConfig["spark.datasource.bigquery.project"] != "my-project" {
		t.Error("BigQuery project not set in Spark config")
	}

	if connector.sparkConfig["spark.datasource.bigquery.dataset"] != "my_dataset" {
		t.Error("BigQuery dataset not set in Spark config")
	}
}

func TestSparkSQLConnector_PostgreSQL(t *testing.T) {
	config := &DatabaseConfig{
		Type:             DatabaseTypePostgreSQL,
		ConnectionString: "postgresql://localhost:5432/testdb",
		WriteMode:        WriteModeReadOnly,
	}

	connector, err := NewSparkSQLConnector(config)
	if err != nil {
		t.Fatalf("Failed to create PostgreSQL connector: %v", err)
	}

	if connector.config.JDBCDriver != "org.postgresql.Driver" {
		t.Errorf("Expected PostgreSQL driver, got %s", connector.config.JDBCDriver)
	}

	// Check JDBC URL conversion
	if connector.sparkConfig["spark.jdbc.url"] != "jdbc:postgresql://localhost:5432/testdb" {
		t.Errorf("JDBC URL not converted correctly: %s", connector.sparkConfig["spark.jdbc.url"])
	}
}

func TestSparkSQLConnector_MySQL(t *testing.T) {
	config := &DatabaseConfig{
		Type:             DatabaseTypeMySQL,
		ConnectionString: "mysql://localhost:3306/testdb",
		WriteMode:        WriteModeReadOnly,
	}

	connector, err := NewSparkSQLConnector(config)
	if err != nil {
		t.Fatalf("Failed to create MySQL connector: %v", err)
	}

	if connector.config.JDBCDriver != "com.mysql.cj.jdbc.Driver" {
		t.Errorf("Expected MySQL driver, got %s", connector.config.JDBCDriver)
	}
}

func TestSparkSQLConnector_ConnectDisconnect(t *testing.T) {
	config := DefaultDatabaseConfig(DatabaseTypePostgreSQL, "postgresql://localhost:5432/testdb")
	connector, err := NewSparkSQLConnector(config)
	if err != nil {
		t.Fatalf("Failed to create connector: %v", err)
	}

	// Initially not connected
	if connector.IsConnected() {
		t.Error("Connector should not be connected initially")
	}

	// Connect
	ctx := context.Background()
	if err := connector.Connect(ctx); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	if !connector.IsConnected() {
		t.Error("Connector should be connected after Connect()")
	}

	// Disconnect
	if err := connector.Disconnect(ctx); err != nil {
		t.Fatalf("Failed to disconnect: %v", err)
	}

	if connector.IsConnected() {
		t.Error("Connector should not be connected after Disconnect()")
	}
}

func TestSparkSQLConnector_ValidateQuery_Blocked(t *testing.T) {
	config := DefaultDatabaseConfig(DatabaseTypePostgreSQL, "postgresql://localhost:5432/testdb")
	config.WriteMode = WriteModeBlocked
	connector, _ := NewSparkSQLConnector(config)

	tests := []struct {
		query     string
		shouldErr bool
	}{
		{"SELECT * FROM users", false},
		{"SHOW TABLES", false},
		{"DESCRIBE users", false},
		{"INSERT INTO users VALUES (1)", true},
		{"UPDATE users SET name='test'", true},
		{"DELETE FROM users", true},
		{"DROP TABLE users", true},
	}

	for _, tt := range tests {
		err := connector.ValidateQuery(tt.query)
		if tt.shouldErr && err == nil {
			t.Errorf("Expected error for query: %s", tt.query)
		}
		if !tt.shouldErr && err != nil {
			t.Errorf("Unexpected error for query '%s': %v", tt.query, err)
		}
	}
}

func TestSparkSQLConnector_ValidateQuery_ReadOnly(t *testing.T) {
	config := DefaultDatabaseConfig(DatabaseTypePostgreSQL, "postgresql://localhost:5432/testdb")
	config.WriteMode = WriteModeReadOnly
	connector, _ := NewSparkSQLConnector(config)

	tests := []struct {
		query     string
		shouldErr bool
	}{
		{"SELECT * FROM users", false},
		{"SELECT id, name FROM users WHERE active = true", false},
		{"INSERT INTO users VALUES (1)", true},
		{"UPDATE users SET name='test'", true},
		{"DELETE FROM users", true},
		{"DROP TABLE users", true},
		{"CREATE TABLE test (id INT)", true},
	}

	for _, tt := range tests {
		err := connector.ValidateQuery(tt.query)
		if tt.shouldErr && err == nil {
			t.Errorf("Expected error for query: %s", tt.query)
		}
		if !tt.shouldErr && err != nil {
			t.Errorf("Unexpected error for query '%s': %v", tt.query, err)
		}
	}
}

func TestSparkSQLConnector_ValidateQuery_Full(t *testing.T) {
	config := DefaultDatabaseConfig(DatabaseTypePostgreSQL, "postgresql://localhost:5432/testdb")
	config.WriteMode = WriteModeFull
	config.BlockedKeywords = []string{} // Remove blocked keywords for this test
	connector, _ := NewSparkSQLConnector(config)

	// All queries should be allowed in FULL mode
	queries := []string{
		"SELECT * FROM users",
		"INSERT INTO users VALUES (1)",
		"UPDATE users SET name='test'",
		"DELETE FROM users WHERE id = 1",
		"CREATE TABLE test (id INT)",
		"DROP TABLE test",
	}

	for _, query := range queries {
		err := connector.ValidateQuery(query)
		if err != nil {
			t.Errorf("Unexpected error in FULL mode for query '%s': %v", query, err)
		}
	}
}

func TestSparkSQLConnector_ValidateQuery_BlockedKeywords(t *testing.T) {
	config := DefaultDatabaseConfig(DatabaseTypePostgreSQL, "postgresql://localhost:5432/testdb")
	config.WriteMode = WriteModeFull
	config.BlockedKeywords = []string{"DROP", "TRUNCATE"}
	connector, _ := NewSparkSQLConnector(config)

	tests := []struct {
		query     string
		shouldErr bool
	}{
		{"SELECT * FROM users", false},
		{"INSERT INTO users VALUES (1)", false},
		{"DROP TABLE users", true},
		{"TRUNCATE TABLE users", true},
		{"SELECT * FROM users WHERE name != 'DROP'", true}, // Contains "DROP"
	}

	for _, tt := range tests {
		err := connector.ValidateQuery(tt.query)
		if tt.shouldErr && err == nil {
			t.Errorf("Expected error for query: %s", tt.query)
		}
		if !tt.shouldErr && err != nil {
			t.Errorf("Unexpected error for query '%s': %v", tt.query, err)
		}
	}
}

func TestSparkSQLConnector_ValidateQuery_RequireWhere(t *testing.T) {
	config := DefaultDatabaseConfig(DatabaseTypePostgreSQL, "postgresql://localhost:5432/testdb")
	config.RequireWhereClause = true
	connector, _ := NewSparkSQLConnector(config)

	tests := []struct {
		query     string
		shouldErr bool
	}{
		{"SELECT * FROM users WHERE active = true", false},
		{"SELECT * FROM users LIMIT 10", false},
		{"SELECT * FROM users", true}, // No WHERE or LIMIT
		{"SELECT COUNT(*) FROM users", true},
	}

	for _, tt := range tests {
		err := connector.ValidateQuery(tt.query)
		if tt.shouldErr && err == nil {
			t.Errorf("Expected error for query: %s", tt.query)
		}
		if !tt.shouldErr && err != nil {
			t.Errorf("Unexpected error for query '%s': %v", tt.query, err)
		}
	}
}

func TestDatabaseToolset_Creation(t *testing.T) {
	config := DefaultDatabaseConfig(DatabaseTypePostgreSQL, "postgresql://localhost:5432/testdb")
	toolset, err := NewDatabaseToolset(config)
	if err != nil {
		t.Fatalf("Failed to create toolset: %v", err)
	}

	tools := toolset.GetTools()
	if len(tools) != 7 {
		t.Errorf("Expected 7 tools, got %d", len(tools))
	}

	// Check tool names
	expectedTools := []string{
		"list_databases",
		"get_database_info",
		"list_tables",
		"get_table_schema",
		"execute_sql",
		"forecast",
		"ask_data_insights",
	}

	for i, tool := range tools {
		if tool.Name != expectedTools[i] {
			t.Errorf("Expected tool %d to be %s, got %s", i, expectedTools[i], tool.Name)
		}

		if !tool.Enabled {
			t.Errorf("Tool %s should be enabled", tool.Name)
		}

		if tool.Handler == nil {
			t.Errorf("Tool %s has no handler", tool.Name)
		}
	}
}

func TestDatabaseToolset_ListDatabases(t *testing.T) {
	config := DefaultDatabaseConfig(DatabaseTypePostgreSQL, "postgresql://localhost:5432/testdb")
	toolset, err := NewDatabaseToolset(config)
	if err != nil {
		t.Fatalf("Failed to create toolset: %v", err)
	}

	ctx := context.Background()
	tools := toolset.GetTools()
	listDbTool := tools[0] // list_databases is first

	result, err := listDbTool.Handler(ctx, map[string]interface{}{})
	if err != nil {
		t.Fatalf("list_databases failed: %v", err)
	}

	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("Result is not a map")
	}

	if _, exists := resultMap["databases"]; !exists {
		t.Error("Result missing 'databases' field")
	}

	if _, exists := resultMap["count"]; !exists {
		t.Error("Result missing 'count' field")
	}

	if resultMap["type"] != DatabaseTypePostgreSQL {
		t.Errorf("Expected type PostgreSQL, got %v", resultMap["type"])
	}
}

func TestDatabaseToolset_GetDatabaseInfo(t *testing.T) {
	config := DefaultDatabaseConfig(DatabaseTypePostgreSQL, "postgresql://localhost:5432/testdb")
	toolset, _ := NewDatabaseToolset(config)

	ctx := context.Background()
	tools := toolset.GetTools()
	getDbInfoTool := tools[1] // get_database_info is second

	result, err := getDbInfoTool.Handler(ctx, map[string]interface{}{
		"database": "testdb",
	})
	if err != nil {
		t.Fatalf("get_database_info failed: %v", err)
	}

	info, ok := result.(*DatabaseInfo)
	if !ok {
		t.Fatal("Result is not DatabaseInfo")
	}

	if info.Name != "testdb" {
		t.Errorf("Expected database name 'testdb', got %s", info.Name)
	}

	if info.Type != DatabaseTypePostgreSQL {
		t.Errorf("Expected type PostgreSQL, got %s", info.Type)
	}
}

func TestDatabaseToolset_ListTables(t *testing.T) {
	config := DefaultDatabaseConfig(DatabaseTypePostgreSQL, "postgresql://localhost:5432/testdb")
	toolset, _ := NewDatabaseToolset(config)

	ctx := context.Background()
	tools := toolset.GetTools()
	listTablesTool := tools[2] // list_tables is third

	result, err := listTablesTool.Handler(ctx, map[string]interface{}{
		"database": "testdb",
	})
	if err != nil {
		t.Fatalf("list_tables failed: %v", err)
	}

	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("Result is not a map")
	}

	if resultMap["database"] != "testdb" {
		t.Errorf("Expected database 'testdb', got %v", resultMap["database"])
	}

	if _, exists := resultMap["tables"]; !exists {
		t.Error("Result missing 'tables' field")
	}

	if _, exists := resultMap["count"]; !exists {
		t.Error("Result missing 'count' field")
	}
}

func TestDatabaseToolset_GetTableSchema(t *testing.T) {
	config := DefaultDatabaseConfig(DatabaseTypePostgreSQL, "postgresql://localhost:5432/testdb")
	toolset, _ := NewDatabaseToolset(config)

	ctx := context.Background()
	tools := toolset.GetTools()
	getTableSchemaTool := tools[3] // get_table_schema is fourth

	result, err := getTableSchemaTool.Handler(ctx, map[string]interface{}{
		"database": "testdb",
		"table":    "users",
	})
	if err != nil {
		t.Fatalf("get_table_schema failed: %v", err)
	}

	info, ok := result.(*TableInfo)
	if !ok {
		t.Fatal("Result is not TableInfo")
	}

	if info.Database != "testdb" {
		t.Errorf("Expected database 'testdb', got %s", info.Database)
	}

	if info.Table != "users" {
		t.Errorf("Expected table 'users', got %s", info.Table)
	}

	if info.FullyQualifiedName != "testdb.users" {
		t.Errorf("Expected fully qualified name 'testdb.users', got %s", info.FullyQualifiedName)
	}
}

func TestDatabaseToolset_ExecuteSQL(t *testing.T) {
	config := DefaultDatabaseConfig(DatabaseTypePostgreSQL, "postgresql://localhost:5432/testdb")
	config.WriteMode = WriteModeReadOnly
	toolset, _ := NewDatabaseToolset(config)

	ctx := context.Background()
	tools := toolset.GetTools()
	executeSQLTool := tools[4] // execute_sql is fifth

	// Test valid SELECT query
	result, err := executeSQLTool.Handler(ctx, map[string]interface{}{
		"query": "SELECT * FROM users WHERE active = true",
	})
	if err != nil {
		t.Fatalf("execute_sql failed: %v", err)
	}

	queryResult, ok := result.(*QueryResult)
	if !ok {
		t.Fatal("Result is not QueryResult")
	}

	if queryResult.Query != "SELECT * FROM users WHERE active = true" {
		t.Error("Query not preserved in result")
	}

	// Test invalid query (write operation in READ_ONLY mode)
	_, err = executeSQLTool.Handler(ctx, map[string]interface{}{
		"query": "DELETE FROM users",
	})
	if err == nil {
		t.Error("Expected error for DELETE in READ_ONLY mode")
	}
}

func TestDatabaseToolset_Forecast(t *testing.T) {
	config := DefaultDatabaseConfig(DatabaseTypeBigQuery, "bigquery://project/dataset")
	toolset, _ := NewDatabaseToolset(config)

	ctx := context.Background()
	tools := toolset.GetTools()
	forecastTool := tools[5] // forecast is sixth

	result, err := forecastTool.Handler(ctx, map[string]interface{}{
		"table":        "dataset.sales",
		"time_column":  "date",
		"value_column": "revenue",
		"horizon":      10,
		"model_type":   "arima",
	})
	if err != nil {
		t.Fatalf("forecast failed: %v", err)
	}

	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("Result is not a map")
	}

	// Check that result indicates Spark ML usage
	if status, exists := resultMap["status"]; !exists || status != "implemented_via_spark_ml" {
		t.Error("Forecast should indicate Spark ML implementation")
	}
}

func TestDatabaseToolset_AskDataInsights(t *testing.T) {
	config := DefaultDatabaseConfig(DatabaseTypePostgreSQL, "postgresql://localhost:5432/testdb")
	toolset, _ := NewDatabaseToolset(config)

	ctx := context.Background()
	tools := toolset.GetTools()
	askTool := tools[6] // ask_data_insights is seventh

	result, err := askTool.Handler(ctx, map[string]interface{}{
		"question": "What are the top 10 customers by revenue?",
	})
	if err != nil {
		t.Fatalf("ask_data_insights failed: %v", err)
	}

	resultMap, ok := result.(map[string]interface{})
	if !ok {
		t.Fatal("Result is not a map")
	}

	if question, exists := resultMap["question"]; !exists || question != "What are the top 10 customers by revenue?" {
		t.Error("Question not preserved in result")
	}
}

func TestSimpleDatabaseToolset(t *testing.T) {
	config := DefaultDatabaseConfig(DatabaseTypePostgreSQL, "postgresql://localhost:5432/testdb")
	tools, err := SimpleDatabaseToolset(config)
	if err != nil {
		t.Fatalf("SimpleDatabaseToolset failed: %v", err)
	}

	if len(tools) != 7 {
		t.Errorf("Expected 7 tools, got %d", len(tools))
	}
}

func TestTableInfo_Struct(t *testing.T) {
	now := time.Now()
	info := &TableInfo{
		Database:           "testdb",
		Schema:             "public",
		Table:              "users",
		FullyQualifiedName: "testdb.public.users",
		Type:               "TABLE",
		Columns: []ColumnInfo{
			{
				Name:         "id",
				DataType:     "INTEGER",
				Nullable:     false,
				IsPrimaryKey: true,
				Position:     1,
			},
			{
				Name:     "name",
				DataType: "VARCHAR(255)",
				Nullable: true,
				Position: 2,
			},
		},
		RowCount:  1000,
		SizeBytes: 65536,
		CreatedAt: &now,
	}

	if len(info.Columns) != 2 {
		t.Errorf("Expected 2 columns, got %d", len(info.Columns))
	}

	if info.Columns[0].IsPrimaryKey != true {
		t.Error("First column should be primary key")
	}

	if info.RowCount != 1000 {
		t.Errorf("Expected row count 1000, got %d", info.RowCount)
	}
}

func TestQueryResult_Struct(t *testing.T) {
	result := &QueryResult{
		Columns: []string{"id", "name", "email"},
		Rows: []map[string]interface{}{
			{"id": 1, "name": "Alice", "email": "alice@example.com"},
			{"id": 2, "name": "Bob", "email": "bob@example.com"},
		},
		RowCount:       2,
		ExecutionTime:  0.125,
		BytesProcessed: 2048,
		Cached:         true,
		Query:          "SELECT * FROM users LIMIT 2",
	}

	if result.RowCount != len(result.Rows) {
		t.Error("RowCount doesn't match number of rows")
	}

	if len(result.Columns) != 3 {
		t.Errorf("Expected 3 columns, got %d", len(result.Columns))
	}

	if !result.Cached {
		t.Error("Result should be cached")
	}
}

func TestDatabaseConfig_AllowedDatabases(t *testing.T) {
	config := DefaultDatabaseConfig(DatabaseTypePostgreSQL, "postgresql://localhost:5432/testdb")
	config.AllowedDatabases = []string{"testdb", "analytics"}

	if len(config.AllowedDatabases) != 2 {
		t.Errorf("Expected 2 allowed databases, got %d", len(config.AllowedDatabases))
	}
}

func TestDatabaseConfig_AllowedTables(t *testing.T) {
	config := DefaultDatabaseConfig(DatabaseTypePostgreSQL, "postgresql://localhost:5432/testdb")
	config.AllowedTables = []string{"users", "orders", "products"}

	if len(config.AllowedTables) != 3 {
		t.Errorf("Expected 3 allowed tables, got %d", len(config.AllowedTables))
	}
}
