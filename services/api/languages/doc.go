// Package languages is the language-configuration module.
//
// Contract source: GitHub issue #88.
//
// This package provides:
//   - Postgres schema migrations for supported_languages and
//     voice_session_language_configs
//   - Store persistence with versioned active configs, idempotency keys,
//     and optimistic expected_version checks
//   - HTTP / internal port stubs that return not_implemented until the
//     service layer is wired on top of Store
package languages
