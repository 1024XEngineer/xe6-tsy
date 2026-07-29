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

Realtime runtime ownership is frozen at this boundary:

- the same `SessionID` and durable Start `OperationID` is an idempotent
  `RealtimeLifecycle.Start` call and returns the latest snapshot for that
  runtime;
- the same `SessionID` with a different `OperationID` cannot claim an existing
  runtime and returns `ErrRealtimeAlreadyRunning` or
  `ErrConcurrentTransition`;
- `RuntimeSnapshot.StartOperationID` identifies the durable Start operation
  that owns the runtime instance.

The shared realtime contract does not yet expose this ownership field. A
follow-up production adapter must map it explicitly when the provider contract
is extended; this slice changes only the sessions consumer-owned port.

## Create flow

```text
validate authenticated request
-> generate session ID
-> Repository.Create(session + create idempotency record)
-> return the created persistent session
```

Create does not start realtime, query runtime state, or create runtime records.

## Start consistency

Every realtime Start attempt is coordinated by a repository-owned
`StartOperation`. The repository must create or replay the operation
idempotently before realtime is called, and must update the matching pending
operation to `completed` in the same transaction that changes the business
session from `created` to `active`.

Language and WebRTC readiness apply only before entering or continuing
`RealtimeLifecycle.Start`. A retry first reads the durable operation for the
same account, session, and idempotency key. An existing `compensating`
operation resumes Claim and Stop immediately with its persisted ClaimID,
regardless of the current language configuration or WebRTC connection state.

An in-process keyed locker may reduce duplicate work, but it is not a
cross-instance ownership boundary. A request may call `RealtimeLifecycle.Stop`
for Start compensation only after `ClaimStartCompensation` atomically confirms
that the session is still `created` and grants or idempotently restores the
matching operation's persisted compensation owner. Any denied or uncertain
claim strictly forbids Stop.

Successful cleanup changes the operation to `compensated`; failed cleanup is
persisted as `compensation_failed` so recovery does not depend on logs.

Operation status semantics are fixed as follows:

- `pending`: the same request may resume; a different request returns
  `ErrSessionStartInProgress`;
- `compensating`: one request owns cleanup and every other request is forbidden
  from stopping realtime;
- `completed`: the business session is `active`, and the same key and hash
  replay the completed operation;
- `compensated`: realtime cleanup completed; a new Start must use a new
  idempotency key;
- `compensation_failed`: cleanup is uncertain and new pipelines are forbidden
  until a follow-up recovery flow resolves it.

`Repository.GetStartOperation` enforces that conflict before readiness. When a
different key owns a `pending`, `compensating`, or `compensation_failed`
operation, the repository returns `ErrSessionStartInProgress`. A
`compensated` operation no longer blocks a new key.

Compensation claim recovery follows one ownership rule:

- `pending` may transition to `compensating` with one ClaimID;
- `compensating` may be reclaimed only by that persisted ClaimID;
- reclaiming with the same ClaimID is idempotent and returns `Claimed=true`;
- a different ClaimID receives `Claimed=false` and must not call
  `RealtimeLifecycle.Stop`;
- successful cleanup records `compensated`;
- failed cleanup records `compensation_failed`.

## Start flow

```text
Repository.GetOwned
-> if active, replay the matching completed StartOperation and return
-> otherwise require business status = created
-> read the matching durable StartOperation
-> if compensating, resume Claim + Stop with the persisted ClaimID
-> if pending, require readiness and continue RealtimeLifecycle.Start
-> if absent, require readiness and begin a durable StartOperation
-> RealtimeLifecycle.Start
-> after any uncertain error, read the latest RuntimeSnapshot once
-> require matching RuntimeSnapshot.StartOperationID
-> classify running, in-progress, stopped, or failed
-> Repository.TransitionToActive(created -> active + operation completed)
```

Every uncertain Start result, including `ErrRealtimeAlreadyRunning`, provider
errors, RPC timeouts, and connection loss, receives one runtime-state
reconciliation. The read and any confirmed activation use a fresh bounded
context that retains request values but does not inherit request cancellation.

A matching `listening`, `asr_processing`, `translating`, `tts_processing`, or
`playing` runtime completes activation. Matching `starting` or `stopping`
remains pending and returns the in-progress error. A missing, `stopped`, or
`failed` runtime remains pending and returns the original Start error, allowing
the same key to retry. Missing or mismatched runtime ownership returns a
concurrent transition without activation or Stop. Active-only runtime states
are acceptable recovery evidence only when `RuntimeSnapshot.StartOperationID`
matches the current durable operation.

The realtime implementation reads a still-`created` session. Compensation is
allowed only after the runtime is confirmed to belong to the current durable
Start operation and runtime validation or the final business transition then
fails. The service first claims repository-owned compensation authority. Only
`Claimed=true` permits `RealtimeLifecycle.Stop`; SessionID equality alone is
never cleanup authority. Successful cleanup persists `compensated`; Stop
failure or an invalid stopped snapshot persists `compensation_failed`. Every
Stop failure remains classifiable as `ErrRealtimeStopFailed` while preserving
cancellation, timeout, not-implemented, or provider causes. An interrupted
`compensating` operation resumes only with its persisted ClaimID. If a
competing instance has already activated the session, the denied claimant
replays the completed operation and never stops the valid pipeline.

Start operations for the same session are serialized by an in-process keyed
locker, while different Session IDs proceed independently. Lock waits honor
request cancellation and deadlines, and entries are reclaimed after the last
holder or waiter releases its reference. Repository operations and
compensation claims remain the cross-process consistency boundary.
Compensation retains request trace values and ignores client cancellation only
inside bounded steps. Claim, Realtime Stop, and terminal persistence each
receive a fresh independent timeout. A slow Claim therefore cannot consume the
Stop budget, and Stop may exhaust its deadline without preventing
`CompleteStartCompensation` or `FailStartCompensation` from attempting the
terminal write. If that fresh persistence attempt also fails, the operation
remains `compensating` so the same persisted ClaimID can resume cleanup later.

Every persisted Start lifecycle timestamp is obtained through one checked UTC
clock boundary. A zero timestamp before operation creation prevents the
operation write. A zero activation timestamp after realtime startup enters
owned compensation. A zero compensation-claim timestamp forbids Stop, and a
zero terminal timestamp leaves the operation `compensating` for recovery.

## End flow

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
That shortcut is safe only because `SaveEndIntent` and
`BeginStartOperation` form an atomic repository interlock: an unresolved Start
operation blocks End intent creation, and an incomplete End intent blocks a new
Start operation.

For an `active` session, Stop failure, timeout, or unconfirmed cleanup leaves
the business status `active`, leaves `ended_at` unset, and preserves the
incomplete intent. A client may repeat End with the same request identity:

- same idempotency key and request hash: replay the intent and run the basic End
  flow from the current persistent Session status;
- same key with a different request hash: return
  `idempotency_key_conflict`.

If Stop succeeds but the database transition fails, a retry invokes the
idempotent Stop again and retries the transition. This package exposes no
background End recovery entrypoint; End uses the request Context for every
Repository and Realtime call.

Only a valid `stopped` snapshot for the requested Session ID confirms cleanup.
`starting`, `stopping`, `failed`, missing timestamps, and dependency timeouts
all preserve the prior business status and incomplete intent. End does not poll
runtime state, retry Stop automatically, or convert a Stop failure to
`StatusFailed`.

An already `ended` or `failed` session remains immutable. A matching replay
returns that stored terminal result. `CompleteEndIntent` is called only when
the persisted intent is unfinished; an already-completed replay has no terminal
write. End never converts `failed` to `ended` and never calls Stop for either
terminal state.

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

`ErrRuntimeSnapshotNotFound` is an internal consumer-owned adapter boundary
signal. Realtime adapters must translate provider-specific missing-runtime
errors into this sentinel. For example, the adapter between
`services/realtime-audio/session` and this package must map the provider's
`ErrRuntimeNotFound` to `sessions.ErrRuntimeSnapshotNotFound`.

The session service must not import provider packages or compare error strings.
This sentinel is not an HTTP error code and must not be exposed directly to
clients.

## Idempotency ownership

| Operation | Owner | Atomic result |
| --- | --- | --- |
| Create | `Repository.Create` | session + create request result |
| Start | `Repository.BeginStartOperation` and `TransitionToActive` | durable request identity + atomic `created -> active` and `completed` |
| End | `Repository.SaveEndIntent` | end request identity and completion marker |

## Current slice

The service currently implements Create, account-scoped Detail, State, and
List queries, durable idempotent Start orchestration with repository-owned
bounded compensation and interrupted-owner recovery, and the basic idempotent
End flow with cleanup-confirmed terminal commits. `PostgresRepository`
implements the persistent Session, StartOperation, compensation, EndIntent,
and lifecycle-transition contracts against the final control-plane tables.
Detail and State combine an owned persistent session with one validated runtime
snapshot; List remains persistent-only.

Runtime-failure handling, HTTP handlers, route registration, OpenAPI,
production wiring, and realtime/language/WebRTC adapters belong to follow-up
reviewable slices.
No stub in this package returns fabricated success data. It does not change
`main.go`, `go.work`, shared authentication, shared error responses, or
request-ID middleware.
