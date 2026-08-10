# 阶段 0/1 Realtime 执行方案

## 目标

本阶段冻结新的产品语义，并把 `realtime-audio` 的内部边界调整为可继续扩展的形态。

- 不新增独立服务，继续复用 `services/realtime-audio`。
- 不做普通对话助手；本期只有同声传译和唤醒后的命令识别。
- 普通语音仍按翻译链路处理；只有前端命中唤醒词并请求进入命令窗口后，语音才进入命令识别链路。
- 命令窗口默认 10 秒，由后端负责超时；VAD 只负责提供语句边界，不再跨模式累积音频。

## 阶段 0：冻结产品和契约

### 已冻结语义

| 场景 | 输入 | 处理 | 输出 |
| --- | --- | --- | --- |
| 翻译模式 | WebRTC 音频 | VAD -> ASR -> 翻译 | FinalTurn，按路由决定 TTS/投递 |
| 命令模式 | 唤醒后的同一条 WebRTC 音频 | VAD -> 命令音频快照 -> 后续 ASR/IntentClassifier | 结构化意图；不进入翻译/TTS |
| 未命中唤醒词 | 普通 WebRTC 音频 | 保持翻译模式 | 不调用意图模型 |

### 命令模式规则

1. 前端本地关键词模型只负责命中检测；命中后通过 HTTP 请求后端开启命令窗口。
2. 后端记录 `session_id`、`capture_id`、开始时间和截止时间，10 秒到期自动关闭。
3. 命令窗口复用现有 WebRTC 音频轨，不新增第二路音频上传。
4. 命令窗口内的 VAD final 只进入命令缓冲区；不得生成翻译 Turn、FinalTurn 或 TTS。
5. 窗口结束后一次性提交命令音频快照；后续 ASR 将其转换为文本，再交给意图识别层。空音频、超时和取消均丢弃缓冲区。
6. 意图识别模型只返回 allow-list 内的结构化意图，不能直接执行任意工具。
7. 当前只定义翻译控制意图；服务启动类意图留待后续能力路由阶段实现。

### 暂不实现

- 前端关键词模型和新的 HTTP Handler。
- IntentClassifier 的真实 LLM Provider。
- API/数据库中的命令审计、权限和幂等实现。
- 企业微信/Outbox 业务改造。
- 翻译上下文缓存和 Prompt 改造。

## 阶段 1：只改 realtime 内部边界

### 当前实现

```text
WebRTC FrameSource -> segment.Service(VAD) -> TurnProcessor -> PipelineService
                                                     -> ASR/翻译/TTS/FinalTurn
```

当前 `segment.Service` 直接把每个 VAD final 交给固定的翻译 `TurnProcessor`，没有模式、命令窗口或缓冲区隔离。

### 本阶段实现

```text
WebRTC FrameSource
        -> AudioIngress（由 `ingress.Dispatcher` + `segment.Boundary` 实现）
        -> TurnDispatcher
             ├─ TranslationHandler -> 现有 TurnProcessor/PipelineService
             └─ CommandHandler     -> 命令文本端口（本阶段只留接口）
```

### 修改清单

| 文件/模块 | 类型 | 之前 | 现在 |
| --- | --- | --- | --- |
| `services/realtime-audio/ingress/dispatcher.go` | 新增 | 无统一音频接收边界 | 管理模式、命令窗口、命令音频缓冲和翻译/命令分流 |
| `services/realtime-audio/pipeline/flow.go` | 调整 | Turn 请求没有模式代际信息 | 增加代际和结束时间，过滤跨模式的排队 VAD final |
| `services/realtime-audio/segment/service.go` | 调整 | VAD 状态不能响应模式切换 | 在边界代际变化时重置 VAD，并给 final 打代际标记 |
| `services/realtime-audio/vad/segmenter.go` | 调整 | 没有安全清空活动语句的入口 | 增加 `Reset`，清除当前语句和前缀缓存 |
| `services/realtime-audio/runtime/manager.go` | 调整 | 直接把 `segment.Service` 接到固定处理器 | 为每个 session 组装独立 Dispatcher，并提供内部 arm/close/cancel 方法 |
| 对应 `*_test.go` | 新增/调整 | 只验证翻译路径 | 增加模式切换、10 秒超时、缓冲区隔离和取消测试 |

### 验收标准

- 默认启动后为翻译模式，现有翻译单元测试行为不变。
- 命令窗口开启后，命令音频不会触发翻译、TTS 或 FinalTurn。
- 命令窗口关闭后缓冲区必定清空，下一段普通语音不会带入上一条命令。
- 超时由后端时钟决定，VAD final 只能提前结束采集，不能延长窗口。
- Stop、取消、音频 EOF 都能关闭接收循环，不泄漏 goroutine 或音频源。
- 本阶段不改变 WebRTC、Start/Stop、RuntimeSnapshot 等跨模块 HTTP 契约。

## 提交与 PR

阶段 0 和阶段 1 的改动规模预计低于 2000 行，且阶段 1 依赖阶段 0 的语义冻结，因此合并为一个 PR，拆成以下最小 commit：

1. `docs: freeze command-only product contract`：本执行文档及阶段边界。
2. `test: cover ingress mode and command window rules`：先写失败测试。
3. `feat: add realtime audio ingress boundaries`：实现 `AudioIngress`、`TurnDispatcher` 和翻译适配器。
4. `test: preserve translation pipeline behavior`：补充 runtime 集成回归测试。

前端 HTTP、IntentClassifier、API 投递和企业微信将在后续 PR 单独实现。
