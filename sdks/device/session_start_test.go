package device

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSessionStartClientSendsInitialMode(t *testing.T) {
	createdAt := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/voice-sessions/session-1/start" || r.Method != http.MethodPost {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer access-1" || r.Header.Get("Idempotency-Key") != "start-1" {
			t.Fatalf("headers = %#v", r.Header)
		}
		var request struct {
			InitialMode Mode `json:"initial_mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.InitialMode != ModeAssistant {
			t.Fatalf("body = %#v, %v", request, err)
		}
		_ = json.NewEncoder(w).Encode(VoiceSessionStartResult{
			ID: "session-1", AccountID: "account-1", Status: VoiceSessionActive, CreatedAt: createdAt,
		})
	}))
	defer server.Close()

	client := &SessionStartClient{
		BaseURL: server.URL,
		AccessToken: AccessTokenSourceFunc(func(context.Context) (string, error) {
			return "access-1", nil
		}),
	}
	result, err := client.Start(context.Background(), "session-1", ModeAssistant, "start-1")
	if err != nil || result.Status != VoiceSessionActive {
		t.Fatalf("Start() = %#v, %v", result, err)
	}
}

func TestSessionStartClientDefaultsToInterpretation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			InitialMode Mode `json:"initial_mode"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request.InitialMode != ModeInterpretation {
			t.Fatalf("initial mode = %q", request.InitialMode)
		}
		_ = json.NewEncoder(w).Encode(VoiceSessionStartResult{
			ID: "session-1", AccountID: "account-1", Status: VoiceSessionActive, CreatedAt: time.Now().UTC(),
		})
	}))
	defer server.Close()
	client := &SessionStartClient{BaseURL: server.URL, AccessToken: AccessTokenSourceFunc(func(context.Context) (string, error) {
		return "access-1", nil
	})}
	if _, err := client.Start(context.Background(), "session-1", "", "start-legacy"); err != nil {
		t.Fatal(err)
	}
}

func TestSessionStartClientRejectsErrorsAndMismatchedResponse(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		code int
		want error
	}{
		{name: "API conflict", body: `{"error":{"code":"mode_operation_conflict"}}`, code: http.StatusConflict, want: ErrOperationConflict},
		{name: "mismatched session", body: `{"id":"other","account_id":"account-1","status":"active","started_at":null,"ended_at":null,"created_at":"2026-08-12T00:00:00Z"}`, code: http.StatusOK, want: ErrInvalidResponse},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.code)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client := &SessionStartClient{BaseURL: server.URL, AccessToken: AccessTokenSourceFunc(func(context.Context) (string, error) {
				return "access-1", nil
			})}
			if _, err := client.Start(context.Background(), "session-1", ModeInterpretation, "start-1"); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}
