# 语音指令

当前联调前端只检测固定唤醒词「小灵小灵」，**不再**通过本地 KWS 或 ASR 解析业务指令。

语言对在设置面板配置，并在开始会话时写入：

`POST /api/v1/voice-sessions/{id}/language-configs`

实时识别/翻译/播报由 xe6-tsy realtime 管线负责；字幕来自 `turns` 轮询或 DataChannel `translation.final`。

Web 入口默认以助手模式启动。会话中检测到「小灵小灵」后，Web 只发送
`wake_word.detected`；后续如「开始同声传译，中译英」或「结束同声传译」统一由 realtime 的
Command ASR、语义解释器、Capability Registry 和 Mode Coordinator 处理。助手回复通过
DataChannel `assistant.reply` 展示，不会伪装成翻译记录。

活动会话提供 `常驻模式` 和 `唤醒词模式`。前者持续发送当前模式的普通语音；后者默认禁用
WebRTC 上行音轨，只保留浏览器本地 KWS。唤醒事件发送成功后开放一轮语音，收到对应
`command.result` 或 15 秒未完成时重新关闭。监听策略仅在客户端保存，不是第三种业务模式。

唤醒词模式仅在助手业务模式生效。同声传译固定使用常驻上行，同时继续运行本地 KWS；检测到
「小灵小灵」后仍发送 `wake_word.detected`，所以用户可以说「小灵小灵，结束同声传译」切回助手。
切回后恢复用户此前保存的助手监听策略。

唤醒后的内容不要求固定短语。语义解释器可将「开始中译英同传」归一为模式动作，也可将
「帮我查一下上海天气」归一为 `assistant_query` 并交给现有 Assistant Handler。普通助手问题只在
当前助手模式执行；同传期间应先说出切回助手的明确意图，避免助手回答和翻译输出混在同一轮。

Web 当前用 sherpa-onnx 在浏览器本地运行 KWS；这不是后端或所有设备的统一模型实现。ESP32-S3
可以使用板载 KWS 模型，但命中后同样只发送 `wake_word.detected`，不得在设备侧解析模式或语言。
命令终止结果通过 `command.result` 返回，Web 只展示结果并刷新权威 ModeState，不据此重放命令。
