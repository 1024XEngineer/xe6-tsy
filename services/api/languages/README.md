# languages

语言配置模块（契约真源：[Issue #88](https://github.com/1024XEngineer/xe6-tsy/issues/88)）。

## 能力

| 边界 | 行为 |
| --- | --- |
| HTTP | 四条 `/api/v1` 路由：目录 / 当前配置 / 创建切换 / 历史 |
| 内部端口 | `LanguageConfigReader`、`LanguageTargetResolver`（由 `Service` 实现） |
| 存储 | Postgres（迁移 + `PostgresStore`）；单测用 `MemoryStore` |

## 运行

```bash
# 必填才启用真实语言服务
DATABASE_URL=postgres://postgres:123456@localhost:5432/lingow?sslmode=disable

# 仅本地演示：跳过 voice_sessions 归属校验（勿用于生产）
LANGUAGE_SESSION_OWNER=trust-auth
```

未设置 `DATABASE_URL` 时，语言路由返回 `501 not_implemented`。  
有 PostgreSQL 时，默认通过 `NewRecordsSessionOwner(CanonicalSessionOwner)` 校验
`voice_sessions.account_id`（含账户 merge 后的 canonical owner）。非所有者返回 `403`，
会话不存在返回 `404`。

## 测试

```bash
cd services/api
go test ./languages/
go test -tags=integration ./languages/ -count=1
```

## 给其它模块

```go
svc := languages.NewService(store, sessionOwner)
snapshot, err := svc.GetCurrentConfig(ctx, sessionID)
```

`GetCurrentConfig` **不接受 turnID**；轮内固定由实时转译模块本地快照完成。
