package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestWeComClientInvalidOAuthCodeReturnsInvalidArgument(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/gettoken"):
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "access_token": "token-1", "expires_in": 7200})
		case strings.Contains(r.URL.Path, "/getuserinfo"):
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": weComErrInvalidOAuthCode, "errmsg": "invalid code"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewWeComClient(WeComConfig{CorpID: "corp", CorpSecret: "secret-value", AgentID: 1000002, APIBase: server.URL})
	if err != nil {
		t.Fatalf("NewWeComClient() error = %v", err)
	}
	_, err = client.UserIDFromOAuthCode(t.Context(), "bad-code")
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("UserIDFromOAuthCode() error = %v, want invalid argument", err)
	}
}

func TestWeComClientSendInvalidUserReturnsProviderRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/gettoken"):
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "access_token": "token-1", "expires_in": 7200})
		case strings.Contains(r.URL.Path, "/message/send"):
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 81013, "errmsg": "user not in app scope"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewWeComClient(WeComConfig{CorpID: "corp", CorpSecret: "secret-value", AgentID: 1000002, APIBase: server.URL})
	if err != nil {
		t.Fatalf("NewWeComClient() error = %v", err)
	}
	err = client.SendTextMessage(t.Context(), "userid-1", "hello")
	if !errors.Is(err, ErrProviderRejected) {
		t.Fatalf("SendTextMessage() error = %v, want provider rejected", err)
	}
}

func TestWeComTransportErrorRedactsCredentials(t *testing.T) {
	client, err := NewWeComClient(WeComConfig{
		CorpID:     "corp",
		CorpSecret: "top-secret-corpsecret",
		AgentID:    1000002,
		APIBase:    "http://127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("NewWeComClient() error = %v", err)
	}
	_, err = client.UserIDFromOAuthCode(context.Background(), "oauth-code-1")
	if err == nil {
		t.Fatal("UserIDFromOAuthCode() error = nil, want transport failure")
	}
	if strings.Contains(err.Error(), "top-secret-corpsecret") || strings.Contains(err.Error(), "access_token") {
		t.Fatalf("transport error leaked credentials: %v", err)
	}
}

func TestWeComProviderSendSurfacesPermanentFailure(t *testing.T) {
	provider, err := NewWeComProvider(failingWeComMessenger{err: fmtErrProviderRejected()})
	if err != nil {
		t.Fatalf("NewWeComProvider() error = %v", err)
	}
	err = provider.Send(t.Context(), validWeChatFakeRequest())
	if !errors.Is(err, ErrProviderRejected) {
		t.Fatalf("Send() error = %v, want provider rejected", err)
	}
}

type failingWeComMessenger struct{ err error }

func (f failingWeComMessenger) SendTextMessage(context.Context, string, string) error { return f.err }

func fmtErrProviderRejected() error {
	return fmt.Errorf("%w: wecom message send: user not in app scope (code 81013)", ErrProviderRejected)
}
