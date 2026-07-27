# Voice session foundation

This package owns the business lifecycle for Issue #86 voice sessions.

## State ownership

| State | Owner | Persistence boundary |
| --- | --- | --- |
| `voice_sessions.status` | `services/api/sessions` | Persistent business lifecycle |
| `runtime_state` | `services/realtime-audio` | Media-plane runtime repository |
| `connection_state` | WebRTC connection manager | Live WebRTC connection state |
| language config | `services/api/languages` | Versioned language configuration |

The three session-related states are independent. This package never persists
runtime or connection state in `VoiceSession`.

## Ports and authorization boundaries

| Boundary | Provider | Consumer | Purpose |
| --- | --- | --- | --- |
| `Repository` | infrastructure adapter | session service | Persistent business state and atomic idempotency |
| `LanguageConfigReader` | language module adapter | session service | Verify an active bilingual config |
| `WebRTCConnectionReader` | realtime WebRTC adapter | session service | Verify connection readiness before start |
| `RealtimeLifecycle` | realtime session adapter | session service | Start, stop, and read runtime state |
| `SessionReader` | session module | realtime and account modules | Read an immutable business-session snapshot |
| `RuntimeFailureConsumer` | session module | realtime session adapter | Mark a cleaned-up unrecoverable runtime failure |

External user flows must read a session through `Repository.GetOwned` or list
through a `ListFilter` with a non-empty `AccountID`. `SessionReader.GetSession`
is reserved for trusted internal modules and is not an authorization boundary.
The two read paths must not be interchanged.

`RealtimeLifecycle` and `LanguageConfigReader` are consumer-owned ports. Their
providers do not directly implement these interfaces: follow-up adapters map
provider commands and snapshots explicitly. Adapters must exhaustively map
`RuntimeState`, `ConnectionState`, `EndReason`, language-config status, and time
fields; unchecked string conversion is not an integration contract.

## Create flow

```text
validate authenticated request
-> generate session ID
-> Repository.Create(session + create idempotency record)
-> return the created persistent session
```

Create does not start realtime, query runtime state, or create runtime records.

## Start flow

```text
Repository.GetOwned
-> if active, use TransitionToActive to validate and replay the stored result
-> otherwise require business status = created
-> require LanguageConfigSnapshot.Ready()
-> require ConnectionState.Ready()
-> RealtimeLifecycle.Start
-> Repository.TransitionToActive(created -> active + start idempotency result)
```

The realtime implementation reads a still-`created` session. If the final
transition fails after realtime startup, the service calls
`RealtimeLifecycle.Stop` as compensation and leaves the business session
`created`. `TransitionToActive` checks an existing idempotency record before
the expected-state condition, so a repeated matching start can return the
stored active session while the same key with a different hash conflicts.

## End and recovery flow

End-request idempotency belongs only to `EndIntent`; it is not repeated in
`EndTransitionParams`.

```text
serialize operations for session_id
-> Repository.GetOwned
-> Repository.SaveEndIntent(key + request hash + reason)
-> for active sessions: RealtimeLifecycle.Stop
-> require cleanup-confirmed RuntimeStopped
-> Repository.TransitionToEnded(expected current state -> ended)
-> Repository.CompleteEndIntent
```

A `created` session skips realtime Stop and transitions directly to `ended`.
For an `active` session, Stop failure, timeout, or unconfirmed cleanup leaves
the business status `active`, leaves `ended_at` unset, and preserves the
incomplete intent. A repeated request reads the intent:

- same idempotency key and request hash: resume incomplete work or return the
  completed result;
- same key with a different request hash: return
  `idempotency_key_conflict`;
- no intent: return `end_intent_not_found` when a recovery lookup is requested.

If Stop succeeds but the database transition fails, a retry invokes the
idempotent Stop again and retries the transition. An unrecoverable runtime
failure may use `TransitionToFailed` only after realtime confirms all owned
resources were cleaned up.

## Query flows

- Detail reads an owned persistent session and one live runtime snapshot.
- State reads only the owned business state and polling runtime fields.
- When a runtime snapshot is explicitly absent:
  - `created` sessions are represented as `stopped` using `created_at`;
  - `ended` sessions are represented as `stopped` using `ended_at`;
  - `active` sessions return `runtime_state_unavailable`.
- Runtime dependency failures and invalid snapshots are never synthesized as
  `stopped`.
- List returns `VoiceSessionListItem` values from persistent storage only.
- List never calls realtime per row or in batch and never filters by runtime or
  connection state.

`ErrRuntimeSnapshotNotFound` is an internal adapter boundary signal. It is not
an HTTP error code and must not be exposed directly to clients.

## Idempotency ownership

| Operation | Owner | Atomic result |
| --- | --- | --- |
| Create | `Repository.Create` | session + create request result |
| Start | `Repository.TransitionToActive` | `created -> active` + start request result |
| End | `Repository.SaveEndIntent` | end request identity and resumable completion state |

## Current slice

The service currently implements Create plus account-scoped Detail, State, and
List queries. Detail and State combine an owned persistent session with one
validated runtime snapshot; List remains persistent-only.

Start, End, runtime-failure handling, HTTP handlers, route registration,
OpenAPI, repositories, and production adapters belong to follow-up reviewable
slices. No stub in this package returns fabricated success data. It does not
change `main.go`, `go.work`, shared authentication, shared error responses, or
request-ID middleware.
