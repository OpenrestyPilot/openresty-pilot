# OpenResty Plus

OpenResty Plus 是 OpenResty 集群管理平台。前端和后端源码分别位于 `orp-frontend/` 与 `orp-backend/`；构建时会把 Vue 管理界面嵌入 Go 服务，生成一个同时提供管理页面和 API 的可执行文件。此仓库负责编译单体程序和制作它的 Docker 镜像。OpenResty 节点、Filebeat 及本地演示环境由独立的 [orp-quickstart](https://github.com/OpenrestyPlus/orp-quickstart) 仓库管理。

## 平台截图

以下截图来自本地运行的平台演示环境。

### 控制面大盘

![控制面大盘：查看请求、带宽和服务状态](docs/screenshots/dashboard.png)

### 节点实例

![节点实例：查看已接入的 OpenResty 节点及在线状态](docs/screenshots/nodes.png)

### 配置比对与下发

![配置比对与下发：查看待下发节点并进入配置差异审查](docs/screenshots/config-publish.png)

## 编译可执行文件

构建环境要求：Go 1.26.1、Node.js 22.18+ 或 24.12+、pnpm 11.16.0。首次构建会自动安装前端依赖：

```sh
./build.sh
```

生成 `dist/openresty-plus`。默认目标为当前操作系统和 CPU 架构；Linux x86-64 和 ARM64 可分别执行：

```sh
GOOS=linux GOARCH=amd64 ./build.sh
GOOS=linux GOARCH=arm64 ./build.sh
```

## 制作 Docker 镜像

仓库根目录中的 Dockerfile 会先编译管理界面，再编译并嵌入 Go 服务：

```sh
docker build -f deploy/Dockerfile -t openresty-plus:local .
```

镜像默认以前台方式运行单体程序，监听 `:8081`。GitLab CI 会在 `beta` 分支发布 Beta 镜像和二进制；符合 `vX.Y.Z` 或 `vX.Y.Z-beta.N` 格式的标签会发布版本镜像并创建 Release。GitHub Release 工作流会为 Linux、macOS、Windows 的 x86-64 和 ARM64 构建独立压缩包，并生成 SHA-256 校验文件。

## 程序运维命令

从后端示例配置复制 `.env` 到仓库根目录，并设置数据库、管理员密码和数据加密密钥，然后可以使用以下命令管理本机进程：

```sh
cp orp-backend/.env.example .env
./dist/openresty-plus start       # 后台启动
./dist/openresty-plus status      # 查看状态
./dist/openresty-plus logs -f     # 持续查看日志
./dist/openresty-plus restart     # 重启
./dist/openresty-plus stop        # 优雅停止
./dist/openresty-plus version     # 查看版本
```

`./dist/openresty-plus run` 会以前台方式运行，适合交给 systemd 或容器运行时管理。默认监听 `:8081`，PID 文件和日志写入 `runtime/`；可用 `OPENRESTY_STATE_DIR` 更改状态目录。数据库和演示环境的启动说明见 [orp-quickstart](https://github.com/OpenrestyPlus/orp-quickstart)。

## 目录

```text
.
├── deploy/Dockerfile   # 编译前后端单体程序并制作应用镜像
├── orp-backend/        # Go 控制面源码
└── orp-frontend/       # Vue 管理控制台
```

许可证见 [`LICENSE`](LICENSE)。
