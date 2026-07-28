// Package languages is the language-configuration module.
//
// Contract source: GitHub issue #88.
//
// This package provides:
//   - Postgres schema migrations for supported_languages and
//     voice_session_language_configs
//   - Store persistence with versioned active configs, idempotency keys,
//     and optimistic expected_version checks
//   - Service rules plus LanguageConfigReader / LanguageTargetResolver
//   - HTTP /api/v1 language routes (auth + session ownership)
//
// Session ownership is provided by SessionOwnerReader from the session-management
// module. Until that adapter exists, main defaults to NotImplementedSessionOwner
// (session-scoped routes return 501) unless LANGUAGE_SESSION_OWNER=trust-auth.
package languages
