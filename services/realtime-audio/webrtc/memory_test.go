package webrtc

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryConnectionManagerOpenIsIdempotentPerSession(t *testing.T) {
	manager := NewMemoryConnectionManager()
	request := validOpenConnectionRequest()

	first, err := manager.Open(context.Background(), request)
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	second, err := manager.Open(context.Background(), request)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	if first.ID == "" || first.ID != second.ID || first.State != ConnectionConnecting {
		t.Fatalf("connections = %#v, %#v", first, second)
	}
	found, ok, err := manager.Find(context.Background(), request.SessionID, request.IdempotencyKey)
	if err != nil || !ok || found.ID != first.ID {
		t.Fatalf("Find() = %#v, %t, %v", found, ok, err)
	}
}

func TestMemoryConnectionManagerCandidatesAreIdempotent(t *testing.T) {
	manager := NewMemoryConnectionManager()
	connection, err := manager.Open(context.Background(), validOpenConnectionRequest())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	response, err := manager.AddCandidates(context.Background(), connection.SessionID, CandidateRequest{
		ConnectionID: connection.ID,
		Candidates: []ICECandidate{
			{ID: "candidate-1", Candidate: "candidate:1"},
			{ID: "candidate-1", Candidate: "candidate:1"},
		},
		EndOfCandidates: true,
	})
	if err != nil {
		t.Fatalf("AddCandidates() error = %v", err)
	}
	if got, want := response.AcceptedCandidateIDs, []string{"candidate-1"}; !sameStrings(got, want) {
		t.Fatalf("accepted = %#v, want %#v", got, want)
	}
	if got, want := response.DeduplicatedCandidateIDs, []string{"candidate-1"}; !sameStrings(got, want) || !response.EndOfCandidates {
		t.Fatalf("response = %#v", response)
	}
}

func TestMemoryConnectionManagerRejectsInvalidRequests(t *testing.T) {
	manager := NewMemoryConnectionManager()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		run  func() error
		want error
	}{
		{name: "empty session", run: func() error { _, err := manager.Open(context.Background(), OpenConnectionRequest{}); return err }, want: ErrSessionIDRequired},
		{name: "missing candidate connection", run: func() error {
			_, err := manager.AddCandidates(context.Background(), "session-1", CandidateRequest{})
			return err
		}, want: ErrConnectionIDRequired},
		{name: "canceled close", run: func() error { return manager.Close(canceled, "session-1") }, want: context.Canceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestMemoryConnectionManagerCloseIsIdempotent(t *testing.T) {
	manager := NewMemoryConnectionManager()
	connection, err := manager.Open(context.Background(), validOpenConnectionRequest())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := manager.Close(context.Background(), connection.SessionID); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := manager.Close(context.Background(), connection.SessionID); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if _, err := manager.AddCandidates(context.Background(), connection.SessionID, CandidateRequest{ConnectionID: connection.ID}); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("AddCandidates() error = %v, want ErrConnectionNotFound", err)
	}
}

func validOpenConnectionRequest() OpenConnectionRequest {
	return OpenConnectionRequest{
		SessionID: "session-1", IdempotencyKey: "offer-device-1",
		Offer:     SessionDescription{SDP: "offer-sdp", Type: "offer"},
		Answer:    SessionDescription{SDP: "answer-sdp", Type: "answer"},
		CreatedAt: time.Unix(1700000000, 0).UTC(),
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
