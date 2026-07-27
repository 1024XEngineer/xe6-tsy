package webrtc

import (
	"context"
	"errors"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

func TestMemoryConnectionManagerTracksCurrentState(t *testing.T) {
	manager := NewMemoryConnectionManager(&fakeTransportFactory{
		transport: &fakeTransport{answer: SessionDescription{SDP: "answer-sdp", Type: "answer"}},
	})
	connection, err := manager.Open(context.Background(), validOpenConnectionRequest())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	snapshot, err := manager.GetCurrent(context.Background(), connection.SessionID)
	if err != nil {
		t.Fatalf("GetCurrent() error = %v", err)
	}
	if snapshot.SessionID != connection.SessionID || snapshot.ConnectionID != connection.ID ||
		snapshot.State != realtimev1.ConnectionConnecting || snapshot.Version != 1 ||
		!snapshot.UpdatedAt.Equal(connection.CreatedAt) {
		t.Fatalf("initial snapshot = %#v", snapshot)
	}

	connectedAt := connection.CreatedAt.Add(time.Second)
	snapshot, err = manager.ApplyState(context.Background(), connection.SessionID, connection.ID, realtimev1.ConnectionConnected, connectedAt)
	if err != nil {
		t.Fatalf("ApplyState() error = %v", err)
	}
	if snapshot.State != realtimev1.ConnectionConnected || snapshot.Version != 2 || !snapshot.UpdatedAt.Equal(connectedAt) {
		t.Fatalf("connected snapshot = %#v", snapshot)
	}
	duplicate, err := manager.ApplyState(context.Background(), connection.SessionID, connection.ID, realtimev1.ConnectionConnected, connectedAt.Add(time.Second))
	if err != nil || duplicate != snapshot {
		t.Fatalf("duplicate ApplyState() = %#v, %v", duplicate, err)
	}

	for index, state := range []realtimev1.ConnectionState{
		realtimev1.ConnectionDisconnected,
		realtimev1.ConnectionFailed,
		realtimev1.ConnectionClosed,
	} {
		snapshot, err = manager.ApplyState(
			context.Background(), connection.SessionID, connection.ID, state,
			connectedAt.Add(time.Duration(index+2)*time.Second),
		)
		if err != nil {
			t.Fatalf("ApplyState(%q) error = %v", state, err)
		}
	}
	if snapshot.State != realtimev1.ConnectionClosed || snapshot.Version != 5 {
		t.Fatalf("closed snapshot = %#v", snapshot)
	}
	_, err = manager.ApplyState(context.Background(), connection.SessionID, connection.ID, realtimev1.ConnectionConnected, connectedAt.Add(5*time.Second))
	if !errors.Is(err, ErrConnectionStateTransition) {
		t.Fatalf("ApplyState() after closed error = %v, want ErrConnectionStateTransition", err)
	}
}

func TestMemoryConnectionManagerRejectsInvalidStateChanges(t *testing.T) {
	manager := NewMemoryConnectionManager(&fakeTransportFactory{
		transport: &fakeTransport{answer: SessionDescription{SDP: "answer-sdp", Type: "answer"}},
	})
	connection, err := manager.Open(context.Background(), validOpenConnectionRequest())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	tests := []struct {
		name  string
		state realtimev1.ConnectionState
		want  error
	}{
		{name: "unknown", state: "unknown", want: ErrConnectionStateInvalid},
		{name: "illegal transition", state: realtimev1.ConnectionNew, want: ErrConnectionStateTransition},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := manager.ApplyState(context.Background(), connection.SessionID, connection.ID, test.state, connection.CreatedAt.Add(time.Second))
			if !errors.Is(err, test.want) {
				t.Fatalf("ApplyState() error = %v, want %v", err, test.want)
			}
		})
	}
	snapshot, err := manager.GetCurrent(context.Background(), connection.SessionID)
	if err != nil || snapshot.State != realtimev1.ConnectionConnecting || snapshot.Version != 1 {
		t.Fatalf("snapshot after rejected updates = %#v, %v", snapshot, err)
	}
}

func TestMemoryConnectionManagerRejectsStaleConnectionCallback(t *testing.T) {
	factory := &sequenceTransportFactory{transports: []ConnectionTransport{
		&fakeTransport{answer: SessionDescription{SDP: "answer-1", Type: "answer"}},
		&fakeTransport{answer: SessionDescription{SDP: "answer-2", Type: "answer"}},
	}}
	manager := NewMemoryConnectionManager(factory)
	first, err := manager.Open(context.Background(), validOpenConnectionRequest())
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	secondRequest := validOpenConnectionRequest()
	secondRequest.IdempotencyKey = "offer-device-2"
	secondRequest.CreatedAt = first.CreatedAt.Add(time.Second)
	second, err := manager.Open(context.Background(), secondRequest)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}

	_, err = manager.ApplyState(context.Background(), first.SessionID, first.ID, realtimev1.ConnectionConnected, second.CreatedAt.Add(time.Second))
	if !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("stale ApplyState() error = %v, want ErrConnectionNotFound", err)
	}
	snapshot, err := manager.GetCurrent(context.Background(), first.SessionID)
	if err != nil || snapshot.ConnectionID != second.ID || snapshot.State != realtimev1.ConnectionConnecting {
		t.Fatalf("current snapshot = %#v, %v", snapshot, err)
	}
}

func TestConnectionStateTransitionsKeepClosedTerminal(t *testing.T) {
	tests := []struct {
		from, to realtimev1.ConnectionState
		want     bool
	}{
		{realtimev1.ConnectionNew, realtimev1.ConnectionConnecting, true},
		{realtimev1.ConnectionConnecting, realtimev1.ConnectionConnected, true},
		{realtimev1.ConnectionConnected, realtimev1.ConnectionDisconnected, true},
		{realtimev1.ConnectionDisconnected, realtimev1.ConnectionConnected, true},
		{realtimev1.ConnectionFailed, realtimev1.ConnectionClosed, true},
		{realtimev1.ConnectionConnected, realtimev1.ConnectionConnecting, false},
		{realtimev1.ConnectionClosed, realtimev1.ConnectionConnected, false},
	}
	for _, test := range tests {
		if got := validConnectionStateTransition(test.from, test.to); got != test.want {
			t.Errorf("validConnectionStateTransition(%q, %q) = %t, want %t", test.from, test.to, got, test.want)
		}
	}
}
