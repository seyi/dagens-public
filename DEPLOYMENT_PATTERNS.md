# Dagens Deployment Patterns

## Overview
This document provides recommended deployment patterns for running Dagens in production environments, including Docker and Kubernetes configurations.

## Architecture Considerations

### Single Node Deployment
- **Use Case**: Development, testing, small-scale production
- **Components**: All services on single node
- **Scalability**: Limited by single node resources
- **Availability**: Single point of failure

### Multi-Node Deployment
- **Use Case**: Production environments requiring high availability
- **Components**: Distributed across multiple nodes
- **Scalability**: Horizontal scaling capabilities
- **Availability**: Fault tolerance through redundancy

## Docker Deployment

### Single Container Deployment
```dockerfile
FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o dagens ./cmd/dagens/main.go

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/dagens .
EXPOSE 8080
CMD ["./dagens"]
```

### Multi-Service Docker Compose
```yaml
version: '3.8'

services:
  dagens-api:
    build: .
    ports:
      - "8080:8080"
    environment:
      - OTEL_EXPORTER_OTLP_ENDPOINT=jaeger:4317
      - OTEL_SERVICE_NAME=dagens-api
    depends_on:
      - redis
      - jaeger
    deploy:
      replicas: 2

  dagens-workers:
    build: .
    command: ["./dagens", "worker"]
    environment:
      - OTEL_EXPORTER_OTLP_ENDPOINT=jaeger:4317
      - OTEL_SERVICE_NAME=dagens-workers
    depends_on:
      - redis
      - queue
    deploy:
      replicas: 3

  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
    volumes:
      - redis_data:/data
    command: redis-server --appendonly yes

  queue:
    image: rabbitmq:3-management
    ports:
      - "5672:5672"
      - "15672:15672"
    environment:
      RABBITMQ_DEFAULT_USER: admin
      RABBITMQ_DEFAULT_PASS: password

  jaeger:
    image: jaegertracing/all-in-one:latest
    environment:
      - COLLECTOR_OTLP_ENABLED=true
    ports:
      - "16686:16686"
      - "4317:4317"
      - "4318:4318"

volumes:
  redis_data:
```

### Environment Configuration
```bash
# dagens.env
# Service Configuration
SERVICE_NAME=dagens-production
SERVICE_VERSION=1.2.3
PORT=8080

# Observability Configuration
OTEL_EXPORTER_OTLP_ENDPOINT=jaeger-collector:4317
OTEL_SERVICE_NAME=dagens-production
OTEL_TRACES_SAMPLER_ARG=0.05  # 5% sampling for production
OTEL_METRICS_EXPORT_INTERVAL=30s

# Redis Configuration
REDIS_URL=redis:6379
REDIS_PASSWORD=your-redis-password

# Queue Configuration
QUEUE_URL=amqp://admin:password@queue:5672

# Agent Configuration
AGENT_POOL_SIZE=10
AGENT_TIMEOUT=30s
AGENT_RETRY_COUNT=3

# Health Check Configuration
HEALTH_CHECK_INTERVAL=30s
HEALTH_CHECK_TIMEOUT=5s

# Circuit Breaker Configuration
CIRCUIT_BREAKER_FAILURE_THRESHOLD=5
CIRCUIT_BREAKER_TIMEOUT=30s
CIRCUIT_BREAKER_SUCCESS_THRESHOLD=2
```

## Kubernetes Deployment

### Namespace and RBAC
```yaml
# namespace.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: dagens
---
# service-account.yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: dagens-sa
  namespace: dagens
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: dagens-role
rules:
- apiGroups: [""]
  resources: ["pods", "services", "configmaps", "secrets"]
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: dagens-rolebinding
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: dagens-role
subjects:
- kind: ServiceAccount
  name: dagens-sa
  namespace: dagens
```

### ConfigMap for Configuration
```yaml
# configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: dagens-config
  namespace: dagens
data:
  service.yaml: |
    service:
      name: dagens-api
      version: "1.2.3"
      port: 8080
    observability:
      otel:
        endpoint: "jaeger-collector.dagens:4317"
        service_name: "dagens-api"
        sample_ratio: 0.05
    redis:
      url: "redis.dagens.svc.cluster.local:6379"
    queue:
      url: "rabbitmq.dagens.svc.cluster.local:5672"
```

### Deployment with Horizontal Pod Autoscaler
```yaml
# deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: dagens-api
  namespace: dagens
spec:
  replicas: 3
  selector:
    matchLabels:
      app: dagens-api
  template:
    metadata:
      labels:
        app: dagens-api
    spec:
      serviceAccountName: dagens-sa
      containers:
      - name: dagens
        image: your-registry/dagens:latest
        ports:
        - containerPort: 8080
        env:
        - name: OTEL_EXPORTER_OTLP_ENDPOINT
          value: "jaeger-collector.dagens:4317"
        - name: OTEL_SERVICE_NAME
          value: "dagens-api"
        - name: OTEL_TRACES_SAMPLER_ARG
          value: "0.05"
        - name: REDIS_URL
          value: "redis.dagens.svc.cluster.local:6379"
        resources:
          requests:
            memory: "128Mi"
            cpu: "100m"
          limits:
            memory: "512Mi"
            cpu: "500m"
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 30
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /ready
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 5
---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: dagens-api-hpa
  namespace: dagens
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: dagens-api
  minReplicas: 3
  maxReplicas: 10
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 70
  - type: Resource
    resource:
      name: memory
      target:
        type: Utilization
        averageUtilization: 80
```

### Service and Ingress
```yaml
# service.yaml
apiVersion: v1
kind: Service
metadata:
  name: dagens-api-service
  namespace: dagens
  annotations:
    service.beta.kubernetes.io/aws-load-balancer-type: "nlb"
spec:
  selector:
    app: dagens-api
  ports:
    - protocol: TCP
      port: 80
      targetPort: 8080
  type: LoadBalancer
---
# ingress.yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: dagens-ingress
  namespace: dagens
  annotations:
    nginx.ingress.kubernetes.io/rate-limit: "100"
    nginx.ingress.kubernetes.io/rate-limit-window: "1m"
spec:
  rules:
  - host: dagens.yourdomain.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: dagens-api-service
            port:
              number: 80
```

## Production Deployment Checklist

### Pre-Deployment
- [ ] Load test results validated
- [ ] Resource requirements calculated
- [ ] Security scanning completed
- [ ] Configuration reviewed
- [ ] Backup/restore procedures tested

### Deployment
- [ ] Blue-green or canary deployment strategy
- [ ] Health checks validated
- [ ] Monitoring and alerting configured
- [ ] Rollback plan prepared
- [ ] Traffic gradually shifted

### Post-Deployment
- [ ] Performance metrics validated
- [ ] Error rates monitored
- [ ] Resource utilization checked
- [ ] Circuit breaker behavior verified
- [ ] Tracing end-to-end tested

## Scaling Patterns

### Horizontal Pod Autoscaling (HPA)
- CPU and memory based scaling
- Custom metrics scaling (queue depth, pending requests)
- Target utilization percentages

### Vertical Pod Autoscaling (VPA)
- Resource recommendation based on usage
- Automated resource adjustment
- Memory and CPU optimization

### Cluster Autoscaling
- Node pool scaling based on resource requests
- Cost optimization through node tainting
- Multi-zone deployment for availability

## Monitoring and Observability

### Key Metrics to Monitor
- Request rate (RPS)
- Error rate (%)
- Latency (p50, p95, p99)
- Circuit breaker states
- Queue depth
- Memory and CPU usage
- Active connections

### Alerting Rules
```yaml
# Prometheus alerting rules
groups:
- name: dagens.rules
  rules:
  - alert: HighErrorRate
    expr: rate(dagens_requests_total{status="error"}[5m]) / rate(dagens_requests_total[5m]) > 0.05
    for: 2m
    labels:
      severity: critical
    annotations:
      summary: "High error rate detected"
      description: "Error rate is above 5% for more than 2 minutes"

  - alert: HighLatency
    expr: histogram_quantile(0.95, rate(dagens_request_duration_seconds_bucket[5m])) > 1
    for: 5m
    labels:
      severity: warning
    annotations:
      summary: "High latency detected"
      description: "95th percentile latency is above 1 second"

  - alert: CircuitBreakerOpen
    expr: dagens_circuit_breaker_state == 1
    for: 1m
    labels:
      severity: warning
    annotations:
      summary: "Circuit breaker is open"
      description: "A circuit breaker has opened, indicating service degradation"
```

## Security Considerations

### Network Security
- Network policies to restrict traffic
- Service mesh for mTLS
- API gateway for authentication

### Secrets Management
- Kubernetes secrets for sensitive data
- External secret stores (HashiCorp Vault, AWS Secrets Manager)
- Environment variable encryption

### Access Control
- RBAC for Kubernetes resources
- API authentication and authorization
- Rate limiting and quotas

## Disaster Recovery

### Backup Strategies
- Redis persistence configuration
- Configuration versioning
- Database backup procedures

### Recovery Procedures
- Rollback procedures
- Data restoration processes
- Failover mechanisms

This deployment documentation provides a solid foundation for production deployments and should help improve the "Production Proof" score by demonstrating production-ready deployment patterns.