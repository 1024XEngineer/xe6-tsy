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
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1024XEngineer/xe6-tsy/services/api/config"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/accounts"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/delivery"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/usage"
	internalwebapi "github.com/1024XEngineer/xe6-tsy/services/api/internal/webapi"
	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
	"github.com/1024XEngineer/xe6-tsy/services/api/recordstore"
	"github.com/1024XEngineer/xe6-tsy/services/api/sessions"
	recordswebapi "github.com/1024XEngineer/xe6-tsy/services/api/webapi"
)

const (
	apiReadHeaderTimeout = 5 * time.Second
	apiReadTimeout       = 15 * time.Second
	apiWriteTimeout      = 45 * time.Second
	apiIdleTimeout       = 60 * time.Second
)

type recordsHTTPDependencies struct {
	handler    *recordswebapi.Server
	accounts   accounts.Service
	tokens     accounts.AccessTokenVerifier
	worker     finalTurnWorker
	maintainer backgroundWorker
	cleanup    func()
}

type languageHTTPDependencies struct {
	service *languages.Service
	handler *languages.Handler
}

type finalTurnWorker interface {
	Run(context.Context) error
}

type backgroundWorker interface {
	Run(context.Context) error
}

type namedBackgroundWorker struct {
	name string
	run  func(context.Context) error
}

// main wires foundation use cases into the HTTP server and owns graceful shutdown.
func main() {
	if err := run(); err != nil {
		slog.Error("api exited", "error", err)
		os.Exit(1)
	}
}

func run() error {
	processConfig, err := config.Load()
	if err != nil {
		return err
	}
	if processConfig.DeliveryEnabled {
		return runConfigured(processConfig)
	}

	pool, err := recordstore.Open(context.Background(), processConfig.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := recordstore.Migrate(context.Background(), pool); err != nil {
		return err
	}

	sessionRepository := sessions.NewPostgresRepository(pool)
	langDependencies, err := newLanguageDependenciesWithPool(
		context.Background(),
		pool,
		sessionOwnerReader{reader: sessionRepository},
	)
	if err != nil {
		return err
	}
	records, err := newRecordsHTTPDependenciesFromPool(
		context.Background(),
		pool,
		processConfig.JWTSecret,
		processConfig.JWTIssuer,
		processConfig.JWTAudience,
	)
	if err != nil {
		return err
	}

	sessionHandler := newSessionHandler(nil)
	var sessionRecovery backgroundWorker
	if processConfig.SessionRuntimeEnabled {
		sessionDependencies, err := newSessionHTTPDependencies(sessionCompositionInputs{
			Repository:     sessionRepository,
			SessionReader:  sessionRepository,
			LanguageReader: langDependencies.service,
			HTTPClient: &http.Client{
				Timeout: realtimeHTTPTimeout(processConfig),
			},
			IDs:    newSessionIDGenerator(),
			Clock:  utcClock{},
			Config: processConfig,
			Logger: slog.Default(),
		})
		if err != nil {
			return err
		}
		sessionHandler = sessionDependencies.handler
		sessionRecovery = sessionDependencies.endRecovery
	} else {
		slog.Warn(
			"voice session runtime disabled",
			"configuration", "LINGOW_SESSION_RUNTIME",
		)
	}

	mux := buildMux(
		langDependencies.handler,
		sessionHandler,
		records.handler,
		records.accounts,
		records.tokens,
	)

	server := &http.Server{
		Addr:              processConfig.APIAddr,
		Handler:           mux,
		ReadHeaderTimeout: apiReadHeaderTimeout,
		ReadTimeout:       apiReadTimeout,
		WriteTimeout:      apiWriteTimeout,
		IdleTimeout:       apiIdleTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	maintainerCtx, cancelMaintainer := context.WithCancel(ctx)
	defer cancelMaintainer()
	if records.maintainer != nil {
		go func() {
			if err := records.maintainer.Run(maintainerCtx); err != nil && maintainerCtx.Err() == nil {
				slog.Error("auth maintenance stopped", "error", err)
			}
		}()
	}

	workers := []namedBackgroundWorker{
		{name: "final turn worker", run: records.worker.Run},
	}
	if sessionRecovery != nil {
		workers = append(workers, namedBackgroundWorker{
			name: "session end recovery worker",
			run:  sessionRecovery.Run,
		})
	}
	return runHTTPAndBackgroundWorkers(ctx, server, workers...)
}

func runHTTPAndBackgroundWorkers(
	ctx context.Context,
	server *http.Server,
	workers ...namedBackgroundWorker,
) error {
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()

	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("Lingow API listening", "address", server.Addr)
		serverErrors <- server.ListenAndServe()
	}()

	workerErrors := make(chan error, max(1, len(workers)))
	var workerGroup sync.WaitGroup
	for _, worker := range workers {
		worker := worker
		workerGroup.Add(1)
		go runFailFastBackgroundWorker(workerCtx, worker.name, worker.run, workerErrors, &workerGroup)
	}

	var runErr error
	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = fmt.Errorf("run API HTTP server: %w", err)
		}
	case err := <-workerErrors:
		runErr = err
	case <-ctx.Done():
	}
	cancelWorkers()

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	shutdownErrors := make(chan error, 1)
	go func() { shutdownErrors <- server.Shutdown(shutdownCtx) }()
	workerDone := make(chan struct{})
	go func() {
		workerGroup.Wait()
		close(workerDone)
	}()
	select {
	case <-workerDone:
	case <-shutdownCtx.Done():
		runErr = errors.Join(runErr, fmt.Errorf("stop background workers: %w", shutdownCtx.Err()))
	}
	shutdownErr := <-shutdownErrors
	return errors.Join(runErr, shutdownErr)
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
	handler, err := newLanguageHandlerWithPool(ctx, pool)
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	return handler, pool.Close, nil
}

func newLanguageHandlerWithPool(ctx context.Context, pool *pgxpool.Pool) (*languages.Handler, error) {
	dependencies, err := newLanguageDependenciesWithPool(ctx, pool, sessionOwnerFromEnv())
	if err != nil {
		return nil, err
	}
	return dependencies.handler, nil
}

func newLanguageDependenciesWithPool(
	ctx context.Context,
	pool *pgxpool.Pool,
	sessions languages.SessionOwnerReader,
) (*languageHTTPDependencies, error) {
	if pool == nil {
		return nil, errors.New("language handler requires PostgreSQL pool")
	}
	if err := languages.ApplyMigrations(ctx, pool); err != nil {
		return nil, err
	}
	svc := languages.NewService(languages.NewPostgresStore(pool, nil), sessions)
	slog.Info("language configuration service enabled")
	accountID := func(r *http.Request) (string, bool) {
		return internalwebapi.AccountIDFromContext(r.Context())
	}
	return &languageHTTPDependencies{
		service: svc,
		handler: languages.NewHandler(svc, accountID),
	}, nil
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

	databaseURL, tokenSecret, err := recordsHTTPConfigurationFromEnv()
	if err != nil {
		return nil, err
	}

	pool, err := recordstore.Open(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("initialize records HTTP: %w", err)
	}
	if err := recordstore.Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("initialize records HTTP: %w", err)
	}

	dependencies, err := newRecordsHTTPDependenciesFromPool(ctx, pool, tokenSecret, accessTokenIssuer, accessTokenAudience)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("initialize records HTTP: %w", err)
	}
	dependencies.cleanup = pool.Close
	return dependencies, nil
}

// newRecordsHTTPDependenciesFromPool wires records HTTP and background workers on
// an already-open pool. The caller owns pool lifecycle unless cleanup is set.
func newRecordsHTTPDependenciesFromPool(
	ctx context.Context,
	pool *pgxpool.Pool,
	tokenSecret, issuer, audience string,
) (*recordsHTTPDependencies, error) {
	if pool == nil {
		return nil, errors.New("records HTTP requires PostgreSQL pool")
	}

	accountRepository := accounts.NewPostgresRepository(pool)
	tokens, err := accounts.NewHMACIssuerWithAccount(
		tokenSecret,
		issuer,
		audience,
		accountRepository.SessionActiveForAccount,
	)
	if err != nil {
		return nil, err
	}
	sessionScope, err := recordstore.NewPostgresSessionScopeReader(pool)
	if err != nil {
		return nil, err
	}

	// Derive a domain-specific key so JWTs and record cursors never use identical key material.
	cursorSigningKey := sha256.Sum256([]byte("lingow-record-cursor\x00" + tokenSecret))
	services, err := recordstore.NewServices(
		pool,
		cursorSigningKey[:],
		recordstore.NewCanonicalSessionOwner(accountRepository),
		sessionScope,
	)
	if err != nil {
		return nil, err
	}

	digester, err := credentialDigesterFromEnv()
	if err != nil {
		return nil, err
	}
	policy, err := accounts.VerificationPolicyFromEnv()
	if err != nil {
		return nil, err
	}
	accountUseCases := accounts.NewPersistentUseCases(
		accountRepository,
		tokens,
		tokens,
		accounts.VerificationSenderFromEnv(),
		digester,
	).WithVerificationPolicy(policy)
	return &recordsHTTPDependencies{
		handler: recordswebapi.NewHandler(recordswebapi.Dependencies{
			Participants: services.Participants,
			Turns:        services.Turns,
			Accounts:     recordswebapi.ContextAccountProvider{},
			// No production system credential exists yet; PATCH routes stay fail-closed.
			System: recordswebapi.ContextSystemAuthorizer{},
			Logger: slog.Default(),
		}),
		accounts:   accountUseCases,
		tokens:     tokens,
		worker:     services.FinalTurnWorker,
		maintainer: accounts.NewAuthMaintainer(accountRepository, 0, 0),
		cleanup:    func() {},
	}, nil
}

func credentialDigesterFromEnv() (*accounts.CredentialDigester, error) {
	pepper := os.Getenv("AUTH_PEPPER")
	if pepper == "" {
		return nil, nil
	}
	return accounts.NewCredentialDigester(pepper)
}

func recordsHTTPConfigurationFromEnv() (string, string, error) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return "", "", fmt.Errorf("initialize records HTTP: DATABASE_URL is required")
	}
	tokenSecret := os.Getenv("JWT_SECRET")
	if tokenSecret == "" {
		return "", "", fmt.Errorf("initialize records HTTP: JWT_SECRET is required")
	}
	if len([]byte(tokenSecret)) < 32 {
		return "", "", fmt.Errorf("initialize records HTTP: JWT_SECRET must be at least 32 bytes")
	}
	return databaseURL, tokenSecret, nil
}

func buildMux(
	lang *languages.Handler,
	sessionHandler *sessions.Handler,
	records *recordswebapi.Server,
	accountUseCases accounts.Service,
	tokens accounts.AccessTokenVerifier,
) *http.ServeMux {
	return buildMuxWithServices(
		lang,
		sessionHandler,
		accountUseCases,
		usage.NewUseCases(),
		delivery.NewUseCases(),
		tokens,
		records,
	)
}

func buildMuxWithServices(
	lang *languages.Handler,
	sessionHandler *sessions.Handler,
	accountService accounts.Service,
	usageService usage.Service,
	deliveryService delivery.Service,
	tokens accounts.AccessTokenVerifier,
	records *recordswebapi.Server,
) *http.ServeMux {
	mux := internalwebapi.New(accountService, usageService, deliveryService, tokens)
	lang.Register(mux, func(next http.Handler) http.Handler {
		return internalwebapi.Authenticate(tokens, next)
	})
	if sessionHandler != nil {
		sessionHandler.Register(mux, func(next http.Handler) http.Handler {
			return internalwebapi.Authenticate(tokens, next)
		})
	}
	if records != nil {
		records.Register(mux, func(next http.Handler) http.Handler {
			return records.Authenticate(tokens, next)
		})
		return mux
	}
	recordswebapi.NewNotImplementedHandler(slog.Default()).Register(mux, func(next http.Handler) http.Handler {
		return internalwebapi.Authenticate(tokens, next)
	})
	return mux
}

func newSessionHandler(service sessions.UseCases) *sessions.Handler {
	accountID := func(r *http.Request) (string, bool) {
		return internalwebapi.AccountIDFromContext(r.Context())
	}
	return sessions.NewHandler(service, accountID)
}
