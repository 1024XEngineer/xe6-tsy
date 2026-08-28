package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebhookProviderPostsAccountPayload(t *testing.T) {
	var got struct {
		Text string `json:"text"`
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("request = %s content-type=%q", r.Method, r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	provider := &WebhookProvider{httpClient: server.Client()}
	message := Message{ID: "msg-1", AccountID: "account-1", Channel: ChannelWebhook, DestinationRef: "primary-webhook", Turns: []FinalTurnSnapshot{{SourceText: "hello", TranslatedText: "你好"}}}
	attempt := DeliveryAttempt{ID: "attempt-1", MessageID: message.ID}
	err := provider.Send(context.Background(), SendRequest{
		Message: message, Attempt: attempt, ProviderIdempotencyKey: attempt.ID,
		Destination: VerifiedDestination{AccountID: message.AccountID, Channel: ChannelWebhook, DestinationRef: message.DestinationRef, ProviderTarget: server.URL},
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if got.Text == "" {
		t.Fatal("webhook payload text is empty")
	}
}

func TestValidateWebhookURLRequiresHTTPSAndNoCredentials(t *testing.T) {
	for _, raw := range []string{"", "http://example.com/hook", "https://user:pass@example.com/hook", "https://example.com/hook#fragment"} {
		if _, err := validateWebhookURL(raw); err == nil {
			t.Fatalf("validateWebhookURL(%q) error = nil", raw)
		}
	}
	if got, err := validateWebhookURL(" https://example.com/hook "); err != nil || got != "https://example.com/hook" {
		t.Fatalf("validateWebhookURL() = (%q, %v)", got, err)
	}
}

func TestNewWebhookProviderConfiguresClient(t *testing.T) {
	provider := NewWebhookProvider()
	if provider == nil || provider.httpClient == nil || provider.httpClient.Timeout == 0 || provider.httpClient.CheckRedirect == nil {
		t.Fatalf("NewWebhookProvider() = %#v", provider)
	}
	if provider.SupportsProviderIdempotency() {
		t.Fatal("webhook provider must not claim idempotency")
	}
	if err := provider.httpClient.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("CheckRedirect() error = %v", err)
	}
}

func TestWebhookProviderRejectsInvalidRequestAndContext(t *testing.T) {
	provider := NewWebhookProvider()
	if err := provider.Send(context.Background(), SendRequest{}); !errors.Is(err, ErrProviderRejected) {
		t.Fatalf("invalid request error = %v", err)
	}
	base := SendRequest{
		Message: Message{ID: "msg-1", AccountID: "account-1", Channel: ChannelWebhook, DestinationRef: "primary-webhook"},
		Attempt: DeliveryAttempt{ID: "attempt-1", MessageID: "msg-1"}, ProviderIdempotencyKey: "attempt-1",
		Destination: VerifiedDestination{AccountID: "account-1", Channel: ChannelWebhook, DestinationRef: "primary-webhook", ProviderTarget: "https://example.com/hook"},
	}
	invalid := []SendRequest{
		func() SendRequest { r := base; r.ProviderIdempotencyKey = "other"; return r }(),
		func() SendRequest { r := base; r.Message.ID = ""; return r }(),
		func() SendRequest { r := base; r.Message.AccountID = ""; return r }(),
		func() SendRequest { r := base; r.Attempt.MessageID = "other"; return r }(),
		func() SendRequest { r := base; r.Message.Channel = ChannelEmail; return r }(),
		func() SendRequest { r := base; r.Destination.AccountID = "other"; return r }(),
		func() SendRequest { r := base; r.Destination.Channel = ChannelEmail; return r }(),
		func() SendRequest { r := base; r.Destination.DestinationRef = "other"; return r }(),
	}
	for index, request := range invalid {
		if err := provider.Send(context.Background(), request); !errors.Is(err, ErrProviderRejected) {
			t.Errorf("invalid request %d error = %v", index, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := provider.Send(ctx, SendRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request error = %v", err)
	}
}

func TestWebhookProviderHandlesProviderAndTransportErrors(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream failure"))
	}))
	defer server.Close()
	message := Message{ID: "msg-1", AccountID: "account-1", Channel: ChannelWebhook, DestinationRef: "primary-webhook"}
	attempt := DeliveryAttempt{ID: "attempt-1", MessageID: message.ID}
	request := SendRequest{Message: message, Attempt: attempt, ProviderIdempotencyKey: attempt.ID, Destination: VerifiedDestination{AccountID: message.AccountID, Channel: ChannelWebhook, DestinationRef: message.DestinationRef, ProviderTarget: server.URL}}
	provider := &WebhookProvider{httpClient: server.Client()}
	if err := provider.Send(context.Background(), request); !errors.Is(err, ErrProviderRejected) {
		t.Fatalf("provider error = %v", err)
	}
	provider.httpClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network down")
	})}
	if err := provider.Send(context.Background(), request); err == nil || !strings.Contains(err.Error(), "webhook transport failed") {
		t.Fatalf("transport error = %v", err)
	}
}

func TestWebhookProviderFallsBackToDefaultHTTPClient(t *testing.T) {
	provider := &WebhookProvider{}
	message := Message{ID: "msg-1", AccountID: "account-1", Channel: ChannelWebhook, DestinationRef: "primary-webhook"}
	attempt := DeliveryAttempt{ID: "attempt-1", MessageID: message.ID}
	request := SendRequest{Message: message, Attempt: attempt, ProviderIdempotencyKey: attempt.ID, Destination: VerifiedDestination{AccountID: message.AccountID, Channel: ChannelWebhook, DestinationRef: message.DestinationRef, ProviderTarget: "https://127.0.0.1:1/hook"}}
	if err := provider.Send(context.Background(), request); err == nil {
		t.Fatal("default client request error = nil")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
