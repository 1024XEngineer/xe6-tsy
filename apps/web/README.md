# apps/web

Lingow Web 对话入口（联调/验收前端）。

当前实现来自 realtime mock 联调页：匿名鉴权、voice-sessions、语言配置、API 签发 realtime ticket、WebRTC、字幕、助手回复与 TTS 播放。

## 技术栈

- TypeScript
- Next.js 16（App Router）
- React 19
- Vitest / Playwright

> 仓库早期文档曾规划 Vue 3 + Vite；本目录以现网可跑的 Next.js 联调前端为准。

## 本地启动

先在仓库根目录启动 API（`:8080`）与 realtime-audio（`:8090`），例如：

```powershell
.\start-local.ps1
```

再启动本前端：

```bash
cd apps/web
cp .env.example .env.local
npm install
npm run dev
```

打开 [http://localhost:3000](http://localhost:3000)。Windows 也可：`.\start-windows.ps1`。

## 环境变量

见 [CONFIG.md](./CONFIG.md)。Next 会把浏览器请求代理到后端：

- `/api/v1/*` → `LINGOW_API_BASE_URL`（默认 `http://127.0.0.1:8080`）
- `/realtime/v1/*` → `LINGOW_REALTIME_BASE_URL`（默认 `http://127.0.0.1:8090`）
- `NEXT_PUBLIC_LINGOW_INITIAL_MODE` → 新 Web 会话入口模式，默认 `assistant`；设为 `interpretation` 可快速回退

正式联调走 `POST /api/v1/voice-sessions/{id}/realtime-ticket`。本地 `/api/dev/realtime-ticket` 旁路默认关闭（需 `ENABLE_DEV_REALTIME_TICKET=true` + `next dev`）。

## 脚本

| 命令 | 说明 |
| --- | --- |
| `npm run dev` | 开发服务器 |
| `npm run test` | Vitest |
| `npm run typecheck` | TypeScript |
| `npm run test:e2e` | Playwright |
| `npm run lint` | ESLint |
| `npm run sync-kws-models` | 手动同步 KWS 模型/WASM（通常不必；`dev`/`build`/`postinstall` 会自动跑） |

## 语音唤醒

点击主按钮启动会话时，页面会请求一次麦克风权限并加载同域 sherpa-onnx KWS；
空闲页面不占用麦克风。

- 点击主按钮 → 开启助手入口（WebRTC + `/start`）；按钮仍可手动结束当前会话
- 会话中只用固定唤醒词「小灵小灵」打开服务端命令窗口；后续自然语言由 Command ASR 和语义解释器处理
- Web 的 sherpa-onnx 模型只属于浏览器本地实现；ESP32-S3 等设备可替换为自己的板载 KWS 模型

活动会话可选择两种客户端交互策略，选择保存在浏览器本地：

- `常驻模式`：保持 WebRTC 上行开启，当前业务模式持续接收普通语音。
- `唤醒词模式`：只让本地 KWS 持续工作，WebRTC 上行默认关闭；命中「小灵小灵」且
  `wake_word.detected` 发送成功后才开放一轮语音。浏览器从 2.5 秒本地环形缓存中选择最近静音边界，
  最多补发 2 秒完整唤醒与命令开头，再衔接实时音频；服务端最多等待 5 秒首段指令语音，收到匹配的
  `command.result` 或 15 秒兜底超时后关闭。

`唤醒词模式` 只适用于 AI 助手。同声传译必须持续接收双方语音，因此进入
`interpretation` 后前端强制采用常驻上行，但本地 KWS 不停止；此时说「小灵小灵，结束同声传译」
仍会进入通用语义命令入口。切回助手后恢复用户原先保存的监听策略。

交互策略不属于 realtime 的业务 Mode，不会切换 `assistant` / `interpretation`，也不会重建
PeerConnection。唤醒后的自然语言既可以是模式指令，也可以是普通助手问题；普通问题通过
`assistant_query` 复用现有 Assistant Handler。为避免助手回答与译文混流，普通问题只在当前
`assistant` 模式执行，同传期间需要先明确切回助手模式。

阶段 14 的 Web 端会在 realtime 暴露模式快照时展示连接、RuntimeState 和
ModeState，并通过带 `runtime_instance_id`、`expected_generation` 的类型化请求切换
`assistant` / `interpretation`。发生 generation 或 runtime instance 冲突时只刷新快照，
不会自动重放旧命令。Web 的“小灵小灵”只发送类型化 `wake_word.detected`，不在本地判断
start/stop、模式或语言方向；realtime 的 Command Gate、Command ASR、语义解释器和 Mode
Coordinator 负责后续执行。连接断开也不会自动创建第二条 PeerConnection。
所有客户端统一发送同一个 `wake_word.detected` 契约；模型文件、阈值和推理运行时由客户端负责，
后端不接收 KWS 音频流或模型。设备接入字段和重试规则见 `docs/DEVICE_KWS_INTEGRATION.md`。

`npm install` / `npm run dev` / `npm run build` 会自动把缺失的 int8 模型与 `.wasm` 拉到 `public/kws/`（已存在则跳过）。首次需要能访问 GitHub Releases 与 jsDelivr；离线时可设 `LINGOW_SKIP_KWS_SYNC=1`，让下载失败不阻断命令。详见 `public/kws/README.md`。

## 职责边界

- 负责：产品交互、会话 API 调用、语言输出模式切换、WebRTC 接入、字幕/TTS 展示
- 不负责：实时音频编排、ASR/翻译/TTS 供应商、硬件采集

实时音频编排仍由 `services/realtime-audio` 负责。

语言设置支持双向播报和单向输出。单向输出只播报当前源语言的译文，反向译文自动投递并保留 Final Turn；活动会话切换后从下一句开始生效，配置更新使用语言配置版本进行并发保护。
