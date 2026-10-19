# GeoIP City 数据导入与真实数据验收

管理平台不会生成或猜测来源地理位置。只有启用有效 City `.mmdb` 且访问日志来源 IP 为可查的公网地址时，大屏才返回 GeoIP 来源点；私网、回环、链路本地、无效和数据库未命中的 IP 会计入 `unknownRequests`。没有数据库时返回 `geography.state=disabled`，仍可显示已配置坐标的中心标记。

## 导入流程

1. 获取部署方有权使用的 City `.mmdb` 文件。支持 MaxMind GeoLite2/GeoIP2 City，以及 DB-IP City Lite；使用 DB-IP Lite 时需遵守 CC BY 4.0，并在显示地理结果的大屏页保留 DB-IP 来源链接。数据库更新时重新核对来源版本及许可。
2. 使用具备 `orp:system:manage` 权限的管理员登录“系统配置”，确认“GeoIP City 数据库”状态。页面通过 `GET /api/orp/geoip-database` 显示来源、状态、数据库类型、大小、SHA-256 和托管导入记录。
3. 通过页面上传 `.mmdb`。后端限制单文件且最大 256 MiB，使用 MMDB Reader 验证数据库完整性和 City 类型，再安全替换托管文件并记录摘要、操作者与时间。无效或超限文件会拒绝；已有有效数据库在替换失败时保留。
4. 确认页面状态为 `ready`、`enabled=true`、`databaseType` 含 City，记录 SHA-256。状态为 `error` 时先处理文件/摘要/读取问题，不要把地图 fallback 当作已启用。

托管数据库优先于 `ORP_GEOIP_DB_PATH` / `system.geoip_db_path` 外部路径。容器部署要将 GeoIP 托管目录放在持久化运行时卷中，并确保升级和备份流程保留该文件；替换数据文件后须再次读取状态并核对 SHA-256。不要把数据库文件提交到仓库或镜像。

## 真实位置验收

1. 准备一个可公网访问、日志确实记录客户端公网地址的验收请求来源。Quickstart 本机回环请求会被有意标记为未知，不能证明 GeoIP 工作。
2. 通过真实公网客户端向 OpenResty 发送请求，核对访问日志经 Filebeat/Kafka 到 MySQL 后，事件保存的是该请求的实际 `remote_addr`，且目标节点与中心关联正确。测试期间须遵循实际的反向代理可信来源配置，不能用任意客户端头覆盖来源 IP。
3. 读取大屏 API 的 `geography` 字段（管理端使用 `/api/orp/dashboard/metrics`，公开大屏事件路由为 `/api/public/screens/events`）。等待下一次 SSE/快照更新，检查 `state=ready`、`sampledEvents`、`points`、`flows` 和 `unknownRequests`。
4. 对照查询 IP 的数据库记录与返回 `countryCode`、`name`、`coord`。确认点来自 GeoIP 解码结果，且坐标落在该数据库返回位置附近；地图中心点应来自中心配置的经纬度。不要仅以地图有线条或中心标记判定通过。
5. 使用纯私网/回环流量验证 unknown 计数；在隔离环境临时切换为无效文件或非 City 数据库验证状态变为 `error`，并确认没有虚构来源点。恢复有效数据库后重新核对状态与摘要。

验收记录需保存脱敏的测试 IP、数据库类型/构建时间/SHA-256、API 响应摘要、流量时间窗口和最终地图核对结果。禁止将 GeoIP 城市级估算描述为精确终端定位。

## 当前项目证据

后端已实现管理 API、托管导入、City 类型/完整性验证、摘要审计及地理事件聚合；前端系统配置页已提供授权上传和状态展示。地图使用 Kafka/MySQL 中实际日志 IP 查询坐标，不对未知地址编造来源；启用的 DB-IP City Lite 元数据会触发地图页的 CC BY 4.0 来源链接。

截至 2026-10-17，本机 Quickstart 已预置 DB-IP City Lite 文件 `orp-quickstart/runtime/geoip/GeoLite2-City.mmdb`（该文件位于 Quickstart 运行目录，不在应用源码仓库内）。只读检查得到文件大小 121 MiB、SHA-256 `9e250f02722d1ad1780f88192f0e908af285478a33e76b88b349c9181ae9d0f5`；使用后端相同的 `maxminddb-golang/v2` Reader 可读取元数据 `DBIP-City-Lite`（build epoch `1790818881`，UTC `2026-10-01T01:41:21Z`），并将验收地址 `8.8.8.8` 解码为 US / Mountain View / `37.42200,-122.08500`。控制面 `/healthz` 返回 `{"status":"ok"}`；公开大屏 SSE 最近快照报告 `geography.state=ready`、`databaseType=DBIP-City-Lite`，但 `sampledEvents=7090`、`unknownRequests=7090`、`points=0`、`flows=0`。因此已验证数据库文件有效且控制面加载了 City 数据库，也验证当前日志采样未产生可定位来源点；这不是公网客户端真实来源端到端验收。Quickstart 大屏请求来自本机/演示流量，私网和回环地址不应被 GeoIP 解码为来源城市。

数据库文件由 `.gitignore` 排除，不会进入源码提交。生产验收仍须使用实际公网客户端发起请求，并逐项核对访问日志中的来源 IP、Kafka/MySQL 事件与大屏位置；不得以本机数据库单 IP 解码替代该验收。
