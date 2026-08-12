# languages

语言配置模块（契约真源：[Issue #88](https://github.com/1024XEngineer/xe6-tsy/issues/88)）。

## 能力

| 边界 | 行为 |
| --- | --- |
| HTTP | 五条 `/api/v1` 路由：目录 / 自动投递 readiness / 当前配置 / 创建切换 / 历史，支持按 target_language 配置输出路由，并返回派生的 `output_mode` |
| 内部端口 | `LanguageConfigReader`、`LanguageTargetResolver`、`SpeechRouteReader`（由 `Service` 实现） |
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

`output_mode` 只有两个值：`bidirectional`（两个方向都播报）和 `single`（当前源语言译文播报，反向译文自动投递）。`single` 只有在 delivery runtime 已启用且目标 channel provider 已配置时才会接受；否则返回 `delivery_target_required`。活动会话切换配置时使用 `expected_version`，新快照从下一 Turn 开始生效；正在处理的 Turn 不被中途改写。

## Speech routing metadata

PostgreSQL also stores internal control-plane metadata in `speech_asr_profiles`,
`speech_tts_profiles`, their supported-language tables, and
`speech_language_pair_routes`. Profiles record non-secret `provider_code`,
`model`, and `voice_id` metadata plus their required ASR input or TTS output
media specification. Routes are keyed by a canonical unordered BCP-47 pair,
with the two stored codes in lexicographic order. A route resolves to only the
ASR/TTS profile IDs; provider credentials, endpoints, and adapter construction
remain deployment and realtime responsibilities.
Routes retain a durable `id` and lifecycle state. The active route for a pair
is unique only while `enabled=true` and `retired_at IS NULL`, allowing an older
route record to remain auditable after it is retired.

Production language-config creation enables strict route validation. It rejects
a pair without an active, unretired route, requires ASR support for both possible
source languages unless auto-detection is declared, and requires TTS support
only for target languages whose output routes set `tts_enabled=true`. The
ordinary `NewService` constructor remains non-strict for existing in-memory
callers and offline tests.

Migration `005_speech_routing.sql` seeds `legacy-default` ASR/TTS profiles and
the `en-US` / `zh-CN` route only when both active catalog entries retain source
and target support. The seed intentionally contains no vendor secrets or
endpoint guesses. Realtime integration consumes the profile IDs through the
shared contract boundary; it is not implemented by this module.
