package languages

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	languagesv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/languages/v1"
	"github.com/1024XEngineer/xe6-tsy/services/api/internal/webapi"
)

const testCommandSystemToken = "command-system-token-secret-123456"

func TestHTTPConfigureFromCommandCreatesAndReplaysConfig(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	handler := NewHandler(NewService(store, MapSessionOwner{"vs_command": "acct_command"}), nil)
	handler.ConfigureSystemCommands(testCommandSystemToken)
	mux := http.NewServeMux()
	handler.Register(mux, withoutAuthentication)

	request := languagesv1.CommandConfigRequest{
		SessionID:      "vs_command",
		CommandID:      "cmd_1",
		SourceLanguage: "zh-CN",
		TargetLanguage: "en-US",
	}
	first := serveCommandConfig(t, mux, request, testCommandSystemToken)
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	firstBody := append([]byte(nil), first.Body.Bytes()...)
	var firstResult languagesv1.CommandConfigResult
	if err := json.Unmarshal(firstBody, &firstResult); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if firstResult.SessionID != request.SessionID || firstResult.CommandID != request.CommandID || firstResult.Version != 1 {
		t.Fatalf("first result=%#v", firstResult)
	}
	if strings.Contains(string(firstBody), `"source_language"`) || strings.Contains(string(firstBody), `"output_mode"`) {
		t.Fatalf("legacy response contains new fields: %s", firstBody)
	}

	replay := serveCommandConfig(t, mux, request, testCommandSystemToken)
	if replay.Code != http.StatusOK {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	var replayResult languagesv1.CommandConfigResult
	if err := json.NewDecoder(replay.Body).Decode(&replayResult); err != nil {
		t.Fatalf("decode replay response: %v", err)
	}
	if replayResult != firstResult {
		t.Fatalf("replay result=%#v, want %#v", replayResult, firstResult)
	}
	history, _, err := store.ListConfigs(t.Context(), ListConfigsQuery{SessionID: request.SessionID, Limit: 10})
	if err != nil || len(history) != 1 {
		t.Fatalf("history=%#v err=%v, want one config", history, err)
	}
}

func TestHTTPConfigureFromCommandCreatesSingleOutputWithVersionCheck(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	handler := NewHandler(NewService(
		store,
		MapSessionOwner{"vs_command": "acct_command"},
		&deliveryReadinessStub{ready: true},
	), nil)
	handler.ConfigureSystemCommands(testCommandSystemToken)
	mux := http.NewServeMux()
	handler.Register(mux, withoutAuthentication)

	seed := languagesv1.CommandConfigRequest{
		SessionID: "vs_command", CommandID: "cmd_1", SourceLanguage: "zh-CN", TargetLanguage: "en-US",
		OutputMode: languagesv1.InterpretationOutputModeBidirectional,
	}
	if got := serveCommandConfig(t, mux, seed, testCommandSystemToken); got.Code != http.StatusOK {
		t.Fatalf("seed status=%d body=%s", got.Code, got.Body.String())
	}
	expectedVersion := 1
	request := languagesv1.CommandConfigRequest{
		SessionID: "vs_command", CommandID: "cmd_2", SourceLanguage: "zh-CN", TargetLanguage: "en-US",
		OutputMode: languagesv1.InterpretationOutputModeSingle, ExpectedVersion: &expectedVersion,
	}
	response := serveCommandConfig(t, mux, request, testCommandSystemToken)
	if response.Code != http.StatusOK {
		t.Fatalf("single status=%d body=%s", response.Code, response.Body.String())
	}
	var result languagesv1.CommandConfigResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.SourceLanguage != "zh-CN" || result.TargetLanguage != "en-US" ||
		result.OutputMode != languagesv1.InterpretationOutputModeSingle || result.Version != 2 {
		t.Fatalf("result = %#v", result)
	}
	replayExpected := result.Version
	request.ExpectedVersion = &replayExpected
	replay := serveCommandConfig(t, mux, request, testCommandSystemToken)
	if replay.Code != http.StatusOK {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	var replayResult languagesv1.CommandConfigResult
	if err := json.NewDecoder(replay.Body).Decode(&replayResult); err != nil || replayResult.Version != result.Version {
		t.Fatalf("replay result=%#v error=%v", replayResult, err)
	}
	active, err := store.GetActiveConfig(t.Context(), request.SessionID)
	if err != nil {
		t.Fatalf("get active config: %v", err)
	}
	if len(active.OutputRoutes) != 2 || !active.OutputRoutes[0].TTSEnabled ||
		active.OutputRoutes[0].TargetLanguage != "en-US" || !active.OutputRoutes[1].DeliveryEnabled ||
		active.OutputRoutes[1].TargetLanguage != "zh-CN" {
		t.Fatalf("output routes = %#v", active.OutputRoutes)
	}
}

func TestHTTPConfigureFromCommandRejectsIdempotencyConflict(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	handler := NewHandler(NewService(store, MapSessionOwner{"vs_command": "acct_command"}), nil)
	handler.ConfigureSystemCommands(testCommandSystemToken)
	mux := http.NewServeMux()
	handler.Register(mux, withoutAuthentication)

	request := languagesv1.CommandConfigRequest{SessionID: "vs_command", CommandID: "cmd_1", SourceLanguage: "zh-CN", TargetLanguage: "en-US"}
	if got := serveCommandConfig(t, mux, request, testCommandSystemToken); got.Code != http.StatusOK {
		t.Fatalf("seed status=%d body=%s", got.Code, got.Body.String())
	}
	request.SourceLanguage, request.TargetLanguage = request.TargetLanguage, request.SourceLanguage
	got := serveCommandConfig(t, mux, request, testCommandSystemToken)
	assertLanguageError(t, got, http.StatusConflict, CodeIdempotencyConflict)
}

func TestHTTPConfigureFromCommandScopesIdempotencyToSession(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	owners := MapSessionOwner{"vs_command": "acct_command", "vs_other": "acct_command"}
	handler := NewHandler(NewService(store, owners), nil)
	handler.ConfigureSystemCommands(testCommandSystemToken)
	mux := http.NewServeMux()
	handler.Register(mux, withoutAuthentication)

	first := languagesv1.CommandConfigRequest{SessionID: "vs_command", CommandID: "cmd_same", SourceLanguage: "zh-CN", TargetLanguage: "en-US"}
	second := first
	second.SessionID = "vs_other"
	for _, request := range []languagesv1.CommandConfigRequest{first, second} {
		if got := serveCommandConfig(t, mux, request, testCommandSystemToken); got.Code != http.StatusOK {
			t.Fatalf("session %s status=%d body=%s", request.SessionID, got.Code, got.Body.String())
		}
	}

	firstHistory, _, err := store.ListConfigs(t.Context(), ListConfigsQuery{SessionID: first.SessionID, Limit: 10})
	if err != nil || len(firstHistory) != 1 {
		t.Fatalf("first history=%#v err=%v, want one config", firstHistory, err)
	}
	secondHistory, _, err := store.ListConfigs(t.Context(), ListConfigsQuery{SessionID: second.SessionID, Limit: 10})
	if err != nil || len(secondHistory) != 1 {
		t.Fatalf("second history=%#v err=%v, want one config", secondHistory, err)
	}
}

func TestHTTPConfigureFromCommandRejectsSupersededReplay(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	handler := NewHandler(NewService(store, MapSessionOwner{"vs_command": "acct_command"}), nil)
	handler.ConfigureSystemCommands(testCommandSystemToken)
	mux := http.NewServeMux()
	handler.Register(mux, withoutAuthentication)

	first := languagesv1.CommandConfigRequest{SessionID: "vs_command", CommandID: "cmd_1", SourceLanguage: "zh-CN", TargetLanguage: "en-US"}
	second := languagesv1.CommandConfigRequest{SessionID: "vs_command", CommandID: "cmd_2", SourceLanguage: "en-US", TargetLanguage: "zh-CN"}
	for _, request := range []languagesv1.CommandConfigRequest{first, second} {
		if got := serveCommandConfig(t, mux, request, testCommandSystemToken); got.Code != http.StatusOK {
			t.Fatalf("seed command %s status=%d body=%s", request.CommandID, got.Code, got.Body.String())
		}
	}

	assertLanguageError(t, serveCommandConfig(t, mux, first, testCommandSystemToken), http.StatusConflict, CodeStaleCommand)
	active, err := store.GetActiveConfig(t.Context(), first.SessionID)
	if err != nil {
		t.Fatalf("get active config: %v", err)
	}
	if active.Version != 2 || active.LanguagePairs[0].Source != "en-US" || active.LanguagePairs[0].Target != "zh-CN" {
		t.Fatalf("active config=%#v, want second command config", active)
	}
}

func TestHTTPConfigureFromCommandRejectsInvalidRequests(t *testing.T) {
	valid := languagesv1.CommandConfigRequest{SessionID: "vs_command", CommandID: "cmd_1", SourceLanguage: "zh-CN", TargetLanguage: "en-US"}
	tests := []struct {
		name       string
		token      string
		configured string
		path       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{name: "missing token", configured: testCommandSystemToken, path: "vs_command", body: mustJSON(t, valid), wantStatus: http.StatusForbidden},
		{name: "wrong token", token: "wrong", configured: testCommandSystemToken, path: "vs_command", body: mustJSON(t, valid), wantStatus: http.StatusForbidden},
		{name: "endpoint disabled", token: testCommandSystemToken, path: "vs_command", body: mustJSON(t, valid), wantStatus: http.StatusForbidden},
		{name: "session mismatch", token: testCommandSystemToken, configured: testCommandSystemToken, path: "other", body: mustJSON(t, valid), wantStatus: http.StatusBadRequest, wantCode: CodeInvalidRequest},
		{name: "unknown field", token: testCommandSystemToken, configured: testCommandSystemToken, path: "vs_command", body: strings.TrimSuffix(mustJSON(t, valid), "}") + `,"extra":true}`, wantStatus: http.StatusBadRequest, wantCode: CodeInvalidRequest},
		{name: "trailing json", token: testCommandSystemToken, configured: testCommandSystemToken, path: "vs_command", body: mustJSON(t, valid) + `{}`, wantStatus: http.StatusBadRequest, wantCode: CodeInvalidRequest},
		{name: "oversized body", token: testCommandSystemToken, configured: testCommandSystemToken, path: "vs_command", body: mustJSON(t, valid) + strings.Repeat(" ", maxRequestBodyBytes), wantStatus: http.StatusBadRequest, wantCode: CodeInvalidRequest},
		{name: "command id too long", token: testCommandSystemToken, configured: testCommandSystemToken, path: "vs_command", body: mustJSON(t, languagesv1.CommandConfigRequest{SessionID: "vs_command", CommandID: strings.Repeat("x", languagesv1.MaxCommandIDLength+1), SourceLanguage: "zh-CN", TargetLanguage: "en-US"}), wantStatus: http.StatusBadRequest, wantCode: CodeInvalidRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHandler(NewService(NewMemoryStore(nil, nil), MapSessionOwner{"vs_command": "acct_command"}), nil)
			handler.ConfigureSystemCommands(tt.configured)
			mux := http.NewServeMux()
			handler.Register(mux, withoutAuthentication)
			req := httptest.NewRequest(http.MethodPost, "/internal/v1/voice-sessions/"+tt.path+"/language-config", strings.NewReader(tt.body))
			req.Header.Set(systemTokenHeader, tt.token)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if tt.wantCode == "" {
				if rec.Code != tt.wantStatus {
					t.Fatalf("status=%d body=%s, want %d", rec.Code, rec.Body.String(), tt.wantStatus)
				}
				return
			}
			assertLanguageError(t, rec, tt.wantStatus, tt.wantCode)
		})
	}
}

func TestHTTPConfigureFromCommandMapsSessionAndLanguageErrors(t *testing.T) {
	tests := []struct {
		name       string
		request    languagesv1.CommandConfigRequest
		owners     MapSessionOwner
		wantStatus int
		wantCode   string
	}{
		{
			name:       "session not found",
			request:    languagesv1.CommandConfigRequest{SessionID: "missing", CommandID: "cmd_1", SourceLanguage: "zh-CN", TargetLanguage: "en-US"},
			owners:     MapSessionOwner{},
			wantStatus: http.StatusNotFound,
			wantCode:   CodeSessionNotFound,
		},
		{
			name:       "unsupported language",
			request:    languagesv1.CommandConfigRequest{SessionID: "vs_command", CommandID: "cmd_1", SourceLanguage: "zh-CN", TargetLanguage: "ja-JP"},
			owners:     MapSessionOwner{"vs_command": "acct_command"},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   CodeUnsupportedLanguage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewHandler(NewService(NewMemoryStore(nil, nil), tt.owners), nil)
			handler.ConfigureSystemCommands(testCommandSystemToken)
			mux := http.NewServeMux()
			handler.Register(mux, withoutAuthentication)
			assertLanguageError(t, serveCommandConfig(t, mux, tt.request, testCommandSystemToken), tt.wantStatus, tt.wantCode)
		})
	}
}

func serveCommandConfig(t *testing.T, handler http.Handler, request languagesv1.CommandConfigRequest, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/internal/v1/voice-sessions/"+request.SessionID+"/language-config", strings.NewReader(mustJSON(t, request)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(systemTokenHeader, token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return string(payload)
}

func assertLanguageError(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode string) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status=%d body=%s, want %d", rec.Code, rec.Body.String(), wantStatus)
	}
	var body ErrorBody
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Error.Code != wantCode {
		t.Fatalf("code=%q body=%#v, want %q", body.Error.Code, body, wantCode)
	}
}

func TestHTTPCreateAndGetConfig(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	svc := NewService(store, MapSessionOwner{"vs_http": "acct_http"})
	mux := http.NewServeMux()
	NewHandler(svc, func(r *http.Request) (string, bool) {
		return webapi.AccountIDFromContext(r.Context())
	}).Register(mux, withoutAuthentication)

	body, _ := json.Marshal(CreateLanguageConfigRequest{Languages: bilingualPairs()})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/voice-sessions/vs_http/language-configs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "ik_http_1")
	req.Header.Set("X-Request-ID", "req_http_1")
	req = req.WithContext(webapi.WithAccountID(req.Context(), "acct_http"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/voice-sessions/vs_http/language-config", nil)
	getReq = getReq.WithContext(webapi.WithAccountID(context.Background(), "acct_http"))
	getRec := httptest.NewRecorder()
	mux.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", getRec.Code, getRec.Body.String())
	}

	var cfg LanguageConfig
	if err := json.NewDecoder(getRec.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Version != 1 || cfg.SessionID != "vs_http" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestHTTPUnauthorized(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	svc := NewService(store, MapSessionOwner{})
	mux := http.NewServeMux()
	NewHandler(svc, nil).Register(mux, withoutAuthentication)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/languages", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", rec.Code)
	}
}

func TestHTTPIdempotencyKeyTooLong(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	svc := NewService(store, MapSessionOwner{"vs_http": "acct_http"})
	mux := http.NewServeMux()
	NewHandler(svc, func(r *http.Request) (string, bool) {
		return webapi.AccountIDFromContext(r.Context())
	}).Register(mux, withoutAuthentication)

	body, _ := json.Marshal(CreateLanguageConfigRequest{Languages: bilingualPairs()})
	longKey := make([]byte, MaxIdempotencyKeyLen+1)
	for i := range longKey {
		longKey[i] = 'a'
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/voice-sessions/vs_http/language-configs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", string(longKey))
	req.Header.Set("X-Request-ID", "req_key_len")
	req = req.WithContext(webapi.WithAccountID(req.Context(), "acct_http"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
	var errBody ErrorBody
	if err := json.NewDecoder(rec.Body).Decode(&errBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errBody.Error.Code != CodeInvalidRequest {
		t.Fatalf("code=%q, want %q", errBody.Error.Code, CodeInvalidRequest)
	}
}

func TestHTTPCreateConfigRejectsOversizedBody(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	svc := NewService(store, MapSessionOwner{"vs_http": "acct_http"})
	mux := http.NewServeMux()
	NewHandler(svc, func(r *http.Request) (string, bool) {
		return webapi.AccountIDFromContext(r.Context())
	}).Register(mux, withoutAuthentication)

	validBody, err := json.Marshal(CreateLanguageConfigRequest{Languages: bilingualPairs()})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	body := append(validBody, bytes.Repeat([]byte(" "), maxRequestBodyBytes-len(validBody)+1)...)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/voice-sessions/vs_http/language-configs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "oversized-body")
	req = req.WithContext(webapi.WithAccountID(req.Context(), "acct_http"))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

func TestHTTPSingleOutputRequiresReadyDeliveryTarget(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	svc := NewService(store, MapSessionOwner{"vs_http": "acct_http"}, &deliveryReadinessStub{})
	mux := http.NewServeMux()
	NewHandler(svc, func(r *http.Request) (string, bool) {
		return webapi.AccountIDFromContext(r.Context())
	}).Register(mux, withoutAuthentication)

	body, _ := json.Marshal(CreateLanguageConfigRequest{
		Languages: bilingualPairs(),
		OutputRoutes: []OutputRoute{
			{TargetLanguage: "en-US", TTSEnabled: true},
			{TargetLanguage: "zh-CN", DeliveryEnabled: true},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/voice-sessions/vs_http/language-configs", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(webapi.WithAccountID(req.Context(), "acct_http"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s, want 422", rec.Code, rec.Body.String())
	}
	var errBody ErrorBody
	if err := json.NewDecoder(rec.Body).Decode(&errBody); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errBody.Error.Code != CodeDeliveryTargetRequired {
		t.Fatalf("code=%q, want %q", errBody.Error.Code, CodeDeliveryTargetRequired)
	}
	if errBody.Error.Retryable || errBody.Error.Details == nil {
		t.Fatalf("error metadata = %#v, want non-retryable error with details", errBody.Error)
	}
}

func TestHTTPAutomaticDeliveryReadiness(t *testing.T) {
	tests := []struct {
		name      string
		readiness DeliveryReadinessReader
		want      bool
	}{
		{name: "runtime unavailable", want: false},
		{name: "runtime ready", readiness: &deliveryReadinessStub{ready: true}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(NewMemoryStore(nil, nil), MapSessionOwner{}, tt.readiness)
			mux := http.NewServeMux()
			NewHandler(svc, func(*http.Request) (string, bool) {
				return "acct_http", true
			}).Register(mux, withoutAuthentication)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/account/automatic-delivery-readiness", nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var response AutomaticDeliveryReadinessResponse
			if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Ready != tt.want {
				t.Fatalf("ready=%v, want %v", response.Ready, tt.want)
			}
		})
	}
}

func TestHTTPListLanguages(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	svc := NewService(store, MapSessionOwner{})
	mux := http.NewServeMux()
	NewHandler(svc, func(r *http.Request) (string, bool) {
		return "acct_http", true
	}).Register(mux, withoutAuthentication)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/languages?active=true", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHTTPListConfigHistory(t *testing.T) {
	store := NewMemoryStore(nil, nil)
	svc := NewService(store, MapSessionOwner{"vs_http": "acct_http"})
	if _, err := svc.CreateConfig(t.Context(), "acct_http", "vs_http", "seed-1", CreateLanguageConfigRequest{
		Languages: bilingualPairs(),
	}); err != nil {
		t.Fatalf("seed v1: %v", err)
	}
	if _, err := svc.CreateConfig(t.Context(), "acct_http", "vs_http", "seed-2", CreateLanguageConfigRequest{
		Languages: bilingualPairs(),
	}); err != nil {
		t.Fatalf("seed v2: %v", err)
	}

	mux := http.NewServeMux()
	NewHandler(svc, func(r *http.Request) (string, bool) {
		return webapi.AccountIDFromContext(r.Context())
	}).Register(mux, withoutAuthentication)

	t.Run("default_limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/voice-sessions/vs_http/language-configs", nil)
		req = req.WithContext(webapi.WithAccountID(req.Context(), "acct_http"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}

		var resp ListLanguageConfigsResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Items) != 2 || resp.NextCursor != nil {
			t.Fatalf("response=%#v", resp)
		}
		if resp.Items[0].Version != 2 || resp.Items[1].Version != 1 {
			t.Fatalf("history order = %#v", resp.Items)
		}
	})

	t.Run("paged", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/voice-sessions/vs_http/language-configs?limit=1", nil)
		req = req.WithContext(webapi.WithAccountID(req.Context(), "acct_http"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}

		var first ListLanguageConfigsResponse
		if err := json.NewDecoder(rec.Body).Decode(&first); err != nil {
			t.Fatalf("decode first page: %v", err)
		}
		if len(first.Items) != 1 || first.NextCursor == nil || first.Items[0].Version != 2 {
			t.Fatalf("first page=%#v", first)
		}

		req = httptest.NewRequest(http.MethodGet, "/api/v1/voice-sessions/vs_http/language-configs?limit=1&cursor="+*first.NextCursor, nil)
		req = req.WithContext(webapi.WithAccountID(req.Context(), "acct_http"))
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}

		var second ListLanguageConfigsResponse
		if err := json.NewDecoder(rec.Body).Decode(&second); err != nil {
			t.Fatalf("decode second page: %v", err)
		}
		if len(second.Items) != 1 || second.NextCursor != nil || second.Items[0].Version != 1 {
			t.Fatalf("second page=%#v", second)
		}
	})

	t.Run("invalid_limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/voice-sessions/vs_http/language-configs?limit=0", nil)
		req = req.WithContext(webapi.WithAccountID(req.Context(), "acct_http"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var errBody ErrorBody
		if err := json.NewDecoder(rec.Body).Decode(&errBody); err != nil {
			t.Fatalf("decode error body: %v", err)
		}
		if errBody.Error.Code != CodeInvalidRequest {
			t.Fatalf("error code=%q", errBody.Error.Code)
		}
	})

	t.Run("unauthenticated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/voice-sessions/vs_http/language-configs", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("forbidden", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/voice-sessions/vs_http/language-configs", nil)
		req = req.WithContext(webapi.WithAccountID(req.Context(), "acct_other"))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	})
}

func withoutAuthentication(next http.Handler) http.Handler {
	return next
}
