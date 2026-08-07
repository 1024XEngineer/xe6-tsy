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
├── translate/
├── tts/
├── pipeline/
├── playback/
└── session/
```

## Qwen provider adapters

The provider packages keep vendor protocol details outside `pipeline`:

- `asr/qwen` uses the Qwen realtime WebSocket endpoint. It sends `session.update`, streams PCM through `input_audio_buffer.append`, and sends `session.finish` before waiting for `session.finished`.
- `translate/qwen` uses the OpenAI-compatible `POST /chat/completions` endpoint with `qwen3.6-flash`. Thinking is disabled by default for turn-level latency.
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
| `ASR_SERVER_VAD` | _(unset → false in entrypoint)_ | Set `true` to enable Qwen server_vad; the local VAD keeps 500 ms prefix audio and preserves quiet frames inside an utterance |
| `LOCAL_VAD_PROVIDER` | `silero` | `silero` (default) or `energy` fallback |
| `LOCAL_VAD_MODEL_PATH` | `vad/silero/silero_vad.onnx` | Silero v5 ONNX model used by the local segmenter |
| `ONNXRUNTIME_SHARED_LIBRARY_PATH` | auto (`third_party/onnxruntime/lib/...`) | downloaded on first Windows start when missing |
| `LOCAL_VAD_THRESHOLD` / `LOCAL_VAD_NEG_THRESHOLD` | `0.5` / `threshold-0.15` | Silero speech start/end hysteresis |

Provider switch (Phase 3): keep `start-local.bat`, set `ASR_PROVIDER=aliyun` + `LLM_PROVIDER=aliyun` plus Qwen keys in root `.env`, restart. Leave downlink at `none` so TTS stays mock while you validate real subtitles. No control-plane protocol change.

Routes: `/realtime/v1/sessions/{id}/webrtc/config|offer`, `ice-candidates`, `start|stop`, `runtime`, `connection`.
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

Terminal media worker failures set `last_error_code=realtime_pipeline_failed` and emit a structured
`realtime pipeline worker failed` log with `session_id`, `operation_id`, `trace_id`, and the complete
wrapped error. This log is the diagnostic source for failures after signaling and lifecycle Start.

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
