# scripts

开发脚本目录。

建议脚本：

- `dev.ps1` / `dev.sh`：启动本地依赖和服务
- `check.ps1` / `check.sh`：统一 lint、typecheck、test
- `gen-contracts.ps1` / `gen-contracts.sh`：从 contracts 生成 Go/TypeScript 类型
- `deploy.sh`：在部署主机上从 SHA staging 目录校验 Compose、获取部署锁、拉取不可变镜像、等待健康检查并强制执行认证 smoke；生产参数和 GitHub Actions 配置见 [`../infra/production/README.md`](../infra/production/README.md)。两参数形式仍用于手工恢复当前目录版本。
- `rollback.sh`：从 `.previous` 恢复稳定版本并等待容器健康，供外部 WebRTC 门禁失败时自动调用，也可由管理员手工执行。首发没有 `.previous` 时，只有工作流显式设置 `ROLLBACK_CLEAN_FIRST_RELEASE=true` 才会清理容器，且不会删除命名数据卷。
- `observe.sh`：在部署后通过受保护的 realtime `/metrics` 采集失败计数增量；超过阈值返回失败，由工作流调用回滚。
- `check-migrations.sh`：检查 API migration 仅使用按序 `.up.sql` 文件且不自行开启/提交事务；它不执行 migration，也不提供 schema down 回滚。
- `deploy_test.sh`：离线验证 smoke 失败时恢复上一成功发布。
