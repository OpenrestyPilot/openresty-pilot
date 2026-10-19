FROM node:24-alpine AS frontend-build
RUN corepack enable && corepack prepare pnpm@11.16.0 --activate
WORKDIR /src/orp-frontend
COPY orp-frontend/ ./
ARG VITE_ORP_SCREEN_URL
ENV VITE_ORP_SCREEN_URL=${VITE_ORP_SCREEN_URL}
ENV VITE_ARCHIVER=false
RUN CI=true pnpm install --frozen-lockfile \
 && pnpm --filter @vben/web-antd build

FROM golang:1.26-alpine AS app-build
WORKDIR /src
COPY orp-backend/ ./
COPY --from=frontend-build /src/orp-frontend/apps/web-antd/dist ./internal/webui/static/dist
ARG BUILD_VERSION=dev
ARG BUILD_COMMIT=unknown
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X main.version=${BUILD_VERSION} -X main.commit=${BUILD_COMMIT}" \
    -o /out/orp ./cmd/control-plane

FROM scratch AS binary
COPY --from=app-build /out/orp /orp

FROM alpine:3.22 AS runtime
RUN apk add --no-cache ca-certificates tzdata docker-cli \
 && mkdir -p /workspace/runtime
COPY --from=app-build /out/orp /usr/local/bin/orp
WORKDIR /workspace
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/orp"]
CMD ["run"]
