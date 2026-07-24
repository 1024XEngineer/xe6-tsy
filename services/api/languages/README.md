# languages

语言配置模块（契约真源：[Issue #88](https://github.com/1024XEngineer/xe6-tsy/issues/88)）。

## 当前阶段（空接口）

已暴露、可联调对齐：

| 边界 | 内容 | 行为 |
| --- | --- | --- |
| 前端 / HTTP | `GET /api/v1/languages` 等四条路由 | 一律 `501 not_implemented` |
| 会话管理 / 实时转译 | `LanguageConfigReader`、`LanguageTargetResolver` | 返回 `ErrNotImplemented` |

尚未实现：持久化、校验、版本切换、幂等、乐观锁、会话归属鉴权。

## 给其它模块

```go
reader := languages.NewStub() // 后续替换为真实 Service
snapshot, err := reader.GetCurrentConfig(ctx, sessionID)
```

`GetCurrentConfig` **不接受 turnID**；轮内固定由实时转译模块本地快照完成。
