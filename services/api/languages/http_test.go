package languages

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPRoutesReturn501(t *testing.T) {
	mux := http.NewServeMux()
	NewHandler().Register(mux)

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{name: "list_languages", method: http.MethodGet, path: "/api/v1/languages"},
		{name: "get_current", method: http.MethodGet, path: "/api/v1/voice-sessions/vs_01TEST/language-config"},
		{name: "create_config", method: http.MethodPost, path: "/api/v1/voice-sessions/vs_01TEST/language-configs"},
		{name: "list_history", method: http.MethodGet, path: "/api/v1/voice-sessions/vs_01TEST/language-configs"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("X-Request-ID", "req_test_001")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501", rec.Code)
			}

			var body ErrorBody
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Error.Code != CodeNotImplemented {
				t.Fatalf("code = %q, want %q", body.Error.Code, CodeNotImplemented)
			}
			if body.Error.RequestID != "req_test_001" {
				t.Fatalf("request_id = %q, want req_test_001", body.Error.RequestID)
			}
		})
	}
}
