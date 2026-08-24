# 流式 ASR 展示与 TTS 打断投递计划

## 状态

- 状态：待实施。
- 基线：`upstream/dev` 的命令运行时 PR 已合入后再开始本计划。
- 首期客户端范围：Web 入口 `apps/web`。协议保持可供 Mobile 和 Device 消费，但本计划不增加移动端页面。
- 单个 PR 总变更控制在 2,000 行以内；每个 commit 必须能够独立编译并通过其相关测试。

## 目标与非目标

1. 将 Qwen `qwen3-asr-flash-realtime` 的临时识别结果实时展示到 Web；翻译和 TTS 仍只使用句末 final 原文，不能流式翻译。
2. 当普通转译 Turn 的本地 Silero VAD 首次确认用户开口时，立即停止正在播放的 TTS。该规则必须同时适用于 PCM DataChannel 和 Opus WebRTC 下行。
3. 被打断的播放对应的已完成原文和译文，按一次、可重试的业务事实走 API 的现有投递体系，由已配置的企业微信等渠道发送。

不做以下事项：

- 不修改 KWS 唤醒语、Command Gate、命令 15 秒窗口、命令音频所有权转移、语义解释或 `command.result`。
- 不让 partial ASR 写入 FinalTurn、历史记录、翻译、TTS、用量或投递。
- 不在 realtime-audio 中直连企业微信，也不新建一条重复的 FinalTurn。
- 不以 Qwen server VAD 事件或纯音量峰值触发打断；权威触发点是普通本地 VAD 的 `EventOpened`。

## 现状与设计决定

Qwen ASR adapter 已将官方 `conversation.item.input_audio_transcription.text` 映射为
`asr.EventPartial`，并将 `conversation.item.input_audio_transcription.completed` 映射为 final。
当前普通 pipeline 没有消费 `Stream.Events()`，所以 partial 没有被送往前端。阿里云事件中的
临时文本应视为可修订的完整快照，客户端按同一 `turn_id` 替换显示内容，不按 token 追加。

现有 `FinalTurnEvent` 已含 `delivery_enabled`，API 已拥有企业微信等渠道、持久化 outbox 和重试。
因此打断事实必须引用已持久化的 `turn_id` 并以该 ID 去重；realtime 的播放取消不能成为投递成功的前置条件。

流式链路：

```text
Qwen ASR partial
  -> ordinary pipeline optional partial observer
  -> authenticated translation-events DataChannel: asr.partial
  -> Web transient subtitle

Qwen ASR final
  -> existing translation / FinalTurn / TTS path
  -> Web final subtitle replaces and clears transient subtitle
```

打断链路：

```text
ordinary VAD EventOpened
  -> runtime playback interrupter
  -> PCM or Opus playback.stop and local output cancellation
  -> durable playback-interrupted fact (final turn id, once)
  -> API delivery policy / outbox
  -> configured WeCom or other channel
```

## 与命令模式的冲突评估

实现有共享边界，但没有必然冲突。风险和隔离规则如下：

| 共享点 | 风险 | 本计划的隔离规则 |
| --- | --- | --- |
| ASR `Stream.Events()` | Command Gate 也使用同一个 provider 接口 | partial observer 只由 ordinary pipeline 创建；Command Gate 不订阅或转发 partial。 |
| VAD | 命令 Gate 有独立 Segmenter，且会接收从普通 VAD 转移的帧 | 打断只在普通 Turn 确认 `EventOpened` 后触发；已被 wake 转交或被 Gate 隔离的帧绝不触发普通 barge-in。 |
| Playback interruption | 命令唤醒已会停止播放 | 统一复用 `PlaybackInterrupter`，但原因值不同；不得改变 wake 时的 `wake_word_detected` 行为或反馈播放清理。 |
| DataChannel | `translation-events` 同时承载翻译、助手和命令结果 | 增加有版本、带 session/turn 身份的 `asr.partial` 事件；partial 是可丢弃的，不能阻塞 final 或 `command.result`。 |
| 投递 | FinalTurn 已可自动投递 | interruption 使用 `turn_id` 幂等标识，只补充/确认一次投递资格，不能再次写 FinalTurn 或由实时服务直投。 |

## PR 划分

本计划压缩为两个可独立评审、验证和回退的 PR：PR 1 负责 ASR partial 从服务端到 Web 的完整展示链路；PR 2 负责普通语音触发的跨编码 TTS 打断及既有投递体系接入。

### PR 1：ASR partial 流式展示

预计 1,600-2,400 行。

1. `feat(contracts): define ephemeral ASR partial event`
   - 在 `packages/contracts` 定义 `asr.partial` 的版本、`session_id`、`turn_id`、文本快照、源语言和发生时间。
   - 明确其不持久化、可丢弃、同 turn 覆盖和 final 后失效的语义；补充 schema/Go/TypeScript 验证测试。
2. `feat(realtime): publish ordinary ASR partial updates`
   - 在普通 pipeline 增加可选且有界的 partial observer；消费既有 `asr.Stream.Events()`，不改变 `Finish`、翻译或 FinalTurn 语义。
   - localruntime 通过已鉴权的 `translation-events` DataChannel 异步发送，慢客户端丢弃旧 partial 并保留 final 路径。
3. `feat(web): parse and render transient ASR partial`
   - 在既有 realtime event parser 中校验和解析 `asr.partial`，忽略错误版本、错误 session、过期 turn 和无效载荷。
   - 将临时原文放在 voice session 局部状态中；同一 turn 替换文本，不启动翻译或 TTS UI 状态。
   - `translation.final`、Turn 失败、会话停止、连接重建或 mode generation 失效时清理临时文本。
4. `test(realtime/web): cover partial isolation and settlement`
   - 覆盖更新、乱序/关闭、背压、final 后清理以及 Command Gate 未收到 partial 的边界。
   - 使用 DataChannel 事件测试连续更新、final 覆盖、重连和错误事件；补充 Web E2E 的可见性断言。

验收：Qwen fake provider 发出多次 partial 时，服务端可观察到同 turn 的 `asr.partial`；用户说话期间 Web 只更新原文临时字幕；没有 partial 进入翻译、TTS、FinalTurn、命令、投递或历史，译文仍只在 final 后改变。

### PR 2：普通语音触发的跨编码 TTS 打断与可靠投递

预计 2,300-3,500 行。

1. `feat(runtime): interrupt playback on ordinary speech start`
   - 在普通音频路径的首个 `vad.EventOpened` 调用运行时播放中断端口。
   - 要求该调用幂等、非阻塞、按 session 作用；Command Gate 认领的音频和 wake 逻辑不走该入口。
2. `fix(playback): settle PCM and Opus interruption consistently`
   - 对 PCM 下行取消排队/正在发送的 chunk，对 Opus 取消节拍发送并发送一次 `playback.stop`。
   - 记录被中断的已播放 Turn ID、原因、时间和编码；不能把取消当成 pipeline 失败或回滚 FinalTurn。
3. `feat(contracts): define playback interruption delivery fact`
   - 定义仅引用 `turn_id` 的 durable interruption fact 与幂等键，不复制原文、译文或会话权威状态。
4. `feat(api): queue interrupted turn output once`
   - API 消费该事实，读取已持久化 FinalTurn 的不可变快照，并在 `delivery_enabled` 和用户投递偏好允许时写入既有投递 outbox。
   - 以 `turn_id + interruption reason` 去重；重复事实、先到事实、投递失败重试和关闭渠道均保持可解释状态。
5. `test(realtime/api): verify barge-in media, command isolation and delivery policy`
   - 分别覆盖 PCM、Opus、无播放、并发重复开口、播放已自然结束、命令唤醒和普通 Turn 的隔离。
   - 用 fake WeCom provider 覆盖一次发送、重复消息、FinalTurn 尚未可读、重试和用户关闭自动投递。

验收：TTS 正在 PCM 或 Opus 播放时，普通 VAD 首次确认语音后播放立即停止，新语句仍完整进入既有 ASR/翻译流程；被打断的已完成转译在渠道已配置且投递开启时，原文和译文各只投递一次；企业微信失败可重试，realtime 不等待投递完成。

## 发布顺序和验证

PR 必须依次合并为 PR 1 -> PR 2。PR 1 执行相关 Go 包测试、contracts 校验、Web typecheck、Web 单测和 partial 可见性 E2E；PR 2 执行 realtime/API 相关 Go 测试、contracts 校验、播放打断 E2E、API delivery 集成测试，并在隔离的企业微信测试凭证下做一次真实渠道验收。

合并前不得把命令分支的文件与本计划的行为性改动混在同一 commit；若 `command/`、`runtime/command_*`、`controlchannel/` 或 KWS 契约出现并发修改，先 rebase 并以本文件的隔离规则做回归测试。

## 实施前默认约定

- “前端”按当前产品验收入口解释为 Web；Mobile/Device 只保持协议兼容，另立需求再做页面呈现。
- “流式输出”是原文临时快照，不是 token 增量，也不触发流式翻译。
- “投到企业微信这种”遵循已有用户投递偏好和已配置渠道；没有开启自动投递时不强制发送，也不因为打断绕过用户设置。
