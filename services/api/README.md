# services/api

Go 应用控制服务，负责业务会话、语言配置、数据访问和状态快照，不是管理后台，也不承载 WebRTC 连接。

## 职责

- 会话创建/结束
- 编排实时会话启动和停止
- 可选语言列表和语言对配置
- 演示客户端/设备接入
- 校验会话归属并签发短期实时连接票据
- 会话状态快照查询
- 健康检查
- 必要的调试记录
- 匿名账户、手机号登录和 Token 生命周期边界
- 会话与账户用量查询边界
- final Turn 的异步消息投递边界

## 非职责

- 不处理实时音频流
- 不交换 SDP offer/answer 或 ICE candidate
- 不创建和保存 PeerConnection、DataChannel、Audio Track
- 不直接调用 ASR/翻译/TTS
- 不维护播放状态机
- 不做组织权限、订单、套餐、支付、发票、术语库和管理后台
- 不在实时主链路中调用第三方消息 Provider

## 建议包结构

```text
services/api/
├── main.go
├── config/
├── devices/
├── sessions/
├── languages/                 # 语言配置：HTTP + Service + Postgres（需 DATABASE_URL）
├── realtimeaccess/            # 会话鉴权和短期实时连接票据
├── internal/
│   ├── accounts/
│   ├── usage/
│   ├── delivery/
│   ├── domain/
│   └── webapi/
├── health/
└── webapi/
```

语言配置能力与本地接线说明见 [`languages/README.md`](./languages/README.md)。Session HTTP
生产装配会在同一 PostgreSQL pool 上复用真实 `languages.Service`，并通过 Session owner
校验语言配置归属；不得使用 `LANGUAGE_SESSION_OWNER=trust-auth` 作为生产替代。

会话生命周期 HTTP 在有 `DATABASE_URL` 时与语言配置共用同一 PostgreSQL pool：
`sessions` 通过 `realtimeaccess.NewLanguageConfigReader` 读取真实 `languages.Service`，
用于 Start 前双语配置校验。未设置 `REALTIME_BASE_URL` 时，Create/List/Get 可用；
Start 返回 `501 not_implemented`。End 对仍为 `created` 的会话可直接成功（无需
Realtime.Stop）；对 `active` 会话因清理依赖 Stop 仍返回 `501`，直到 realtime
control-plane 客户端接入。

WebRTC config、offer/answer 和 ICE candidate 由 `services/realtime-audio/webrtc`
统一处理。部署时可以由 API Gateway 转发 `/realtime/v1`，但本服务不实现信令逻辑。

## 模式控制 API

当 `LINGOW_SESSION_RUNTIME=enabled` 时，Session API 额外挂载：

- `GET /api/v1/voice-sessions/{id}/mode`：返回 realtime 当前权威 `ModeStateSnapshot`；
- `POST /api/v1/voice-sessions/{id}/mode`：在已认证账户拥有且处于 `active` 的 Session 上转发一次模式切换。

切换请求的 JSON 只包含 `runtime_instance_id`、`expected_generation` 和 `target_mode`。操作 ID 必须来自
`Idempotency-Key`。`X-Request-ID` 只跟踪单次 HTTP 尝试；交给 realtime 的命令追踪 ID 由 API 按
Session 和 operation ID 稳定生成，确保客户端更换请求 ID 重试时仍是同一命令。API 会先执行 Session 归属校验，再把这些字段交给 realtime
的 compare-and-switch 协调器。API 不保存 `active_mode`、不实现本地幂等缓存，也不通过 Stop/Start 模拟切换。
重复操作、代次冲突和 runtime 实例冲突均由 realtime 返回，API 只做稳定错误映射。旧的 Session state 查询
仍只读取原有媒体 RuntimeSnapshot，避免模式依赖故障改变旧接口的失败路径。

`services/realtime-audio` 提供本地可运行的 `/realtime/v1` HTTP 入口（`go run .`，默认
`:8090`）；当前使用 Pion + `localruntime` 适配器，真实 ASR/TTS pipeline 仍为后续工作。
`LINGOW_SESSION_RUNTIME` 默认 `disabled`；disabled 时 Session 路由明确返回 501，
不会构造 Realtime client、adapter 或 EndRecoveryWorker。联调完整 Start 可用仓库根目录
`start-local.bat`（同时拉起 API 与 realtime-audio），并将 API 侧设为 `enabled`。

启用 Session runtime 时需要 `DATABASE_URL`、至少 32 字节的 `JWT_SECRET`、
`REALTIME_BASE_URL` 和至少 32 字节的 `REALTIME_TICKET_SECRET`。`REALTIME_BASE_URL`
是 API 访问 `services/realtime-audio` control-plane 的 HTTP/HTTPS URL，例如
`http://127.0.0.1:8090`；可带路径以兼容 API Gateway，但不得包含用户信息、query 或
fragment。`REALTIME_HTTP_TIMEOUT` 可选，默认 `5s`，最大 `5s`。API 会使用短期 HMAC
realtime ticket 调用 WebRTC connection、Start、Stop 和 runtime state 接口，ticket secret
必须与 realtime-audio 验证端一致，不能与 JWT secret 混用或写入日志。

语义命令需要调整同传语言方向时，`services/realtime-audio` 调用 API 内部语言配置端点。两服务必须
配置相同的 `LINGOW_COMMAND_SYSTEM_TOKEN`，令牌至少 32 bytes；API 只负责持久化权威语言配置，
不接收唤醒词事件、不运行 KWS 或命令语义模型。命令幂等键在 API 内部按 `session_id + command_id`
作用域化；已经被后续配置替代的旧命令重放返回 `stale_command` 冲突，不会把历史配置当作当前配置。
Realtime 侧同时配置 `LINGOW_API_BASE_URL`。

结束会话时，本服务先幂等调用 realtime 的 `Stop`。realtime 确认 Pipeline 和 WebRTC 连接已关闭后，
本服务再把业务会话标记为 `ended`。调用失败时保持会话未结束并重试，不允许只改业务状态而遗留实时连接。

账户持久化和 HMAC Access Token 已接入生产装配；手机号验证码发送、用量消费和消息投递仍是
未完成边界。受保护路由只接受 `AccessTokenVerifier` 验证后写入 Context 的账户身份。未接入
验证码发送或真实 Email Provider 的业务方法必须返回 `not_implemented`，不得伪造成功结果。

消息投递的 Queue、Worker 和 Provider 适配器已提供可注入的运行时边界，但默认不会启动。
将 `LINGOW_DELIVERY_RUNTIME=enabled` 后，`main.go` 会在**同一 PostgreSQL pool** 上组合
records HTTP、`FinalTurnWorker`、`AuthMaintainer`、持久化账户/用量/消息服务、Outbox Dispatcher
和 Delivery Worker。未接入真实 Email Provider 的发送仍保持 fail-closed。
生产组合必须先基于最新鉴权迁移（包括账户 lineage 函数）完成，再启用异步投递。

运行时启用还要求 `DATABASE_URL`、`REDIS_URL`、至少 32 字节的 `JWT_SECRET` 和
`LINGOW_DELIVERY_DESTINATION_KEY`。enabled 路径还会启动 usage stream consumer，
并暴露 `/api/v1/account/message-targets/*`（email bind 在 local 环境支持 `dev:` token；
非 local 环境通过 `POST /api/v1/account/message-targets/email/verification-codes` 发送一次性
验证 token，再调用 bind）。`LINGOW_DELIVERY_PROVIDER` 默认是
`unconfigured`；`fake_email` 只允许本地或测试环境显式选择；生产环境使用 `smtp`
并配置 `LINGOW_SMTP_*`。WeChat Work 通道通过 `POST /api/v1/account/message-targets/wechat/bind`
绑定 OAuth code（local/test 支持 `dev:<userid>` 或 `dev:<destination_ref>:<userid>`），
出站投递在配置 `LINGOW_WECOM_*` 后由 `WeComProvider` 发送应用消息。
语言配置的单向输出只有在 delivery runtime 已启用且目标 channel provider 已配置时才会接受；
否则返回 `delivery_target_required`，保持反向译文不被静默丢弃。

## 语音记录 HTTP 装配

API 启动语音记录和 Session 路由需要 `DATABASE_URL` 和至少 32 字节的 `JWT_SECRET`。缺少配置或数据库
不可用时启动直接失败，不回退到 501 handler。启动时会应用 recordstore migration，并组装真实
PostgreSQL participant/turn repositories 与账户 session scope。

六条 records 路由统一经过 Bearer token 验证。GET 只读取当前账户拥有会话中的 final records；
客户端账户字段和 `X-Account-ID` 不参与授权。两个 AI-only PATCH 路由要求账户 Bearer token 和
`X-Lingow-System-Token` 双重认证；`LINGOW_RECORDS_SYSTEM_TOKEN` 未配置时 PATCH 保持
fail-closed（`403 forbidden`），配置后由 `SystemAuthenticate` 做常量时间比对并在成功后标记为
system actor。

带有 `provider_speaker_id` 的 pending/provisional FinalTurn 在落库同一事务内入队一个 durable
attribution task。API 启动时运行 attribution worker：worker 领取任务，按持久化的 provider key
通过账户范围的 participant service 建立稳定映射，再通过 turns service 确认或修正归属。没有
provider speaker key 的 turn 保持 pending，但不创建永远无法解析的任务，也不会伪造本地说话人；
可重试失败采用指数退避并在达到上限后停止。两个 API runtime（普通与
`LINGOW_DELIVERY_RUNTIME=enabled`）都会启动该 worker。

FinalTurn 的 `language_config_version` 是新事件的必填字段，且必须大于 0。Realtime 在每个 Turn
开始时固定语言配置版本并写入事件；API 不为缺失、零值或负值补默认版本，而是将事件拒绝为非法请求。
该字段同时用于 `voice_turns` 审计和重放一致性，归属修正不会修改它。

API 同时运行 PostgreSQL `final_turn_outbox` consumer。事件使用 `event_id` 和完整 payload hash
保证发布重放一致，worker 通过 receipt lease 领取消息：成功写入后 Ack，临时存储错误 Nack 并延迟
重试，非法事件或幂等冲突 Reject。服务关闭时停止领取新事件，并在数据库 pool 关闭前等待当前结算；
如果结算失败，进程以错误退出而不会把它伪装成正常关闭。

## 模式变更审计投影

启用 `LINGOW_SESSION_RUNTIME` 时，API 会从 `LINGOW_MODE_CHANGED_STREAM` 消费
`realtime.mode.changed`，在同一事务内写入不可变的 `realtime_mode_events` 审计事实，并更新
`realtime_mode_projections`。后者只表示 API **最新已观察**的模式，不是实时状态权威；需要当前状态时
仍必须查询 `services/realtime-audio`。

同一 `runtime_instance_id` 内仅更高 `generation` 可以推进投影；跨 runtime 以 `occurred_at` 排序，
时间相同再以稳定 `event_id` 决定结果。跨 runtime 的排序依赖主机时钟，因此部署必须保持时钟同步，
查询方也不能把该投影用于实时并发控制。相同 `event_id` 和完整契约 payload 可安全重放，异载荷冲突、
非法事件和不存在的 Session 会被确认并拒绝，临时 PostgreSQL 错误保留 pending 等待重试。

本地默认使用 `lingow:realtime:mode:changed`、`lingow-mode-projection` 和
`mode-projection-worker`；生产环境必须为每个 API 实例配置唯一的
`LINGOW_MODE_CHANGED_CONSUMER`。启用 Session runtime 后 `REDIS_URL` 为必填项。

## 语音记录存储集成测试

语音记录 migration 使用 PostgreSQL，并通过 `integration` build tag 与默认离线测试隔离。创建
名称以 `_test` 结尾的专用本地数据库后，设置其连接地址并执行：

```powershell
docker compose -f ../../infra/docker-compose.yml exec postgres createdb -U postgres lingow_records_test
$env:RECORDSTORE_TEST_DATABASE_URL = 'postgres://postgres:postgres@localhost:5432/lingow_records_test?sslmode=disable'
go test -count=1 -tags=integration . ./recordstore/...
```

Delivery 全链路集成测试（匿名鉴权 → 创建 outbound message → outbox → Valkey → worker → fake email provider）同样使用
`integration` build tag，依赖 `RECORDSTORE_TEST_DATABASE_URL` 与 miniredis（进程内，无需外部 Valkey）：

```powershell
go test -count=1 -tags=integration -run 'TestMember5DeliveryAcceptance|TestPostgresDestinationReader|TestConfiguredRuntimeComposition' . ./recordstore/...
```

测试 helper 会为每个测试创建并删除随机 schema，并拒绝连接名称不以 `_test` 结尾的数据库。主包的
生产装配测试只会从 `RECORDSTORE_TEST_DATABASE_URL` 派生隔离后的 `DATABASE_URL`，不会使用外部设置的
`DATABASE_URL`。
