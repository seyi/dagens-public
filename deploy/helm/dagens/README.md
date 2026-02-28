# Dagens Helm Chart

This chart deploys the Dagens agent server.

## Prerequisites
- Kubernetes 1.24+
- Helm 3.10+
- Container image accessible (default `ghcr.io/dagens/agent-server:latest`)
- Ingress controller if enabling ingress

## Install (dev dry-run)
```bash
helm upgrade --install dagens-agent deploy/helm/dagens \
  --namespace dagens --create-namespace \
  --values deploy/helm/dagens/values-dev.yaml \
  --dry-run=server
```

## Install (staging)
```bash
helm upgrade --install dagens-agent deploy/helm/dagens \
  --namespace dagens --create-namespace \
  --values deploy/helm/dagens/values-staging.yaml
```

## Key Features
- RollingDeployment with probes and securityContext
- PDB and optional HPA
- NetworkPolicy (default deny ingress; allow listed sources)
- Optional ServiceMonitor for Prometheus Operator
- Config checksum annotations for automatic restarts on config/secret change

Default probes expect:
- Liveness: `GET /health/live`
- Readiness: `GET /health/ready`
- Startup (optional): `GET /health/ready`

## Validation
```bash
helm lint deploy/helm/dagens
helm template dagens deploy/helm/dagens \
  --values deploy/helm/dagens/values-dev.yaml | kubeconform -strict
kubectl wait --for=condition=available deploy/dagens-agent -n dagens --timeout=120s
helm rollback dagens-agent 0
```

## Values
See `values.yaml` for defaults and the environment-specific files for overrides.
