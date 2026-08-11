# AGENTS.md

这些规则适用于`../services`及其子目录。它们扩展了仓库级别的AGENTS.md和统一开发规范。

## Go 注释与风格

- 复杂逻辑必须配解释型注释，尤其是状态迁移、重试恢复、授权审计、并发控制、
  超时幂等、存储映射、兼容 shim、错误包装和交付对象构建。注释说明职责、原因、边界、
  失败路径和幂等假设；不要复述代码表面含义或写空话。
- 修改复杂逻辑时同步修正旧注释；当前改动范围内发现中文代码注释时改为英文。顶层
  doc comment 必须说明 contract、invariant、side effect 或限制条件，不能只重复声明名。
- 涉及正式请求、响应、通知、错误对象、事件或交付对象时，先对齐 `../contracts`
  和相关真源文档。穿过 HTTP/RPC 边界后优先使用 typed request/response struct；正式
  struct 必须有清晰 JSON tag 和显式可选性。
- `map[string]any` 只用于真正动态、第三方透传、审计扩展或迁移期 adapter 边界，不作为
  业务层正式入参或出参。字段名、错误码、状态值应能被类型、常量和跳转引用追踪。
- 成功路径向下流动；invalid、noop、special case 和错误路径尽早 return。避免多层嵌套、
  长 `else if` 链，以及在主流程中混杂校验、编排、状态迁移、协议组装和用户文案。
- 单函数约 80 行、单文件约 400 行是重构信号，不是机械阻断条件。优先按行为阶段或支撑
  职责拆分，如 `task_start.go`、`input_submit.go`、`response_builders.go`、`error_specs.go`；
  `service.go` 只保留核心结构、构造和少量总入口。
- 构造器明确区分 required dependencies 与 optional capabilities。必选依赖集中到 `Deps`
  或清晰 staged build；`New(...)` 创建后的对象必须处于最小可用状态，不长期暴露半初始化对象。
- 包名短、准、自然；长期依赖 import alias 才能读懂或避开标准库冲突时，优先评估包名是否
  应调整。不要为没有明确收益的重命名或抽象扩大改动面。
- 错误 contract、复用文案和交付组装应集中维护；当同类映射跨多个 handler 或流程复用时，
  使用声明式映射、registry、builder 或 typed state object，避免散落在长 `if errors.Is(...)` 链中。
- `cmd` 层保持 boring，只做最薄 wiring 和退出管理；主要逻辑下沉到可测试的 `run() error`
  或等价入口。不要在 `main` 中堆构造、分支和副作用，也不要多处随意 `Fatal`。

## 后端测试

- 使用标准库 `testing` 和 `net/http/httptest`。
- 多场景使用带 `name` 的表格驱动测试和 `t.Run`。
- 测试 helper 调用 `t.Helper()`。
- service/domain 测试业务规则且不依赖 Gin。
- Handler 测试只验证路由、输入、状态码、响应 JSON 以及依赖交互。
- 单元测试和 Handler 测试默认不得连接真实数据库、Redis、MQ 或第三方服务。
- 集成测试必须使用 `//go:build integration` 隔离，通过 `go test -tags=integration ./...` 显式执行。
- 默认 `go test ./...` 必须稳定、离线可运行。
