# 开发说明

## 1. 本地依赖

建议版本：

- Node.js 22 LTS
- pnpm 9+
- Go 1.25+
- Docker Desktop
- PostgreSQL 16
- Redis 7

## 2. 本地启动

```bash
pnpm install
docker compose -f infra/docker-compose.yml up -d

pnpm --filter web dev
pnpm --filter mobile dev

cd services/api && go run .
cd services/realtime-audio && go run .
```

首个实现 PR 需要补齐 `pnpm-workspace.yaml`、各 app 的 `package.json`、Go `go.mod` / `go.work` 和 `infra/docker-compose.yml`。

## 3. 环境变量

根目录提供 `.env.example`，各服务只读取自己需要的变量。

建议变量：

```bash
APP_ENV=local
API_ADDR=:8080
REALTIME_ADDR=:8090
LINGOW_SESSION_RUNTIME=disabled
REALTIME_BASE_URL=http://127.0.0.1:8090
REALTIME_TICKET_SECRET=
REALTIME_HTTP_TIMEOUT=5s
DATABASE_URL=postgres://postgres:postgres@localhost:5432/lingow?sslmode=disable
REDIS_URL=redis://localhost:6379/0
JWT_SECRET=local-dev-only-secret-change-me-1234
ASR_PROVIDER=mock
LLM_PROVIDER=mock
TTS_PROVIDER=mock
```

本地默认使用 mock ASR/翻译/TTS，避免开发阶段被第三方额度和网络状态阻塞。

## 4. Go 开发规范

- Go 服务按业务域拆包，不按 `controller/service/repository` 分层。
- `main.go` 只做配置、依赖注入和启动，不写业务逻辑。
- 每个 goroutine 必须有退出条件，通常由 `context.Context` 控制。
- HTTP 服务必须设置 read/write/idle timeout。
- 标准库优先：路由优先使用 Go 1.22+ `net/http` ServeMux。
- 日志使用 `log/slog`，不要在业务包里使用全局 logger。
- 错误返回时加上下文，边界处统一记录日志。
- 测试优先写 table-driven tests，少用重型 mock。

建议 Go 包边界：

```text
services/api/
├── main.go
├── config/
├── auth/
├── devices/
├── sessions/
├── realtimeaccess/
└── webapi/

services/realtime-audio/
├── main.go
├── config/
├── webrtc/
├── audio/
├── vad/
├── segment/
├── asr/
├── translate/
├── tts/
├── playback/
└── session/
```

## 5. TypeScript 开发规范

- Web 和 Mobile 都使用 TypeScript strict mode。
- API 类型从 `packages/contracts` 生成，不手写重复 DTO。
- UI 组件只处理展示和交互，不直接拼实时音频协议。
- 业务调用统一放在 `features/*/api.ts` 或 `lib/api/*`。
- 状态管理先用 Vue 组件局部状态和少量共享 store，避免过早引入复杂全局状态。

建议前端结构：

```text
apps/web/
├── app/
├── features/
│   ├── conversation/
│   ├── language/
│   └── sessions/
├── components/
└── lib/

apps/mobile/
├── app/
├── features/
│   ├── conversation/
│   ├── language/
│   └── diagnostics/
├── components/
└── lib/
```

## 6. 协议规范

实时音频首期用 WebRTC 传输音频。信令优先走 HTTP，由 `services/realtime-audio`
负责 WebRTC config、offer/answer、ICE candidate 和 PeerConnection 生命周期；
`services/api` 负责业务会话、语言配置、短期实时连接票据和状态快照。音频媒体流不走 WebSocket。

- 媒体通道：WebRTC audio track
- 控制事件：WebRTC data channel，或通过 `services/realtime-audio` 的 HTTP 实时接口传递
- API 职责：创建业务会话、校验会话归属、签发短期实时连接票据
- Realtime 职责：交换 offer/answer、交换 ICE candidate、绑定 PeerConnection 与会话
- 结束职责：API 幂等调用 realtime `Stop`；realtime 关闭 Pipeline 和 WebRTC 连接后，API 才写入 `ended`
- 失败处理：`Stop` 失败时 API 保持可重试状态，客户端关闭本地连接，realtime 通过租约或空闲超时兜底清理
- 音频编码：优先 Opus；如硬件仅支持 PCM，则由 SDK 或边缘适配层转码

核心事件：

```text
session.start
language.selected
wakeup.detected
webrtc.connected
asr.partial
asr.final
translation.final
tts.ready
playback.start
playback.stop
session.end
error
```

WebRTC 连接和运行时状态机由 `services/realtime-audio` 维护。`services/api` 只暴露业务会话、语言配置、实时连接票据和状态快照查询，不交换 SDP/ICE，也不重复维护播放状态机。

## 7. 模型服务适配规范

ASR、翻译和 TTS 都通过 provider 适配层接入，首期默认使用 mock provider。

```text
services/realtime-audio/
├── asr/          # 输入音频片段，输出 partial/final 识别结果
├── translate/    # 输入 final 原文、语言方向和上下文，输出 final 译文
├── tts/          # 输入 final 译文和语言配置，输出可播放音频或播放资源
└── pipeline/     # 编排 ASR -> 翻译 -> TTS，不写供应商细节
```

规则：

- provider 接口只放在调用方需要的最小方法，不暴露供应商 SDK 类型。
- 每次外部模型调用必须接收 `context.Context`，并设置超时。
- 错误统一映射为 contracts 中的错误码，例如 `asr_unavailable`、`translation_timeout`、`tts_failed`。
- mock provider 必须能跑通本地端到端链路。
- 替换供应商时只新增 provider 实现，不改 Web、Mobile、API 和状态机协议。

## 8. 验收标准

首期验收不追求全功能，先验收链路：

- 中英互译能完成一轮面对面对话。
- 说完一句后才播音，不提前播放 partial 译文。
- 能展示 ASR partial 和 final。
- 能记录原文、译文、时间戳和方向。
- TTS 播放时，如果对方开始说话，能停止播放或进入打断状态。
- Web/移动端能看到会话状态、语言选择、最新字幕预览和详情入口。
- Mobile demo 能模拟设备侧接入。
