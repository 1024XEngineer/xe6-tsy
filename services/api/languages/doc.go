// Package languages is the language-configuration module.
//
// Contract source: GitHub issue #88.
//
// This package provides:
//   - Postgres schema migrations for supported_languages,
//     voice_session_language_configs, and internal speech-route metadata
//   - Store persistence with versioned active configs, idempotency keys,
//     and optimistic expected_version checks
//   - Service rules plus LanguageConfigReader / LanguageTargetResolver and
//     the shared SpeechRouteReader boundary
//   - HTTP /api/v1 language routes (auth + session ownership)
//
// Session ownership is provided by SessionOwnerReader. Production composition
// wires NewRecordsSessionOwner around recordstore.NewCanonicalSessionOwner so
// ownership matches turns and participants. LANGUAGE_SESSION_OWNER=trust-auth
// remains a local-only override that skips real ownership checks.
package languages
