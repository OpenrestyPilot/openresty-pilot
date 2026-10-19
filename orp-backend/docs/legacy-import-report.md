# 旧 UUID 资源导入报告（可选兼容实现）

当前项目尚未正式发布，旧项目业务数据迁移已按决策排除。控制面启动不再调用旧数据导入器；本文件描述的是保留在代码中的可选兼容实现，不构成当前部署步骤或发布验收项。

V50 新增 `legacy_resource_import_report`，用于持久记录旧资源导入中实际新增或被阻断的类别。每条记录包含导入批次 UUID、资源类别、状态、新增记录数、阻断原因及时间戳。报告不包含旧数据正文，也不更新或删除任何旧表行。

导入报告在原有 SERIALIZABLE 导入事务中写入，和目标资源导入原子提交。若报告写入失败，目标资源事务回滚。逐类映射冲突由 savepoint 隔离并留下 `blocked` 报告；中心导入等发生在逐类报告阶段之前的致命错误仍通过启动日志排查。没有新增数据且没有阻断的类别不会单独产生报告行。

`imported_count` 表示本次新插入的目标资源数量；此前已按 `legacyId` 映射的记录不重复计数。阻断原因只描述无法无损映射的条件。Location、策略、按中心保存的 HTTP singleton 配置、审计和运行历史仍按类阻断并保留原始行，报告不代表这些数据已完成迁移，也不提供自动恢复运行状态的功能。

可用 SQL 按时间查看近期结果：

```sql
SELECT BIN_TO_UUID(run_id) AS run_id,
       resource_class,
       status,
       imported_count,
       blocked_reason,
       created_at
FROM legacy_resource_import_report
ORDER BY created_at DESC, id DESC;
```

结果需要结合控制面启动日志核对；若导入在报告阶段前失败，错误只会出现在启动日志中。
