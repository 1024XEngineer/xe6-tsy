# services/realtime-audio

Go 实时音频服务。

## 职责

- WebRTC config、offer/answer 和 ICE candidate 信令
- PeerConnection、DataChannel 和 Track 生命周期
- WebRTC 音频会话
- WebRTC audio track 接入
- 运行时会话状态机事实来源
- VAD 和句末检测
- ASR / 翻译 / TTS 编排
- 上下文纠偏
- 播放指令下发
- 抢话/打断处理
- 会话事件输出

## 首期规则

- 每个会话只支持一组双语语言对，默认 `zh-CN <-> en-US`
- 只支持两方面对面
- partial 结果只用于后台纠偏
- 句末 final 译文才进入 TTS
- TTS / 渠道输出可按 target_language 单独关闭
- TTS 播放中检测到对方发言时，发送 `playback.stop`

## 建议包结构

```text
services/realtime-audio/
├── main.go
├── config/
├── webrtc/                    # HTTP 信令和 PeerConnection 管理
├── audio/
├── vad/
├── segment/
├── asr/
├── assistant/
├── translate/
├── tts/
├── pipeline/
├── playback/
├── runtime/
└── session/
```

## Qwen provider adapters

The provider packages keep vendor protocol details outside `pipeline`:

- `asr/qwen` uses the Qwen realtime WebSocket endpoint. It sends `session.update`, streams PCM through `input_audio_buffer.append`, and sends `session.finish` before waiting for `session.finished`.
- `assistant/qwen` uses a dedicated assistant request contract over the OpenAI-compatible chat endpoint; it does not reuse translation prompts or publish translation `FinalTurn` records.
- `translate/qwen` uses the OpenAI-compatible `POST /chat/completions` endpoint with `qwen3.6-flash`. Thinking is disabled by default for turn-level latency. User content nests ASR text inside sanitized `<source>` tags with a language-aware translate instruction; meta-refusals trigger one reinforced retry, then persist `realtime_translation_rejected` while still publishing token usage.
- `tts/qwen` supports both Qwen3-TTS-Flash (`/services/aigc/multimodal-generation/generation`, `language_type`) and CosyVoice v3/v3.5 (`/services/audio/tts/SpeechSynthesizer`, `instruction`). CosyVoice instructions are generated from the target BCP-47 language so multilingual pairs can use the same stream port. For CosyVoice v3.5, configure a compatible designed voice with `TTS_VOICE`.
- `tts/qwen` supports Qwen3-TTS-Flash HTTP SSE, Qwen3-TTS-Flash-Realtime WebSocket (`wss://dashscope.aliyuncs.com/api-ws/v1/realtime`), and CosyVoice v3/v3.5 (`/services/audio/tts/SpeechSynthesizer`). Realtime synthesis sends `session.update`, `input_text_buffer.append`, and `input_text_buffer.commit`, then streams `response.audio.delta` PCM. Use `qwen3-tts-flash-realtime` with a multilingual voice such as `Cherry`; CosyVoice instructions are generated from the target BCP-47 language and v3.5 requires a compatible designed voice in `TTS_VOICE`.

The adapters are constructed explicitly from typed configuration values.
`config.LoadProviderConfigFromEnvironment` reads typed settings from the process environment, and
`config.BuildProviders` selects each adapter independently. The HTTP entrypoint (`server.go` /
`main.go`) assembles `runtime.Manager` with those providers plus `localruntime` WebRTC/media
adapters. The process does not load `.env` files automatically — export variables (or use
`start-local`) before `go run .`. Keep API keys in an ignored `.env`. The canonical selector keys
are `ASR_PROVIDER`, `LLM_PROVIDER`, and `TTS_PROVIDER`; each defaults to `mock` and currently
accepts `mock` or `aliyun`. Mock selection requires explicit offline provider instances, which
prevents a production startup from silently constructing fake behavior. Building Aliyun providers
validates credentials and endpoints but does not make a network request. Ordinary unit tests
continue to use offline fakes and never call a third-party service.

`runtime.ModeRouter` 为旧客户端保留 `interpretation` 初始模式。配置 Assistant Provider 后，
Router 同时注册 `AssistantHandler`；两个 Handler 复用同一个 `SpeechOutput` 和已有 WebRTC 连接。
助手回复通过 DataChannel 的 `assistant.reply` 事件发送，模型用量记录为 `assistant_llm`。

`GET /realtime/v1/sessions/{id}/mode` 返回 realtime 持有的权威模式快照；
`POST /realtime/v1/sessions/{id}/mode` 使用 `runtime_instance_id + expected_generation` 执行
幂等 CAS 切换。调用方应先读取快照，再提交目标模式；generation 或 runtime 实例不匹配时必须
重新读取，不能盲目覆盖。模式切换只替换 Router 状态，不执行 Session Stop/Start，也不重新建立
WebRTC。真正发生切换时，Coordinator 会先将 `realtime.mode.changed` 交给 Outbox，收到持久接受
确认后再提交 ModeState；重复 operation 和未变更模式不会重复产生事件。默认 `memory` 后端只用于
本地离线运行；生产环境应配置 `REALTIME_OUTBOX=valkey`。API 侧长期投影在后续独立阶段接入。

阶段 16 的模式观测使用结构化日志作为可聚合指标来源：`runtime_started` 只在 runtime entry
成功登记后记录一次，可按 `active_mode` 统计入口分布；`mode_switch` 按请求记录
`applied`、`unchanged` 或 `failed`，失败率按这些结果统计并用 `error_class` 分组；助手回复延迟
使用现有 `assistant_reply_done` 检查点，且附带 `mode`、`runtime_instance_id` 和 `generation`。
这些日志不是独立的 `/metrics` counter，聚合系统应按 `operation_id` 去重重试请求（如需操作级口径）。

## Local utterance VAD

The realtime entrypoint segments microphone audio with **Silero VAD** before ASR:

```text
WebRTC Opus → 16 kHz PCM frames → silero.Classifier → vad.Segmenter → ASR
```

`vad.Segmenter` is unchanged. Only the `vad.Classifier` implementation switched from
RMS energy to Silero (512-sample windows + 64-sample rolling context, start/end
hysteresis). On first Windows start, missing ONNX Runtime is downloaded into
`third_party/onnxruntime` automatically. Optional override:
`LOCAL_VAD_PROVIDER=energy`.


From the repo root on Windows, start API + realtime together (loads root `.env`):

```bat
start-local.bat
```

Or PowerShell:

```powershell
.\start-local.ps1                # both (realtime child window + API foreground)
.\start-local.ps1 -Service realtime
.\start-local.ps1 -Service api
```

Manual start without the launcher (process env only — this service does not auto-load `.env`):

```bash
export REALTIME_ADDR=:8090
export REALTIME_TICKET_SECRET='same-32+-byte-secret-as-api'
cd services/realtime-audio && go run .
```

Required env:

| Variable | Default | Notes |
| --- | --- | --- |
| `REALTIME_ADDR` | `:8090` | Listen address |
| `REALTIME_TICKET_SECRET` | _(required)_ | Raw secret (≥32 bytes), must match API `REALTIME_TICKET_SECRET` |
| `ASR_PROVIDER` / `LLM_PROVIDER` / `TTS_PROVIDER` | `mock` | `mock` or `aliyun` (same wiring; offline fakes injected for mock) |
| `REALTIME_TTS_DOWNLINK` | `none` | `none` = subtitles only (forces mock TTS); `pcm` = whole-clip TTS PCM over DataChannel; `opus` = 120ms-buffered, 20ms-paced WebRTC Opus at 32kbps |
| `REALTIME_SOURCE_LANGUAGE` / `REALTIME_TARGET_LANGUAGE` | `zh-CN` / `en-US` | Fallback pair when API DB link is off |
| `REALTIME_API_DATABASE` | _(off)_ | `enabled` + `DATABASE_URL` → Postgres session/language readers + FinalTurn outbox |
| `REALTIME_OUTBOX` | `memory` | `memory` 用于本地离线运行；生产设置为 `valkey`，需要 `REDIS_URL` |
| `LINGOW_MODE_CHANGED_STREAM` | `lingow:realtime:mode:changed` | `realtime.mode.changed` 的 Valkey Stream |
| `ASR_SERVER_VAD` | _(unset → false in entrypoint)_ | Set `true` to enable Qwen server_vad; the local VAD keeps 500 ms prefix audio and preserves quiet frames inside an utterance |
| `LOCAL_VAD_PROVIDER` | `silero` | `silero` (default) or `energy` fallback |
| `LOCAL_VAD_MODEL_PATH` | `vad/silero/silero_vad.onnx` | Silero v5 ONNX model used by the local segmenter |
| `ONNXRUNTIME_SHARED_LIBRARY_PATH` | auto (`third_party/onnxruntime/lib/...`) | downloaded on first Windows start when missing |
| `LOCAL_VAD_THRESHOLD` / `LOCAL_VAD_NEG_THRESHOLD` | `0.5` / `threshold-0.15` | Silero speech start/end hysteresis |

Provider switch (Phase 3): keep `start-local.bat`, set `ASR_PROVIDER=aliyun` + `LLM_PROVIDER=aliyun` plus Qwen keys in root `.env`, restart. Leave downlink at `none` so TTS stays mock while you validate real subtitles. No control-plane protocol change.

Routes: `/realtime/v1/sessions/{id}/webrtc/config|offer`, `ice-candidates`, `start|stop`, `runtime`, `mode`, `connection`.
Local adapters live under `localruntime/` (`TrustSessionReader`, `StaticLanguageConfigReader`, `StaticWebRTCConfig`, WebRTC frame/sink bridges).

`pipeline.NewPostgresFinalTurnSink(pool)` is the production final-turn sink adapter. It writes the
validated immutable event into the API service's PostgreSQL `final_turn_outbox`; the API consumer
worker owns receipt settlement and persistence into `voice_turns`.

Speaker evidence: the pipeline copies a non-empty `asr.FinalResult.ProviderSpeakerID` into the
FinalTurn event as `provider_speaker_id`. When the ASR/diarization provider returns no speaker key,
the turn stays `pending`, no attribution task or participant is created, and there is no implicit
`local-mic` fallback because a missing cluster key is not evidence of a single speaker. When a key
exists, the API async attribution worker uses it to build the stable participant mapping and
confirm/correct the turn.

Terminal media worker failures emit a structured `realtime pipeline worker failed`
log with `session_id`, `operation_id`, `trace_id`, `error_code`, and the complete
wrapped error. `last_error_code` is usually `realtime_pipeline_failed`; when the
translator abandons the task after a reinforced retry it is
`realtime_translation_rejected` instead. This log is the diagnostic source for
failures after signaling and lifecycle Start.

Official protocol references:

- [Qwen ASR realtime interaction](https://help.aliyun.com/zh/model-studio/qwen-asr-realtime-interaction-process)
- [Qwen ASR client events](https://help.aliyun.com/zh/model-studio/qwen-asr-realtime-client-events)
- [Qwen ASR server events](https://help.aliyun.com/zh/model-studio/qwen-asr-realtime-server-events)
- [Qwen-TTS API](https://help.aliyun.com/zh/model-studio/qwen-tts-api)
- [Qwen3.6 model release and model names](https://help.aliyun.com/zh/model-studio/newly-released-models)
- [Qwen DashScope API](https://help.aliyun.com/zh/model-studio/qwen-api-via-dashscope)
- [OpenAI-compatible DashScope API](https://help.aliyun.com/zh/model-studio/compatibility-of-openai-with-dashscope)

`main.go` 通过 `/realtime/v1` 暴露信令与生命周期 HTTP，并校验 `services/api` 签发的短期
实时连接票据。部署时可以由 API Gateway 转发该路径，但 PeerConnection 和连接状态始终由本服务管理。

当前入口使用 Pion transport factory + 内存 connection manager：Offer 成功后产生初始
`connecting` 快照，并在 Pion 回调下迁移到 `connected` / `failed` / `closed`。API Start 仍应以
`connected` 作为启动条件。manager 的 `Close` 成功后删除记录，后续查询返回 `not_found`。

当前票据校验也是 `Open` 前的单次授权检查。接入正式会话生命周期时，必须在 `Open` 准入点
重新校验可撤销的生命周期授权，或由 manager 强制校验 session generation/终止标记，使已通过
前置校验但尚未开户的旧请求无法越过 `Stop(session_id)`。

`Stop(session_id)` 必须幂等，并在返回成功前停止 Pipeline、取消 Provider Context、关闭
DataChannel、Track 和 PeerConnection。连接租约或空闲超时负责兜底清理失去控制面的孤立连接。

## Realtime mode metrics and alerts

`metrics` 包提供进程内、单调递增的实时运行计数器。它不记录
`session_id`、`turn_id`、`operation_id` 或 provider 名称，因此不会把高基数标识放入指标标签。
配置非空 `REALTIME_METRICS_TOKEN` 后才会注册 `GET /metrics`；采集请求必须携带
`Authorization: Bearer <token>`。生产 ingress 仍应限制该路径只允许内部监控访问。采集器应按 5 分钟窗口计算 counter delta；进程重启会将计数器归零，不能把累计值直接当作速率。

`mode_commands.total` 是已经通过鉴权、幂等键和请求校验并进入 runtime Coordinator 的命令数，以下结果字段互斥且总和必须等于 `total`：

- `applied_response` / `unchanged_response`：Coordinator 返回成功；精确重放仍属于响应结果，不代表再次发生状态切换。
- `generation_conflict` / `runtime_mismatch`：调用方持有的快照已过期或属于旧 runtime；这是客户端一致性信号，不是服务故障。
- `operation_conflict`：同一 operation 使用了不同 payload；通常表示调用方幂等键复用错误。
- `mode_unavailable` / `transition_pending`：模式未注册或前一个事件仍等待持久接受。
- `event_unavailable` / `other_failure`：模式事件无法持久化或发生未分类错误。

`mode_change_publications.attempted` 只统计真实状态变更的事件发布尝试；在一个已完成的采集窗口内应满足
`attempted = accepted + failed`。`accepted` 表示事件已被下游 outbox 接受，Coordinator 随后才会提交新的 mode/generation；它与命令响应计数有意分离。

建议告警规则（均要求窗口内分母至少 10 次，避免低流量误报）：

| 告警 | 5 分钟窗口条件 | 处理方向 |
| --- | --- | --- |
| 模式事件持久化失败 | `failed / attempted > 1%`，或 `event_unavailable` 增量大于 0 | 检查 Valkey/Outbox 可用性；在恢复前不要把 runtime mode 当作已长期保存 |
| 模式切换成功率下降 | `accepted / attempted < 99%` | 检查 outbox 延迟、连接和重试；该比例只针对确实发生切换的事件 |
| 过期客户端集中出现 | `(generation_conflict + runtime_mismatch) / total > 20%`，持续 15 分钟 | 检查客户端快照刷新、runtime 重启通知和请求重试退避；不要直接放宽 CAS |
| 幂等键冲突 | `operation_conflict / total > 5%`，持续 15 分钟 | 检查 operation_id 生成和重试 payload 是否稳定 |
| Provider 失败 | `provider_failures` 任一能力 5 分钟增量大于 5 | 结合结构化日志中的 `stage`、`provider` 定位 ASR、Assistant、翻译或 TTS 依赖 |
| DataChannel 投递失败 | `data_channel_failures` 5 分钟增量大于 5 | 检查连接状态、客户端消费和发送超时；FinalTurn 的持久提交不因此回滚 |
| Runtime 异常重启 | `runtimes_started - runtimes_stopped` 持续增长，或同一实例 15 分钟启动量超过正常会话基线 2 倍 | 检查 worker 失败日志、会话清理和控制面重试 |

旧 generation、旧 runtime 和 operation 冲突属于可预期拒绝，默认不触发服务不可用告警；只有持续超过上述阈值才升级为客户端或控制面异常。provider 失败、DataChannel 断开和 runtime 重启仍应沿用带 `session_id`/`turn_id` 的结构化日志及各自边界的阶段计数器，不能把这些标识写入本包的指标维度。

结构化日志按责任边界携带关联字段：

- `realtime mode switch resolved/rejected` 携带 `session_id`、`operation_id`、`runtime_instance_id`、`mode`/`target_mode` 和 `generation`；模式命令没有 `turn_id` 或 provider，因此不填充这两个字段。
- `realtime latency checkpoint` 和 `realtime provider failed` 携带 `session_id`、`turn_id`、`runtime_instance_id`、`mode`、`generation`，以及已从配置边界确定的 `provider`；Turn 不拥有模式命令的 `operation_id`，因此不强行关联。
- `realtime pipeline worker failed` 在失败发生于 Turn 之外时携带 `session_id`、启动 `operation_id` 和 `trace_id`；如果失败发生在 Provider/Turn 内，则同时使用上面的 Turn 日志。

当前阶段仍未宣称以下验收完成：上行 Control DataChannel、服务端 Command Gate/唤醒词、DataChannel 永久关闭后的重新绑定，以及浏览器与真实 Pion 的跨模式媒体 E2E。这些能力分别依赖阶段 12–14；本阶段验证现有 HTTP/Manager 模式控制、共享媒体输入、Pion 连接状态短暂断开后在同一 PeerConnection 上恢复、generation/Runtime 隔离、提交竞态和离线 Provider 失败矩阵。
