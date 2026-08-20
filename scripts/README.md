# scripts

开发脚本目录。

建议脚本：

- `dev.ps1` / `dev.sh`：启动本地依赖和服务
- `check.ps1` / `check.sh`：统一 lint、typecheck、test
- `gen-contracts.ps1` / `gen-contracts.sh`：从 contracts 生成 Go/TypeScript 类型
- `deploy.sh`：在部署主机上校验 Compose、拉取不可变镜像并等待健康检查；生产参数和 GitHub Actions 配置见 [`../infra/production/README.md`](../infra/production/README.md)。
