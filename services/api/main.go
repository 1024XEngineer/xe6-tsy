package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1024XEngineer/xe6-tsy/services/api/internal/accounts"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/delivery"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/usage"
	internalwebapi "github.com/1024XEngineer/xe6-tsy/services/api/internal/webapi"
	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
	"github.com/1024XEngineer/xe6-tsy/services/api/recordstore"
	recordswebapi "github.com/1024XEngineer/xe6-tsy/services/api/webapi"
)

type recordsHTTPDependencies struct {
	handler  *recordswebapi.Server
	accounts accounts.Service
	tokens   accounts.AccessTokenVerifier
	cleanup  func()
}

// main wires foundation use cases into the HTTP server and owns graceful shutdown.
func main() {
	if err := run(); err != nil {
		slog.Error("api exited", "error", err)
		os.Exit(1)
	}
}

func run() error {
	address := os.Getenv("API_ADDR")
	if address == "" {
		address = ":8080"
	}

	langHandler, cleanup, err := newLanguageHandler(context.Background())
	if err != nil {
		return err
	}
	if cleanup != nil {
		defer cleanup()
	}

	records, err := newRecordsHTTPDependencies(context.Background())
	if err != nil {
		return err
	}
	defer records.cleanup()

	mux := buildMux(langHandler, records.handler, records.accounts, records.tokens)

	server := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("Lingow API listening", "address", address)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

func newLanguageHandler(ctx context.Context) (*languages.Handler, func(), error) {
	accountID := func(r *http.Request) (string, bool) {
		return internalwebapi.AccountIDFromContext(r.Context())
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		slog.Warn("DATABASE_URL unset; language HTTP routes return not_implemented until wired")
		return languages.NewHandler(nil, accountID), nil, nil
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, nil, err
	}
	if err := languages.ApplyMigrations(ctx, pool); err != nil {
		pool.Close()
		return nil, nil, err
	}

	sessions := sessionOwnerFromEnv()
	svc := languages.NewService(languages.NewPostgresStore(pool, nil), sessions)
	slog.Info("language configuration service enabled")
	return languages.NewHandler(svc, accountID), pool.Close, nil
}

func sessionOwnerFromEnv() languages.SessionOwnerReader {
	switch os.Getenv("LANGUAGE_SESSION_OWNER") {
	case "trust-auth":
		slog.Warn("LANGUAGE_SESSION_OWNER=trust-auth enabled; sessions are not ownership-checked")
		return languages.TrustAuthSessionOwner{
			AccountIDFromCtx: internalwebapi.AccountIDFromContext,
		}
	default:
		return languages.NotImplementedSessionOwner{}
	}
}

func newRecordsHTTPDependencies(ctx context.Context) (*recordsHTTPDependencies, error) {
	const (
		accessTokenIssuer   = "lingow-api"
		accessTokenAudience = "lingow-client"
	)

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return nil, fmt.Errorf("initialize records HTTP: DATABASE_URL is required")
	}
	tokenSecret := os.Getenv("JWT_SECRET")
	if tokenSecret == "" {
		return nil, fmt.Errorf("initialize records HTTP: JWT_SECRET is required")
	}
	if len([]byte(tokenSecret)) < 32 {
		return nil, fmt.Errorf("initialize records HTTP: JWT_SECRET must be at least 32 bytes")
	}

	pool, err := recordstore.Open(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("initialize records HTTP: %w", err)
	}
	if err := recordstore.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("initialize records HTTP: %w", err)
	}

	accountRepository := accounts.NewPostgresRepository(pool)
	tokens, err := accounts.NewHMACIssuerWithAccount(
		tokenSecret,
		accessTokenIssuer,
		accessTokenAudience,
		accountRepository.SessionActiveForAccount,
	)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("initialize records HTTP: %w", err)
	}
	sessionScope, err := recordstore.NewPostgresSessionScopeReader(pool)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("initialize records HTTP: %w", err)
	}

	// Derive a domain-specific key so JWTs and record cursors never use identical key material.
	cursorSigningKey := sha256.Sum256([]byte("lingow-record-cursor\x00" + tokenSecret))
	services, err := recordstore.NewServices(pool, cursorSigningKey[:], accountRepository, sessionScope)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("initialize records HTTP: %w", err)
	}

	accountUseCases := accounts.NewPersistentUseCases(accountRepository, tokens, tokens, nil)
	return &recordsHTTPDependencies{
		handler: recordswebapi.NewHandler(recordswebapi.Dependencies{
			Participants: services.Participants,
			Turns:        services.Turns,
			Accounts:     recordswebapi.ContextAccountProvider{},
			// No production system credential exists yet; PATCH routes stay fail-closed.
			System: recordswebapi.ContextSystemAuthorizer{},
			Logger: slog.Default(),
		}),
		accounts: accountUseCases,
		tokens:   tokens,
		cleanup:  pool.Close,
	}, nil
}

func buildMux(
	lang *languages.Handler,
	records *recordswebapi.Server,
	accountUseCases accounts.Service,
	tokens accounts.AccessTokenVerifier,
) *http.ServeMux {
	mux := internalwebapi.New(accountUseCases, usage.NewUseCases(), delivery.NewUseCases(), tokens)
	lang.Register(mux)
	records.Register(mux, func(next http.Handler) http.Handler {
		return records.Authenticate(tokens, next)
	})
	return mux
}
