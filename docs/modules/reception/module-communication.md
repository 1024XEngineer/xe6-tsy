# 模块 3 后端通信文档

## 1. 当前通信方式

当前是单个 Go/Gin 模块化单体。模块 3 在自己的 `ports.go` 中定义调用方所需的最小接口，
通过构造函数注入；上游未完成时使用 deterministic Fake。

| 调用方向 | 方式 | 目的 | 数据所有者 |
| --- | --- | --- | --- |
| 模块 3 → 模块 1 | 同步 Go interface `AccessAuthorizer` | 每个写命令即时授权 | 模块 1 |
| 模块 3 → 模块 2 | 同步 Go interface `OrganizationConfigReader` | 读取已发布配置并固定版本 | 模块 2 |
| 模块 3 → Processing Gate | 同步 Go interface `ProcessingGate` | 只读检查接待、实时音频和录音持久化门禁 | 横切处理门禁 |
| 模块 3 → 媒体适配层 | 同步 Go interface `MediaAdapter` | 接入/断开 Fake Media | 模块 3 只拥有绑定状态 |
| 模块 3 → 模块 4 | 待确认；仅传只读引用 | 为后续处理预留媒体可用事件 | 模块 4 拥有处理任务 |

接口均以 `context.Context` 为首参数，使用专用 Request/Response DTO，不传 Gin Context、ORM
Model、数据库连接或 `map[string]any`。

## 2. 模块 3 → 模块 4 的边界

允许传递的最小内容为 `session_id`、`binding_id`、`track_ref`、聚合版本和
`organization_config_version`。事件不携带音频字节、全文转写、群众敏感信息或供应商 Token。

明确约束：

- 模块 3 不拥有转写任务和转写片段。
- 模块 4 不得修改 `ReceptionSession` 或 `MediaTrackBinding`。
- 模块 3 不直接 import 模块 4 的数据库层和具体实现。
- 媒体失败只改变媒体绑定，不自动结束或取消接待会话。

## 3. 超时、错误、幂等和 Mock

同步依赖错误安全映射为稳定业务码，不向调用方泄漏底层响应。写命令用
`operation + idempotency_key + request fingerprint` 去重，状态修改使用乐观锁版本。当前 Fake
固定支持访问成功/拒绝/过期/范围不匹配、配置 `config-v1`、Processing Gate 开关及四种媒体
场景。真实适配层替换 Fake 时不得改变领域状态机或 HTTP 契约。

`MediaAdapter.Detach` 和 `MediaResourceCleaner.Clean` 必须可幂等重试。结束或取消会话时，
两项外部操作均成功后才将绑定置为 `detached` 并提交会话终态；失败时不写入伪成功或
`SESSION_CLOSED` 状态。

## 4. 尚待双方确认

- 模块 4 启动处理的准确触发时机。
- 模块 4 接收领域事件还是由模块 3 同步调用。
- 模块间调用或事件处理的超时规则。
- 同一 binding 重复启动处理的规则。
- 运行中断线后模块 4 如何终止、flush 或标记失败。
- 会话结束时 flush 的责任方和等待边界。

本骨架不替模块 4 作最终决定。
