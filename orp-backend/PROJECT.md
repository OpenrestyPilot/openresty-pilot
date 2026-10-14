# ORP Backend — ORP Go Control Plane

Project identifier: `orp-backend`.

This directory is an independent Go project and can be built or deployed without the frontend workspace.

## Requirements

- Go 1.26.1
- MySQL 8.4 (schema is initialized/upgraded from `internal/store/migrations/`)
- Redis and Kafka are optional for core API availability; configure them when using dashboard snapshot queueing and Kafka log ingestion.

## Run locally

1. Copy `.env.example` to `.env`.
2. Set a strong `ORP_ADMIN_PASSWORD` and generate `ORP_DATA_KEY` with `openssl rand -hex 32`.
3. Configure a reachable MySQL instance, then load `.env` and run `go run ./cmd/control-plane`.

The API listens on `ORP_HTTP_ADDR` (default `:8081`) and exposes `/healthz`. To build the combined frontend/backend executable or its Docker image, use `build.sh` or `deploy/Dockerfile` from the monorepo root. Local MySQL/Redis/Kafka, OpenResty nodes, and Filebeat are started from the separate `orp-quickstart` repository.

To enable external Node Agent heartbeats and fixed reload tasks, configure the Agent HTTPS address, TLS certificate/key paths, client CA path, and public URL in **系统配置** (or set their corresponding `ORP_AGENT_*` environment variables to override them). This starts a dedicated HTTPS listener; nodes can authenticate using registered mTLS certificates or rotated per-node Bearer tokens. The node page's «快速接入» action issues a token and provides binary or Docker startup commands. See [Agent quick enrollment](docs/agent-quick-enrollment.md) and `../orp-node-agent/docs/protocol.md`.

Runtime options (HTTP/Agent listeners, Redis, Kafka, CAPTCHA and GeoIP paths) are seeded into **系统配置** at startup. Their priority is explicit process environment/`.env` value > enabled database setting > built-in default. Database settings are snapshotted during startup, so changes take effect after restarting the control plane. Keep MySQL connection credentials, `ORP_ADMIN_PASSWORD`, and `ORP_DATA_KEY` in the environment: they are needed before or while connecting to the settings database and must not be stored there. `.env.example` shows optional runtime overrides commented out.

## Checks

- `go test ./...`
- `go vet ./...`
- `go run ./cmd/openapi-check`

The frontend maintains the client OpenAPI document and its own consistency check in the separate frontend project.

## Node operations

The current publish adapter supports the fixed local Docker nodes supplied by `orp-quickstart` only. Agent heartbeat and standalone fixed reload task APIs are implemented, but certificate issuance/rotation, artifact transfer, target validation, and publish-batch integration remain future work.
