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

模块 3 已提供接待会话与 Fake Media 的最小可运行骨架；其他模块仍只有注册边界。模块 3
使用内存状态、deterministic fake、内存 Audit/Outbox，不连接数据库或真实媒体设备。

## 运行

需要 Go 1.26 或更高版本。

```bash
make check
make run
```

默认监听 `127.0.0.1:8080`。可通过 `XE6_API_ADDRESS` 和 `XE6_GIN_MODE` 覆盖；设置
`PORT` 时使用 `:<PORT>`，适合容器或平台注入端口。

模块 3 冒烟验收：

```bash
make run
BASE_URL=http://127.0.0.1:8080 bash scripts/smoke/reception.sh
```

关联 Issue：[#36](https://github.com/1024XEngineer/xe6-tsy/issues/36)、
[#37](https://github.com/1024XEngineer/xe6-tsy/issues/37)、
[#42](https://github.com/1024XEngineer/xe6-tsy/issues/42)。

目录规划、前后端边界和工程门禁见
[`docs/项目前后端统一开发规范.md`](docs/项目前后端统一开发规范.md)。
