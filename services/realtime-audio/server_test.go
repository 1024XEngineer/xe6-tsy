package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

func secretEnv(secret string) func(string) string {
	return func(key string) string {
		switch key {
		case "REALTIME_TICKET_SECRET":
			return secret
		case "REALTIME_ADDR":
			return ":18090"
		default:
			return ""
		}
	}
}

func TestLoadProcessConfigDefaultsAndValidatesSecret(t *testing.T) {
	cfg, err := loadProcessConfig(func(key string) string {
		if key == "REALTIME_TICKET_SECRET" {
			return strings.Repeat("s", 32)
		}
		return ""
	})
	if err != nil {
		t.Fatalf("loadProcessConfig() error = %v", err)
	}
	if cfg.Addr != defaultAddr {
		t.Fatalf("Addr = %q, want %q", cfg.Addr, defaultAddr)
	}

	if _, err := loadProcessConfig(func(string) string { return "" }); err == nil {
		t.Fatal("loadProcessConfig() error = nil, want secret validation error")
	}
	if _, err := loadProcessConfig(nil); err == nil {
		t.Fatal("loadProcessConfig(nil) error = nil, want secret validation error")
	}
}

func TestNewControlPlaneHandlerServesWebRTCConfig(t *testing.T) {
	secret := strings.Repeat("r", 32)
	handler, err := newControlPlaneHandler(secret)
	if err != nil {
		t.Fatalf("newControlPlaneHandler() error = %v", err)
	}

	codec, err := realtimev1.NewHMACTicketCodec(realtimev1.TicketConfig{
		Secret: []byte(secret),
		TTL:    time.Minute,
	})
	if err != nil {
		t.Fatalf("NewHMACTicketCodec() error = %v", err)
	}
	ticket, err := codec.Issue("vs_test", "account_test")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	request := httptest.NewRequest(
		http.MethodGet,
		"/realtime/v1/sessions/vs_test/webrtc/config",
		nil,
	)
	request.Header.Set("Authorization", "Bearer "+ticket)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}

	var body struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.SessionID != "vs_test" {
		t.Fatalf("session_id = %q", body.SessionID)
	}
}

func TestNewControlPlaneHandlerRejectsMissingTicket(t *testing.T) {
	handler, err := newControlPlaneHandler(strings.Repeat("r", 32))
	if err != nil {
		t.Fatalf("newControlPlaneHandler() error = %v", err)
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"/realtime/v1/sessions/vs_test/webrtc/config",
		nil,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code == http.StatusOK {
		t.Fatalf("status = %d, want auth failure", response.Code)
	}
}

func TestNewHTTPServerAppliesTimeouts(t *testing.T) {
	server := newHTTPServer(":8090", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	if server.Addr != ":8090" {
		t.Fatalf("Addr = %q", server.Addr)
	}
	if server.ReadHeaderTimeout != httpReadHeaderTimeout ||
		server.ReadTimeout != httpReadTimeout ||
		server.WriteTimeout != httpWriteTimeout ||
		server.IdleTimeout != httpIdleTimeout {
		t.Fatalf("timeouts not applied: %#v", server)
	}
}

func TestNewControlPlaneHandlerRejectsShortSecret(t *testing.T) {
	if _, err := newControlPlaneHandler("short"); err == nil {
		t.Fatal("newControlPlaneHandler(short) error = nil")
	}
}

func TestRunRejectsMissingSecretWithoutListening(t *testing.T) {
	err := run(context.Background(), func(string) string { return "" }, nil)
	if err == nil {
		t.Fatal("run() error = nil, want config error")
	}
	if !strings.Contains(err.Error(), "REALTIME_TICKET_SECRET") {
		t.Fatalf("run() error = %v", err)
	}
}

func TestRunShutsDownAfterStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})

	done := make(chan error, 1)
	go func() {
		done <- run(ctx, secretEnv(strings.Repeat("u", 32)), func(server *http.Server) error {
			close(started)
			<-ctx.Done()
			return http.ErrServerClosed
		})
	}()

	select {
	case <-started:
		cancel()
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("listener did not start")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("run() did not return after shutdown")
	}
}

func TestRunReturnsListenError(t *testing.T) {
	want := errors.New("listen failed")
	err := run(context.Background(), secretEnv(strings.Repeat("v", 32)), func(*http.Server) error {
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("run() error = %v, want %v", err, want)
	}
}

func TestControlPlaneStartWithTrustSession(t *testing.T) {
	secret := strings.Repeat("t", 32)
	handler, err := newControlPlaneHandler(secret)
	if err != nil {
		t.Fatalf("newControlPlaneHandler() error = %v", err)
	}
	codec, err := realtimev1.NewHMACTicketCodec(realtimev1.TicketConfig{
		Secret: []byte(secret),
		TTL:    time.Minute,
	})
	if err != nil {
		t.Fatalf("NewHMACTicketCodec() error = %v", err)
	}
	ticket, err := codec.Issue("vs_start", "account_start")
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	body := strings.NewReader(`{"operation_id":"op-1"}`)
	request := httptest.NewRequest(
		http.MethodPost,
		"/realtime/v1/sessions/vs_start/start",
		body,
	)
	request.Header.Set("Authorization", "Bearer "+ticket)
	request.Header.Set("Idempotency-Key", "start-1")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("start status = %d body = %s", response.Code, response.Body.String())
	}

	runtimeReq := httptest.NewRequest(
		http.MethodGet,
		"/realtime/v1/sessions/vs_start/runtime",
		nil,
	)
	runtimeReq.Header.Set("Authorization", "Bearer "+ticket)
	runtimeRes := httptest.NewRecorder()
	handler.ServeHTTP(runtimeRes, runtimeReq)
	if runtimeRes.Code != http.StatusOK {
		t.Fatalf("runtime status = %d body = %s", runtimeRes.Code, runtimeRes.Body.String())
	}
}
