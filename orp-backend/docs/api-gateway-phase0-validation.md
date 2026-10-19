# API 网关阶段 0 验收手册

更新日期：2026-10-19。

本手册用于验收 HTTP 路由允许方法和路由级请求限流。仅对 Quickstart 本地演示节点或一次性隔离实例操作；不要对生产节点执行本文的验证请求或临时发布。

## 当前实现边界

- 路由的 `allowedMethods` 为空或未设置时保留既有行为；非空时渲染 `limit_except <methods> { deny all; }`。
- 限流以 `$binary_remote_addr` 为 key，使用 `limit_req_zone` 和 `limit_req`。速率范围是 1–100000 次/秒，burst 范围是 0–1000000；未启用限流时不输出这些指令。
- 默认 NGINX 超限状态是 HTTP 503，除非资源中的其他配置覆盖了错误处理。`nodelay` 控制 burst 请求是否延后，不代表可突破速率和 burst 额度。
- 限流计数只在单个 OpenResty 节点内共享，不构成跨节点全局配额。
- 当前阶段验收对象是新资源模型的 HTTP 监听/路由与候选发布快照；旧 `/api/centers/{centerID}/http-servers` 兼容接口不在此轮验收范围。

## 安全边界与准备

1. 确认目标是本地 Quickstart 节点，且没有真实用户流量。记录节点名、监听端口和当前活动 release ID。
2. 选择专用的临时 host/path 和临时 upstream；不要在现有业务路由上叠加策略。为测试使用独立源 IP 或全新限流 key（本实现按客户端 IP 计数）。
3. 准备可回滚的当前配置快照。优先在隔离副本/临时容器运行；若只能使用 Quickstart 节点，需安排维护窗口，并确保测试完成后恢复原快照。
4. 发布前从控制面运行候选预检查，确认生成的冻结配置包含期望的 `limit_except`、`limit_req_zone` 与 `limit_req`。目标节点的 `nginx -t` 必须通过。只执行 `nginx -t` 不算真实流量验收。
5. 通过后才在隔离实例激活候选配置。不要把验证候选发布到公网或生产节点。

## 请求验收矩阵

建议准备一个临时路由，例如 `/_gateway-phase0/<随机值>`，指向只返回固定响应的测试 upstream。以下行为都要保留请求时间、方法、状态码、响应头、请求 ID 和 OpenResty 日志。

| 用例 | 路由策略 | 请求 | 预期 |
|---|---|---|---|
| 默认兼容 | `allowedMethods` 为空；限流关闭 | GET、POST | 均按原路由行为转发，不产生限流拒绝 |
| 允许方法 | `allowedMethods=[GET]` | GET | 请求到达 upstream，得到测试响应 |
| 拒绝方法 | `allowedMethods=[GET]` | POST、DELETE | 被 `limit_except` 拒绝，不能到达 upstream；记录实际 HTTP 状态（预期 NGINX `deny all` 为 403） |
| 速率限制 | `ratePerSecond=1`、`burst=0`、`nodelay=false` | 同一客户端短时间连续请求 | 正常请求受限；超额请求得到 NGINX 限流状态（默认 503），日志含 `limiting requests` |
| burst 延后 | `ratePerSecond=1`、`burst=2`、`nodelay=false` | 同一客户端快速发送至少 4 次请求，并测量延迟 | 允许的突发请求可能被延迟；超出速率与 burst 容量的请求被拒绝 |
| burst 即时 | `ratePerSecond=1`、`burst=2`、`nodelay=true` | 同一客户端快速发送至少 4 次请求 | 容量内突发请求不等待，超出容量的请求被拒绝 |
| 多客户端隔离 | 同一策略，从两个独立客户端 IP 发请求 | 每个客户端各自发送低于阈值的请求 | 两个独立 IP 的计数分别生效；从同一 IP 的并发客户端共享该 IP 的 bucket |

方法拒绝与限流拒绝可能在请求链路上竞争；要分别验证，应先单独启用一种策略，再做组合策略的回归用例。不要把 `curl` 的并发波动当作精确速率基准；多轮重复，并从 access/error log 核对结果。

## 回滚和清理

1. 停止测试流量并确认临时 upstream 无残留请求。
2. 恢复测试前保存的 release/config，执行目标 OpenResty 的 `nginx -t` 后再 reload。
3. 确认活动 release ID 已恢复、业务探针正常、临时路由不再出现在活动配置中。
4. 删除临时 listener/route/upstream 和临时容器、网络、文件。保留去敏后的验证记录。

若恢复后的 `nginx -t`、release ID 或业务探针任一项不符合预期，停止后续操作并按本地 Quickstart 的配置恢复流程处理；不得把测试配置留在活动节点。

## 验收记录

阶段 0 只有在以下字段均有证据后才可标记完成：

```text
执行日期 / 操作者：
隔离环境与节点：
测试 release ID / digest：
候选渲染配置位置（去敏）：
nginx -t 输出：
允许方法用例及 upstream 证据：
拒绝方法用例及状态码：
限流参数（rate / burst / nodelay）：
限流请求数、响应状态和日志证据：
双客户端 IP 用例：
恢复后的 release ID 与健康探针：
遗留问题：
```

截至本文更新，本地构建和静态实现检查已有记录，但尚无本手册矩阵的真实请求证据。因此阶段 0 仍为“进行中”，不得根据配置渲染成功或 `nginx -t` 单项通过宣称闭环完成。
