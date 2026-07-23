# services/api

Go 应用 API，负责会话、信令、语言配置和状态快照，不是管理后台。

## 职责

- 会话创建/结束
- WebRTC 信令：offer/answer、ICE candidate
- 可选语言列表和语言对配置
- 演示客户端/设备接入
- 会话状态快照查询
- 健康检查
- 必要的调试记录

## 非职责

- 不处理实时音频流
- 不直接调用 ASR/翻译/TTS
- 不维护播放状态机
- 不做账号组织、订单、套餐、发票、术语库和管理后台

## 建议包结构

```text
services/api/
├── main.go
├── config/
├── devices/
├── signaling/
├── sessions/
├── languages/
├── health/
└── webapi/
```
