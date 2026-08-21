# 生产部署

该目录提供通用 Docker Compose 主机部署。它构建并运行 Web、API 和 realtime-audio；PostgreSQL、Redis/Valkey、TLS 终止和 TURN 服务不在 Compose 中创建，必须由目标环境以私网方式提供。

Web 默认只绑定宿主机回环地址 `127.0.0.1:3000`。如需绑定其他地址，在环境文件中修改 `WEB_BIND_IP`；在其前方配置已有的 HTTPS 反向代理，将公网流量转发到该端口。WebRTC 的 ICE/TURN 配置由 `REALTIME_ICE_SERVERS_JSON` 同时提供给浏览器和 realtime 服务；生产配置必须包含可从客户端访问的 TURN/TURNS 地址，推荐使用短期凭据和 `relay` 策略。

## 首次配置

1. 在 Linux x86_64 部署主机安装 Docker Engine、Docker Compose v2 和 Bash，创建专用非 root 部署用户，并确保该用户可以使用 Docker。当前工作流发布 `linux/amd64` 镜像。
2. 从 `.env.production.example` 创建部署环境文件。实际文件只保存在 GitHub `production` Environment secret `DEPLOY_ENV_FILE` 和部署主机，不能提交到仓库。模板中的尖括号字段就是需要替换的值，具体位置见下方“占位符位置”。
3. 将 PostgreSQL 与 Redis/Valkey 地址配置为仅部署主机可访问的 TLS/认证连接。为 API 生成独立且至少 32 字节的 `JWT_SECRET`、`AUTH_PEPPER`、`REALTIME_TICKET_SECRET`、`LINGOW_DELIVERY_DESTINATION_KEY`、`LINGOW_RECORDS_SYSTEM_TOKEN` 与 `LINGOW_COMMAND_SYSTEM_TOKEN`。
4. 配置 GitHub `production` Environment，可开启所需审批。添加以下 secrets：

   - `DEPLOY_HOST`：部署主机名或 IPv4 地址。
   - `DEPLOY_USER`：部署用户。
   - `DEPLOY_SSH_PRIVATE_KEY`：该用户的专用 SSH 私钥。
   - `DEPLOY_KNOWN_HOSTS`：目标主机的已验证 SSH host key 行。
   - `DEPLOY_ENV_FILE`：将环境模板中的尖括号字段说明替换为真实配置后的完整环境文件，不包含三项 `LINGOW_*_IMAGE` 值。
   - `GHCR_PULL_TOKEN`：仅具 `packages:read` 权限、可读取三个 GHCR 镜像的访问令牌。
   - `DEPLOY_SMOKE_ACCESS_TOKEN`：用于部署后业务冒烟的专用账号访问令牌。
   - `DEPLOY_SMOKE_SESSION_ID`：该账号预先创建且保持可用的 voice session ID。

   添加 repository variables：

   - `DEPLOY_PATH`：部署用户可写的绝对目录，例如 `/srv/lingow`。
   - `GHCR_PULL_USERNAME`：创建 `GHCR_PULL_TOKEN` 的 GitHub 用户名或专用服务账号。

5. 使三个 GHCR package 对该令牌或部署组织可读。若将 package 设为 public，可移除主机 GHCR 登录步骤及对应 secret。

## 占位符位置

- `.env.production.example` 的镜像字段 `LINGOW_API_IMAGE`、`LINGOW_REALTIME_AUDIO_IMAGE`、`LINGOW_WEB_IMAGE` 使用 `<GitHub owner>` 和 `<commit SHA>`。这三项由 GitHub Actions 自动写入部署文件，不要放入 `DEPLOY_ENV_FILE`。
- `.env.production.example` 的 `DATABASE_URL`、`REDIS_URL`、六项系统密钥、三项 consumer 名称，以及短信、SMTP、企业微信、ASR/LLM/TTS/command provider 字段中的 `<...>`，都要替换为目标生产环境的真实配置。`VERIFICATION_SENDER` 必须为 `http`，短信 endpoint 必须使用 HTTPS；该 endpoint 接收 `POST` JSON `{ "phone": "...", "code": "..." }`，token（如配置）通过 Bearer 头发送。
- `REALTIME_ICE_SERVERS_JSON` 中的 TURN 主机、临时用户名和临时凭据必须由 TURN 服务提供；不要提交长期凭据。`REALTIME_ICE_TRANSPORT_POLICY=relay` 会强制媒体走 TURN。
- `.env.production.example` 的 `WEB_BIND_IP` 和 `WEB_PORT` 不使用尖括号；默认值分别为 `127.0.0.1` 和 `3000`，需要改变监听地址或端口时直接修改对应值。
- `docker-compose.yml` 不应填写尖括号文本。它只通过 `${VARIABLE}` 读取环境文件；带 `:?` 的变量为必填项，带 `:-` 的变量使用默认值。Web 的宿主机绑定由 `WEB_BIND_IP` 与 `WEB_PORT` 控制。
- 本 README 中的 `/srv/lingow` 只是命令示例，不是需要填入环境文件的尖括号字段；执行回滚命令时将其替换为实际 `DEPLOY_PATH`。

## 发布与回滚

`.github/workflows/deploy-production.yml` 只在 `main` 分支执行。工作流构建三个不可变的 commit-SHA 镜像、上传 Compose 与环境文件，并通过 SSH 执行 `scripts/deploy.sh`。脚本会先校验 Compose 插值，再拉取镜像并等待所有 health check 成功；失败时恢复上一次成功发布的 Compose、环境文件和应用容器。数据库迁移是向前兼容、不会自动回滚，回退前必须确认 schema 仍兼容旧版本。

回滚时，把上一成功部署的三个 SHA 镜像值写入部署主机的 `.env.production`，再执行：

```bash
bash /srv/lingow/deploy.sh /srv/lingow /srv/lingow/.env.production
```

将 `/srv/lingow` 替换为实际 `DEPLOY_PATH`。不要使用可变 `latest` 标签。

## 本地验收

填写不含真实生产凭据的环境文件后，可在具备可访问依赖的 Linux Docker 主机执行：

```bash
docker compose --env-file .env.production -f docker-compose.yml config --quiet
docker compose --env-file .env.production -f docker-compose.yml up --detach --wait
```

工作流随后执行 `scripts/deploy-smoke.sh`：验证 API/realtime 健康、用专用访问令牌获取 realtime ticket，并用该 ticket 获取 WebRTC 配置（包括 TURN 配置）。这不替代真实浏览器媒体通话和收费 provider 调用；两者仍应在发布窗口执行一次人工验收。
