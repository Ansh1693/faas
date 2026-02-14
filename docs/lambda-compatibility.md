# AWS Lambda Compatibility

## Table of Contents

- [Compatibility Matrix](#compatibility-matrix)
- [Behavioral Differences](#behavioral-differences)
- [API Mapping](#api-mapping)
- [Handler Compatibility](#handler-compatibility)
- [Migration Guide](#migration-guide)
- [Known Limitations](#known-limitations)

## Compatibility Matrix

| Feature | AWS Lambda | FaaS | Status |
|---|---|---|---|
| Function create/update/delete | yes | yes | Supported |
| Direct sync invoke | yes | yes | Supported |
| Event source queue trigger | SQS | EvtQ | Supported (Different source) |
| Batch event payload | yes | yes | Supported |
| Partial batch failure | yes | yes | Supported |
| Node.js runtime | yes | yes (`nodejs22`) | Supported |
| Go runtime | yes | `go122` config accepted, execution path pending | Partial |
| Cold start | yes | yes | Supported |
| Warm reuse | managed internally | warm pool configurable | Supported (Different controls) |
| Function versions | yes | no | Not supported |
| Aliases and weighted traffic | yes | no | Not supported |
| IAM execution role | yes | no | Not supported |
| Layers | yes | no | Not supported |
| CloudWatch Logs | yes | postgres logs API | Different |
| VPC integration | yes | local Docker network | Different |

## Behavioral Differences

- FaaS uses Docker container isolation directly.
- FaaS log sink is PostgreSQL, not CloudWatch.
- Event source is EvtQ, not SQS API endpoint.
- Warm reuse strategy is explicit per function.
- No Lambda control-plane auth/roles by default.

## API Mapping

| Lambda API | FaaS API |
|---|---|
| `CreateFunction` | `POST /functions` |
| `GetFunction` | `GET /functions/{name}` |
| `UpdateFunctionConfiguration` | `PUT /functions/{name}` |
| `DeleteFunction` | `DELETE /functions/{name}` |
| `Invoke` | `POST /functions/{name}/invoke` |
| `ListFunctions` | `GET /functions` |
| `GetFunctionLogs` (CloudWatch) | `GET /functions/{name}/logs` |

## Handler Compatibility

Node.js handlers are mostly portable if:
- they depend only on event/context fields present in both systems,
- they avoid AWS-specific SDK calls without local mocks.

Go handlers are portable with adaptation:
- Lambda Go runtime API differs from FaaS SDK boot contract.
- business logic can usually be shared; bootstrap glue changes.

## Migration Guide

1. Copy function code to local `code_path`.
2. Keep handler signature equivalent.
3. Replace AWS event source with EvtQ queue + trigger.
4. Register function with same timeout/memory targets.
5. Validate batch failure behavior using local test messages.

Example:

```bash
curl -sS -X POST http://localhost:8081/functions \
  -H "Content-Type: application/json" \
  -d '{"name":"my-fn","runtime":"nodejs22","handler":"index.handler","code_path":"/workspace/my-fn"}'
```

## Known Limitations

- No versions or aliases.
- No IAM role emulation.
- No Lambda layers.
- No cloud-native VPC route semantics.
- Metrics API surface may differ by deployment build.
