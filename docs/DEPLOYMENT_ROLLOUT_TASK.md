# Lingow 自动部署与灰度发布改造任务

## 目标

为 `dev` 与 `main` 部署建立可验证、可隔离、可回退的发布链路，覆盖 Web、API、realtime、TURN 和数据库兼容性边界。候选版本在未通过业务验证前不得接管稳定流量；发布后发现运行指标异常时，能够停止候选流量并恢复稳定版本。

## 范围

- GitHub Actions 的开发/生产环境隔离、审批和并发保护。
- SHA 镜像发布、候选 Compose 项目、健康检查和业务冒烟。
- 从外部网络验证真实 WebRTC ICE、DataChannel 和 TURN relay。
- 灰度流量切换、观察窗口、自动回滚和旧会话排空。
- 数据库迁移的 expand/contract 门禁、备份提示和不可逆变更边界。
- TURN 配置检查与应用发布解耦。

不在本任务内：新增业务功能、替换 PostgreSQL/Valkey/TURN 供应商、删除现有数据、自动执行数据库 down migration。

## 推进顺序

1. 创建任务文档并冻结验收标准。
2. 隔离 `development` 与 `production` 的主机/部署目录，并为同一目标增加远程互斥锁。
3. 将认证冒烟凭证和测试会话设为必需；缺失时阻止发布，不允许静默跳过。
4. 扩展冒烟检查，覆盖外部 WebRTC ICE relay、DataChannel 和连接保持。
5. 启动独立候选 Compose 项目，使用独立 Web 端口和候选域名/代理路由。
6. 增加灰度观察窗口、指标阈值、流量撤回和候选清理。
7. 增加数据库迁移兼容性门禁和发布前备份检查；保留手工恢复边界。
8. 增加 GitHub Environment 审批、`main` 分支保护和发布审计信息。
9. 完成离线测试、外部 WebRTC 测试、开发环境验证部署，再考虑生产环境启用。

## 验收标准

- `dev` 和 `main` 不共享部署目录、Compose project name 或运行容器。
- 冒烟凭证缺失时工作流失败；成功日志必须包含 API、ticket、TURN 和真实 ICE 检查结果。
- 候选版本失败不会改变稳定流量；健康检查、冒烟或观察指标失败会自动撤回候选并恢复稳定版本。
- WebRTC 测试从服务器外部执行，ICE 在 20 秒内进入 `connected`，选中 relay candidate，DataChannel 往返成功并保持连接。
- 数据库迁移失败或不满足兼容性门禁时不得发布；回滚说明明确 schema 不自动回退。
- 生产发布必须经过 GitHub Environment 审批和必需状态检查。
- 所有脚本通过 `bash -n`、离线部署测试和相关项目测试；真实服务器验证结果记录在发布日志中。

## 回滚边界

应用镜像、Compose、代理路由和候选容器可以自动回滚。数据库 schema、外部 provider 数据、已发送通知和已经建立的客户端连接不自动回滚；这些部分必须采用兼容设计、备份或人工处置。

## 当前基线

- 目标仓库：`Gwen317/xe6-tsy`，`dev`。
- 当前已验证提交：`dec7d29702e9918ccbb3f2c0dcf2258a235834dd`。
- 现有 `scripts/deploy.sh` 已具备失败后恢复上一应用版本的基础逻辑，但当前同一 Compose 项目原地替换，且业务冒烟可被跳过。
- 本任务完成前不执行生产流量切换，不删除现有发布或数据库数据。

## 变更记录

| 阶段 | 状态 | 说明 |
| --- | --- | --- |
| 任务文档 | 已完成 | 本文档先于实现创建 |
| 环境隔离与互斥 | 部分完成 | 工作流按分支使用独立 project name 和目录变量；管理员仍需配置 development/production 不同 DEPLOY_PATH，目标主机隔离尚未验证 |
| 强制业务冒烟 | 已完成 | 缺少 token、session 或 HTTPS 公网地址时工作流失败；脚本按目标 project 执行 API/realtime/TURN 检查 |
| 外部 WebRTC 测试 | 已完成 | GitHub runner 使用真实 Chromium、TURN、ICE relay、DataChannel 和 realtime 状态门禁；真实网络结果需在部署日志中确认 |
| 候选灰度与观察回滚 | 部分完成 | 已完成发布后 metrics 观察、阈值失败回滚和应用版本恢复；独立 canary project、代理切流和 session 粘性路由仍需服务器代理配合 |
| 数据库/审批安全门禁 | 部分完成 | 已加入 up-only migration 静态检查、生产备份确认和环境审批入口；GitHub Environment reviewers、main branch protection 尚需管理员设置 |
| 开发环境验证 | 基础部署已完成，业务冒烟待补齐 | 已使用 Gwen317/xe6-tsy 的 `dev` 提交 `dec7d297` 完成镜像构建、SSH 上传、TURN 检查、容器重建和健康检查；严格业务冒烟仍需 development Environment 配置 `DEPLOY_SMOKE_ACCESS_TOKEN`、`DEPLOY_SMOKE_SESSION_ID` 和公网 HTTPS 地址 |
