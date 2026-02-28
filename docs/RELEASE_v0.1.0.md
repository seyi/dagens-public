# v0.1.0

Initial public OSS release.

## Caveats

- The default public CI gate covers only the verified Tier 1 Go test subset documented in `docs/TESTING.md`.
- The broad recursive suite (`go test ./pkg/... ./cmd/...`) is not the release gate for v0.1.x and is expected to include failing or environment-dependent packages.
- Tier 2 packages require explicit environment setup such as Docker, shells in PATH, or provider credentials.
- Tier 3 packages remain a repair backlog and are not release-blocking for v0.1.x.

## Release Gate

```bash
go test \
  ./pkg/a2a \
  ./pkg/actor \
  ./pkg/agent \
  ./pkg/agentic \
  ./pkg/events \
  ./pkg/evaluation \
  ./pkg/registry \
  ./pkg/resilience \
  ./pkg/scheduler \
  ./pkg/secrets \
  ./pkg/state \
  ./pkg/telemetry
```
