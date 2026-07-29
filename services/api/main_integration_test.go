//go:build integration

package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/accounts"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/usage"
	"github.com/1024XEngineer/xe6-tsy/services/api/languages"
	"github.com/1024XEngineer/xe6-tsy/services/api/recordstore"
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
	registered, err := accountRepository.FindOrCreateByPhoneHash(t.Context(), "phone_hash_http_merge")
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
