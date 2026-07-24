package webapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/accounts"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/delivery"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/usage"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/webapi"
)

type deliveryFake struct {
	created delivery.CreateInput
}

func (f *deliveryFake) Create(_ context.Context, input delivery.CreateInput) (delivery.Message, error) {
	f.created = input
	return delivery.Message{ID: "message-1", AccountID: input.AccountID, Channel: input.Channel}, nil
}
func (*deliveryFake) Get(context.Context, string, string) (delivery.Message, error) {
	return delivery.Message{}, domain.ErrNotImplemented
}
func (*deliveryFake) Retry(context.Context, string, string, string) (delivery.Message, error) {
	return delivery.Message{}, domain.ErrNotImplemented
}
func (*deliveryFake) Preferences(context.Context, string) ([]delivery.Preference, error) {
	return nil, domain.ErrNotImplemented
}
func (*deliveryFake) PutPreference(context.Context, string, delivery.Channel, bool) (delivery.Preference, error) {
	return delivery.Preference{}, domain.ErrNotImplemented
}

func TestCreateMessagePassesAuthenticatedAccount(t *testing.T) {
	fake := &deliveryFake{}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/outbound-messages", strings.NewReader(
		`{"channel":"email","destination_ref":"verified-email","turn_ids":["turn-1"]}`,
	))
	request.Header.Set("X-Account-ID", "account-1")
	request.Header.Set("Idempotency-Key", "create-message-1")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusAccepted, response.Body.String())
	}
	if fake.created.AccountID != "account-1" || fake.created.IdempotencyKey != "create-message-1" || len(fake.created.TurnIDs) != 1 {
		t.Fatalf("unexpected input: %#v", fake.created)
	}
}

func TestInvalidMessageDoesNotReachService(t *testing.T) {
	fake := &deliveryFake{}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/outbound-messages", strings.NewReader(`{"channel":"email"}`))
	request.Header.Set("X-Account-ID", "account-1")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if fake.created.AccountID != "" {
		t.Fatal("service was called for an invalid request")
	}
}

func TestPlaceholderUseCaseReturnsNotImplemented(t *testing.T) {
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), delivery.NewUseCases())
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/anonymous", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotImplemented)
	}
	if !strings.Contains(response.Body.String(), `"code":"not_implemented"`) {
		t.Fatalf("unexpected body: %s", response.Body.String())
	}
}

func TestAccountUsageRejectsReversedPeriod(t *testing.T) {
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), delivery.NewUseCases())
	end := time.Now().UTC()
	start := end.Add(time.Hour)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/usage/summary?period_start="+start.Format(time.RFC3339)+"&period_end="+end.Format(time.RFC3339), nil)
	request.Header.Set("X-Account-ID", "account-1")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestErrorResponseIncludesRequestID(t *testing.T) {
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), delivery.NewUseCases())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/account/me", nil)
	request.Header.Set("X-Request-ID", "req-test-1")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if !strings.Contains(response.Body.String(), `"request_id":"req-test-1"`) {
		t.Fatalf("unexpected body: %s", response.Body.String())
	}
}

func TestCreateMessageRequiresUniqueTurnsAndEmail(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"unsupported channel", `{"channel":"sms","destination_ref":"verified","turn_ids":["turn-1"]}`},
		{"duplicate turn IDs", `{"channel":"email","destination_ref":"verified","turn_ids":["turn-1","turn-1"]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &deliveryFake{}
			handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/outbound-messages", strings.NewReader(test.body))
			request.Header.Set("X-Account-ID", "account-1")
			request.Header.Set("Idempotency-Key", "message-key")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("body %s: status = %d, want %d", test.body, response.Code, http.StatusBadRequest)
			}
			if fake.created.AccountID != "" {
				t.Fatalf("body %s reached service", test.body)
			}
		})
	}
}

func TestFormalRoutesReachUseCases(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		auth   bool
		key    bool
	}{
		{"create anonymous account", http.MethodPost, "/api/v1/auth/anonymous", "", false, false},
		{"create verification code", http.MethodPost, "/api/v1/auth/verification-codes", `{"phone":"+8613800000000"}`, false, false},
		{"log in by phone", http.MethodPost, "/api/v1/auth/phone/login", `{"challenge_id":"challenge-1","code":"123456"}`, false, false},
		{"refresh token", http.MethodPost, "/api/v1/auth/token/refresh", `{"refresh_token":"opaque"}`, false, false},
		{"log out", http.MethodPost, "/api/v1/auth/logout", `{"refresh_token":"opaque"}`, false, false},
		{"get account", http.MethodGet, "/api/v1/account/me", "", true, false},
		{"get session usage", http.MethodGet, "/api/v1/voice-sessions/session-1/usage", "", true, false},
		{"get account usage", http.MethodGet, "/api/v1/usage/summary?period_start=2026-07-01T00:00:00Z&period_end=2026-08-01T00:00:00Z", "", true, false},
		{"create outbound message", http.MethodPost, "/api/v1/outbound-messages", `{"channel":"email","destination_ref":"verified-email","turn_ids":["turn-1"]}`, true, true},
		{"get outbound message", http.MethodGet, "/api/v1/outbound-messages/message-1", "", true, false},
		{"retry outbound delivery", http.MethodPost, "/api/v1/outbound-deliveries/message-1/retry", "", true, true},
		{"get message preferences", http.MethodGet, "/api/v1/account/message-preferences", "", true, false},
		{"update message preference", http.MethodPut, "/api/v1/account/message-preferences/email", `{"enabled":true}`, true, false},
	}

	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), delivery.NewUseCases())
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			if test.auth {
				request.Header.Set("X-Account-ID", "account-1")
			}
			if test.key {
				request.Header.Set("Idempotency-Key", "test-key")
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusNotImplemented {
				t.Errorf("%s %s: status = %d, want %d; body=%s", test.method, test.path, response.Code, http.StatusNotImplemented, response.Body.String())
			}
		})
	}
}
