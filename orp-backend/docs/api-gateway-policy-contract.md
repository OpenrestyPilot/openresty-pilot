# API 网关授权与配额契约（阶段 2）

更新日期：2026-10-19。

本文定义 Consumer/API Key 接入后的路由授权与配额边界。它是实现前契约，不表示当前数据面已执行 Consumer 授权或消费者配额。

## 当前证据与边界

- Quickstart 路由保存在 `orp_resource` 的 `http-listeners` JSON 文档中；当前字段 `allowedMethods` 和 `rateLimit*` 分别限制 HTTP 方法、按 `$binary_remote_addr` 执行单节点速率限制。
- 发布器会渲染路由方法限制、IP 策略和上述 IP 速率限制。它没有 Consumer 解析、API Key 验证、Consumer 授权或 Consumer 配额执行能力。
- 旧模型 `api_policy` / `api_policy_rule` 在迁移中存在；旧 HTTP Server/Location API 读写 `apiPolicyEnabled` 和策略顺序字段。但当前 Quickstart `orp_resource` 发布器没有消费这些旧表或 `api_policy_rule`。不能因旧表存在或开关可保存，就认定 Quickstart 路由授权有效。
- Consumer/API Key 管理与密钥生命周期正在由并行工作实现；该切片目前不接入代理请求认证、身份注入或路由授权。因此管理 API 可用（待合并与验证）不代表数据面已保护路由。
- 不允许把客户端可自行设置的普通请求头作为 Consumer 身份或计量 key。当前尚未确定 API Key 校验结果到路由执行层的可信身份传递协议，因此不在路由文档中预设 header、NGINX 变量或 Lua 接口。

## 目标资源与策略文档

授权策略绑定到已解析出的具体 HTTP 路由，而不是仅绑定路径字符串。首选引用稳定且不可复用的路由标识，并同时校验 listener/center 归属；当前 `route.id` 是监听器文档内的数字 ID，不应直接假定为全局 ID。策略保存时若引用不存在、跨监听器或已删除的路由，必须拒绝保存或使候选校验失败，不能静默放宽为匿名访问。

建议策略语义：

| 字段 | 语义 | 安全默认值 |
|---|---|---|
| `authentication` | `public` 或 `api_key_required`；描述是否要求已验证身份，不负责验证密钥 | 新路由维持兼容的 `public`；受保护必须显式配置 |
| `consumerMode` | `any_authenticated` 或 `allow_list` | `any_authenticated` 仅在明确选择后生效 |
| `consumerIds` | `allow_list` 时允许访问的 Consumer 不可变 ID 集合 | 空集合拒绝所有已认证 Consumer，保存时提示配置错误 |
| `quotaPolicies` | 带作用域、额度和周期的限额集合 | 空集合表示不配置 Consumer 配额，不等于 IP 限流 |
| `schemaVersion` | 策略文档版本 | 必须显式版本化，未知版本拒绝发布 |

具体字段名需与 Consumer/API Key 实现者统一后再固化进共享资源 schema/OpenAPI。策略应随路由配置冻结进候选和发布快照，以确保审计、预检、发布、回滚使用同一输入。

## 决策顺序

1. 先依据 listener/location 匹配选出唯一实际路由，并执行 `allowedMethods` 等路由约束。
2. 若路由要求 `api_key_required`，缺失、无效、撤销或过期凭证返回 401；认证成功后身份必须来自控制面/网关的可信验证结果。
3. `any_authenticated` 接受任一有效 Consumer；`allow_list` 只接受明确列出的 Consumer。已认证但无访问权返回 403。空/缺失策略不能意外扩大已有保护范围。
4. 完成授权后再计量并检查 Consumer 配额。超额返回 429，并记录 Consumer、路由、额度维度和周期，不记录原始 API Key。
5. 现有按 IP 的 `limit_req` 可与 Consumer 配额并存，作为独立保护层；其中任何一层都不能替代另一层。

相同路由上不引入多条隐式覆盖规则。首版以单一有效路由策略为准；如果未来允许组织、产品、Consumer 多层策略，则必须明确 deny 优先，并以每个配额维度最严格的非空上限生效，禁止“后写覆盖前写”导致额度放宽。任何策略冲突应在候选校验期报错，而不是运行期不确定裁决。

## 配额语义与数据面选择

- 配额维度至少需要区分 `consumer`、`route` 和 `consumer_route`；不能仅凭单一速率字段表达多个维度。
- 额度应明确算法、单位、窗口/周期、时区边界、超额响应、计量点和失败模式。首版可选择固定窗口或令牌桶其中之一，API/界面/API 文档必须统一，不能将 NGINX 的平滑 `limit_req` 语义描述为精确自然日/月配额。
- 当前 NGINX 原生 `limit_req` 的 key 是客户端 IP，zone 是单节点 worker 共享。把它更换成 Consumer key 需要可信身份变量和身份设置时序；它仍然是每节点额度，多节点会各自计数，不是中心级配额。
- 若产品承诺多节点共享的 Consumer 配额，需要有跨节点原子计数器及超时/不可用策略（例如 Redis/Lua 明确处理），并验证集群故障期间 fail-open 或 fail-closed 决策。不能只依赖控制面配置或每节点 zone 就宣称全局额度。
- 必须防止身份混淆：覆盖/清除用户输入的保留身份头；只允许验证组件写入可信上下文；代理到上游时只转发经过验证的 Consumer ID/必要声明。认证/配额密钥不写入访问日志、审计 diff、错误响应或普通 debug 日志。

## 实现门槛

在把阶段 2 标为完成前，至少需要：

- 与 Consumer/API Key 实现统一不可变 Consumer ID、验证结果接口、密钥轮换/撤销状态和可信身份传递协议。
- 路由策略 CRUD 与归属校验、RBAC、审计、OpenAPI schema、候选冻结、版本兼容规则；旧路由无新字段时保持原行为。
- Renderer/Agent 只接受受支持的策略版本和固定指令模板，不把用户字符串拼为任意 NGINX/Lua 代码；所有引用做中心、监听器和路由级交叉校验。
- 验证 401、403、允许请求、429、匿名路由兼容、伪造身份头、撤销/轮换、并发额度边界、多节点计数及缓存/Redis 故障行为；部署目标上的 `nginx -t` 和真实请求均须通过。

在上述依赖尚未完成前，本项目阶段 2 状态应保持“设计/实现中”，并明确区分路由级 IP 限流与 Consumer 配额。旧 `api_policy` 表不作为 Quickstart 路由授权已经实现的证据；旧表是否保留或清理需另行评估，不在本契约中迁移。
