# ORP Backend Development Notes

## 项目概述

本目录保存 Go 控制面源码和平台文档。前端源码位于同一 monorepo 的 `orp-frontend/`；应用单体构建由仓库根目录负责，本地 Compose 演示环境由独立的 `orp-quickstart` 仓库维护。

## 开发环境

- Go：1.26.1
- Node：22.18+ 或 24+
- pnpm：11.16.0
- Docker Compose：从独立 `orp-quickstart` 仓库启动本地 MySQL、Redis、Kafka 与演示节点

## 常用命令

### 控制面

```bash
cd orp-backend
go test ./...
go run ./cmd/control-plane
```

控制面默认监听 `:8081`，数据库连接通过 `ORP_DB_*` 环境变量配置。单体构建入口是仓库根目录 `build.sh` 和 `deploy/Dockerfile`。

### 前端

```bash
cd ../orp-frontend
pnpm install
pnpm -F @vben/web-antd run dev
pnpm -F @vben/web-antd run build
pnpm -F @vben/web-antd run typecheck
```

开发服务器将 `/api` 代理到 `http://127.0.0.1:8081`。

### 本地联调

中间件、OpenResty 演示节点及 Filebeat 的 Compose 配置位于独立的 `orp-quickstart` 仓库。请从该仓库按 README 启动；本项目目录只包含 Go 控制面源码。

## 后端结构

- `cmd/control-plane`：控制面入口。
- `internal/config`：环境变量与数据库连接配置。
- `internal/store`：MySQL 连接。
- `internal/httpapi`：REST 路由、资源处理器、认证兼容接口与审计。

## 约束

- MySQL 是配置权威；Redis 和 Kafka 为本地联调依赖，不得阻断基础配置管理。
- 写操作必须记录审计事件；UUID 按 MySQL `BINARY(16)` 存储并在 API 层转换。
- 页面请求统一使用 `/api`；生产前端通过 Nginx 同源代理访问控制面。
- 保存配置不等同于节点生效；发布、原生配置物化和 reload 必须分别验证。
- 真实凭据仅通过环境变量注入，禁止写入源码、文档或前端构建产物。
