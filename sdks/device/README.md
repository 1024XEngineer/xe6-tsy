# sdks/device

面向硬件厂商和方案商的设备 SDK 参考实现。当前提交提供一个不依赖第三方库的 Go 控制核心，
用于验证状态投影、模式切换和弱网策略；硬件 WebRTC、音频 HAL 和唤醒词引擎仍由平台适配层提供。

## 当前能力

- `contracts.go` 直接复用 `packages/contracts/realtime/v1` 的 Runtime、Connection、Mode 类型别名。
- `HTTPModeTransport` 调用 realtime 的 `GET/POST /realtime/v1/sessions/{session_id}/mode`。
- `ModeController` 保存最新观察快照，发送带 `runtime_instance_id` 和 `expected_generation` 的类型化命令。
- 每个 operation 首次发送时固定完整命令 payload；不确定错误后的显式重试只重放原 payload，不会根据刷新后的 generation/runtime 重新组装。发生 generation 或 runtime instance 冲突时，旧 operation 会被废弃并立即 GET 刷新；不会自动重放旧命令。
- `StateStore` 按连接版本、Runtime 时间和 Mode generation 过滤迟到快照，允许新 runtime instance 替换旧观察值。
- `Reconnector` 通过注入的 `ReconnectPolicy` 和 `Connect` 函数执行平台自定义重连。
- `WakeCommandController` 将 `WakeWordEngine` 的板载 KWS 结果转换为统一 `wake_word.detected`，并通过平台实现的 `WakeWordSignalSender` 写入现有可靠有序 DataChannel；设备不解析业务命令，也不管理服务端命令窗口。
- `SessionStartClient` 向 API Start 发送类型化 `initial_mode`；省略时显式使用 `interpretation`。
- 硬件认证使用 `device_id` 和出厂 Ed25519 私钥；设备先与登录账户配对，再以挑战签名获得短期 device token。
- `DeviceAuthClient` 实现 `AccessTokenSource`；配合 `SessionStartClient{SessionPath: "api/v1/device/voice-sessions"}` 调用受限设备会话路由。

从仓库根目录运行测试会自动通过 `go.work` 覆盖本 SDK；也可以在 SDK 目录独立运行：

```bash
go test ./sdks/device/...
go test -race ./sdks/device/...
go vet ./sdks/device/...
```

## 明确的阶段边界

本提交不伪造以下尚未存在的后端能力：

- WebRTC offer/answer、ICE、音频 Track 和 PeerConnection 生命周期；
- 任意芯片/OS 的真实本地唤醒词模型。

因此设备端默认不改变旧行为：省略 mode 时由 realtime 使用 `interpretation`，模式控制、KWS 或刷新失败
不会阻断普通音频和当前活动模式。后续平台适配必须通过本目录的接口接入，不能在 SDK 核心中引入供应商类型。

## 事件方向

```text
device -> api:
  device.pair / device-auth.challenge / device-auth.token
  session.start
  realtime_ticket.request
  session.end

device -> realtime-audio:
  webrtc.offer / ice.candidate / audio track
  wake_word.detected (可靠有序 lingow-control-v1 DataChannel)

realtime-audio -> device:
  webrtc.answer / ice.candidate
  runtime.snapshot / mode.snapshot
  asr.partial / asr.final / translation.final
  playback.start / playback.stop / error
  command.result
```

ESP32-S3 等平台应让同一份麦克风 PCM 持续进入板载 KWS 和既有 WebRTC 编码链路。KWS 命中固定
「小灵小灵」后调用 SDK 控制器即可；不得停止上行、重建 PeerConnection，或把模型名称、阈值、
目标模式和语言方向放入事件。模型格式、版本和阈值由固件自行管理。

设备结束会话时只向 API 发送 `session.end`，随后停止采集并关闭本地 PeerConnection。API 负责幂等调用
realtime `Stop`；realtime 完成 Pipeline 和连接清理后，API 才把业务会话标记为 `ended`。

设备不能保存用户 Access Token 或 Refresh Token，也不能只凭 `product_id` 或 `device_id` 访问服务。
生产固件应使用 `/api/v1/device/voice-sessions/*`，并仅在短期 device token 有效时请求 realtime ticket。
