# Design Decisions

## Table of Contents

- [Docker as Execution Substrate](#docker-as-execution-substrate)
- [HTTP Runtime Interface](#http-runtime-interface)
- [Buffered Channel Warm Pool](#buffered-channel-warm-pool)
- [Container Recycling](#container-recycling)
- [Shared PostgreSQL with EvtQ](#shared-postgresql-with-evtq)
- [Cold vs Warm Tradeoffs](#cold-vs-warm-tradeoffs)
- [Conservative Resource Defaults](#conservative-resource-defaults)
- [PostgreSQL Log Storage](#postgresql-log-storage)
- [Alternatives Considered](#alternatives-considered)

## Docker as Execution Substrate

Decision: use Docker containers for runtime isolation.

Why:
- universally available in local dev,
- deterministic packaging model,
- well-understood resource controls.

Tradeoff:
- slower than microVM-native approaches in some cases.

## HTTP Runtime Interface

Decision: runtime contract is `GET /health` and `POST /invoke`.

Why:
- simple language-agnostic contract,
- easy to debug with curl,
- avoids runtime-specific stdin/stdout protocols.

Tradeoff:
- minor HTTP overhead per invoke.

## Buffered Channel Warm Pool

Decision: one buffered channel per function as the warm pool.

Why:
- lock-light acquire/return path,
- predictable bounded capacity,
- simple backpressure semantics.

Tradeoff:
- less feature-rich than full scheduler queues.

## Container Recycling

Decision: recycle warm containers by invocation count and idle time.

Why:
- mitigates stale state and memory creep,
- reduces long-lived runtime drift,
- keeps local reliability stable across long test sessions.

## Shared PostgreSQL with EvtQ

Decision: run FaaS and EvtQ against one PostgreSQL cluster.

Why:
- operational simplicity for local stacks,
- fewer moving parts for dev/test,
- easy transactional reasoning for metadata and logs.

Tradeoff:
- shared DB resource contention if not sized correctly.

## Cold vs Warm Tradeoffs

- Cold: highest isolation, higher latency.
- Warm: lower latency, more lifecycle complexity.

Decision: support both and let users choose per function.

## Conservative Resource Defaults

Decision: default low CPU/memory/PID values and read-only filesystem.

Why:
- prevents host instability in local environments,
- encourages explicit sizing for production-like testing.

## PostgreSQL Log Storage

Decision: store function logs in PostgreSQL initially.

Why:
- simple query path via API and SQL,
- no extra infra for local users.

When to change:
- high-volume production usage should move logs to dedicated systems (Loki, Elasticsearch, cloud log backends).

## Alternatives Considered

### Firecracker
- Rejected for initial phase due to setup complexity for typical local dev workflows.

### gVisor
- Rejected initially; stronger isolation but higher operational complexity.

### Wasm runtimes
- Rejected for now; limited compatibility for existing Lambda-like handler ecosystems.

### Process-per-invoke (no containers)
- Rejected due to weak isolation and inconsistent dependency/runtime behavior.

### Lambda Runtime API polling model
- Rejected in favor of direct HTTP `/invoke` for local simplicity.
