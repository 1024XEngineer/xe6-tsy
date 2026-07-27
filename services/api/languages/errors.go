package languages

import "errors"

// Stable error codes for HTTP and internal callers (issue #88 §9).
const (
	CodeInvalidRequest        = "invalid_request"
	CodeUnauthenticated       = "unauthenticated"
	CodeForbidden             = "forbidden"
	CodeSessionNotFound       = "session_not_found"
	CodeNoActiveConfig        = "no_active_config"
	CodeVersionConflict       = "version_conflict"
	CodeIdempotencyConflict   = "idempotency_conflict"
	CodeUnsupportedLanguage   = "unsupported_language"
	CodeInvalidLanguagePair   = "invalid_language_pair"
	CodeUnsupportedSourceLang = "unsupported_source_language"
	CodeInternalError         = "internal_error"
	CodeNotImplemented        = "not_implemented"
)

// Sentinel errors for internal ports. HTTP maps these to the codes above.
var (
	ErrNotImplemented            = errors.New(CodeNotImplemented)
	ErrNoActiveConfig            = errors.New(CodeNoActiveConfig)
	ErrUnsupportedSourceLanguage = errors.New(CodeUnsupportedSourceLang)
)
