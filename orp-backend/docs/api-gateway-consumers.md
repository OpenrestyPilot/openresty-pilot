# API 调用方与密钥管理

更新日期：2026-10-19。

## 当前实现

管理端 API 使用 `orp:system:manage` 权限，提供 Consumer/App 生命周期以及 API Key 的签发、查看元数据、轮换和撤销。Consumer 的删除是软删除：停用调用方并在同一事务中撤销其有效密钥。重新启用 Consumer 不会恢复旧密钥。

API Key 使用 `orp_live_` 前缀和 256 位随机值。数据库只保存 SHA-256 哈希和用于识别的短前缀；列表、Consumer CRUD 响应、审计记录均不包含完整密钥或哈希。创建与轮换成功时完整密钥仅在响应中显示一次，并附加 `Cache-Control: no-store`。客户端应立即安全保存。

轮换会创建新密钥并记录 `rotatedFromId`，旧密钥在轮换时继续有效，便于调用方切换；确认新密钥已部署后，管理员需单独撤销旧密钥。可以在签发时提供 `expiresAt` RFC3339 时间；不提供时当前为不自动过期。

## 管理 API

所有端点均需管理员登录，并要求 `orp:system:manage`：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/orp/consumers?page=1&pageSize=20` | 分页列出调用方和有效密钥数 |
| POST | `/api/orp/consumers` | 创建调用方：`name`、`description`、可选 `status` |
| GET | `/api/orp/consumers/{id}` | 读取调用方元数据 |
| PUT | `/api/orp/consumers/{id}` | 更新名称、描述和可选状态 |
| DELETE | `/api/orp/consumers/{id}` | 停用调用方并撤销所有有效密钥 |
| GET | `/api/orp/consumers/{id}/keys?page=1&pageSize=20` | 列出密钥元数据，不返回密钥值 |
| POST | `/api/orp/consumers/{id}/keys` | 签发密钥，可选 `expiresAt` |
| POST | `/api/orp/consumers/{id}/keys/{keyID}/rotate` | 签发替代密钥，旧密钥保持有效直到显式撤销 |
| DELETE | `/api/orp/consumers/{id}/keys/{keyID}` | 撤销指定密钥；重复撤销安全幂等 |

签发和轮换返回的 `data.secret` 只出现一次。API Key 校验查询 helper 会拒绝格式错误、哈希不存在、已撤销、过期密钥，以及所属 Consumer 已停用的密钥。当前该 helper 尚未接入代理请求链路；所以这些管理接口**还没有让 API Key 实际保护上游路由**。

## 尚待完成

- 将密钥校验接入具体 HTTP 路由认证策略，建立匿名与受保护路由的明确配置，并返回有效 Consumer 身份。
- 清理客户端可伪造的身份头，定义可信上游身份传递约定；补充请求审计时的 Consumer/API Key 标识，但不记录密钥。
- 为消费者授权策略、方法/路径匹配和应用级配额建立独立可发布模型。
- 完成数据库集成验证、API Key 正反向请求验收、轮换过渡与撤销即时生效验收；为密钥过期/使用状态增加运维检索。

现阶段只完成了调用方数据与凭据生命周期管理，不应将阶段 1 描述为“API Key 认证已上线”。
