#!/usr/bin/env bash
set -Eeuo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
frontend_dir="$repo_root/orp-frontend"
frontend_app="$frontend_dir/apps/web-antd"
backend_dir="$repo_root/orp-backend"
frontend_dist="$frontend_app/dist"
embedded_dist="$backend_dir/internal/webui/static/dist"
output_dir="$repo_root/dist"
frontend_mode_override="$frontend_app/.env.production.local"

for command_name in go node pnpm python3; do
  if ! command -v "$command_name" >/dev/null 2>&1; then
    printf '缺少构建工具：%s\n' "$command_name" >&2
    exit 1
  fi
done

node_version=$(node -p 'process.versions.node')
if ! node -e 'const [major, minor] = process.versions.node.split(".").map(Number); process.exit((major === 22 && minor >= 18) || (major === 24 && minor >= 12) ? 0 : 1)'; then
  printf '前端要求 Node.js 22.18+ 或 24.12+，当前版本为 %s。\n' "$node_version" >&2
  exit 1
fi
pnpm_version=$(pnpm --version)
if [[ "$pnpm_version" != "11.16.0" ]]; then
  printf '项目要求 pnpm 11.16.0，当前版本为 %s。\n' "$pnpm_version" >&2
  exit 1
fi

if [[ ! -x "$frontend_dir/node_modules/.bin/vite" ]]; then
  printf '安装前端依赖……\n'
  (cd "$frontend_dir" && pnpm install --frozen-lockfile)
fi

temporary_dir=$(mktemp -d "${TMPDIR:-/tmp}/orp-build.XXXXXX")
saved_mode_override="$temporary_dir/env.production.local"
had_mode_override=false

# A fresh checkout has only the tracked example; this enables same-origin API
# requests when building the production frontend without local env files.
if [[ ! -f "$frontend_app/.env.production" ]]; then
  cp "$frontend_app/.env.example" "$frontend_app/.env.production"
fi
if [[ -f "$frontend_mode_override" ]]; then
  cp "$frontend_mode_override" "$saved_mode_override"
  had_mode_override=true
fi
printf '\nVITE_ARCHIVER=false\n' >> "$frontend_mode_override"

cleanup() {
  python3 - "$embedded_dist" "$frontend_mode_override" "$saved_mode_override" "$temporary_dir" "$had_mode_override" <<'PY'
import os
import shutil
import sys
embedded_dist, mode_override, saved_override, temporary_dir, had_override = sys.argv[1:]
shutil.rmtree(embedded_dist, ignore_errors=True)
if had_override == "true":
    shutil.copy2(saved_override, mode_override)
elif os.path.exists(mode_override):
    os.unlink(mode_override)
shutil.rmtree(temporary_dir, ignore_errors=True)
PY
}
trap cleanup EXIT

printf '构建 Vue 管理界面……\n'
(cd "$frontend_dir" && pnpm --filter @vben/web-antd build)
if [[ ! -s "$frontend_dist/index.html" ]]; then
  printf '前端构建未生成 index.html：%s\n' "$frontend_dist" >&2
  exit 1
fi

mkdir -p "$embedded_dist"
cp -R "$frontend_dist"/. "$embedded_dist"/

mkdir -p "$output_dir"
build_version=${BUILD_VERSION:-$(git -C "$repo_root" describe --tags --always 2>/dev/null || printf 'dev')}
build_commit=${BUILD_COMMIT:-$(git -C "$repo_root" rev-parse --short HEAD 2>/dev/null || printf 'unknown')}
printf '编译 Go 服务并嵌入前端……\n'
(cd "$backend_dir" && CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=$build_version -X main.commit=$build_commit" \
  -o "$output_dir/orp" ./cmd/control-plane)
chmod 0755 "$output_dir/orp"
printf '构建完成：%s\n' "$output_dir/orp"
