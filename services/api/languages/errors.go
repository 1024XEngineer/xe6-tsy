package languages

import "errors"

// Stable error codes for HTTP and internal callers (issue #88 §9).
const (
	CodeInvalidRequest         = "invalid_request"
	CodeUnauthenticated        = "unauthenticated"
	CodeForbidden              = "forbidden"
	CodeSessionNotFound        = "session_not_found"
	CodeNoActiveConfig         = "no_active_config"
	CodeVersionConflict        = "version_conflict"
	CodeIdempotencyConflict    = "idempotency_conflict"
	CodeStaleCommand           = "stale_command"
	CodeUnsupportedLanguage    = "unsupported_language"
	CodeInvalidLanguagePair    = "invalid_language_pair"
	CodeDeliveryTargetRequired = "delivery_target_required"
	CodeUnsupportedSourceLang  = "unsupported_source_language"
	CodeInternalError          = "internal_error"
	CodeNotImplemented         = "not_implemented"
)

// Sentinel errors for service/store/HTTP mapping.
var (
	ErrInvalidRequest            = errors.New(CodeInvalidRequest)
	ErrUnauthenticated           = errors.New(CodeUnauthenticated)
	ErrForbidden                 = errors.New(CodeForbidden)
	ErrSessionNotFound           = errors.New(CodeSessionNotFound)
	ErrNoActiveConfig            = errors.New(CodeNoActiveConfig)
	ErrVersionConflict           = errors.New(CodeVersionConflict)
	ErrIdempotencyConflict       = errors.New(CodeIdempotencyConflict)
	ErrStaleCommand              = errors.New(CodeStaleCommand)
	ErrUnsupportedLanguage       = errors.New(CodeUnsupportedLanguage)
	ErrInvalidLanguagePair       = errors.New(CodeInvalidLanguagePair)
	ErrDeliveryTargetRequired    = errors.New(CodeDeliveryTargetRequired)
	ErrUnsupportedSourceLanguage = errors.New(CodeUnsupportedSourceLang)
	ErrNotImplemented            = errors.New(CodeNotImplemented)
)
