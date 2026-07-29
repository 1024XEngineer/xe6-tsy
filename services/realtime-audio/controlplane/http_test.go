package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
)

func TestHandlerStartStopDelegatesAndReplaysIdempotently(t *testing.T) {
	fixture := newFixture(t)
	startBody := `{"trace_id":"trace-start","started_by":"browser"}`

	first := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/start", startBody, "start-key")
	if first.Code != http.StatusOK {
		t.Fatalf("first start status = %d, body=%s", first.Code, first.Body.String())
	}
	second := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/start", startBody, "start-key")
	if second.Code != http.StatusOK {
		t.Fatalf("replayed start status = %d, body=%s", second.Code, second.Body.String())
	}
	if fixture.lifecycle.starts != 1 {
		t.Fatalf("lifecycle starts = %d, want 1", fixture.lifecycle.starts)
	}

	stopBody := `{"trace_id":"trace-stop","reason":"user_requested"}`
	firstStop := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/stop", stopBody, "stop-key")
	if firstStop.Code != http.StatusOK {
		t.Fatalf("first stop status = %d, body=%s", firstStop.Code, firstStop.Body.String())
	}
	secondStop := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/stop", stopBody, "stop-key")
	if secondStop.Code != http.StatusOK {
		t.Fatalf("replayed stop status = %d, body=%s", secondStop.Code, secondStop.Body.String())
	}
	if fixture.lifecycle.stops != 1 {
		t.Fatalf("lifecycle stops = %d, want 1", fixture.lifecycle.stops)
	}
}

func TestHandlerDelegatesOfferCandidatesRuntimeAndConfig(t *testing.T) {
	fixture := newFixture(t)

	runtime := fixture.request(http.MethodGet, "/realtime/v1/sessions/session-1/runtime", "", "")
	if runtime.Code != http.StatusOK {
		t.Fatalf("runtime status = %d, body=%s", runtime.Code, runtime.Body.String())
	}
	config := fixture.request(http.MethodGet, "/realtime/v1/sessions/session-1/webrtc/config", "", "")
	if config.Code != http.StatusOK {
		t.Fatalf("config status = %d, body=%s", config.Code, config.Body.String())
	}
	if !strings.Contains(config.Body.String(), `"session_id":"session-1"`) {
		t.Fatalf("config body = %s", config.Body.String())
	}

	offer := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/webrtc/offer", `{"sdp":"offer-sdp","type":"offer"}`, "offer-key")
	if offer.Code != http.StatusOK {
		t.Fatalf("offer status = %d, body=%s", offer.Code, offer.Body.String())
	}
	var offerResponse webrtc.OfferResponse
	if err := json.Unmarshal(offer.Body.Bytes(), &offerResponse); err != nil {
		t.Fatalf("decode offer response: %v", err)
	}
	if offerResponse.ConnectionID != "connection-1" {
		t.Fatalf("connection id = %q, want connection-1", offerResponse.ConnectionID)
	}

	candidates := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/ice-candidates", `{"connection_id":"connection-1","candidates":[{"candidate_id":"candidate-1","candidate":"candidate:1"}],"end_of_candidates":true}`, "")
	if candidates.Code != http.StatusOK {
		t.Fatalf("candidates status = %d, body=%s", candidates.Code, candidates.Body.String())
	}
	if fixture.signaling.offerCalls != 1 || fixture.signaling.candidateCalls != 1 {
		t.Fatalf("signaling calls = offer %d, candidates %d", fixture.signaling.offerCalls, fixture.signaling.candidateCalls)
	}
}

func TestHandlerRejectsMalformedAndMissingIdentity(t *testing.T) {
	fixture := newFixture(t)

	missingTicket := httptest.NewRecorder()
	fixture.handler.ServeHTTP(missingTicket, httptest.NewRequest(http.MethodGet, "/realtime/v1/sessions/session-1/runtime", nil))
	if missingTicket.Code != http.StatusUnauthorized {
		t.Fatalf("missing ticket status = %d, want 401", missingTicket.Code)
	}

	malformed := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/start", `{"trace_id":`, "start-key")
	if malformed.Code != http.StatusBadRequest {
		t.Fatalf("malformed status = %d, want 400", malformed.Code)
	}

	unknown := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/start", `{"trace_id":"x","unknown":true}`, "start-key")
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, want 400", unknown.Code)
	}

	missingKey := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/webrtc/offer", `{"sdp":"offer-sdp","type":"offer"}`, "")
	if missingKey.Code != http.StatusBadRequest {
		t.Fatalf("missing idempotency status = %d, want 400", missingKey.Code)
	}
}

func TestHandlerMapsLifecycleAndTicketErrors(t *testing.T) {
	fixture := newFixture(t)
	fixture.lifecycle.startErr = session.ErrRuntimeCleanupRequired
	conflict := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/start", `{}`, "start-key")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("lifecycle conflict status = %d, body=%s", conflict.Code, conflict.Body.String())
	}

	fixture.tickets.err = webrtc.ErrTicketExpired
	expired := fixture.request(http.MethodGet, "/realtime/v1/sessions/session-1/runtime", "", "")
	if expired.Code != http.StatusUnauthorized {
		t.Fatalf("expired ticket status = %d, want 401", expired.Code)
	}
}

func TestHandlerReservesReplayBeforeRunningLifecycle(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	lifecycle := &blockingLifecycleFake{
		runtime: session.RuntimeSnapshot{SessionID: "session-1", RuntimeState: session.RuntimeListening, UpdatedAt: now},
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	handler := newReplayHandler(t, lifecycle, func() time.Time { return now }, time.Minute, 8)
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		firstDone <- replayRequest(handler, `{"trace_id":"trace-start","started_by":"browser"}`, "start-key")
	}()
	<-lifecycle.entered

	conflict := replayRequest(handler, `{"trace_id":"different","started_by":"browser"}`, "start-key")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("in-flight payload conflict status = %d, body=%s", conflict.Code, conflict.Body.String())
	}
	close(lifecycle.release)
	if first := <-firstDone; first.Code != http.StatusOK {
		t.Fatalf("first start status = %d, body=%s", first.Code, first.Body.String())
	}
	if got := lifecycle.starts.Load(); got != 1 {
		t.Fatalf("lifecycle starts = %d, want 1", got)
	}
}

func TestHandlerExpiresReplayRecords(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	lifecycle := &lifecycleFake{runtime: session.RuntimeSnapshot{SessionID: "session-1", RuntimeState: session.RuntimeListening, UpdatedAt: now}}
	handler := newReplayHandler(t, lifecycle, func() time.Time { return now }, time.Minute, 8)
	body := `{"trace_id":"trace-start","started_by":"browser"}`
	if response := replayRequest(handler, body, "start-key"); response.Code != http.StatusOK {
		t.Fatalf("first start status = %d, body=%s", response.Code, response.Body.String())
	}
	now = now.Add(time.Minute + time.Second)
	if response := replayRequest(handler, body, "start-key"); response.Code != http.StatusOK {
		t.Fatalf("expired replay status = %d, body=%s", response.Code, response.Body.String())
	}
	if lifecycle.starts != 2 {
		t.Fatalf("lifecycle starts after expiry = %d, want 2", lifecycle.starts)
	}
}

func TestHandlerRejectsReplayWhenCapacityIsExhausted(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	lifecycle := &lifecycleFake{runtime: session.RuntimeSnapshot{SessionID: "session-1", RuntimeState: session.RuntimeListening, UpdatedAt: now}}
	handler := newReplayHandler(t, lifecycle, func() time.Time { return now }, time.Minute, 1)
	body := `{"trace_id":"trace-start","started_by":"browser"}`
	if response := replayRequest(handler, body, "start-key-1"); response.Code != http.StatusOK {
		t.Fatalf("first start status = %d, body=%s", response.Code, response.Body.String())
	}
	full := replayRequest(handler, body, "start-key-2")
	if full.Code != http.StatusServiceUnavailable {
		t.Fatalf("full replay status = %d, body=%s", full.Code, full.Body.String())
	}
	now = now.Add(time.Minute + time.Second)
	if response := replayRequest(handler, body, "start-key-2"); response.Code != http.StatusOK {
		t.Fatalf("recovered replay status = %d, body=%s", response.Code, response.Body.String())
	}
}

func TestHandlerIsolatesReplayCapacityPerSession(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	lifecycle := &multiSessionLifecycleFake{runtime: session.RuntimeSnapshot{RuntimeState: session.RuntimeListening, UpdatedAt: now}}
	handler := newReplayHandlerWithLimits(t, lifecycle, func() time.Time { return now }, time.Minute, 2, 1)
	body := `{}`
	if response := replayRequestForSession(handler, "session-1", body, "session-1-key"); response.Code != http.StatusOK {
		t.Fatalf("first session start status = %d, body=%s", response.Code, response.Body.String())
	}
	if response := replayRequestForSession(handler, "session-1", body, "session-1-key-2"); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("second session-1 key status = %d, body=%s", response.Code, response.Body.String())
	}
	if response := replayRequestForSession(handler, "session-2", body, "session-2-key"); response.Code != http.StatusOK {
		t.Fatalf("session-2 start status = %d, body=%s", response.Code, response.Body.String())
	}
	if lifecycle.starts != 2 {
		t.Fatalf("lifecycle starts = %d, want 2", lifecycle.starts)
	}
}

func TestHandlerRejectsOversizedIdempotencyKey(t *testing.T) {
	fixture := newFixture(t)
	key := strings.Repeat("k", maxIdempotencyKeyBytes+1)
	response := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/start", `{}`, key)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized idempotency key status = %d, body=%s", response.Code, response.Body.String())
	}
	if fixture.lifecycle.starts != 0 {
		t.Fatalf("lifecycle starts = %d, want 0", fixture.lifecycle.starts)
	}
}

func TestHandlerRejectsBodyBeyondLimitIncludingTrailingWhitespace(t *testing.T) {
	fixture := newFixture(t)
	body := `{}` + strings.Repeat(" ", maxBodyBytes)
	response := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/start", body, "body-limit-key")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("oversized body status = %d, body=%s", response.Code, response.Body.String())
	}
	if fixture.lifecycle.starts != 0 {
		t.Fatalf("lifecycle starts = %d, want 0", fixture.lifecycle.starts)
	}
}

func TestHandlerMapsMissingConnectionIDToBadRequest(t *testing.T) {
	fixture := newFixture(t)
	response := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/ice-candidates", `{"candidates":[]}`, "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing connection id status = %d, body=%s", response.Code, response.Body.String())
	}
}

func TestHandlerDoesNotCacheFailedReplay(t *testing.T) {
	fixture := newFixture(t)
	fixture.lifecycle.startErr = errors.New("temporary lifecycle failure")
	failed := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/start", `{}`, "start-key")
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("failed start status = %d, body=%s", failed.Code, failed.Body.String())
	}
	fixture.lifecycle.startErr = nil
	recovered := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/start", `{}`, "start-key")
	if recovered.Code != http.StatusOK {
		t.Fatalf("recovered start status = %d, body=%s", recovered.Code, recovered.Body.String())
	}
	if fixture.lifecycle.starts != 2 {
		t.Fatalf("lifecycle starts after recovery = %d, want 2", fixture.lifecycle.starts)
	}
}

func TestHandlerRejectsWrongSessionTicketAndAcceptsRepeatedCandidate(t *testing.T) {
	fixture := newFixture(t)
	fixture.tickets.ticket.SessionID = "another-session"
	wrongSession := fixture.request(http.MethodGet, "/realtime/v1/sessions/session-1/runtime", "", "")
	if wrongSession.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-session ticket status = %d, want 401", wrongSession.Code)
	}

	fixture.tickets.ticket.SessionID = "session-1"
	body := `{"connection_id":"connection-1","candidates":[],"end_of_candidates":true}`
	first := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/ice-candidates", body, "")
	second := fixture.request(http.MethodPost, "/realtime/v1/sessions/session-1/ice-candidates", body, "")
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("repeated candidate statuses = %d, %d", first.Code, second.Code)
	}
}

type fixture struct {
	handler   http.Handler
	lifecycle *lifecycleFake
	signaling *signalingFake
	tickets   *ticketFake
	config    *configFake
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	now := time.Unix(1700000000, 0).UTC()
	lifecycle := &lifecycleFake{
		runtime: session.RuntimeSnapshot{SessionID: "session-1", RuntimeState: session.RuntimeListening, UpdatedAt: now},
		stopped: session.RuntimeSnapshot{SessionID: "session-1", RuntimeState: session.RuntimeStopped, UpdatedAt: now},
	}
	tickets := &ticketFake{ticket: webrtc.ConnectionTicket{SessionID: "session-1", AccountID: "account-1", ExpiresAt: now.Add(time.Hour)}}
	config := &configFake{value: WebRTCConfig{SessionID: "session-1", ExpiresAt: now.Add(time.Hour), ICETransportPolicy: "all"}}
	handler, err := New(Dependencies{Lifecycle: lifecycle, Signaling: &signalingFake{}, Tickets: tickets, Config: config, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return fixture{handler: handler, lifecycle: lifecycle, signaling: handler.signaling.(*signalingFake), tickets: tickets, config: config}
}

func newReplayHandler(t *testing.T, lifecycle Lifecycle, now func() time.Time, replayTTL time.Duration, replayMax int) *Handler {
	return newReplayHandlerWithLimits(t, lifecycle, now, replayTTL, replayMax, 0)
}

func newReplayHandlerWithLimits(t *testing.T, lifecycle Lifecycle, now func() time.Time, replayTTL time.Duration, replayMax, replayMaxPerSession int) *Handler {
	t.Helper()
	baseNow := time.Unix(1700000000, 0).UTC()
	tickets := &sessionTicketFake{expiresAt: baseNow.Add(time.Hour)}
	config := &configFake{value: WebRTCConfig{SessionID: "session-1", ExpiresAt: baseNow.Add(time.Hour), ICETransportPolicy: "all"}}
	handler, err := New(Dependencies{
		Lifecycle: lifecycle, Signaling: &signalingFake{}, Tickets: tickets, Config: config, Now: now,
		ReplayTTL: replayTTL, ReplayMaxEntries: replayMax, ReplayMaxEntriesPerSession: replayMaxPerSession,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return handler
}

func replayRequest(handler http.Handler, body, idempotencyKey string) *httptest.ResponseRecorder {
	return replayRequestForSession(handler, "session-1", body, idempotencyKey)
}

func replayRequestForSession(handler http.Handler, sessionID, body, idempotencyKey string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/realtime/v1/sessions/"+sessionID+"/start", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer realtime-ticket")
	request.Header.Set("Idempotency-Key", idempotencyKey)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func (f fixture) request(method, path, body, idempotencyKey string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer realtime-ticket")
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

type lifecycleFake struct {
	runtime  session.RuntimeSnapshot
	stopped  session.RuntimeSnapshot
	startErr error
	stopErr  error
	starts   int
	stops    int
}

type blockingLifecycleFake struct {
	runtime session.RuntimeSnapshot
	entered chan struct{}
	release chan struct{}
	starts  atomic.Int32
}

func (f *blockingLifecycleFake) Start(ctx context.Context, _ session.StartRealtimeCommand) (session.RuntimeSnapshot, error) {
	if f.starts.Add(1) == 1 {
		close(f.entered)
		select {
		case <-f.release:
		case <-ctx.Done():
			return session.RuntimeSnapshot{}, ctx.Err()
		}
	}
	return f.runtime, nil
}

func (f *blockingLifecycleFake) Stop(context.Context, session.StopRealtimeCommand) (session.RuntimeSnapshot, error) {
	return f.runtime, nil
}

func (f *blockingLifecycleFake) GetRuntimeState(context.Context, string) (session.RuntimeSnapshot, error) {
	return f.runtime, nil
}

func (f *lifecycleFake) Start(_ context.Context, command session.StartRealtimeCommand) (session.RuntimeSnapshot, error) {
	f.starts++
	if f.startErr != nil {
		return session.RuntimeSnapshot{}, f.startErr
	}
	if command.SessionID != "session-1" {
		return session.RuntimeSnapshot{}, errors.New("unexpected session")
	}
	return f.runtime, nil
}

type multiSessionLifecycleFake struct {
	runtime session.RuntimeSnapshot
	starts  int
}

func (f *multiSessionLifecycleFake) Start(_ context.Context, command session.StartRealtimeCommand) (session.RuntimeSnapshot, error) {
	f.starts++
	f.runtime.SessionID = command.SessionID
	return f.runtime, nil
}

func (f *multiSessionLifecycleFake) Stop(_ context.Context, command session.StopRealtimeCommand) (session.RuntimeSnapshot, error) {
	f.runtime.SessionID = command.SessionID
	return f.runtime, nil
}

func (f *multiSessionLifecycleFake) GetRuntimeState(context.Context, string) (session.RuntimeSnapshot, error) {
	return f.runtime, nil
}

func (f *lifecycleFake) Stop(_ context.Context, command session.StopRealtimeCommand) (session.RuntimeSnapshot, error) {
	f.stops++
	if f.stopErr != nil {
		return session.RuntimeSnapshot{}, f.stopErr
	}
	if command.SessionID != "session-1" {
		return session.RuntimeSnapshot{}, errors.New("unexpected session")
	}
	return f.stopped, nil
}

func (f *lifecycleFake) GetRuntimeState(context.Context, string) (session.RuntimeSnapshot, error) {
	return f.runtime, nil
}

type signalingFake struct {
	offerCalls     int
	candidateCalls int
}

func (f *signalingFake) Offer(_ context.Context, token, sessionID string, request webrtc.OfferRequest) (webrtc.OfferResponse, error) {
	f.offerCalls++
	if token == "" || sessionID == "" || request.IdempotencyKey == "" {
		return webrtc.OfferResponse{}, webrtc.ErrRealtimeTokenRequired
	}
	return webrtc.OfferResponse{SDP: "answer-sdp", Type: "answer", SessionID: sessionID, ConnectionID: "connection-1", ConnectionState: realtimev1.ConnectionConnecting}, nil
}

func (f *signalingFake) AddCandidates(_ context.Context, token, sessionID string, request webrtc.CandidateRequest) (webrtc.CandidateResponse, error) {
	f.candidateCalls++
	if token == "" || sessionID == "" {
		return webrtc.CandidateResponse{}, webrtc.ErrRealtimeTokenRequired
	}
	if request.ConnectionID == "" {
		return webrtc.CandidateResponse{}, webrtc.ErrConnectionIDRequired
	}
	return webrtc.CandidateResponse{ConnectionID: request.ConnectionID, AcceptedCandidateIDs: []string{"candidate-1"}, EndOfCandidates: request.EndOfCandidates}, nil
}

type ticketFake struct {
	ticket webrtc.ConnectionTicket
	err    error
}

func (f *ticketFake) Validate(context.Context, string, string) (webrtc.ConnectionTicket, error) {
	return f.ticket, f.err
}

type sessionTicketFake struct {
	expiresAt time.Time
}

func (f *sessionTicketFake) Validate(_ context.Context, _ string, sessionID string) (webrtc.ConnectionTicket, error) {
	return webrtc.ConnectionTicket{SessionID: sessionID, AccountID: "account-1", ExpiresAt: f.expiresAt}, nil
}

type configFake struct {
	value WebRTCConfig
	err   error
}

func (f *configFake) GetConfig(context.Context, string) (WebRTCConfig, error) {
	return f.value, f.err
}
