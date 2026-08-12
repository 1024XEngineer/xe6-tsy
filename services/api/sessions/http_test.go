package sessions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

func TestHandlerCreatePassesCanonicalRequest(t *testing.T) {
	now := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
	useCases := &handlerUseCases{
		createResult: VoiceSession{ID: "vs_1", AccountID: "acct_1", Status: StatusCreated, CreatedAt: now},
	}
	handler := NewHandler(useCases, headerAccount)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/voice-sessions",
		bytes.NewBufferString(`{
			"capabilities": {
				"webrtc": true,
				"data_channel": true,
				"microphone": true,
				"speaker": true,
				"speaker_diarization": true
			}
		}`),
	)
	request.Header.Set("X-Test-Account", "acct_1")
	request.Header.Set("Idempotency-Key", "create-key")
	response := httptest.NewRecorder()

	handler.create(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body %s", response.Code, response.Body.String())
	}
	if useCases.createInput.AccountID != "acct_1" ||
		useCases.createInput.IdempotencyKey != "create-key" ||
		useCases.createInput.RequestHash == "" {
		t.Fatalf("CreateInput = %#v", useCases.createInput)
	}
	wantHash := canonicalHash("voice-sessions.create", createRequest{
		Capabilities: validCapabilities(),
	})
	if useCases.createInput.RequestHash != wantHash {
		t.Fatalf("RequestHash = %q, want %q", useCases.createInput.RequestHash, wantHash)
	}
}

func TestHandlerRejectsClientAccountFields(t *testing.T) {
	useCases := &handlerUseCases{}
	handler := NewHandler(useCases, headerAccount)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/voice-sessions",
		bytes.NewBufferString(`{"account_id":"acct_2","capabilities":{}}`),
	)
	request.Header.Set("X-Test-Account", "acct_1")
	request.Header.Set("Idempotency-Key", "create-key")
	response := httptest.NewRecorder()

	handler.create(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("create with account_id status = %d, want 400", response.Code)
	}
	if useCases.createCalls != 0 {
		t.Fatalf("Create calls = %d, want 0", useCases.createCalls)
	}
}

func TestHandlerStartDefaultsToInterpretationAndAddsTrace(t *testing.T) {
	useCases := &handlerUseCases{
		startResult: VoiceSession{ID: "vs_1", AccountID: "acct_1", Status: StatusActive},
	}
	handler := NewHandler(useCases, headerAccount)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/voice-sessions/vs_1/start",
		http.NoBody,
	)
	request.SetPathValue("id", "vs_1")
	request.Header.Set("X-Test-Account", "acct_1")
	request.Header.Set("Idempotency-Key", "start-key")
	request.Header.Set("X-Request-ID", "req_1")
	response := httptest.NewRecorder()

	handler.start(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200; body %s", response.Code, response.Body.String())
	}
	if useCases.startInput.AccountID != "acct_1" ||
		useCases.startInput.SessionID != "vs_1" ||
		useCases.startInput.TraceID != "req_1" ||
		useCases.startInput.StartedBy != "acct_1" ||
		useCases.startInput.InitialMode != realtimev1.ModeInterpretation {
		t.Fatalf("StartInput = %#v", useCases.startInput)
	}
	wantHash := canonicalHash("voice-sessions.start", struct {
		SessionID string `json:"session_id"`
	}{SessionID: "vs_1"})
	if useCases.startInput.RequestHash != wantHash {
		t.Fatalf("RequestHash = %q, want %q", useCases.startInput.RequestHash, wantHash)
	}
}

func TestHandlerStartAcceptsAssistantMode(t *testing.T) {
	useCases := &handlerUseCases{
		startResult: VoiceSession{ID: "vs_1", AccountID: "acct_1", Status: StatusActive},
	}
	handler := NewHandler(useCases, headerAccount)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/voice-sessions/vs_1/start",
		bytes.NewBufferString(`{"initial_mode":"assistant"}`),
	)
	request.SetPathValue("id", "vs_1")
	request.Header.Set("X-Test-Account", "acct_1")
	request.Header.Set("Idempotency-Key", "start-key")
	response := httptest.NewRecorder()

	handler.start(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200; body %s", response.Code, response.Body.String())
	}
	if useCases.startInput.InitialMode != realtimev1.ModeAssistant {
		t.Fatalf("InitialMode = %q, want assistant", useCases.startInput.InitialMode)
	}
	wantHash := canonicalHash("voice-sessions.start", struct {
		SessionID   string          `json:"session_id"`
		InitialMode realtimev1.Mode `json:"initial_mode"`
	}{SessionID: "vs_1", InitialMode: realtimev1.ModeAssistant})
	if useCases.startInput.RequestHash != wantHash {
		t.Fatalf("RequestHash = %q, want %q", useCases.startInput.RequestHash, wantHash)
	}
}

func TestHandlerStartExplicitInterpretationKeepsLegacyRequestHash(t *testing.T) {
	useCases := &handlerUseCases{
		startResult: VoiceSession{ID: "vs_1", AccountID: "acct_1", Status: StatusActive},
	}
	handler := NewHandler(useCases, headerAccount)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/voice-sessions/vs_1/start",
		bytes.NewBufferString(`{"initial_mode":"interpretation"}`),
	)
	request.SetPathValue("id", "vs_1")
	request.Header.Set("X-Test-Account", "acct_1")
	request.Header.Set("Idempotency-Key", "start-key")
	response := httptest.NewRecorder()

	handler.start(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200; body %s", response.Code, response.Body.String())
	}
	wantHash := canonicalHash("voice-sessions.start", struct {
		SessionID string `json:"session_id"`
	}{SessionID: "vs_1"})
	if useCases.startInput.RequestHash != wantHash {
		t.Fatalf("RequestHash = %q, want legacy hash %q", useCases.startInput.RequestHash, wantHash)
	}
}

func TestHandlerStartRejectsInvalidBody(t *testing.T) {
	useCases := &handlerUseCases{}
	handler := NewHandler(useCases, headerAccount)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/voice-sessions/vs_1/start",
		bytes.NewBufferString(`{"initial_mode":"english_practice"}`),
	)
	request.SetPathValue("id", "vs_1")
	request.Header.Set("X-Test-Account", "acct_1")
	request.Header.Set("Idempotency-Key", "start-key")
	response := httptest.NewRecorder()

	handler.start(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("start with invalid body status = %d, want 400", response.Code)
	}
	if useCases.startCalls != 0 {
		t.Fatalf("Start calls = %d, want 0", useCases.startCalls)
	}
}

func TestHandlerRejectsOversizedBodies(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		body        []byte
		withTickets bool
		handle      func(*Handler, http.ResponseWriter, *http.Request)
	}{
		{
			name: "create valid JSON with trailing padding",
			path: "/api/v1/voice-sessions",
			body: oversizedHTTPBody(t, []byte(`{"capabilities":{}}`)),
			handle: func(handler *Handler, w http.ResponseWriter, r *http.Request) {
				handler.create(w, r)
			},
		},
		{
			name: "end valid JSON with trailing padding",
			path: "/api/v1/voice-sessions/vs_1/end",
			body: oversizedHTTPBody(t, []byte(`{"reason":"user_requested"}`)),
			handle: func(handler *Handler, w http.ResponseWriter, r *http.Request) {
				handler.end(w, r)
			},
		},
		{
			name: "start whitespace-only body",
			path: "/api/v1/voice-sessions/vs_1/start",
			body: oversizedHTTPBody(t, nil),
			handle: func(handler *Handler, w http.ResponseWriter, r *http.Request) {
				handler.start(w, r)
			},
		},
		{
			name:        "realtime ticket whitespace-only body",
			path:        "/api/v1/voice-sessions/vs_1/realtime-ticket",
			body:        oversizedHTTPBody(t, nil),
			withTickets: true,
			handle: func(handler *Handler, w http.ResponseWriter, r *http.Request) {
				handler.mintRealtimeTicket(w, r)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useCases := &handlerUseCases{}
			minter := &ticketMinterFake{}
			handler := NewHandler(useCases, headerAccount)
			if test.withTickets {
				handler.WithRealtimeTickets(minter)
			}
			request := httptest.NewRequest(http.MethodPost, test.path, bytes.NewReader(test.body))
			request.SetPathValue("id", "vs_1")
			request.Header.Set("X-Test-Account", "acct_1")
			request.Header.Set("Idempotency-Key", "test-key")
			response := httptest.NewRecorder()

			test.handle(handler, response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body %s", response.Code, response.Body.String())
			}
			if useCases.createCalls != 0 || useCases.startCalls != 0 || useCases.endCalls != 0 {
				t.Fatalf("use case calls = create:%d start:%d end:%d, want none", useCases.createCalls, useCases.startCalls, useCases.endCalls)
			}
			if minter.calls != 0 {
				t.Fatalf("MintRealtimeTicket calls = %d, want 0", minter.calls)
			}
		})
	}
}

func oversizedHTTPBody(t *testing.T, prefix []byte) []byte {
	t.Helper()
	padding := maxHTTPBodyBytes + 1 - len(prefix)
	return append(append([]byte(nil), prefix...), bytes.Repeat([]byte(" "), padding)...)
}

func TestHandlerEndDefaultsReasonAndCanonicalHash(t *testing.T) {
	useCases := &handlerUseCases{
		endResult: VoiceSession{ID: "vs_1", AccountID: "acct_1", Status: StatusEnded},
	}
	handler := NewHandler(useCases, headerAccount)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/voice-sessions/vs_1/end",
		http.NoBody,
	)
	request.SetPathValue("id", "vs_1")
	request.Header.Set("X-Test-Account", "acct_1")
	request.Header.Set("Idempotency-Key", "end-key")
	response := httptest.NewRecorder()

	handler.end(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("end status = %d, want 200; body %s", response.Code, response.Body.String())
	}
	if useCases.endInput.Reason != EndReasonUserRequested {
		t.Fatalf("Reason = %q, want default user_requested", useCases.endInput.Reason)
	}
	wantHash := canonicalHash("voice-sessions.end", struct {
		SessionID string    `json:"session_id"`
		Reason    EndReason `json:"reason"`
	}{SessionID: "vs_1", Reason: EndReasonUserRequested})
	if useCases.endInput.RequestHash != wantHash {
		t.Fatalf("RequestHash = %q, want %q", useCases.endInput.RequestHash, wantHash)
	}
}

func TestHandlerEndParsesExplicitReasonAndRejectsMalformedBody(t *testing.T) {
	useCases := &handlerUseCases{
		endResult: VoiceSession{ID: "vs_1", AccountID: "acct_1", Status: StatusEnded},
	}
	handler := NewHandler(useCases, headerAccount)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/voice-sessions/vs_1/end",
		bytes.NewBufferString(`{"reason":"operator_cancelled"}`),
	)
	request.SetPathValue("id", "vs_1")
	request.Header.Set("X-Test-Account", "acct_1")
	request.Header.Set("Idempotency-Key", "end-key")
	request.Header.Set("X-Request-ID", "req_end")
	response := httptest.NewRecorder()

	handler.end(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("end status = %d, want 200; body %s", response.Code, response.Body.String())
	}
	if useCases.endInput.Reason != EndReasonOperatorCancelled ||
		useCases.endInput.TraceID != "req_end" {
		t.Fatalf("EndInput = %#v", useCases.endInput)
	}
	wantHash := canonicalHash("voice-sessions.end", struct {
		SessionID string    `json:"session_id"`
		Reason    EndReason `json:"reason"`
	}{SessionID: "vs_1", Reason: EndReasonOperatorCancelled})
	if useCases.endInput.RequestHash != wantHash {
		t.Fatalf("RequestHash = %q, want %q", useCases.endInput.RequestHash, wantHash)
	}

	malformed := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/voice-sessions/vs_1/end",
		bytes.NewBufferString(`{"reason":"user_requested"}{"reason":"operator_cancelled"}`),
	)
	malformed.SetPathValue("id", "vs_1")
	malformed.Header.Set("X-Test-Account", "acct_1")
	malformed.Header.Set("Idempotency-Key", "end-key")
	response = httptest.NewRecorder()

	handler.end(response, malformed)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("malformed end status = %d, want 400", response.Code)
	}
	if useCases.endCalls != 1 {
		t.Fatalf("End calls = %d, want only the successful call", useCases.endCalls)
	}
}

func TestHandlerDetailAndStatePassAccountScopedInput(t *testing.T) {
	now := time.Date(2026, 7, 30, 9, 30, 0, 0, time.UTC)
	useCases := &handlerUseCases{
		detailResult: VoiceSessionDetail{
			VoiceSession: VoiceSession{ID: "vs_1", AccountID: "acct_1", Status: StatusActive, CreatedAt: now},
			RuntimeState: RuntimeListening, Retryable: false, RuntimeUpdatedAt: now,
		},
		stateResult: StateSnapshot{
			SessionID: "vs_1", Status: StatusActive,
			RuntimeState: RuntimeListening, RuntimeUpdatedAt: now,
		},
	}
	handler := NewHandler(useCases, headerAccount)

	detailRequest := httptest.NewRequest(http.MethodGet, "/api/v1/voice-sessions/vs_1", http.NoBody)
	detailRequest.SetPathValue("id", "vs_1")
	detailRequest.Header.Set("X-Test-Account", "acct_1")
	detailResponse := httptest.NewRecorder()

	handler.detail(detailResponse, detailRequest)

	if detailResponse.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200; body %s", detailResponse.Code, detailResponse.Body.String())
	}
	if useCases.detailInput != (DetailInput{AccountID: "acct_1", SessionID: "vs_1"}) {
		t.Fatalf("detail input = %#v", useCases.detailInput)
	}
	var detail VoiceSessionDetail
	if err := json.NewDecoder(detailResponse.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.ID != "vs_1" || detail.RuntimeState != RuntimeListening {
		t.Fatalf("detail = %#v", detail)
	}

	stateRequest := httptest.NewRequest(http.MethodGet, "/api/v1/voice-sessions/vs_1/state", http.NoBody)
	stateRequest.SetPathValue("id", "vs_1")
	stateRequest.Header.Set("X-Test-Account", "acct_1")
	stateResponse := httptest.NewRecorder()

	handler.state(stateResponse, stateRequest)

	if stateResponse.Code != http.StatusOK {
		t.Fatalf("state status = %d, want 200; body %s", stateResponse.Code, stateResponse.Body.String())
	}
	if useCases.stateInput != (DetailInput{AccountID: "acct_1", SessionID: "vs_1"}) {
		t.Fatalf("state input = %#v", useCases.stateInput)
	}
	var state StateSnapshot
	if err := json.NewDecoder(stateResponse.Body).Decode(&state); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	if state.SessionID != "vs_1" || state.RuntimeState != RuntimeListening {
		t.Fatalf("state = %#v", state)
	}
}

func TestHandlerListParsesPersistentFiltersOnly(t *testing.T) {
	next := "cursor_2"
	useCases := &handlerUseCases{
		listResult: ListPage{
			Sessions:   []VoiceSessionListItem{{ID: "vs_1", AccountID: "acct_1", Status: StatusEnded}},
			NextCursor: &next,
		},
	}
	handler := NewHandler(useCases, headerAccount)
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/voice-sessions?status=ended&cursor=cursor_1&limit=2",
		http.NoBody,
	)
	request.Header.Set("X-Test-Account", "acct_1")
	response := httptest.NewRecorder()

	handler.list(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body %s", response.Code, response.Body.String())
	}
	if useCases.listInput.AccountID != "acct_1" ||
		useCases.listInput.Cursor != "cursor_1" ||
		useCases.listInput.Limit != 2 ||
		useCases.listInput.Status == nil ||
		*useCases.listInput.Status != StatusEnded {
		t.Fatalf("ListInput = %#v", useCases.listInput)
	}
	var page ListPage
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if page.NextCursor == nil || *page.NextCursor != next {
		t.Fatalf("NextCursor = %v, want %q", page.NextCursor, next)
	}
}

func TestHandlerListRejectsInvalidLimitBeforeUseCase(t *testing.T) {
	useCases := &handlerUseCases{}
	handler := NewHandler(useCases, headerAccount)
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/voice-sessions?limit=101",
		http.NoBody,
	)
	request.Header.Set("X-Test-Account", "acct_1")
	response := httptest.NewRecorder()

	handler.list(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("list invalid limit status = %d, want 400", response.Code)
	}
	if useCases.listCalls != 0 {
		t.Fatalf("List calls = %d, want 0", useCases.listCalls)
	}
}

func TestHandlerRegisterRoutesAndRequiresMiddleware(t *testing.T) {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	useCases := &handlerUseCases{
		createResult: VoiceSession{ID: "vs_created", AccountID: "acct_1", Status: StatusCreated, CreatedAt: now},
		startResult:  VoiceSession{ID: "vs_1", AccountID: "acct_1", Status: StatusActive, CreatedAt: now},
		endResult:    VoiceSession{ID: "vs_1", AccountID: "acct_1", Status: StatusEnded, CreatedAt: now},
		detailResult: VoiceSessionDetail{
			VoiceSession: VoiceSession{ID: "vs_1", AccountID: "acct_1", Status: StatusActive, CreatedAt: now},
			RuntimeState: RuntimeListening, RuntimeUpdatedAt: now,
		},
		stateResult: StateSnapshot{
			SessionID: "vs_1", Status: StatusActive,
			RuntimeState: RuntimeListening, RuntimeUpdatedAt: now,
		},
		listResult: ListPage{Sessions: []VoiceSessionListItem{{
			ID: "vs_1", AccountID: "acct_1", Status: StatusActive, CreatedAt: now,
		}}},
	}
	handler := NewHandler(useCases, headerAccount)
	mux := http.NewServeMux()
	authorized := 0
	handler.Register(mux, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authorized++
			next.ServeHTTP(w, r)
		})
	})

	requests := []struct {
		method string
		path   string
		body   string
		status int
		key    string
	}{
		{method: http.MethodPost, path: "/api/v1/voice-sessions", body: `{"capabilities":{"webrtc":true,"data_channel":true,"microphone":true,"speaker":true,"speaker_diarization":true}}`, status: http.StatusCreated, key: "create-key"},
		{method: http.MethodPost, path: "/api/v1/voice-sessions/vs_1/start", status: http.StatusOK, key: "start-key"},
		{method: http.MethodPost, path: "/api/v1/voice-sessions/vs_1/end", status: http.StatusOK, key: "end-key"},
		{method: http.MethodGet, path: "/api/v1/voice-sessions/vs_1", status: http.StatusOK},
		{method: http.MethodGet, path: "/api/v1/voice-sessions/vs_1/state", status: http.StatusOK},
		{method: http.MethodGet, path: "/api/v1/voice-sessions", status: http.StatusOK},
	}
	for _, test := range requests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, bytes.NewBufferString(test.body))
			request.Header.Set("X-Test-Account", "acct_1")
			if test.key != "" {
				request.Header.Set("Idempotency-Key", test.key)
			}
			response := httptest.NewRecorder()

			mux.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf("status = %d, want %d; body %s", response.Code, test.status, response.Body.String())
			}
		})
	}
	if authorized != len(requests) {
		t.Fatalf("authorized calls = %d, want %d", authorized, len(requests))
	}

	defer func() {
		if recover() == nil {
			t.Fatal("Register without middleware should panic")
		}
	}()
	NewHandler(useCases, headerAccount).Register(http.NewServeMux(), nil)
}

func TestHandlerRejectsUnauthenticatedRequestsBeforeUseCases(t *testing.T) {
	useCases := &handlerUseCases{}
	handler := NewHandler(useCases, headerAccount)
	requests := []struct {
		name   string
		handle func(http.ResponseWriter, *http.Request)
		method string
		path   string
		body   string
	}{
		{name: "create", handle: handler.create, method: http.MethodPost, path: "/api/v1/voice-sessions", body: `{}`},
		{name: "start", handle: handler.start, method: http.MethodPost, path: "/api/v1/voice-sessions/vs_1/start"},
		{name: "end", handle: handler.end, method: http.MethodPost, path: "/api/v1/voice-sessions/vs_1/end"},
		{name: "detail", handle: handler.detail, method: http.MethodGet, path: "/api/v1/voice-sessions/vs_1"},
		{name: "state", handle: handler.state, method: http.MethodGet, path: "/api/v1/voice-sessions/vs_1/state"},
		{name: "list", handle: handler.list, method: http.MethodGet, path: "/api/v1/voice-sessions"},
	}
	for _, test := range requests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, bytes.NewBufferString(test.body))
			request.SetPathValue("id", "vs_1")
			response := httptest.NewRecorder()

			test.handle(response, request)

			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", response.Code)
			}
		})
	}
	if useCases.anyCalls() {
		t.Fatalf("use cases must not run for unauthenticated requests: %#v", useCases)
	}
}

func TestHandlerReturnsNotImplementedWhenServiceIsMissing(t *testing.T) {
	handler := NewHandler(nil, headerAccount)
	requests := []struct {
		name   string
		handle func(http.ResponseWriter, *http.Request)
		method string
		path   string
		body   string
	}{
		{name: "create", handle: handler.create, method: http.MethodPost, path: "/api/v1/voice-sessions", body: `{}`},
		{name: "start", handle: handler.start, method: http.MethodPost, path: "/api/v1/voice-sessions/vs_1/start"},
		{name: "end", handle: handler.end, method: http.MethodPost, path: "/api/v1/voice-sessions/vs_1/end"},
		{name: "detail", handle: handler.detail, method: http.MethodGet, path: "/api/v1/voice-sessions/vs_1"},
		{name: "state", handle: handler.state, method: http.MethodGet, path: "/api/v1/voice-sessions/vs_1/state"},
		{name: "list", handle: handler.list, method: http.MethodGet, path: "/api/v1/voice-sessions"},
	}
	for _, test := range requests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, bytes.NewBufferString(test.body))
			request.SetPathValue("id", "vs_1")
			request.Header.Set("X-Test-Account", "acct_1")
			response := httptest.NewRecorder()

			test.handle(response, request)

			if response.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501; body %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestHandlerMapsSessionErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   ErrorCode
	}{
		{name: "not found", err: ErrVoiceSessionNotFound, wantStatus: http.StatusNotFound, wantCode: CodeVoiceSessionNotFound},
		{name: "idempotency conflict", err: ErrIdempotencyKeyConflict, wantStatus: http.StatusConflict, wantCode: CodeIdempotencyKeyConflict},
		{name: "state conflict", err: ErrSessionStateConflict, wantStatus: http.StatusConflict, wantCode: CodeSessionStateConflict},
		{name: "start in progress", err: ErrSessionStartInProgress, wantStatus: http.StatusConflict, wantCode: CodeSessionStartInProgress},
		{name: "language not ready", err: ErrLanguageConfigNotReady, wantStatus: http.StatusConflict, wantCode: CodeLanguageConfigNotReady},
		{name: "webrtc not ready", err: ErrWebRTCNotReady, wantStatus: http.StatusConflict, wantCode: CodeWebRTCNotReady},
		{name: "already running", err: ErrRealtimeAlreadyRunning, wantStatus: http.StatusConflict, wantCode: CodeRealtimeAlreadyRunning},
		{name: "unsupported audio", err: ErrUnsupportedAudio, wantStatus: http.StatusUnprocessableEntity, wantCode: CodeUnsupportedAudio},
		{name: "start failed", err: ErrRealtimeStartFailed, wantStatus: http.StatusServiceUnavailable, wantCode: CodeRealtimeStartFailed},
		{name: "runtime unavailable", err: ErrRuntimeUnavailable, wantStatus: http.StatusServiceUnavailable, wantCode: CodeRuntimeUnavailable},
		{name: "webrtc unavailable", err: ErrWebRTCUnavailable, wantStatus: http.StatusServiceUnavailable, wantCode: CodeWebRTCUnavailable},
		{name: "mode unavailable", err: ErrModeUnavailable, wantStatus: http.StatusServiceUnavailable, wantCode: CodeModeUnavailable},
		{name: "mode generation conflict", err: ErrModeGenerationConflict, wantStatus: http.StatusConflict, wantCode: CodeModeGenerationConflict},
		{name: "mode runtime mismatch", err: ErrModeRuntimeMismatch, wantStatus: http.StatusConflict, wantCode: CodeModeRuntimeMismatch},
		{name: "mode operation conflict", err: ErrModeOperationConflict, wantStatus: http.StatusConflict, wantCode: CodeModeOperationConflict},
		{name: "mode not available", err: ErrModeNotAvailable, wantStatus: http.StatusUnprocessableEntity, wantCode: CodeModeNotAvailable},
		{name: "not implemented", err: ErrNotImplemented, wantStatus: http.StatusNotImplemented, wantCode: CodeNotImplemented},
		{name: "unknown", err: errors.New("boom"), wantStatus: http.StatusInternalServerError, wantCode: ErrorCode("internal_error")},
		{name: "wrapped stop failed", err: errors.Join(ErrRealtimeStopFailed, errDependency), wantStatus: http.StatusServiceUnavailable, wantCode: CodeRealtimeStopFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			useCases := &handlerUseCases{detailErr: test.err}
			handler := NewHandler(useCases, headerAccount)
			request := httptest.NewRequest(http.MethodGet, "/api/v1/voice-sessions/vs_1", http.NoBody)
			request.SetPathValue("id", "vs_1")
			request.Header.Set("X-Test-Account", "acct_1")
			response := httptest.NewRecorder()

			handler.detail(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			var body httpErrorEnvelope
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body.Error.Code != string(test.wantCode) {
				t.Fatalf("error code = %q, want %q", body.Error.Code, test.wantCode)
			}
		})
	}
}

func TestHandlerModeRoutesForwardTrustedControlMetadata(t *testing.T) {
	state := ModeSnapshot{
		SessionID: "vs_1", RuntimeInstanceID: "runtime-1", ActiveMode: ModeInterpretation,
		Generation: 1, Phase: "active", UpdatedAt: time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC),
	}
	modes := &handlerModeUseCases{state: state, result: ModeSwitchResult{
		OperationID: "mode-op-1", Status: "applied", State: state,
	}}
	handler := NewHandler(&handlerUseCases{}, headerAccount).WithRealtimeModes(modes)

	get := httptest.NewRequest(http.MethodGet, "/api/v1/voice-sessions/vs_1/mode", http.NoBody)
	get.SetPathValue("id", "vs_1")
	get.Header.Set("X-Test-Account", "acct_1")
	getResponse := httptest.NewRecorder()
	handler.modeState(getResponse, get)
	if getResponse.Code != http.StatusOK || modes.getInput != (DetailInput{AccountID: "acct_1", SessionID: "vs_1"}) {
		t.Fatalf("GET status=%d input=%#v body=%s", getResponse.Code, modes.getInput, getResponse.Body.String())
	}

	post := httptest.NewRequest(http.MethodPost, "/api/v1/voice-sessions/vs_1/mode", bytes.NewBufferString(
		`{"runtime_instance_id":"runtime-1","expected_generation":1,"target_mode":"assistant"}`,
	))
	post.SetPathValue("id", "vs_1")
	post.Header.Set("X-Test-Account", "acct_1")
	post.Header.Set("Idempotency-Key", "mode-op-1")
	post.Header.Set("X-Request-ID", "trace-header-1")
	postResponse := httptest.NewRecorder()
	handler.switchMode(postResponse, post)
	if postResponse.Code != http.StatusOK {
		t.Fatalf("POST status=%d body=%s", postResponse.Code, postResponse.Body.String())
	}
	want := SwitchModeInput{
		AccountID: "acct_1", SessionID: "vs_1", RuntimeInstanceID: "runtime-1",
		OperationID: "mode-op-1", TraceID: modeOperationTraceID("vs_1", "mode-op-1"), ExpectedGeneration: 1,
		TargetMode: ModeAssistant,
	}
	if !reflect.DeepEqual(modes.switchInput, want) {
		t.Fatalf("SwitchMode input = %#v, want %#v", modes.switchInput, want)
	}
	if modes.switchInput.TraceID == post.Header.Get("X-Request-ID") {
		t.Fatalf("command trace ID = HTTP request ID %q, want independent identities", modes.switchInput.TraceID)
	}
}

func TestHandlerModeRouteKeepsOperationTraceStableAcrossRequestIDs(t *testing.T) {
	modes := &handlerModeUseCases{switchErr: ErrModeUnavailable}
	handler := NewHandler(&handlerUseCases{}, headerAccount).WithRealtimeModes(modes)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/voice-sessions/vs_1/mode", bytes.NewBufferString(
		`{"runtime_instance_id":"runtime-1","expected_generation":1,"target_mode":"assistant"}`,
	))
	request.SetPathValue("id", "vs_1")
	request.Header.Set("X-Test-Account", "acct_1")
	request.Header.Set("Idempotency-Key", "mode-op-1")
	request.Header.Set("X-Request-ID", "http-attempt-1")
	response := httptest.NewRecorder()

	handler.switchMode(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body %s", response.Code, response.Body.String())
	}
	if modes.switchInput.TraceID == "" || modes.switchInput.TraceID == request.Header.Get("X-Request-ID") {
		t.Fatalf("SwitchMode trace ID = %q, want stable identity independent of HTTP request ID", modes.switchInput.TraceID)
	}
	var body httpErrorEnvelope
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if body.Error.RequestID != "http-attempt-1" ||
		request.Header.Get("X-Request-ID") != "http-attempt-1" {
		t.Fatalf(
			"HTTP request IDs = response %q header %q, want per-attempt value; command trace %q",
			body.Error.RequestID,
			request.Header.Get("X-Request-ID"),
			modes.switchInput.TraceID,
		)
	}

	retryModes := &handlerModeUseCases{switchErr: ErrModeUnavailable}
	retryHandler := NewHandler(&handlerUseCases{}, headerAccount).WithRealtimeModes(retryModes)
	retry := httptest.NewRequest(http.MethodPost, "/api/v1/voice-sessions/vs_1/mode", bytes.NewBufferString(
		`{"runtime_instance_id":"runtime-1","expected_generation":1,"target_mode":"assistant"}`,
	))
	retry.SetPathValue("id", "vs_1")
	retry.Header.Set("X-Test-Account", "acct_1")
	retry.Header.Set("Idempotency-Key", "mode-op-1")
	retry.Header.Set("X-Request-ID", "http-attempt-2")
	retryResponse := httptest.NewRecorder()
	retryHandler.switchMode(retryResponse, retry)
	if retryModes.switchInput.TraceID != modes.switchInput.TraceID {
		t.Fatalf(
			"retry trace ID = %q, want stable operation trace %q",
			retryModes.switchInput.TraceID,
			modes.switchInput.TraceID,
		)
	}
	var retryBody httpErrorEnvelope
	if err := json.NewDecoder(retryResponse.Body).Decode(&retryBody); err != nil {
		t.Fatalf("decode retry error body: %v", err)
	}
	if retryBody.Error.RequestID != "http-attempt-2" {
		t.Fatalf("retry response request ID = %q, want per-attempt HTTP identity", retryBody.Error.RequestID)
	}
}

func TestHandlerModeRoutesRejectUnauthenticatedAndMalformedRequests(t *testing.T) {
	modes := &handlerModeUseCases{}
	handler := NewHandler(&handlerUseCases{}, headerAccount).WithRealtimeModes(modes)

	unauthenticated := httptest.NewRequest(http.MethodGet, "/api/v1/voice-sessions/vs_1/mode", http.NoBody)
	unauthenticated.SetPathValue("id", "vs_1")
	response := httptest.NewRecorder()
	handler.modeState(response, unauthenticated)
	if response.Code != http.StatusUnauthorized || modes.getCalls != 0 {
		t.Fatalf("unauthenticated status=%d getCalls=%d", response.Code, modes.getCalls)
	}

	malformed := httptest.NewRequest(http.MethodPost, "/api/v1/voice-sessions/vs_1/mode", bytes.NewBufferString(
		`{"runtime_instance_id":"runtime-1","expected_generation":1,"target_mode":"english_practice"}`,
	))
	malformed.SetPathValue("id", "vs_1")
	malformed.Header.Set("X-Test-Account", "acct_1")
	malformed.Header.Set("Idempotency-Key", "mode-op-1")
	response = httptest.NewRecorder()
	handler.switchMode(response, malformed)
	if response.Code != http.StatusBadRequest || modes.switchCalls != 0 {
		t.Fatalf("malformed status=%d switchCalls=%d", response.Code, modes.switchCalls)
	}

	oversizedTrace := httptest.NewRequest(http.MethodPost, "/api/v1/voice-sessions/vs_1/mode", bytes.NewBufferString(
		`{"runtime_instance_id":"runtime-1","expected_generation":1,"target_mode":"assistant"}`,
	))
	oversizedTrace.SetPathValue("id", "vs_1")
	oversizedTrace.Header.Set("X-Test-Account", "acct_1")
	oversizedTrace.Header.Set("Idempotency-Key", "mode-op-1")
	oversizedTrace.Header.Set("X-Request-ID", strings.Repeat("t", maxRequestIDLength+1))
	response = httptest.NewRecorder()
	handler.switchMode(response, oversizedTrace)
	if response.Code != http.StatusBadRequest || modes.switchCalls != 0 {
		t.Fatalf("oversized trace status=%d switchCalls=%d", response.Code, modes.switchCalls)
	}
	var body httpErrorEnvelope
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode oversized trace error: %v", err)
	}
	if body.Error.RequestID == "" || len(body.Error.RequestID) > maxRequestIDLength {
		t.Fatalf("error request ID = %q, want bounded generated value", body.Error.RequestID)
	}
}

func TestHandlerRegisterMountsModeRoutesOnlyWhenConfigured(t *testing.T) {
	withModes := NewHandler(&handlerUseCases{}, headerAccount).WithRealtimeModes(&handlerModeUseCases{
		state: ModeSnapshot{SessionID: "vs_1", RuntimeInstanceID: "runtime-1", ActiveMode: ModeAssistant, Generation: 1, Phase: "active", UpdatedAt: time.Now()},
	})
	mux := http.NewServeMux()
	withModes.Register(mux, func(next http.Handler) http.Handler { return next })
	request := httptest.NewRequest(http.MethodGet, "/api/v1/voice-sessions/vs_1/mode", http.NoBody)
	request.Header.Set("X-Test-Account", "acct_1")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("configured mode route status=%d body=%s", response.Code, response.Body.String())
	}

	withoutModes := NewHandler(&handlerUseCases{}, headerAccount)
	mux = http.NewServeMux()
	withoutModes.Register(mux, func(next http.Handler) http.Handler { return next })
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unconfigured mode route status=%d, want 404", response.Code)
	}
}

func TestHandlerMintsRealtimeTicketForOwner(t *testing.T) {
	expires := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	minter := &ticketMinterFake{ticket: RealtimeTicket{
		Ticket: "v1.ticket", SessionID: "vs_1", ExpiresAt: expires,
	}}
	handler := NewHandler(&handlerUseCases{}, headerAccount).WithRealtimeTickets(minter)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/voice-sessions/vs_1/realtime-ticket", http.NoBody)
	request.SetPathValue("id", "vs_1")
	request.Header.Set("X-Test-Account", "acct_1")
	response := httptest.NewRecorder()

	handler.mintRealtimeTicket(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", response.Code, response.Body.String())
	}
	if minter.accountID != "acct_1" || minter.sessionID != "vs_1" {
		t.Fatalf("mint args = %s/%s", minter.accountID, minter.sessionID)
	}
	var body RealtimeTicket
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Ticket != "v1.ticket" || body.SessionID != "vs_1" {
		t.Fatalf("body = %#v", body)
	}
}

func TestHandlerMintRealtimeTicketRequiresAuthAndEmptyBody(t *testing.T) {
	handler := NewHandler(&handlerUseCases{}, headerAccount).WithRealtimeTickets(&ticketMinterFake{
		ticket: RealtimeTicket{Ticket: "t", SessionID: "vs_1"},
	})
	unauth := httptest.NewRequest(http.MethodPost, "/api/v1/voice-sessions/vs_1/realtime-ticket", http.NoBody)
	unauth.SetPathValue("id", "vs_1")
	unauthRes := httptest.NewRecorder()
	handler.mintRealtimeTicket(unauthRes, unauth)
	if unauthRes.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status = %d, want 401", unauthRes.Code)
	}

	withBody := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/voice-sessions/vs_1/realtime-ticket",
		bytes.NewBufferString(`{"nope":true}`),
	)
	withBody.SetPathValue("id", "vs_1")
	withBody.Header.Set("X-Test-Account", "acct_1")
	bodyRes := httptest.NewRecorder()
	handler.mintRealtimeTicket(bodyRes, withBody)
	if bodyRes.Code != http.StatusBadRequest {
		t.Fatalf("body status = %d, want 400", bodyRes.Code)
	}
}

func TestHandlerMintRealtimeTicketNotImplementedWithoutMinter(t *testing.T) {
	handler := NewHandler(&handlerUseCases{}, headerAccount)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/voice-sessions/vs_1/realtime-ticket", http.NoBody)
	request.SetPathValue("id", "vs_1")
	request.Header.Set("X-Test-Account", "acct_1")
	response := httptest.NewRecorder()
	handler.mintRealtimeTicket(response, request)
	if response.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", response.Code)
	}
}

func TestHandlerMintRealtimeTicketMapsMinterErrors(t *testing.T) {
	handler := NewHandler(&handlerUseCases{}, headerAccount).WithRealtimeTickets(&ticketMinterFake{
		err: ErrVoiceSessionNotFound,
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/voice-sessions/vs_1/realtime-ticket", http.NoBody)
	request.SetPathValue("id", "vs_1")
	request.Header.Set("X-Test-Account", "acct_1")
	response := httptest.NewRecorder()
	handler.mintRealtimeTicket(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}

func TestHandlerRegisterMountsRealtimeTicketRoute(t *testing.T) {
	minter := &ticketMinterFake{ticket: RealtimeTicket{Ticket: "t", SessionID: "vs_1"}}
	handler := NewHandler(&handlerUseCases{}, headerAccount).WithRealtimeTickets(minter)
	mux := http.NewServeMux()
	handler.Register(mux, func(next http.Handler) http.Handler { return next })
	request := httptest.NewRequest(http.MethodPost, "/api/v1/voice-sessions/vs_1/realtime-ticket", http.NoBody)
	request.Header.Set("X-Test-Account", "acct_1")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("registered route status = %d body=%s", response.Code, response.Body.String())
	}
}

func headerAccount(r *http.Request) (string, bool) {
	accountID := r.Header.Get("X-Test-Account")
	return accountID, accountID != ""
}

type ticketMinterFake struct {
	calls     int
	accountID string
	sessionID string
	ticket    RealtimeTicket
	err       error
}

func (m *ticketMinterFake) MintRealtimeTicket(_ context.Context, accountID, sessionID string) (RealtimeTicket, error) {
	m.calls++
	m.accountID = accountID
	m.sessionID = sessionID
	return m.ticket, m.err
}

type handlerUseCases struct {
	createCalls  int
	createInput  CreateInput
	createResult VoiceSession
	createErr    error

	startCalls  int
	startInput  StartInput
	startResult VoiceSession
	startErr    error

	endCalls  int
	endInput  EndInput
	endResult VoiceSession
	endErr    error

	detailInput  DetailInput
	detailResult VoiceSessionDetail
	detailErr    error

	stateInput  DetailInput
	stateResult StateSnapshot
	stateErr    error

	listInput  ListInput
	listResult ListPage
	listErr    error
	listCalls  int
}

type handlerModeUseCases struct {
	state       ModeSnapshot
	result      ModeSwitchResult
	getErr      error
	switchErr   error
	getInput    DetailInput
	switchInput SwitchModeInput
	getCalls    int
	switchCalls int
}

func (m *handlerModeUseCases) GetMode(_ context.Context, input DetailInput) (ModeSnapshot, error) {
	m.getCalls++
	m.getInput = input
	return m.state, m.getErr
}

func (m *handlerModeUseCases) SwitchMode(_ context.Context, input SwitchModeInput) (ModeSwitchResult, error) {
	m.switchCalls++
	m.switchInput = input
	return m.result, m.switchErr
}

func (h *handlerUseCases) Create(_ context.Context, input CreateInput) (VoiceSession, error) {
	h.createCalls++
	h.createInput = input
	return h.createResult, h.createErr
}

func (h *handlerUseCases) Start(_ context.Context, input StartInput) (VoiceSession, error) {
	h.startCalls++
	h.startInput = input
	return h.startResult, h.startErr
}

func (h *handlerUseCases) End(_ context.Context, input EndInput) (VoiceSession, error) {
	h.endCalls++
	h.endInput = input
	return h.endResult, h.endErr
}

func (h *handlerUseCases) GetDetail(_ context.Context, input DetailInput) (VoiceSessionDetail, error) {
	h.detailInput = input
	return h.detailResult, h.detailErr
}

func (h *handlerUseCases) GetState(_ context.Context, input DetailInput) (StateSnapshot, error) {
	h.stateInput = input
	return h.stateResult, h.stateErr
}

func (h *handlerUseCases) List(_ context.Context, input ListInput) (ListPage, error) {
	h.listCalls++
	h.listInput = input
	return h.listResult, h.listErr
}

func (h *handlerUseCases) anyCalls() bool {
	return h.createCalls > 0 ||
		h.startCalls > 0 ||
		h.endCalls > 0 ||
		h.detailInput != (DetailInput{}) ||
		h.stateInput != (DetailInput{}) ||
		h.listCalls > 0
}
