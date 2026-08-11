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
	created                delivery.CreateInput
	retryAccountID         string
	retryMessageID         string
	retryIdempotency       string
	targets                []delivery.MessageTarget
	messages               []delivery.Message
	automaticStatuses      []delivery.AutomaticOutputStatus
	messageListAccountID   string
	messageListLimit       int
	automaticStatusAccount string
	automaticStatusSession string
	automaticStatusLimit   int
	automaticStatusErr     error
	listAccountID          string
	listChannel            *delivery.Channel
	preferences            []delivery.Preference
	preferencesErr         error
	putPreferenceAccount   string
	putPreferenceChannel   delivery.Channel
	putPreferenceRef       string
	putPreferenceEnabled   bool
	putPreferenceErr       error
	bindAccountID          string
	bindToken              string
	revokeAccountID        string
	revokeChannel          delivery.Channel
	revokeRef              string
	bindEmailErr           error
	emailVerificationErr   error
	listTargetsErr         error
	revokeErr              error
}

type tokenVerifierFake struct{}

func (tokenVerifierFake) VerifyAccessToken(_ context.Context, token string) (accounts.AccessTokenClaims, error) {
	if token != "access-token" {
		return accounts.AccessTokenClaims{}, domain.ErrUnauthorized
	}
	return accounts.AccessTokenClaims{AccountID: "account-1", SessionID: "session-1"}, nil
}

type accountFake struct {
	verifyPhoneCalled    bool
	verifyPhoneCtx       context.Context
	verifyPhoneAnon      string
	phoneChallengeCalled bool
	phoneChallengeErr    error
}

func (f *accountFake) CreateAnonymous(context.Context) (accounts.AuthResult, error) {
	return accounts.AuthResult{}, domain.ErrNotImplemented
}
func (f *accountFake) CreatePhoneChallenge(context.Context, string) (string, error) {
	f.phoneChallengeCalled = true
	if f.phoneChallengeErr != nil {
		return "", f.phoneChallengeErr
	}
	return "", domain.ErrNotImplemented
}

func TestPhoneChallengeRejectsOversizedJSONBody(t *testing.T) {
	accounts := &accountFake{}
	handler := webapi.New(accounts, usage.NewUseCases(), &deliveryFake{}, tokenVerifierFake{})
	validBody := `{"phone":"15500000000"}`
	body := validBody + strings.Repeat(" ", 1<<20-len(validBody)+1)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verification-codes", strings.NewReader(body))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if accounts.phoneChallengeCalled {
		t.Fatal("oversized request reached service")
	}
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
func (f *deliveryFake) ListMessages(_ context.Context, accountID string, limit int) ([]delivery.Message, error) {
	f.messageListAccountID = accountID
	f.messageListLimit = limit
	return f.messages, nil
}
func (f *deliveryFake) ListAutomaticOutputStatus(_ context.Context, accountID, sessionID string, limit int) ([]delivery.AutomaticOutputStatus, error) {
	f.automaticStatusAccount = accountID
	f.automaticStatusSession = sessionID
	f.automaticStatusLimit = limit
	if f.automaticStatusErr != nil {
		return nil, f.automaticStatusErr
	}
	return f.automaticStatuses, nil
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
func (f *deliveryFake) Preferences(_ context.Context, accountID string) ([]delivery.Preference, error) {
	f.listAccountID = accountID
	if f.preferencesErr != nil {
		return nil, f.preferencesErr
	}
	return f.preferences, nil
}
func (f *deliveryFake) PutPreference(_ context.Context, accountID string, channel delivery.Channel, destinationRef string, enabled bool) (delivery.Preference, error) {
	f.putPreferenceAccount = accountID
	f.putPreferenceChannel = channel
	f.putPreferenceRef = destinationRef
	f.putPreferenceEnabled = enabled
	if f.putPreferenceErr != nil {
		return delivery.Preference{}, f.putPreferenceErr
	}
	return delivery.Preference{AccountID: accountID, Channel: channel, DestinationRef: destinationRef, Enabled: enabled, Verified: true}, nil
}
func (f *deliveryFake) ListMessageTargets(_ context.Context, accountID string, channel *delivery.Channel) ([]delivery.MessageTarget, error) {
	f.listAccountID = accountID
	f.listChannel = channel
	if f.listTargetsErr != nil {
		return nil, f.listTargetsErr
	}
	return f.targets, nil
}
func (f *deliveryFake) RequestEmailBindVerification(_ context.Context, accountID, email, destinationRef string) error {
	f.bindAccountID = accountID
	f.bindToken = email + ":" + destinationRef
	if f.emailVerificationErr != nil {
		return f.emailVerificationErr
	}
	return f.bindEmailErr
}
func (f *deliveryFake) BindEmailTarget(_ context.Context, accountID, token string) (delivery.MessageTarget, error) {
	f.bindAccountID = accountID
	f.bindToken = token
	if f.bindEmailErr != nil {
		return delivery.MessageTarget{}, f.bindEmailErr
	}
	return delivery.MessageTarget{DestinationRef: "primary-email", Channel: delivery.ChannelEmail, Verified: true}, nil
}
func (f *deliveryFake) BindWeChatTarget(_ context.Context, accountID, code string) (delivery.MessageTarget, error) {
	f.bindAccountID = accountID
	f.bindToken = code
	if f.bindEmailErr != nil {
		return delivery.MessageTarget{}, f.bindEmailErr
	}
	return delivery.MessageTarget{DestinationRef: "primary-wechat", Channel: delivery.ChannelWeChat, Verified: true}, nil
}
func (f *deliveryFake) RevokeMessageTarget(_ context.Context, accountID string, channel delivery.Channel, destinationRef string) error {
	f.revokeAccountID = accountID
	f.revokeChannel = channel
	f.revokeRef = destinationRef
	return f.revokeErr
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

func TestListMessagesUsesAuthenticatedAccount(t *testing.T) {
	fake := &deliveryFake{messages: []delivery.Message{{ID: "message-1", Status: delivery.MessageStatusSent}}}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := authenticate(httptest.NewRequest(http.MethodGet, "/api/v1/outbound-messages", nil))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if fake.messageListAccountID != "account-1" || fake.messageListLimit != 20 {
		t.Fatalf("list input = %q, %d", fake.messageListAccountID, fake.messageListLimit)
	}
	if !strings.Contains(response.Body.String(), `"id":"message-1"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestAutomaticOutputStatusUsesAuthenticatedAccountAndSession(t *testing.T) {
	fake := &deliveryFake{automaticStatuses: []delivery.AutomaticOutputStatus{{
		TurnID: "turn-1", Status: delivery.AutomaticTurnRunRestored, UpdatedAt: time.Now().UTC(),
	}}}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := authenticate(httptest.NewRequest(http.MethodGet, "/api/v1/voice-sessions/session-1/automatic-output-status", nil))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if fake.automaticStatusAccount != "account-1" || fake.automaticStatusSession != "session-1" || fake.automaticStatusLimit != 20 {
		t.Fatalf("status input = (%q, %q, %d)", fake.automaticStatusAccount, fake.automaticStatusSession, fake.automaticStatusLimit)
	}
	var payload struct {
		Items []delivery.AutomaticOutputStatus `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload.Items) != 1 || payload.Items[0].TurnID != "turn-1" || payload.Items[0].Status != delivery.AutomaticTurnRunRestored {
		t.Fatalf("status payload = %#v", payload.Items)
	}
}

func TestAutomaticOutputStatusReturnsServiceError(t *testing.T) {
	fake := &deliveryFake{automaticStatusErr: domain.ErrNotFound}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := authenticate(httptest.NewRequest(http.MethodGet, "/api/v1/voice-sessions/session-1/automatic-output-status", nil))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusNotFound, response.Body.String())
	}
}

func TestCreateMessageAcceptsWeChatChannel(t *testing.T) {
	fake := &deliveryFake{}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/outbound-messages", strings.NewReader(
		`{"channel":"wechat","destination_ref":"primary-wechat","turn_ids":["turn-1"]}`,
	))
	request = authenticate(request)
	request.Header.Set("Idempotency-Key", "wechat-message-1")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted || fake.created.Channel != delivery.ChannelWeChat {
		t.Fatalf("status = %d, created = %#v; want accepted wechat", response.Code, fake.created)
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

func TestRetryRejectsOversizedIdempotencyKey(t *testing.T) {
	fake := &deliveryFake{}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/outbound-deliveries/message-1/retry", nil)
	request = authenticate(request)
	request.Header.Set("Idempotency-Key", strings.Repeat("k", delivery.MaxIdempotencyKeyLength+1))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if fake.retryMessageID != "" {
		t.Fatal("oversized retry key reached service")
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
		{"get automatic output status", http.MethodGet, "/api/v1/voice-sessions/session-1/automatic-output-status", "", true, false},
		{"get account usage", http.MethodGet, "/api/v1/usage/summary?period_start=2026-07-01T00:00:00Z&period_end=2026-08-01T00:00:00Z", "", true, false},
		{"create outbound message", http.MethodPost, "/api/v1/outbound-messages", `{"channel":"email","destination_ref":"verified-email","turn_ids":["turn-1"]}`, true, true},
		{"get outbound message", http.MethodGet, "/api/v1/outbound-messages/message-1", "", true, false},
		{"retry outbound delivery", http.MethodPost, "/api/v1/outbound-deliveries/message-1/retry", "", true, true},
		{"get message preferences", http.MethodGet, "/api/v1/account/message-preferences", "", true, false},
		{"update message preference", http.MethodPut, "/api/v1/account/message-preferences/email/primary-email", `{"enabled":true}`, true, false},
		{"list message targets", http.MethodGet, "/api/v1/account/message-targets", "", true, false},
		{"request email bind verification", http.MethodPost, "/api/v1/account/message-targets/email/verification-codes", `{"email":"user@example.test"}`, true, false},
		{"bind email target", http.MethodPost, "/api/v1/account/message-targets/email/bind", `{"token":"dev:user@example.test"}`, true, false},
		{"unbind email target", http.MethodDelete, "/api/v1/account/message-targets/email/primary-email", "", true, false},
		{"bind wechat target", http.MethodPost, "/api/v1/account/message-targets/wechat/bind", `{"code":"oauth-code"}`, true, false},
		{"unbind wechat target", http.MethodDelete, "/api/v1/account/message-targets/wechat/primary-wechat", "", true, false},
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

func TestListMessageTargetsPassesAuthenticatedAccountAndChannelFilter(t *testing.T) {
	fake := &deliveryFake{targets: []delivery.MessageTarget{{DestinationRef: "primary-email", Channel: delivery.ChannelEmail, Verified: true}}}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/account/message-targets?channel=email", nil)
	request = authenticate(request)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if fake.listAccountID != "account-1" || fake.listChannel == nil || *fake.listChannel != delivery.ChannelEmail {
		t.Fatalf("list input = (%q, %v)", fake.listAccountID, fake.listChannel)
	}
}

func TestListMessageTargetsRejectsUnsupportedChannel(t *testing.T) {
	fake := &deliveryFake{}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/account/message-targets?channel=sms", nil)
	request = authenticate(request)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if fake.listAccountID != "" {
		t.Fatal("unsupported channel reached service")
	}
}

func TestPutMessagePreferencePassesAuthenticatedTarget(t *testing.T) {
	fake := &deliveryFake{}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodPut, "/api/v1/account/message-preferences/email/work-email", strings.NewReader(`{"enabled":true}`))
	request = authenticate(request)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if fake.putPreferenceAccount != "account-1" || fake.putPreferenceChannel != delivery.ChannelEmail || fake.putPreferenceRef != "work-email" || !fake.putPreferenceEnabled {
		t.Fatalf("preference input = (%q, %q, %q, %t)", fake.putPreferenceAccount, fake.putPreferenceChannel, fake.putPreferenceRef, fake.putPreferenceEnabled)
	}
}

func TestPutMessagePreferenceRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "unsupported channel", path: "/api/v1/account/message-preferences/sms/work-email", body: `{"enabled":true}`},
		{name: "missing enabled", path: "/api/v1/account/message-preferences/email/work-email", body: `{}`},
		{name: "invalid json", path: "/api/v1/account/message-preferences/email/work-email", body: `{`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &deliveryFake{}
			handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
			request := httptest.NewRequest(http.MethodPut, test.path, strings.NewReader(test.body))
			request = authenticate(request)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if fake.putPreferenceAccount != "" {
				t.Fatal("invalid preference request reached service")
			}
		})
	}
}

func TestPutMessagePreferenceReturnsServiceError(t *testing.T) {
	fake := &deliveryFake{putPreferenceErr: domain.ErrNotFound}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodPut, "/api/v1/account/message-preferences/email/missing", strings.NewReader(`{"enabled":true}`))
	request = authenticate(request)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusNotFound, response.Body.String())
	}
}

func TestBindEmailTargetPassesTokenToService(t *testing.T) {
	fake := &deliveryFake{}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/account/message-targets/email/bind", strings.NewReader(`{"token":"dev:user@example.test"}`))
	request = authenticate(request)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if fake.bindAccountID != "account-1" || fake.bindToken != "dev:user@example.test" {
		t.Fatalf("bind input = (%q, %q)", fake.bindAccountID, fake.bindToken)
	}
}

func TestBindEmailTargetRejectsMissingToken(t *testing.T) {
	fake := &deliveryFake{}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/account/message-targets/email/bind", strings.NewReader(`{"token":" "}`))
	request = authenticate(request)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if fake.bindAccountID != "" {
		t.Fatal("invalid bind request reached service")
	}
}

func TestRequestEmailBindVerificationPassesAuthenticatedAccount(t *testing.T) {
	fake := &deliveryFake{}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/account/message-targets/email/verification-codes", strings.NewReader(`{"email":"user@example.test","destination_ref":"work-email"}`))
	request = authenticate(request)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusAccepted, response.Body.String())
	}
	if fake.bindAccountID != "account-1" || fake.bindToken != "user@example.test:work-email" {
		t.Fatalf("verification input = (%q, %q)", fake.bindAccountID, fake.bindToken)
	}
}

func TestRequestEmailBindVerificationRejectsMissingEmail(t *testing.T) {
	fake := &deliveryFake{}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/account/message-targets/email/verification-codes", strings.NewReader(`{"email":" "}`))
	request = authenticate(request)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if fake.bindAccountID != "" {
		t.Fatal("invalid verification request reached service")
	}
}

func TestEmailBindVerificationRateLimitUsesRetryableHTTPStatus(t *testing.T) {
	fake := &deliveryFake{emailVerificationErr: domain.ErrRateLimited}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/account/message-targets/email/verification-codes", strings.NewReader(`{"email":"user@example.test"}`))
	request = authenticate(request)
	request.Header.Set("Content-Type", "application/json")
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

func TestUnbindEmailTargetPassesDestinationRef(t *testing.T) {
	fake := &deliveryFake{}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/account/message-targets/email/primary-email", nil)
	request = authenticate(request)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusNoContent, response.Body.String())
	}
	if fake.revokeAccountID != "account-1" || fake.revokeChannel != delivery.ChannelEmail || fake.revokeRef != "primary-email" {
		t.Fatalf("revoke input = (%q, %q, %q)", fake.revokeAccountID, fake.revokeChannel, fake.revokeRef)
	}
}

func TestBindWeChatTargetInvalidOAuthCodeReturnsBadRequest(t *testing.T) {
	fake := &deliveryFake{bindEmailErr: domain.ErrInvalidArgument}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/account/message-targets/wechat/bind", strings.NewReader(`{"code":"expired-code"}`))
	request = authenticate(request)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}

func TestBindWeChatTargetPassesCodeToService(t *testing.T) {
	fake := &deliveryFake{}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/account/message-targets/wechat/bind", strings.NewReader(`{"code":"oauth-code-1"}`))
	request = authenticate(request)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if fake.bindAccountID != "account-1" || fake.bindToken != "oauth-code-1" {
		t.Fatalf("bind input = (%q, %q)", fake.bindAccountID, fake.bindToken)
	}
}

func TestUnbindWeChatTargetPassesDestinationRef(t *testing.T) {
	fake := &deliveryFake{}
	handler := webapi.New(accounts.NewUseCases(), usage.NewUseCases(), fake, tokenVerifierFake{})
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/account/message-targets/wechat/work-wechat", nil)
	request = authenticate(request)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusNoContent, response.Body.String())
	}
	if fake.revokeAccountID != "account-1" || fake.revokeChannel != delivery.ChannelWeChat || fake.revokeRef != "work-wechat" {
		t.Fatalf("revoke input = (%q, %q, %q)", fake.revokeAccountID, fake.revokeChannel, fake.revokeRef)
	}
}
