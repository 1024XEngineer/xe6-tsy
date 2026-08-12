# 语音指令

当前联调前端**不再**通过本地 ASR 解析「开启单向传译」等语音指令。

语言对在设置面板配置，并在开始会话时写入：

`POST /api/v1/voice-sessions/{id}/language-configs`

实时识别/翻译/播报由 xe6-tsy realtime 管线负责；字幕来自 `turns` 轮询或 DataChannel `translation.final`。

新 Web 入口默认以助手模式启动，可用「小灵，开始对话」和「小灵，停止对话」唤醒或停止；旧的「开始翻译」和「停止翻译」短语继续兼容。助手回复通过 DataChannel `assistant.reply` 展示，不会伪装成翻译记录。
