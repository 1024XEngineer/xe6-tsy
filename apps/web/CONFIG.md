# 联调配置（apps/web → xe6-tsy 后端）

```bash
cp .env.example .env.local
```

## 前端环境变量（`.env.local`）

| 变量 | 说明 |
| --- | --- |
| `LINGOW_API_BASE_URL` | API 地址，默认 `http://127.0.0.1:8080` |
| `LINGOW_REALTIME_BASE_URL` | Realtime 控制面，默认 `http://127.0.0.1:8090` |
| `REALTIME_TICKET_SECRET` | 与仓库根目录 `.env` 的 `REALTIME_TICKET_SECRET` 相同（≥32 字节）。仅供 Next `/api/dev/realtime-ticket` 本地签发旁路 |

Next 代理：

- `/api/v1/*` → `LINGOW_API_BASE_URL`
- `/realtime/v1/*` → `LINGOW_REALTIME_BASE_URL`
- `/api/dev/realtime-ticket` 留在 Next 本机（联调旁路；产品路径用 API `.../realtime-ticket`）

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
