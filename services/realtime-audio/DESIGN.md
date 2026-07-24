# Realtime Audio Service Skeleton Design

## 1. Scope

This module owns the realtime media plane for a voice session. The initial
implementation provides testable service boundaries and mock implementations,
without integrating real WebRTC, ASR, translation, or TTS providers.

The module is responsible for:

- realtime runtime state;
- WebRTC signaling and connection lifecycle;
- ASR, translation, and TTS orchestration;
- per-session Turn allocation;
- reliable FinalTurn and UsageFact publication;
- interruption and playback control boundaries.

It does not update business session state, language configuration, speaker
records, usage summaries, billing, or message delivery owned by other members.

## 2. Service Boundaries

The control plane and media plane remain separate:

```text
services/api
  - owns business session state and language configuration
  - validates ownership and issues short-lived realtime tickets
  - calls RealtimeLifecycle.Start/Stop/GetRuntimeState

services/realtime-audio
  - owns runtime state and WebRTC resources
  - orchestrates ASR -> translation -> TTS
  - allocates turn_id and sequence_no
  - publishes FinalTurn and UsageFact events
```

`services/realtime-audio/webrtc` exposes `/realtime/v1` signaling endpoints.
Issue #84 references to `/api/v1` are treated as stale routing examples; API
Gateway may forward requests, but PeerConnection state remains owned here.

## 3. Delivery Stages

The skeleton is delivered as three reviewable PRs. Each PR must remain below
2,000 changed lines, and each commit must remain below 500 changed lines.

### PR 1: Session Lifecycle

```text
services/realtime-audio/
├── go.mod
├── DESIGN.md
└── session/
    ├── errors.go
    ├── model.go
    ├── ports.go
    ├── repository.go
    ├── service.go
    └── service_test.go
```

PR 1 establishes the module, lifecycle service, runtime repository, external
ports, in-memory fakes, and unit tests. It performs no real media processing.

### PR 2: Translation Pipeline

PR 2 adds `pipeline/`, `asr/`, `translate/`, `tts/`, and reliable sink adapter
boundaries. Providers and the durable Outbox implementation remain fakes, but
the complete final-result flow is exercised by tests.

### PR 3: Realtime Signaling

PR 3 adds `webrtc/`, interrupt handling, `/realtime/v1` HTTP handlers, service
wiring, and HTTP tests. PeerConnection operations remain mock implementations.

No branch push or PR creation happens without explicit user approval.

## 4. PR 1 Architecture

### 4.1 Domain Models

`SessionSnapshot` contains the member 1 business data needed to start realtime
processing: session ID, account ID, business status, audio configuration, and
capabilities. It intentionally excludes runtime state.

`RuntimeSnapshot` contains:

- session ID;
- runtime state;
- optional current Turn ID;
- optional current playback ID;
- optional last error code;
- update timestamp.

Supported runtime states are:

```text
stopped
starting
listening
asr_processing
translating
tts_processing
playing
stopping
failed
```

WebRTC connection state is modeled separately and must not be stored as a
runtime state.

### 4.2 Ports

`SessionReader` reads a session owned by member 1.

`LanguageConfigReader` reads the currently active language configuration owned
by member 2. Its method accepts only `sessionID`; member 2 neither supplies nor
receives `turnID`.

`PipelineManager` starts and stops the module-local processing pipeline.

`WebRTCConnectionManager` closes DataChannel, Tracks, and PeerConnection for a
session. The PR 1 implementation is a fake used to verify lifecycle ordering.

`RuntimeRepository` reads and saves runtime snapshots. The initial in-memory
implementation is safe for concurrent use and keeps stopped snapshots available
for state queries. A later Redis adapter may implement the same interface.

### 4.3 Lifecycle Start

`Start` follows this sequence:

1. Read the current runtime snapshot.
2. Return the existing snapshot when a pipeline is already running.
3. Read the business session through `SessionReader`.
4. Require business status `created`.
5. Save runtime state `starting`.
6. Start the pipeline.
7. Save and return runtime state `listening`.

Member 3 never changes business status. Member 1 changes the session from
`created` to `active` only after `Start` succeeds. If that update fails, member
1 calls `Stop` as compensation.

When pipeline startup fails, the runtime moves to `failed`, records a stable
error code, and remains retryable. Concurrent starts must not create two
pipelines for the same session; the lifecycle service serializes operations per
session rather than relying only on a read-before-write repository check.

### 4.4 Lifecycle Stop

`Stop` is idempotent. A missing or already stopped runtime returns a stopped
snapshot without creating or closing resources twice.

For an active runtime, `Stop`:

1. saves runtime state `stopping`;
2. cancels the pipeline and provider contexts;
3. closes DataChannel, Tracks, and PeerConnection;
4. clears current Turn and playback identifiers;
5. saves and returns runtime state `stopped`.

Success is returned only after cleanup completes. A cleanup failure moves the
runtime to `failed`, preserves a stable error code, and allows a later retry.

### 4.5 Runtime Reads

`GetRuntimeState` returns the current repository snapshot. Member 1 may compose
it into a detail or state response, but list APIs should not perform one remote
runtime lookup per session.

If an `active` business session has no runtime snapshot, the caller must report
`runtime_state_unavailable`; it must not invent a `stopped` state.

## 5. Turn and Pipeline Contracts

At the beginning of each utterance, member 3 atomically increments the
per-session sequence and generates the corresponding `turn_id`:

```go
type TurnAllocator interface {
	Next(ctx context.Context, sessionID string) (turnID string, sequenceNo int64, err error)
}
```

The same `turn_id` is passed through ASR, translation, TTS, FinalTurn,
UsageFact, and structured logs. It is sent to members 4 and 5. It is not
provided by member 2.

After allocating a Turn, member 3 calls
`LanguageConfigReader.GetCurrentConfig(ctx, sessionID)` exactly once and stores
the returned configuration in the Turn context. A language change affects the
next Turn only; the current Turn continues with its captured configuration and
version.

Only ASR final results trigger translation and TTS. Partial results may produce
ephemeral realtime events but are not persisted as FinalTurn records.

FinalTurn allows a nil participant ID with attribution status `pending`. It
includes actual source and target languages plus the captured language config
version.

UsageFact v1 includes event version, fact ID, trace ID, idempotency key, account
ID, session ID, Turn ID, service type, provider, model, token counts, audio
duration, decimal cost string, currency, and occurrence time.

Production FinalTurn and UsageFact sinks accept an event only after a durable
Outbox or equivalent reliable publisher has persisted it. An in-memory channel
is allowed only in tests and must never be the production delivery guarantee.

## 6. Error Handling

Domain errors are stable and testable, including missing runtime, invalid
business session status, invalid runtime transition, startup failure, and
cleanup failure. Dependency errors are wrapped with operation context.

Expected dependency failures update runtime state before returning. Tests use
fakes with injected failures to verify that callers receive both the error and
the resulting snapshot state.

## 7. Testing

PR 1 unit tests cover:

- successful start from a `created` business session;
- rejection of unsupported business session states;
- repeated and concurrent start behavior;
- startup dependency failure and retry;
- successful stop and cleanup ordering;
- missing-runtime and repeated stop behavior;
- pipeline and WebRTC cleanup failures;
- runtime repository concurrency and snapshot isolation;
- runtime state reads.

PR 2 adds pipeline tests for Turn allocation, language snapshots, ASR final
flow, target-language errors, pending speaker attribution, reliable publication,
UsageFact fields, and provider failures.

PR 3 adds HTTP tests for signaling validation, connection state, interruption,
idempotency, malformed requests, and mapped domain errors.

All tests use deterministic clocks, IDs, fake providers, and in-memory test
adapters. No test requires external network services or credentials.
