# apps/web

Lingow Web 对话入口。

## 职责

- 搭建响应式页面基础结构，兼容桌面端和手机浏览器
- 首页支持主按钮和语音唤醒，用于进入对话模式
- 语音唤醒仅在页面已打开且麦克风授权后生效
- 提供开始传译和语言选择页面/区域
- 进入后显示“Lingow 已进入对话模式”
- 动态展示自动语言识别结果，例如“已识别中文/英语”，后续可按配置展示“已识别法语/西班牙语”
- 展示语音识别、双向翻译和 TTS 播放组件的运行状态
- 预留后续面对面沟通、跨设备会话和多人会议入口
- 作为实时音频服务和设备 SDK 的产品验收入口

## 首期页面与状态

- `/` Lingow 首页
- `/interpret/start` 开始传译
- `/interpret/languages` 语言选择
- `idle`：展示等待按钮或语音唤醒
- `language_selected`：展示当前会话语言对
- `conversation_ready`：展示“Lingow 已进入对话模式”
- `language_detected`：展示已识别语言
- `speaking_translation`：展示正在语音合成播放

## 技术栈

- TypeScript
- Vue 3
- Vite
- Tailwind CSS

首期没有字幕、没有管理后台、没有控制台页面。Web 可以接入实时音频事件展示状态，但实时音频编排仍由 `services/realtime-audio` 负责。
