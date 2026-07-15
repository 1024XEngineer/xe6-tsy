package reception

import (
	"fmt"
	"net/http"
)

const (
	CodeValidationFailed          = "VALIDATION_FAILED"
	CodeAccessContextInvalid      = "ACCESS_CONTEXT_INVALID"
	CodeAccessDenied              = "ACCESS_DENIED"
	CodeOrganizationScopeMismatch = "ORGANIZATION_SCOPE_MISMATCH"
	CodeConfigNotPublished        = "CONFIG_NOT_PUBLISHED"
	CodeConfigVersionMismatch     = "CONFIG_VERSION_MISMATCH"
	CodeProcessingContextExpired  = "PROCESSING_CONTEXT_EXPIRED"
	CodeReceptionNotAllowed       = "RECEPTION_NOT_ALLOWED"
	CodeRealtimeAudioNotAllowed   = "REALTIME_AUDIO_NOT_ALLOWED"
	CodeSessionNotFound           = "SESSION_NOT_FOUND"
	CodeBindingNotFound           = "BINDING_NOT_FOUND"
	CodeInvalidSessionState       = "INVALID_SESSION_STATE"
	CodeInvalidBindingState       = "INVALID_BINDING_STATE"
	CodeVersionMismatch           = "VERSION_MISMATCH"
	CodeIdempotencyConflict       = "IDEMPOTENCY_CONFLICT"
	CodeActiveMediaBindingExists  = "ACTIVE_MEDIA_BINDING_EXISTS"
	CodeUnsupportedFakeScenario   = "UNSUPPORTED_FAKE_SCENARIO"
	CodeMediaAttachFailed         = "MEDIA_ATTACH_FAILED"
	CodeMediaDetachFailed         = "MEDIA_DETACH_FAILED"
	CodeInternalError             = "INTERNAL_ERROR"
)

type Error struct {
	Code      string
	Message   string
	Retryable bool
	Details   map[string]any
	Cause     error
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }
func (e *Error) Unwrap() error { return e.Cause }

func businessError(code, message string) *Error {
	return &Error{Code: code, Message: message, Details: map[string]any{}}
}

func dependencyError(code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Retryable: true, Details: map[string]any{}, Cause: cause}
}

func statusForCode(code string) int {
	switch code {
	case CodeValidationFailed:
		return http.StatusBadRequest
	case CodeAccessContextInvalid, CodeAccessDenied, CodeOrganizationScopeMismatch, CodeReceptionNotAllowed,
		CodeRealtimeAudioNotAllowed, CodeProcessingContextExpired:
		return http.StatusForbidden
	case CodeSessionNotFound, CodeBindingNotFound:
		return http.StatusNotFound
	case CodeInvalidSessionState, CodeInvalidBindingState, CodeVersionMismatch, CodeIdempotencyConflict,
		CodeActiveMediaBindingExists, CodeConfigNotPublished, CodeConfigVersionMismatch:
		return http.StatusConflict
	case CodeUnsupportedFakeScenario:
		return http.StatusUnprocessableEntity
	case CodeMediaAttachFailed, CodeMediaDetachFailed:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
