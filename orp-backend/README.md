# ORP Backend — ORP Control Plane

ORP Backend 是 ORP 的独立 Go 控制面项目，运行参数可在系统配置中管理；对应环境变量优先于数据库值，数据库值优先于内置默认值。MySQL 结构通过 `internal/store/migrations/` 中的版本化 SQL 初始化。Redis 与 Kafka 不作为控制面数据正确性的依赖。独立运行说明见 [PROJECT.md](PROJECT.md)。

## 当前能力

- 登录、RBAC、OTP、敏感字段加密、配置资源 CRUD、审计、仪表盘与日志查询。
- 公开登录配置接口默认不返回管理员密码；仅本地演示明确设置 `ORP_LOGIN_PREFILL_CREDENTIALS=true` 时才启用登录页凭据预填。公网部署保持关闭。
- MySQL 冻结发布快照、语义与文本 Diff、节点内 `nginx -t`、分批发布、回滚、节点版本核验。
- 配置比对分开展示审计操作记录、资源级最终净差异和 NGINX 文本增删行数；三者统计单位不同，不应直接比较数值。
- 本地三节点使用受限 Docker 适配器；已登记 Agent 节点可通过固定 task/artifact API 运行候选校验与发布，但仅支持 Agent 与 OpenResty master 共用非 root 用户及固定 release profile。Agent 失败会在节点上恢复旧活动链接并探测旧版本；V47 持久化 Agent publish task 到批次项的关联。重启后存在未开始或结果未知项的批次进入人工恢复状态；`POST /api/orp/publish/batches/{id}/reconcile-continue` 要求逐节点人工记录观察 UUID 并通过固定探针核对，只接受冻结候选 UUID 或冻结预检基线 UUID。已开始但仍在基线的失败项只跳过；只有仍在基线的未开始目标才可继续排队，绝不重放状态不明的激活任务。
- V44 异步候选 API/持久化 worker 已接入创建、读取、归档、重试、暂停后继续/停止和租约调度；worker 对固定 Quickstart 节点执行非激活暂存与 `nginx -t`，对已登记 Agent 节点复用固定 `validate` task/artifact API。默认仅校验；创建时可显式打开“全部目标校验通过后自动发布”，也可在通过后手动确认发布。自动路径以数据库租约防止多实例重复建批，并重新核对权限、当前配置、节点身份和冻结发布基线；两种发布路径都复用既有 Canary/Agent 批次执行器，失败或结果未知不自动激活。
- V46 为每个冻结目标保存当时成功发布的基线版本/摘要及复用发布逻辑计算的语义、文本 Diff。中心校验策略由 `GET/PUT /api/orp/validation-centers/{centerId}/policy` 管理，`maxInflight` 为 1–32；中心 RBAC、审计、候选并发校验与 MySQL 跨实例调度共同约束该上限。
- Upstream 渲染支持轮询、最少连接、HTTP `ip_hash`、变量 Hash、一致性 Hash、被动失败参数及 Stream 多组后端展开。OpenResty 指令以渲染结果和目标节点语法校验为准。
- 大屏 GeoIP City 数据库可在系统配置页查看和导入/替换 `.mmdb`（256 MiB 上限，校验 MMDB City 类型、摘要并记录审计），文件默认保存在 `runtime/geoip/`，可在系统配置中指定持久目录或外部 `.mmdb` 路径（环境变量 `ORP_GEOIP_STORAGE_DIR`、`ORP_GEOIP_DB_PATH` 可覆盖）。支持 MaxMind GeoLite2/GeoIP2 City 与 DB-IP City Lite；DB-IP 数据启用时，大屏会按 CC BY 4.0 展示 DB-IP 来源链接。中心可保存经纬度，地图按公网客户端 IP 绘制真实来源聚合与流线。
- 控制面大盘提供带 Bearer 鉴权的 SSE 事件流：连接建立后立即发送等待事件；Redis 缓存命中时推送最近快照并后台刷新，冷缓存时先推送访问统计快照，再推送含 Docker/上游探测与 GeoIP 的完整快照。Redis 队列由两个 worker 处理，刷新任务按 5 秒窗口合并；Redis 不可用时使用短时进程内快照缓存。慢阶段耗时会写入控制面日志；Filebeat 日志可经 Kafka 持久化至 MySQL，本地文件作为无 Kafka 数据时的回退源。
- 实时日志接口提供 Bearer 鉴权 SSE；快速启动仓库中的 Filebeat 示例连接 `ORP_KAFKA_BOOTSTRAP_SERVERS`。
- 独立 Agent HTTPS listener 支持已登记节点的心跳、固定 reload/validate/publish 任务、候选制品读取与结果回报；Agent 与服务端可使用已登记 mTLS 身份或节点 Token。候选发布仅适用于文档规定的非 root profile。

控制面代码由本仓库提供；OpenResty、Filebeat 和中间件的本地启动配置位于独立的 [orp-quickstart](https://github.com/OpenrestyPilot/orp-quickstart) 仓库。Agent 生产发布只在受限非 root profile 下可用，不等于通用生产环境验收。Go 控制面功能盘点、验证证据和未闭环项见 [`docs/frontend-backend-migration-status.md`](docs/frontend-backend-migration-status.md)。

## 尚未闭环

- Agent 生产发布仍只支持固定非 root profile；root-master/helper 节点及其他拓扑仍未闭环。
- 异步候选目前只支持固定 Quickstart 三节点和使用非 root release profile 的已登记 Agent 节点；未登记 Agent、root-master/helper profile、节点原生指令及其他拓扑会产生明确校验失败，仍需按目标环境接入受限 adapter。
- Agent 证书由运维侧签发并通过超级管理员接口登记指纹；尚未提供自动证书签发/轮换服务，也没有 CRL/OCSP 流程。
- 生产 Kafka 集群部署、吞吐量容量规划与 MySQL 日志归档保留策略。
- 旧项目数据迁移不属于当前完成条件：项目尚未正式发布，按决策忽略旧数据。已有受限导入代码仅作为可选兼容路径保留。
- Go 路由与 OpenAPI 路径检查可通过 `go run ./cmd/openapi-check` 执行。前端维护的 OpenAPI 规范校验位于独立前端项目；有正文的成功响应均声明 schema。运行中 API 响应与完整业务 payload 的端到端契约仍只抽样覆盖。
- 商业版主动健康检查、`slow_start`、节点级原生指令及需额外 NGINX 模块的功能。

## 本地开发

后端单独开发方式见 [PROJECT.md](PROJECT.md)。平台文档位于 [`docs/`](docs/)。源码编译前后端单体程序和本地演示环境的说明见 [orp-quickstart](https://github.com/OpenrestyPilot/orp-quickstart)。

![ORP architecture](docs/openresty-architecture-polished.png)
