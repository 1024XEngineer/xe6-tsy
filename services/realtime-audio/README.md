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

`webrtc` 规划通过 `/realtime/v1` 提供信令接口，并校验 `services/api` 签发的短期实时连接票据。
当前仅提供服务层信令骨架，尚未注册公网 HTTP 路由。后续可以由 API Gateway 转发该路径，
但 PeerConnection 和连接状态始终由本服务管理。

当前内存 manager 在 Offer 成功后产生初始的 `connecting` 快照，并支持读取当前连接及应用
`new/connecting/connected/disconnected/failed/closed` 状态回调。Pion Adapter 尚未接入，
因此骨架不会自动进入 `connected`，也不能作为 Pipeline 启动就绪依据。接入 Pion 后仍须以
`connected` 作为启动条件。`Close` 成功后删除快照，后续查询返回 `not_found`；`closed` 只作为
删除前可见的 transport 状态，不承诺持久保留。

当前票据校验也是 `Open` 前的单次授权检查。接入正式会话生命周期时，必须在 `Open` 准入点
重新校验可撤销的生命周期授权，或由 manager 强制校验 session generation/终止标记，使已通过
前置校验但尚未开户的旧请求无法越过 `Stop(session_id)`。

`Stop(session_id)` 必须幂等，并在返回成功前停止 Pipeline、取消 Provider Context、关闭
DataChannel、Track 和 PeerConnection。连接租约或空闲超时负责兜底清理失去控制面的孤立连接。
