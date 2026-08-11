# AI 对话助手与实时模式编排方案

关联：[Issue #213](https://github.com/1024XEngineer/xe6-tsy/issues/213)

状态：阶段 0 决策已冻结，尚未进入生产代码实现

代码基线：`upstream/dev@2cdb38bbd72cabafb6cd85d063aab84d84f77277`

## 1. 用户与问题

目标用户是通过硬件、Web 或 Mobile 使用 Lingow 实时语音能力的用户。

当前 `services/realtime-audio` 只有一条固定的句级同声传译链路。用户希望设备连接后默认可以进行
AI 对话，并通过“小灵小灵，开始同声传译”“停止翻译”等明确指令，在同一条实时连接内切换
助手和同声传译模式。

本方案解决以下问题：

- 不为 AI 对话、同声传译和未来模式分别建立 WebRTC 连接。
- 不复制 PCM、VAD、ASR、TTS、播放、打断和运行时观测基础设施。
- 将公共语音处理与模式专属业务处理分开。
- 防止命令音频被误当成普通对话或翻译内容。
- 让模式切换具备明确的状态归属、幂等和并发边界。
- 在保持现有同传可用的前提下逐步迁移。

## 2. 范围

本期交付：

- `assistant` 与 `interpretation` 两个可执行模式。
- 一个 `VoiceSession` 复用一个 Runtime 和一条 WebRTC PeerConnection。
- 普通业务 Turn 的公共 ASR 与 ASR final 后的模式路由。
- 助手回复、同传结果和共享语音输出的明确边界。
- 唤醒词后的有界命令窗口、类型化命令和模式原子切换。
- 旧客户端和旧调用继续按同传模式运行。

本期不交付：

- 英语口语训练实现、模式枚举、Provider、存储、事件或验收用例。
- 第二个 WebRTC 网关或第二套媒体处理管线。
- 独立部署的“AI 助手服务”。
- 通用自治 Agent、任意工具调用或由 LLM 直接修改运行状态。
- 新的 `ActivitySession` 持久化实体。
- 多人会议、流式同传或硬件传输协议重构。

英语口语训练只在架构图中以虚线扩展位出现，用于证明 Handler 边界可以扩展，不代表系统支持
该模式。

## 3. 当前代码基线

当前主链路是：

```text
runtime.Manager
  -> segment.Service
  -> VAD final event
  -> pipeline.TurnProcessor.ProcessAudio
  -> TurnOpener.OpenTurn
  -> ASR
  -> PipelineService.HandleASRFinal
  -> Translation
  -> FinalTurn
  -> TTS / Playback
```

现状约束：

- `segment.Service` 在 ASR 前完成 VAD 和语句切分。
- `TurnProcessor` 已经拥有公共 ASR，但 ASR final 仍固定交给 `PipelineService`。
- `runtime.Manager` 为每个 Session 保存一个 `entry`，适合作为模式运行时状态的所有者。
- `PipelineService.playTranslatedText` 已经集中负责 TTS 流、音频块、播放完成和取消。
- `RuntimeState` 表达 listening、ASR、translation、TTS、playing 等媒体进度，不表达业务模式。
- 当前不存在 `ModeState`、`ModeCoordinator`、`ModeRouter` 或 `AssistantHandler`。

因此本方案是在现有链路上增加边界，不重建 AudioIngress 或替换现有 WebRTC 实现。

## 4. 固定架构决策

### 4.1 服务数量不变

继续保持两个物理服务：

- `services/api`：账户、业务会话、授权、配置、审计、长期投影和持久化。
- `services/realtime-audio`：WebRTC、实时音频、模式实际状态、AI 处理、播放和打断。

新增的 Mode Router、Mode Coordinator、Handler 和 Command Gate 都是
`services/realtime-audio` 内部模块，不是新服务。

### 4.2 一条连接承载多个模式

一个 `VoiceSession` 对应一个 Runtime 和一条 PeerConnection。模式切换只替换后续 Turn 的
Handler，不执行 Runtime Stop/Start，不关闭 Track、DataChannel 或 PeerConnection。

只有以下情况关闭连接：

- 业务会话结束。
- 客户端主动断开。
- 连接租约或空闲超时。
- Runtime 不可恢复失败或服务关闭。

### 4.3 模式事实源在 realtime

`services/realtime-audio` 中的 `ModeState.active_mode` 是当前实际生效模式的事实源。

`services/api` 负责：

- 校验账户和会话权限。
- 保存初始模式和允许的模式配置。
- 保存 `mode.changed` 审计和查询投影。
- 提供跨实例路由所需的控制面能力。

API 不重复维护播放、识别或模式切换中的实时状态机，也不能先写“切换成功”再要求 realtime
追赶该状态。

### 4.4 模式状态与媒体状态分离

```text
RuntimeState
  stopped / starting / listening / asr_processing /
  translating / tts_processing / playing / stopping / failed

ModeState
  active_mode / generation / phase / last_operation_id /
  runtime_instance_id
```

`RuntimeState` 回答“媒体管线正在做什么”，`ModeState` 回答“下一轮业务语音交给谁处理”。两者
不能合并为一个枚举，否则模式与处理阶段会形成不可维护的组合状态。

### 4.5 普通 Turn 只执行一次公共 ASR

普通音频经过 VAD 后由 Turn Runner 打开 Turn、固定快照并执行一次 ASR。ASR final 再交给
Mode Router。Handler 不得再次识别同一段音频。

命令窗口是独立控制通道。它可以复用同一 ASR Provider，但命令音频、Turn 类型、用量和失败恢复
必须与普通业务 Turn 隔离。

### 4.6 Handler 从 ASR final 开始

```text
AssistantHandler
  ASR final -> LLM 对话 -> AssistantReply -> 可选 SpeechOutput

InterpretationHandler
  ASR final -> Translation -> FinalTurn -> 可选 SpeechOutput
```

WebRTC、PCM、VAD、Turn 打开和普通 ASR 都位于 Handler 之前。

### 4.7 事实类型不混用

- `FinalTurn` 只表示已经定稿的同传原文和译文。
- `AssistantReply` 表示助手回复，是独立实时事件。
- `ModeChanged` 表示成功的模式切换事实。
- `UsageFact` 按 ASR、LLM、Translation、TTS 等能力分别记录。

助手回复不能伪装成空译文 `FinalTurn`，否则会污染历史、投递和用量语义。

### 4.8 兼容策略

- 缺少模式字段的旧调用按 `interpretation` 处理。
- 在 InterpretationHandler 接入前，不把默认模式切换为助手。
- 新客户端完整上线后显式传 `initial_mode=assistant`。
- 未注册模式返回 `mode_not_available`，不能静默回退到同传。

## 5. 目标内部结构

架构图：[`diagrams/ai-assistant-mode-orchestration.drawio`](diagrams/ai-assistant-mode-orchestration.drawio)

预览：[`diagrams/ai-assistant-mode-orchestration.preview.png`](diagrams/ai-assistant-mode-orchestration.preview.png)

切换时序图：[`diagrams/ai-assistant-mode-orchestration-sequence.drawio`](diagrams/ai-assistant-mode-orchestration-sequence.drawio)

时序预览：[`diagrams/ai-assistant-mode-orchestration-sequence.preview.png`](diagrams/ai-assistant-mode-orchestration-sequence.preview.png)

目标处理链路：

```text
Device / Web / Mobile
  -> one WebRTC PeerConnection
  -> Audio Normalize
  -> Input Classifier
     -> Command Lane
        -> bounded command window
        -> Command ASR
        -> deterministic parser / bounded intent classifier
        -> ModeCoordinator
     -> Normal Lane
        -> VAD / Segment
        -> TurnOpener(mode + runtime instance + generation + language config)
        -> Shared ASR + ASR Usage
        -> ModeRouter
           -> AssistantHandler
           -> InterpretationHandler
           -.-> Future Handler placeholder
        -> Shared SpeechOutput
```

关键点：

- Input Classifier 依据已经打开的命令窗口分流音频，不直接根据自然语言猜测模式。
- Command Lane 不创建普通翻译 Turn，也不产生 `FinalTurn`。
- 普通 Lane 在 Turn 开始时固定模式、代次、Runtime 实例和语言配置。
- Router 是无状态分发边界；ModeCoordinator 是每个 Runtime 的状态所有者。
- SpeechOutput 从现有 `playTranslatedText` 泛化，保留播放、取消和 Usage 语义。

## 6. 核心状态和伪代码

以下是不可执行伪代码，只表达职责和并发约束。

```text
Mode = ASSISTANT | INTERPRETATION
InputPhase = NORMAL | COMMAND_CAPTURE
SwitchPhase = ACTIVE | SWITCHING

ModeState:
    active_mode
    generation
    phase
    runtime_instance_id
    last_operation_id

TurnSnapshot:
    turn_id
    session_id
    mode
    generation
    runtime_instance_id
    language_config_version

OnNormalUtterance(audio):
    state = ModeCoordinator.Snapshot()
    if state.phase != ACTIVE:
        return RetryableBusy

    turn = TurnOpener.Open(
        audio,
        mode = state.active_mode,
        generation = state.generation,
        runtime_instance_id = state.runtime_instance_id,
        language_config = ReadOnce()
    )

    asr_final = SharedASR.Recognize(turn.audio)
    PublishASRUsage(asr_final.usage)
    ModeRouter.Dispatch(turn.snapshot, asr_final)

OnWakeWord(command_window_id):
    InputClassifier.OpenCommandWindow(command_window_id, max_duration = 10s)
    CancelCurrentPlaybackOnly()

OnCommandAudio(audio):
    command_text = CommandASR.Recognize(audio)
    command = ParseKnownCommandOrClassifyToWhitelist(command_text)
    if command is invalid or ambiguous:
        RestoreNormalInputAndReplyError()
        return

    ModeCoordinator.Switch(command)

ModeCoordinator.Switch(command):
    SerializeBySession()
    ValidateRuntimeInstance(command.runtime_instance_id)
    ValidateExpectedGeneration(command.expected_generation)
    ReturnPreviousResultWhenOperationRepeated(command.operation_id)
    ValidateTargetModeRegistered(command.target_mode)

    if command.target_mode == state.active_mode:
        return NoopSuccess(state)

    state.phase = SWITCHING
    PauseOpeningNormalTurns()
    CancelUncommittedTurns(state.generation)
    CancelCurrentPlaybackOnly()

    state.active_mode = command.target_mode
    state.generation = state.generation + 1
    state.last_operation_id = command.operation_id
    state.phase = ACTIVE

    ResumeNormalTurns()
    PublishModeChangedReliably(state)

InterpretationHandler.Handle(turn, asr_final):
    translation = Translate(asr_final)

    CommitGate.WithCurrentGeneration(turn.snapshot):
        PublishFinalTurnOnce(translation)

    if CommitGate.IsCurrent(turn.snapshot):
        SpeechOutput.Play(translation.text)

AssistantHandler.Handle(turn, asr_final):
    reply = LLMConversation(asr_final.text)

    CommitGate.WithCurrentGeneration(turn.snapshot):
        PublishAssistantReply(reply)

    if CommitGate.IsCurrent(turn.snapshot):
        SpeechOutput.Play(reply.text)
```

### 6.1 为什么需要 runtime_instance_id

`generation` 只在当前 Runtime 实例内递增。realtime 进程重启后 generation 可能重新从初始值开始，
旧命令可能错误命中新 Runtime。`runtime_instance_id` 用于拒绝重启前创建的命令和异步结果。

### 6.2 为什么需要提交门

“先检查 generation，再提交 FinalTurn”仍有竞态：检查通过后可能立即发生模式切换。提交门必须让
generation 校验和不可变事实提交共享同一个串行化边界。已经成功提交的 `FinalTurn` 不回滚；模式
切换只阻止尚未提交的旧结果及后续播放。

## 7. 跨服务契约边界

所有正式契约放在 `packages/contracts`，第一阶段至少需要定义：

- `Mode`：只包含 `assistant`、`interpretation`。
- `ModeStateSnapshot`。
- `SwitchModeCommand` 和 `SwitchModeResult`。
- `ModeChangedEvent`。
- `AssistantReplyEvent`。
- 模式相关错误码。

关键字段：

```text
session_id
runtime_instance_id
operation_id
trace_id
expected_generation
from_mode
target_mode
resulting_generation
occurred_at
```

控制请求可以来自 API 到 realtime 的类型化接口，也可以来自已经鉴权的 DataChannel 控制消息。
无论入口来自哪里，都必须进入同一个 ModeCoordinator，不能形成两个状态修改路径。

## 8. 失败和恢复规则

- 命令窗口超时、空 ASR 或意图不明确：关闭窗口，恢复普通监听，不切换模式。
- operation 重放：返回第一次结果，不重复取消、切换、播音或发布事件。
- generation 冲突：返回明确冲突，由调用方读取最新快照后决定是否重试。
- runtime instance 不匹配：拒绝旧命令，不自动迁移到新 Runtime。
- 未支持模式：返回 `mode_not_available`。
- Assistant LLM 失败：不产生 AssistantReply，恢复 listening。
- Translation 失败：保持现有同传失败语义，不产生 FinalTurn。
- FinalTurn 已提交后 TTS 失败：不重新翻译、不重复提交 FinalTurn。
- ModeChanged 投影失败：realtime 的实际模式不回滚，通过可靠事件重试 API 投影。
- 连接断开：取消所有模式任务并释放 Runtime；重连建立新的 runtime instance。

## 9. 增量实施计划

每个提交“新增行 + 删除行”必须小于 2,000，并且提交后现有同传仍能运行。

### 提交 0：冻结架构决策

- 统一 Proposal、Issue、架构图和时序图。
- 删除重复 ASR、状态双写和模式切换重连等冲突描述。
- 不修改生产代码。

### 提交 1：模式契约

- 兼容增加 Mode、ModeState、SwitchModeCommand、ModeChanged 和错误码。
- 增加 `runtime_instance_id`、`operation_id`、`generation`。
- 缺少模式的旧调用仍按 `interpretation`。
- 同步 Schema、Go/TypeScript 生成物和消费者编译测试。

### 提交 2：Runtime ModeCoordinator

- 在每个 `runtime.entry` 增加独立 ModeState 和 Coordinator。
- 默认只注册 `interpretation`。
- 完成幂等、串行切换、generation 和 runtime instance 测试。
- 此时不改变 Turn 分发，现有同传路径继续工作。

### 提交 3：ASR final 后增加 ModeRouter

- 将 TurnProcessor 的下游从具体 PipelineService 改为窄接口。
- 增加无状态 ModeRouter。
- 先只用适配器注册现有 PipelineService 作为 InterpretationHandler。
- 用现有回归测试证明行为不变。

### 提交 4：Turn 快照和提交门

- Turn 打开时固定 mode、generation、runtime instance 和语言配置。
- ASR Usage 保持在公共 Turn Runner。
- generation 校验与 FinalTurn 提交共用提交门。
- 覆盖切换与翻译完成并发、过期 Turn 和已提交事实不回滚。

### 提交 5：共享 SpeechOutput 和 AssistantHandler

- 将现有 `playTranslatedText` 泛化为不依赖译文语义的 SpeechOutput。
- 保留现有 TTS Usage、播放完成、取消和 fallback 规则。
- 增加 AssistantHandler、LLM Usage 和 AssistantReply 实时事件。
- 仍不默认启用助手模式。

### 提交 6：类型化模式切换链路

- 增加 realtime 切换接口、API 授权代理和可靠 `mode.changed` 投影。
- 所有入口统一调用同一个 Coordinator。
- 不使用 Stop/Start 模拟模式切换。
- 覆盖重复、并发、过期和跨 Runtime 命令。

### 提交 7：Command Gate

- 在同一 Runtime 音频入口增加 NORMAL / COMMAND_CAPTURE。
- 命令窗口有最大时长、独立缓冲和明确恢复路径。
- 固定指令优先走确定性解析，必要时由受限分类器输出白名单命令。
- 命令音频不创建普通 Turn、FinalTurn 或 AssistantReply。

### 提交 8：客户端和硬件接入

- 在现有 PeerConnection DataChannel 增加上行控制消息。
- 接入硬件本地唤醒词及 Web/Mobile 交互。
- 新客户端显式请求 `initial_mode=assistant`。
- 增加不重连切换、播放中唤醒和断线恢复 E2E。

## 10. 防偏规则

后续实现必须持续满足：

1. 不增加第三个物理服务。
2. 不为每个模式创建新的 WebRTC 连接或 Runtime。
3. ModeCoordinator 不进入 `services/api`。
4. 普通业务音频不执行两次 ASR。
5. Handler 不接收原始 WebRTC Track 或 PCM FrameSource。
6. `FinalTurn` 不承载助手回复。
7. 模式切换不调用 Runtime Stop/Start。
8. 未实现模式不加入正式枚举和注册表。
9. 不复制现有 TTS、播放、取消和 fallback 实现。
10. 不绕过 `packages/contracts` 定义跨服务结构。
11. 不在同一个提交同时完成契约、Runtime、助手、命令窗口和客户端全链路。
12. 每个阶段先证明同传行为不回归，再开放新的默认行为。

## 11. 验收标准

### 架构验收

- 仍只有 `services/api` 和 `services/realtime-audio` 两个服务。
- 同一 Session 的助手和同传使用同一 PeerConnection。
- Mode Router 可以增加新 Handler，而不修改 WebRTC、VAD、普通 ASR 和播放模块。
- 英语口语训练只显示为不可调用的未来占位。

### 行为验收

- 旧客户端未传模式时，同传行为与当前版本一致。
- 新客户端可以在助手和同传之间双向切换且不重连。
- 命令音频不会生成翻译 FinalTurn 或普通助手回复。
- 重复、过期和并发命令不会重复切换。
- 已提交 FinalTurn 不因 TTS 失败或模式切换而重做。

### 测试验收

- contracts、realtime-audio、api 的默认测试离线通过。
- 新逻辑覆盖成功、输入错误、依赖失败和并发路径。
- 使用 fake/stub 隔离 ASR、LLM、Translation 和 TTS Provider。
- 不用固定 sleep 协调异步测试。
- 切换 E2E 能证明 PeerConnection 标识在切换前后不变。

## 12. 阶段 0 完成定义

- Proposal、Issue、架构图和时序图表达同一套状态归属和数据流。
- 图稿不再包含 `ActivitySession` 第一版要求。
- 公共 ASR 位于普通 Mode Router 之前，Command ASR 位于独立命令通道。
- 文档明确复用现有 `playTranslatedText` 并在后续泛化。
- 每个后续提交的边界、兼容行为和验收条件可独立评审。
