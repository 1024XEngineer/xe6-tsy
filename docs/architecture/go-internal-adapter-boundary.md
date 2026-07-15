# Go Internal Package And Top-Level Adapter Boundary

## Context

The repository places the modular-monolith backend under `apps/api` and reusable provider adapters under top-level `adapters/` directories. Module 4 is implemented under `apps/api/internal/transcript` so its business state and service implementation remain private to the API application.

During the module 4 skeleton implementation, top-level fake adapters attempted to import provider interfaces and DTOs directly from `apps/api/internal/transcript`. Go rejected this dependency:

```text
use of internal package github.com/1024XEngineer/xe6-tsy/apps/api/internal/transcript not allowed
```

## Root Cause

Go permits imports of an `internal` package only from code located inside the directory tree rooted at the parent of `internal`.

For this repository:

```text
apps/api/internal/transcript
         ^
allowed import root: apps/api
```

Therefore, packages such as `adapters/asr/fake`, `adapters/translation/fake`, and `adapters/tts/fake` cannot import `apps/api/internal/transcript`. The same restriction will affect other top-level adapters that try to implement ports declared inside any `apps/api/internal/*` package.

## Decision

Provider-facing interfaces and transport DTOs that must be shared with top-level adapters live in:

```text
apps/api/pkg/speechport
```

This package contains only:

- ASR stream interfaces and ASR provider events;
- translation request/result DTOs and the translation provider interface;
- TTS request/result DTOs and the TTS provider interface;
- provider and model version references required at the boundary.

Module-owned state remains private under `apps/api/internal/transcript`, including:

- `SpeechProcessingRun`;
- immutable `SpeechUtterance`;
- speaker-role bindings;
- TTS task lifecycle state;
- stores, services, authorization rules, and events emitted by the module.

The internal module aliases or consumes public provider-boundary types but does not expose its authority state to top-level adapters.

## Rejected Alternatives

1. Move every adapter under `apps/api/internal`: this would discard the repository's current top-level adapter structure and make reuse by other applications harder.
2. Move the whole transcript module out of `internal`: this would unnecessarily expose business services and authoritative state.
3. Duplicate provider DTOs in each adapter and add mapping wrappers: this would create contract drift and repetitive conversion code without improving isolation.

## Consequences

- Other modules should declare externally implemented provider ports in a public, narrowly scoped package rather than inside `apps/api/internal/*`.
- Public port packages must contain boundary contracts only, not business entities or writable module state.
- Top-level adapters may import public port packages; internal modules may import adapters only in bootstrap/wiring code, keeping dependency direction explicit.
- Cross-frontend API contracts remain a separate concern. This decision does not resolve the existing `contracts/` versus `packages/contracts/` path conflict.

## PR Note

The following text can be reused in the implementation PR:

> While adding top-level fake speech adapters, Go's `internal` visibility rule prevented them from importing ports declared in `apps/api/internal/transcript`. Because this affects every top-level adapter targeting an `apps/api/internal/*` interface, the PR introduces the narrow public package `apps/api/pkg/speechport` for provider interfaces and transport DTOs. Module-owned runs, utterances, role bindings, stores, and services remain private under `apps/api/internal/transcript`.
