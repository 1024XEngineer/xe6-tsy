# services/api

Go 应用控制服务，负责业务会话、语言配置、数据访问和状态快照，不是管理后台，也不承载 WebRTC 连接。

## 职责

- 会话创建/结束
- 可选语言列表和语言对配置
- 演示客户端/设备接入
- 校验会话归属并签发短期实时连接票据
- 会话状态快照查询
- 健康检查
- 必要的调试记录

## 非职责

- 不处理实时音频流
- 不交换 SDP offer/answer 或 ICE candidate
- 不创建和保存 PeerConnection、DataChannel、Audio Track
- 不直接调用 ASR/翻译/TTS
- 不维护播放状态机
- 不做账号组织、订单、套餐、发票、术语库和管理后台

## 建议包结构

```text
services/api/
├── main.go
├── config/
├── devices/
├── sessions/
├── languages/
├── realtimeaccess/            # 会话鉴权和短期实时连接票据
├── health/
└── webapi/
```

WebRTC config、offer/answer 和 ICE candidate 由 `services/realtime-audio/webrtc`
统一处理。部署时可以由 API Gateway 转发 `/realtime/v1`，但本服务不实现信令逻辑。
