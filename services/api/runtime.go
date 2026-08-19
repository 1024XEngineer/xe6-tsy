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
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/domain"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/modeprojection"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/usage"
	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
	"github.com/1024XEngineer/xe6-tsy/services/api/recordstore"
	"github.com/1024XEngineer/xe6-tsy/services/api/sessions"
	recordswebapi "github.com/1024XEngineer/xe6-tsy/services/api/webapi"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const (
	deliveryComponentRetryDelay = time.Second
	runtimeStartupTimeout       = 30 * time.Second
)

var configuredRuntimeShutdownTimeout = 10 * time.Second

type configuredRuntime struct {
	pool                  *pgxpool.Pool
	redis                 *redis.Client
	dispatcher            *delivery.OutboxDispatcher
	worker                *delivery.Worker
	usageConsumer         *usage.Consumer
	modeConsumer          backgroundWorker
	accountService        accounts.Service
	usageService          usage.Service
	deliveryService       delivery.Service
	tokenVerifier         accounts.AccessTokenVerifier
	sessionRuntimeEnabled bool
	sessionHandler        *sessions.Handler
	sessionRecovery       backgroundWorker
	fallbackWorker        *delivery.AutomaticTurnFallbackWorker
	recordsHandler        *recordswebapi.Server
	finalTurnWorker       finalTurnWorker
	attributionWorker     backgroundWorker
	authMaintainer        backgroundWorker
}

type automaticOutputOwnedSessionReader interface {
	GetOwned(context.Context, string, string) (sessions.VoiceSession, error)
}

type automaticOutputSessionReader struct {
	reader automaticOutputOwnedSessionReader
}

func (r automaticOutputSessionReader) RequireOwnedSession(ctx context.Context, accountID, sessionID string) error {
	if r.reader == nil {
		return domain.ErrNotImplemented
	}
	_, err := r.reader.GetOwned(ctx, accountID, sessionID)
	if errors.Is(err, sessions.ErrVoiceSessionNotFound) {
		return domain.ErrNotFound
	}
	return err
}

func runConfigured(config config.Config) error {
	runtime, languageHandler, err := newConfiguredRuntime(context.Background(), config)
	if err != nil {
		return err
	}
	defer runtime.Close()

	mux := buildMuxWithServices(
		languageHandler,
		runtime.sessionHandler,
		runtime.accountService,
		runtime.usageService,
		runtime.deliveryService,
		runtime.tokenVerifier,
		runtime.recordsHandler,
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
	sessionRepository := sessions.NewPostgresRepository(pool)
	smtpMailer, err := newConfiguredSMTPMailer(processConfig)
	if err != nil {
		return nil, nil, err
	}
	wecomClient, err := newConfiguredWeComClient(processConfig)
	if err != nil {
		return nil, nil, err
	}
	provider, err := configuredProvider(processConfig, smtpMailer, wecomClient)
	if err != nil {
		return nil, nil, err
	}
	providerRouter, ok := provider.(*delivery.ChannelRouter)
	if !ok || providerRouter == nil {
		return nil, nil, errors.New("configured provider does not expose channel capabilities")
	}
	languageDependencies, err := newLanguageDependenciesWithPool(
		startupCtx,
		pool,
		sessionOwnerReader{reader: sessionRepository},
		delivery.NewRuntimeReadiness(delivery.NewPostgresRepository(pool), providerRouter),
	)
	if err != nil {
		return nil, nil, err
	}
	languageDependencies.handler.ConfigureSystemCommands(processConfig.CommandSystemToken)

	records, err := newRecordsHTTPDependenciesFromPool(
		startupCtx,
		pool,
		processConfig.JWTSecret,
		processConfig.JWTIssuer,
		processConfig.JWTAudience,
		processConfig.RecordsSystemToken,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("initialize configured records HTTP: %w", err)
	}
	records.pool = pool

	sessionHandler := newSessionHandler(nil)
	var sessionRecovery backgroundWorker
	var fallbackPlayer delivery.AutomaticTurnFallbackPlayer
	if processConfig.SessionRuntimeEnabled {
		sessionDependencies, err := newSessionHTTPDependencies(sessionCompositionInputs{
			Repository:     sessionRepository,
			SessionReader:  sessionRepository,
			LanguageReader: languageDependencies.service,
			HTTPClient: &http.Client{
				Timeout: realtimeHTTPTimeout(processConfig),
			},
			IDs:    newSessionIDGenerator(),
			Clock:  utcClock{},
			Config: processConfig,
			Logger: slog.Default(),
		})
		if err != nil {
			return nil, nil, fmt.Errorf("initialize configured session HTTP: %w", err)
		}
		sessionHandler = sessionDependencies.handler
		sessionRecovery = sessionDependencies.endRecovery
		fallbackPlayer = sessionDependencies.realtime
	} else {
		slog.Warn(
			"voice session runtime disabled",
			"configuration", "LINGOW_SESSION_RUNTIME",
		)
	}

	redisClient, err := openValkeyClient(startupCtx, processConfig.RedisURL)
	if err != nil {
		return nil, nil, err
	}

	accountRepository := accounts.NewPostgresRepository(pool)
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
	deliveryService.ConfigureAutomaticOutputSessionReader(
		automaticOutputSessionReader{reader: sessionRepository},
	)
	if records.turns != nil {
		records.turns.SetFinalTurnScheduler(deliveryService)
	}
	deliveryService.ConfigureTargetBinding(destinationKey, processConfig.AppEnv)
	deliveryService.ConfigureEmailVerification(deliveryRepository, newEmailBindSender(processConfig, smtpMailer))
	deliveryService.ConfigureWeChatBinding(wecomClient)
	var fallbackWorker *delivery.AutomaticTurnFallbackWorker
	if fallbackPlayer != nil {
		deliveryService.ConfigureAutomaticFallback(fallbackPlayer)
		deliveryService.ConfigureAutomaticOutputRestorer(languageOutputRestorer{service: languageDependencies.service})
		fallbackWorker = delivery.NewAutomaticTurnFallbackWorker(deliveryService, time.Second)
	}

	usageConsumerName := processConfig.UsageConsumer
	if usageConsumerName == "" {
		usageConsumerName = processConfig.DeliveryConsumer + "-usage"
	}
	usageStream, err := usage.NewValkeyUsageStream(startupCtx, redisClient, processConfig.UsageStream, processConfig.UsageGroup, usageConsumerName)
	if err != nil {
		redisClient.Close()
		return nil, nil, fmt.Errorf("initialize usage stream: %w", err)
	}
	usageConsumer := usage.NewConsumer(usageStream, usageService)
	var modeConsumer backgroundWorker
	if processConfig.SessionRuntimeEnabled {
		modeConsumer, err = newModeProjectionConsumer(startupCtx, pool, redisClient, processConfig)
		if err != nil {
			redisClient.Close()
			return nil, nil, err
		}
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
		usageConsumer:         usageConsumer,
		modeConsumer:          modeConsumer,
		accountService:        records.accounts,
		usageService:          usageService,
		deliveryService:       deliveryService,
		tokenVerifier:         records.tokens,
		sessionRuntimeEnabled: processConfig.SessionRuntimeEnabled,
		sessionHandler:        sessionHandler,
		sessionRecovery:       sessionRecovery,
		fallbackWorker:        fallbackWorker,
		recordsHandler:        records.handler,
		finalTurnWorker:       records.worker,
		attributionWorker:     records.attributionWorker,
		authMaintainer:        records.maintainer,
	}
	closeOnError = false
	return runtime, languageDependencies.handler, nil
}

func openValkeyClient(ctx context.Context, rawURL string) (*redis.Client, error) {
	redisOptions, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse REDIS_URL: %w", err)
	}
	client := redis.NewClient(redisOptions)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping Valkey: %w", err)
	}
	return client, nil
}

func newModeProjectionConsumer(
	ctx context.Context,
	pool *pgxpool.Pool,
	client *redis.Client,
	processConfig config.Config,
) (*modeprojection.Consumer, error) {
	stream, err := modeprojection.NewValkeyStream(
		ctx,
		client,
		processConfig.ModeChangedStream,
		processConfig.ModeChangedGroup,
		processConfig.ModeChangedConsumer,
	)
	if err != nil {
		return nil, fmt.Errorf("initialize mode changed stream: %w", err)
	}
	return modeprojection.NewConsumer(stream, modeprojection.NewPostgresRepository(pool)), nil
}

func configuredProvider(processConfig config.Config, smtpMailer *delivery.SMTPMailer, wecomClient *delivery.WeComClient) (delivery.Provider, error) {
	emailProvider, err := configuredEmailProvider(processConfig, smtpMailer)
	if err != nil {
		return nil, err
	}
	wechatProvider, err := configuredWeChatProvider(wecomClient)
	if err != nil {
		return nil, err
	}
	return delivery.NewChannelRouter(emailProvider, wechatProvider), nil
}

func configuredEmailProvider(processConfig config.Config, smtpMailer *delivery.SMTPMailer) (delivery.Provider, error) {
	switch processConfig.DeliveryProvider {
	case "unconfigured", "":
		return delivery.UnconfiguredProvider{}, nil
	case "fake_email":
		return delivery.NewFakeEmailProvider(delivery.FakeEmailProviderConfig{}), nil
	case "smtp":
		if smtpMailer == nil {
			return nil, fmt.Errorf("smtp mailer is required when LINGOW_DELIVERY_PROVIDER=smtp")
		}
		return delivery.NewSMTPProvider(smtpMailer)
	default:
		return nil, fmt.Errorf("unsupported delivery provider %q", processConfig.DeliveryProvider)
	}
}

func configuredWeChatProvider(wecomClient *delivery.WeComClient) (delivery.Provider, error) {
	if wecomClient == nil {
		return delivery.UnconfiguredProvider{}, nil
	}
	return delivery.NewWeComProvider(wecomClient)
}

func newConfiguredWeComClient(processConfig config.Config) (*delivery.WeComClient, error) {
	if processConfig.WeComCorpID == "" {
		return nil, nil
	}
	return delivery.NewWeComClient(delivery.WeComConfig{
		CorpID:     processConfig.WeComCorpID,
		CorpSecret: processConfig.WeComCorpSecret,
		AgentID:    processConfig.WeComAgentIDInt(),
	})
}

func newConfiguredSMTPMailer(processConfig config.Config) (*delivery.SMTPMailer, error) {
	if processConfig.SMTPHost == "" {
		return nil, nil
	}
	return delivery.NewSMTPMailer(delivery.SMTPConfig{
		Host:     processConfig.SMTPHost,
		Port:     processConfig.SMTPPortInt(587),
		Username: processConfig.SMTPUser,
		Password: processConfig.SMTPPassword,
		From:     processConfig.SMTPFrom,
		UseTLS:   processConfig.SMTPTLS,
	})
}

func newEmailBindSender(processConfig config.Config, smtpMailer *delivery.SMTPMailer) delivery.EmailBindSender {
	if smtpMailer != nil {
		return delivery.NewSMTPEmailBindSender(smtpMailer)
	}
	if delivery.AllowsDevEmailBindEnvironment(processConfig.AppEnv) {
		return delivery.LogEmailBindSender{}
	}
	return nil
}

func (r *configuredRuntime) Serve(address string, handler http.Handler) error {
	if r == nil || r.pool == nil || r.redis == nil || r.dispatcher == nil || r.worker == nil ||
		r.sessionHandler == nil || r.recordsHandler == nil || r.finalTurnWorker == nil ||
		handler == nil {
		return errors.New("configured runtime is incomplete")
	}
	if r.sessionRuntimeEnabled && r.sessionRecovery == nil {
		return errors.New("configured runtime is incomplete")
	}
	if r.sessionRuntimeEnabled && r.modeConsumer == nil {
		return errors.New("configured runtime is incomplete")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	componentCtx, cancelComponents := context.WithCancel(ctx)
	defer cancelComponents()

	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: apiReadHeaderTimeout,
		ReadTimeout:       apiReadTimeout,
		WriteTimeout:      apiWriteTimeout,
		IdleTimeout:       apiIdleTimeout,
	}
	errs := make(chan error, 8)
	var components sync.WaitGroup
	components.Add(2)
	go runDeliveryComponent(componentCtx, "outbox dispatcher", r.dispatcher.Run, errs, &components)
	go runDeliveryComponent(componentCtx, "delivery worker", r.worker.Run, errs, &components)
	if r.usageConsumer != nil {
		components.Add(1)
		go runDeliveryComponent(componentCtx, "usage consumer", r.usageConsumer.Run, errs, &components)
	}
	if r.modeConsumer != nil {
		components.Add(1)
		go runDeliveryComponent(componentCtx, "mode projection consumer", r.modeConsumer.Run, errs, &components)
	}
	if r.authMaintainer != nil {
		components.Add(1)
		go runDeliveryComponent(componentCtx, "auth maintainer", r.authMaintainer.Run, errs, &components)
	}
	components.Add(1)
	go runFailFastBackgroundWorker(componentCtx, "final turn worker", r.finalTurnWorker.Run, errs, &components)
	if r.attributionWorker != nil {
		components.Add(1)
		go runFailFastBackgroundWorker(componentCtx, "attribution worker", r.attributionWorker.Run, errs, &components)
	}
	if r.sessionRecovery != nil {
		components.Add(1)
		go runFailFastBackgroundWorker(componentCtx, "session end recovery worker", r.sessionRecovery.Run, errs, &components)
	}
	if r.fallbackWorker != nil {
		components.Add(1)
		go runDeliveryComponent(componentCtx, "automatic fallback worker", r.fallbackWorker.Run, errs, &components)
	}
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

// runFailFastBackgroundWorker supervises records workers whose Run contract requires
// process-level restart on error. FinalTurnWorker returns ErrFinalTurnSettlement when
// receipt settlement is uncertain; retrying in-process would violate the default-path
// shutdown semantics documented for records HTTP composition.
func runFailFastBackgroundWorker(ctx context.Context, name string, run func(context.Context) error, errs chan<- error, components *sync.WaitGroup) {
	defer components.Done()
	err := run(ctx)
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		errs <- fmt.Errorf("%s stopped: %w", name, err)
		return
	}
	errs <- fmt.Errorf("%s stopped unexpectedly", name)
}

func shutdownConfiguredServer(server *http.Server, components *sync.WaitGroup) error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), configuredRuntimeShutdownTimeout)
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
