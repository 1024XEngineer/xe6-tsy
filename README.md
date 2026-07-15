# xe6-tsy

本仓库是基层政务窗口方言沟通辅助 PC Agent 的模块化单体工程。

当前模块 3 提供接待会话和 Fake Media 的最小可运行骨架，使用内存状态、Audit、
Outbox 和可替换端口，不连接真实媒体设备、数据库或消息中间件。

## 本地运行

需要 Go 1.26 或更高版本。

```bash
make check
make run
```

服务默认监听 `127.0.0.1:8080`，可通过 `XE6_API_ADDRESS` 覆盖。健康检查地址为
`GET /healthz`，业务接口位于 `/api/v1/reception`。

模块 3 冒烟验收：

```bash
BASE_URL=http://127.0.0.1:8080 bash scripts/smoke/reception.sh
```

关联 Issue：[#36](https://github.com/1024XEngineer/xe6-tsy/issues/36)、
[#42](https://github.com/1024XEngineer/xe6-tsy/issues/42)。
