# ORP 前端

ORP 前端是 ORP 的管理控制台，作为独立项目维护。项目目录和包标识均为 `orp-frontend`，基于 Vue 3、Vite、TypeScript 与 Vben Admin 构建。框架专用包保留上游包名及 MIT 许可声明。

安装、启动、构建所需的 Node.js/pnpm 版本和环境配置请参阅 [`PROJECT.md`](PROJECT.md)。

<div align="center">
  <a href="https://github.com/anncwb/vue-vben-admin">
    <img alt="Vben Admin 标志" width="215" src="https://unpkg.com/@vbenjs/static-source@0.1.7/source/logo-v1.webp">
  </a>
  <br>
  <br>

[![许可证](https://img.shields.io/github/license/anncwb/vue-vben-admin.svg)](LICENSE)

  <h1>ORP 前端</h1>
</div>

## 项目特点

- **现代技术栈**：使用 Vue 3、Vite 和 TypeScript 构建。
- **主题定制**：提供多种主题颜色与自定义选项。
- **国际化**：内置多语言支持。
- **权限管理**：支持按权限动态生成路由。
- **管理控制台**：为 ORP 提供前端管理界面。

## 本地开发

```sh
pnpm install --frozen-lockfile
pnpm --filter @vben/web-antd dev
```

构建前端应用：

```sh
pnpm --filter @vben/web-antd typecheck
pnpm --filter @vben/web-antd build
```

## 参考资料

- [Vben Admin 中文站](https://vben.pro/)
- [Vben Admin 文档](https://doc.vben.pro/)
- [项目变更记录](https://github.com/vbenjs/vue-vben-admin/releases)

## 贡献

欢迎通过 GitHub Issues 反馈问题，或提交 Pull Request。项目采用 Conventional Commits 风格的提交前缀，例如 `feat`、`fix`、`docs`、`refactor`、`test` 和 `chore`。

## 浏览器支持

开发环境建议使用 Chrome 80 或更新版本。项目面向现代浏览器，不支持 Internet Explorer。

## 许可

本项目基于 Vben Admin，许可信息见 [`LICENSE`](LICENSE)。
