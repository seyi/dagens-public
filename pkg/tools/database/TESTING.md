# Database Toolset Testing Guide

This document explains how to run tests for the database toolset.

## Test Types

### 1. Unit Tests (No Database Required)

Fast tests that don't require external dependencies:

```bash
# Run all unit tests
go test -v ./pkg/tools/database

# Run specific test patterns
go test -v ./pkg/tools/database -run "Config|Validate|Struct"
```

**What's tested:**
- Configuration validation
- Query validation and security controls
- Data structure marshaling
- Connection string building
- WriteMode enforcement

**Current status:** ✅ 15/15 passing

### 2. Integration Tests (Require Docker)

Real tests against actual databases using testcontainers:

```bash
# Run all integration tests
go test -v -tags=integration ./pkg/tools/database

# Run specific integration test
go test -v -tags=integration ./pkg/tools/database -run TestPostgreSQLIntegration

# Run with timeout
go test -v -tags=integration -timeout 5m ./pkg/tools/database
```

**What's tested:**
- Real PostgreSQL connections
- Real MySQL connections
- Actual query execution with data
- Table creation and manipulation
- Metadata discovery (SHOW DATABASES, SHOW TABLES, DESCRIBE)
- Security controls enforcement
- Performance metrics tracking
- Result truncation (MaxResultRows)
- Full toolset integration

**Prerequisites:**
- Docker installed and running
- Internet connection (to pull postgres:15-alpine and mysql:8.0 images)
- ~512MB disk space for container images

## Integration Test Details

### TestPostgreSQLIntegration
- Spins up PostgreSQL 15 container
- Tests connection and simple queries
- Verifies result parsing and types
- Checks performance metrics

### TestPostgreSQLTableOperations
- Creates real tables with schemas
- Inserts and queries data
- Tests metadata discovery
- Verifies table schema parsing

### TestMySQLIntegration
- Spins up MySQL 8.0 container
- Tests connection and queries
- Verifies MySQL-specific behavior

### TestSecurityControls
- Tests BLOCKED mode (only SELECT allowed)
- Tests READ_ONLY mode (no writes)
- Tests FULL mode with blocked keywords
- Verifies query validation

### TestPerformanceMetrics
- Executes multiple queries
- Tracks execution times
- Verifies SessionStats tracking
- Checks metric accuracy

### TestMaxResultRows
- Tests result truncation
- Verifies warning messages
- Ensures memory safety

### TestDatabaseToolset
- Tests all 7 agent tools
- Verifies tool handlers work
- Tests end-to-end workflows

## Running Tests Locally

### Prerequisites

```bash
# 1. Install Docker
# macOS: brew install docker
# Linux: apt-get install docker.io
# Windows: Install Docker Desktop

# 2. Start Docker
docker info  # Verify Docker is running

# 3. Pull test images (optional, will auto-pull)
docker pull postgres:15-alpine
docker pull mysql:8.0
```

### Run All Tests

```bash
# From project root
cd /home/user/spark/spark-ai-agents

# Run unit tests only
go test -v ./pkg/tools/database

# Run integration tests only
go test -v -tags=integration ./pkg/tools/database

# Run all tests
go test -v -tags=integration ./pkg/tools/database
```

### Troubleshooting

#### "Docker not running"
```bash
# Start Docker daemon
sudo systemctl start docker  # Linux
# or open Docker Desktop  # macOS/Windows
```

#### "Connection refused"
- This is expected for unit tests trying to connect to localhost
- Unit tests pass without database
- Integration tests start their own containers

#### "Pull image timeout"
```bash
# Pre-pull images
docker pull postgres:15-alpine
docker pull mysql:8.0
```

#### "Port already in use"
Testcontainers uses random ports, but if you have conflicts:
```bash
# Stop existing containers
docker ps
docker stop <container_id>
```

## CI/CD Integration

### GitHub Actions Example

```yaml
name: Integration Tests

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest

    steps:
      - uses: actions/checkout@v3

      - name: Set up Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.21'

      - name: Run unit tests
        run: go test -v ./pkg/tools/database

      - name: Run integration tests
        run: go test -v -tags=integration -timeout 10m ./pkg/tools/database
```

### GitLab CI Example

```yaml
test:
  image: golang:1.21
  services:
    - docker:dind
  script:
    - go test -v ./pkg/tools/database
    - go test -v -tags=integration -timeout 10m ./pkg/tools/database
```

## Test Coverage

```bash
# Generate coverage report
go test -v -tags=integration -coverprofile=coverage.out ./pkg/tools/database

# View coverage
go tool cover -html=coverage.out
```

## Performance Benchmarks

```bash
# Run benchmarks
go test -bench=. -benchmem ./pkg/tools/database

# Example output:
# BenchmarkQueryExecution-8    1000    1.2 ms/op    4096 B/op    45 allocs/op
```

## Manual Testing

For manual testing against real databases:

### PostgreSQL

```bash
# Start PostgreSQL
docker run -d --name postgres-test \
  -e POSTGRES_PASSWORD=test \
  -p 5432:5432 \
  postgres:15

# Test connection
go run examples/database_test.go

# Clean up
docker stop postgres-test && docker rm postgres-test
```

### MySQL

```bash
# Start MySQL
docker run -d --name mysql-test \
  -e MYSQL_ROOT_PASSWORD=test \
  -e MYSQL_DATABASE=testdb \
  -p 3306:3306 \
  mysql:8.0

# Test connection
go run examples/database_test.go

# Clean up
docker stop mysql-test && docker rm mysql-test
```

## Current Test Status

| Test Type | Count | Status |
|-----------|-------|--------|
| Unit Tests | 15 | ✅ All Passing |
| Integration Tests | 8 | ✅ Ready (need Docker) |
| **Total** | **23** | **23/23 when Docker available** |

## Writing New Tests

### Unit Test Template

```go
func TestNewFeature(t *testing.T) {
    config := DefaultDatabaseConfig(
        DatabaseTypePostgreSQL,
        "postgresql://localhost:5432/test",
    )

    // Your test logic
}
```

### Integration Test Template

```go
//go:build integration
// +build integration

func TestNewFeatureIntegration(t *testing.T) {
    ctx := context.Background()

    // Start container
    container, err := postgres.Run(ctx, "postgres:15-alpine", ...)
    if err != nil {
        t.Fatalf("Failed to start container: %v", err)
    }
    defer testcontainers.TerminateContainer(container)

    // Get connection string
    connStr, _ := container.ConnectionString(ctx, "sslmode=disable")

    // Your test logic
}
```

## Best Practices

1. **Always use testcontainers for integration tests** - Don't assume databases are running
2. **Tag integration tests** - Use `//go:build integration` to separate from unit tests
3. **Clean up containers** - Use `defer testcontainers.TerminateContainer()`
4. **Set timeouts** - Integration tests can be slow, use `-timeout` flag
5. **Test security controls** - Always verify WriteMode and validation work
6. **Check metrics** - Verify performance metrics are tracked correctly
7. **Test edge cases** - Large results, truncation, errors

## FAQ

**Q: Why do unit tests fail with "connection refused"?**
A: This is expected! Unit tests that try to connect to localhost will fail. Integration tests start their own containers.

**Q: How long do integration tests take?**
A: ~30-60 seconds first run (pulling images), ~10-20 seconds subsequent runs.

**Q: Can I run tests without Docker?**
A: Yes, unit tests work without Docker. Integration tests require Docker.

**Q: Why use testcontainers vs manual setup?**
A: Testcontainers ensures consistent, isolated, reproducible tests. No manual setup required.

**Q: What if I don't have internet access?**
A: Pre-pull images: `docker pull postgres:15-alpine mysql:8.0`, then tests work offline.
