# Mixed-Version Validation Plan

This document defines the first validation plan for adjacent-version rolling
deploy compatibility.

It is deliberately scoped to what Dagens can support today:
- adjacent-version skew only (`N` with `N+1`)
- additive schema evolution only
- no replay-critical semantic drift without explicit release handling

Related policy:
- [`docs/VERSION_SKEW_POLICY.md`](./VERSION_SKEW_POLICY.md)

## Goal

Prove that a conservative rolling deploy does not break:
- scheduler leadership/takeover
- durable replay of unfinished jobs
- worker heartbeat ingestion
- API submission/forwarding behavior
- HITL checkpoint handling for compatible graph versions

## Validation Matrix

### 1. Control-plane adjacency

Required scenarios:
1. old leader (`N`) with new follower (`N+1`)
2. new leader (`N+1`) with old follower (`N`)
3. rollback from `N+1` back to `N` within the same rollout window

Success criteria:
- no replay failure at startup
- no leader-forwarding loop or submission outage beyond expected transition window
- no durable-state corruption or unreadable rows

### 2. Worker/API adjacency

Required scenarios:
1. old workers -> new API servers
2. new workers -> old API servers
3. mixed worker fleet -> mixed adjacent API fleet

Success criteria:
- heartbeat requests remain accepted
- no retry amplification from request-shape mismatch
- no unexpected `4xx`/`5xx` from adjacent-version worker/API traffic

### 3. Replay compatibility

Required scenarios:
1. unfinished jobs written by `N`, replayed by `N+1`
2. unfinished jobs written by `N+1`, replayed by `N` only if rollback/leadership makes that possible

Success criteria:
- replay succeeds
- unfinished job visibility matches pre-restart expectation
- no malformed transition/replay failure from adjacent-version rows

Note:
- if `N+1` writes replay-critical semantics that `N` cannot interpret, this is not a rolling-compatible release and must be reclassified

### 4. HITL compatibility

Required scenarios:
1. compatible graph/checkpoint rollout across `N` -> `N+1`
2. incompatible graph/checkpoint rollout must fail safely

Success criteria:
- compatible checkpoint resumes continue working
- incompatible checkpoint resumes fail by policy:
  - mismatch detected
  - no silent auto-resume
  - DLQ/manual remediation path remains intact

### 5. Archive/prune tooling compatibility

Required scenarios:
1. current tooling reads current archive schema (`transition-archive-v1`)
2. if a new archive schema is introduced, rollout notes define tooling upgrade order

Success criteria:
- old tooling either reads the current schema successfully or rejects unknown versions clearly
- no silent archive corruption across mixed tooling use

## First Test Plan

### Test A: Adjacent control-plane rolling deploy

Setup:
- two control-plane instances
- shared Postgres transition store
- scheduler leadership enabled

Steps:
1. start both nodes on `N`
2. submit durable unfinished work
3. upgrade follower to `N+1`
4. verify submissions, forwarding, and follower replay remain healthy
5. fail over leadership to `N+1`
6. verify durable unfinished work visibility and continued operation
7. optionally rollback one node to `N`

### Test B: Adjacent worker/API skew

Setup:
- mixed adjacent API/control-plane nodes
- worker heartbeat path active

Steps:
1. run old worker heartbeat traffic against new API path
2. run new worker heartbeat traffic against old API path
3. validate no unexpected auth/payload regression

### Test C: Replay regression gate

Setup:
- isolated Postgres instance
- seeded unfinished jobs

Steps:
1. seed with version `N`
2. replay with `N+1`
3. if rollback is supported for this release, seed with `N+1` and replay with `N`

### Test D: HITL compatibility gate

Setup:
- compatible and incompatible checkpoint samples

Steps:
1. verify compatible resumes still succeed
2. verify incompatible resumes fail by policy and do not auto-resume

## Release-Gate Rules

Minimum gate for an adjacent-version rolling-compatible release:
1. Test A passes
2. Test B passes
3. Test C passes
4. if HITL or graph behavior changed, Test D passes

If any of these fail:
- the release is not “safe for rolling deploy”
- it must be reclassified as conditional or coordinated/lockstep

## What This Plan Does Not Yet Guarantee

This plan does not yet prove:
- arbitrary version mixing
- multi-release skip compatibility
- destructive schema migration safety during live mixed windows
- automatic downgrade compatibility after replay-critical semantic change

## Next Work

After this plan:
1. add migration runbook requirements for schema/checkpoint evolution
2. tie release categories in `VERSION_SKEW_POLICY.md` to required validation subsets
3. execute the first adjacent-version harness using version-pinned images

## Harness Scaffold

The first executable harness now exists as:

- `scripts/mixed_version_validation.sh`
- `deploy/ha/docker-compose.mixed-version.yml`

Current scope of the harness:
- uses the existing HA topology
- requires caller-supplied adjacent images for API and worker slots
- runs the first adjacent control-plane gate via `scripts/failover_drill.sh`

Current artifact contract:
- `MIXED_VERSION_API_IMAGE_A`
- `MIXED_VERSION_API_IMAGE_B`
- `MIXED_VERSION_WORKER_IMAGE_A`
- `MIXED_VERSION_WORKER_IMAGE_B`

Artifact-preparation helper now added:
- `scripts/build_mixed_version_images.sh`
- builds version-pinned API and worker images from:
  - `REF_OLD`
  - `REF_NEW`
- writes the image environment contract consumed by:
  - `scripts/mixed_version_validation.sh`

This keeps the mixed-version validation path honest:
- the harness is executable now
- but it does not pretend version skew has been validated until real `N` and `N+1`
  artifacts are supplied
