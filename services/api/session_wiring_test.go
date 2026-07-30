package main

import (
	"strings"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
	"github.com/1024XEngineer/xe6-tsy/services/api/realtimeaccess"
)

func TestNewSessionHandlerFromPoolRequiresPoolWhenLanguagePresent(t *testing.T) {
	_, err := newSessionHandlerFromPool(nil, languages.NewService(languages.NewMemoryStore(nil, nil), languages.MapSessionOwner{}), "")
	if err == nil {
		t.Fatal("newSessionHandlerFromPool() error = nil, want pool required")
	}
}

func TestNewSessionHandlerFromPoolAllowsNilLanguageService(t *testing.T) {
	handler, err := newSessionHandlerFromPool(nil, nil, "")
	if err != nil {
		t.Fatalf("newSessionHandlerFromPool() error = %v", err)
	}
	if handler == nil {
		t.Fatal("newSessionHandlerFromPool() handler = nil")
	}
}

func TestRealtimeSessionAdaptersDefaultToDeferred(t *testing.T) {
	t.Setenv("REALTIME_BASE_URL", "")
	webrtc, realtime, err := realtimeSessionAdapters(nil, "")
	if err != nil {
		t.Fatalf("realtimeSessionAdapters() error = %v", err)
	}
	if _, ok := webrtc.(realtimeaccess.DeferredWebRTCConnection); !ok {
		t.Fatalf("webrtc type = %T, want DeferredWebRTCConnection", webrtc)
	}
	if _, ok := realtime.(realtimeaccess.DeferredRealtime); !ok {
		t.Fatalf("realtime type = %T, want DeferredRealtime", realtime)
	}
}

func TestRealtimeSessionAdaptersRequireTicketSecret(t *testing.T) {
	t.Setenv("REALTIME_BASE_URL", "http://127.0.0.1:8090")
	_, _, err := realtimeSessionAdapters(nil, "")
	if err == nil {
		t.Fatal("realtimeSessionAdapters() error = nil, want ticket secret required")
	}
	if !strings.Contains(err.Error(), "JWT_SECRET") {
		t.Fatalf("realtimeSessionAdapters() error = %v, want JWT_SECRET mention", err)
	}
}
