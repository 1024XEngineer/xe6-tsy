# Voice session foundation

This package owns the business lifecycle for Issue #86 voice sessions.

## Responsibilities

- Persist `voice_sessions` and its `created`, `active`, `ended`, and `failed`
  business states.
- Preserve authenticated `account_id`, audio configuration, and terminal
  capability snapshots.
- Coordinate `Start` and `Stop` through the realtime lifecycle port.
- Read WebRTC readiness through a separate connection port.
- Combine persistent business state with a live runtime snapshot for detail and
  state queries.
- Provide an account-scoped, persistent-only list without runtime N+1 calls.

## Boundaries

This package does not persist `runtime_state` or `connection_state`, configure
language pairs, operate WebRTC resources, run ASR/translation/TTS, or write
turn and usage records.

| Boundary | Provider | Consumer | Purpose |
| --- | --- | --- | --- |
| `Repository` | infrastructure adapter | session service | Persistent business state and atomic idempotency |
| `LanguageConfigReader` | language module adapter | session service | Verify an active bilingual config |
| `WebRTCConnectionReader` | realtime WebRTC adapter | session service | Verify connection readiness before start |
| `RealtimeLifecycle` | realtime session adapter | session service | Start, stop, and read runtime state |
| `SessionReader` | session module | realtime and account modules | Read an immutable business-session snapshot |
| `RuntimeFailureConsumer` | session module | realtime session adapter | Mark a cleaned-up unrecoverable runtime failure |

The startup order is fixed:

```text
business status = created
-> validate language and WebRTC readiness
-> RealtimeLifecycle.Start
-> conditional business transition created -> active
```

If the final transition fails after realtime startup, the service must call
`RealtimeLifecycle.Stop` as compensation. Ending an active session performs the
inverse order: successful realtime cleanup first, then `active -> ended`.
An end request whose cleanup is not yet confirmed is stored as an `EndIntent`;
it does not change business status or `ended_at`.

## Current slice

This foundation defines domain models, errors, and ports only. Service
orchestration, HTTP handlers, route registration, OpenAPI, repositories, and
production adapters belong to follow-up reviewable slices. No stub in this
package returns fabricated success data.
