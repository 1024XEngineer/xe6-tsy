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
  webrtc.offer
  webrtc.answer
  ice.candidate

device -> realtime-audio:
  WebRTC audio track
  playback.finished
  playback.interrupted
  session.end

realtime-audio -> device:
  asr.partial
  asr.final
  translation.final
  tts.ready
  playback.start
  playback.stop
  error
```
