# 联调配置（apps/web → xe6-tsy 后端）

```bash
cp .env.example .env.local
```

## 前端环境变量（`.env.local`）

| 变量 | 说明 |
| --- | --- |
| `LINGOW_API_BASE_URL` | API 地址，默认 `http://127.0.0.1:8080` |
| `LINGOW_REALTIME_BASE_URL` | Realtime 控制面，默认 `http://127.0.0.1:8090` |
| `ENABLE_DEV_REALTIME_TICKET` | 仅本地 `next dev`：设为 `true` 才开放 `/api/dev/realtime-ticket`（默认关闭） |
| `REALTIME_TICKET_SECRET` | 仅上述旁路需要；与仓库根 `.env` 一致（≥32 字节） |

Next 代理：

- `/api/v1/*` → `LINGOW_API_BASE_URL`
- `/realtime/v1/*` → `LINGOW_REALTIME_BASE_URL`

正式联调走 `POST /api/v1/voice-sessions/{id}/realtime-ticket`。`/api/dev/realtime-ticket` 默认关闭，需同时满足 `NODE_ENV=development` 与 `ENABLE_DEV_REALTIME_TICKET=true`。

## 后端启动

在仓库根目录：

```powershell
.\start-local.ps1
# 或分别启动 services/api (:8080) 与 services/realtime-audio (:8090)
```

联调至少开启：

```
LINGOW_SESSION_RUNTIME=enabled
REALTIME_BASE_URL=http://127.0.0.1:8090
REALTIME_TICKET_SECRET=<与 apps/web .env.local 一致，≥32 字节>
```

可选：

```
REALTIME_API_DATABASE=enabled
REALTIME_TTS_DOWNLINK=pcm
```
