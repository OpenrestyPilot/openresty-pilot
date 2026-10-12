# 应用镜像

`Dockerfile` 只构建 ORP单体应用：先编译 `orp-frontend/`，再把生成的页面嵌入 `orp-backend/` 的 Go 服务，最终生成应用容器镜像。二进制导出阶段供 GitLab CI 提取可下载的可执行文件。

从仓库根目录构建：

```sh
docker build -f deploy/Dockerfile -t openresty-plus:local .
```

OpenResty 节点、Filebeat 镜像及它们的启动编排由 `orp-quickstart` 仓库提供。
