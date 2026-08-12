package webrtc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	pion "github.com/pion/webrtc/v4"
)

func TestPionTransportReceivesWakeWordFromClientDataChannel(t *testing.T) {
	handler := &controlHandlerRecorder{}
	transport, peer := newWakeWordTransportFixture(t, handler)
	source := transport.WakeWordSource()
	if source == nil {
		t.Fatal("WakeWordSource() = nil")
	}

	control := newControlChannelRecorder(realtimev1.ControlDataChannelLabel, true)
	peer.deliverChannel(control)
	control.Receive(t, validControlModeSwitchRequest(), true)
	if response := control.NextResponse(t); response.Result == nil || handler.CallCount() != 1 {
		t.Fatalf("control response = %#v, calls = %d", response, handler.CallCount())
	}

	channel := &inboundDataChannelRecorder{label: defaultDataChannelLabel}
	peer.deliverChannel(channel)
	channel.deliver(pion.DataChannelMessage{Data: encodeWakeWordSignal(t, "wake-1"), IsString: true})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	signal, err := source.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if signal.Type != realtimev1.WakeWordDetectedType || signal.SignalID != "wake-1" {
		t.Fatalf("Receive() signal = %#v", signal)
	}
}

func TestPionWakeWordIngressIgnoresInvalidMessagesWithoutClosingMedia(t *testing.T) {
	tests := []struct {
		name    string
		message pion.DataChannelMessage
	}{
		{name: "binary", message: pion.DataChannelMessage{Data: encodeWakeWordSignal(t, "invalid"), IsString: false}},
		{name: "malformed JSON", message: pion.DataChannelMessage{Data: []byte(`{"type":`), IsString: true}},
		{name: "unknown type", message: pion.DataChannelMessage{Data: []byte(`{"type":"command.detected","event_version":1,"signal_id":"invalid","detected_at":"2023-11-14T22:13:20Z"}`), IsString: true}},
		{name: "unknown field", message: pion.DataChannelMessage{Data: []byte(`{"type":"wake_word.detected","event_version":1,"signal_id":"invalid","detected_at":"2023-11-14T22:13:20Z","command":"start"}`), IsString: true}},
		{name: "trailing JSON", message: pion.DataChannelMessage{Data: []byte(`{"type":"wake_word.detected","event_version":1,"signal_id":"invalid","detected_at":"2023-11-14T22:13:20Z"} {}`), IsString: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport, peer := newWakeWordTransportFixture(t, nil)
			channel := &inboundDataChannelRecorder{label: defaultDataChannelLabel}
			peer.deliverChannel(channel)
			channel.deliver(test.message)
			channel.deliver(pion.DataChannelMessage{Data: encodeWakeWordSignal(t, "valid"), IsString: true})

			signal, err := transport.WakeWordSource().Receive(context.Background())
			if err != nil || signal.SignalID != "valid" {
				t.Fatalf("Receive() = (%#v, %v), invalid message must be ignored", signal, err)
			}
			if peer.closeCalls != 0 {
				t.Fatalf("PeerConnection Close() calls = %d, want 0", peer.closeCalls)
			}
		})
	}
}

func TestPionWakeWordIngressIgnoresOversizedMessage(t *testing.T) {
	transport, peer := newWakeWordTransportFixture(t, nil)
	channel := &inboundDataChannelRecorder{label: defaultDataChannelLabel}
	peer.deliverChannel(channel)
	channel.deliver(pion.DataChannelMessage{
		Data:     []byte(strings.Repeat("x", maxWakeWordDataChannelMessageBytes+1)),
		IsString: true,
	})
	channel.deliver(pion.DataChannelMessage{Data: encodeWakeWordSignal(t, "valid"), IsString: true})

	signal, err := transport.WakeWordSource().Receive(context.Background())
	if err != nil || signal.SignalID != "valid" {
		t.Fatalf("Receive() = (%#v, %v), oversized message must be ignored", signal, err)
	}
	if peer.closeCalls != 0 {
		t.Fatalf("PeerConnection Close() calls = %d, want 0", peer.closeCalls)
	}
}

func TestPionWakeWordIngressDropsWhenQueueIsFull(t *testing.T) {
	source := newPionWakeWordSource(1)
	channel := &inboundDataChannelRecorder{label: defaultDataChannelLabel}
	attachPionWakeWordIngress(source, channel)

	channel.deliver(pion.DataChannelMessage{Data: encodeWakeWordSignal(t, "first"), IsString: true})
	// The second callback must return immediately even though no consumer has
	// drained the only queue slot.
	channel.deliver(pion.DataChannelMessage{Data: encodeWakeWordSignal(t, "second"), IsString: true})

	signal, err := source.Receive(context.Background())
	if err != nil || signal.SignalID != "first" {
		t.Fatalf("Receive() = (%#v, %v), want first signal", signal, err)
	}
	if queued := len(source.signals); queued != 0 {
		t.Fatalf("queued signals = %d, saturated callback must drop second signal", queued)
	}
}

func TestPionWakeWordSourceReceiveHonorsCancellation(t *testing.T) {
	source := newPionWakeWordSource(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := source.Receive(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Receive() error = %v, want context.Canceled", err)
	}
}

func TestPionWakeWordSourceClosesWithTransport(t *testing.T) {
	transport, _ := newWakeWordTransportFixture(t, nil)
	source := transport.WakeWordSource()
	if err := transport.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	_, err := source.Receive(context.Background())
	if !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("Receive() error = %v, want ErrTransportClosed", err)
	}
}

func newWakeWordTransportFixture(t *testing.T, handler ControlCommandHandler) (*PionTransport, *inboundMediaPeerRecorder) {
	t.Helper()
	peer := &inboundMediaPeerRecorder{
		mediaPeerRecorder: &mediaPeerRecorder{fakePionPeerConnection: &fakePionPeerConnection{gatherComplete: closedChannel()}},
	}
	factory := &PionTransportFactory{
		newPeerConnection: func(pion.Configuration) (pionPeerConnection, error) { return peer, nil },
		now:               func() time.Time { return time.Now().UTC() },
		control:           ControlConfig{Handler: handler},
	}
	created, err := factory.Create(context.Background(), "session-1", "rtc-1", nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	transport, ok := created.(*PionTransport)
	if !ok {
		t.Fatalf("Create() transport = %T, want *PionTransport", created)
	}
	t.Cleanup(func() { _ = transport.Close(context.Background()) })
	return transport, peer
}

func encodeWakeWordSignal(t *testing.T, signalID string) []byte {
	t.Helper()
	payload, err := json.Marshal(realtimev1.WakeWordDetectedSignal{
		Type:         realtimev1.WakeWordDetectedType,
		EventVersion: realtimev1.WakeWordDetectedEventVersion,
		SignalID:     signalID,
		DetectedAt:   time.Unix(1700000000, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return payload
}

type inboundDataChannelRecorder struct {
	label     string
	onMessage func(pion.DataChannelMessage)
}

func (c *inboundDataChannelRecorder) Label() string { return c.label }
func (c *inboundDataChannelRecorder) OnMessage(handler func(pion.DataChannelMessage)) {
	c.onMessage = handler
}
func (c *inboundDataChannelRecorder) deliver(message pion.DataChannelMessage) {
	if c.onMessage != nil {
		c.onMessage(message)
	}
}

type inboundMediaPeerRecorder struct {
	*mediaPeerRecorder
	onInbound func(pionInboundDataChannel)
}

func (p *inboundMediaPeerRecorder) OnDataChannel(handler func(pionInboundDataChannel)) {
	p.onInbound = handler
}
func (p *inboundMediaPeerRecorder) deliverChannel(channel pionInboundDataChannel) {
	if p.onInbound != nil {
		p.onInbound(channel)
	}
}

var _ pionInboundDataChannel = (*inboundDataChannelRecorder)(nil)
var _ pionInboundDataChannelPeerConnection = (*inboundMediaPeerRecorder)(nil)
var _ pionMediaPeerConnection = (*inboundMediaPeerRecorder)(nil)
