// Package languages is the language-configuration module.
//
// Contract source: GitHub issue #88.
//
// This package currently exposes only empty boundary surfaces:
//   - HTTP handlers under /api/v1 that return 501 not_implemented
//   - LanguageConfigReader / LanguageTargetResolver stubs for the
//     session-management and realtime-translation modules
//
// Persistence, validation, versioning, and idempotency are intentionally
// unimplemented until the internal service is filled in.
package languages
