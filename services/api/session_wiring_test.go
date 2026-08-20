package main

import (
	"errors"
	"strings"
	"testing"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
	"github.com/1024XEngineer/xe6-tsy/services/api/realtimeaccess"
	"github.com/1024XEngineer/xe6-tsy/services/api/sessions"
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
	for _, baseURL := range []string{"", "   \t"} {
		t.Run("base URL "+strings.TrimSpace(baseURL), func(t *testing.T) {
			t.Setenv("REALTIME_BASE_URL", baseURL)
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
			if _, err := webrtc.GetConnectionState(t.Context(), "session-1"); !errors.Is(err, sessions.ErrNotImplemented) {
				t.Fatalf("GetConnectionState() error = %v, want ErrNotImplemented", err)
			}
			if _, err := realtime.Start(t.Context(), sessions.StartRealtimeCommand{}); !errors.Is(err, sessions.ErrNotImplemented) {
				t.Fatalf("Start() error = %v, want ErrNotImplemented", err)
			}
			if _, err := realtime.Stop(t.Context(), sessions.StopRealtimeCommand{}); !errors.Is(err, sessions.ErrNotImplemented) {
				t.Fatalf("Stop() error = %v, want ErrNotImplemented", err)
			}
			if _, err := realtime.GetRuntimeState(t.Context(), "session-1"); !errors.Is(err, sessions.ErrRuntimeSnapshotNotFound) {
				t.Fatalf("GetRuntimeState() error = %v, want ErrRuntimeSnapshotNotFound", err)
			}
		})
	}
}

func TestRealtimeSessionAdaptersRequireTicketSecret(t *testing.T) {
	t.Setenv("REALTIME_BASE_URL", "http://127.0.0.1:8090")
	for _, ticketSecret := range []string{"", "   \t"} {
		t.Run("ticket secret "+strings.TrimSpace(ticketSecret), func(t *testing.T) {
			_, _, err := realtimeSessionAdapters(nil, ticketSecret)
			if err == nil {
				t.Fatal("realtimeSessionAdapters() error = nil, want ticket secret required")
			}
			if !strings.Contains(err.Error(), "JWT_SECRET") {
				t.Fatalf("realtimeSessionAdapters() error = %v, want JWT_SECRET mention", err)
			}
		})
	}
}

func TestRealtimeSessionAdaptersRejectInvalidBaseURL(t *testing.T) {
	t.Setenv("REALTIME_BASE_URL", "://invalid")

	_, _, err := realtimeSessionAdapters(nil, strings.Repeat("s", 32))
	if err == nil || !strings.Contains(err.Error(), "configure realtime control-plane client") {
		t.Fatalf("realtimeSessionAdapters() error = %v, want control-plane client configuration failure", err)
	}
}

func TestRealtimeSessionAdaptersWrapsTicketCodecConfigurationFailure(t *testing.T) {
	codecErr := errors.New("ticket codec unavailable")
	originalCodec := newRealtimeTicketCodec
	newRealtimeTicketCodec = func(realtimev1.TicketConfig) (*realtimev1.HMACTicketCodec, error) {
		return nil, codecErr
	}
	t.Cleanup(func() { newRealtimeTicketCodec = originalCodec })
	t.Setenv("REALTIME_BASE_URL", "http://127.0.0.1:8090")

	_, _, err := realtimeSessionAdapters(nil, strings.Repeat("s", 32))
	if !errors.Is(err, codecErr) || !strings.Contains(err.Error(), "configure realtime ticket codec") {
		t.Fatalf("realtimeSessionAdapters() error = %v, want wrapped ticket codec failure", err)
	}
}
