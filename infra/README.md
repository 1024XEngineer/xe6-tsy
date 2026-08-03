# infra

本地和部署配置目录。

## 当前内容

- `docker-compose.yml`：PostgreSQL 16 与 Redis/Valkey 7，供 API 与 realtime-audio 本地联调。

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

5. 在 `services/api` 目录启动 API；enabled 路径会同时监督 delivery outbox/worker、usage stream consumer、records FinalTurnWorker。

## 后续

- API / realtime-audio 独立部署 manifest
- 生产环境密钥与 Valkey consumer 命名规范
