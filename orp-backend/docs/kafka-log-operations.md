# Kafka 日志消费运维

控制面消费 Filebeat envelope 后将有效事件写入 `orp_log_event`。对于无法安全解析或超过 1 MiB 上限的消息，V48 会将 topic、partition、offset、字节数、payload SHA-256、固定原因码和记录时间写入 `orp_log_rejection`；拒绝表不保存原始日志、节点名、URI、IP 或其他 envelope 字段。此表可能仍暴露消息来源 topic、处理时间和消息大小，按运维元数据保护。

拒绝元数据写入成功（包括相同 offset 已存在）后 consumer 才提交 Kafka offset。数据库写入失败时不提交 offset，消费者暂停该轮并重连重试；因此修复数据库可用性后会重新处理同一消息。有效事件使用原有 offset 去重表，再提交 offset。拒绝记录不自动重放或转换成业务事件。

## 本机 Quickstart 现状（2026-10-17）

只读检查时 Quickstart 的 Kafka、控制面、三个 OpenResty 节点及三个 Filebeat 容器均处于运行状态。Filebeat 配置从各节点 `/var/log/nginx/*.access.log` 与 `*.error.log` 读取日志，向本机 `127.0.0.1:9092` Kafka topic `orp-access-logs` 发送；控制面使用 consumer group `orp-control-plane-v1`。Kafka consumer group 查询结果为 partition 0 的 current offset 与 log end offset 均为 `2030688`，lag 为 `0`。最近采样的 Filebeat metrics 中三个节点均有发布和确认事件（观测到 published/acked 分别为 19、21、19 条；这是短时间窗口指标，不是累计总量），未见积压。

控制面 `/healthz` 返回 `{"status":"ok"}`；公开大屏 SSE 当前汇总到 7090 条请求事件，说明本机 Quickstart 日志消费与聚合链路有真实运行数据。该本机证据只覆盖 Quickstart 演示环境，不代表生产集群的 Kafka 可用性、持久化/副本配置、告警、Consumer Lag SLO、故障恢复或容量验收。生产环境应独立记录 broker/topic 配置、消费者积压曲线、Filebeat 队列与重试指标、数据库写入失败及拒绝表增长情况。

## 观察与人工处置

当前没有拒绝记录管理页面或专用管理 API。具备 MySQL 运维权限的人员可以使用 SQL 统计近期原因和积压：

```sql
SELECT reason, COUNT(*) AS rejected, MIN(recorded_at) AS first_seen,
       MAX(recorded_at) AS last_seen
FROM orp_log_rejection
WHERE recorded_at >= UTC_TIMESTAMP(6) - INTERVAL 24 HOUR
GROUP BY reason
ORDER BY rejected DESC;

SELECT kafka_topic, kafka_partition, kafka_offset, payload_bytes,
       payload_sha256, reason, recorded_at
FROM orp_log_rejection
ORDER BY recorded_at DESC
LIMIT 200;
```

使用 topic/partition/offset 在 Kafka 运维工具中核对原消息与 producer/schema 版本；只在受控环境临时检查原始消息，避免将内容复制到普通应用日志、工单或本表。确认 producer 修复后，运维人员可按平台事件保留政策清理到期拒绝元数据。清理拒绝表不会改变 Kafka offset，也不会触发重放。若需要重放，应先评估业务重复风险并通过单独的受控 Kafka 运维流程执行。

## 保留和容量

建议先将拒绝元数据保留 30 天，再按组织审计要求调整。表没有自动清理任务；运维方须配置经审计的周期清理，并监控拒绝数、最早记录时间、表大小、数据库写入失败和 Kafka consumer lag。可按日期批量删除，避免一次大事务：

```sql
DELETE FROM orp_log_rejection
WHERE recorded_at < UTC_TIMESTAMP(6) - INTERVAL 30 DAY
ORDER BY recorded_at
LIMIT 10000;
```

重复执行直至影响行数为 0。按 topic 最长 249 字节计算，含唯一索引和时间索引后容量会随拒绝量线性增长；粗略规划可按每条 0.3–0.5 KiB 预留（100 万条约 0.3–0.5 GiB），上线前应在实际 MySQL 版本和数据分布下测量表与索引空间。`INSERT IGNORE` 的 offset 唯一键使 consumer 在“数据库已写入、Kafka commit 尚未确认”的崩溃窗口内可安全重试，而不会重复计数。

## 拒绝原因

`invalid_json`、`missing_node_name`、`invalid_log_type`、`missing_message`、`invalid_access_message` 和 `message_too_large` 均为固定原因码。拒绝写入本身失败会阻止 offset commit，并在服务日志中只输出 topic/partition/offset 与数据库错误，不输出 payload 或 digest。
