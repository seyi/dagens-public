# Dagens Agent Server - Load Testing Report

**Date:** 2025-12-09
**Version:** v0.1.0
**Commit SHA:** _To be filled after test run_

---

## Overview

This document contains load testing results, analysis, and recommendations for the Dagens Agent Server. Load tests are conducted using [k6](https://k6.io/) to validate system performance under various concurrent user loads.

### Objectives

1. Establish baseline performance metrics for agent execution endpoints
2. Identify system bottlenecks under load
3. Validate acceptable error rates and latency thresholds
4. Document performance characteristics for capacity planning

### Endpoints Tested

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Health check (liveness probe) |
| `/api/v1/agents` | GET | List available agents |
| `/api/v1/agents/execute` | POST | Execute single agent |
| `/api/v1/agents/batch_execute` | POST | Execute multiple agents in batch |

---

## Methodology

### Test Scenarios

1. **Execute Scenario** (`execute_scenario.js`)
   - Single agent execution with randomized agent selection
   - Tests: echo, summarizer, classifier, sentiment agents
   - Think time: 100-200ms between requests

2. **Batch Scenario** (`batch_scenario.js`)
   - Batch execution with 3-10 requests per batch
   - Mixed agent types per batch
   - Think time: 200-500ms between requests

3. **Health Scenario** (`health_scenario.js`)
   - High-frequency health check validation
   - Baseline latency measurement
   - Minimal think time (10ms)

### Load Levels

| Level | Virtual Users (VUs) | Duration | Purpose |
|-------|---------------------|----------|---------|
| Light | 10 | 2 min | Baseline, smoke test |
| Medium | 50 | 2 min | Normal production load |
| Heavy | 100 | 2 min | Peak load validation |

### Data Model

- Request payloads: JSON with agent_id, instruction, context
- Response payloads: JSON with output, metadata, success, duration_ms
- Instruction content: Randomized from predefined test strings

---

## Environment

### Test Environment

| Component | Specification |
|-----------|---------------|
| **Platform** | k3s on AWS EC2 |
| **Instance Type** | _To be filled_ |
| **CPU** | _To be filled_ |
| **Memory** | _To be filled_ |
| **Go Version** | 1.24 |
| **k6 Version** | _To be filled_ |

### Server Configuration

```yaml
# Helm values used for testing
replicaCount: 1
resources:
  limits:
    cpu: 200m
    memory: 256Mi
  requests:
    cpu: 100m
    memory: 128Mi
```

---

## Results

### Execute Endpoint (`/api/v1/agents/execute`)

#### 10 VUs (Light Load)

| Metric | Value |
|--------|-------|
| Requests/sec | _TBD_ |
| p50 Latency | _TBD_ |
| p95 Latency | _TBD_ |
| p99 Latency | _TBD_ |
| Error Rate | _TBD_ |
| Success Rate | _TBD_ |

#### 50 VUs (Medium Load)

| Metric | Value |
|--------|-------|
| Requests/sec | _TBD_ |
| p50 Latency | _TBD_ |
| p95 Latency | _TBD_ |
| p99 Latency | _TBD_ |
| Error Rate | _TBD_ |
| Success Rate | _TBD_ |

#### 100 VUs (Heavy Load)

| Metric | Value |
|--------|-------|
| Requests/sec | _TBD_ |
| p50 Latency | _TBD_ |
| p95 Latency | _TBD_ |
| p99 Latency | _TBD_ |
| Error Rate | _TBD_ |
| Success Rate | _TBD_ |

### Batch Execute Endpoint (`/api/v1/agents/batch_execute`)

#### 10 VUs (Light Load)

| Metric | Value |
|--------|-------|
| Requests/sec | _TBD_ |
| Avg Batch Size | _TBD_ |
| p50 Latency | _TBD_ |
| p95 Latency | _TBD_ |
| Error Rate | _TBD_ |

#### 50 VUs (Medium Load)

| Metric | Value |
|--------|-------|
| Requests/sec | _TBD_ |
| Avg Batch Size | _TBD_ |
| p50 Latency | _TBD_ |
| p95 Latency | _TBD_ |
| Error Rate | _TBD_ |

### Health Endpoint (`/health`)

#### 100 VUs Baseline

| Metric | Value |
|--------|-------|
| Requests/sec | _TBD_ |
| p50 Latency | _TBD_ |
| p95 Latency | _TBD_ |
| p99 Latency | _TBD_ |
| Error Rate | _TBD_ |

---

## Analysis

### Performance Observations

1. **Baseline Performance**
   - _Analysis to be added after test execution_

2. **Scaling Behavior**
   - _Analysis to be added after test execution_

3. **Error Patterns**
   - _Analysis to be added after test execution_

### Identified Bottlenecks

| Bottleneck | Severity | Impact | Recommendation |
|------------|----------|--------|----------------|
| _TBD_ | _TBD_ | _TBD_ | _TBD_ |

### Resource Utilization

| Metric | 10 VUs | 50 VUs | 100 VUs |
|--------|--------|--------|---------|
| CPU Usage | _TBD_ | _TBD_ | _TBD_ |
| Memory Usage | _TBD_ | _TBD_ | _TBD_ |
| Network I/O | _TBD_ | _TBD_ | _TBD_ |

---

## Recommendations

### Immediate Actions

1. **_TBD_**: Description of immediate optimization
2. **_TBD_**: Description of immediate optimization

### Future Optimizations

1. **Connection Pooling**: Consider implementing HTTP connection pooling for batch requests
2. **GOMAXPROCS Tuning**: Validate GOMAXPROCS setting matches container CPU limits
3. **HTTP Server Timeouts**: Review and tune server timeout configurations
4. **Agent Execution Caching**: Consider caching for deterministic agent responses

### Target SLOs

Based on test results, proposed Service Level Objectives:

| Metric | Target |
|--------|--------|
| p95 Latency (execute) | < 500ms |
| p99 Latency (execute) | < 1000ms |
| Error Rate | < 0.1% |
| Availability | > 99.9% |

---

## How to Run Tests

### Prerequisites

1. Install k6: https://k6.io/docs/get-started/installation/
2. Ensure agent-server is running and accessible

### Running Individual Scenarios

```bash
# Execute scenario - light load
k6 run --vus 10 --duration 2m tests/load/k6/execute_scenario.js

# Execute scenario - medium load
VUS=50 DURATION=2m BASE_URL=http://localhost:8080 k6 run tests/load/k6/execute_scenario.js

# Execute scenario - heavy load
VUS=100 DURATION=2m k6 run tests/load/k6/execute_scenario.js

# Batch scenario
k6 run --vus 10 --duration 2m tests/load/k6/batch_scenario.js

# Health scenario (high VUs)
k6 run --vus 100 --duration 1m tests/load/k6/health_scenario.js
```

### Running Against k3s Deployment

```bash
# Port forward the service
kubectl port-forward svc/dagens 8080:80 -n dagens &

# Run tests
BASE_URL=http://localhost:8080 k6 run tests/load/k6/execute_scenario.js
```

### Exporting Results

```bash
# Export JSON summary
k6 run --summary-export results.json tests/load/k6/execute_scenario.js

# Export to InfluxDB (for Grafana dashboards)
k6 run --out influxdb=http://localhost:8086/k6 tests/load/k6/execute_scenario.js
```

---

## Appendix

### k6 JSON Summary Files

_Attach or link to JSON summary files from test runs_

### Raw Metrics

_Include any additional raw metrics or graphs_

### Test Environment Details

_Include detailed environment specifications if needed_

---

## Revision History

| Date | Version | Changes |
|------|---------|---------|
| 2025-12-09 | 0.1.0 | Initial template created |

---

**Report generated by:** Dagens E2E Testing Suite
**Test framework:** k6 by Grafana Labs
