# PR5 provider adapter plan

## Scope

PR5 completes the outbound Provider boundary that PR4's delivery runtime
consumes:

- `UnconfiguredProvider`, which fails closed when no sender has been injected;
- an explicitly injected `FakeEmailProvider` for offline demonstrations and
  unit tests;
- process-local duplicate suppression keyed by the durable `delivery_attempts.id`;
- sanitized request observations so the verified `ProviderTarget` is not
  returned by test helpers or persisted by delivery code;
- offline tests for success, retry after failure, permanent rejection,
  concurrent duplicate calls, invalid requests, and cancellation.

The adapter receives a complete `SendRequest`, but the core delivery package
owns the request shape and error classification. A real provider implementation
must pass `ProviderIdempotencyKey` to its vendor's idempotency facility when the
vendor supports one. The stable key is `delivery_attempts.id`; a caller key is
never used as a global provider key. The in-memory fake deliberately does not
claim this crash-safe capability.

## Out of scope

This PR does not change database tables, OpenAPI, account authentication,
WeCom configuration, or the public email/SMS contract. It does not add a real
SMS, WeCom, or email vendor, and it does not wire `main.go`, Valkey, or a worker
supervisor. Those changes belong to the follow-up composition PR after the
post-PR122 account-lineage migrations and records/session composition are
available.

## Runtime wiring reserved for PR6

PR6 must inject exactly one Provider into the Worker. The default must remain
`UnconfiguredProvider` and therefore fail closed; a missing adapter is requeued
as a configuration failure rather than marked as a provider rejection. Selecting
`FakeEmailProvider` must be an explicit local/demo choice and must never be
inferred from a missing credential.

PR6 must also freeze the deployment variable names before wiring the process.
The repository currently documents `REDIS_URL`; deployment work has also used
`VALKEY_URL`. Select one name rather than supporting two aliases:

```text
REDIS_URL or VALKEY_URL       # choose one in the composition PR
LINGOW_DELIVERY_STREAM
LINGOW_DELIVERY_GROUP
LINGOW_DELIVERY_CONSUMER
LINGOW_DELIVERY_DELAY_KEY
LINGOW_DELIVERY_BLOCK
LINGOW_DELIVERY_CLAIM_IDLE
LINGOW_DELIVERY_BATCH_SIZE
LINGOW_DELIVERY_MAX_LEN
LINGOW_DELIVERY_PROVIDER
```

## Acceptance

```text
verified destination + immutable message snapshot
 -> Provider.Send(attempt ID as idempotency key)
 -> provider success or classified error
```

The adapter must never report success when it is unconfigured. A successful fake
call is deduplicated for the same attempt key only during the lifetime of that
fake instance; a process restart or uncertain provider response is not treated
as crash-safe idempotency. Provider target data is available only during `Send`
and is absent from returned request observations.
Unknown acceptance and terminal database settlement remain Worker concerns from
PR4; they are not reimplemented in this PR.
