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

打开页面后会请求麦克风并加载同域 sherpa-onnx KWS。

- 说「小灵，开始翻译」或点击主按钮 → 开启助手入口（WebRTC + `/start`）；回退为 `interpretation` 时继续进入传译
- 说「小灵，停止翻译」或再次点击 → 结束当前会话，麦克风继续监听唤醒词

`npm install` / `npm run dev` / `npm run build` 会自动把缺失的 int8 模型与 `.wasm` 拉到 `public/kws/`（已存在则跳过）。首次需要能访问 GitHub Releases 与 jsDelivr；离线时可设 `LINGOW_SKIP_KWS_SYNC=1`，让下载失败不阻断命令。详见 `public/kws/README.md`。

## 职责边界

- 负责：产品交互、会话 API 调用、语言输出模式切换、WebRTC 接入、字幕/TTS 展示
- 不负责：实时音频编排、ASR/翻译/TTS 供应商、硬件采集

实时音频编排仍由 `services/realtime-audio` 负责。

语言设置支持双向播报和单向输出。单向输出只播报当前源语言的译文，反向译文自动投递并保留 Final Turn；活动会话切换后从下一句开始生效，配置更新使用语言配置版本进行并发保护。
