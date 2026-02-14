# FaaS

Run serverless functions locally. No cloud required.

[![Go Version](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](./LICENSE)
[![Docker Required](https://img.shields.io/badge/Docker-24%2B-2496ED?logo=docker)](https://www.docker.com/)
[![Build Status](https://img.shields.io/badge/build-passing-brightgreen)](#)

FaaS is a local, self-hosted AWS Lambda clone built with Go and Docker. It runs user functions inside isolated containers, supports cold and warm execution strategies, and integrates with EvtQ as an event source. You can deploy a function from local source code, invoke it directly, or connect it to an EvtQ queue trigger for automatic event-driven execution.

## Table of Contents

- [Highlights](#highlights)
- [Quick Start](#quick-start)
- [Runtime Support](#runtime-support)
- [Configuration](#configuration)
- [Documentation](#documentation)
- [Plan ahead](#plan-ahead)
- [AWS Lambda Comparison](#aws-lambda-comparison)
- [Alternatives Comparison](#alternatives-comparison)
- [Contributing](#contributing)
- [License](#license)

## Highlights

- Runtime support today: Node.js 22 (Go runtime wiring is present but not yet executable)
- Docker-based isolation with per-function resource limits
- Cold start and warm pool execution strategies
- Automatic image build on function create/update
- Native EvtQ integration through queue triggers
- Direct invocation endpoint for local development
- Partial batch failure contract for event batches
- Invocation logs stored in PostgreSQL and queryable by API
- Container recycling and readiness health checks

## Quick Start

### 1) Start dependencies (EvtQ, PostgreSQL, FaaS)

```bash
docker compose up -d
```

Expected services:
- `evtq` on `http://localhost:8080`
- `faas` on `http://localhost:8081`
- `postgres` on `localhost:5432`

### 2) Create a simple Node.js function

```bash
mkdir -p ./examples/nodejs/hello-world
cat > ./examples/nodejs/hello-world/index.js <<'EOF'
exports.handler = async (event, context) => {
  const records = event.records || [];
  return {
    status: "ok",
    invocation_id: context.invocation_id,
    processed: records.length
  };
};
EOF
```

### 3) Register the function

```bash
curl -sS -X POST http://localhost:8081/functions \
  -H "Content-Type: application/json" \
  -d '{
    "name": "hello-fn",
    "runtime": "nodejs22",
    "handler": "index.handler",
    "code_path": "'"$(pwd)"'/examples/nodejs/hello-world",
    "container_strategy": "warm",
    "warm_pool_size": 2,
    "timeout_seconds": 30,
    "memory_mb": 256
  }' | jq
```

### 4) Invoke directly

```bash
curl -sS -X POST http://localhost:8081/functions/hello-fn/invoke \
  -H "Content-Type: application/json" \
  -d '{
    "records": [{
      "message_id": "550e8400-e29b-41d4-a716-446655440000",
      "body": "{\"order_id\":12345,\"status\":\"created\"}",
      "attributes": {"event_type": {"type":"String","value":"order.created"}},
      "approximate_receive_count": 1
    }],
    "source_queue": "orders",
    "invocation_id": "7c9e6679-7425-40de-944b-e07fc1f90ae7"
  }' | jq
```

### 5) Attach the function to an EvtQ queue trigger

```bash
# Create queue in EvtQ
curl -sS -X POST http://localhost:8080/queues \
  -H "Content-Type: application/json" \
  -d '{"name":"orders"}' | jq

# Create lambda-style trigger in EvtQ
curl -sS -X POST http://localhost:8080/queues/orders/triggers \
  -H "Content-Type: application/json" \
  -d '{
    "target_type":"lambda",
    "function_name":"hello-fn",
    "batch_size":5,
    "enabled":true
  }' | jq
```

### 6) Send a queue message and watch execution

```bash
curl -sS -X POST http://localhost:8080/queues/orders/messages \
  -H "Content-Type: application/json" \
  -d '{"body":"{\"order_id\":12345,\"status\":\"created\"}"}' | jq

sleep 1
curl -sS "http://localhost:8081/functions/hello-fn/logs" | jq
```

## Runtime Support

| Runtime | ID | Status | Handler Convention |
|---|---|---|---|
| Node.js 22 | `nodejs22` | Supported | `index.handler` |
| Go 1.22 | `go122` | Planned (validation exists, runtime build path pending) | `faas-go-sdk Start(handler)` |

See [Runtime Guide](./docs/runtimes.md) for runtime-specific details.

## Configuration

FaaS supports environment variables and `config.yaml`. Common settings:

| Variable | Default | Description |
|---|---|---|
| `LISTEN_ADDR` | `:8081` | HTTP bind address |
| `DATABASE_URL` | `postgres://localhost:5432/evtq_faas?sslmode=disable` | PostgreSQL connection |
| `MAX_CONCURRENT_CONTAINERS` | `20` | Global running container cap |
| `WARM_POOL_ACQUIRE_TIMEOUT_MS` | `15000` | Wait time when warm pool is empty |
| `WARM_CONTAINER_IDLE_TIMEOUT_MS` | `900000` | Idle warm container eviction window |
| `WARM_CONTAINER_RECYCLE_INVOCATIONS` | `50` | Recycle threshold by invocation count |
| `FAAS_DOCKER_NETWORK` | `faas-net` | Dedicated bridge network name |

Full configuration: [docs/configuration.md](./docs/configuration.md)

## Documentation

- [Architecture](./docs/architecture.md)
- [Concepts](./docs/concepts.md)
- [API Reference](./docs/api-reference.md)
- [Runtimes](./docs/runtimes.md)
- [Container Lifecycle](./docs/container-lifecycle.md)
- [Configuration](./docs/configuration.md)
- [Deployment](./docs/deployment.md)
- [Design Decisions](./docs/design-decisions.md)
- [EvtQ Integration](./docs/evtq-integration.md)
- [Examples](./docs/examples.md)
- [Testing](./docs/testing.md)
- [Lambda Compatibility](./docs/lambda-compatibility.md)

## Plan ahead

Possible next extensions for FaaS:

- Function versioning + aliases (blue/green rollouts and rollback).
- Provisioned concurrency and explicit per-function reservations.
- Runtime layers for shared dependencies and faster builds.
- Custom runtime contract for non-Node languages via user-provided Docker images.
- End-to-end tracing across EvtQ trigger to function invocation/logs.

## AWS Lambda Comparison

### Supported
- Event-driven invocation from queue source (EvtQ)
- Batch payload processing
- Partial batch failure response
- Runtime-level handler model for Node.js
- Per-function memory/timeout
- Warm container reuse and cold starts

### Not Yet Supported
- IAM roles and cloud identity integration
- Lambda layers
- Version/alias traffic shifting
- VPC-native cloud networking model
- AWS Lambda API parity for every control-plane action

See [Lambda Compatibility Matrix](./docs/lambda-compatibility.md).

## Alternatives Comparison

| Tool | Focus | Best Fit | Notes |
|---|---|---|---|
| FaaS | Local Lambda-like runtime + EvtQ | Local queue-driven Lambda development | Lightweight, explicit Docker model |
| LocalStack | Broad AWS emulation | Full AWS API simulation | Heavier footprint, wider service surface |
| OpenFaaS | Kubernetes-native serverless platform | Cluster-native function deployment | Different operational model from Lambda |
| Knative | Eventing + serving on Kubernetes | Cloud-native serverless abstractions | Strong for K8s ecosystems, more setup |

## Contributing

Contributions are welcome. Open an issue before major changes.

Basic workflow:

```bash
git clone https://github.com/your-org/faas.git
cd faas
go test ./...
```

For detailed development and testing practices, see [docs/testing.md](./docs/testing.md).

## License

MIT. See `LICENSE`.
