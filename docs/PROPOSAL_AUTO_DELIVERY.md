# 逐句自动推送设计方案

关联：[Issue #176](https://github.com/1024XEngineer/xe6-tsy/issues/176)
状态：已确认

## 方案

系统在每个 Final Turn 落库后，根据译文语言的输出模式，决定是否播放 TTS，或是否自动发送到邮件、企业微信等投递目标。

- 双向模式：两个译文方向都播放 TTS，不创建自动投递消息。
- 单向模式：当前源语言的译文播放 TTS；反向译文不播放 TTS，只发送到一个已启用且已验证的自动投递目标。
- 两种模式都正常翻译、保存 Final Turn；单向模式只改变输出方式。
- 一个渠道只配置一个自动目标，每条渠道消息同时包含原文和译文。
- 同一个译文方向不能同时开启 TTS 和自动投递。

例如中文和英文互译时，可配置为：

| 翻译方向 | 译文语言 | TTS | 渠道发送 |
| --- | --- | --- | --- |
| 中文 -> 英文 | 英文 | 开启 | 关闭 |
| 英文 -> 中文 | 中文 | 关闭 | 开启 |

双向模式则将两个方向都配置为 TTS。单向模式需要账户存在有效自动投递目标；没有目标时，语言配置切换会被拒绝。

## 处理链路

```text
Turn 开始时固定读取输出规则
  -> ASR 和翻译完成
  -> Final Turn 异步落库
  -> tts_enabled=true：realtime 播放 TTS
  -> delivery_enabled=true：API 创建逐句异步消息
  -> Outbox -> Queue -> Worker -> SMTP / WeCom Provider
```

渠道发送失败不阻塞翻译或 Final Turn 保存。首轮目标失败时只重试失败目标一次；所有目标仍失败时，系统使用已保存的译文补播当前 Turn。补播成功后，从下一 Turn 恢复双向 TTS；用户在此期间手动切换到更新版本时，以用户配置为准。

## 需要改动

- 在语言配置快照中按 `target_language` 保存 `tts_enabled` 和 `delivery_enabled`，并在 Turn 开始时固定版本；API 同时派生返回 `output_mode=bidirectional|single`。
- 在消息配置中保存 `channel` 和唯一的 `destination_ref`。
- Final Turn 落库后新增自动发送调度器，复用现有 Message、Attempt、Outbox、Queue、Worker 和 Provider。
- 使用幂等键 `auto:final_turn:{turn_id}:{channel}:{destination_ref}`，避免重放产生重复消息。
- 放开当前 HTTP Handler 对 `wechat` 的限制。
- 前端接入输出语言规则、目标绑定、自动发送开关和投递状态。
- 移除 Web Demo 通过 Python Webhook 直发企业微信的旁路，统一走 Go 投递链路。

## 验收标准

1. 符合 `delivery_enabled` 规则的 Final Turn 落库后，会自动创建一条同时包含原文和译文的消息。
2. `tts_enabled=false` 时不播放该方向的 TTS，但翻译、落库和渠道发送正常执行。
3. 双向与单向输出模式可切换；单向模式的反向译文只投递不播报。
4. Final Turn 重放、Outbox 重发或 Worker 重启不会重复创建消息。
5. 邮件和企业微信均沿现有投递链路得到 `queued/sending/sent/failed` 状态。
