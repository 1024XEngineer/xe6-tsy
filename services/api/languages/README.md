# languages

语言配置模块（契约真源：[Issue #88](https://github.com/1024XEngineer/xe6-tsy/issues/88)）。

## 当前阶段

| 边界 | 内容 | 行为 |
| --- | --- | --- |
| 数据库 | `supported_languages`、`voice_session_language_configs` | 迁移 + Postgres `Store` 已可用 |
| 前端 / HTTP | 四条 `/api/v1` 路由 | 仍返回 `501 not_implemented`（下一阶段接 Service） |
| 会话管理 / 实时转译 | `LanguageConfigReader` / `LanguageTargetResolver` | Stub 仍返回 `not_implemented` |

## 数据库

迁移文件：`migrations/001_language_config.sql`（权威配置表；暂不建 `voice_sessions` FK）。

```bash
# 默认连接（可用 DATABASE_URL 覆盖）
postgres://postgres:123456@localhost:5432/lingow?sslmode=disable

cd services/api
go test -tags=integration ./languages/ -count=1
```

`ApplyMigrations` 幂等；P0 种子语言为 `zh-CN` / `en-US`。

## 给其它模块

```go
reader := languages.NewStub() // 后续替换为基于 Store 的 Service
snapshot, err := reader.GetCurrentConfig(ctx, sessionID)
```

`GetCurrentConfig` **不接受 turnID**；轮内固定由实时转译模块本地快照完成。
