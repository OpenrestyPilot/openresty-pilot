# ORP 领域词汇

## 领域边界

本项目管理多中心 OpenResty/Nginx 配置的版本、策略和发布状态。MySQL 保存结构化配置、不可变版本快照和审计；当前 Compose 使用 Go 控制面处理已迁移的资源管理 API；管理 Web 提供可视化操作；OpenResty Lua 负责已发布运行时快照，Control API 负责原生配置 reload；Filebeat 将节点日志发送到 Kafka。

## 术语

### 中心（Center）

一组具有共同配置发布边界和运维归属的 OpenResty/Nginx 节点集合。中心拥有自己的配置合成结果、发布版本和访问策略作用域。

### 节点实例（Node Instance）

中心内承载 OpenResty/Nginx 数据面的具体主机或实例。节点实例具有协议监听器、能力基线、活动制品版本和运行状态。

### 监听器（Listener）

节点实例上一个明确的协议、监听地址/端口及其业务标识。HTTP 监听器可关联域名和 location；Stream 监听器关联 TCP/UDP 服务和端口。

### 配置制品（Configuration Artifact）

由 MySQL 配置版本、中心变量、策略数据、节点运行描述、渲染校验基线和内容摘要组成的不可变发布对象。

### 配置候选（Configuration Candidate）

在当前 Compose 实现中，发布预检会冻结一致配置快照、固定节点目标、渲染制品并执行节点语法校验，随后可创建发布批次；目前采用按需预检，不是 ADR-0015 所述的候选生成后自动异步校验。它不实现独立候选工作台、候选归档/重试/公平调度，也不实现中心配置编辑权限校验。后续草稿修改会使发布前重新渲染摘要不匹配，从而要求重新预检。

### 节点运行描述（Node Runtime Profile）

描述节点已登记的服务管理方式、受控 systemd unit 设置及 OpenResty 启动参数的配置对象；秘密通过受控环境文件引用，不将值放入配置制品。

### 配置输入（Configuration Input）

进入配置草稿的受管理配置内容，包含平台结构化资源和按明确上下文归属的原生指令片段。

### 原生指令片段（Native Directive Fragment）

归属于一个明确 NGINX 配置上下文、用于承载尚未由结构化资源表达的配置指令的文本片段。

### 受控原生片段（Governed Native Fragment）

仅用于平台开放的业务配置上下文、且不得接管平台控制的根配置、模块加载、include 路径、Lua 执行入口或监听骨架的原生指令片段。

### 配置变更（Configuration Change）

两个配置制品之间可定位到配置块、配置对象或单条指令的增删改记录，包含变更前后值。

### 配置审查（Configuration Review）

展示配置变更及目标校验结果的审查证据。长期产品不设置独立人工审批门禁，也不要求提交人与审批人分离；后续发布人仍须确认语义 Diff、最终配置文本 Diff、目标校验结果和发布计划摘要后才能触发发布。首期候选生成、校验、归档和取消归档要求具备目标中心配置编辑权限；候选证据保留且不提供硬删除。后续发布权限仍需在发布阶段定义。

### 策略模块（Policy Module）

独立管理和启停的 IP 访问策略、API 访问策略或限速策略。策略模块默认关闭，只有模块启用且绑定到适用资源并成功发布后才生效。

### 资源绑定（Policy Binding）

将策略模块关联到中心、监听器、HTTP 域名端口或 location/API 的明确关系。绑定拥有独立启用状态和活动发布版本。

### 发布（Deployment）

将一个配置制品按中心和节点批次执行校验、原子切换、reload 和健康确认的过程。

### 活动版本（Active Version）

节点当前实际运行并经 reload 确认的配置制品版本，不等同于数据库中尚未发布的编辑状态。

### 配置回滚（Configuration Rollback）

以历史配置内容为来源、相对当前活动版本重新生成候选制品并经过目标校验，再依当前发布门禁触发的发布操作。

### 配置 JSON

控制面、策略文件和前后端 API 中的 JSON 统一使用 Go 标准库 `encoding/json` 进行序列化和反序列化。

### Web 配置生效

Web 保存只更新 MySQL 草稿。发布先生成绑定配置摘要和发布计划摘要的候选，经目标节点校验并展示审查证据后，由获授权的发布路径按 reload/restart 计划逐波激活；不设置独立人工审批门禁。成功需节点报告期望摘要、Control API 操作成功及受影响监听器/路由合成探测通过。

### MVP 范围

当前 Go Compose 可运行版本包含中心、节点、HTTP/Stream、TLS、DNS Resolver、策略、账号/RBAC、审计等 API。三个固定本地 Docker 示例节点支持冻结发布预检、渲染、目标容器 `nginx -t`、活动路径切换、reload、工作进程版本探针、权重分批/观察期、中止及单节点历史快照回滚；节点指标、定时探测、Kafka 日志持久化、鉴权 SSE 大盘/实时日志、GeoIP 管理接口和 TLS 到期告警也已实现。

当前不是生产完备状态：发布适配器仅接受三个固定 Compose 节点；Agent mTLS listener 已支持心跳、固定 reload 任务和结果回报，但还未接入候选制品暂存、校验及发布批次。证书由外部签发并手动登记指纹。Go 控制面重启时通过版本探针核对执行中 Compose 任务、终止排队项，不支持租约续跑。候选校验仍是同步预检；没有 ADR-0015 定义的异步候选校验工作流。节点级原生指令、主动健康检查和 `slow_start` 等能力会被拒绝或未建模。GeoIP 来源需导入合法数据库，生产日志容量/归档尚未验收。详见 `frontend-backend-migration-status.md` 与 `../orp-node-agent/docs/protocol.md`。

### 配置覆盖目标

平台目标是管理可发布的 NGINX/OpenResty 配置块和指令，并对每项变化提供人工审查依据。配置通过结构化资源及明确上下文归属的原生指令片段共同表达；“可发布”不代表每项配置均能无 reload 或 restart 地动态生效。

### 开发依赖凭据

MySQL 和 Redis 仅作为外置开发依赖通过环境变量或本地未提交配置注入。真实密码不得写入源码、默认配置、前端资源、日志或 Git 文档；仓库只提供变量名示例。

### 工程命名

工作区内项目目录分别为 `orp-backend`、`orp-frontend` 和 `orp-node-agent`；单体应用镜像定义位于仓库根目录 `deploy/Dockerfile`，OpenResty 与 Filebeat 演示镜像位于独立的 `orp-quickstart/deploy` 仓库。Go 模块名为 `net.daoke/orp-backend`，本地服务标识为 `orp-control-plane`。

前端项目为 `orp-frontend`，固定使用 Vben Admin `v5.7.0` 基线；Node 与 pnpm 版本以 `orp-frontend/PROJECT.md` 为准。

### 后端构建

Go 控制面使用 Go 1.26、`go test ./...` 和 `go vet ./...`；单体应用镜像由仓库根目录的 `deploy/Dockerfile` 构建。

### 后端版本基线

当前运行后端采用 Go 1.26。

### API 契约

控制面提供 REST API；前端维护 OpenAPI 3.1 契约并检查 Go ServeMux 路径映射。该检查不验证完整业务响应 payload，也不等同于运行时 API 契约测试。发布批次创建返回批次 ID 并通过状态接口查询；并非所有后台操作都采用异步任务模式。

### 异步任务

本地 Docker 示例节点的发布和 reload 编排已迁移到 Go 控制面；生产发布通道仍待实现。Redis 是外部可选依赖，不是配置权威来源；MySQL 保存候选、目标预检、批次、节点结果和审计证据。

### 数据库结构初始化

版本化 SQL 是 MySQL schema 的唯一来源；控制面启动时自动执行尚未应用的 SQL，以初始化新数据库或升级既有结构。该流程不导入外部数据库中的旧业务数据。

### 节点发布通道

生产配置候选在发布前通过受限 SSH 和节点固定脚本暂存到目标节点，并在目标节点真实运行环境执行 `nginx -t`；暂存不得改变活动配置。发布只激活与当前候选摘要一致的字节。平台管理已登记的 systemd unit 和 OpenResty 启动参数；restart 由控制面经管理网双向 TLS 调用节点受保护的 Lua 管理入口，Lua 只请求固定、受限的一次性 systemd service/job 执行 helper，以隔离 helper 与 OpenResty unit 的停止生命周期。执行单元和 helper 均采用固定白名单，不接受任意 unit、命令或参数。首期不负责把直接运行 `./sbin/nginx` 的节点迁移到 systemd；无受支持 unit 的节点不得执行平台 restart 或 unit 变更。节点不运行常驻管理代理。当前 Compose 联调仍以项目目录绑定挂载和节点 Control API 转发为主，尚未实现上述生产链路。

### 实例日志通道

节点日志不由控制面直接读取作为主链路。每个节点配套 Filebeat，采集 `/var/log/nginx/*.access.log` 和 `/var/log/nginx/*.error.log`，写入 Kafka topic `orp-access-logs`。事件必须携带 `orp.node_name` 和 `orp_log_type`，控制面按节点缓存最近事件并提供 SSE；文件读取接口只作为故障排查备用接口。

### Control API 发布硬依赖

生产节点必须具备可用的 Nginx Control REST API 和本机 Unix socket。Control API 不可用时发布失败或进入未知状态，不自动回退到 `nginx -s reload`。
