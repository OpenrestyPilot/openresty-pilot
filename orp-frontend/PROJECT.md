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

## Big screen gallery

The single `大屏展示` route (`/screens`) is a responsive card gallery. Its default cards include world maps and all 23 provinces, each with 2D and 3D standalone traffic screens that open in new tabs; every screen has a light/dark toggle and remembers the choice in the browser; the world group also includes a rotating cyan dotted globe (`scene=globe`) and a light world-dot map (`scene=globe-light`) with animated GeoIP routes. Real source and center coordinates come from the public dashboard SSE; the ambient scan is decorative and never creates synthetic traffic when GeoIP data is missing. Operators can search province, city, and district selectors by name or administrative code to show the selected region's 2D and 3D cards; the hierarchy is loaded from `/maps/admin-index.json`. Municipalities, autonomous regions, and SARs remain accessible through the selectors. The standalone map can drill into child boundaries and its breadcrumb returns to parent administrative areas. Map geometry is served by the standalone screen using local files. `VITE_ORP_SCREEN_URL` sets the screen base URL; otherwise the console uses the same-origin `/screen/` path.

The control-plane dashboard opens its authenticated SSE stream immediately while center filter options load in parallel. A pending frame marks the stream as connected; a partial first snapshot shows access statistics before node/container, upstream, and GeoIP details arrive in the complete snapshot.

The root `README.md` introduces ORP Frontend and credits the upstream Vben framework. This file contains the ORP application setup.

## Configuration candidate console

The `配置比对与下发` drawer reports three separate measures: center-scoped audit events since the selected node's last successful publish, final resource-level net changes compared with that node's publish snapshot, and added/deleted lines in the rendered NGINX text. Audit operations, resource records, and text lines are different units; fields that do not affect rendered NGINX output can appear in resource changes without producing text lines. Before the first successful publish, the text diff uses the node's initial configuration and resource-level net changes are unavailable.

The `配置候选与校验` route (`/config-candidates`) is a local route-menu entry requiring `orp:read`, backed by the OpenAPI-described `/orp/candidates` and `/orp/validation-centers/{centerId}/policy` APIs. Operators with center-scoped `orp:config:write` permission can freeze a candidate for selected center nodes, set the center validation concurrency cap, inspect attempts/jobs and the frozen semantic/text Diff, and archive/restore, retry, continue, or stop where the server state permits. The policy editor is read-only without `orp:config:write`. The create form offers an opt-in automatic publish intent, disabled by default and unavailable without `orp:publish`; only the latest completed attempt with every frozen target passed can start that batch. Failed, paused, or unknown validation never auto-publishes. A publish-permitted operator can explicitly confirm a batch for an eligible passed candidate; the API rechecks authorization and frozen state. Read-only users can inspect candidate/job state; the detail response may omit Diff content when the account lacks write permission for that center. API authorization remains authoritative and rejected actions are surfaced by the shared request interceptor.

Candidate validation is non-activating by itself. Without the explicit automatic intent, a passed candidate waits for a publish-permitted operator to confirm a batch. With the intent, publication starts only after all frozen validation targets pass. The console displays server-returned intent/status/error/batch IDs and blockers; it does not synthesize validation progress. A batch link opens the publication progress drawer, which also collects observed release UUIDs only for server-marked `requiresReconciliation` items and submits the operator-confirmed continue request. It never guesses which failed or aborted items are eligible to resume.

If a linked batch is `recovery_required`, the progress drawer asks the operator to record the current active release UUID for only those items the batch API marks `requiresReconciliation`. The operator can submit the observations to the reconciliation endpoint; the backend then independently probes each node and decides whether an item is already applied, safely resumable, or rejected. Other aborted or failed items are not inferred as recovery targets, and unknown started activations are not replayed.

The archived-candidate filter follows the OpenAPI meaning of `archived=true` (archived records only), and the console also filters returned rows by their `archived` flag. The backend list query applies the archived/active predicate before pagination so older archived records remain reachable.
