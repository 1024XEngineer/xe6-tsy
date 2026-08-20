# 生产部署

该目录提供通用 Docker Compose 主机部署。它构建并运行 Web、API 和 realtime-audio；PostgreSQL、Redis/Valkey、TLS 终止和 TURN 服务不在 Compose 中创建，必须由目标环境以私网方式提供。

Web 对外只绑定宿主机回环地址 `127.0.0.1:3000`。在其前方配置已有的 HTTPS 反向代理，将公网流量转发到该端口。WebRTC 在生产中还需要可从客户端访问的 STUN/TURN 配置；当前 realtime 服务的 ICE server 仍是代码内的公共 STUN 地址，部署前应确认这符合网络与可靠性要求。

## 首次配置

1. 在 Linux x86_64 部署主机安装 Docker Engine、Docker Compose v2 和 Bash，创建专用非 root 部署用户，并确保该用户可以使用 Docker。当前工作流发布 `linux/amd64` 镜像。
2. 从 `.env.production.example` 创建部署环境文件。实际文件只保存在 GitHub `production` Environment secret `DEPLOY_ENV_FILE` 和部署主机，不能提交到仓库。
3. 将 PostgreSQL 与 Redis/Valkey 地址配置为仅部署主机可访问的 TLS/认证连接。为 API 生成独立且至少 32 字节的 `JWT_SECRET`、`AUTH_PEPPER`、`REALTIME_TICKET_SECRET`、`LINGOW_DELIVERY_DESTINATION_KEY`、`LINGOW_RECORDS_SYSTEM_TOKEN` 与 `LINGOW_COMMAND_SYSTEM_TOKEN`。
4. 配置 GitHub `production` Environment，可开启所需审批。添加以下 secrets：

   - `DEPLOY_HOST`：部署主机名或 IPv4 地址。
   - `DEPLOY_USER`：部署用户。
   - `DEPLOY_SSH_PRIVATE_KEY`：该用户的专用 SSH 私钥。
   - `DEPLOY_KNOWN_HOSTS`：目标主机的已验证 SSH host key 行。
   - `DEPLOY_ENV_FILE`：将环境模板中的尖括号字段说明替换为真实配置后的完整环境文件，不包含三项 `LINGOW_*_IMAGE` 值。
   - `GHCR_PULL_TOKEN`：仅具 `packages:read` 权限、可读取三个 GHCR 镜像的访问令牌。

   添加 repository variable `DEPLOY_PATH`，值为部署用户可写的绝对目录，例如 `/srv/lingow`。

5. 使三个 GHCR package 对该令牌或部署组织可读。若将 package 设为 public，可移除主机 GHCR 登录步骤及对应 secret。

## 发布与回滚

`.github/workflows/deploy-production.yml` 只在 `main` 分支执行。工作流构建三个不可变的 commit-SHA 镜像、上传 Compose 与环境文件，并通过 SSH 执行 `scripts/deploy.sh`。脚本会先校验 Compose 插值，再拉取镜像并等待所有 health check 成功。

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

健康检查只证明 HTTP 进程已经启动。发布后仍应执行一轮已认证的会话创建、WebRTC 信令和 provider 调用冒烟测试。
