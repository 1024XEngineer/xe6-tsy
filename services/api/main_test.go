package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	recordswebapi "github.com/1024XEngineer/xe6-tsy/services/api/webapi"
)

func TestBuildMuxRegistersVoiceRecordRoutes(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		system bool
	}{
		{name: "list participants", method: http.MethodGet, path: "/api/v1/voice-sessions/vs_01/participants"},
		{name: "update participant", method: http.MethodPatch, path: "/api/v1/voice-sessions/vs_01/participants/p_01", body: `{"voice_profile_id":null}`, system: true},
		{name: "list session turns", method: http.MethodGet, path: "/api/v1/voice-sessions/vs_01/turns"},
		{name: "get turn", method: http.MethodGet, path: "/api/v1/voice-turns/vt_01"},
		{name: "correct attribution", method: http.MethodPatch, path: "/api/v1/voice-turns/vt_01/attribution", body: `{"participant_id":"p_01","attribution_status":"corrected"}`, system: true},
		{name: "list history", method: http.MethodGet, path: "/api/v1/translation-history"},
	}

	handler := buildMux()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var body io.Reader
			if test.body != "" {
				body = strings.NewReader(test.body)
			}
			request := httptest.NewRequest(test.method, test.path, body)
			ctx := recordswebapi.WithAccountID(request.Context(), "acct_01")
			if test.system {
				ctx = recordswebapi.WithSystemActor(ctx)
			}
			request = request.WithContext(ctx)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want %d, body = %s", response.Code, http.StatusNotImplemented, response.Body.String())
			}
			var errorResponse recordsv1.ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &errorResponse); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if errorResponse.Error.Code != recordsv1.ErrorNotImplemented {
				t.Fatalf("error code = %q, want %q", errorResponse.Error.Code, recordsv1.ErrorNotImplemented)
			}
		})
	}
}
