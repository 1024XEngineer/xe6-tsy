package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
	"github.com/1024XEngineer/xe6-tsy/services/api/realtimeaccess"
	"github.com/1024XEngineer/xe6-tsy/services/api/sessions"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/controlplane"
	"github.com/jackc/pgx/v5/pgxpool"
)

var newRealtimeTicketCodec = realtimev1.NewHMACTicketCodec

// newSessionHandlerFromPool builds the voice-session HTTP boundary with a real
// languages.Service for start readiness. When REALTIME_BASE_URL is unset,
// Start stays deferred (501) because WebRTC readiness and Realtime.Start are
// unavailable. End of a still-created session succeeds without calling Stop;
// End of an active session stays deferred until realtime cleanup is wired.
// Create/List/Get remain available.
func newSessionHandlerFromPool(
	pool *pgxpool.Pool,
	languageService *languages.Service,
	ticketSecret string,
) (*sessions.Handler, error) {
	if languageService == nil {
		return newSessionHandler(nil), nil
	}
	if pool == nil {
		return nil, errors.New("session handler requires PostgreSQL pool")
	}
	languageConfigs, err := realtimeaccess.NewLanguageConfigReader(languageService)
	if err != nil {
		return nil, fmt.Errorf("wire language config reader: %w", err)
	}
	webrtc, realtime, err := realtimeSessionAdapters(pool, ticketSecret)
	if err != nil {
		return nil, err
	}
	service, err := sessions.NewService(sessions.Dependencies{
		Repository:        sessions.NewPostgresRepository(pool),
		LanguageConfigs:   languageConfigs,
		WebRTCConnections: webrtc,
		Realtime:          realtime,
		IDs:               sessions.NewULIDGenerator(),
		Clock:             sessions.SystemClock{},
		Logger:            slog.Default(),
	})
	if err != nil {
		return nil, fmt.Errorf("construct session service: %w", err)
	}
	return newSessionHandler(service), nil
}

func realtimeSessionAdapters(
	pool *pgxpool.Pool,
	ticketSecret string,
) (sessions.WebRTCConnectionReader, sessions.RealtimeLifecycle, error) {
	baseURL := strings.TrimSpace(os.Getenv("REALTIME_BASE_URL"))
	if baseURL == "" {
		slog.Info("REALTIME_BASE_URL unset; session Start and active End stay not_implemented until realtime is wired")
		return realtimeaccess.DeferredWebRTCConnection{}, realtimeaccess.DeferredRealtime{}, nil
	}
	if strings.TrimSpace(ticketSecret) == "" {
		return nil, nil, errors.New("REALTIME_BASE_URL requires JWT_SECRET for ticket signing")
	}
	ticketKey := sha256.Sum256([]byte("lingow-realtime-ticket\x00" + ticketSecret))
	codec, err := newRealtimeTicketCodec(realtimev1.TicketConfig{
		Secret: ticketKey[:],
		TTL:    time.Minute,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("configure realtime ticket codec: %w", err)
	}
	tickets, err := realtimeaccess.NewTicketSource(sessions.NewPostgresRepository(pool), codec)
	if err != nil {
		return nil, nil, fmt.Errorf("configure realtime ticket source: %w", err)
	}
	client, err := controlplane.NewClient(controlplane.ClientConfig{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: 10 * time.Second},
		Tickets: tickets,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("configure realtime control-plane client: %w", err)
	}
	webrtc, err := realtimeaccess.NewWebRTCConnectionReader(client)
	if err != nil {
		return nil, nil, fmt.Errorf("wire WebRTC connection reader: %w", err)
	}
	lifecycle, err := realtimeaccess.NewRealtimeLifecycle(client)
	if err != nil {
		return nil, nil, fmt.Errorf("wire realtime lifecycle: %w", err)
	}
	slog.Info("session realtime control-plane client enabled", "base_url", baseURL)
	return webrtc, lifecycle, nil
}
