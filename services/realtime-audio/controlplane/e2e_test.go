package controlplane

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/outbox"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
)

func TestHTTPStartOfferICEDeliveryStop(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	durable := outbox.NewMemoryOutbox()
	lifecycle := &e2eLifecycle{
		listening: session.RuntimeSnapshot{SessionID: "session-1", RuntimeState: session.RuntimeListening, UpdatedAt: now},
		stopped:   session.RuntimeSnapshot{SessionID: "session-1", RuntimeState: session.RuntimeStopped, UpdatedAt: now},
	}
	tickets := &ticketFake{ticket: webrtc.ConnectionTicket{SessionID: "session-1", AccountID: "account-1", ExpiresAt: now.Add(time.Hour)}}
	signaling := &deliverySignaling{outbox: durable}
	handler, err := New(Dependencies{
		Lifecycle: lifecycle, Signaling: signaling, Connections: &connectionFake{},
		Tickets: tickets,
		Config:  &configFake{value: WebRTCConfig{SessionID: "session-1", ExpiresAt: now.Add(time.Hour)}},
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	do := func(method, path, body, key string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer realtime-ticket")
		if key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	if response := do(http.MethodPost, "/realtime/v1/sessions/session-1/start", `{"operation_id":"operation-1"}`, "start-key"); response.Code != http.StatusOK {
		t.Fatalf("start status = %d, body=%s", response.Code, response.Body.String())
	}
	if response := do(http.MethodPost, "/realtime/v1/sessions/session-1/webrtc/offer", `{"sdp":"offer-sdp","type":"offer"}`, "offer-key"); response.Code != http.StatusOK {
		t.Fatalf("offer status = %d, body=%s", response.Code, response.Body.String())
	}
	if response := do(http.MethodPost, "/realtime/v1/sessions/session-1/ice-candidates", `{"connection_id":"connection-1","candidates":[],"end_of_candidates":true}`, ""); response.Code != http.StatusOK {
		t.Fatalf("candidate status = %d, body=%s", response.Code, response.Body.String())
	}
	if response := do(http.MethodPost, "/realtime/v1/sessions/session-1/webrtc/offer", `{"sdp":"offer-sdp","type":"offer"}`, "offer-key"); response.Code != http.StatusOK {
		t.Fatalf("replayed offer status = %d, body=%s", response.Code, response.Body.String())
	}
	stopBody := `{"trace_id":"trace-stop","reason":"user_requested","ended_at":"2023-11-14T22:14:20Z"}`
	if response := do(http.MethodPost, "/realtime/v1/sessions/session-1/stop", stopBody, "stop-key"); response.Code != http.StatusOK {
		t.Fatalf("stop status = %d, body=%s", response.Code, response.Body.String())
	}
	if response := do(http.MethodPost, "/realtime/v1/sessions/session-1/stop", stopBody, "stop-key"); response.Code != http.StatusOK {
		t.Fatalf("replayed stop status = %d, body=%s", response.Code, response.Body.String())
	}

	if lifecycle.starts != 1 || lifecycle.stops != 1 {
		t.Fatalf("lifecycle calls = start %d, stop %d", lifecycle.starts, lifecycle.stops)
	}
	if signaling.offers != 2 {
		t.Fatalf("offer calls = %d, want 2 replay attempts", signaling.offers)
	}
	if got := len(durable.Entries()); got != 2 {
		t.Fatalf("durable entries = %d, want one FinalTurn and one UsageFact", got)
	}
}

type e2eLifecycle struct {
	listening session.RuntimeSnapshot
	stopped   session.RuntimeSnapshot
	starts    int
	stops     int
}

func (f *e2eLifecycle) Start(context.Context, session.StartRealtimeCommand) (session.RuntimeSnapshot, error) {
	f.starts++
	return f.listening, nil
}

func (f *e2eLifecycle) Stop(context.Context, session.StopRealtimeCommand) (session.RuntimeSnapshot, error) {
	f.stops++
	return f.stopped, nil
}

func (f *e2eLifecycle) GetRuntimeState(context.Context, string) (session.RuntimeSnapshot, error) {
	return f.listening, nil
}

type deliverySignaling struct {
	outbox *outbox.MemoryOutbox
	offers int
}

func (s *deliverySignaling) Offer(ctx context.Context, sessionToken, sessionID string, request webrtc.OfferRequest) (webrtc.OfferResponse, error) {
	s.offers++
	if err := s.outbox.Append(ctx, recordsv1.FinalTurnTopic, "final_turn-1", e2eFinalTurn()); err != nil {
		return webrtc.OfferResponse{}, err
	}
	if err := s.outbox.Append(ctx, "usage.recorded", "usage:turn-1:translation", e2eUsageFact()); err != nil {
		return webrtc.OfferResponse{}, err
	}
	return webrtc.OfferResponse{SDP: "answer-sdp", Type: "answer", SessionID: sessionID, ConnectionID: "connection-1", ConnectionState: realtimev1.ConnectionConnecting}, nil
}

func (s *deliverySignaling) AddCandidates(context.Context, string, string, webrtc.CandidateRequest) (webrtc.CandidateResponse, error) {
	return webrtc.CandidateResponse{ConnectionID: "connection-1", EndOfCandidates: true}, nil
}

func e2eFinalTurn() pipeline.FinalTurnEvent {
	return pipeline.FinalTurnEvent{
		EventVersion: recordsv1.FinalTurnEventVersion,
		EventID:      "final_turn-1", TraceID: "trace-1", TurnID: "turn-1", SessionID: "session-1", SequenceNo: 1,
		SourceLanguage: "zh-CN", TargetLanguage: "en-US", LanguageConfigVersion: 1,
		SourceText: "你好", TranslatedText: "hello", SpeakerCode: recordsv1.PendingSpeakerCode,
		AttributionStatus: recordsv1.AttributionPending, StartedAt: time.Unix(1700000000, 0).UTC(),
		EndedAt: time.Unix(1700000001, 0).UTC(), OccurredAt: time.Unix(1700000001, 0).UTC(),
	}
}

func e2eUsageFact() pipeline.UsageFact {
	return pipeline.UsageFact{
		EventVersion: 1, ID: "usage-1", TraceID: "trace-1", IdempotencyKey: "usage:turn-1:translation",
		AccountID: "account-1", SessionID: "session-1", TurnID: "turn-1", ServiceType: "translation",
		Provider: "fake", Model: "fake", OccurredAt: time.Unix(1700000001, 0).UTC(),
	}
}
