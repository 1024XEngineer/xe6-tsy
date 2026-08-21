# 流式同声传译实施计划

## 背景与当前状态

当前实时链路已经具备普通同传 Turn、流式 ASR partial、VAD final、整句翻译、TTS
和 Web 瞬态状态的基础能力，但稳定短语尚未形成独立的协议、结算和播放生命周期。
本计划将短语字幕、短语翻译、连续播放、PCM 下行和可观测性拆成可灰度的阶段，保留
现有整句 `FinalTurn` 作为唯一持久化业务记录。

- 状态：Phase 1、Phase 2 和 Phase 3 已实现；Phase 4-5 待实施。
- 默认开关：短语字幕、Opus 短语播放和 PCM 流式播放均默认关闭，按阶段独立灰度。
- Phase 3 灰度使用 `REALTIME_PHRASE_SUBTITLES=enabled`、`REALTIME_PHRASE_PLAYBACK=enabled` 和
  `REALTIME_TTS_DOWNLINK=opus`；短语翻译事件在短语字幕开关打开后实时产生，短语 TTS 仍由独立播放开关控制。
- 兼容边界：关闭开关时，partial 预览、整句 FinalTurn、用量和现有媒体链路的行为必须不变。
- 术语：`utterance_id` 标识一次 VAD 开启到 final 的语音轮次；`phrase_sequence` 在轮次内从 1
  开始递增；源文、译文和状态事件均为瞬态事件，不替代最终记录。

## 目标与非目标

目标是让稳定短语在 VAD final 前按序展示并可选地翻译、播放，最终以已交付短语结果和
未处理残余构建一次整轮 FinalTurn 与用量事实。单个短语失败只保留源字幕并继续采集，
不会升级为 realtime pipeline 失败。

非目标是改变现有整句 final 路径、重建媒体链路、把 partial 写入历史或 FinalTurn，或在
首期引入服务端回声消除/DSP。普通同传仍保持麦克风上行与 TTS 下行全双工。

## 分期原则

各阶段按 Phase 1 -> Phase 2 -> Phase 3 -> Phase 4 的依赖顺序合并；Phase 5 负责实测
调优和观测。每个阶段都必须有协议/服务端测试，涉及 Web 的阶段还要运行 Vitest、
typecheck，并完成真实浏览器验收。真实设备录音集属于灰度验证材料，不塞入代码 PR。

会话级另设硬性内存/任务上限。触发时只取消未开始播放音频，记录告警，始终保留文本和字幕。

为避免翻译完成顺序影响播放顺序，必须按 `sequence` 交付给 TTS 队列。慢短语不能被后一个快短语越过；若需要提高吞吐，可并行翻译，但交付仍严格有序。

### 4. 流式播放协议

Opus 是首期低延迟下行：TTS provider 到达首个 audio chunk 后应立刻经现有 Opus track 发送，浏览器持续播放。每个短语使用独立 `playback_id`，并通过 sequence/generation 防止旧片段在强打断或会话重连后迟到播放。

PCM DataChannel 需要去除“完整缓存后 Complete 再发送”的行为，改为：

1. TTS chunk 到达立即分片发送；
2. DataChannel 事件标明 `playback_id`、短语序号、chunk sequence、encoding 和 final；
3. Web 使用持续 PCM 队列（优先 AudioWorklet）按短语序号衔接播放；
4. Cancel/strong interrupt 使浏览器丢弃对应 playback ID 的未播 chunk；
5. 同一 TTS chunk 的背压不能阻塞 ASR partial、VAD 读取或 FinalTurn 收尾。

所有播放输出都要区分以下动作：正常完成、当前轮字幕降级取消待播项、命令强打断、会话停止、provider 失败。只有强打断和会话停止允许取消正在播放项；当前轮字幕降级不允许。

### 5. 噪声、回声与 ASR 准确率

浏览器保持 `echoCancellation`、`noiseSuppression`、`autoGainControl`，并在支持的浏览器记录实际生效的 track settings。普通同传不会因为正在播放而关闭采集。

服务端的防线分层如下：

| 层级 | 措施 | 目的 |
| --- | --- | --- |
| 客户端 | AEC、降噪、自动增益、稳定的 microphone constraints | 优先消除本机 TTS 回声与背景噪声。 |
| VAD | Silero 阈值、滞回、最小连续语音时长与可配置静音结束 | 降低噪声错误打开或异常拉长 utterance 的概率。 |
| ASR | 保持原始有效语音帧，不把 VAD 判为 silence 的帧从活跃 utterance 波形中删除 | 避免削掉轻音、停顿和词尾造成识别下降。 |
| 短语提交 | 最小文本长度、稳定前缀、500ms 定时器、语气词/噪声过滤 | 阻止误识别直接生成翻译与音频。 |
| 播放期间诊断 | 记录 playback state、VAD 概率/RMS、partial 长度、最终文本与提交决定 | 验证 AEC 是否在实际扬声器、耳机和设备环境下可靠。 |

不应在服务端仅凭“当前有 TTS 播放”直接丢弃上行声音，因为那会破坏全双工。若真实设备验证显示浏览器 AEC 无法可靠消除回声，再单独评估以 TTS PCM 为参考信号的服务端 AEC/DSP；它属于高成本音频处理项目，不纳入首期功能承诺。

## 分期实施

### Phase 1：短语状态与文本正确性

1. 在 contracts 定义 phrase/subtitle 的瞬态事件及版本，包含 utterance ID、short phrase sequence、源文本、译文和状态。
2. 实现纯内存 `PhraseStabilizer`，覆盖标点、500ms 稳定前缀、ASR 修订、VAD final flush、最小长度与噪声过滤。
3. 在普通流式 ASR 路径创建/关闭 utterance context，partial 仍可继续驱动现有原文预览。
4. 增加短语字幕事件，但不接入 TTS；保留当前整句 FinalTurn 行为。

验收：持续讲话时，稳定短语在 VAD final 前按序显示；partial 回滚不产生重复字幕；静音 final 正确提交尾段；没有短语写入 FinalTurn 或触发重复用量。

### Phase 2：短语级翻译与整轮结算

1. 新增可取消、按 sequence 交付的 phrase translation task。
2. 让短语译文实时显示，保存用于最终整轮译文拼接或 reconciliation。
3. 重构 FinalTurn 收尾：整轮最终 ASR 和完整译文仍持久化一次；已处理短语不得重新翻译或重复记费。
4. 定义 provider 失败策略：单个短语失败保留源字幕并记录失败，不中断采集；VAD final 仍可尝试最终残余与整轮收尾。

验收：正常 utterance 的短语译文顺序正确，最终记录与可见字幕一致；一个短语翻译失败不会导致 realtime pipeline 失败或丢失后续短语。

### Phase 3：Opus 连续播放与本轮积压策略

1. 将短语译文接入 session 串行 playback scheduler 与独立 playback ID。
2. 实现每 utterance 5 个未完成 TTS segment 的计数、字幕降级、待播取消以及下一 VAD open 重置。
3. 保留普通同传期间的当前播放；仅 wake/command/stop 可强制中断它。
4. 对 `PlaybackAudioSink`、Opus sample track、runtime state 和浏览器播放顺序补全生命周期测试。

验收：用户连续讲话时，首个稳定短语可在 VAD final 前播放；同一轮达到 5 个未完成任务后只保留字幕，当前音频不中断；下一轮恢复 TTS；唤醒词和命令仍可立即停止播放。

### Phase 4：PCM 实时下行与浏览器连续队列

1. 将 `DataChannelTTSAudioSink` 从整段缓存模型改为实时 chunk 发送，并提供稳定的 backpressure/drop 规则。
2. Web 引入 PCM 流式播放队列，优先使用 AudioWorklet；无法使用时明确降级策略。
3. 补齐 playback ID、短语序号、chunk 乱序、迟到、取消、重连与多短语连续播放测试。

验收：PCM 与 Opus 均在 TTS 首 chunk 后开始播放，不再等待 TTS Finish；强打断和会话停止不会播放迟到 PCM；慢客户端不会阻塞服务端主链路。

### Phase 5：实测调优与可观测性

1. 为 `vad_open`、首 partial、短语稳定、翻译完成、TTS start、首音频 chunk、浏览器实际播放、VAD final 增加可关联的 latency checkpoints。
2. 建立真实麦克风、扬声器、耳机、噪声和回声录音测试集，覆盖中文、英文及中英混说。
3. 调整 Silero threshold、hysteresis、静音结束、最小文本长度与 500ms 稳定窗口；配置必须可灰度而非硬编码。
4. 按 session/设备类型观测误提交率、短语重叠率、TTS 队列长度、字幕降级率、AEC 回声疑似率和端到端延迟分位数。

## 验收指标

首期以实际设备网络为准，建议目标而非协议硬保证：

| 指标 | 目标 |
| --- | --- |
| ASR 首个 partial | 用户开口后 200-500ms。 |
| 稳定短语提交 | 识别到标点后不额外等待；无标点确认前缀稳定 500ms 后提交。 |
| 短语首音频 | 稳定短语提交后约 800-1200ms 内开始播放，取决于翻译与 TTS provider。 |
| 普通同传 | 用户说话期间当前 TTS 不停止，ASR 采集持续。 |
| 积压降级 | 单轮达到 5 个未完成 TTS segment 后，不丢文本/字幕，不中断当前播放，不再为该轮创建新的 TTS。 |
| 下一轮恢复 | 下一次 `vad.EventOpened` 自动恢复该轮 TTS 资格。 |
| 强打断 | wake/command 后当前播放与待播项可在可观测的短延迟内停止，且不导致 realtime pipeline 失败。 |

## 测试与发布要求

每个 phase 至少包含单元测试、包级集成测试和 Web 协议测试。涉及 `services/realtime-audio` 的改动至少执行：

```bash
go test ./packages/contracts/... ./services/realtime-audio/...
```

涉及 Web 播放或 DataChannel 协议的改动还需执行对应 TypeScript 单测、typecheck，并在浏览器完成以下人工验收：

1. 连续讲话、含逗号和句末标点的实时播放。
2. 无标点连续讲话触发 500ms 稳定提交。
3. VAD final 残余文本 flush。
4. TTS 播放期间继续讲话，确认不发生普通打断且 ASR 不明显回录 TTS。
5. 同一轮超过 5 段时字幕降级，下一轮恢复音频。
6. 唤醒词、命令、会话停止、断网重连时的强取消与迟到音频抑制。

发布采用 feature flag：先仅启用短语字幕，再灰度短语翻译，最后灰度 Opus 播放。PCM 流式播放独立开关，未经浏览器兼容性验证不得默认开启。
