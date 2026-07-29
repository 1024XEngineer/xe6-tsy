//go:build integration

package webrtc

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/playback"
	"github.com/pion/rtp"
	pion "github.com/pion/webrtc/v4"
)

func TestPionTransportIntegrationAppliesGatheredAnswer(t *testing.T) {
	api, err := newPionAPI(MediaConfig{})
	if err != nil {
		t.Fatalf("create client Pion API: %v", err)
	}
	client, err := api.NewPeerConnection(pion.Configuration{})
	if err != nil {
		t.Fatalf("create client PeerConnection: %v", err)
	}
	defer func() { _ = client.Close() }()
	connected := make(chan struct{})
	var connectedOnce sync.Once
	client.OnConnectionStateChange(func(state pion.PeerConnectionState) {
		if state == pion.PeerConnectionStateConnected {
			connectedOnce.Do(func() { close(connected) })
		}
	})
	downstream := make(chan *rtp.Packet, 1)
	client.OnTrack(func(track *pion.TrackRemote, _ *pion.RTPReceiver) {
		packet, _, readErr := track.ReadRTP()
		if readErr == nil {
			downstream <- packet
		}
	})
	events := make(chan playback.Event, 2)
	client.OnDataChannel(func(channel *pion.DataChannel) {
		if channel.Label() != defaultDataChannelLabel {
			return
		}
		channel.OnMessage(func(message pion.DataChannelMessage) {
			var event playback.Event
			if json.Unmarshal(message.Data, &event) == nil {
				events <- event
			}
		})
	})
	microphone, err := pion.NewTrackLocalStaticRTP(pion.RTPCodecCapability{
		MimeType: pion.MimeTypeOpus, ClockRate: 48_000, Channels: 2,
	}, "microphone", "client")
	if err != nil {
		t.Fatalf("create client audio track: %v", err)
	}
	if _, err := client.AddTransceiverFromTrack(microphone, pion.RTPTransceiverInit{Direction: pion.RTPTransceiverDirectionSendonly}); err != nil {
		t.Fatalf("add client send audio transceiver: %v", err)
	}
	if _, err := client.AddTransceiverFromKind(pion.RTPCodecTypeAudio, pion.RTPTransceiverInit{Direction: pion.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatalf("add client receive audio transceiver: %v", err)
	}
	if _, err := client.CreateDataChannel("client-control", nil); err != nil {
		t.Fatalf("create client DataChannel: %v", err)
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
	select {
	case <-connected:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out connecting local PeerConnections")
	}

	mediaTransport := transport.(*PionTransport)
	if err := microphone.WriteRTP(&rtp.Packet{
		Header:  rtp.Header{Version: 2, SequenceNumber: 1, Timestamp: 960},
		Payload: []byte{0xf8, 0xff, 0xfe},
	}); err != nil {
		t.Fatalf("write upstream Opus: %v", err)
	}
	readCtx, cancelRead := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRead()
	frame, err := mediaTransport.AudioSource().ReadFrame(readCtx)
	if err != nil || len(frame.PCM) == 0 || frame.SampleRate != 16_000 {
		t.Fatalf("upstream frame = %#v, error = %v", frame, err)
	}

	playCtx, cancelPlay := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPlay()
	chunk := pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-1", PlaybackID: "playback-1", SequenceNo: 1, Data: []byte{1, 2, 3, 4}}
	if err := mediaTransport.Playback().Publish(playCtx, chunk); err != nil {
		t.Fatalf("publish downstream playback: %v", err)
	}
	select {
	case packet := <-downstream:
		if !reflect.DeepEqual(packet.Payload, []byte{2, 1, 4, 3}) {
			t.Fatalf("downstream L16 payload = %#v", packet.Payload)
		}
	case <-playCtx.Done():
		t.Fatal("timed out reading downstream TTS RTP")
	}
	if err := mediaTransport.Playback().Complete(playCtx, "session-1", "playback-1"); err != nil {
		t.Fatalf("complete playback: %v", err)
	}
	for _, want := range []playback.EventType{playback.EventStarted, playback.EventFinished} {
		select {
		case event := <-events:
			if event.Type != want || event.PlaybackID != "playback-1" {
				t.Fatalf("translation event = %#v, want %q", event, want)
			}
		case <-playCtx.Done():
			t.Fatalf("timed out reading %s event", want)
		}
	}
}
