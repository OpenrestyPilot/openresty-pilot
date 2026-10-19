# 0013：通过 Lua Location 触发 systemd restart

## 状态（2026-10-15）

本文记录的 Lua Location/systemd 一次性 helper 尚未实现，不能作为当前节点操作能力说明。当前已接入的是受限 Agent 固定任务协议；候选发布只支持 Agent 与 OpenResty master 共用非 root 用户的固定 release profile。root-master 示例 helper 仅支持无参数的固定配置校验/reload，不能用于候选发布。把候选配置交给普通 sudo helper 会让低权限 Agent 伪造摘要并可能借助 NGINX Lua/动态模块获得 root 执行能力，因此在控制面签名授权、root-owned 公钥和防重放 ledger 落地前，root-master/helper 发布必须保持禁用。详见 `../orp-node-agent/docs/protocol.md` 与根目录实现状态盘点。

平台不部署常驻节点管理代理。候选制品同时包含 NGINX 配置和节点运行描述，systemd unit 由固定模板与白名单字段生成；节点运行描述继承中心默认值，也可声明显式节点覆盖，覆盖进入独立 Diff、制品摘要及逐节点验证。候选先经受限 SSH 暂存并在各目标节点以真实运行环境完成 `nginx -t` 与 `systemd-analyze verify`；发布触发绑定不可变制品和发布计划摘要。控制面校验发布身份、摘要和目标校验结果后，经管理网双向 TLS 调用节点受保护的 Location；Lua 仅以固定参数调用受限本机 helper，helper 仅允许激活已登记的 systemd unit/启动参数并执行 `daemon-reload` 与 restart，不接受任意命令或环境变量值。EnvironmentFile 只绑定受控文件身份、版本及摘要，不暴露其值。首期不负责将直接运行 `./sbin/nginx` 且无 systemd unit 的节点迁移纳管；此类节点可以执行已确认安全的 reload，但 restart-required 或 unit 变更必须阻断。该决策保留无常驻代理原则并允许受控管理现存 unit；当前 Compose 运行形态没有 systemd 和 helper，尚不支持验证此通道。

**helper 生命周期隔离决策：** 采用固定、受限的一次性 systemd service/job 执行 helper，Lua Location 只请求此固定执行入口。执行单元与 OpenResty unit 生命周期隔离，避免重启 OpenResty 时 helper 随目标服务一同退出。单元名称/模板、helper 路径、操作类型和参数均采用白名单；不允许调用方指定任意 transient unit、命令或参数。helper 仍只操作已登记的 systemd unit，并且候选与发布计划摘要、目标校验及逐节点 canary 门禁保持不变。
