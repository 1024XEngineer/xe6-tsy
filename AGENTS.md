# Project Agent Rules

## 必读与优先级

修改产品、架构、代码或文档前，必须阅读：

1. `docs/项目前后端统一开发规范.md`；
2. 根目录 `README.md`；
3. 改动目录内最近的 `README.md`；
4. 本次工作关联的 Issue、Proposal 和验收标准。

已接受的项目决策和用户最新明确指令优先于仍在讨论的 Proposal。发现规则、Issue、
代码现状冲突时，不得静默选择；先以最小范围保留现有行为，并在交付说明中指出冲突。

## 项目边界

- 产品是供基层政务窗口工作人员使用的方言沟通辅助 PC Agent，不是群众端 App、
  自动审批系统或开放式自主 Agent。
- `apps/api` 是 Go/Gin 模块化单体，拥有七个业务模块的权威状态；七模块不是七个微服务。
- `apps/desktop` 只承载 Wails 桌面与设备能力，`apps/desktop/frontend` 是 Vue 3 +
  TypeScript 薄客户端，不复制后端权威业务状态。
- 跨前后端契约统一放在 `packages/contracts`。生成的类型和客户端不得手工修改，前端不得
  绕过 `services` 层散落裸 HTTP 调用或复制 API 类型。
- ASR、模型、RAG、TTS、数据库、Redis、MQ 等外部能力必须位于可替换边界之后；密钥和
  真实群众数据不得进入仓库、测试夹具、日志或截图。
- 模型输出只能是候选或草稿；最终业务结果必须保留人工复核入口和来源可追溯性。

## 开发流程

- 遵循训练营链路：`Proposal -> Design(Issue) -> Coding(PR) -> Review -> Merge`。
- 先明确用户、问题、范围、不做什么、接口/数据影响和可验证验收标准，再编码。
- 一个 Issue 对应一个或少量范围可控的 PR；改动不得夹带无关重构或提前实现未来范围。
- 系统架构以可编译、可测试的真实 package、interface、struct、contract 和模块调用表达；
  目录占位、技术栈清单或无法串联的伪骨架不算“代码即架构”。
- 进入开发的工程设计视为基线。需求变化通过新的 Issue/Proposal 或 delta 说明追加，
  不用覆盖历史的方式抹掉决策演进。
- Codex 不得创建目标为 `main` 的 PR，不得合并或直接推送 `main`。默认在 `dev` 或用户
  明确指定的非 `main` 分支准备改动。

## 测试门禁

Issue #43 的 Go/Gin 测试约定是本项目后端默认规则：

- 使用标准库 `testing` 和 `net/http/httptest`；多场景使用带 `name` 的表格驱动测试和
  `t.Run`，测试 helper 调用 `t.Helper()`。
- service/domain 测试业务规则且不依赖 Gin；Handler 测试只验证路由、输入、状态码、
  响应 JSON 以及依赖交互。
- 单元测试和 Handler 测试默认不得连接真实数据库、Redis、MQ 或第三方服务，优先使用
  小型手写 fake/stub，不为简单边界引入复杂 mock 框架。
- 集成测试必须使用 `//go:build integration` 隔离，通过
  `go test -tags=integration ./...` 显式执行；默认 `go test ./...` 必须稳定、离线可运行。
- 不用 `time.Sleep` 协调并发测试；使用 channel、context 或其他确定性同步机制。
- 新功能和缺陷修复必须补充对应测试，并覆盖主要成功、输入错误和依赖失败路径。
- 前端同样遵循“业务逻辑、组件交互、端到端流程分层测试”和“默认隔离真实后端/设备/
  供应商”的原则；具体要求见统一规范。

## 完成标准

- 保持改动小、可解释、可回退，并遵循相邻代码和目录的既有模式。
- Go 改动至少执行 `gofmt` 和相关包测试；交付前执行 `make check`。
- 前端工具链建立后，改动必须通过仓库脚本提供的 format/lint/typecheck/unit/build；影响关键
  桌面流程时同时运行 E2E。不得通过删除、跳过或弱化测试来制造通过结果。
- 契约变化必须同时校验 schema、生成物和前后端消费者；禁止只改单侧并假定兼容。
- PR 合入前必须有关联 Issue、验证证据、AI 代码质量检查和人工 Review；Milestone 结束时
  产出可打 tag 的版本及 Release 说明。
