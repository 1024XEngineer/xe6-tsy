package delivery

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
)

func TestWeComClientUserIDFromOAuthCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/gettoken"):
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "access_token": "token-1", "expires_in": 7200})
		case strings.Contains(r.URL.Path, "/getuserinfo"):
			if r.URL.Query().Get("code") != "oauth-code-1" {
				_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 40029, "errmsg": "invalid code"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "userid": "userid-1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewWeComClient(WeComConfig{CorpID: "corp", CorpSecret: "secret", AgentID: 1000002, APIBase: server.URL})
	if err != nil {
		t.Fatalf("NewWeComClient() error = %v", err)
	}
	userid, err := client.UserIDFromOAuthCode(t.Context(), "oauth-code-1")
	if err != nil {
		t.Fatalf("UserIDFromOAuthCode() error = %v", err)
	}
	if userid != "userid-1" {
		t.Fatalf("userid = %q, want userid-1", userid)
	}
}

func TestWeComClientSendTextMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/gettoken"):
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "access_token": "token-1", "expires_in": 7200})
		case strings.Contains(r.URL.Path, "/message/send"):
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewWeComClient(WeComConfig{CorpID: "corp", CorpSecret: "secret", AgentID: 1000002, APIBase: server.URL})
	if err != nil {
		t.Fatalf("NewWeComClient() error = %v", err)
	}
	if err := client.SendTextMessage(t.Context(), "userid-1", "hello"); err != nil {
		t.Fatalf("SendTextMessage() error = %v", err)
	}
}

func TestValidateWeComUserIDRejectsControlCharacters(t *testing.T) {
	_, err := validateWeComUserID("user\r\nid")
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("validateWeComUserID() error = %v, want invalid argument", err)
	}
}
