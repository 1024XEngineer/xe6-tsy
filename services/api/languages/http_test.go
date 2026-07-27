package languages

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/webapi"
)

func TestHTTPCreateAndGetConfig(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	svc := NewService(store, MapSessionOwner{"vs_http": "acct_http"})
	mux := http.NewServeMux()
	NewHandler(svc, func(r *http.Request) (string, bool) {
		return webapi.AccountIDFromContext(r.Context())
	}).Register(mux)

	body, _ := json.Marshal(CreateLanguageConfigRequest{Languages: bilingualPairs()})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/voice-sessions/vs_http/language-configs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "ik_http_1")
	req.Header.Set("X-Request-ID", "req_http_1")
	req = req.WithContext(webapi.WithAccountID(req.Context(), "acct_http"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/voice-sessions/vs_http/language-config", nil)
	getReq = getReq.WithContext(webapi.WithAccountID(context.Background(), "acct_http"))
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getRec.Code, getRec.Body.String())
	}

	var cfg LanguageConfig
	if err := json.NewDecoder(getRec.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Version != 1 || cfg.SessionID != "vs_http" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestHTTPUnauthorized(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	svc := NewService(store, MapSessionOwner{})
	mux := http.NewServeMux()
	NewHandler(svc, nil).Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/languages", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", rec.Code)
	}
}

func TestHTTPListLanguages(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	svc := NewService(store, MapSessionOwner{})
	mux := http.NewServeMux()
	NewHandler(svc, func(r *http.Request) (string, bool) {
		return "acct_http", true
	}).Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/languages?active=true", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}
