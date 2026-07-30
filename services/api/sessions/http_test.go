package sessions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
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

func TestHandlerStartRequiresEmptyBodyAndAddsTrace(t *testing.T) {
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
		useCases.startInput.StartedBy != "acct_1" {
		t.Fatalf("StartInput = %#v", useCases.startInput)
	}
	wantHash := canonicalHash("voice-sessions.start", struct {
		SessionID string `json:"session_id"`
	}{SessionID: "vs_1"})
	if useCases.startInput.RequestHash != wantHash {
		t.Fatalf("RequestHash = %q, want %q", useCases.startInput.RequestHash, wantHash)
	}
}

func TestHandlerStartRejectsRequestBody(t *testing.T) {
	useCases := &handlerUseCases{}
	handler := NewHandler(useCases, headerAccount)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/voice-sessions/vs_1/start",
		bytes.NewBufferString(`{}`),
	)
	request.SetPathValue("id", "vs_1")
	request.Header.Set("X-Test-Account", "acct_1")
	request.Header.Set("Idempotency-Key", "start-key")
	response := httptest.NewRecorder()

	handler.start(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("start with body status = %d, want 400", response.Code)
	}
	if useCases.startCalls != 0 {
		t.Fatalf("Start calls = %d, want 0", useCases.startCalls)
	}
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

func TestHandlerMapsSessionErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   ErrorCode
	}{
		{name: "not found", err: ErrVoiceSessionNotFound, wantStatus: http.StatusNotFound, wantCode: CodeVoiceSessionNotFound},
		{name: "idempotency conflict", err: ErrIdempotencyKeyConflict, wantStatus: http.StatusConflict, wantCode: CodeIdempotencyKeyConflict},
		{name: "runtime unavailable", err: ErrRuntimeUnavailable, wantStatus: http.StatusServiceUnavailable, wantCode: CodeRuntimeUnavailable},
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

func headerAccount(r *http.Request) (string, bool) {
	accountID := r.Header.Get("X-Test-Account")
	return accountID, accountID != ""
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
	h.listInput = input
	return h.listResult, h.listErr
}
