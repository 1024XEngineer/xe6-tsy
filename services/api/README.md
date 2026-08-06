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

## 语音记录 HTTP 装配

API 启动语音记录和 Session 路由需要 `DATABASE_URL` 和至少 32 字节的 `JWT_SECRET`。缺少配置或数据库
不可用时启动直接失败，不回退到 501 handler。启动时会应用 recordstore migration，并组装真实
PostgreSQL participant/turn repositories 与账户 session scope。

六条 records 路由统一经过 Bearer token 验证。GET 只读取当前账户拥有会话中的 final records；
客户端账户字段和 `X-Account-ID` 不参与授权。当前仓库尚无可信 system actor 凭据契约，因此两个
AI-only PATCH 路由在生产装配中保持 fail-closed，普通 Access Token 返回 `403 forbidden`。

API 同时运行 PostgreSQL `final_turn_outbox` consumer。事件使用 `event_id` 和完整 payload hash
保证发布重放一致，worker 通过 receipt lease 领取消息：成功写入后 Ack，临时存储错误 Nack 并延迟
重试，非法事件或幂等冲突 Reject。服务关闭时停止领取新事件，并在数据库 pool 关闭前等待当前结算；
如果结算失败，进程以错误退出而不会把它伪装成正常关闭。

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
