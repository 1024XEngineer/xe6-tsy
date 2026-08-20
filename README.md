# Lingow
[![Coverage](https://img.shields.io/endpoint?url=https%3A%2F%2F1024XEngineer.github.io%2Fxe6-tsy%2Fcoverage.json)](https://github.com/1024XEngineer/xe6-tsy/actions/workflows/go.yml)
![GitHub Actions Workflow Status](https://img.shields.io/github/actions/workflow/status/1024XEngineer/xe6-tsy/go.yml)
[![Github repo](https://img.shields.io/badge/github-repo-blue?logo=github)](https://github.com/1024XEngineer/xe6-tsy)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/1024XEngineer/xe6-tsy?filename=services%2Fapi%2Fgo.mod)
[![GitHub stars](https://img.shields.io/github/stars/1024XEngineer/xe6-tsy?style=social)](https://github.com/1024XEngineer/xe6-tsy)

Lingow 是面向硬件载体和 Web/移动端演示入口的 AI 智能同传助手，首期支持两种语言面对面句级传译。

产品采用轮流说话、句末播音的交互方式：用户说话时系统在后台进行流式语音识别、翻译和上下文纠偏；普通句子结束后通过 TTS 播放译文。显式启用 realtime 长句投递能力后，去除首尾空白的原文超过 50 个 Unicode 字符，或原声音频时长达到 20 秒时，译文跳过初始 TTS，只通过现有投递通道发送到企业微信；企业微信未绑定、未配置、目标无效或最终投递失败时再回放 TTS。该能力默认关闭，未启用时长句保持原 TTS 路由。

## 产品能力

- 语言选择
- 按钮或语音唤醒进入对话模式
- 自动语言识别
- 说话人识别
- 流式语音识别
- 双向翻译
- 句末 TTS 播放
- 长句企业微信字幕降级与失败 TTS 回放
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
- [数据设计](docs/DATA_DESIGN.md)
