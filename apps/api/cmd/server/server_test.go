package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewRouter(t *testing.T) {
	router := newRouter()
	if router == nil {
		t.Fatal("newRouter() returned nil")
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestDefaultServerConfig(t *testing.T) {
	cfg := defaultServerConfig()

	if cfg.Addr != defaultAddr {
		t.Fatalf("default addr = %q, want %q", cfg.Addr, defaultAddr)
	}
	if cfg.ReadHeaderTimeout == 0 || cfg.ReadTimeout == 0 || cfg.WriteTimeout == 0 || cfg.IdleTimeout == 0 {
		t.Fatal("server timeouts must be configured")
	}
}

func TestNewHTTPServer(t *testing.T) {
	cfg := defaultServerConfig()
	srv := newHTTPServer(cfg, newRouter())

	if srv.Addr != cfg.Addr {
		t.Fatalf("server addr = %q, want %q", srv.Addr, cfg.Addr)
	}
	if srv.Handler == nil {
		t.Fatal("server handler is nil")
	}
	if srv.ReadHeaderTimeout != cfg.ReadHeaderTimeout {
		t.Fatalf("read header timeout = %s, want %s", srv.ReadHeaderTimeout, cfg.ReadHeaderTimeout)
	}
}
