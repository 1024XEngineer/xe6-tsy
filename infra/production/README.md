# 生产部署

该目录提供通用 Docker Compose 主机部署。它构建并运行 Web、API 和 realtime-audio；PostgreSQL、Redis/Valkey、TLS 终止和 TURN 服务不在 Compose 中创建，必须由目标环境以私网方式提供。

Compose 默认使用 `APP_ENV=staging`，便于在没有外部供应商凭证时进行虚拟联调。此模式配合
`VERIFICATION_SENDER=log`、`VERIFICATION_UNIVERSAL_CODE=8888`、
`LINGOW_DELIVERY_PROVIDER=fake_email` 以及 `ASR_PROVIDER`/`LLM_PROVIDER`/`TTS_PROVIDER=mock`；
SMTP 和企业微信配置留空即禁用真实出站调用。正式生产发布必须显式设置 `APP_ENV=production`，并改用
真实短信、SMTP、企业微信和模型配置。

Web 默认只绑定宿主机回环地址 `127.0.0.1:3000`。部署工作流同时维护 `proxy/` 下的 Nginx 配置；代理必须使用部署主机上的 `${DEPLOY_PATH}/proxy/certs/tls.crt` 和 `tls.key`，并将 80/443 端口交给该代理。代理通过 Docker DNS 动态解析 `web`、`api` 和 `realtime-audio`，应用容器重建后无需手工重启代理。WebRTC 的 ICE/TURN 配置由 `REALTIME_ICE_SERVERS_JSON` 同时提供给浏览器和 realtime 服务；生产配置必须包含可从客户端访问的 TURN/TURNS 地址，推荐使用短期凭据和 `relay` 策略。

## 首次配置

1. 在 Linux x86_64 部署主机安装 Docker Engine、Docker Compose v2 和 Bash，创建专用非 root 部署用户，并确保该用户可以使用 Docker。当前工作流发布 `linux/amd64` 镜像。
2. 为 `${DEPLOY_PATH}/proxy/certs/` 准备证书文件 `tls.crt` 和 `tls.key`，并限制为部署用户可读。开发环境可以使用自签名证书；浏览器冒烟会忽略证书校验，正式环境应使用受信任证书。
3. 从 `.env.production.example` 创建部署环境文件。实际文件只保存在 GitHub 对应 Environment（`development` 或 `production`）的 `DEPLOY_ENV_FILE` secret 和部署主机，不能提交到仓库。模板中的尖括号字段就是需要替换的值，具体位置见下方“占位符位置”。
4. 将 PostgreSQL 与 Redis/Valkey 地址配置为仅部署主机可访问的 TLS/认证连接。为 API 生成独立且至少 32 字节的 `JWT_SECRET`、`AUTH_PEPPER`、`REALTIME_TICKET_SECRET`、`LINGOW_DELIVERY_DESTINATION_KEY`、`LINGOW_RECORDS_SYSTEM_TOKEN` 与 `LINGOW_COMMAND_SYSTEM_TOKEN`。
5. 配置 GitHub `development`/`production` Environment，可为 production 开启 required reviewers。分别添加以下 secrets：

   - `DEPLOY_HOST`：部署主机名或 IPv4 地址。
   - `DEPLOY_USER`：部署用户。
   - `DEPLOY_SSH_PRIVATE_KEY`：该用户的专用 SSH 私钥。
   - `DEPLOY_KNOWN_HOSTS`：目标主机的已验证 SSH host key 行。
   - `DEPLOY_ENV_FILE`：将环境模板中的尖括号字段说明替换为真实配置后的完整环境文件，不包含三项 `LINGOW_*_IMAGE` 值。
   - `DEPLOY_SMOKE_ACCESS_TOKEN`：部署后业务冒烟的专用账号访问令牌，缺失时发布会失败。
   - `DEPLOY_SMOKE_SESSION_ID`：该账号预先创建且保持可用的 voice session ID，缺失时发布会失败；后续应改为每次动态创建测试会话。
   - `DEPLOY_PUBLIC_BASE_URL`：从 GitHub runner 可访问的 HTTPS Web 地址，用于真实浏览器 WebRTC 冒烟，缺失时发布会失败。
   - `DEPLOY_METRICS_TOKEN`：与环境文件中的 `REALTIME_METRICS_TOKEN` 相同的内部监控 token，用于部署后观察窗口；生产环境必填。

   添加 repository variables：

   - `DEPLOY_PATH`：部署用户可写的绝对目录，例如 `/srv/lingow`。
   - `GHCR_PULL_USERNAME`：用于 GHCR 登录的 GitHub 用户名；通常填写仓库所有者 `Gwen317`。
   - `DEPLOY_OBSERVE_SECONDS`：生产发布后的观察时长，建议 600-900 秒；设为 0 只允许 development 跳过。
   - `DEPLOY_OBSERVE_INTERVAL_SECONDS`：采样间隔，默认 30 秒。
   - `DEPLOY_OBSERVE_MAX_PROVIDER_FAILURES`、`DEPLOY_OBSERVE_MAX_DATA_CHANNEL_FAILURES`、`DEPLOY_OBSERVE_MAX_INTERPRETATION_FAILURES`：观察窗口允许的失败计数增量，默认均为 0。
   - `DEPLOY_DATABASE_BACKUP_CONFIRMED=true`：管理员完成外部 PostgreSQL 备份后设置。应用回滚不会回退数据库 schema。

6. 工作流使用当前运行的短期 `GITHUB_TOKEN` 发布和拉取三个 GHCR package，不需要额外长期 PAT；确保 package 与该仓库关联且允许 Actions 读写即可。

## 占位符位置

- `.env.production.example` 的镜像字段 `LINGOW_API_IMAGE`、`LINGOW_REALTIME_AUDIO_IMAGE`、`LINGOW_WEB_IMAGE` 使用 `<GitHub owner>` 和 `<commit SHA>`。这三项由 GitHub Actions 自动写入部署文件，不要放入 `DEPLOY_ENV_FILE`。
- `.env.production.example` 的 `DATABASE_URL`、`REDIS_URL`、六项系统密钥、三项 consumer 名称，以及短信、SMTP、企业微信、ASR/LLM/TTS/command provider 字段中的 `<...>`，都要替换为目标生产环境的真实配置。生产模式下 `VERIFICATION_SENDER` 必须为 `http`，短信 endpoint 必须使用 HTTPS；该 endpoint 接收 `POST` JSON `{ "phone": "...", "code": "..." }`，token（如配置）通过 Bearer 头发送。staging 虚拟模式使用 `log` 和固定码 `8888`，不会调用短信服务。
- `development` 与 `production` 必须使用不同的 `DEPLOY_PATH` 和 `DEPLOY_PROJECT_NAME`；推荐使用不同主机。部署脚本会锁定目标目录，防止同一目标的并发发布。生产发布还会在外部 WebRTC 冒烟后执行观察窗口，provider、DataChannel 或语义解释失败计数超过阈值会自动恢复上一应用版本。
- 工作流会将 `LINGOW_DEPLOY_ENV` 写入发布环境文件，并使用 `lingow-development`/`lingow-production` project name；不要手工复用另一环境的发布目录。
- `REALTIME_ICE_SERVERS_JSON` 中的 TURN 主机、临时用户名和临时凭据必须由 TURN 服务提供；不要提交长期凭据。`REALTIME_ICE_TRANSPORT_POLICY=relay` 会强制媒体走 TURN。
- `.env.production.example` 的 `WEB_BIND_IP` 和 `WEB_PORT` 不使用尖括号；默认值分别为 `127.0.0.1` 和 `3000`，需要改变监听地址或端口时直接修改对应值。
- `docker-compose.yml` 不应填写尖括号文本。它只通过 `${VARIABLE}` 读取环境文件；带 `:?` 的变量为必填项，带 `:-` 的变量使用默认值。Web 的宿主机绑定由 `WEB_BIND_IP` 与 `WEB_PORT` 控制。
- 本 README 中的 `/srv/lingow` 只是命令示例，不是需要填入环境文件的尖括号字段；执行回滚命令时将其替换为实际 `DEPLOY_PATH`。

## 发布与回滚

`.github/workflows/deploy-production.yml` 在 `dev` 和 `main` 分支执行。工作流构建三个不可变的 commit-SHA 镜像，并将 Compose、环境文件和发布脚本上传到 `${DEPLOY_PATH}/.staging/<commit SHA>`。`scripts/deploy.sh` 在同一个远程发布事务中校验 Compose 插值、按环境使用独立 Compose project、获取部署锁、拉取镜像、等待 health check 并强制执行认证冒烟；冒烟失败时恢复上一次成功发布的 Compose、环境文件和应用容器。数据库迁移由 API 启动时按序向前应用；工作流只检查 up-only 文件和事务边界，不执行 down migration。生产部署前必须由管理员完成外部备份并设置 `DEPLOY_DATABASE_BACKUP_CONFIRMED=true`；应用回滚不会恢复 schema、provider 数据或已建立连接。

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

发布事务中的 `scripts/deploy-smoke.sh` 会验证 API/realtime 健康、用专用访问令牌获取 realtime ticket，并用该 ticket 获取 WebRTC 配置（包括 TURN 配置）。部署完成后，GitHub runner 还会使用 `apps/web/playwright.deploy.config.ts` 从外部 HTTPS 地址建立真实 PeerConnection，检查 TURN relay、DataChannel 和 realtime 状态；该检查失败会调用 `scripts/rollback.sh` 恢复上一版本。收费 provider 的真实质量仍需在发布窗口人工验收。
