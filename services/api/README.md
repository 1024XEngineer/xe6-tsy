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

语言配置能力与本地接线说明见 [`languages/README.md`](./languages/README.md)。

WebRTC config、offer/answer 和 ICE candidate 由 `services/realtime-audio/webrtc`
统一处理。部署时可以由 API Gateway 转发 `/realtime/v1`，但本服务不实现信令逻辑。

结束会话时，本服务先幂等调用 realtime 的 `Stop`。realtime 确认 Pipeline 和 WebRTC 连接已关闭后，
本服务再把业务会话标记为 `ended`。调用失败时保持会话未结束并重试，不允许只改业务状态而遗留实时连接。

账户持久化和 HMAC Access Token 已接入生产装配；手机号验证码发送、用量消费和消息投递仍是
未完成边界。受保护路由只接受 `AccessTokenVerifier` 验证后写入 Context 的账户身份。未接入
验证码发送、Queue 或 Email Provider 的业务方法必须返回 `not_implemented`，不得伪造成功结果。

## 语音记录 HTTP 装配

API 启动语音记录路由需要 `DATABASE_URL` 和至少 32 字节的 `JWT_SECRET`。缺少配置或数据库
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

测试 helper 会为每个测试创建并删除随机 schema，并拒绝连接名称不以 `_test` 结尾的数据库。主包的
生产装配测试只会从 `RECORDSTORE_TEST_DATABASE_URL` 派生隔离后的 `DATABASE_URL`，不会使用外部设置的
`DATABASE_URL`。
