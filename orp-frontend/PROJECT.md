# ORP Frontend — ORP Web Console

This independent project contains the ORP web console in `apps/web-antd/` and the shared Vben workspace packages it uses. Its project identifier is `orp-frontend`.

## Requirements

- Node.js 22.18+ or 24+
- pnpm 11.16.0
- The Go control-plane API, or another compatible API deployment

## Install and run

```bash
pnpm install --frozen-lockfile
cp apps/web-antd/.env.example apps/web-antd/.env.development.local
pnpm --filter @vben/web-antd dev
```

The development server listens on port `5666`. Its `/api` requests are proxied to `VITE_API_PROXY_TARGET`, which defaults to `http://127.0.0.1:8081`. Set that variable in `apps/web-antd/.env.development.local` to point at a separately running backend.

For a production build:

```bash
pnpm --filter @vben/web-antd typecheck
pnpm --filter @vben/web-antd build
```

In production, configure the web server or ingress to route `/api` to the backend service. Vite's development proxy is not part of the production bundle.

The root `README.md` introduces ORP Frontend and credits the upstream Vben framework. This file contains the ORP application setup.
