# Deployment

## Table of Contents

- [Docker Compose (Development)](#docker-compose-development)
- [Docker Socket Mount](#docker-socket-mount)
- [Docker Standalone](#docker-standalone)
- [Build and Run Binary](#build-and-run-binary)
- [Production Considerations](#production-considerations)
- [Health Checks](#health-checks)
- [Monitoring Recommendations](#monitoring-recommendations)

## Docker Compose (Development)

Use this `docker-compose.yml` for local development:

```yaml
version: "3.9"

services:
  postgres:
    image: postgres:16-alpine
    container_name: evtq-postgres
    environment:
      POSTGRES_USER: admin
      POSTGRES_PASSWORD: 12345678
      POSTGRES_DB: postgres
    ports:
      - "5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data

  evtq:
    image: ghcr.io/your-org/evtq:latest
    container_name: evtq
    environment:
      DATABASE_URL: postgres://admin:12345678@postgres:5432/evtq?sslmode=disable
      LISTEN_ADDR: :8080
      FAAS_BASE_URL: http://faas:8081
    depends_on:
      - postgres
    ports:
      - "8080:8080"

  faas:
    image: ghcr.io/your-org/faas:latest
    container_name: faas
    environment:
      DATABASE_URL: postgres://admin:12345678@postgres:5432/evtq_faas?sslmode=disable
      LISTEN_ADDR: :8081
      DOCKER_HOST: unix:///var/run/docker.sock
      MAX_CONCURRENT_CONTAINERS: 20
      FAAS_DOCKER_NETWORK: faas-net
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - ./examples:/workspace/examples
    depends_on:
      - postgres
    ports:
      - "8081:8081"

volumes:
  pgdata:
```

## Docker Socket Mount

FaaS must create and manage containers, so it needs Docker daemon access.

Required mount:

```yaml
volumes:
  - /var/run/docker.sock:/var/run/docker.sock
```

Security implications:
- Socket access is effectively root-equivalent on the host.
- Do not expose FaaS publicly without auth/network controls.
- Restrict deployment to trusted environments or isolate on dedicated hosts.

## Docker Standalone

```bash
docker run --rm -p 8081:8081 \
  -e DATABASE_URL="postgres://admin:12345678@host.docker.internal:5432/evtq_faas?sslmode=disable" \
  -e LISTEN_ADDR=":8081" \
  -e DOCKER_HOST="unix:///var/run/docker.sock" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  ghcr.io/your-org/faas:latest
```

## Build and Run Binary

```bash
go build -o ./bin/faas ./cmd/server
DATABASE_URL="postgres://localhost:5432/evtq_faas?sslmode=disable" \
LISTEN_ADDR=":8081" \
./bin/faas
```

## Production Considerations

- Protect Docker socket access aggressively.
- Set global and per-function resource limits.
- Enable image and container cleanup policies.
- Monitor disk usage (`docker image ls`, `docker system df`).
- Apply DB retention policy for function logs.
- Use reverse proxy with TLS and authentication in front of FaaS API.

## Health Checks

Service health:

```bash
curl -sS http://localhost:8081/health
```

Container readiness is checked by FaaS before each container serves invocations.

## Monitoring Recommendations

- Track:
  - invocation success/failure rate,
  - cold start latency,
  - warm pool saturation,
  - running container count,
  - DB log growth.
- Export API/service logs and function logs to your monitoring stack.
- Alert on repeated build failures and Docker daemon disconnects.
