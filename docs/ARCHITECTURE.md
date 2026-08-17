# 项目骨架与模块边界

## 1. 关键假设

- 首期允许用户选择一组双语语言对；每个会话只允许两种语言参与传译，默认语言对为 `zh-CN <-> en-US`。
- 首期是面对面双向传译，不做多人会议同传。
- 产品交互是句级轮流传译，不做边听边播。
- 你们做 SDK 与后端能力，不做硬件制造。
- 产品名为 Lingow，首期提供语音识别、语音同传和字幕预览，不做独立字幕产品和管理后台。
- 硬件接入方案需要预设降噪、弱网重连和基础遥测。

## 2. 模块边界

### apps/web

Web 是 Lingow 对话入口，负责极简产品交互，不做管理后台。

职责：

- 首页支持按钮或语音唤醒进入对话模式
- 语音唤醒仅在页面已打开且麦克风授权后生效
- 提供助手优先的语音入口、语言选择区域和基础状态展示区域；首页显示最新助手回复或字幕预览，点击后显示完整识别内容
- 展示“Lingow 已进入对话模式”
- 展示自动语言识别结果，例如“已识别中文/英语”；首页仅展示最新一条字幕预览，点击后显示完整识别内容；后续可按配置展示“已识别法语/西班牙语”
- 展示语音识别、助手回复、双向翻译和 TTS 播放组件的运行状态
- 预留后续面对面沟通、跨设备会话和多人会议入口
- 作为实时音频服务的产品验收入口

不负责：

- 实时音频流处理
- ASR/翻译/TTS 编排
- 硬件蓝牙或底层音频采集
- 管理后台、订单、套餐和发票

### apps/mobile

手机端使用 Vue，是 Lingow 移动端对话入口、演示和验收工具。

职责：

- 模拟硬件采集音频
- 按钮或语音唤醒进入对话模式
- 语音唤醒仅在页面已打开且麦克风授权后生效
- 提供语言选择和基础状态展示，首页仅展示最新一条字幕预览，点击后显示完整识别内容
- 验收说话人识别、自动语言识别、流式语音识别
- 验收双向翻译、TTS 播放、句末等待时间、打断处理和弱网重连

不负责：

- 官网付费
- 管理后台
- 生产级硬件固件能力

### services/api

后端 API 是应用控制服务，不是管理后台。

职责：

- 会话创建/结束
- 设备或演示客户端接入
- 校验会话归属并签发短期实时连接票据
- 对话模式配置
- 当前语言对配置和可选语言列表
- 会话状态快照查询
- 基础健康检查
- 必要的调试记录

不负责：

- 长连接音频流
- SDP offer/answer 和 ICE candidate 交换
- PeerConnection、DataChannel 和 Track 生命周期
- 句末检测和实时播放状态机
- 运行时状态机事实来源
- 订单、支付、发票、控制台页面

### services/realtime-audio

实时音频服务是媒体面服务。

职责：

- WebRTC config、offer/answer 和 ICE candidate 信令
- PeerConnection、DataChannel 和 Track 生命周期
- WebRTC 音频接入
- 运行时会话状态机事实来源
- 音频帧鉴权和会话绑定
- 自动语言识别
- 说话人识别
- VAD、句末检测、抢话/打断判断
- ASR partial/final 管理
- 中英翻译编排
- 上下文纠偏
- TTS 合成
- 播放指令下发
- 实时事件落库或推送给 API

不负责：

- 订单、支付、发票
- 组织权限和套餐管理
- 官网页面
- 字幕生成和字幕排版

### sdks/device

设备 SDK 是硬件伙伴接入入口。

职责：

- WebRTC 音频接入
- 设备鉴权和 token 刷新
- 会话创建/结束
- 播放指令接收
- 打断/停止播放指令处理
- 弱网重连和断点恢复
- 设备遥测：麦克风状态、音量、网络、延迟

### packages/contracts

合同层是跨端协议来源。

职责：

- REST API OpenAPI 定义
- WebRTC 信令协议
- 实时事件协议
- 音频媒体链路说明
- 错误码
- 会话状态机

规则：

- Web、Mobile、Go 服务、设备 SDK 都以 contracts 为边界。
- 不允许各端私自定义重复字段。
- 字段废弃先标记 deprecated，不直接删除。

## 3. 实时链路

```text
Web / Mobile / Device SDK
  -> api: create session / language config / realtime ticket
  -> realtime-audio: WebRTC signaling / audio track / data channel
  -> speaker identification
  -> language identification
  -> noise / VAD
  -> utterance segmenter
  -> ASR partial
  -> ASR final
  -> translation draft
  -> context correction
  -> ordinary turn: TTS -> playback.command
  -> long turn: WeCom subtitle delivery -> fallback TTS when unavailable or finally failed
  -> Lingow UI state update
  -> api: state snapshot query
```

## 4. 会话状态机

```text
idle
-> conversation_ready
-> listening
-> utterance_open
-> translating
-> speaking_translation
-> interrupted | listening
-> ended
```

关键规则：

- partial 识别结果可以用于后台纠偏，但不能直接播音。
- 只有句末确认后的 final 译文才能进入 TTS。去除首尾空白后的原文超过 50 个 Unicode 字符，或原声音频时长达到 20 秒时，跳过初始 TTS，仅投递企业微信字幕。
- 长句企业微信未绑定、未配置、目标无效或最终投递失败时，通过已有 fallback playback 回放 TTS；该恢复不修改会话输出配置。
- 如果对方在 TTS 播放时开始说话，realtime-audio 发送 `playback.stop` 并进入 `interrupted`。
- 每个会话只允许两个语言槽，由用户在语言选择页确定，默认 `source=zh-CN,target=en-US` 或反向。
- `services/realtime-audio` 是 WebRTC 连接和运行时状态机事实来源；`services/api` 只负责业务会话、配置、实时连接票据和状态快照查询。
- UI 首页只展示最新一条字幕预览，详情页展示完整识别内容。

阶段 16 的入口兼容约定：新 Web 客户端在启动会话时显式发送 `initial_mode=assistant`，旧客户端省略该字段时仍由 realtime 按 `interpretation` 启动。Web 可通过 `NEXT_PUBLIC_LINGOW_INITIAL_MODE=interpretation` 快速回退；英语口语训练仍未加入当前模式枚举。

会话结束链路：

```text
Client / Device -> api: end session
api -> realtime-audio: idempotent Stop(session_id)
realtime-audio: stop pipeline and close DataChannel / Track / PeerConnection
realtime-audio -> api: stopped
api: mark business session as ended
```

`Stop` 失败时 API 不得直接写入 `ended`，应保留可重试状态并重试。客户端发出结束请求后立即
停止本地采集并关闭本地 PeerConnection；realtime-audio 使用连接租约或空闲超时兜底回收孤立连接。
