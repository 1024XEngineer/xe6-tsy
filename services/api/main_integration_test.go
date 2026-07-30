//go:build integration

package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/config"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/accounts"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/usage"
	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
	"github.com/1024XEngineer/xe6-tsy/services/api/recordstore"
	"github.com/1024XEngineer/xe6-tsy/services/api/sessions"
	controlplane "github.com/1024XEngineer/xe6-tsy/services/realtime-audio/controlplane"
	rtsession "github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	rtwebrtc "github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRecordsHTTPProductionCompositionReadsOnlyOwnedTurns(t *testing.T) {
	databaseURL := recordsHTTPTestDatabaseURL(t)
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("JWT_SECRET", strings.Repeat("s", 36))

	dependencies, err := newRecordsHTTPDependencies(t.Context())
	if err != nil {
		t.Fatalf("newRecordsHTTPDependencies() error = %v", err)
	}
	t.Cleanup(dependencies.cleanup)
	if dependencies.worker == nil {
		t.Fatal("newRecordsHTTPDependencies() worker = nil")
	}

	owner, err := dependencies.accounts.CreateAnonymous(t.Context())
	if err != nil {
		t.Fatalf("create owner account: %v", err)
	}
	other, err := dependencies.accounts.CreateAnonymous(t.Context())
	if err != nil {
		t.Fatalf("create other account: %v", err)
	}

	pool, err := recordstore.Open(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("open fixture pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO voice_sessions (id, account_id, status, audio_config, capabilities) VALUES
			('session_http_owner', $1, 'created', '{}'::jsonb, '{}'::jsonb),
			('session_http_other', $2, 'created', '{}'::jsonb, '{}'::jsonb)`,
		owner.Account.ID,
		other.Account.ID,
	); err != nil {
		t.Fatalf("insert records HTTP sessions: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO voice_turns (
			id, event_id, event_payload_hash, session_id, speaker_code, sequence_no,
			source_language, target_language, language_config_version, source_text,
			translated_text, attribution_status, started_at, ended_at, created_at
		) VALUES
			('turn_http_owner', 'event_http_owner', $1, 'session_http_owner', 'speaker_owner', 1,
				'zh-CN', 'en-US', 1, 'owner source', 'owner translation', 'pending', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
			('turn_http_other', 'event_http_other', $1, 'session_http_other', 'speaker_other', 1,
				'zh-CN', 'en-US', 1, 'other source', 'other translation', 'pending', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		make([]byte, 32),
	); err != nil {
		t.Fatalf("insert records HTTP turns: %v", err)
	}

	handler := buildMux(
		languages.NewHandler(nil, nil),
		nil,
		dependencies.handler,
		dependencies.accounts,
		dependencies.tokens,
	)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/translation-history?limit=20", nil)
	request.Header.Set("Authorization", "Bearer "+owner.Tokens.AccessToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("history status = %d, want %d, body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	var history recordsv1.VoiceTurnListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &history); err != nil {
		t.Fatalf("decode history response: %v", err)
	}
	if len(history.Items) != 1 || history.Items[0].ID != "turn_http_owner" {
		t.Fatalf("history items = %#v, want only owner turn", history.Items)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/voice-sessions/session_http_other/turns?limit=20", nil)
	request.Header.Set("Authorization", "Bearer "+owner.Tokens.AccessToken)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("foreign session status = %d, want %d, body = %s", response.Code, http.StatusForbidden, response.Body.String())
	}
	var errorResponse recordsv1.ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &errorResponse); err != nil {
		t.Fatalf("decode foreign session response: %v", err)
	}
	if errorResponse.Error.Code != recordsv1.ErrorForbidden {
		t.Fatalf("foreign session error = %q, want %q", errorResponse.Error.Code, recordsv1.ErrorForbidden)
	}

	accountRepository := accounts.NewPostgresRepository(pool)
	registered, err := accountRepository.FindOrCreateByPhoneHashes(
		t.Context(),
		"phone_hash_v2_http_merge",
		"phone_hash_legacy_http_merge",
	)
	if err != nil {
		t.Fatalf("create registered account: %v", err)
	}
	claims, err := dependencies.tokens.VerifyAccessToken(t.Context(), owner.Tokens.AccessToken)
	if err != nil {
		t.Fatalf("verify anonymous token before binding: %v", err)
	}
	if _, err := accountRepository.BindAnonymous(t.Context(), owner.Account.ID, registered.ID); err != nil {
		t.Fatalf("bind anonymous account: %v", err)
	}
	issuer, ok := dependencies.tokens.(accounts.TokenIssuer)
	if !ok {
		t.Fatal("production token verifier does not implement TokenIssuer")
	}
	registeredTokens, err := issuer.Issue(t.Context(), registered, accounts.Session{ID: claims.SessionID})
	if err != nil {
		t.Fatalf("issue registered token: %v", err)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/translation-history?limit=20", nil)
	request.Header.Set("Authorization", "Bearer "+registeredTokens.AccessToken)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("merged account history status = %d, want %d, body = %s", response.Code, http.StatusOK, response.Body.String())
	}
	history = recordsv1.VoiceTurnListResponse{}
	if err := json.Unmarshal(response.Body.Bytes(), &history); err != nil {
		t.Fatalf("decode merged account history: %v", err)
	}
	if len(history.Items) != 1 || history.Items[0].ID != "turn_http_owner" {
		t.Fatalf("merged account history items = %#v, want original owner turn", history.Items)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/voice-sessions/session_http_owner/turns?limit=20", nil)
	request.Header.Set("Authorization", "Bearer "+registeredTokens.AccessToken)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("merged session turns status = %d, want %d, body = %s", response.Code, http.StatusOK, response.Body.String())
	}

	usageService := usage.NewPersistentUseCases(usage.NewPostgresRepository(pool), accountRepository)
	if _, err := usageService.Record(t.Context(), usage.RecordInput{
		EventVersion:   usage.UsageEventVersion,
		ID:             "usage_http_merge",
		TraceID:        "trace_http_merge",
		IdempotencyKey: "usage-key-http-merge",
		AccountID:      registered.ID,
		SessionID:      "session_http_owner",
		TurnID:         "turn_http_owner",
		ServiceType:    usage.StageTranslation,
		Provider:       "test-provider",
		Model:          "test-model",
		OccurredAt:     time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("record merged-account usage: %v", err)
	}

	var storedAccountID string
	if err := pool.QueryRow(t.Context(), `SELECT account_id FROM lingow_usage_records WHERE event_id=$1`, "usage_http_merge").Scan(&storedAccountID); err != nil {
		t.Fatalf("read merged-account usage: %v", err)
	}
	if storedAccountID != owner.Account.ID {
		t.Fatalf("stored usage account_id = %q, want original owner %q", storedAccountID, owner.Account.ID)
	}
}

func TestSessionProductionCompositionRunsControlPlaneFlow(t *testing.T) {
	databaseURL := recordsHTTPTestDatabaseURL(t)
	pool, err := recordstore.Open(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := recordstore.Migrate(t.Context(), pool); err != nil {
		t.Fatalf("migrate recordstore: %v", err)
	}

	ticketSecret := strings.Repeat("r", 32)
	realtime := newSessionRuntimeControlPlane(t, ticketSecret)
	defer realtime.server.Close()

	sessionRepository := sessions.NewPostgresRepository(pool)
	languageDependencies, err := newLanguageDependenciesWithPool(
		t.Context(),
		pool,
		sessionOwnerReader{reader: sessionRepository},
	)
	if err != nil {
		t.Fatalf("newLanguageDependenciesWithPool() error = %v", err)
	}
	sessionDependencies, err := newSessionHTTPDependencies(sessionCompositionInputs{
		Repository:     sessionRepository,
		SessionReader:  sessionRepository,
		LanguageReader: languageDependencies.service,
		HTTPClient: &http.Client{
			Transport: realtime.server.Client().Transport,
			Timeout:   time.Second,
		},
		IDs:   newSessionIDGenerator(),
		Clock: utcClock{},
		Config: config.Config{
			RealtimeBaseURL:      realtime.server.URL,
			RealtimeTicketSecret: ticketSecret,
		},
	})
	if err != nil {
		t.Fatalf("newSessionHTTPDependencies() error = %v", err)
	}
	records, err := newRecordsHTTPDependenciesFromPool(
		t.Context(),
		pool,
		strings.Repeat("j", 32),
		"lingow-api",
		"lingow-client",
	)
	if err != nil {
		t.Fatalf("newRecordsHTTPDependenciesFromPool() error = %v", err)
	}
	account, err := records.accounts.CreateAnonymous(t.Context())
	if err != nil {
		t.Fatalf("create anonymous account: %v", err)
	}
	mux := buildMux(
		languageDependencies.handler,
		sessionDependencies.handler,
		records.handler,
		records.accounts,
		records.tokens,
	)

	create := serveAPIRequest(t, mux, http.MethodPost, "/api/v1/voice-sessions", account.Tokens.AccessToken, "create-session", strings.NewReader(
		`{"capabilities":{"webrtc":true,"data_channel":true,"microphone":true,"speaker":true,"speaker_diarization":true}}`,
	))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", create.Code, create.Body.String())
	}
	var created sessions.VoiceSession
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created session: %v", err)
	}
	if created.ID == "" || created.AccountID != account.Account.ID || created.Status != sessions.StatusCreated {
		t.Fatalf("created session = %#v", created)
	}

	languageConfig := serveAPIRequest(
		t,
		mux,
		http.MethodPost,
		"/api/v1/voice-sessions/"+created.ID+"/language-configs",
		account.Tokens.AccessToken,
		"language-config",
		strings.NewReader(`{"languages":[{"source":"zh-CN","target":"en-US"},{"source":"en-US","target":"zh-CN"}]}`),
	)
	if languageConfig.Code != http.StatusCreated {
		t.Fatalf("language config status = %d, body = %s", languageConfig.Code, languageConfig.Body.String())
	}

	start := serveAPIRequest(
		t,
		mux,
		http.MethodPost,
		"/api/v1/voice-sessions/"+created.ID+"/start",
		account.Tokens.AccessToken,
		"start-session",
		http.NoBody,
	)
	if start.Code != http.StatusOK {
		t.Fatalf("start status = %d, body = %s", start.Code, start.Body.String())
	}
	var started sessions.VoiceSession
	if err := json.Unmarshal(start.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode started session: %v", err)
	}
	if started.Status != sessions.StatusActive || realtime.startCalls.Load() != 1 {
		t.Fatalf("started session = %#v, realtime start calls = %d", started, realtime.startCalls.Load())
	}

	detail := serveAPIRequest(t, mux, http.MethodGet, "/api/v1/voice-sessions/"+created.ID, account.Tokens.AccessToken, "", http.NoBody)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status = %d, body = %s", detail.Code, detail.Body.String())
	}
	state := serveAPIRequest(t, mux, http.MethodGet, "/api/v1/voice-sessions/"+created.ID+"/state", account.Tokens.AccessToken, "", http.NoBody)
	if state.Code != http.StatusOK {
		t.Fatalf("state status = %d, body = %s", state.Code, state.Body.String())
	}
	runtimeReads := realtime.runtimeCalls.Load()
	list := serveAPIRequest(t, mux, http.MethodGet, "/api/v1/voice-sessions", account.Tokens.AccessToken, "", http.NoBody)
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", list.Code, list.Body.String())
	}
	if realtime.runtimeCalls.Load() != runtimeReads {
		t.Fatalf("list called realtime: before=%d after=%d", runtimeReads, realtime.runtimeCalls.Load())
	}

	end := serveAPIRequest(
		t,
		mux,
		http.MethodPost,
		"/api/v1/voice-sessions/"+created.ID+"/end",
		account.Tokens.AccessToken,
		"end-session",
		http.NoBody,
	)
	if end.Code != http.StatusOK {
		t.Fatalf("end status = %d, body = %s", end.Code, end.Body.String())
	}
	var ended sessions.VoiceSession
	if err := json.Unmarshal(end.Body.Bytes(), &ended); err != nil {
		t.Fatalf("decode ended session: %v", err)
	}
	if ended.Status != sessions.StatusEnded || realtime.stopCalls.Load() != 1 {
		t.Fatalf("ended session = %#v, realtime stop calls = %d", ended, realtime.stopCalls.Load())
	}
}

func serveAPIRequest(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	accessToken string,
	idempotencyKey string,
	body io.Reader,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, body)
	request.Header.Set("Authorization", "Bearer "+accessToken)
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

type sessionRuntimeControlPlane struct {
	server       *httptest.Server
	startCalls   atomic.Int32
	stopCalls    atomic.Int32
	runtimeCalls atomic.Int32
	operationID  atomic.Value
}

func newSessionRuntimeControlPlane(t *testing.T, ticketSecret string) *sessionRuntimeControlPlane {
	t.Helper()
	codec, err := realtimev1.NewHMACTicketCodec(realtimev1.TicketConfig{
		Secret: []byte(ticketSecret),
		TTL:    time.Minute,
	})
	if err != nil {
		t.Fatalf("NewHMACTicketCodec() error = %v", err)
	}
	ticketValidator, err := rtwebrtc.NewHMACTicketValidator(codec)
	if err != nil {
		t.Fatalf("NewHMACTicketValidator() error = %v", err)
	}
	realtime := &sessionRuntimeControlPlane{}
	realtime.operationID.Store("")
	handler, err := controlplane.New(controlplane.Dependencies{
		Lifecycle:   realtime,
		Signaling:   sessionRuntimeSignaling{},
		Connections: realtime,
		Tickets:     ticketValidator,
		Config:      realtime,
		Now:         func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("controlplane.New() error = %v", err)
	}
	realtime.server = httptest.NewServer(handler)
	return realtime
}

func (p *sessionRuntimeControlPlane) Start(_ context.Context, command rtsession.StartRealtimeCommand) (rtsession.RuntimeSnapshot, error) {
	now := time.Now().UTC()
	p.startCalls.Add(1)
	p.operationID.Store(command.OperationID)
	return realtimev1.RuntimeSnapshot{
		SessionID:        command.SessionID,
		StartOperationID: command.OperationID,
		RuntimeState:     realtimev1.RuntimeListening,
		UpdatedAt:        now,
	}, nil
}

func (p *sessionRuntimeControlPlane) Stop(_ context.Context, command rtsession.StopRealtimeCommand) (rtsession.RuntimeSnapshot, error) {
	now := time.Now().UTC()
	p.stopCalls.Add(1)
	operationID, _ := p.operationID.Load().(string)
	return realtimev1.RuntimeSnapshot{
		SessionID:        command.SessionID,
		StartOperationID: operationID,
		RuntimeState:     realtimev1.RuntimeStopped,
		UpdatedAt:        now,
	}, nil
}

func (p *sessionRuntimeControlPlane) GetRuntimeState(_ context.Context, sessionID string) (rtsession.RuntimeSnapshot, error) {
	now := time.Now().UTC()
	p.runtimeCalls.Add(1)
	operationID, _ := p.operationID.Load().(string)
	return realtimev1.RuntimeSnapshot{
		SessionID:        sessionID,
		StartOperationID: operationID,
		RuntimeState:     realtimev1.RuntimeListening,
		UpdatedAt:        now,
	}, nil
}

func (p *sessionRuntimeControlPlane) GetCurrent(_ context.Context, sessionID string) (realtimev1.ConnectionSnapshot, error) {
	return realtimev1.ConnectionSnapshot{
		SessionID:    sessionID,
		ConnectionID: "conn_" + sessionID,
		State:        realtimev1.ConnectionConnected,
		Version:      1,
		UpdatedAt:    time.Now().UTC(),
	}, nil
}

func (p *sessionRuntimeControlPlane) GetConfig(_ context.Context, sessionID string) (controlplane.WebRTCConfig, error) {
	return controlplane.WebRTCConfig{
		SessionID: sessionID,
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}, nil
}

type sessionRuntimeSignaling struct{}

func (sessionRuntimeSignaling) Offer(context.Context, string, string, rtwebrtc.OfferRequest) (rtwebrtc.OfferResponse, error) {
	return rtwebrtc.OfferResponse{}, nil
}

func (sessionRuntimeSignaling) AddCandidates(context.Context, string, string, rtwebrtc.CandidateRequest) (rtwebrtc.CandidateResponse, error) {
	return rtwebrtc.CandidateResponse{}, nil
}

func recordsHTTPTestDatabaseURL(t *testing.T) string {
	t.Helper()
	const environmentVariable = "RECORDSTORE_TEST_DATABASE_URL"

	databaseURL := os.Getenv(environmentVariable)
	if databaseURL == "" {
		t.Skipf("set %s to run PostgreSQL integration tests", environmentVariable)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse %s: %v", environmentVariable, err)
	}
	if !strings.HasSuffix(strings.ToLower(config.ConnConfig.Database), "_test") {
		t.Fatalf("%s must target a dedicated database ending in _test, got %q", environmentVariable, config.ConnConfig.Database)
	}

	admin, err := pgxpool.NewWithConfig(t.Context(), config)
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	t.Cleanup(admin.Close)

	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("generate integration schema name: %v", err)
	}
	schema := fmt.Sprintf("records_http_%x", suffix)
	if _, err := admin.Exec(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create integration schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop integration schema: %v", err)
		}
	})

	parsedURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse integration database URL: %v", err)
	}
	query := parsedURL.Query()
	query.Set("search_path", schema)
	parsedURL.RawQuery = query.Encode()
	return parsedURL.String()
}
