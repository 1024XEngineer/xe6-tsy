# Realtime Audio PR B Design

## Goal

Add the member-3 delivery and control-plane adapters on top of the contracts already present in `upstream/dev`, without importing PR A or changing another member's business or database implementation.

## Baseline and ownership

- Base is the refreshed `upstream/dev` commit used to create this worktree.
- `pipeline.FinalTurnEvent`, `pipeline.UsageFact`, their schema versions, event topics, and idempotency keys are frozen.
- `session.LifecycleService` owns realtime runtime start/stop and resource cleanup.
- `webrtc.SignalingService` and `ConnectionManager` own ticket validation, offer/answer, ICE idempotency, and transport closure.
- The API sessions module remains the owner of business `created/active/ended` state and its durable start/end operation identity. This PR does not write that state or its tables.
- Playback, TTS configuration, interrupt, browser WebRTC negotiation, and provider/database implementations from other PRs are outside this PR.

## Durable delivery adapter

The existing `pipeline.DurableOutbox` remains the only sink boundary:

```text
FinalTurnSink / UsageFactSink
  -> pipeline.DurableOutbox.Append
  -> member-3 outbox adapter
  -> injected durable writer
```

The adapter will expose a typed writer port owned by member 3. The writer accepts a canonical JSON payload, topic, idempotency key, and payload hash and returns an explicit durable-accept acknowledgement. The adapter maps a nil acknowledgement or ambiguous writer result to an error; it never reports delivery success before durable acceptance.

The adapter validates the already-frozen typed payloads before marshaling. FinalTurn uses `records/v1.FinalTurnTopic` and `event.EventID`; UsageFact uses `usage.recorded` and `fact.IdempotencyKey`. JSON encoding is stable and copied before the writer is called. A repeated `(topic, key, hash)` is successful, while a repeated key with a different hash returns a conflict and never overwrites the stored entry. Writer errors are returned unchanged or wrapped with a stable adapter sentinel so callers can retry the same immutable payload.

`MemoryOutbox` is an offline fake implementing the same writer/`DurableOutbox` contract. It stores canonical copies, supports injected transient failures, and records accepted entries for assertions. It is test infrastructure, not a production persistence claim.

No SQL table, migration, broker client, member-4 consumer, or member-5 usage implementation is added.

## HTTP control-plane adapter

The adapter is an independent `http.Handler` package with typed dependencies. It performs transport-level work only:

- extracts the Bearer realtime ticket and validates the session path before calling signaling;
- decodes one bounded JSON value with unknown fields and trailing content rejected;
- requires an idempotency key for start, stop, and offer operations;
- delegates start/stop to the injected lifecycle port and returns the authoritative runtime snapshot;
- delegates offer and ICE requests to `webrtc.SignalingService`, including `end_of_candidates`;
- reads runtime and typed WebRTC configuration through injected readers;
- never synthesizes business session state or writes a business repository.

The default route prefix is `/realtime/v1`, matching the realtime-audio README. Route registration is explicit so an API gateway can mount the handler under another prefix without changing behavior:

| Method | Route | Result |
| --- | --- | --- |
| POST | `/sessions/{session_id}/start` | lifecycle start snapshot |
| POST | `/sessions/{session_id}/stop` | lifecycle stop snapshot |
| GET | `/sessions/{session_id}/runtime` | current runtime snapshot |
| GET | `/sessions/{session_id}/webrtc/config` | typed ICE/media/data-channel config |
| POST | `/sessions/{session_id}/webrtc/offer` | typed SDP answer and connection metadata |
| POST | `/sessions/{session_id}/ice-candidates` | accepted/deduplicated candidate ids and end state |

The handler maps stable errors to the shared JSON error envelope: malformed input and missing identities are `400`, missing/expired ticket is `401`, ownership failure is `403`, missing session/runtime/connection is `404`, idempotency or lifecycle conflicts are `409`, request cancellation/deadline is `408`/`504`, and unknown dependency failures are `500`. Repeated stop, repeated identical offer/candidate, and transport close are successful idempotent replays; a different payload under the same idempotency key is a conflict.

Start/stop replay records reserve their key before invoking the lifecycle operation, so concurrent identical requests share one in-flight result and concurrent payload changes are rejected. Only successful results are retained, for a bounded ten-minute replay window, up to 4,096 records per handler, and up to 64 records per session; expired records are purged and a full cache returns `503 replay_capacity_exhausted` until capacity is available. Idempotency keys are limited to 256 UTF-8 bytes. JSON bodies are fully read up to the 1 MiB boundary so oversized trailing content cannot bypass the limit.

Start/stop HTTP calls represent realtime resource control. The API sessions service remains responsible for its separate business transition and operation reconciliation; no HTTP handler in this PR bypasses that contract.

## Verification design

Tests are written before implementation for each boundary:

1. Fake outbox tests cover canonical payloads, durable-accept acknowledgement, transient failure recovery, same-key replay, payload conflict, duplicate acknowledgement, and cancellation.
2. Sink tests prove FinalTurn/UsageFact schema, topic, version, and frozen idempotency keys are unchanged and that an unaccepted append is never reported as success.
3. HTTP contract tests cover every route, body validation, missing/expired/wrong-session tickets, wrong connection ids, repeated stop, repeated offer/candidate, end-of-candidates, timeout, and transport-close mapping.
4. A fake-backed end-to-end test exercises `HTTP start -> lifecycle -> offer/ICE -> pipeline FinalTurn/UsageFact durable acceptance -> HTTP stop` and asserts no duplicate durable records.

Real browser WebRTC and external provider/database tests remain separate integration work after PR A and PR B are both merged.

## Change budget

Implementation will be split into small commits: durable adapter and tests, HTTP adapter and contract tests, then the fake-backed end-to-end test and verification fixes. Each commit will stay below 500 changed lines and the total diff below 2,000 lines.
