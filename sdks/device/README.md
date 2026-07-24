# sdks/device

面向硬件厂商和方案商的设备 SDK 规范与参考实现。

## SDK 边界

SDK 负责把硬件音频和后端实时能力连接起来，不负责硬件制造。

职责：

- 设备鉴权
- token 管理
- 会话创建和结束
- WebRTC 音频接入；硬件只支持 PCM 时由 SDK 或边缘适配层转码
- 播放指令接收
- 播放完成/中断上报
- 网络重连
- 设备遥测

## 事件方向

```text
device -> api:
  session.start
  realtime_ticket.request
  session.end

device -> realtime-audio:
  webrtc.offer
  ice.candidate
  WebRTC audio track
  playback.finished
  playback.interrupted

realtime-audio -> device:
  webrtc.answer
  ice.candidate
  asr.partial
  asr.final
  translation.final
  tts.ready
  playback.start
  playback.stop
  error
```

设备结束会话时只向 API 发送 `session.end`，随后立即停止本地采集并关闭本地 PeerConnection。
API 负责幂等调用 realtime `Stop`；realtime 停止 Pipeline 并关闭服务端连接后，API 才把业务会话
标记为 `ended`。`Stop` 失败时由 API 重试，realtime 同时使用连接租约或空闲超时回收孤立连接。
