package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/config"
	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
	"github.com/1024XEngineer/xe6-tsy/services/api/sessions"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/controlplane"
)

func TestNewSessionHTTPDependenciesValidatesInputs(t *testing.T) {
	tests := []struct {
		name string
		edit func(*sessionCompositionInputs)
		want error
	}{
		{
			name: "pool",
			edit: func(inputs *sessionCompositionInputs) {
				inputs.Pool = nil
			},
			want: sessions.ErrInvalidDependency,
		},
		{
			name: "language reader",
			edit: func(inputs *sessionCompositionInputs) {
				inputs.LanguageReader = nil
			},
			want: sessions.ErrInvalidDependency,
		},
		{
			name: "HTTP client",
			edit: func(inputs *sessionCompositionInputs) {
				inputs.HTTPClient = nil
			},
			want: sessions.ErrInvalidDependency,
		},
		{
			name: "ID generator",
			edit: func(inputs *sessionCompositionInputs) {
				inputs.IDs = nil
			},
			want: sessions.ErrInvalidDependency,
		},
		{
			name: "clock",
			edit: func(inputs *sessionCompositionInputs) {
				inputs.Clock = nil
			},
			want: sessions.ErrInvalidDependency,
		},
		{
			name: "ticket secret",
			edit: func(inputs *sessionCompositionInputs) {
				inputs.Config.RealtimeTicketSecret = "short"
			},
			want: realtimev1.ErrTicketConfig,
		},
		{
			name: "realtime client",
			edit: func(inputs *sessionCompositionInputs) {
				inputs.Config.RealtimeBaseURL = "://bad-url"
			},
			want: controlplane.ErrClientDependency,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			inputs := validSessionCompositionInputs(t)
			test.edit(&inputs)

			_, err := newSessionHTTPDependencies(inputs)
			if !errors.Is(err, test.want) {
				t.Fatalf("newSessionHTTPDependencies() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestNewSessionHTTPDependenciesBuildsRealHandler(t *testing.T) {
	dependencies, err := newSessionHTTPDependencies(validSessionCompositionInputs(t))
	if err != nil {
		t.Fatalf("newSessionHTTPDependencies() error = %v", err)
	}
	if dependencies == nil || dependencies.service == nil || dependencies.handler == nil ||
		dependencies.endRecovery == nil {
		t.Fatalf("dependencies = %#v, want service, handler, and end recovery worker", dependencies)
	}
}

func TestNewSessionHTTPDependenciesFromPoolUsesDefaultTimeout(t *testing.T) {
	dependencies, err := newSessionHTTPDependenciesFromPool(
		context.Background(),
		testConfiguredRuntimePool(t),
		languages.NewStub(),
		validSessionConfig(),
	)
	if err != nil {
		t.Fatalf("newSessionHTTPDependenciesFromPool() error = %v", err)
	}
	if dependencies.handler == nil {
		t.Fatal("handler is nil")
	}
}

func TestSessionOwnerReaderMapsSessionOwner(t *testing.T) {
	reader := sessionOwnerReader{reader: sessionReaderStub{snapshot: sessions.SessionSnapshot{
		SessionID: "vs_1",
		AccountID: "acct_1",
		Status:    sessions.StatusCreated,
	}}}
	accountID, err := reader.GetOwnerAccountID(t.Context(), "vs_1")
	if err != nil {
		t.Fatalf("GetOwnerAccountID() error = %v", err)
	}
	if accountID != "acct_1" {
		t.Fatalf("accountID = %q, want acct_1", accountID)
	}
}

func TestSessionOwnerReaderMapsMissingSession(t *testing.T) {
	reader := sessionOwnerReader{reader: sessionReaderStub{err: sessions.ErrVoiceSessionNotFound}}
	_, err := reader.GetOwnerAccountID(t.Context(), "vs_missing")
	if !errors.Is(err, languages.ErrSessionNotFound) {
		t.Fatalf("GetOwnerAccountID() error = %v, want ErrSessionNotFound", err)
	}
}

func TestSessionIDGeneratorProducesStablePrefixes(t *testing.T) {
	generator := newSessionIDGenerator()
	generator.now = func() time.Time { return time.Unix(1700000000, 0).UTC() }

	sessionID := generator.NewVoiceSessionID()
	operationID := generator.NewStartOperationID()

	if !strings.HasPrefix(sessionID, "vs_") || !strings.HasPrefix(operationID, "op_") {
		t.Fatalf("generated ids = (%q, %q), want vs_/op_ prefixes", sessionID, operationID)
	}
	if sessionID == "vs_" || operationID == "op_" {
		t.Fatalf("generated ids must not be empty: (%q, %q)", sessionID, operationID)
	}
}

func TestNewSessionEndRecoveryConfigUsesUniqueWorkerID(t *testing.T) {
	config := newSessionEndRecoveryConfig(fixedClock{now: time.Unix(1700000000, 0).UTC()})
	if !strings.HasPrefix(config.WorkerID, "api_end_recovery_") {
		t.Fatalf("WorkerID = %q, want api_end_recovery_ prefix", config.WorkerID)
	}
	if config.AttemptTimeout <= 0 || config.AttemptTimeout >= config.LeaseDuration {
		t.Fatalf("attempt timeout = %v, lease duration = %v, want positive attempt shorter than lease",
			config.AttemptTimeout, config.LeaseDuration)
	}
	if config.PollInterval <= 0 || config.InitialBackoff <= 0 ||
		config.MaxBackoff < config.InitialBackoff {
		t.Fatalf("invalid recovery config: %#v", config)
	}
}

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}

func validSessionCompositionInputs(t *testing.T) sessionCompositionInputs {
	t.Helper()
	return sessionCompositionInputs{
		Pool:           testConfiguredRuntimePool(t),
		LanguageReader: languages.NewStub(),
		HTTPClient:     httpDoerStub{},
		IDs:            newSessionIDGenerator(),
		Clock:          utcClock{},
		Config:         validSessionConfig(),
	}
}

func validSessionConfig() config.Config {
	return config.Config{
		RealtimeBaseURL:      "http://127.0.0.1:8090",
		RealtimeTicketSecret: strings.Repeat("s", 32),
		RealtimeHTTPTimeout:  5 * time.Second,
	}
}

type sessionReaderStub struct {
	snapshot sessions.SessionSnapshot
	err      error
}

func (s sessionReaderStub) GetSession(context.Context, string) (sessions.SessionSnapshot, error) {
	return s.snapshot, s.err
}

type httpDoerStub struct{}

func (httpDoerStub) Do(*http.Request) (*http.Response, error) {
	return nil, errors.New("unexpected HTTP request")
}
