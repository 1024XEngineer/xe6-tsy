package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/config"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/accounts"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/delivery"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/usage"
	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
	"github.com/1024XEngineer/xe6-tsy/services/api/recordstore"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const (
	deliveryComponentRetryDelay = time.Second
	runtimeStartupTimeout       = 30 * time.Second
)

type configuredRuntime struct {
	pool            *pgxpool.Pool
	redis           *redis.Client
	dispatcher      *delivery.OutboxDispatcher
	worker          *delivery.Worker
	accountService  accounts.Service
	usageService    usage.Service
	deliveryService delivery.Service
	tokenVerifier   accounts.AccessTokenVerifier
}

func runConfigured(config config.Config) error {
	runtime, languageHandler, err := newConfiguredRuntime(context.Background(), config)
	if err != nil {
		return err
	}
	defer runtime.Close()

	mux := buildMuxWithServices(
		languageHandler,
		runtime.accountService,
		runtime.usageService,
		runtime.deliveryService,
		runtime.tokenVerifier,
		nil,
	)
	return runtime.Serve(config.APIAddr, mux)
}

// newConfiguredRuntime builds every persistent dependency once. The same pool
// is shared by account, usage, language, record, and delivery adapters so a
// request cannot observe different migration or transaction boundaries.
func newConfiguredRuntime(ctx context.Context, processConfig config.Config) (*configuredRuntime, *languages.Handler, error) {
	startupCtx, cancel := context.WithTimeout(ctx, runtimeStartupTimeout)
	defer cancel()

	pool, err := recordstore.Open(startupCtx, processConfig.DatabaseURL)
	if err != nil {
		return nil, nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			pool.Close()
		}
	}()
	if err := recordstore.Migrate(startupCtx, pool); err != nil {
		return nil, nil, err
	}
	languageHandler, err := newLanguageHandlerWithPool(startupCtx, pool)
	if err != nil {
		return nil, nil, err
	}

	redisOptions, err := redis.ParseURL(processConfig.RedisURL)
	if err != nil {
		return nil, nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	redisClient := redis.NewClient(redisOptions)
	if err := redisClient.Ping(startupCtx).Err(); err != nil {
		redisClient.Close()
		return nil, nil, fmt.Errorf("ping Valkey: %w", err)
	}

	accountRepository := accounts.NewPostgresRepository(pool)
	tokenIssuer, err := accounts.NewHMACIssuerWithAccount(
		processConfig.JWTSecret,
		processConfig.JWTIssuer,
		processConfig.JWTAudience,
		accountRepository.SessionActiveForAccount,
	)
	if err != nil {
		redisClient.Close()
		return nil, nil, err
	}
	accountService := accounts.NewPersistentUseCases(accountRepository, tokenIssuer, tokenIssuer, nil)
	usageService := usage.NewPersistentUseCases(usage.NewPostgresRepository(pool), accountRepository)

	destinationKey, err := delivery.DecodeDestinationKey(processConfig.DestinationKey)
	if err != nil {
		redisClient.Close()
		return nil, nil, fmt.Errorf("decode delivery destination key: %w", err)
	}
	destinationReader, err := delivery.NewPostgresDestinationReader(pool, destinationKey)
	if err != nil {
		redisClient.Close()
		return nil, nil, err
	}
	queue := delivery.NewValkeyQueue(redisClient, delivery.ValkeyQueueConfig{
		Stream:      processConfig.DeliveryStream,
		Group:       processConfig.DeliveryGroup,
		Consumer:    processConfig.DeliveryConsumer,
		DelayStream: processConfig.DeliveryDelayStream,
		DelayKey:    processConfig.DeliveryDelayKey,
	})
	deliveryRepository := delivery.NewPostgresRepository(pool)
	deliveryService := delivery.NewPersistentUseCases(
		deliveryRepository,
		delivery.NewPostgresTurnReader(pool),
		destinationReader,
		queue,
	)
	provider, err := configuredProvider(processConfig.DeliveryProvider)
	if err != nil {
		redisClient.Close()
		return nil, nil, err
	}
	runtime := &configuredRuntime{
		pool:       pool,
		redis:      redisClient,
		dispatcher: delivery.NewOutboxDispatcher(deliveryRepository, queue, time.Second),
		worker: delivery.NewConfiguredWorker(queue, delivery.WorkerDependencies{
			Repository:   deliveryRepository,
			Destinations: destinationReader,
			Provider:     provider,
		}),
		accountService:  accountService,
		usageService:    usageService,
		deliveryService: deliveryService,
		tokenVerifier:   tokenIssuer,
	}
	closeOnError = false
	return runtime, languageHandler, nil
}

func configuredProvider(name string) (delivery.Provider, error) {
	switch name {
	case "unconfigured", "":
		return delivery.UnconfiguredProvider{}, nil
	case "fake_email":
		return delivery.NewFakeEmailProvider(delivery.FakeEmailProviderConfig{}), nil
	default:
		return nil, fmt.Errorf("unsupported delivery provider %q", name)
	}
}

func (r *configuredRuntime) Serve(address string, handler http.Handler) error {
	if r == nil || r.pool == nil || r.redis == nil || r.dispatcher == nil || r.worker == nil || handler == nil {
		return errors.New("configured runtime is incomplete")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	componentCtx, cancelComponents := context.WithCancel(ctx)
	defer cancelComponents()

	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errs := make(chan error, 3)
	var components sync.WaitGroup
	components.Add(2)
	go runDeliveryComponent(componentCtx, "outbox dispatcher", r.dispatcher.Run, errs, &components)
	go runDeliveryComponent(componentCtx, "delivery worker", r.worker.Run, errs, &components)
	go func() {
		slog.Info("Lingow API listening", "address", address, "delivery_runtime", "enabled")
		err := server.ListenAndServe()
		if !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	select {
	case err := <-errs:
		cancelComponents()
		shutdownErr := shutdownConfiguredServer(server, &components)
		return errors.Join(err, shutdownErr)
	case <-ctx.Done():
		cancelComponents()
		return shutdownConfiguredServer(server, &components)
	}
}

func runDeliveryComponent(ctx context.Context, name string, run func(context.Context) error, errs chan<- error, components *sync.WaitGroup) {
	defer components.Done()
	for {
		err := run(ctx)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, delivery.ErrWorkerNotConfigured) {
			errs <- fmt.Errorf("%s stopped: %w", name, err)
			return
		}
		slog.Warn("delivery component stopped; retrying", "component", name, "error", err)
		timer := time.NewTimer(deliveryComponentRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func shutdownConfiguredServer(server *http.Server, components *sync.WaitGroup) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := server.Shutdown(shutdownCtx)
	done := make(chan struct{})
	go func() {
		components.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-shutdownCtx.Done():
		return errors.Join(shutdownErr, shutdownCtx.Err())
	}
	return shutdownErr
}

func (r *configuredRuntime) Close() {
	if r == nil {
		return
	}
	if r.redis != nil {
		_ = r.redis.Close()
	}
	if r.pool != nil {
		r.pool.Close()
	}
}
