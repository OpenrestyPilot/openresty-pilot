# ORP Backend — ORP Go Control Plane

Project identifier: `orp-backend`.

This directory is an independent Go project and can be built or deployed without the frontend workspace.

## Requirements

- Go 1.26.1
- MySQL 8.4 (schema is initialized/upgraded from `internal/store/migrations/`)
- Redis and Kafka are optional for core API availability; configure them when using dashboard snapshot queueing and Kafka log ingestion.

## Run locally

1. Copy `.env.example` to `.env`.
2. Set a strong `OPENRESTY_ADMIN_PASSWORD` and generate `OPENRESTY_DATA_KEY` with `openssl rand -hex 32`.
3. Configure a reachable MySQL instance, then load `.env` and run `go run ./cmd/control-plane`.

The API listens on `OPENRESTY_HTTP_ADDR` (default `:8081`) and exposes `/healthz`. To build the combined frontend/backend executable or its Docker image, use `build.sh` or `deploy/Dockerfile` from the monorepo root. Local MySQL/Redis/Kafka, OpenResty nodes, and Filebeat are started from the separate `orp-quickstart` repository.

To enable external Node Agent heartbeats and fixed reload tasks, configure all four optional settings: `OPENRESTY_AGENT_HTTPS_ADDR`, `OPENRESTY_AGENT_TLS_CERT_FILE`, `OPENRESTY_AGENT_TLS_KEY_FILE`, and `OPENRESTY_AGENT_CLIENT_CA_FILE`. This starts a dedicated HTTPS listener requiring client certificates; the browser API remains on its normal listener. Register each leaf-certificate SHA-256 fingerprint through the super-admin node API before starting the Agent. See `../orp-node-agent/docs/protocol.md`.

## Checks

- `go test ./...`
- `go vet ./...`
- `go run ./cmd/openapi-check`

The frontend maintains the client OpenAPI document and its own consistency check in the separate frontend project.

## Node operations

The current publish adapter supports the fixed local Docker nodes supplied by `orp-quickstart` only. Agent heartbeat and standalone fixed reload task APIs are implemented, but certificate issuance/rotation, artifact transfer, target validation, and publish-batch integration remain future work.
