# apps/mobile

Mobile 端核心控制面客户端骨架，供 Vue、uni-app、Capacitor 或原生壳接入。

本阶段只提供可编译的 TypeScript 状态和 HTTP 控制面核心，不绑定 UI 框架。

## 职责

- 模拟硬件侧音频采集
- 展示 Lingow 面对面传译流程
- 支持按钮或语音唤醒进入对话模式
- 语音唤醒仅在页面已打开且麦克风授权后生效
- 提供开始传译、语言选择和基础状态展示
- 展示“Lingow 已进入对话模式”
- 动态展示“已识别中文/英语”等自动语言识别结果
- 首页仅显示最新一条字幕预览，点击进入后展示完整识别内容
- 验收句末播音、打断处理和弱网重连
- 验收说话人识别、流式语音识别、双向翻译和 TTS 播放
- 预留后续跨设备会话和多人会议入口

## 非职责

- 不作为官网付费入口
- 不承载生产级管理后台
- 不做首页完整字幕列表展示
- 不替代硬件 SDK

## 技术栈

- TypeScript
- Vue 3
- uni-app / Capacitor

## 当前阶段边界

已实现：

- typed `ConnectionSnapshot`、`RuntimeSnapshot` 和 `ModeStateSnapshot`；
- HTTP `GET` 快照和 `POST /mode` 类型化模式命令；
- generation/runtime instance/operation conflict 后刷新 ModeState，并废弃旧 operation；刷新失败会进入错误状态；
- 可订阅的展示状态模型，包含最后一次模式命令的 operation ID 和结果；
- 连接断开状态和可注入的真实媒体重连适配器；未注入时明确失败，不使用状态 GET 冒充重连；
- 仅明确返回 `501 not_implemented` 的旧部署按兼容规则使用 `interpretation`；鉴权和依赖错误不会降级为可用状态。
- `SessionStartClient` 向 API Start 发送类型化 `initial_mode`；新客户端省略时显式使用 `assistant`。

明确未实现：

- WebRTC PeerConnection、DataChannel 上行命令和命令窗口确认；
- 本地唤醒词模型/原生 KWS；待真实原生依赖确定后再引入对应适配边界。

## 本地验证

```bash
npm test
npm run typecheck
npm run build
```

`npm test` 使用 Node 内置 `node:test` 和 TypeScript 类型擦除运行，无需连接 API、realtime 或真实设备。
