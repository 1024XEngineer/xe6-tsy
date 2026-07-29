# PR6 runtime composition plan

## Scope

PR6 wires the persistent account, usage, and delivery boundaries into the API
process. The runtime is opt-in through `LINGOW_DELIVERY_RUNTIME=enabled`; an
unset or disabled flag keeps the existing fail-closed skeleton.

When enabled, startup performs the following in order:

```text
load and validate environment
 -> open PostgreSQL and apply record-store migrations
 -> apply language migrations on the same pool
 -> ping Valkey
 -> construct account HMAC issuer/verifier
 -> construct usage and delivery repositories/services
 -> start Outbox Dispatcher and Delivery Worker
 -> serve HTTP
```

The dispatcher and worker are supervised independently. A transient component
error is retried after a bounded delay; a missing Worker dependency is fatal.
Shutdown cancels both components before closing the HTTP server, Valkey client,
and PostgreSQL pool.

## Configuration

Required when enabled:

```text
DATABASE_URL
REDIS_URL
JWT_SECRET                       # at least 32 bytes
LINGOW_DELIVERY_DESTINATION_KEY  # base64url/raw base64, decodes to 32 bytes
```

Optional delivery settings use the `LINGOW_DELIVERY_*` prefix. The provider
defaults to `unconfigured` and therefore never reports a successful send.
`fake_email` is an explicit local/test-only choice. Production also requires an
explicit unique `LINGOW_DELIVERY_CONSUMER` value; a static consumer name must
not be shared by multiple API instances.

## Out of scope

This PR does not add a real SMS, WeCom, or email vendor, a sessions PostgreSQL
Repository, a realtime-audio production adapter, new database tables, or page
changes. Phone verification remains `not_implemented` until a
`VerificationSender` is explicitly injected.

## Acceptance

```text
anonymous auth -> verified Bearer context
 -> create message snapshot and durable outbox row
 -> dispatcher publishes to Valkey
 -> worker claims attempt
 -> provider sends with attempt ID as idempotency key
 -> terminal database transition commits
 -> broker ACK
```
