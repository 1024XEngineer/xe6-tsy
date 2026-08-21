# Lingow
[![Coverage](https://img.shields.io/endpoint?url=https%3A%2F%2F1024xengineer.github.io%2Fxe6-tsy%2Fcoverage.json)](https://github.com/1024xengineer/xe6-tsy/actions/workflows/go.yml)
![GitHub Actions Workflow Status](https://img.shields.io/github/actions/workflow/status/1024XEngineer/xe6-tsy/go.yml)
[![Github repo](https://img.shields.io/badge/github-repo-blue?logo=github)](https://github.com/1024XEngineer/xe6-tsy)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/1024XEngineer/xe6-tsy?filename=services%2Fapi%2Fgo.mod)
[![GitHub stars](https://img.shields.io/github/stars/1024XEngineer/xe6-tsy?style=social)](https://github.com/1024XEngineer/xe6-tsy)

Lingow 是面向 Web、移动端和硬件设备的 AI 语音助手与面对面同传系统。当前 `dev` 分支以
`assistant` 和 `interpretation` 两种后端权威模式复用同一条 WebRTC 会话：客户端只负责采集、
播放和交互，业务会话由 API 控制面管理，实时音频与运行状态由 realtime-audio 媒体面管理。

## 核心能力

- 助手模式：客户端唤醒词检测、自然语言语义命令、助手问答与模式切换。
- 同传模式：双语配置、自动语言识别、流式 ASR、翻译、句末 TTS 和抢话打断。
- 实时链路：WebRTC 音频、可靠有序 DataChannel、运行状态与模式快照、弱网重连边界。
- 记录与归属：Final Turn 持久化、临时说话人、后续归属修正和历史查询。
- 账户与设备：匿名/注册账户、短期访问令牌、Ed25519 设备配对与受限设备会话。
- 可选投递：用量事件、Email/企业微信消息，以及长句企业微信字幕与失败 TTS 回放。
- 可观测性：结构化日志、模式和延迟指标，以及固定节点集合下的会话哈希网关示例。

长句投递和短语字幕默认关闭。启用长句投递后，去除首尾空白的原文超过 50 个 Unicode 字符，
或原声音频达到 20 秒时，译文跳过初始 TTS 并投递到企业微信；目标不可用或最终投递失败时回放 TTS。

## 架构

| 模块 | 职责 | 当前形态 |
| --- | --- | --- |
| `apps/web` | 会话、语言设置、WebRTC、字幕、助手回复和 TTS 交互 | Next.js 16 / React 19 |
| `apps/mobile` | 移动端控制面状态、模式命令和重连核心 | TypeScript 库，尚未绑定 UI 与 WebRTC |
| `services/api` | 账户、会话、语言配置、记录、用量、投递和设备身份 | Go 控制面服务，默认 `:8080` |
| `services/realtime-audio` | WebRTC、VAD、ASR、翻译、TTS、命令与运行状态机 | Go 媒体面服务，默认 `:8090` |
| `packages/contracts` | REST、实时事件、错误码和跨端类型 | OpenAPI / AsyncAPI / Go / TypeScript |
| `sdks/device` | 设备鉴权、会话、模式、唤醒事件和重连参考实现 | Go 控制核心，媒体与 KWS 由平台适配 |
| `infra` | PostgreSQL、Redis/Valkey 和可选 realtime 网关 | Docker Compose / Nginx |

```text
Web / Mobile / Device
  -> services/api: account / session / language config / realtime ticket
  -> services/realtime-audio: WebRTC signaling / audio / control events
  -> VAD -> ASR -> translation -> TTS or message delivery
  -> services/api: Final Turn / usage / history / asynchronous messages
```

跨模块数据必须先在 `packages/contracts` 定义。`services/api` 拥有长期业务状态，
`services/realtime-audio` 是实时连接、播放和模式状态的事实来源。

## 当前边界

- Web 是当前可运行的主要联调入口；本地 KWS 使用 sherpa-onnx，固定唤醒词为“小灵小灵”。
- Mobile 当前只有可编译、可测试的控制面核心，不包含 UI、PeerConnection 或原生 KWS。
- Device SDK 提供控制核心和接口边界，不包含具体芯片的音频 HAL、WebRTC 或 KWS 模型。
- 默认 mock Provider 可离线验证普通音频编排，但不能验证真实语音命令；真实命令需要 ASR 和 Qwen 语义解释器配置。
- `REALTIME_TTS_DOWNLINK=pcm` 可向浏览器发送 PCM；Opus 下行编码仍是待完成边界。
- 当前不提供管理后台、订单、支付、发票、多人会议同传或硬件制造能力。

## 本地启动

建议准备 Go 1.26、Node.js 22、npm，以及 PostgreSQL 16 和 Redis/Valkey 7。需要容器化依赖时安装
Docker Desktop。

1. 复制根配置并填写本地凭证：

```bash
cp .env.example .env
```

完整会话联调至少需要设置：

```dotenv
LINGOW_SESSION_RUNTIME=enabled
REALTIME_TICKET_SECRET=<至少 32 字节，API 与 realtime 共用>
LINGOW_COMMAND_SYSTEM_TOKEN=<至少 32 字节，API 与 realtime 共用>
COMMAND_LLM_API_KEY=<Qwen API key>
COMMAND_LLM_BASE_URL=<Qwen compatible API base URL>
```

真实麦克风语义命令还需要配置真实 ASR；`ASR_PROVIDER=mock` 只产生固定离线文本。全部变量和可选
delivery、TTS、数据库配置见 [`.env.example`](./.env.example)。

2. Windows 可从仓库根目录启动 API 与 realtime-audio：

```powershell
.\start-local.ps1 -UseDocker
```

不传 `-UseDocker` 时脚本优先使用 `.env` 中可访问的本地 PostgreSQL 和 Redis，并在不可访问时询问
是否启动 Docker Compose。也可运行 `start-local.bat`，或通过 `-Service api|realtime` 单独启动服务。

3. 启动 Web：

```bash
cd apps/web
cp .env.example .env.local
npm install
npm run dev
```

打开 [http://localhost:3000](http://localhost:3000)。Web 默认以助手模式创建新会话；设置
`NEXT_PUBLIC_LINGOW_INITIAL_MODE=interpretation` 可回退为同传入口。

非 Windows 环境可以手动启动依赖和两个 Go 服务。`go run` 不会自动读取根 `.env`，请先把变量导入
当前 shell，再在两个终端分别运行服务：

```bash
set -a
. ./.env
set +a

docker compose -f infra/docker-compose.yml up -d
```

API 终端：

```bash
(cd services/api && go run .)
```

Realtime 终端：

```bash
(cd services/realtime-audio && go run .)
```

默认端口：

| 服务 | 地址 |
| --- | --- |
| Web | `http://localhost:3000` |
| API | `http://localhost:8080` |
| Realtime Audio | `http://localhost:8090` |
| PostgreSQL | `localhost:5432` |
| Redis/Valkey | `localhost:6379` |

## 验证

Go 工作区：

```bash
go test ./packages/contracts/... ./services/api/... ./services/realtime-audio/... ./sdks/device/...
```

Web：

```bash
cd apps/web
npm run lint
npm run typecheck
npm test
npm run build
```

Mobile 控制核心：

```bash
cd apps/mobile
npm install
npm test
npm run typecheck
npm run build
```

## 文档

- [Lingow 架构总览](https://github.com/1024XEngineer/xe6-tsy/pull/165)
- [开发说明](docs/DEVELOPMENT.md)
- [Lingow 模块详细设计](https://github.com/1024XEngineer/xe6-tsy/pull/169)
- [Lingow P0 协议设计](https://github.com/1024XEngineer/xe6-tsy/pull/171)
