# FaaS + EvtQ Integration

## Table of Contents

- [Architecture Flow](#architecture-flow)
- [Setup](#setup)
- [How EvtQ Triggers Invoke FaaS](#how-evtq-triggers-invoke-faas)
- [Batch Processing](#batch-processing)
- [Partial Batch Failure](#partial-batch-failure)
- [FIFO Behavior](#fifo-behavior)
- [Error Handling](#error-handling)
- [Tuning Guide](#tuning-guide)
- [Worked Example: orders.fifo](#worked-example-ordersfifo)

## Architecture Flow

```mermaid
flowchart LR
    PROD[Producer] --> Q[(EvtQ Queue)]
    Q --> TM[EvtQ Trigger Manager]
    TM --> LD[Lambda Dispatcher]
    LD --> FI[FaaS /internal/invoke/{function}]
    FI --> CM[Container Manager]
    CM --> FC[Function Container]
    FC --> CM
    CM --> FI
    FI --> LD
    LD --> TM
    TM --> DEL[Delete successful messages]
    TM --> RETRY[Retry failed after visibility timeout]
```

## Setup

1. Create queue in EvtQ.
2. Register function in FaaS.
3. Create trigger in EvtQ with `target_type=lambda` and `function_name`.

Example:

```bash
curl -sS -X POST http://localhost:8080/queues \
  -H "Content-Type: application/json" \
  -d '{"name":"orders.fifo","queue_type":"FIFO","content_based_dedup":true}' | jq

curl -sS -X POST http://localhost:8081/functions \
  -H "Content-Type: application/json" \
  -d '{
    "name":"process-order",
    "runtime":"nodejs22",
    "handler":"index.handler",
    "code_path":"'"$(pwd)"'/examples/nodejs/sqs-processor",
    "container_strategy":"warm",
    "warm_pool_size":2
  }' | jq

curl -sS -X POST http://localhost:8080/queues/orders.fifo/triggers \
  -H "Content-Type: application/json" \
  -d '{
    "target_type":"lambda",
    "function_name":"process-order",
    "batch_size":5,
    "batch_window_seconds":2,
    "enabled":true
  }' | jq
```

## How EvtQ Triggers Invoke FaaS

- EvtQ pollers read visible queue messages.
- Dispatcher posts payload to:
  - `POST /internal/invoke/{functionName}`
- FaaS executes function and returns success/partial failures.
- EvtQ deletes only successful records.

See [EvtQ Concepts](../evtq/docs/concepts.md).

## Batch Processing

- `batch_size`: upper bound records per invoke.
- `batch_window_seconds`: wait window to accumulate a fuller batch.
- For FIFO queues, ordering constraints still apply by message group.

## Partial Batch Failure

Function returns:

```json
{
  "batch_item_failures": [
    {"item_identifier":"550e8400-e29b-41d4-a716-446655440000"}
  ]
}
```

EvtQ behavior:
- successful records deleted,
- failed records become visible again after visibility timeout.

## FIFO Behavior

Guarantees:
- in-order processing per `message_group_id`,
- one in-flight record per group,
- parallel execution across different groups.

This behavior depends on EvtQ FIFO receive semantics plus trigger concurrency settings.

## Error Handling

- Function timeout:
  - FaaS returns failure,
  - EvtQ keeps message for retry after visibility timeout.
- Function crash/container crash:
  - invocation fails, container recycled.
- Batch partial failures:
  - only failed records retried.

## Tuning Guide

- Set `visibility_timeout` > function timeout + network overhead.
- Increase `batch_size` for throughput, reduce for low latency.
- Increase warm pool size for bursty workloads.
- For FIFO:
  - higher parallelism needs more distinct message groups.

## Worked Example: orders.fifo

### Function code (`examples/nodejs/sqs-processor/index.js`)

```javascript
exports.handler = async (event) => {
  const failures = [];
  for (const rec of event.records || []) {
    try {
      const order = JSON.parse(rec.body);
      if (!order.order_id) throw new Error("missing order_id");
      console.log(`processed order_id=${order.order_id}`);
    } catch (err) {
      console.error(`failed id=${rec.message_id} err=${err.message}`);
      failures.push({ item_identifier: rec.message_id });
    }
  }
  return failures.length ? { batch_item_failures: failures } : { status: "ok" };
};
```

### Send test messages

```bash
curl -sS -X POST http://localhost:8080/queues/orders.fifo/messages \
  -H "Content-Type: application/json" \
  -d '{"message_group_id":"orders","body":"{\"order_id\":1001,\"status\":\"created\"}"}' | jq

curl -sS -X POST http://localhost:8080/queues/orders.fifo/messages \
  -H "Content-Type: application/json" \
  -d '{"message_group_id":"orders","body":"{\"status\":\"invalid\"}"}' | jq
```

### Observe logs and metrics

```bash
curl -sS "http://localhost:8081/functions/process-order/logs" | jq
curl -sS "http://localhost:8080/queues/orders.fifo/triggers/<trigger-id>/metrics" | jq
```
