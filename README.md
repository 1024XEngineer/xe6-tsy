# Lingow

Lingow 是面向硬件载体和 Web/移动端演示入口的 AI 智能同传助手，首期支持两种语言面对面句级传译。

产品采用轮流说话、句末播音的交互方式：用户说话时系统在后台进行流式语音识别、翻译和上下文纠偏；一句话结束后，再通过 TTS 播放译文。

## 产品能力

- 语言选择
- 按钮或语音唤醒进入对话模式
- 自动语言识别
- 说话人识别
- 流式语音识别
- 双向翻译
- 句末 TTS 播放
- 抢话/打断处理

## 当前支持范围

- 每个会话支持一组双语语言对，默认 `zh-CN <-> en-US`。
- 支持 Web 和移动端页面骨架，兼容桌面端和手机浏览器。
- 支持 WebRTC 音频接入。
- 支持 ASR、翻译和 TTS provider 适配层。
- 首页仅显示最新一条字幕预览，点击进入后展示完整识别内容；不做管理后台、官网售卖、多人会议同传和自研硬件制造。

## 快速启动

```bash
pnpm install
docker compose -f infra/docker-compose.yml up -d

pnpm --filter web dev
pnpm --filter mobile dev

cd services/api && go run .
cd services/realtime-audio && go run .
```

默认端口：

| 服务 | 端口 |
| --- | --- |
| Web | `3000` |
| Mobile | `8081` |
| API | `8080` |
| Realtime Audio | `8090` |
| PostgreSQL | `5432` |
| Redis | `6379` |

## 文档

- [架构设计](docs/ARCHITECTURE.md)
- [开发说明](docs/DEVELOPMENT.md)
- [当前阶段范围](docs/CURRENT_SCOPE.md)
