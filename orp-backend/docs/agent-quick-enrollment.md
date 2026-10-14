# OpenResty Pilot Agent 快速接入

在控制台的「节点实例」页面创建节点后，点击「快速接入」，选择二进制安装或 Docker 容器启动，再复制 curl、wget 或 Docker 命令。节点 Token 只在签发响应中显示；重新签发会立即替换旧 Token。删除节点会撤销其 Token。

## 控制面配置

为 Agent 启用独立 HTTPS listener，并配置服务端证书、私钥和客户端 CA：

```dotenv
ORP_AGENT_HTTPS_ADDR=:18443
ORP_AGENT_TLS_CERT_FILE=/etc/orp/tls/server.crt
ORP_AGENT_TLS_KEY_FILE=/etc/orp/tls/server.key
ORP_AGENT_CLIENT_CA_FILE=/etc/orp/tls/client-ca.crt
ORP_AGENT_PUBLIC_URL=https://agent.example.com:18443
```

防火墙或负载均衡应将 `ORP_AGENT_PUBLIC_URL` 暴露到该 listener。页面签发的命令使用此 URL；Agent 请求仍通过 TLS 校验证书。已有 mTLS Agent 可继续使用证书认证。

这些运行参数也可以在控制面「系统配置」页设置。显式环境变量或 `.env` 的优先级高于数据库值，数据库设置在控制面重启后生效；证书和私钥内容仍由运维安全部署到文件系统。

## 二进制节点

curl/wget 命令下载 Linux amd64 Agent GitLab Release 包，安装固定 OpenResty reload helper 和 systemd 服务，再启动心跳。OpenResty 默认路径为 `/usr/local/openresty/nginx/sbin/nginx` 和 `/usr/local/openresty/nginx/conf/nginx.conf`。系统需要 systemd、sudo、curl 或 wget、tar，并以 root/sudo 执行安装命令。

GitLab Release workflow 在 `OpenrestyPilot/orp-node-agent` 的 `v*` 标签推送后发布 `orp-node-agent-linux-amd64.tar.gz`，快速接入命令通过 GitLab 最新 Release 的稳定资产链接下载。此包当前只覆盖 Linux amd64。

## Docker 节点

生成的 Docker 命令使用 `openresty/openresty:alpine`，在同一容器启动 Agent 和 OpenResty，因此 Agent 可以读取本地配置并执行受限 reload。命令默认发布容器 80 端口。需要自定义监听端口、镜像、卷或非标准 OpenResty 二进制/配置路径时，应在复制后调整启动参数与 Agent 配置。

Token 认证只接受节点 Agent HTTPS listener 上的 Bearer 请求；不能把 Agent listener 暴露为明文 HTTP。数据库只保存 Token 的 SHA-256 摘要。
