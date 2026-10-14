#!/usr/bin/env bash
set -euo pipefail

backend_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
workspace_root="$(cd "$backend_dir/.." && pwd)"
env_file=${ORP_ENV_FILE:-"$backend_dir/.env"}
runtime_dir="$backend_dir/runtime/dev"
if [[ -z "${ORP_ENV_FILE:-}" && ! -f "$env_file" && -f "$workspace_root/.env" ]]; then
  env_file="$workspace_root/.env"
  runtime_dir="$workspace_root/runtime/dev"
fi

if [[ ! -f "$env_file" ]]; then
  printf '未找到 .env。请先复制 .env.example 并配置本地开发环境。\n' >&2
  exit 1
fi

# Parse KEY=VALUE without sourcing JDBC URLs containing shell metacharacters.
while IFS= read -r line || [[ -n "$line" ]]; do
  line=${line%$'\r'}
  [[ -z "$line" || "$line" =~ ^[[:space:]]# ]] && continue
  [[ "$line" == *=* ]] || continue
  key=${line%%=*}
  value=${line#*=}
  [[ "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || continue
  if [[ ${#value} -ge 2 && ( "${value:0:1}" == '"' || "${value:0:1}" == "'" ) ]]; then
    value=${value:1:${#value}-2}
  fi
  if ! declare -p "$key" >/dev/null 2>&1; then
    export "$key=$value"
  fi
done < "$env_file"

export ORP_DB_URL=${ORP_DB_URL:-"jdbc:mysql://127.0.0.1:${MYSQL_PORT:-3306}/${MYSQL_DATABASE:-orp}?useUnicode=true&characterEncoding=utf8&serverTimezone=Asia/Shanghai"}
export ORP_DB_USERNAME=${ORP_DB_USERNAME:-${MYSQL_USER:-orp}}
export ORP_DB_PASSWORD=${ORP_DB_PASSWORD:-${MYSQL_PASSWORD:-}}
export ORP_ADMIN_USERNAME=${ORP_ADMIN_USERNAME:-admin}

if [[ -z "${ORP_ADMIN_PASSWORD:-}" && -r "$runtime_dir/admin-password" ]]; then
  IFS= read -r ORP_ADMIN_PASSWORD < "$runtime_dir/admin-password"
  export ORP_ADMIN_PASSWORD
fi
if [[ -z "${ORP_DATA_KEY:-}" && -r "$runtime_dir/data-key" ]]; then
  IFS= read -r ORP_DATA_KEY < "$runtime_dir/data-key"
  export ORP_DATA_KEY
fi

if [[ -z "${ORP_ADMIN_PASSWORD:-}" || -z "${ORP_DATA_KEY:-}" ]]; then
  printf '缺少 ORP_ADMIN_PASSWORD 或 ORP_DATA_KEY；请先在 orp-quickstart 初始化 .env，再通过 ORP_ENV_FILE 指定配置文件。\n' >&2
  exit 1
fi

cd "$backend_dir"
go run ./cmd/seed-alert-demo
