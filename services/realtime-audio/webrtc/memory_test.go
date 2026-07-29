package webrtc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
)

func TestMemoryConnectionManagerOpenIsIdempotentPerSession(t *testing.T) {
	factory := &fakeTransportFactory{transport: &fakeTransport{answer: SessionDescription{SDP: "answer-sdp", Type: "answer"}}}
	manager := NewMemoryConnectionManager(factory)
	request := validOpenConnectionRequest()

	first, err := manager.Open(context.Background(), request)
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	second, err := manager.Open(context.Background(), request)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	if first.ID == "" || first.ID != second.ID || first.State != realtimev1.ConnectionConnecting {
		t.Fatalf("connections = %#v, %#v", first, second)
	}
	if factory.createCalls != 1 || factory.transport.answerCalls != 1 {
		t.Fatalf("factory calls = %d, answer calls = %d", factory.createCalls, factory.transport.answerCalls)
	}
}

func TestMemoryConnectionManagerAppliesTransportStateDuringAnswer(t *testing.T) {
	connectedAt := time.Unix(1700000001, 0).UTC()
	factory := &stateCallbackTransportFactory{
		transport: &stateCallbackTransport{
			answer:    SessionDescription{SDP: "answer-sdp", Type: "answer"},
			state:     realtimev1.ConnectionConnected,
			updatedAt: connectedAt,
		},
	}
	manager := NewMemoryConnectionManager(factory)
	connection, err := manager.Open(context.Background(), validOpenConnectionRequest())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	snapshot, err := manager.GetCurrent(context.Background(), connection.SessionID)
	if err != nil {
		t.Fatalf("GetCurrent() error = %v", err)
	}
	if snapshot.State != realtimev1.ConnectionConnected || snapshot.Version != 2 || !snapshot.UpdatedAt.Equal(connectedAt) {
		t.Fatalf("snapshot after transport callback = %#v", snapshot)
	}
}

func TestTransportStateGateDrainsPendingBeforeNewCallbacks(t *testing.T) {
	var (
		states       []realtimev1.ConnectionState
		statesMu     sync.Mutex
		pendingStart = make(chan struct{})
		release      = make(chan struct{})
	)
	gate := newTransportStateGate(func(state realtimev1.ConnectionState, _ time.Time) {
		statesMu.Lock()
		states = append(states, state)
		statesMu.Unlock()
		if state == realtimev1.ConnectionConnecting {
			close(pendingStart)
			<-release
		}
	})
	gate.Notify(realtimev1.ConnectionConnecting, time.Unix(1700000001, 0).UTC())

	activated := make(chan struct{})
	go func() {
		gate.Activate()
		close(activated)
	}()
	select {
	case <-pendingStart:
	case <-time.After(time.Second):
		t.Fatal("Activate() did not deliver pending state")
	}

	newCallbackDone := make(chan struct{})
	go func() {
		gate.Notify(realtimev1.ConnectionConnected, time.Unix(1700000002, 0).UTC())
		close(newCallbackDone)
	}()
	select {
	case <-newCallbackDone:
		t.Fatal("new callback overtook pending state delivery")
	case <-time.After(20 * time.Millisecond):
	}

	close(release)
	select {
	case <-activated:
	case <-time.After(time.Second):
		t.Fatal("Activate() did not finish")
	}
	select {
	case <-newCallbackDone:
	case <-time.After(time.Second):
		t.Fatal("new callback did not finish after pending delivery")
	}

	statesMu.Lock()
	defer statesMu.Unlock()
	want := []realtimev1.ConnectionState{realtimev1.ConnectionConnecting, realtimev1.ConnectionConnected}
	if len(states) != len(want) || states[0] != want[0] || states[1] != want[1] {
		t.Fatalf("delivered states = %#v, want %#v", states, want)
	}
}

func TestMemoryConnectionManagerDiscardsStateAfterAnswerFailureAndAllowsRetry(t *testing.T) {
	answerErr := errors.New("answer failed")
	first := &stateCallbackTransport{
		answerErr: answerErr,
		state:     realtimev1.ConnectionConnected,
		updatedAt: time.Unix(1700000001, 0).UTC(),
	}
	second := &stateCallbackTransport{
		answer:    SessionDescription{SDP: "answer-sdp", Type: "answer"},
		state:     realtimev1.ConnectionConnected,
		updatedAt: time.Unix(1700000002, 0).UTC(),
	}
	factory := &sequenceStateCallbackTransportFactory{transports: []*stateCallbackTransport{first, second}}
	manager := NewMemoryConnectionManager(factory)
	request := validOpenConnectionRequest()

	if _, err := manager.Open(context.Background(), request); !errors.Is(err, answerErr) {
		t.Fatalf("failed Open() error = %v, want %v", err, answerErr)
	}
	if _, err := manager.GetCurrent(context.Background(), request.SessionID); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("GetCurrent() after failed Open() error = %v, want %v", err, ErrConnectionNotFound)
	}

	connection, err := manager.Open(context.Background(), request)
	if err != nil {
		t.Fatalf("retry Open() error = %v", err)
	}
	if connection.State != realtimev1.ConnectionConnected {
		t.Fatalf("retry connection state = %q, want %q", connection.State, realtimev1.ConnectionConnected)
	}
	snapshot, err := manager.GetCurrent(context.Background(), request.SessionID)
	if err != nil {
		t.Fatalf("GetCurrent() after retry error = %v", err)
	}
	if snapshot.ConnectionID != connection.ID || snapshot.State != realtimev1.ConnectionConnected {
		t.Fatalf("retry snapshot = %#v", snapshot)
	}
}

func TestMemoryConnectionManagerRejectsChangedOfferForIdempotencyKey(t *testing.T) {
	factory := &fakeTransportFactory{transport: &fakeTransport{answer: SessionDescription{SDP: "answer-sdp", Type: "answer"}}}
	manager := NewMemoryConnectionManager(factory)
	request := validOpenConnectionRequest()
	if _, err := manager.Open(context.Background(), request); err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	request.Offer.SDP = "different-offer-sdp"
	if _, err := manager.Open(context.Background(), request); !errors.Is(err, ErrIdempotencyPayloadConflict) {
		t.Fatalf("second Open() error = %v, want %v", err, ErrIdempotencyPayloadConflict)
	}
	if factory.createCalls != 1 {
		t.Fatalf("factory calls = %d, want 1", factory.createCalls)
	}
}

func TestMemoryConnectionManagerRejectsInvalidTransportResults(t *testing.T) {
	tests := []struct {
		name      string
		transport *fakeTransport
		want      error
	}{
		{name: "missing transport", want: ErrTransportRequired},
		{name: "empty answer SDP", transport: &fakeTransport{answer: SessionDescription{Type: "answer"}}, want: ErrAnswerSDPRequired},
		{name: "unexpected answer type", transport: &fakeTransport{answer: SessionDescription{SDP: "answer-sdp", Type: "offer"}}, want: ErrAnswerTypeInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager := NewMemoryConnectionManager(&fakeTransportFactory{transport: test.transport})
			if _, err := manager.Open(context.Background(), validOpenConnectionRequest()); !errors.Is(err, test.want) {
				t.Fatalf("Open() error = %v, want %v", err, test.want)
			}
			if test.transport != nil && test.transport.closeCalls != 1 {
				t.Fatalf("transport close calls = %d, want 1", test.transport.closeCalls)
			}
		})
	}
}

func TestMemoryConnectionManagerCandidatesAreIdempotent(t *testing.T) {
	transport := &fakeTransport{answer: SessionDescription{SDP: "answer-sdp", Type: "answer"}}
	manager := NewMemoryConnectionManager(&fakeTransportFactory{transport: transport})
	connection, err := manager.Open(context.Background(), validOpenConnectionRequest())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	request := CandidateRequest{
		ConnectionID: connection.ID,
		Candidates: []ICECandidate{
			{ID: "candidate-1", Candidate: "candidate:1"},
			{ID: "candidate-1", Candidate: "candidate:1"},
		},
		EndOfCandidates: true,
	}
	response, err := manager.AddCandidates(context.Background(), connection.SessionID, request)
	if err != nil {
		t.Fatalf("AddCandidates() error = %v", err)
	}
	if got, want := response.AcceptedCandidateIDs, []string{"candidate-1"}; !sameStrings(got, want) {
		t.Fatalf("accepted = %#v, want %#v", got, want)
	}
	if got, want := response.DeduplicatedCandidateIDs, []string{"candidate-1"}; !sameStrings(got, want) || !response.EndOfCandidates {
		t.Fatalf("response = %#v", response)
	}
	if len(transport.candidates) != 1 {
		t.Fatalf("transport candidates = %#v", transport.candidates)
	}
	if transport.endCandidatesCalls != 1 {
		t.Fatalf("transport end candidates calls = %d, want 1", transport.endCandidatesCalls)
	}
	if _, err := manager.AddCandidates(context.Background(), connection.SessionID, request); err != nil {
		t.Fatalf("repeated AddCandidates() error = %v", err)
	}
	if transport.endCandidatesCalls != 1 {
		t.Fatalf("transport end candidates calls after retry = %d, want 1", transport.endCandidatesCalls)
	}
}

func TestMemoryConnectionManagerRejectsNewCandidateAfterEnd(t *testing.T) {
	transport := &fakeTransport{answer: SessionDescription{SDP: "answer-sdp", Type: "answer"}}
	manager := NewMemoryConnectionManager(&fakeTransportFactory{transport: transport})
	connection, err := manager.Open(context.Background(), validOpenConnectionRequest())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	first := CandidateRequest{
		ConnectionID:    connection.ID,
		Candidates:      []ICECandidate{{ID: "candidate-1", Candidate: "candidate:1"}},
		EndOfCandidates: true,
	}
	if _, err := manager.AddCandidates(context.Background(), connection.SessionID, first); err != nil {
		t.Fatalf("first AddCandidates() error = %v", err)
	}

	late := CandidateRequest{
		ConnectionID: connection.ID,
		Candidates:   []ICECandidate{{ID: "candidate-2", Candidate: "candidate:2"}},
	}
	if _, err := manager.AddCandidates(context.Background(), connection.SessionID, late); !errors.Is(err, ErrCandidatesCompleted) {
		t.Fatalf("late AddCandidates() error = %v, want %v", err, ErrCandidatesCompleted)
	}
	if len(transport.candidates) != 1 {
		t.Fatalf("transport candidates = %#v, want only the pre-EOC candidate", transport.candidates)
	}
}

func TestMemoryConnectionManagerRejectsChangedCandidateForID(t *testing.T) {
	transport := &fakeTransport{answer: SessionDescription{SDP: "answer-sdp", Type: "answer"}}
	manager := NewMemoryConnectionManager(&fakeTransportFactory{transport: transport})
	connection, err := manager.Open(context.Background(), validOpenConnectionRequest())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	first := CandidateRequest{ConnectionID: connection.ID, Candidates: []ICECandidate{{ID: "candidate-1", Candidate: "candidate:1"}}}
	if _, err := manager.AddCandidates(context.Background(), connection.SessionID, first); err != nil {
		t.Fatalf("first AddCandidates() error = %v", err)
	}
	changed := CandidateRequest{ConnectionID: connection.ID, Candidates: []ICECandidate{{ID: "candidate-1", Candidate: "candidate:2"}}}
	if _, err := manager.AddCandidates(context.Background(), connection.SessionID, changed); !errors.Is(err, ErrIdempotencyPayloadConflict) {
		t.Fatalf("second AddCandidates() error = %v, want %v", err, ErrIdempotencyPayloadConflict)
	}
	if len(transport.candidates) != 1 {
		t.Fatalf("transport candidates = %#v, want one candidate", transport.candidates)
	}
}

func TestMemoryConnectionManagerRejectsInvalidRequests(t *testing.T) {
	manager := NewMemoryConnectionManager(&fakeTransportFactory{transport: &fakeTransport{answer: SessionDescription{SDP: "answer-sdp", Type: "answer"}}})
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
	transport := &fakeTransport{answer: SessionDescription{SDP: "answer-sdp", Type: "answer"}}
	manager := NewMemoryConnectionManager(&fakeTransportFactory{transport: transport})
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
	if _, err := manager.GetCurrent(context.Background(), connection.SessionID); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("GetCurrent() error = %v, want ErrConnectionNotFound", err)
	}
	if _, err := manager.AddCandidates(context.Background(), connection.SessionID, CandidateRequest{ConnectionID: connection.ID}); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("AddCandidates() error = %v, want ErrConnectionNotFound", err)
	}
	if transport.closeCalls != 1 {
		t.Fatalf("transport close calls = %d, want 1", transport.closeCalls)
	}
}

func TestMemoryConnectionManagerRejectsOfferWhileCloseIsInProgress(t *testing.T) {
	closeStarted := make(chan struct{})
	closeRelease := make(chan struct{})
	transport := &fakeTransport{
		answer:       SessionDescription{SDP: "answer-sdp", Type: "answer"},
		closeStarted: closeStarted,
		closeRelease: closeRelease,
	}
	factory := &fakeTransportFactory{transport: transport}
	manager := NewMemoryConnectionManager(factory)
	first, err := manager.Open(context.Background(), validOpenConnectionRequest())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	closeResult := make(chan error, 1)
	go func() {
		closeResult <- manager.Close(context.Background(), first.SessionID)
	}()
	<-closeStarted

	secondRequest := validOpenConnectionRequest()
	secondRequest.IdempotencyKey = "offer-device-2"
	if _, err := manager.Open(context.Background(), secondRequest); !errors.Is(err, ErrConnectionClosing) {
		t.Fatalf("concurrent Open() error = %v, want %v", err, ErrConnectionClosing)
	}
	if factory.createCalls != 1 {
		t.Fatalf("factory calls during close = %d, want 1", factory.createCalls)
	}

	close(closeRelease)
	if err := <-closeResult; err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestMemoryConnectionManagerDoesNotReuseIDsAfterClose(t *testing.T) {
	manager := NewMemoryConnectionManager(&fakeTransportFactory{transport: &fakeTransport{answer: SessionDescription{SDP: "answer-sdp", Type: "answer"}}})
	first, err := manager.Open(context.Background(), validOpenConnectionRequest())
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	if err := manager.Close(context.Background(), first.SessionID); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	secondRequest := validOpenConnectionRequest()
	secondRequest.IdempotencyKey = "offer-device-2"
	second, err := manager.Open(context.Background(), secondRequest)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("connection ID was reused after close: %q", second.ID)
	}
}

func TestMemoryConnectionManagerRetainsFailedCloseForRetry(t *testing.T) {
	closeErr := errors.New("close failed")
	failing := &fakeTransport{answer: SessionDescription{SDP: "answer-sdp", Type: "answer"}, closeErr: closeErr}
	manager := NewMemoryConnectionManager(&fakeTransportFactory{transport: failing})
	connection, err := manager.Open(context.Background(), validOpenConnectionRequest())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	if err := manager.Close(context.Background(), connection.SessionID); !errors.Is(err, closeErr) {
		t.Fatalf("Close() error = %v, want %v", err, closeErr)
	}
	connections := manager.getSession(connection.SessionID)
	if connections == nil || len(connections.byID) != 1 || connections.byID[connection.ID] == nil {
		t.Fatalf("remaining connections = %#v", connections)
	}
	if failing.closeCalls != 1 {
		t.Fatalf("close calls = %d, want 1", failing.closeCalls)
	}

	failing.closeErr = nil
	if err := manager.Close(context.Background(), connection.SessionID); err != nil {
		t.Fatalf("retry Close() error = %v", err)
	}
	if failing.closeCalls != 2 {
		t.Fatalf("close calls after retry = %d, want 2", failing.closeCalls)
	}
	if connections := manager.getSession(connection.SessionID); connections != nil {
		t.Fatalf("connections after retry = %#v, want nil", connections)
	}
}

func TestMemoryConnectionManagerRejectsConcurrentSecondConnection(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	factory := &fakeTransportFactory{
		transport: &fakeTransport{answer: SessionDescription{SDP: "answer-sdp", Type: "answer"}},
		started:   started, release: release,
	}
	manager := NewMemoryConnectionManager(factory)
	firstDone := make(chan error, 1)
	go func() {
		_, err := manager.Open(context.Background(), validOpenConnectionRequest())
		firstDone <- err
	}()
	<-started

	secondRequest := validOpenConnectionRequest()
	secondRequest.IdempotencyKey = "offer-device-2"
	secondStarted := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		close(secondStarted)
		_, err := manager.Open(context.Background(), secondRequest)
		secondDone <- err
	}()
	<-secondStarted
	close(release)

	if err := <-firstDone; err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	if err := <-secondDone; !errors.Is(err, ErrConnectionAlreadyExists) {
		t.Fatalf("second Open() error = %v, want ErrConnectionAlreadyExists", err)
	}
	if factory.createCalls != 1 {
		t.Fatalf("factory calls = %d, want 1", factory.createCalls)
	}
}

func TestMemoryConnectionManagerSerializesConcurrentOffers(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	factory := &fakeTransportFactory{
		transport: &fakeTransport{answer: SessionDescription{SDP: "answer-sdp", Type: "answer"}},
		started:   started, release: release,
	}
	manager := NewMemoryConnectionManager(factory)
	results := make(chan Connection, 2)
	errs := make(chan error, 2)
	request := validOpenConnectionRequest()

	for range 2 {
		go func() {
			connection, err := manager.Open(context.Background(), request)
			if err != nil {
				errs <- err
				return
			}
			results <- connection
		}()
	}
	<-started
	close(release)
	first := <-results
	second := <-results
	if first.ID != second.ID || factory.createCalls != 1 {
		t.Fatalf("connections = %#v, %#v; factory calls = %d", first, second, factory.createCalls)
	}
	select {
	case err := <-errs:
		t.Fatalf("Open() error = %v", err)
	default:
	}
}

func TestMemoryConnectionManagerDoesNotBlockOtherSessionsDuringOffer(t *testing.T) {
	releaseFirst := make(chan struct{})
	factory := &blockingTransportFactory{firstStarted: make(chan struct{}), releaseFirst: releaseFirst, otherStarted: make(chan struct{})}
	manager := NewMemoryConnectionManager(factory)
	firstDone := make(chan error, 1)
	go func() {
		_, err := manager.Open(context.Background(), validOpenConnectionRequest())
		firstDone <- err
	}()
	<-factory.firstStarted

	secondRequest := validOpenConnectionRequest()
	secondRequest.SessionID = "session-2"
	secondRequest.IdempotencyKey = "offer-device-2"
	secondDone := make(chan error, 1)
	go func() {
		_, err := manager.Open(context.Background(), secondRequest)
		secondDone <- err
	}()
	select {
	case <-factory.otherStarted:
	case <-time.After(time.Second):
		t.Fatal("offer for a second session was blocked by the first session transport")
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
}

func TestMemoryConnectionManagerCloseDoesNotWaitForAnswer(t *testing.T) {
	answerStarted := make(chan struct{})
	answerRelease := make(chan struct{})
	transport := &blockingAnswerTransport{
		answer:        SessionDescription{SDP: "answer-sdp", Type: "answer"},
		answerStarted: answerStarted,
		answerRelease: answerRelease,
	}
	manager := NewMemoryConnectionManager(&singleTransportFactory{transport: transport})
	request := validOpenConnectionRequest()

	openResult := make(chan error, 1)
	go func() {
		_, err := manager.Open(context.Background(), request)
		openResult <- err
	}()
	<-answerStarted

	closeResult := make(chan error, 1)
	go func() { closeResult <- manager.Close(context.Background(), request.SessionID) }()
	released := false
	select {
	case err := <-closeResult:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		close(answerRelease)
		released = true
		t.Fatal("Close() waited for Answer() to return")
	}
	if !released {
		close(answerRelease)
	}
	if err := <-openResult; !errors.Is(err, ErrConnectionClosing) {
		t.Fatalf("Open() error = %v, want %v", err, ErrConnectionClosing)
	}
}

func TestMemoryConnectionManagerHidesOpeningConnection(t *testing.T) {
	answerStarted := make(chan struct{})
	answerRelease := make(chan struct{})
	transport := &blockingAnswerTransport{
		answer:        SessionDescription{SDP: "answer-sdp", Type: "answer"},
		answerStarted: answerStarted,
		answerRelease: answerRelease,
	}
	manager := NewMemoryConnectionManager(&singleTransportFactory{transport: transport})
	request := validOpenConnectionRequest()
	connectionID := "rtc_000001"

	openResult := make(chan error, 1)
	go func() {
		_, err := manager.Open(context.Background(), request)
		openResult <- err
	}()
	<-answerStarted

	if _, err := manager.GetCurrent(context.Background(), request.SessionID); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("GetCurrent() during opening error = %v, want %v", err, ErrConnectionNotFound)
	}
	if _, err := manager.ApplyState(
		context.Background(), request.SessionID, connectionID,
		realtimev1.ConnectionConnected, request.CreatedAt.Add(time.Second),
	); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("ApplyState() during opening error = %v, want %v", err, ErrConnectionNotFound)
	}
	if _, err := manager.AddCandidates(context.Background(), request.SessionID, CandidateRequest{
		ConnectionID: connectionID,
		Candidates:   []ICECandidate{{ID: "candidate-1", Candidate: "candidate:1"}},
	}); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("AddCandidates() during opening error = %v, want %v", err, ErrConnectionNotFound)
	}

	closeResult := make(chan error, 1)
	go func() { closeResult <- manager.Close(context.Background(), request.SessionID) }()
	select {
	case err := <-closeResult:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		close(answerRelease)
		t.Fatal("Close() waited for Answer() to return")
	}
	close(answerRelease)
	if err := <-openResult; !errors.Is(err, ErrConnectionClosing) {
		t.Fatalf("Open() error = %v, want %v", err, ErrConnectionClosing)
	}
}

func validOpenConnectionRequest() OpenConnectionRequest {
	return OpenConnectionRequest{
		SessionID: "session-1", IdempotencyKey: "offer-device-1",
		Offer:     SessionDescription{SDP: "offer-sdp", Type: "offer"},
		CreatedAt: time.Unix(1700000000, 0).UTC(),
	}
}

type fakeTransportFactory struct {
	transport   *fakeTransport
	err         error
	started     chan struct{}
	release     <-chan struct{}
	createCalls int
	once        sync.Once
}

type stateCallbackTransportFactory struct {
	transport *stateCallbackTransport
}

type sequenceStateCallbackTransportFactory struct {
	transports []*stateCallbackTransport
	calls      int
}

func (f *stateCallbackTransportFactory) Create(
	_ context.Context,
	_ string,
	_ string,
	onState ConnectionStateHandler,
) (ConnectionTransport, error) {
	f.transport.onState = onState
	return f.transport, nil
}

func (f *sequenceStateCallbackTransportFactory) Create(
	_ context.Context,
	_ string,
	_ string,
	onState ConnectionStateHandler,
) (ConnectionTransport, error) {
	transport := f.transports[f.calls]
	f.calls++
	transport.onState = onState
	return transport, nil
}

type stateCallbackTransport struct {
	answer    SessionDescription
	answerErr error
	state     realtimev1.ConnectionState
	updatedAt time.Time
	onState   ConnectionStateHandler
}

func (t *stateCallbackTransport) Answer(context.Context, SessionDescription) (SessionDescription, error) {
	t.onState(t.state, t.updatedAt)
	if t.answerErr != nil {
		return SessionDescription{}, t.answerErr
	}
	return t.answer, nil
}

func (*stateCallbackTransport) AddCandidate(context.Context, ICECandidate) error { return nil }

func (*stateCallbackTransport) EndCandidates(context.Context) error { return nil }

func (*stateCallbackTransport) Close(context.Context) error { return nil }

type blockingTransportFactory struct {
	mu           sync.Mutex
	firstStarted chan struct{}
	releaseFirst <-chan struct{}
	otherStarted chan struct{}
	calls        int
}

type sequenceTransportFactory struct {
	transports []ConnectionTransport
	calls      int
}

func (f *sequenceTransportFactory) Create(_ context.Context, _, _ string, _ ConnectionStateHandler) (ConnectionTransport, error) {
	transport := f.transports[f.calls]
	f.calls++
	return transport, nil
}

func (f *blockingTransportFactory) Create(_ context.Context, _, _ string, _ ConnectionStateHandler) (ConnectionTransport, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.mu.Unlock()
	if call == 1 {
		close(f.firstStarted)
		<-f.releaseFirst
	} else {
		close(f.otherStarted)
	}
	return &fakeTransport{answer: SessionDescription{SDP: "answer-sdp", Type: "answer"}}, nil
}

func (f *fakeTransportFactory) Create(_ context.Context, _, _ string, _ ConnectionStateHandler) (ConnectionTransport, error) {
	f.createCalls++
	if f.started != nil {
		f.once.Do(func() { close(f.started) })
	}
	if f.release != nil {
		<-f.release
	}
	if f.err != nil {
		return nil, f.err
	}
	if f.transport == nil {
		return nil, nil
	}
	return f.transport, nil
}

type fakeTransport struct {
	answer             SessionDescription
	answerErr          error
	candidateErr       error
	endErr             error
	closeErr           error
	candidates         []ICECandidate
	answerCalls        int
	endCandidatesCalls int
	closeCalls         int
	closeStarted       chan struct{}
	closeRelease       <-chan struct{}
	closeOnce          sync.Once
}

type blockingAnswerTransport struct {
	answer        SessionDescription
	answerStarted chan struct{}
	answerRelease <-chan struct{}
}

func (t *blockingAnswerTransport) Answer(context.Context, SessionDescription) (SessionDescription, error) {
	close(t.answerStarted)
	<-t.answerRelease
	return t.answer, nil
}

func (*blockingAnswerTransport) AddCandidate(context.Context, ICECandidate) error { return nil }

func (*blockingAnswerTransport) EndCandidates(context.Context) error { return nil }

func (*blockingAnswerTransport) Close(context.Context) error { return nil }

type singleTransportFactory struct{ transport ConnectionTransport }

func (f *singleTransportFactory) Create(context.Context, string, string, ConnectionStateHandler) (ConnectionTransport, error) {
	return f.transport, nil
}

func (f *fakeTransport) Answer(_ context.Context, _ SessionDescription) (SessionDescription, error) {
	f.answerCalls++
	if f.answerErr != nil {
		return SessionDescription{}, f.answerErr
	}
	return f.answer, nil
}

func (f *fakeTransport) AddCandidate(_ context.Context, candidate ICECandidate) error {
	if f.candidateErr != nil {
		return f.candidateErr
	}
	f.candidates = append(f.candidates, candidate)
	return nil
}

func (f *fakeTransport) EndCandidates(context.Context) error {
	f.endCandidatesCalls++
	return f.endErr
}

func (f *fakeTransport) Close(context.Context) error {
	f.closeCalls++
	if f.closeStarted != nil {
		f.closeOnce.Do(func() { close(f.closeStarted) })
	}
	if f.closeRelease != nil {
		<-f.closeRelease
	}
	return f.closeErr
}

var _ ConnectionTransportFactory = (*fakeTransportFactory)(nil)
var _ ConnectionTransportFactory = (*blockingTransportFactory)(nil)
var _ ConnectionTransportFactory = (*sequenceTransportFactory)(nil)
var _ ConnectionTransport = (*fakeTransport)(nil)
var _ ConnectionTransportFactory = (*singleTransportFactory)(nil)
var _ ConnectionTransport = (*blockingAnswerTransport)(nil)

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
