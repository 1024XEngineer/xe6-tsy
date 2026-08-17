# 客户端与设备侧 KWS 接入规范

## 目标与边界

Lingow 的唤醒词检测运行在客户端或硬件设备本地，后端不运行 KWS 模型。当前固定产品唤醒词为
「小灵小灵」；Web 可使用 sherpa-onnx，ESP32-S3 可使用适合板载资源的 KWS 模型，其他设备也可
替换自己的本地推理实现。

KWS 实现可以不同，但输出协议和职责必须一致：

```text
Web sherpa-onnx / ESP32-S3 KWS / 其他本地模型
  -> 检测固定唤醒词「小灵小灵」
  -> 通过已鉴权 PeerConnection 的可靠有序 DataChannel 发送 wake_word.detected
  -> 命令语音继续走同一条 WebRTC 上行音轨
  -> realtime-audio 将普通 VAD 当前未结束的完整语句转交 Command Gate
  -> Command Gate 复用同一套 VAD 断句实现，等待该语句自然结束
  -> Command ASR + AI 语义理解 + 确定性校验 + Capability Executor
  -> command.result
```

客户端或设备负责：

- 加载、更新和运行适合自身平台的 KWS 模型；
- 自行管理模型版本、阈值、回滚和设备兼容性，后端协议不绑定 ONNX、TFLite Micro 或厂商格式；
- 只检测固定唤醒词，不在设备侧判断模式、开始/停止动作或语言方向；
- 每次新的本地唤醒生成唯一 `signal_id`；不确定是否送达时，重试必须复用原 ID；
- 唤醒后继续通过同一 WebRTC 音轨发送完整命令语音，不新建 PeerConnection；
- 收到 `command.result` 后展示或播放结果，不根据结果事件重新执行命令。

`services/realtime-audio` 负责：

- 从已鉴权 PeerConnection 绑定 Session，拒绝消息内自报 `session_id`；
- 校验唤醒事件类型、版本、大小和字段，不信任客户端业务判断；
- wake 校验并成功打开 Command Gate 后，按普通 VAD 已持有的完整语句转移音频所有权，不使用固定秒数回看窗口；
- 普通音频与命令音频使用独立 VAD 状态，但复用同一个 Segmenter 断句实现和句末规则；
- 使用 Command ASR 和 AI Interpreter 理解自然语言，再经 Registry、Validator 和 Executor 执行；
- 对同一 `signal_id` 幂等处理，对新的 ID 取消尚未完成的旧命令；
- 模式切换复用当前 Runtime 和 WebRTC 连接，不调用 Session Stop/Start。

## 上行事件

事件通过 `lingow-control-v1` DataChannel 发送。该通道必须可靠、有序，并在首次 Offer 前由客户端创建。

```json
{
  "type": "wake_word.detected",
  "event_version": 1,
  "signal_id": "wake-018f7b9f",
  "detected_at": "2026-08-12T10:00:00Z"
}
```

字段规则：

| 字段 | 规则 |
| --- | --- |
| `type` | 固定为 `wake_word.detected` |
| `event_version` | 当前固定为 `1` |
| `signal_id` | 每次新唤醒唯一，最长 128 字符；重试必须保持不变 |
| `detected_at` | 设备观测时间，仅用于诊断，不作为服务端音频边界 |

事件中不得加入 `session_id`、命令文本、目标 Mode、开始/停止标记或语言方向。Session 已由实时票据、
PeerConnection 和 DataChannel 绑定；业务语义来自随后上传的音频。

## 下行结果

后端对终止的命令尝试发送 `command.result`：

```json
{
  "type": "command.result",
  "event_version": 1,
  "command_id": "wake-018f7b9f",
  "session_id": "vs_123",
  "runtime_instance_id": "rt_456",
  "generation": 2,
  "status": "applied",
  "action": "activate_mode",
  "target_mode": "interpretation",
  "message": "已进入同声传译模式",
  "occurred_at": "2026-08-12T10:00:02Z"
}
```

状态包括 `applied`、`unchanged`、`clarification_required`、`unsupported` 和 `failed`。解释或校验在
执行前失败时，`runtime_instance_id`、`generation`、`action` 和 `target_mode` 可以省略。结果投递是
尽力而为：失败不会回滚或重新执行已经完成的命令。

## ESP32-S3 实现要求

本仓库当前不实现 ESP32-S3 固件，也不固定芯片侧模型格式。设备实现至少需要满足：

1. KWS 在板端离线运行，模型和阈值可随固件或设备资源更新。
2. 只有固定唤醒词命中才发送 `wake_word.detected`。
3. 唤醒后不要停止 WebRTC 上行，否则后端无法收到紧随唤醒词的自然语言命令。
4. 网络重试复用同一 `signal_id`；用户再次说出唤醒词才生成新 ID。
5. 设备时钟不需要与服务器精确同步，`detected_at` 不参与服务端音频切分。
6. 设备不能直接发送 `mode.switch` 代替语义入口，也不能在本地维护权威 active mode。

KWS 应在用户说出唤醒词时立即发送事件。服务端只认领普通 VAD 中仍处于 active 状态的当前语句；
已经按 VAD 句末结束并提交的普通 Turn 不会被追溯撤回。该规则避免固定两秒缓存截断长命令，也避免
同一段 active 音频同时进入普通处理器和命令处理器。

建议 ESP32-S3 固件将 KWS 作为 WebRTC 音频采集旁路：同一份麦克风 PCM 一路持续送入板载 KWS，
另一路按现有编码和发送策略进入 WebRTC。KWS 命中只产生控制事件，不能重启采集器、切换音轨或
清空紧随唤醒词的音频。固件发布记录应能追踪 KWS 模型版本和阈值，但这些观测信息当前不进入
`wake_word.detected` v1，避免设备实现细节污染业务契约。

英语口语训练在当前阶段不增加 Mode、Schema、Provider、存储或 Handler。未来能力实现后只注册新的
Capability 和 Handler，继续复用本规范的 KWS、WebRTC、Command Gate、语义解释与结果反馈链路。
