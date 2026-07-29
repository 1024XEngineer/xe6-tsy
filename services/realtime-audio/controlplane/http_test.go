package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
	return webrtc.CandidateResponse{ConnectionID: request.ConnectionID, AcceptedCandidateIDs: []string{"candidate-1"}, EndOfCandidates: request.EndOfCandidates}, nil
}

type ticketFake struct {
	ticket webrtc.ConnectionTicket
	err    error
}

func (f *ticketFake) Validate(context.Context, string, string) (webrtc.ConnectionTicket, error) {
	return f.ticket, f.err
}

type configFake struct {
	value WebRTCConfig
	err   error
}

func (f *configFake) GetConfig(context.Context, string) (WebRTCConfig, error) {
	return f.value, f.err
}
