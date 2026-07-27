package webrtc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
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
	if first.ID == "" || first.ID != second.ID || first.State != ConnectionConnecting {
		t.Fatalf("connections = %#v, %#v", first, second)
	}
	if factory.createCalls != 1 || factory.transport.answerCalls != 1 {
		t.Fatalf("factory calls = %d, answer calls = %d", factory.createCalls, factory.transport.answerCalls)
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
	if _, err := manager.AddCandidates(context.Background(), connection.SessionID, CandidateRequest{ConnectionID: connection.ID}); !errors.Is(err, ErrConnectionNotFound) {
		t.Fatalf("AddCandidates() error = %v, want ErrConnectionNotFound", err)
	}
	if transport.closeCalls != 1 {
		t.Fatalf("transport close calls = %d, want 1", transport.closeCalls)
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

func TestMemoryConnectionManagerRetainsOnlyFailedCloses(t *testing.T) {
	closeErr := errors.New("close failed")
	failing := &fakeTransport{answer: SessionDescription{SDP: "answer-sdp", Type: "answer"}, closeErr: closeErr}
	successful := &fakeTransport{answer: SessionDescription{SDP: "answer-sdp", Type: "answer"}}
	manager := NewMemoryConnectionManager(&sequenceTransportFactory{transports: []ConnectionTransport{failing, successful}})
	first, err := manager.Open(context.Background(), validOpenConnectionRequest())
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	secondRequest := validOpenConnectionRequest()
	secondRequest.IdempotencyKey = "offer-device-2"
	if _, err := manager.Open(context.Background(), secondRequest); err != nil {
		t.Fatalf("second Open() error = %v", err)
	}

	if err := manager.Close(context.Background(), first.SessionID); !errors.Is(err, closeErr) {
		t.Fatalf("Close() error = %v, want %v", err, closeErr)
	}
	connections := manager.getSession(first.SessionID)
	if connections == nil || len(connections.byID) != 1 || connections.byID[first.ID] == nil {
		t.Fatalf("remaining connections = %#v", connections)
	}
	if successful.closeCalls != 1 || failing.closeCalls != 1 {
		t.Fatalf("close calls = successful:%d failing:%d", successful.closeCalls, failing.closeCalls)
	}

	failing.closeErr = nil
	if err := manager.Close(context.Background(), first.SessionID); err != nil {
		t.Fatalf("retry Close() error = %v", err)
	}
	if successful.closeCalls != 1 || failing.closeCalls != 2 {
		t.Fatalf("close calls after retry = successful:%d failing:%d", successful.closeCalls, failing.closeCalls)
	}
	if connections := manager.getSession(first.SessionID); connections != nil {
		t.Fatalf("connections after retry = %#v, want nil", connections)
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

func (f *sequenceTransportFactory) Create(_ context.Context, _, _ string) (ConnectionTransport, error) {
	transport := f.transports[f.calls]
	f.calls++
	return transport, nil
}

func (f *blockingTransportFactory) Create(_ context.Context, _, _ string) (ConnectionTransport, error) {
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

func (f *fakeTransportFactory) Create(_ context.Context, _, _ string) (ConnectionTransport, error) {
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
	return f.closeErr
}

var _ ConnectionTransportFactory = (*fakeTransportFactory)(nil)
var _ ConnectionTransportFactory = (*blockingTransportFactory)(nil)
var _ ConnectionTransportFactory = (*sequenceTransportFactory)(nil)
var _ ConnectionTransport = (*fakeTransport)(nil)

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
