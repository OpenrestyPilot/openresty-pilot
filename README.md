# ORP

ORP 是面向多中心网关集群的配置管理平台。前端和后端源码分别位于 `orp-frontend/` 与 `orp-backend/`；构建时会把 Vue 管理界面嵌入 Go 服务，生成一个同时提供管理页面和 API 的可执行文件。此仓库负责编译单体程序和制作它的 Docker 镜像。OpenResty 节点、Filebeat 及本地演示环境由独立的 [orp-quickstart](https://github.com/OpenrestyPilot/orp-quickstart) 仓库管理。

## 平台截图

以下截图来自本地运行的平台演示环境。

| 控制面大盘 | 节点实例 |
| :---: | :---: |
| ![控制面大盘：查看请求、带宽和服务状态](docs/screenshots/dashboard.png) | ![节点实例：查看已接入的 OpenResty 节点及在线状态](docs/screenshots/nodes.png) |
| 中心化展示请求、带宽、延迟及节点状态。 | 查看已接入的 OpenResty 节点和运行健康状态。 |

| 中心管理 | HTTP 流量 |
| :---: | :---: |
| ![中心管理：按中心登记和管理节点实例](docs/screenshots/centers.png) | ![HTTP 流量：管理监听端口、域名绑定与路由规则](docs/screenshots/http-traffic.png) |
| 登记管理中心及其配置真相源。 | 管理 HTTP 监听、域名绑定和 Location 路由。 |

| Stream 流量 | 上游服务器组 |
| :---: | :---: |
| ![Stream 流量管理](docs/screenshots/stream.png) | ![上游服务器组管理](docs/screenshots/upstream.png) |
| 管理 TCP/UDP 四层代理服务。 | 管理后端服务器组及负载均衡配置。 |

| TLS 证书 | DNS 解析器 |
| :---: | :---: |
| ![TLS 证书管理](docs/screenshots/tls.png) | ![DNS 解析器管理](docs/screenshots/dns.png) |
| 管理证书及其部署状态。 | 配置中心范围内的 DNS 解析服务。 |

| IP 组管理 | 告警通知 |
| :---: | :---: |
| ![IP 组管理](docs/screenshots/ip-groups.png) | ![告警通知管理](docs/screenshots/alerts.png) |
| 管理可复用的 IP 地址组。 | 查看和管理平台告警规则与通知。 |

| 审计日志 | 实时日志 |
| :---: | :---: |
| ![审计日志](docs/screenshots/audit.png) | ![实时日志](docs/screenshots/realtime-logs.png) |
| 按操作人、模块和时间追溯变更。 | 汇聚查看节点及流量服务日志。 |

| 配置比对与下发 | 系统配置 |
| :---: | :---: |
| ![配置比对与下发：审查配置差异并进入发布流程](docs/screenshots/config-publish.png) | ![系统配置管理](docs/screenshots/system-settings.png) |
| 按中心审查配置差异，并进入预检和发布流程。 | 管理平台运行参数和配置分组。 |

| 用户管理 | 角色管理 |
| :---: | :---: |
| ![用户管理](docs/screenshots/users.png) | ![角色管理：查看系统内置角色和权限划分](docs/screenshots/roles.png) |
| 管理登录账号及其所属角色。 | 按运维职责拆分平台权限。 |

| 权限定义 | 个人中心 |
| :---: | :---: |
| ![权限定义](docs/screenshots/permissions.png) | ![个人中心](docs/screenshots/profile.png) |
| 查看并维护系统权限定义。 | 管理个人资料、安全设置和操作记录。 |

| 世界地图（2D） | 世界地图（3D） |
| :---: | :---: |
| ![世界地图大屏（2D）](docs/screenshots/screen-world-2d.png) | ![世界地图大屏（3D）](docs/screenshots/screen-world-3d.png) |
| 按全球视图展示实时请求态势。 | 以三维地球展示实时请求态势。 |

| 四川省地图（2D） | 四川省地图（3D） |
| :---: | :---: |
| ![四川省地图大屏（2D）](docs/screenshots/screen-sichuan-2d.png) | ![四川省地图大屏（3D）](docs/screenshots/screen-sichuan-3d.png) |
| 按四川省视图展示实时请求态势。 | 以三维视图展示四川省请求态势。 |

| 成都市地图（2D） | 成都市地图（3D） |
| :---: | :---: |
| ![成都市地图大屏（2D）](docs/screenshots/screen-chengdu-2d.png) | ![成都市地图大屏（3D）](docs/screenshots/screen-chengdu-3d.png) |
| 按成都市视图展示实时请求态势。 | 以三维视图展示成都市请求态势。 |

| 接口文档 | 登录页面 |
| :---: | :---: |
| ![OpenAPI 接口文档](docs/screenshots/api-docs.png) | ![平台登录页面](docs/screenshots/login.png) |
| 浏览平台提供的 OpenAPI 3.0 接口。 | 展示平台账号登录入口。 |

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

`./dist/openresty-plus run` 会以前台方式运行，适合交给 systemd 或容器运行时管理。默认监听 `:8081`，PID 文件和日志写入 `runtime/`；可用 `OPENRESTY_STATE_DIR` 更改状态目录。数据库和演示环境的启动说明见 [orp-quickstart](https://github.com/OpenrestyPilot/orp-quickstart)。

## 目录

```text
.
├── deploy/Dockerfile   # 编译前后端单体程序并制作应用镜像
├── orp-backend/        # Go 控制面源码
└── orp-frontend/       # Vue 管理控制台
```

许可证见 [`LICENSE`](LICENSE)。
