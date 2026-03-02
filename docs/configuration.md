# Configuration

## Table of Contents

- [Overview](#overview)
- [Environment Variables](#environment-variables)
- [config.yaml](#configyaml)
- [Database Configuration](#database-configuration)
- [Server Configuration](#server-configuration)
- [Docker Configuration](#docker-configuration)
- [Default Function Settings](#default-function-settings)
- [Container Limits](#container-limits)
- [Cleanup Intervals](#cleanup-intervals)
- [Logging Configuration](#logging-configuration)
- [EvtQ Integration Settings](#evtq-integration-settings)

## Overview

Current implementation reads runtime/server settings from environment variables only.

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `LISTEN_ADDR` | `:8081` | HTTP listen address |
| `DATABASE_URL` | `postgres://localhost:5432/evtq_faas?sslmode=disable` | PostgreSQL DSN |
| `MAX_CONCURRENT_CONTAINERS` | `20` | Global running container cap |
| `WARM_POOL_ACQUIRE_TIMEOUT_MS` | `15000` | Max wait for warm container |
| `WARM_CONTAINER_IDLE_TIMEOUT_MS` | `900000` | Idle warm container eviction |
| `WARM_CONTAINER_SWEEP_INTERVAL_MS` | `30000` | Idle sweeper interval |
| `WARM_CONTAINER_RECYCLE_INVOCATIONS` | `50` | Per-container recycle threshold |
| `DEFAULT_FUNCTION_CPUS` | `0.5` | Default CPU quota |
| `DEFAULT_FUNCTION_PIDS_LIMIT` | `256` | PID limit per container |
| `CONTAINER_READ_ONLY_FS` | `true` | Read-only root filesystem |
| `FAAS_DOCKER_NETWORK` | `faas-net` | Docker network name |
| `DOCKER_HOST` | Docker default | Docker daemon socket/endpoint used by `docker` CLI |

## config.yaml

`config.yaml` is documented as a future structured config format, but is not currently parsed by the server binary.

```yaml
server:
  listen_addr: ":8081"
  read_timeout_seconds: 30
  write_timeout_seconds: 30
  idle_timeout_seconds: 60

database:
  url: "postgres://localhost:5432/evtq_faas?sslmode=disable"
  max_open_conns: 20
  max_idle_conns: 10
  conn_max_lifetime_seconds: 300

docker:
  host: "unix:///var/run/docker.sock"
  network_name: "faas-net"
  read_only_fs: true
  default_cpus: 0.5
  default_pids_limit: 256

runtime:
  max_concurrent_containers: 20
  warm_pool_acquire_timeout_ms: 15000
  warm_container_idle_timeout_ms: 900000
  warm_container_sweep_interval_ms: 30000
  warm_container_recycle_invocations: 50
  default_timeout_seconds: 30
  default_memory_mb: 128
  default_strategy: "warm"
  default_warm_pool_size: 1

cleanup:
  orphan_sweep_interval_seconds: 60
  log_retention_days: 14

logging:
  level: "info"
  format: "json"

integration:
  evtq_base_url: "http://localhost:8080"
  evtq_internal_token: ""
```

## Database Configuration

- FaaS uses PostgreSQL for function metadata and logs.
- FaaS and EvtQ may share one PostgreSQL instance.
- Recommended: separate DB names (`evtq`, `evtq_faas`) in one cluster.

## Server Configuration

- Bind to `:8081` for FaaS API.
- Enable conservative timeouts to avoid stalled clients.
- Keep graceful shutdown enabled to drain inflight requests.

## Docker Configuration

- FaaS must access Docker daemon.
- Set `DOCKER_HOST` explicitly when using rootless Docker.
- Use dedicated bridge network and avoid host network mode.

## Default Function Settings

Applied when omitted in create request:
- `timeout_seconds`: `30`
- `memory_mb`: `128`
- `container_strategy`: `warm`
- `warm_pool_size`: `1`

## Container Limits

- Global max running containers via `MAX_CONCURRENT_CONTAINERS`.
- Per-function behavior through pool size and strategy.
- Resource controls enforce local safety over max throughput.

## Cleanup Intervals

- Idle container sweep interval controls how frequently warm pools are trimmed.
- Idle timeout controls warm container lifetime.
- Optional log retention cleanup should run as scheduled SQL job.

## Logging Configuration

- JSON logs recommended for ingestion.
- Function logs are persisted in `function_logs`.
- Keep app logs and function logs separated conceptually.

## EvtQ Integration Settings

- Triggers are configured in EvtQ, not FaaS.
- See [EvtQ Integration](./evtq-integration.md) and [EvtQ Concepts](../evtq/docs/concepts.md).
