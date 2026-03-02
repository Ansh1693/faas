# Examples

## Table of Contents

- [Hello World (Node.js)](#hello-world-nodejs)
- [Hello World (Go)](#hello-world-go)
- [EvtQ Message Processor](#evtq-message-processor)
- [Batch Processor with Partial Failures](#batch-processor-with-partial-failures)

## Hello World (Node.js)

### Code

```javascript
exports.handler = async (event, context) => {
  return {
    message: "hello world",
    invocation_id: context.invocation_id,
    records: (event.records || []).length
  };
};
```

### Register

```bash
curl -sS -X POST http://localhost:8081/functions \
  -H "Content-Type: application/json" \
  -d '{
    "name":"hello-node",
    "runtime":"nodejs22",
    "handler":"index.handler",
    "code_path":"'"$(pwd)"'/examples/nodejs/hello-world",
    "container_strategy":"warm",
    "warm_pool_size":1
  }' | jq
```

### Invoke

```bash
curl -sS -X POST http://localhost:8081/functions/hello-node/invoke \
  -H "Content-Type: application/json" \
  -d '{"records":[],"source_queue":"manual"}' | jq
```

Expected:

```json
{"message":"hello world","records":0}
```

## Hello World (Go, Planned Runtime Path)

### Code

```go
package main

import sdk "github.com/yourname/faas/sdk/go"

func handler(event map[string]any, ctx sdk.Context) (any, error) {
	return map[string]any{
		"message": "hello world",
		"invocation_id": ctx.InvocationID,
	}, nil
}

func main() { sdk.Start(handler) }
```

### Register and invoke (expected shape)

```bash
curl -sS -X POST http://localhost:8081/functions \
  -H "Content-Type: application/json" \
  -d '{
    "name":"hello-go",
    "runtime":"go122",
    "handler":"main.handler",
    "code_path":"'"$(pwd)"'/examples/go/hello-world",
    "container_strategy":"cold"
  }' | jq

curl -sS -X POST http://localhost:8081/functions/hello-go/invoke \
  -H "Content-Type: application/json" \
  -d '{"records":[]}' | jq
```

Note: in the current build, `go122` runtime creation/invocation is not yet enabled in container manager.

## EvtQ Message Processor

### Code (`examples/nodejs/sqs-processor/index.js`)

```javascript
exports.handler = async (event) => {
  for (const rec of event.records || []) {
    const payload = JSON.parse(rec.body);
    console.log(`process order=${payload.order_id} status=${payload.status}`);
  }
  return { status: "ok", processed: (event.records || []).length };
};
```

### Wiring

```bash
curl -sS -X POST http://localhost:8080/queues \
  -H "Content-Type: application/json" \
  -d '{"name":"orders"}' | jq

curl -sS -X POST http://localhost:8081/functions \
  -H "Content-Type: application/json" \
  -d '{
    "name":"order-processor",
    "runtime":"nodejs22",
    "handler":"index.handler",
    "code_path":"'"$(pwd)"'/examples/nodejs/sqs-processor"
  }' | jq

curl -sS -X POST http://localhost:8080/queues/orders/triggers \
  -H "Content-Type: application/json" \
  -d '{"target_type":"lambda","function_name":"order-processor","batch_size":5,"enabled":true}' | jq
```

### Send event

```bash
curl -sS -X POST http://localhost:8080/queues/orders/messages \
  -H "Content-Type: application/json" \
  -d '{"body":"{\"order_id\":42,\"status\":\"created\"}"}' | jq
```

## Batch Processor with Partial Failures

### Code

```javascript
exports.handler = async (event) => {
  const failures = [];
  for (const rec of event.records || []) {
    try {
      const body = JSON.parse(rec.body);
      if (!body.order_id) throw new Error("invalid payload");
      console.log(`ok message_id=${rec.message_id}`);
    } catch (e) {
      failures.push({ item_identifier: rec.message_id });
    }
  }
  return failures.length ? { batch_item_failures: failures } : { status: "ok" };
};
```

### Test batch

```bash
curl -sS -X POST http://localhost:8080/queues/orders/messages \
  -H "Content-Type: application/json" \
  -d '{"body":"{\"order_id\":1001}"}' | jq

curl -sS -X POST http://localhost:8080/queues/orders/messages \
  -H "Content-Type: application/json" \
  -d '{"body":"{\"bad\":true}"}' | jq
```

Expected:
- valid message deleted,
- invalid message retried after visibility timeout.
