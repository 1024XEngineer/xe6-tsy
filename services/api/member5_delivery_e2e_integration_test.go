//go:build integration

package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/api/config"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/accounts"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/delivery"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/usage"
	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
	"github.com/1024XEngineer/xe6-tsy/services/api/recordstore"
	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func TestMember5DeliveryAcceptanceFromHTTPToProvider(t *testing.T) {
	fixture := newMember5DeliveryE2EFixture(t)

	authResponse := httptest.NewRecorder()
	fixture.handler.ServeHTTP(authResponse, httptest.NewRequest(http.MethodPost, "/api/v1/auth/anonymous", nil))
	if authResponse.Code != http.StatusCreated {
		t.Fatalf("anonymous auth status = %d, want %d, body = %s", authResponse.Code, http.StatusCreated, authResponse.Body.String())
	}
	var auth accounts.AuthResult
	if err := json.Unmarshal(authResponse.Body.Bytes(), &auth); err != nil {
		t.Fatalf("decode anonymous auth: %v", err)
	}
	seedDeliveryTurnFixture(
		t,
		fixture.pool,
		auth.Account.ID,
		fixture.destinationKey,
		fixture.targetEmail,
		"session_delivery_e2e",
		"turn_delivery_e2e",
	)

	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/outbound-messages", strings.NewReader(
		`{"channel":"email","destination_ref":"primary-email","turn_ids":["turn_delivery_e2e"]}`,
	))
	createRequest.Header.Set("Authorization", "Bearer "+auth.Tokens.AccessToken)
	createRequest.Header.Set("Content-Type", "application/json")
	createRequest.Header.Set("Idempotency-Key", "member5-delivery-e2e-create")
	createResponse := httptest.NewRecorder()
	fixture.handler.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusAccepted {
		t.Fatalf("create message status = %d, want %d, body = %s", createResponse.Code, http.StatusAccepted, createResponse.Body.String())
	}
	var created delivery.Message
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create message response: %v", err)
	}
	if created.Status != delivery.MessageStatusQueued {
		t.Fatalf("created message status = %q, want %q", created.Status, delivery.MessageStatusQueued)
	}

	if err := fixture.dispatcher.DispatchOnce(t.Context()); err != nil {
		t.Fatalf("DispatchOnce() error = %v", err)
	}

	receiveCtx, cancelReceive := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelReceive()
	item, err := fixture.queue.Receive(receiveCtx)
	if err != nil {
		t.Fatalf("queue.Receive() error = %v", err)
	}
	if err := fixture.worker.Process(t.Context(), item); err != nil {
		t.Fatalf("worker.Process() error = %v", err)
	}

	delivered, err := fixture.repository.GetMessage(t.Context(), auth.Account.ID, created.ID)
	if err != nil {
		t.Fatalf("GetMessage() error = %v", err)
	}
	if delivered.Status != delivery.MessageStatusSent {
		t.Fatalf("delivered message status = %q, want %q", delivered.Status, delivery.MessageStatusSent)
	}

	requests := fixture.provider.Requests()
	if len(requests) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(requests))
	}
	if requests[0].Destination.ProviderTarget != fixture.targetEmail {
		t.Fatalf("provider target = %q, want %q", requests[0].Destination.ProviderTarget, fixture.targetEmail)
	}
	if requests[0].Message.AccountID != auth.Account.ID {
		t.Fatalf("provider message account = %q, want %q", requests[0].Message.AccountID, auth.Account.ID)
	}
}

func TestConfiguredRuntimeCompositionExposesDeliveryAndRecordsRoutes(t *testing.T) {
	databaseURL := recordsHTTPTestDatabaseURL(t)
	destinationKey, encodedDestinationKey := testDeliveryDestinationKey(t)

	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	t.Cleanup(server.Close)

	t.Setenv("APP_ENV", "local")
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("REDIS_URL", "redis://"+server.Addr())
	t.Setenv("JWT_SECRET", strings.Repeat("j", 36))
	t.Setenv("AUTH_PEPPER", strings.Repeat("p", 36))
	t.Setenv("VERIFICATION_SENDER", "log")
	t.Setenv("LINGOW_DELIVERY_RUNTIME", "enabled")
	t.Setenv("LINGOW_DELIVERY_DESTINATION_KEY", encodedDestinationKey)
	t.Setenv("LINGOW_DELIVERY_PROVIDER", "fake_email")
	t.Setenv("LINGOW_DELIVERY_CONSUMER", "member5-runtime-test")

	processConfig, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}

	runtime, languageHandler, err := newConfiguredRuntime(t.Context(), processConfig)
	if err != nil {
		t.Fatalf("newConfiguredRuntime() error = %v", err)
	}
	t.Cleanup(runtime.Close)

	pool, err := recordstore.Open(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("open fixture pool: %v", err)
	}
	t.Cleanup(pool.Close)

	auth, accountService := seedConfiguredRuntimeDeliveryFixtures(t, pool, destinationKey, runtime.accountService)

	handler := buildMuxWithServices(
		languageHandler,
		runtime.sessionHandler,
		accountService,
		runtime.usageService,
		runtime.deliveryService,
		runtime.tokenVerifier,
		runtime.recordsHandler,
	)

	historyRequest := httptest.NewRequest(http.MethodGet, "/api/v1/translation-history?limit=20", nil)
	historyRequest.Header.Set("Authorization", "Bearer "+auth.Tokens.AccessToken)
	historyResponse := httptest.NewRecorder()
	handler.ServeHTTP(historyResponse, historyRequest)
	if historyResponse.Code == http.StatusNotImplemented {
		t.Fatalf("records route returned 501, want real handler")
	}
	if historyResponse.Code != http.StatusOK {
		t.Fatalf("history status = %d, want %d, body = %s", historyResponse.Code, http.StatusOK, historyResponse.Body.String())
	}

	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/outbound-messages", strings.NewReader(
		`{"channel":"email","destination_ref":"primary-email","turn_ids":["turn_runtime_e2e"]}`,
	))
	createRequest.Header.Set("Authorization", "Bearer "+auth.Tokens.AccessToken)
	createRequest.Header.Set("Content-Type", "application/json")
	createRequest.Header.Set("Idempotency-Key", "member5-runtime-e2e-create")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createRequest)
	if createResponse.Code == http.StatusNotImplemented {
		t.Fatalf("delivery route returned 501, want real handler")
	}
	if createResponse.Code != http.StatusAccepted {
		t.Fatalf("create message status = %d, want %d, body = %s", createResponse.Code, http.StatusAccepted, createResponse.Body.String())
	}
}

type member5DeliveryE2EFixture struct {
	handler        http.Handler
	dispatcher     *delivery.OutboxDispatcher
	queue          delivery.Queue
	worker         *delivery.Worker
	repository     *delivery.PostgresRepository
	provider       *delivery.FakeEmailProvider
	pool           *pgxpool.Pool
	destinationKey []byte
	targetEmail    string
}

func newMember5DeliveryE2EFixture(t *testing.T) *member5DeliveryE2EFixture {
	t.Helper()

	databaseURL := recordsHTTPTestDatabaseURL(t)
	pool, err := recordstore.Open(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("open delivery e2e pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := recordstore.Migrate(t.Context(), pool); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	destinationKey, _ := testDeliveryDestinationKey(t)
	targetEmail := "member5-delivery@example.test"

	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	t.Cleanup(server.Close)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	var consumerSuffix [4]byte
	if _, err := rand.Read(consumerSuffix[:]); err != nil {
		t.Fatalf("generate queue consumer suffix: %v", err)
	}
	queue := delivery.NewValkeyQueue(client, delivery.ValkeyQueueConfig{
		Stream:   "member5-delivery-e2e",
		Group:    "member5-delivery-e2e",
		Consumer: fmt.Sprintf("member5-consumer-%x", consumerSuffix),
		Block:    100 * time.Millisecond,
	})

	repository := delivery.NewPostgresRepository(pool)
	destinationReader, err := delivery.NewPostgresDestinationReader(pool, destinationKey)
	if err != nil {
		t.Fatalf("NewPostgresDestinationReader() error = %v", err)
	}
	turnReader := delivery.NewPostgresTurnReader(pool)
	deliveryService := delivery.NewPersistentUseCases(repository, turnReader, destinationReader, queue)
	deliveryService.ConfigureTargetBinding(destinationKey, "local")

	accountRepository := accounts.NewPostgresRepository(pool)
	tokenSecret := strings.Repeat("j", 36)
	tokens, err := accounts.NewHMACIssuerWithAccount(tokenSecret, "lingow-api", "lingow-client", accountRepository.SessionActiveForAccount)
	if err != nil {
		t.Fatalf("NewHMACIssuerWithAccount() error = %v", err)
	}
	digester, err := accounts.NewCredentialDigester(strings.Repeat("p", 36))
	if err != nil {
		t.Fatalf("NewCredentialDigester() error = %v", err)
	}
	accountService := accounts.NewPersistentUseCases(accountRepository, tokens, tokens, accounts.VerificationSenderFromEnv(), digester)
	usageService := usage.NewPersistentUseCases(usage.NewPostgresRepository(pool), accountRepository)

	provider := delivery.NewFakeEmailProvider(delivery.FakeEmailProviderConfig{})
	worker := delivery.NewConfiguredWorker(queue, delivery.WorkerDependencies{
		Repository:   repository,
		Destinations: destinationReader,
		Provider:     provider,
	})
	dispatcher := delivery.NewOutboxDispatcher(repository, queue, time.Millisecond)

	handler := buildMuxWithServices(
		languages.NewHandler(nil, nil),
		nil,
		accountService,
		usageService,
		deliveryService,
		tokens,
		nil,
	)

	return &member5DeliveryE2EFixture{
		handler:        handler,
		dispatcher:     dispatcher,
		queue:          queue,
		worker:         worker,
		repository:     repository,
		provider:       provider,
		pool:           pool,
		destinationKey: destinationKey,
		targetEmail:    targetEmail,
	}
}

func seedConfiguredRuntimeDeliveryFixtures(t *testing.T, pool *pgxpool.Pool, destinationKey []byte, accountService accounts.Service) (accounts.AuthResult, accounts.Service) {
	t.Helper()
	auth, err := accountService.CreateAnonymous(t.Context())
	if err != nil {
		t.Fatalf("CreateAnonymous() error = %v", err)
	}
	seedDeliveryTurnFixture(t, pool, auth.Account.ID, destinationKey, "runtime-e2e@example.test", "session_runtime_e2e", "turn_runtime_e2e")
	return auth, accountService
}

func seedDeliveryTurnFixture(t *testing.T, pool *pgxpool.Pool, accountID string, destinationKey []byte, targetEmail, sessionID, turnID string) {
	t.Helper()

	ciphertext, err := delivery.EncryptProviderTarget(destinationKey, targetEmail)
	if err != nil {
		t.Fatalf("EncryptProviderTarget() error = %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO voice_sessions (id, account_id, status, audio_config, capabilities)
		VALUES ($1, $2, 'created', '{}'::jsonb, '{}'::jsonb)`,
		sessionID, accountID,
	); err != nil {
		t.Fatalf("insert voice session: %v", err)
	}
	payloadHash := make([]byte, 32)
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO voice_turns (
			id, event_id, event_payload_hash, session_id, speaker_code, sequence_no,
			source_language, target_language, language_config_version, source_text,
			translated_text, attribution_status, started_at, ended_at, created_at
		) VALUES (
			$1, $2, $3, $4, 'speaker_delivery', 1,
			'zh-CN', 'en-US', 1, 'delivery source', 'delivery translation',
			'pending', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		)`,
		turnID, "event_"+turnID, payloadHash, sessionID,
	); err != nil {
		t.Fatalf("insert voice turn: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO account_destinations (
			id, account_id, channel, destination_ref, provider_target_ciphertext,
			key_version, verified_at, created_at, updated_at
		) VALUES ($1, $2, 'email', 'primary-email', $3, 'v1', $4, $4, $4)`,
		"dest_"+accountID, accountID, ciphertext, now,
	); err != nil {
		t.Fatalf("insert verified destination: %v", err)
	}
}

func testDeliveryDestinationKey(t *testing.T) ([]byte, string) {
	t.Helper()
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	return key, base64.RawURLEncoding.EncodeToString(key)
}
