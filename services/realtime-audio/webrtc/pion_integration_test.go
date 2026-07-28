//go:build integration

package webrtc

import (
	"context"
	"testing"
	"time"

	pion "github.com/pion/webrtc/v4"
)

func TestPionTransportIntegrationAppliesGatheredAnswer(t *testing.T) {
	client, err := pion.NewPeerConnection(pion.Configuration{})
	if err != nil {
		t.Fatalf("create client PeerConnection: %v", err)
	}
	defer func() { _ = client.Close() }()
	if _, err := client.AddTransceiverFromKind(pion.RTPCodecTypeAudio); err != nil {
		t.Fatalf("add audio transceiver: %v", err)
	}
	offer, err := client.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	gatherComplete := pion.GatheringCompletePromise(client)
	if err := client.SetLocalDescription(offer); err != nil {
		t.Fatalf("set client local description: %v", err)
	}
	select {
	case <-gatherComplete:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out gathering client ICE candidates")
	}
	localOffer := client.LocalDescription()
	if localOffer == nil {
		t.Fatal("client local description is nil")
	}

	factory, err := NewPionTransportFactory(PionTransportConfig{})
	if err != nil {
		t.Fatalf("create Pion transport factory: %v", err)
	}
	transport, err := factory.Create(context.Background(), "session-1", "rtc_1", nil)
	if err != nil {
		t.Fatalf("create server transport: %v", err)
	}
	defer func() { _ = transport.Close(context.Background()) }()
	answer, err := transport.Answer(context.Background(), SessionDescription{SDP: localOffer.SDP, Type: localOffer.Type.String()})
	if err != nil {
		t.Fatalf("create server answer: %v", err)
	}
	if answer.Type != "answer" || answer.SDP == "" {
		t.Fatalf("server answer = %#v", answer)
	}
	if err := client.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeAnswer, SDP: answer.SDP}); err != nil {
		t.Fatalf("apply server answer to client: %v", err)
	}
}
