# FaaS Runtimes

## Table of Contents

- [Overview](#overview)
- [Node.js 22](#nodejs-22)
- [Go 1.22](#go-122)
- [Runtime Comparison](#runtime-comparison)

## Overview

FaaS currently executes one managed runtime in production path:
- `nodejs22`
- `go122` (configuration accepted, runtime execution path planned)

Both expose the same runtime interface contract (`/health`, `/invoke`) and receive the same event payload shape.

See [Concepts](./concepts.md) for the shared invocation model.

## Node.js 22

### Handler format

```javascript
exports.handler = async (event, context) => {
  return { status: "ok" };
};
```

`handler` config maps module export:
- `index.handler` => `require("./index").handler`
- `src/order.process` => `require("./src/order").process`

### Context fields

| Field | Type | Description |
|---|---|---|
| `invocation_id` | string | stable ID for one invocation |
| `function_name` | string | function logical name |
| `timeout_ms` | number | invocation timeout budget |
| `memory_mb` | number | configured memory limit |

### Dependencies

- If `package.json` exists, dependencies are installed during image build.
- Prefer lockfiles for deterministic local builds.

### Environment variables inside function container

| Variable | Description |
|---|---|
| `HANDLER` | handler symbol |
| `FUNCTION_NAME` | configured function name |
| `FUNCTION_TIMEOUT_MS` | timeout in ms |
| custom envs from function config | user values |

### Example: simple handler

```javascript
exports.handler = async () => ({ message: "hello from nodejs22" });
```

### Example: handler with dependency

```javascript
const { v4: uuidv4 } = require("uuid");

exports.handler = async (event) => ({
  id: uuidv4(),
  count: (event.records || []).length
});
```

### Example: EvtQ batch processor

```javascript
exports.handler = async (event) => {
  const failures = [];
  for (const rec of event.records || []) {
    try {
      const order = JSON.parse(rec.body);
      if (!order.order_id) throw new Error("missing order_id");
      // business logic...
    } catch (_) {
      failures.push({ item_identifier: rec.message_id });
    }
  }
  if (failures.length > 0) return { batch_item_failures: failures };
  return { status: "ok" };
};
```

### Debugging tips

- `console.log` and `console.error` are captured into `function_logs`.
- Include `invocation_id` in logs for correlation.
- Use direct invoke for tight feedback loops.

## Go 1.22 (Planned Runtime Path)

### SDK model

Go functions are expected to use the runtime SDK boot helper:

```go
package main

import sdk "github.com/yourname/faas/sdk/go"

func main() {
    sdk.Start(handler)
}
```

### Handler signature

```go
func handler(event map[string]any, ctx sdk.Context) (any, error)
```

### Build modes (planned)

- **Source-based**: FaaS compiles from `code_path`.
- **Prebuilt binary**: packaged into image by runtime Dockerfile.

Current implementation note:
- `go122` passes API validation but the runtime manager currently builds/invokes only Node.js images.

### Example: simple handler

```go
package main

import sdk "github.com/yourname/faas/sdk/go"

func handler(event map[string]any, ctx sdk.Context) (any, error) {
    return map[string]any{"message": "hello from go122"}, nil
}

func main() { sdk.Start(handler) }
```

### Example: EvtQ processor

```go
package main

import (
    "encoding/json"
    sdk "github.com/yourname/faas/sdk/go"
)

type Record struct {
    MessageID string `json:"message_id"`
    Body      string `json:"body"`
}

func handler(event map[string]any, ctx sdk.Context) (any, error) {
    raw, _ := json.Marshal(event["records"])
    var records []Record
    _ = json.Unmarshal(raw, &records)
    return map[string]any{"processed": len(records), "invocation_id": ctx.InvocationID}, nil
}

func main() { sdk.Start(handler) }
```

### Debugging tips

- Use structured logs with invocation IDs.
- Keep startup fast; heavy init increases cold start.
- Verify compiled binary target matches container architecture.

## Runtime Comparison

| Dimension | Node.js 22 | Go 1.22 |
|---|---|---|
| Typical cold start | medium | low |
| Typical image size | medium | low-to-medium |
| Memory overhead | medium | low |
| Startup model | interpreted + bootstrap | compiled binary |
| Best for | fast iteration, JS ecosystem | planned low-latency static binaries |
