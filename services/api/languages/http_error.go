package languages

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// ErrorBody is the standard API error envelope from issue #88 §4.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail carries a stable machine code plus human message.
type ErrorDetail struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details"`
}

func writeJSONError(w http.ResponseWriter, status int, code, message, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorBody{
		Error: ErrorDetail{
			Code:      code,
			Message:   message,
			RequestID: requestID,
			Details:   map[string]any{},
		},
	})
}

func requestIDFrom(r *http.Request) string {
	if id := r.Header.Get("X-Request-ID"); id != "" {
		return id
	}
	return "req_missing"
}

func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := http.StatusInternalServerError, CodeInternalError, "internal error"
	switch {
	case errors.Is(err, ErrInvalidRequest):
		status, code, message = http.StatusBadRequest, CodeInvalidRequest, err.Error()
	case errors.Is(err, ErrUnauthenticated):
		status, code, message = http.StatusUnauthorized, CodeUnauthenticated, "authentication required"
	case errors.Is(err, ErrForbidden):
		status, code, message = http.StatusForbidden, CodeForbidden, "session does not belong to the current account"
	case errors.Is(err, ErrSessionNotFound):
		status, code, message = http.StatusNotFound, CodeSessionNotFound, "session not found"
	case errors.Is(err, ErrNoActiveConfig):
		status, code, message = http.StatusNotFound, CodeNoActiveConfig, "no active language config"
	case errors.Is(err, ErrVersionConflict):
		status, code, message = http.StatusConflict, CodeVersionConflict, "expected_version does not match the active config"
	case errors.Is(err, ErrIdempotencyConflict):
		status, code, message = http.StatusConflict, CodeIdempotencyConflict, "idempotency key was reused with a different payload"
	case errors.Is(err, ErrUnsupportedLanguage):
		status, code, message = http.StatusUnprocessableEntity, CodeUnsupportedLanguage, err.Error()
	case errors.Is(err, ErrInvalidLanguagePair):
		status, code, message = http.StatusUnprocessableEntity, CodeInvalidLanguagePair, err.Error()
	case errors.Is(err, ErrDeliveryTargetRequired):
		status, code, message = http.StatusUnprocessableEntity, CodeDeliveryTargetRequired, "single output requires an enabled and verified delivery target"
	case errors.Is(err, ErrUnsupportedSourceLanguage):
		status, code, message = http.StatusUnprocessableEntity, CodeUnsupportedSourceLang, err.Error()
	case errors.Is(err, ErrNotImplemented):
		status, code, message = http.StatusNotImplemented, CodeNotImplemented, "language configuration dependency is not implemented yet"
	}
	writeJSONError(w, status, code, message, requestIDFrom(r))
}

func invalidRequest(detail string) error {
	return fmt.Errorf("%w: %s", ErrInvalidRequest, detail)
}
