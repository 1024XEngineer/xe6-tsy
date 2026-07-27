package languages

import (
	"encoding/json"
	"net/http"
)

// ErrorBody is the standard API error envelope from issue #88 §4.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail carries a stable machine code plus human message.
type ErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func writeJSONError(w http.ResponseWriter, status int, code, message, requestID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorBody{
		Error: ErrorDetail{
			Code:      code,
			Message:   message,
			RequestID: requestID,
		},
	})
}

func requestIDFrom(r *http.Request) string {
	if id := r.Header.Get("X-Request-ID"); id != "" {
		return id
	}
	return "req_unimplemented"
}

// notImplemented writes the issue #88 501 not_implemented response.
func notImplemented(w http.ResponseWriter, r *http.Request) {
	writeJSONError(
		w,
		http.StatusNotImplemented,
		CodeNotImplemented,
		"language configuration API is not implemented yet",
		requestIDFrom(r),
	)
}
