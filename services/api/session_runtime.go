package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/config"
	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
	"github.com/1024XEngineer/xe6-tsy/services/api/realtimeaccess"
	"github.com/1024XEngineer/xe6-tsy/services/api/sessions"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/controlplane"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oklog/ulid/v2"
)

type sessionHTTPDependencies struct {
	service     *sessions.Service
	handler     *sessions.Handler
	endRecovery backgroundWorker
}

type sessionCompositionInputs struct {
	Pool           *pgxpool.Pool
	Repository     sessions.Repository
	SessionReader  sessions.SessionReader
	LanguageReader languages.LanguageConfigReader
	HTTPClient     controlplane.HTTPDoer
	IDs            sessions.IDGenerator
	Clock          sessions.Clock
	Config         config.Config
	Logger         *slog.Logger
}

func newSessionHTTPDependenciesFromPool(
	_ context.Context,
	pool *pgxpool.Pool,
	languageReader languages.LanguageConfigReader,
	processConfig config.Config,
) (*sessionHTTPDependencies, error) {
	return newSessionHTTPDependencies(sessionCompositionInputs{
		Pool:           pool,
		LanguageReader: languageReader,
		HTTPClient: &http.Client{
			Timeout: realtimeHTTPTimeout(processConfig),
		},
		IDs:    newSessionIDGenerator(),
		Clock:  utcClock{},
		Config: processConfig,
		Logger: slog.Default(),
	})
}

func newSessionHTTPDependencies(inputs sessionCompositionInputs) (*sessionHTTPDependencies, error) {
	repository := inputs.Repository
	sessionReader := inputs.SessionReader
	if repository == nil || sessionReader == nil {
		if inputs.Pool == nil {
			return nil, fmt.Errorf("%w: PostgreSQL pool is required", sessions.ErrInvalidDependency)
		}
		postgresRepository := sessions.NewPostgresRepository(inputs.Pool)
		if repository == nil {
			repository = postgresRepository
		}
		if sessionReader == nil {
			sessionReader = postgresRepository
		}
	}
	if inputs.LanguageReader == nil {
		return nil, fmt.Errorf("%w: language config reader is required", sessions.ErrInvalidDependency)
	}
	if inputs.HTTPClient == nil {
		return nil, fmt.Errorf("%w: realtime HTTP client is required", sessions.ErrInvalidDependency)
	}
	if inputs.IDs == nil {
		return nil, fmt.Errorf("%w: ID generator is required", sessions.ErrInvalidDependency)
	}
	if inputs.Clock == nil {
		return nil, fmt.Errorf("%w: clock is required", sessions.ErrInvalidDependency)
	}

	codec, err := realtimev1.NewHMACTicketCodec(realtimev1.TicketConfig{
		Secret: []byte(inputs.Config.RealtimeTicketSecret),
		Now:    inputs.Clock.Now,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize realtime ticket codec: %w", err)
	}
	tickets, err := realtimeaccess.NewTicketSource(sessionReader, codec)
	if err != nil {
		return nil, fmt.Errorf("initialize realtime ticket source: %w", err)
	}
	client, err := controlplane.NewClient(controlplane.ClientConfig{
		BaseURL: inputs.Config.RealtimeBaseURL,
		HTTP:    inputs.HTTPClient,
		Tickets: tickets,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize realtime control-plane client: %w", err)
	}
	languageAdapter, err := realtimeaccess.NewLanguageConfigReader(inputs.LanguageReader)
	if err != nil {
		return nil, fmt.Errorf("initialize language config adapter: %w", err)
	}
	connectionReader, err := realtimeaccess.NewWebRTCConnectionReader(client)
	if err != nil {
		return nil, fmt.Errorf("initialize WebRTC connection adapter: %w", err)
	}
	realtimeLifecycle, err := realtimeaccess.NewRealtimeLifecycle(client)
	if err != nil {
		return nil, fmt.Errorf("initialize realtime lifecycle adapter: %w", err)
	}
	service, err := sessions.NewService(sessions.Dependencies{
		Repository:        repository,
		LanguageConfigs:   languageAdapter,
		WebRTCConnections: connectionReader,
		Realtime:          realtimeLifecycle,
		IDs:               inputs.IDs,
		Clock:             inputs.Clock,
		Logger:            inputs.Logger,
	})
	if err != nil {
		return nil, err
	}
	endRecovery, err := sessions.NewEndRecoveryWorker(service, newSessionEndRecoveryConfig(inputs.Clock))
	if err != nil {
		return nil, fmt.Errorf("initialize session end recovery worker: %w", err)
	}
	return &sessionHTTPDependencies{
		service:     service,
		handler:     newSessionHandler(service),
		endRecovery: endRecovery,
	}, nil
}

type sessionOwnerReader struct {
	reader sessions.SessionReader
}

func (r sessionOwnerReader) GetOwnerAccountID(ctx context.Context, sessionID string) (string, error) {
	if r.reader == nil {
		return "", languages.ErrNotImplemented
	}
	snapshot, err := r.reader.GetSession(ctx, sessionID)
	switch {
	case err == nil:
		return snapshot.AccountID, nil
	case errors.Is(err, sessions.ErrVoiceSessionNotFound):
		return "", languages.ErrSessionNotFound
	case errors.Is(err, sessions.ErrInvalidRequest):
		return "", languages.ErrInvalidRequest
	default:
		return "", err
	}
}

type utcClock struct{}

func (utcClock) Now() time.Time {
	return time.Now().UTC()
}

type sessionIDGenerator struct {
	mu       sync.Mutex
	entropy  *ulid.MonotonicEntropy
	now      func() time.Time
	fallback uint64
}

func newSessionIDGenerator() *sessionIDGenerator {
	return &sessionIDGenerator{
		entropy: ulid.Monotonic(rand.Reader, 0),
		now:     func() time.Time { return time.Now().UTC() },
	}
}

func (g *sessionIDGenerator) NewVoiceSessionID() string {
	return g.newULID("vs_")
}

func (g *sessionIDGenerator) NewStartOperationID() string {
	return g.newULID("op_")
}

func (g *sessionIDGenerator) newULID(prefix string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	entropy := g.entropy
	if entropy == nil {
		entropy = ulid.Monotonic(rand.Reader, 0)
		g.entropy = entropy
	}
	id, err := ulid.New(ulid.Timestamp(now().UTC()), entropy)
	if err != nil {
		g.fallback++
		return fmt.Sprintf("%s%x%012x", prefix, now().UTC().UnixNano(), g.fallback)
	}
	return prefix + id.String()
}

func realtimeHTTPTimeout(processConfig config.Config) time.Duration {
	if processConfig.RealtimeHTTPTimeout > 0 {
		return processConfig.RealtimeHTTPTimeout
	}
	return 5 * time.Second
}

func newSessionEndRecoveryConfig(clock sessions.Clock) sessions.EndRecoveryConfig {
	return sessions.EndRecoveryConfig{
		WorkerID:       newSessionEndRecoveryWorkerID(clock),
		PollInterval:   time.Second,
		LeaseDuration:  10 * time.Second,
		AttemptTimeout: 5 * time.Second,
		InitialBackoff: time.Second,
		MaxBackoff:     time.Minute,
	}
}

func newSessionEndRecoveryWorkerID(clock sessions.Clock) string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown"
	}
	now := time.Now().UTC()
	if clock != nil {
		now = clock.Now().UTC()
	}
	id, err := ulid.New(ulid.Timestamp(now), rand.Reader)
	if err != nil {
		return fmt.Sprintf("api_end_recovery_%s_%d_%d", hostname, os.Getpid(), now.UnixNano())
	}
	return fmt.Sprintf("api_end_recovery_%s_%d_%s", hostname, os.Getpid(), id.String())
}

var (
	_ sessions.IDGenerator         = (*sessionIDGenerator)(nil)
	_ sessions.Clock               = utcClock{}
	_ languages.SessionOwnerReader = sessionOwnerReader{}
)
