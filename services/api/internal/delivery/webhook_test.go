package delivery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
