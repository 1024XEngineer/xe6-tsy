package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestDefaultConfigUsesAPIAddr(t *testing.T) {
	cfg := defaultConfig(func(key string) string {
		if key == "API_ADDR" {
			return "127.0.0.1:18080"
		}
		return ""
	})

	if cfg.addr != "127.0.0.1:18080" {
		t.Fatalf("defaultConfig addr = %q, want %q", cfg.addr, "127.0.0.1:18080")
	}
}

func TestServeListenerStartsAndShutsDown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	cfg := defaultConfig(func(string) string { return "" })
	srv := newHTTPServer(cfg)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- serveListener(ctx, srv, ln, 2*time.Second, discardLogger())
	}()

	resp, err := http.Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil {
		cancel()
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("GET /healthz status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		cancel()
		t.Fatalf("decode health response: %v", err)
	}
	if body.Status != "ok" {
		cancel()
		t.Fatalf("health status = %q, want %q", body.Status, "ok")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveListener returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestRunReturnsWhenContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := run(ctx, func(key string) string {
		if key == "API_ADDR" {
			return "127.0.0.1:0"
		}
		return ""
	}, discardLogger())
	if err != nil && err != context.Canceled {
		t.Fatalf("run returned %v, want nil or context.Canceled", err)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
