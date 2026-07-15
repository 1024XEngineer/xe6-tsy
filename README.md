# xe6-tsy Gin 基础框架

本分支根据 Issue #37 和训练营“代码即架构”要求，先建立最小可运行的 Go + Gin
服务骨架，不实现业务功能接口。

当前包含：

- Gin HTTP 服务启动和优雅退出；
- 环境变量配置入口；
- `/api/v1` 版本分组；
- 七个业务模块的挂载边界；
- 基础健康检查 `GET /healthz`；
- 最小单元测试和本地检查命令。
- Wails/Vue 前端目录与仓库级目录规划。

七模块当前只有注册边界，没有业务路由、数据模型、provider、数据库或外部服务实现。
这些内容应由后续独立 Issue 和小范围 PR 增量补充。

## 运行

需要 Go 1.26 或更高版本。

```bash
make check
make run
```

默认监听 `127.0.0.1:8080`。可通过 `XE6_API_ADDRESS` 和 `XE6_GIN_MODE` 覆盖。

Issue：<https://github.com/1024XEngineer/xe6-tsy/issues/37>

目录规划、前后端边界和工程门禁见
[`docs/项目前后端统一开发规范.md`](docs/项目前后端统一开发规范.md)。
