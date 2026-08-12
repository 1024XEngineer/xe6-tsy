# packages/contracts

跨端协议定义。

## 内容

- REST OpenAPI
- WebRTC 信令协议
- 实时事件协议
- WebRTC 音频媒体链路说明
- 错误码
- 会话状态机
- TypeScript 类型生成
- Go 类型生成

当前客户端可直接复用 `typescript/realtime.d.ts` 导出的实时控制和 VoiceSession 类型；该语言绑定由契约测试与 OpenAPI 必填字段、枚举保持一致。

语言配置模块的 HTTP/内部实现仍位于 `services/api/languages`（Issue #88）；本目录的
OpenAPI 已声明 `/languages` 与 `language-config(s)` 路径及其请求、响应和错误 schema，
并作为跨模块契约真源。

## 规则

- 所有跨端字段先改 contracts，再改实现。
- 不在 Web、Mobile、Go 服务里重复手写协议类型。
- 破坏性字段变更必须写迁移说明。
- 音频媒体流走 WebRTC audio track；contracts 只定义信令、控制事件、状态和错误码。

## 阶段 16：初始模式兼容约定

- `POST /voice-sessions/{id}/start` 的请求体可省略；`initial_mode` 省略时保持旧客户端行为，按 `interpretation` 启动。
- 新客户端应显式发送 `{"initial_mode":"assistant"}`，以助手模式作为产品入口；需要快速回退时发送 `interpretation`。
- `initial_mode` 仅接受契约枚举中的 `assistant` 或 `interpretation`。英语口语训练暂不加入枚举，也不在本阶段改变其实现范围。

## Records P0 decisions

- Every new `FinalTurnEvent` must include `language_config_version` and its value must be at least `1`.
- `language_config_version` is fixed when realtime opens a turn and records the language configuration used for that turn; it is not inferred or defaulted by the API consumer.
- The field is required in the Go contract, OpenAPI, AsyncAPI, validation, and `voice_turns` storage schema. Missing, zero, or negative values are rejected as invalid events.
- Issue #83's earlier nullable proposal is superseded for new events. Existing historical data is handled by migration and replay compatibility rules rather than by making the current event contract nullable.
