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

Compose 发布仍保留同步预检路径：冻结配置和节点目标、渲染制品、执行目标语法校验后创建批次。另有 V44/V46 异步候选工作流及 `/config-candidates` 管理界面，提供候选冻结、attempt/job、公平租约调度、失败重试、暂停后继续/停止、归档、Diff 与每中心并发策略；目标只覆盖固定 Quickstart 节点和已登记的固定非 root Agent，其他拓扑明确阻断。候选默认仅校验，V49 增加显式手动发布以及默认关闭的“全部目标通过后自动发布”选项，发布前会重新校验中心权限、当前配置、节点身份和冻结基线。后续草稿修改会使发布前重新渲染摘要不匹配，从而要求重新校验候选。

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

展示配置变更及目标校验结果的审查证据。长期产品不设置独立人工审批门禁，也不要求提交人与审批人分离；发布人仍须确认语义 Diff、最终配置文本 Diff、目标校验结果和发布计划摘要。候选创建、校验管理和归档要求目标中心配置编辑权限；发布单独要求目标中心发布权限。候选默认仅校验；创建时可明确选择全部目标校验通过后自动发起发布，或在校验通过后由发布人手动确认。自动发布会重新校验权限、配置快照、节点身份及冻结基线；失败或未知结果不会自动发布。候选证据保留且不提供硬删除。

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

当前不是生产完备状态：固定三节点 Compose 发布路径继续可用；已登记 Agent 可通过固定任务与制品 API，在 Agent 和 OpenResty master 共用非 root 用户、使用 `/etc/openresty/generated` 固定目录的 profile 下执行目标校验、激活、reload、版本探针和失败回滚。Agent Canary 与中央历史快照回滚已进入固定 `probe`/`validate`/`publish` 任务链。V47 为 Agent publish task 与批次项持久化关联；重启时未领取任务安全取消，可信成功回报核对为成功，结果未知的批次进入 `recovery_required`。`POST /api/orp/publish/batches/{id}/reconcile-continue` 要求操作员逐节点提交观察 UUID，并经本地或 Agent 固定版本探针核实。它只允许冻结候选 UUID 或成功预检记录的基线 UUID；已开始但仍在基线的失败项被跳过，原未开始目标仅在仍处冻结基线、候选有效且节点身份未变化时重新入队，不重放未知激活。root-master/helper 与其他拓扑仍未实现。V44 异步候选 API、MySQL lease worker 和审计已接入；worker 对固定 Quickstart 节点执行非激活 `nginx -t`，对已登记 Agent 节点复用固定 Agent `validate` 操作。V46 冻结时保存逐节点基线版本/摘要与语义、文本 Diff，并提供受 RBAC 与审计保护的每中心 `maxInflight` 策略接口，候选并发和跨实例调度受同一中心上限约束。Agent 校验仅暂存候选并运行 `nginx -t`，不切换活动配置或 reload；异步候选与旧发布候选由 V49 显式关联；候选默认仅校验，可选择手动发布或默认关闭的校验通过后自动发布。非 Quickstart 且无 Agent 的节点、root-master/helper、原生指令或不匹配拓扑会明确校验失败。Agent 证书由外部签发并手动登记指纹。主动健康检查和 `slow_start` 等能力会被拒绝或未建模。GeoIP 来源需导入合法数据库，生产日志容量/归档尚未验收。详见 `frontend-backend-migration-status.md` 与 `../orp-node-agent/docs/protocol.md`。

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

本地 Docker 示例节点由 Go 控制面编排发布和 reload。固定非 root Agent profile 也可承载既有同步预检和批次发布；V47 保存 Agent publish task 与批次项关联。重启恢复只将未领取 task 安全取消，将明确成功结果记为成功；已领取但结果未知等情况保持暂停并要求人工核对，不会自动重发。独立 V44 候选校验 worker 以 MySQL 保存候选、尝试、节点任务和租约；当前只调度固定 Quickstart 非激活校验器或已登记 Agent 的固定 validate task，不会激活配置。其他节点拓扑仍需受限 adapter。Redis 是外部可选依赖，不是配置权威来源；MySQL 保存候选、目标预检、批次、节点结果和审计证据。

### 数据库结构初始化

版本化 SQL 是 MySQL schema 的唯一来源；控制面启动时自动执行尚未应用的 SQL，以初始化新数据库或升级既有结构。控制面启动不导入之前项目的旧业务数据；受限兼容导入代码不在默认启动路径中。

### 节点发布通道

目标生产通道采用固定 Agent 操作和不可变候选制品，禁止任务携带命令、路径或服务名。当前已实现的 Agent profile 限 Agent 与 OpenResty master 以同一非 root 用户运行：写入固定候选目录、校验摘要和文件名、执行 `nginx -t`、原子切换 `active`、reload、版本探针并在失败时恢复旧链接。它不调用 sudo/helper。V44 异步候选 worker 复用 Agent 的 `validate` 操作时只写入不可变候选目录并执行 `nginx -t`，不会切换 `active` 或 reload。V47 提供保守的 Agent batch 重启核对及人工续跑接口：未领取任务取消、可信成功结果落库，未知状态经逐节点人工记录和固定探针核对；候选仍活动的记录为成功，基线仍活动的已开始任务只跳过，未开始目标仅在身份未变化且基线仍匹配时恢复排队。root-master 发布及其他节点拓扑仍不支持。详见 `../orp-node-agent/docs/protocol.md`。

### 实例日志通道

节点日志不由控制面直接读取作为主链路。每个节点配套 Filebeat，采集 `/var/log/nginx/*.access.log` 和 `/var/log/nginx/*.error.log`，写入 Kafka topic `orp-access-logs`。事件必须携带 `orp.node_name` 和 `orp_log_type`，控制面按节点缓存最近事件并提供 SSE；文件读取接口只作为故障排查备用接口。

### Control API 发布硬依赖

生产节点必须具备可用的 Nginx Control REST API 和本机 Unix socket。Control API 不可用时发布失败或进入未知状态，不自动回退到 `nginx -s reload`。
