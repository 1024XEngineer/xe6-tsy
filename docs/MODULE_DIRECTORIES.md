# 三大模块涉及目录

本文说明首期三大模块对应的项目目录，方便后续拆任务和分配开发边界。

## 1. Web / 移动端页面骨架

职责范围：

- 响应式页面基础结构，兼容桌面端和手机浏览器
- 首页、开始传译、语言选择和基础状态展示区域
- 按钮或语音唤醒进入对话模式
- 预留后续面对面沟通、跨设备会话和多人会议入口

涉及目录：

```text
apps/web/
├── app/
├── features/
│   ├── conversation/
│   ├── language/
│   └── sessions/
├── components/
└── lib/

apps/mobile/
├── app/
├── features/
│   ├── conversation/
│   ├── language/
│   └── diagnostics/
├── components/
└── lib/
```

相关协议类型：

```text
packages/contracts/
```

## 2. 实时语音交互模块

职责范围：

- 麦克风授权、音频采集、播放和半双工交互流程
- WebRTC 音频链路接入
- WebRTC 信令
- 实时控制事件和状态定义
- 会话运行时状态机

涉及目录：

```text
services/api/
├── sessions/
├── devices/
├── realtimeaccess/
└── webapi/

services/realtime-audio/
├── webrtc/
├── audio/
├── vad/
├── segment/
├── playback/
└── session/

sdks/device/

packages/contracts/
```

边界说明：

- `services/api` 负责业务会话、语言配置、实时连接票据和状态快照查询。
- `services/realtime-audio` 负责 WebRTC 信令、PeerConnection、音频接入、运行时状态机、句末检测和播放控制。
- 音频媒体流走 WebRTC audio track，不通过 WebSocket 传输。

## 3. 同声传译核心服务模块

职责范围：

- 会话管理
- 语言配置
- 音频处理基础接口
- ASR、翻译、TTS 三个能力模块的调用边界
- 模型服务适配层、错误处理和后续 provider 替换方式

涉及目录：

```text
services/api/
├── sessions/
├── languages/
└── webapi/

services/realtime-audio/
├── asr/
├── translate/
├── tts/
├── pipeline/
├── audio/
└── session/

packages/contracts/
```

边界说明：

- `services/api` 不直接调用 ASR、翻译和 TTS。
- `services/realtime-audio/pipeline` 负责编排 ASR -> 翻译 -> TTS。
- `services/realtime-audio/asr`、`translate`、`tts` 分别封装供应商 provider，首期默认使用 mock provider。
- `packages/contracts` 定义跨端状态、事件、错误码和接口类型。
