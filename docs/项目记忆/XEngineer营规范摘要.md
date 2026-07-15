# XEngineer 营规范摘要

更新时间：2026-07-15

来源：<https://github.com/1024XEngineer/techcamp/wiki>

如本摘要与训练营 wiki 最新内容冲突，以 wiki 为准并同步更新本文件。

## 工程节奏

- 每个 Milestone 都完成一轮“产品 -> 架构 -> 开发 -> 验收”。
- 先设计再开发，重要决策进入 Issue、Proposal 或仓库文档，不只保留在聊天中。
- Milestone 结束时应有可打 tag 的版本和 Release 说明。

## 代码即架构

- MS1 要建立能支撑主链串联的模块边界、关键数据模型和接口契约。
- 模块、接口和数据模型必须用真实代码表达；模块内部可以使用 fake 或 stub。
- 骨架必须可编译、可测试，模块间必须存在真实调用，不能只是目录占位或技术栈清单。
- 架构说明用于解释边界与协作逻辑，代码骨架是主要架构载体。
- 后续 Milestone 在稳定主干上增量演进，并记录相对上一轮的架构变化。

## GitHub 过程

- Milestone 管目标，Issue 管任务和工程文档草稿，PR 管代码与 Review，Release 管产出。
- 一个 Issue 对应一个或少量范围可控的 PR；PR 必须关联 Issue，且不夹带无关改动。
- PR 合入前必须通过必要验证、AI 代码质量检查和人工 Review。
- Proposal/Design 先于编码；设计变化通过新的 Issue、Proposal 或 delta 记录，不覆盖历史。
- 训练营通常采用 Fork + PR 协作，但本项目对 Codex 有更严格约束：不得创建目标为 `main` 的 PR，不得合并或直接推送 `main`。

## 当前基础框架的应用

Issue #37 是技术选型和架构输入。本次 `dev` 只建立可启动的 Gin 服务、统一配置、
API 版本分组和七模块挂载边界，不提前实现业务接口。后续模块应以独立 Issue 和
小范围 PR 增量增加真实 contract、数据模型与调用链。
