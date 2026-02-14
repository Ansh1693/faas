# FaaS API Reference

## Table of Contents

- [Conventions](#conventions)
- [Function Management](#function-management)
  - [Create Function](#create-function)
  - [List Functions](#list-functions)
  - [Get Function](#get-function)
  - [Update Function](#update-function)
  - [Delete Function](#delete-function)
- [Invocation](#invocation)
  - [Direct Invoke](#direct-invoke)
  - [Internal Invoke (EvtQ)](#internal-invoke-evtq)
- [Logs](#logs)
  - [Get Function Logs](#get-function-logs)
- [Metrics](#metrics)
  - [Current Status](#current-status)

## Conventions

- Base URL: `http://localhost:8081`
- Content type: `application/json`
- Error shape:

```json
{"error":"human readable message"}
```

---

## Function Management

### Create Function

`POST /functions`

Registers a function and builds its runtime image.

Request body:

| Field | Type | Required | Default | Constraints |
|---|---|---|---|---|
| `name` | string | yes | - | unique, non-empty |
| `runtime` | string | yes | - | `nodejs22` or `go122` |
| `handler` | string | yes | - | e.g. `index.handler` |
| `timeout_seconds` | int | no | `30` | `1..900` |
| `memory_mb` | int | no | `128` | `>=64` |
| `environment` | object | no | `{}` | string map |
| `code_path` | string | yes | - | absolute path |
| `container_strategy` | string | no | `warm` | `cold` or `warm` |
| `warm_pool_size` | int | no | `1` | `>=1` |

Example:

```bash
curl -sS -X POST http://localhost:8081/functions \
  -H "Content-Type: application/json" \
  -d '{
    "name":"process-order",
    "runtime":"nodejs22",
    "handler":"index.handler",
    "code_path":"/workspace/examples/nodejs/process-order",
    "timeout_seconds":45,
    "memory_mb":256,
    "container_strategy":"warm",
    "warm_pool_size":2
  }' | jq
```

Success (`201`): function object.

Common errors:
- `400` invalid runtime/path/memory/timeout/strategy
- `409` duplicate function name
- `500` image build failure

Current runtime note:
- `go122` passes input validation but build/invoke is not enabled in runtime manager yet; create/update returns runtime unsupported errors.

---

### List Functions

`GET /functions`

Returns all active functions.

Example:

```bash
curl -sS http://localhost:8081/functions | jq
```

Success (`200`):

```json
{"functions":[{"name":"process-order","runtime":"nodejs22"}]}
```

---

### Get Function

`GET /functions/{name}`

Returns one function by name.

Example:

```bash
curl -sS http://localhost:8081/functions/process-order | jq
```

Success (`200`): function object.  
Error (`404`): `{"error":"function not found"}`

---

### Update Function

`PUT /functions/{name}`

Updates mutable configuration and rebuilds image when needed.

Request body (all optional):

| Field | Type | Constraints |
|---|---|---|
| `runtime` | string | `nodejs22` or `go122` |
| `handler` | string | non-empty |
| `timeout_seconds` | int | `1..900` |
| `memory_mb` | int | `>=64` |
| `environment` | object | string map |
| `code_path` | string | absolute path |
| `container_strategy` | string | `cold` or `warm` |
| `warm_pool_size` | int | `>=1` |

Example:

```bash
curl -sS -X PUT http://localhost:8081/functions/process-order \
  -H "Content-Type: application/json" \
  -d '{"code_path":"/workspace/examples/nodejs/process-order","warm_pool_size":4}' | jq
```

Success (`200`): updated function object.  
Notes: warm containers for this function are recycled after update.

---

### Delete Function

`DELETE /functions/{name}`

Soft-deletes function metadata and evicts warm containers for the function.

Example:

```bash
curl -sS -X DELETE http://localhost:8081/functions/process-order -i
```

Success: `204 No Content`  
Errors: `404` if function does not exist.

---

## Invocation

### Direct Invoke

`POST /functions/{name}/invoke`

Synchronous invocation for local testing.

Request body:

| Field | Type | Required | Description |
|---|---|---|---|
| `records` | array | no | queue records |
| `source_queue` | string | no | source queue name |
| `invocation_id` | uuid | no | generated if omitted |

Example:

```bash
curl -sS -X POST http://localhost:8081/functions/process-order/invoke \
  -H "Content-Type: application/json" \
  -d '{
    "records":[
      {
        "message_id":"550e8400-e29b-41d4-a716-446655440000",
        "body":"{\"order_id\":12345}",
        "attributes":{"event_type":{"type":"String","value":"order.created"}},
        "approximate_receive_count":1
      }
    ],
    "source_queue":"orders"
  }' | jq
```

Success (`200`): raw function response JSON.  
Errors:
- `404` function missing
- `500` runtime/container/image errors
- `504` timeout behavior if caller timeout is exceeded

---

### Internal Invoke (EvtQ)

`POST /internal/invoke/{functionName}`

Used by EvtQ trigger dispatcher only.

Contract:
- same request format as direct invoke,
- response may include `batch_item_failures`.

Example response:

```json
{
  "batch_item_failures": [
    {"item_identifier":"550e8400-e29b-41d4-a716-446655440000"}
  ]
}
```

Note: this endpoint is intentionally internal; do not expose publicly without auth.

---

## Logs

### Get Function Logs

`GET /functions/{name}/logs`

Query params:

| Param | Type | Required | Description |
|---|---|---|---|
| `invocation_id` | uuid | no | filter one invocation |

Example:

```bash
curl -sS "http://localhost:8081/functions/process-order/logs?invocation_id=7c9e6679-7425-40de-944b-e07fc1f90ae7" | jq
```

Success (`200`):

```json
{
  "logs": [
    {
      "function_name":"process-order",
      "invocation_id":"7c9e6679-7425-40de-944b-e07fc1f90ae7",
      "stream":"combined",
      "log_line":"START inv=7c9e6679-7425-40de-944b-e07fc1f90ae7",
      "logged_at":"2026-02-15T01:47:03.021281Z"
    }
  ]
}
```

Notes:
- Log rows are newest-first by default.
- Retention cleanup is operator-managed.

---

## Metrics

### Current Status

Function and system metrics endpoints are planned but not currently exposed by the FaaS API.

Current observability surface:
- function logs: `GET /functions/{name}/logs`
- trigger metrics (on EvtQ side): `GET /queues/{name}/triggers/{id}/metrics`
