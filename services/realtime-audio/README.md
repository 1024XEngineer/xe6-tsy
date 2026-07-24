# services/realtime-audio

Go 实时音频服务。

## 职责

- WebRTC config、offer/answer 和 ICE candidate 信令
- PeerConnection、DataChannel 和 Track 生命周期
- WebRTC 音频会话
- WebRTC audio track 接入
- 运行时会话状态机事实来源
- VAD 和句末检测
- ASR / 翻译 / TTS 编排
- 上下文纠偏
- 播放指令下发
- 抢话/打断处理
- 会话事件输出

## 首期规则

- 每个会话只支持一组双语语言对，默认 `zh-CN <-> en-US`
- 只支持两方面对面
- partial 结果只用于后台纠偏
- 句末 final 译文才进入 TTS
- TTS 播放中检测到对方发言时，发送 `playback.stop`

## 建议包结构

```text
services/realtime-audio/
├── main.go
├── config/
├── webrtc/                    # HTTP 信令和 PeerConnection 管理
├── audio/
├── vad/
├── segment/
├── asr/
├── translate/
├── tts/
├── pipeline/
├── playback/
└── session/
```

`webrtc` 对外提供 `/realtime/v1` 信令接口，并校验 `services/api` 签发的短期实时连接票据。
API Gateway 可以转发该路径，但 PeerConnection 和连接状态始终由本服务管理。
