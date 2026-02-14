# Testing

## Table of Contents

- [Run Test Suite](#run-test-suite)
- [Test Categories](#test-categories)
- [Component Scenarios](#component-scenarios)
- [Testing Without Docker](#testing-without-docker)
- [Testing With Docker](#testing-with-docker)
- [End-to-End Flow Test](#end-to-end-flow-test)
- [Performance Benchmarking](#performance-benchmarking)

## Run Test Suite

```bash
go test ./...
```

With race detection:

```bash
go test -race ./...
```

## Test Categories

- **Unit tests**
  - validation logic,
  - request decoding/encoding,
  - store query behavior (mocked DB layer).
- **Integration tests**
  - PostgreSQL-backed store operations,
  - function create/update/delete paths.
- **Container runtime tests**
  - image build,
  - container health and invoke,
  - warm pool behavior.
- **End-to-end tests**
  - EvtQ queue -> trigger -> FaaS -> container -> response.

## Component Scenarios

API layer:
- invalid payloads return `400`
- unknown functions return `404`

Function service:
- defaults applied correctly
- update triggers rebuild + warm refresh

Container manager:
- cold path create/start/invoke/remove
- warm path acquire/invoke/return
- recycle on threshold

Store:
- function CRUD
- log insert/query by function and invocation

## Testing Without Docker

Use mock container manager interface in service tests:
- mock `BuildImage`
- mock `Invoke`/`InvokeRaw`
- assert service behavior independent of Docker daemon.

This is required for fast CI and deterministic unit checks.

## Testing With Docker

Prerequisites:
- Docker daemon running
- PostgreSQL running

Run targeted runtime tests:

```bash
go test ./internal/lambda -v
```

Recommended in isolated environment to avoid cross-test container noise.

## End-to-End Flow Test

1. Start stack (`docker compose up -d`).
2. Create function in FaaS.
3. Create queue in EvtQ.
4. Create lambda trigger in EvtQ.
5. Send queue message.
6. Assert:
   - function invoked,
   - logs persisted,
   - trigger metrics updated,
   - message deleted or retried as expected.

Example check:

```bash
curl -sS "http://localhost:8081/functions/process-order/logs" | jq
curl -sS "http://localhost:8080/queues/orders/triggers/<id>/metrics" | jq
```

## Performance Benchmarking

Measure:
- cold start latency (`create->health->invoke`)
- warm invoke latency (`acquire->invoke->return`)
- throughput under mixed group IDs for FIFO

Suggested approach:

```bash
# warm baseline
for i in $(seq 1 100); do
  curl -sS -X POST http://localhost:8081/functions/process-order/invoke \
    -H "Content-Type: application/json" \
    -d '{"records":[]}' >/dev/null
done
```

Record:
- p50/p95/p99 latency
- running container count
- pool saturation and acquire wait time.
