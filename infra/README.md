# infra

本地和部署配置目录。

## 当前内容

- `docker-compose.yml`：PostgreSQL 16 与 Redis/Valkey 7，供 API 与 realtime-audio 本地联调。
- `realtime-gateway/nginx.conf`：可选的双 realtime 实例会话哈希网关。

## 本地启动（Member5 控制面）

1. 启动依赖：

```bash
docker compose -f infra/docker-compose.yml up -d
```

2. 复制根目录 `.env.example` 为 `.env`，至少设置：

- `DATABASE_URL`
- `REDIS_URL`
- `JWT_SECRET`（≥ 32 字节）
- `LINGOW_DELIVERY_RUNTIME=enabled`（若需 delivery + usage consumer + message-targets）
- `LINGOW_DELIVERY_DESTINATION_KEY`（32 字节 base64url）

3. Email destination 绑定：

- **local/test**：dev token 直接 bind

```text
POST /api/v1/account/message-targets/email/bind
{"token":"dev:primary-email:user@example.test"}
```

- **非 local**：先请求验证邮件，再用邮件中的 token bind

```text
POST /api/v1/account/message-targets/email/verification-codes
{"email":"user@example.test","destination_ref":"primary-email"}

POST /api/v1/account/message-targets/email/bind
{"token":"<token-from-email>"}
```

4. 生产 email 发送配置 `LINGOW_DELIVERY_PROVIDER=smtp` 与 `LINGOW_SMTP_*`（本地可用 MailHog：`host=localhost port=1025`）。

5. 在 `services/api` 目录启动 API；enabled 路径会同时监督 delivery outbox/worker、usage stream consumer、records FinalTurnWorker 与 AttributionWorker。

## 最小 realtime 多实例

该模式保持 realtime 节点集合固定，由 Gateway 从
`/realtime/v1/sessions/{session_id}/...` 提取会话 ID 并进行一致性哈希。同一会话的 WebRTC
信令、生命周期和模式控制请求会到达同一个进程；随机会话 ID 会在两个进程间分布。

先在两个终端中使用完全相同的环境配置启动 realtime，仅覆盖监听端口：

```bash
cd services/realtime-audio
REALTIME_ADDR=:8091 go run .
```

```bash
cd services/realtime-audio
REALTIME_ADDR=:8092 go run .
```

然后启动 Gateway，并让 API 和 Web 的 realtime 地址继续使用 `http://127.0.0.1:8090`：

```bash
docker compose -f infra/docker-compose.yml --profile realtime-multi up -d realtime-gateway
infra/realtime-gateway/smoke-test.sh
```

两个进程必须共享 `REALTIME_TICKET_SECRET`、Provider 配置、数据库和 Valkey 配置。Gateway 只接受
包含 Session ID 的 `/realtime/v1/sessions/...` 路径；进程级 `/metrics` 应直接从 `8091` 和 `8092`
分别采集。

这是固定节点集合下的会话级负载分担，不提供容量感知、节点自动注册、故障迁移或活跃会话重平衡。
已有会话期间不得修改 Gateway upstream 列表。节点不可达时 Gateway 返回失败，不把该会话重试到
另一个节点；客户端需要重新创建会话。Pion 媒体不会经过 HTTP Gateway，本地两个进程会分别发布
宿主机 ICE candidate；跨主机部署仍需为每个节点提供客户端可达的 candidate 或 TURN。

## 后续

- API / realtime-audio 独立部署 manifest
- 生产环境密钥与 Valkey consumer 命名规范
