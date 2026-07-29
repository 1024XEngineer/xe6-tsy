package webapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/accounts"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/delivery"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/usage"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/webapi"
)

type deliveryFake struct {
	created          delivery.CreateInput
	retryAccountID   string
	retryMessageID   string
	retryIdempotency string
}

type tokenVerifierFake struct{}

func (tokenVerifierFake) VerifyAccessToken(_ context.Context, token string) (accounts.AccessTokenClaims, error) {
	if token != "access-token" {
		return accounts.AccessTokenClaims{}, domain.ErrUnauthorized
	}
	return accounts.AccessTokenClaims{AccountID: "account-1", SessionID: "session-1"}, nil
}

type accountFake struct {
	verifyPhoneCalled bool
	verifyPhoneCtx    context.Context
	verifyPhoneAnon   string
	phoneChallengeErr error
}

func (f *accountFake) CreateAnonymous(context.Context) (accounts.AuthResult, error) {
	return accounts.AuthResult{}, domain.ErrNotImplemented
}
func (f *accountFake) CreatePhoneChallenge(context.Context, string) (string, error) {
	if f.phoneChallengeErr != nil {
		return "", f.phoneChallengeErr
	}
	return "", domain.ErrNotImplemented
}
func (f *accountFake) VerifyPhone(ctx context.Context, _, _, anonymousAccountID string) (accounts.AuthResult, error) {
	f.verifyPhoneCalled = true
	f.verifyPhoneCtx = ctx
	f.verifyPhoneAnon = anonymousAccountID
	return accounts.AuthResult{Account: accounts.Account{ID: "registered-account"}}, nil
}
func (f *accountFake) Refresh(context.Context, string) (accounts.Tokens, error) {
	return accounts.Tokens{}, domain.ErrNotImplemented
}
func (f *accountFake) Logout(context.Context, string) error { return domain.ErrNotImplemented }
func (f *accountFake) Me(context.Context, string) (accounts.Account, error) {
	return accounts.Account{}, domain.ErrNotImplemented
}

func authenticate(request *http.Request) *http.Request {
	request.Header.Set("Authorization", "Bearer access-token")
	return request
}

func (f *deliveryFake) Create(_ context.Context, input delivery.CreateInput) (delivery.Message, error) {
	f.created = input
	return delivery.Message{ID: "message-1", AccountID: input.AccountID, Channel: input.Channel}, nil
}
func (*deliveryFake) Get(context.Context, string, string) (delivery.Message, error) {
	return delivery.Message{}, domain.ErrNotImplemented
}
func (f *deliveryFake) Retry(_ context.Context, accountID, messageID, idempotencyKey string) (delivery.Message, error) {
	f.retryAccountID = accountID
	f.retryMessageID = messageID
	f.retryIdempotency = idempotencyKey
	return delivery.Message{ID: messageID, AccountID: accountID, Status: delivery.MessageStatusRetrying}, nil
}
func (*deliveryFake) Preferences(context.Context, string) ([]delivery.Preference, error) {
	return nil, domain.ErrNotImplemented
}
func (*deliveryFake) PutPreference(context.Context, string, delivery.Channel, bool) (delivery.Preference, error) {
	return delivery.Preference{}, domain.ErrNotImplemented
}

func TestCreateMessagePassesAuthenticatedAccount(t *testing.T) {
	fake := &deliveryFake{}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/outbound-messages", strings.NewReader(
		`{"channel":"email","destination_ref":"verified-email","turn_ids":["turn-1"]}`,
	))
	request = authenticate(request)
	request.Header.Set("X-Account-ID", "forged-account")
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
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/outbound-messages", strings.NewReader(`{"channel":"email"}`))
	request = authenticate(request)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if fake.created.AccountID != "" {
		t.Fatal("service was called for an invalid request")
	}
}

func TestPhoneChallengeRateLimitUsesRetryableHTTPStatus(t *testing.T) {
	fake := &accountFake{phoneChallengeErr: domain.ErrRateLimited}
	handler := webapi.New(fake, usage.NewUseCases(), delivery.NewUseCases(), tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verification-codes", strings.NewReader(`{"phone":"+8613800000000"}`))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusTooManyRequests)
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Error.Code != "rate_limited" {
		t.Fatalf("error code = %q, want rate_limited", payload.Error.Code)
	}
}

func TestPlaceholderUseCaseReturnsNotImplemented(t *testing.T) {
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), delivery.NewUseCases(), tokenVerifierFake{})
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
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), delivery.NewUseCases(), tokenVerifierFake{})
	end := time.Now().UTC()
	start := end.Add(time.Hour)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/usage/summary?period_start="+start.Format(time.RFC3339)+"&period_end="+end.Format(time.RFC3339), nil)
	request = authenticate(request)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestErrorResponseIncludesRequestID(t *testing.T) {
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), delivery.NewUseCases(), tokenVerifierFake{})
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
			handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/outbound-messages", strings.NewReader(test.body))
			request = authenticate(request)
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

func TestCreateMessageRejectsOversizedTurnBatch(t *testing.T) {
	turnIDs := make([]string, recordsv1.MaxFinalTurnBatchSize+1)
	for index := range turnIDs {
		turnIDs[index] = "turn-" + strconv.Itoa(index)
	}
	body, err := json.Marshal(delivery.CreateInput{
		Channel:        delivery.ChannelEmail,
		DestinationRef: "verified",
		TurnIDs:        turnIDs,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	fake := &deliveryFake{}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/outbound-messages", strings.NewReader(string(body)))
	request = authenticate(request)
	request.Header.Set("Idempotency-Key", "oversized-message")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if fake.created.AccountID != "" {
		t.Fatal("oversized request reached service")
	}
}

func TestCreateMessageAllowsMaximumTurnBatch(t *testing.T) {
	turnIDs := make([]string, recordsv1.MaxFinalTurnBatchSize)
	for index := range turnIDs {
		turnIDs[index] = "turn-" + strconv.Itoa(index)
	}
	body, err := json.Marshal(delivery.CreateInput{
		Channel:        delivery.ChannelEmail,
		DestinationRef: "verified",
		TurnIDs:        turnIDs,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	fake := &deliveryFake{}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/outbound-messages", strings.NewReader(string(body)))
	request = authenticate(request)
	request.Header.Set("Idempotency-Key", "maximum-message")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusAccepted, response.Body.String())
	}
	if fake.created.AccountID != "account-1" || len(fake.created.TurnIDs) != recordsv1.MaxFinalTurnBatchSize {
		t.Fatalf("unexpected input: %#v", fake.created)
	}
}

func TestRetryPassesMessageResourceID(t *testing.T) {
	fake := &deliveryFake{}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/outbound-deliveries/message-1/retry", nil)
	request = authenticate(request)
	request.Header.Set("Idempotency-Key", "retry-message-1")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusAccepted, response.Body.String())
	}
	if fake.retryAccountID != "account-1" || fake.retryMessageID != "message-1" || fake.retryIdempotency != "retry-message-1" {
		t.Fatalf("unexpected retry input: account=%q message=%q key=%q", fake.retryAccountID, fake.retryMessageID, fake.retryIdempotency)
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

	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), delivery.NewUseCases(), tokenVerifierFake{})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			if test.auth {
				request = authenticate(request)
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

func TestClientSuppliedAccountIDIsNotTrusted(t *testing.T) {
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), delivery.NewUseCases(), tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/account/me", nil)
	request.Header.Set("X-Account-ID", "forged-account")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestInvalidBearerTokenCannotReuseInjectedAccountContext(t *testing.T) {
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), delivery.NewUseCases(), tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/account/me", nil)
	request.Header.Set("Authorization", "Bearer invalid-token")
	request = request.WithContext(webapi.WithAccountID(request.Context(), "forged-account"))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestAuthenticateMiddlewareInjectsVerifiedIdentity(t *testing.T) {
	var gotAccountID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccountID, _ = webapi.AccountIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
	handler := webapi.Authenticate(tokenVerifierFake{}, next)
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer access-token")
	request.Header.Set("X-Account-ID", "forged-account")
	request = request.WithContext(webapi.WithAccountID(request.Context(), "preexisting-account"))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if gotAccountID != "account-1" {
		t.Fatalf("account context = %q, want verified account", gotAccountID)
	}
}

func TestPhoneBindingRequiresBearerForMatchingAnonymousAccount(t *testing.T) {
	tests := []struct {
		name       string
		authorize  string
		anonymous  string
		wantStatus int
		wantCall   bool
	}{
		{name: "missing token", anonymous: "account-1", wantStatus: http.StatusUnauthorized},
		{name: "mismatched account", authorize: "Bearer access-token", anonymous: "other-account", wantStatus: http.StatusForbidden},
		{name: "matching account", authorize: "Bearer access-token", anonymous: "account-1", wantStatus: http.StatusOK, wantCall: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &accountFake{}
			handler := webapi.New(fake, usage.NewUseCases(), delivery.NewUseCases(), tokenVerifierFake{})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/phone/login", strings.NewReader(`{"challenge_id":"challenge-1","code":"123456","anonymous_account_id":"`+test.anonymous+`"}`))
			if test.authorize != "" {
				request.Header.Set("Authorization", test.authorize)
			}
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body.String())
			}
			if fake.verifyPhoneCalled != test.wantCall {
				t.Fatalf("VerifyPhone called = %v, want %v", fake.verifyPhoneCalled, test.wantCall)
			}
			if test.wantCall {
				accountID, ok := webapi.AccountIDFromContext(fake.verifyPhoneCtx)
				if !ok || accountID != "account-1" {
					t.Fatalf("service context account = %q (ok=%v), want account-1", accountID, ok)
				}
				if fake.verifyPhoneAnon != "account-1" {
					t.Fatalf("anonymous account ID = %q, want account-1", fake.verifyPhoneAnon)
				}
			}
		})
	}
}

func TestPhoneLoginWithoutAnonymousBindingRemainsPublic(t *testing.T) {
	fake := &accountFake{}
	handler := webapi.New(fake, usage.NewUseCases(), delivery.NewUseCases(), tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/phone/login", strings.NewReader(`{"challenge_id":"challenge-1","code":"123456"}`))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if !fake.verifyPhoneCalled {
		t.Fatal("public phone login did not reach account service")
	}
}
