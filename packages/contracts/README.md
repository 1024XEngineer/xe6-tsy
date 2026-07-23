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

## 规则

- 所有跨端字段先改 contracts，再改实现。
- 不在 Web、Mobile、Go 服务里重复手写协议类型。
- 破坏性字段变更必须写迁移说明。
- 音频媒体流走 WebRTC audio track；contracts 只定义信令、控制事件、状态和错误码。
