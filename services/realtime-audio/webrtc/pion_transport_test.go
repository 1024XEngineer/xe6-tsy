package webrtc

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	pion "github.com/pion/webrtc/v4"
)

func TestPionTransportAnswerAppliesOfferAndWaitsForGathering(t *testing.T) {
	gatherComplete := make(chan struct{})
	fake := &fakePionPeerConnection{
		answer:         pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: "answer-with-candidates"},
		gatherComplete: gatherComplete,
	}
	factory := newFakePionTransportFactory(fake)
	transport, err := factory.Create(context.Background(), "session-1", "rtc_1", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	answerDone := make(chan struct{})
	var answer SessionDescription
	var answerErr error
	go func() {
		answer, answerErr = transport.Answer(context.Background(), SessionDescription{SDP: "offer-sdp", Type: "offer"})
		close(answerDone)
	}()
	select {
	case <-answerDone:
		t.Fatal("Answer() returned before ICE gathering completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(gatherComplete)
	<-answerDone

	if answerErr != nil {
		t.Fatalf("Answer() error = %v", answerErr)
	}
	if answer != (SessionDescription{SDP: "answer-with-candidates", Type: "answer"}) {
		t.Fatalf("answer = %#v", answer)
	}
	if got, want := fake.calls, []string{"remote", "create-answer", "gather", "local", "description"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Pion calls = %#v, want %#v", got, want)
	}
	if got := fake.remoteDescription; got.Type != pion.SDPTypeOffer || got.SDP != "offer-sdp" {
		t.Fatalf("remote description = %#v", got)
	}
}

func TestPionTransportAnswerHonorsContextWhileGathering(t *testing.T) {
	fake := &fakePionPeerConnection{
		answer:         pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: "answer-sdp"},
		gatherComplete: make(chan struct{}),
		localSet:       make(chan struct{}),
	}
	transport, err := newFakePionTransportFactory(fake).Create(context.Background(), "session-1", "rtc_1", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	localSet := fake.localSet
	answerDone := make(chan error, 1)
	go func() {
		_, err := transport.Answer(ctx, SessionDescription{SDP: "offer-sdp", Type: "offer"})
		answerDone <- err
	}()
	<-localSet
	cancel()
	if err := <-answerDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("Answer() error = %v, want context.Canceled", err)
	}
}

func TestPionTransportAddsCandidatesAndEndsOnlyOnce(t *testing.T) {
	fake := &fakePionPeerConnection{gatherComplete: closedChannel()}
	transport, err := newFakePionTransportFactory(fake).Create(context.Background(), "session-1", "rtc_1", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	mid := "audio"
	line := uint16(0)
	ufrag := "ufrag"
	candidate := ICECandidate{ID: "candidate-1", Candidate: "candidate:1 1 UDP 1 127.0.0.1 9 typ host", SDPMid: &mid, SDPMLineIndex: &line, UsernameFragment: &ufrag}
	if err := transport.AddCandidate(context.Background(), candidate); err != nil {
		t.Fatalf("AddCandidate() error = %v", err)
	}
	if err := transport.EndCandidates(context.Background()); err != nil {
		t.Fatalf("first EndCandidates() error = %v", err)
	}
	if err := transport.EndCandidates(context.Background()); err != nil {
		t.Fatalf("second EndCandidates() error = %v", err)
	}
	wantCandidates := []pion.ICECandidateInit{{Candidate: candidate.Candidate, SDPMid: &mid, SDPMLineIndex: &line, UsernameFragment: &ufrag}, {}}
	if !reflect.DeepEqual(fake.candidates, wantCandidates) {
		t.Fatalf("Pion candidates = %#v, want %#v", fake.candidates, wantCandidates)
	}
}

func TestPionTransportCloseIsIdempotent(t *testing.T) {
	fake := &fakePionPeerConnection{gatherComplete: closedChannel()}
	transport, err := newFakePionTransportFactory(fake).Create(context.Background(), "session-1", "rtc_1", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := transport.Close(context.Background()); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := transport.Close(context.Background()); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if fake.closeCalls != 1 {
		t.Fatalf("Pion Close() calls = %d, want 1", fake.closeCalls)
	}
}
