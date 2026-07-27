package webrtc

import (
	"context"
	"errors"
	"testing"
	"time"
)

var errTicket = errors.New("ticket rejected")

func TestSignalingServiceOfferAndCandidateFlow(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	validator := &fakeTicketValidator{ticket: ConnectionTicket{SessionID: "session-1", AccountID: "account-1", ExpiresAt: now.Add(time.Minute)}}
	answerer := &fakeAnswerer{answer: SessionDescription{SDP: "answer-sdp", Type: "answer"}}
	service := newTestSignalingService(t, validator, answerer, now)

	offer, err := service.Offer(context.Background(), "ticket-1", "session-1", OfferRequest{
		SDP: "offer-sdp", Type: "offer", IdempotencyKey: "offer-device-1",
	})
	if err != nil {
		t.Fatalf("Offer() error = %v", err)
	}
	if offer.ConnectionID == "" || offer.SessionID != "session-1" || offer.Type != "answer" || offer.ConnectionState != ConnectionConnecting {
		t.Fatalf("Offer() = %#v", offer)
	}
	retry, err := service.Offer(context.Background(), "ticket-1", "session-1", OfferRequest{
		SDP: "offer-sdp", Type: "offer", IdempotencyKey: "offer-device-1",
	})
	if err != nil || retry.ConnectionID != offer.ConnectionID {
		t.Fatalf("retried Offer() = %#v, %v", retry, err)
	}

	candidates, err := service.AddCandidates(context.Background(), "ticket-1", "session-1", CandidateRequest{
		ConnectionID: offer.ConnectionID, Candidates: []ICECandidate{{ID: "candidate-1", Candidate: "candidate:1"}},
	})
	if err != nil || !sameStrings(candidates.AcceptedCandidateIDs, []string{"candidate-1"}) {
		t.Fatalf("AddCandidates() = %#v, %v", candidates, err)
	}
	if validator.calls != 3 || answerer.calls != 1 {
		t.Fatalf("validator calls = %d, answerer calls = %d", validator.calls, answerer.calls)
	}
}

func TestSignalingServiceRejectsInvalidTicketBeforeOffer(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	tests := []struct {
		name   string
		ticket ConnectionTicket
		err    error
		want   error
	}{
		{name: "validator error", err: errTicket, want: errTicket},
		{name: "session mismatch", ticket: ConnectionTicket{SessionID: "session-2", AccountID: "account-1", ExpiresAt: now.Add(time.Minute)}, want: ErrTicketSessionMismatch},
		{name: "missing account", ticket: ConnectionTicket{SessionID: "session-1", ExpiresAt: now.Add(time.Minute)}, want: ErrTicketAccountRequired},
		{name: "expired", ticket: ConnectionTicket{SessionID: "session-1", AccountID: "account-1", ExpiresAt: now}, want: ErrTicketExpired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			validator := &fakeTicketValidator{ticket: test.ticket, err: test.err}
			answerer := &fakeAnswerer{answer: SessionDescription{SDP: "answer-sdp", Type: "answer"}}
			service := newTestSignalingService(t, validator, answerer, now)

			_, err := service.Offer(context.Background(), "ticket-1", "session-1", OfferRequest{
				SDP: "offer-sdp", Type: "offer", IdempotencyKey: "offer-device-1",
			})
			if !errors.Is(err, test.want) {
				t.Fatalf("Offer() error = %v, want %v", err, test.want)
			}
			if answerer.calls != 0 {
				t.Fatalf("answerer calls = %d, want 0", answerer.calls)
			}
		})
	}
}

func TestNewSignalingServiceRequiresDependencies(t *testing.T) {
	valid := Dependencies{Tickets: &fakeTicketValidator{}, Answers: &fakeAnswerer{}, Connections: NewMemoryConnectionManager(), Now: time.Now}
	tests := []struct {
		name string
		edit func(*Dependencies)
	}{
		{name: "tickets", edit: func(dependencies *Dependencies) { dependencies.Tickets = nil }},
		{name: "answers", edit: func(dependencies *Dependencies) { dependencies.Answers = nil }},
		{name: "connections", edit: func(dependencies *Dependencies) { dependencies.Connections = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dependencies := valid
			test.edit(&dependencies)
			if _, err := NewSignalingService(dependencies); !errors.Is(err, ErrInvalidDependency) {
				t.Fatalf("NewSignalingService() error = %v, want ErrInvalidDependency", err)
			}
		})
	}
}

func newTestSignalingService(t *testing.T, validator TicketValidator, answerer SDPAnswerer, now time.Time) *SignalingService {
	t.Helper()
	service, err := NewSignalingService(Dependencies{
		Tickets: validator, Answers: answerer, Connections: NewMemoryConnectionManager(), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewSignalingService() error = %v", err)
	}
	return service
}

type fakeTicketValidator struct {
	ticket ConnectionTicket
	err    error
	calls  int
}

func (f *fakeTicketValidator) Validate(_ context.Context, _, _ string) (ConnectionTicket, error) {
	f.calls++
	if f.err != nil {
		return ConnectionTicket{}, f.err
	}
	return f.ticket, nil
}

type fakeAnswerer struct {
	answer SessionDescription
	err    error
	calls  int
}

func (f *fakeAnswerer) Answer(_ context.Context, _ SessionDescription) (SessionDescription, error) {
	f.calls++
	if f.err != nil {
		return SessionDescription{}, f.err
	}
	return f.answer, nil
}

var _ TicketValidator = (*fakeTicketValidator)(nil)
var _ SDPAnswerer = (*fakeAnswerer)(nil)
