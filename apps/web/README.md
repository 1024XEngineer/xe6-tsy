# apps/web

Lingow Web 对话入口（联调/验收前端）。

当前实现来自 realtime mock 联调页：匿名鉴权、voice-sessions、语言配置、API 签发 realtime ticket、WebRTC、字幕与 TTS 播放。

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

正式联调走 `POST /api/v1/voice-sessions/{id}/realtime-ticket`。本地 `/api/dev/realtime-ticket` 旁路默认关闭（需 `ENABLE_DEV_REALTIME_TICKET=true` + `next dev`）。

## 脚本

| 命令 | 说明 |
| --- | --- |
| `npm run dev` | 开发服务器 |
| `npm run test` | Vitest |
| `npm run typecheck` | TypeScript |
| `npm run test:e2e` | Playwright |
| `npm run lint` | ESLint |
| `npm run sync-kws-models` | 同步 sherpa-onnx 中文唤醒词 int8 模型到 `public/kws/` |

## 语音唤醒

打开页面后会请求麦克风并加载本地 sherpa-onnx KWS（WASM 来自 jsDelivr）。

- 说「小灵，开始翻译」或点击主按钮 → 开启传译（WebRTC + `/start`）
- 说「小灵，停止翻译」或再次点击 → 结束传译，麦克风继续监听唤醒词

需先执行 `npm run sync-kws-models`（同步 int8 模型 + 同域 WASM 到 `public/kws/`）。Edge 等浏览器会对 jsDelivr 触发 Tracking Prevention，因此 WASM **必须同域托管**，不要改回 CDN。详见 `public/kws/README.md`。

## 职责边界

- 负责：产品交互、会话 API 调用、WebRTC 接入、字幕/TTS 展示
- 不负责：实时音频编排、ASR/翻译/TTS 供应商、硬件采集

实时音频编排仍由 `services/realtime-audio` 负责。
