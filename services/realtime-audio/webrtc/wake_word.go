package webrtc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"sync"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	pion "github.com/pion/webrtc/v4"
)

const (
	defaultWakeWordQueueCapacity       = 8
	maxWakeWordDataChannelMessageBytes = 4 * 1024
)

// WakeWordSource exposes validated local detections outside the normal audio track.
type WakeWordSource interface {
	Receive(ctx context.Context) (realtimev1.WakeWordDetectedSignal, error)
}

// WakeWordTransport keeps inbound controls optional for signaling-only transports.
type WakeWordTransport interface {
	WakeWordSource() WakeWordSource
}

type pionWakeWordSource struct {
	signals chan realtimev1.WakeWordDetectedSignal
	done    chan struct{}
	close   sync.Once
	stateMu sync.Mutex
	closed  bool
}

func newPionWakeWordSource(capacity int) *pionWakeWordSource {
	if capacity < 1 {
		capacity = defaultWakeWordQueueCapacity
	}
	return &pionWakeWordSource{signals: make(chan realtimev1.WakeWordDetectedSignal, capacity), done: make(chan struct{})}
}

// Receive waits for one signal or the caller/transport lifetime; the source owns no goroutine.
func (s *pionWakeWordSource) Receive(ctx context.Context) (realtimev1.WakeWordDetectedSignal, error) {
	if s == nil {
		return realtimev1.WakeWordDetectedSignal{}, ErrMediaUnavailable
	}
	if err := ctx.Err(); err != nil {
		return realtimev1.WakeWordDetectedSignal{}, err
	}
	select {
	case <-ctx.Done():
		return realtimev1.WakeWordDetectedSignal{}, ctx.Err()
	case <-s.done:
		return realtimev1.WakeWordDetectedSignal{}, ErrTransportClosed
	case signal := <-s.signals:
		s.stateMu.Lock()
		closed := s.closed
		s.stateMu.Unlock()
		if closed {
			return realtimev1.WakeWordDetectedSignal{}, ErrTransportClosed
		}
		return signal, nil
	}
}

// offer drops saturated edge notifications instead of blocking Pion's callback.
func (s *pionWakeWordSource) offer(signal realtimev1.WakeWordDetectedSignal) bool {
	if s == nil {
		return false
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.closed {
		return false
	}
	select {
	case s.signals <- signal:
		return true
	default:
		return false
	}
}

func (s *pionWakeWordSource) closeSource() {
	if s != nil {
		s.close.Do(func() {
			s.stateMu.Lock()
			s.closed = true
			close(s.done)
			s.stateMu.Unlock()
		})
	}
}

type pionInboundDataChannel interface {
	Label() string
	OnMessage(func(pion.DataChannelMessage))
}

// pionInboundDataChannelPeerConnection shares one callback across inbound protocols.
type pionInboundDataChannelPeerConnection interface {
	OnDataChannel(func(pionInboundDataChannel))
}

func attachPionWakeWordIngress(source *pionWakeWordSource, channel pionInboundDataChannel) {
	if source == nil || channel == nil {
		return
	}
	channel.OnMessage(func(message pion.DataChannelMessage) {
		signal, ok := decodeWakeWordDataChannelMessage(message)
		if ok {
			source.offer(signal)
		}
	})
}

func decodeWakeWordDataChannelMessage(message pion.DataChannelMessage) (realtimev1.WakeWordDetectedSignal, bool) {
	if !message.IsString || len(message.Data) == 0 || len(message.Data) > maxWakeWordDataChannelMessageBytes {
		return realtimev1.WakeWordDetectedSignal{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(message.Data))
	decoder.DisallowUnknownFields()
	var signal realtimev1.WakeWordDetectedSignal
	if err := decoder.Decode(&signal); err != nil {
		return realtimev1.WakeWordDetectedSignal{}, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return realtimev1.WakeWordDetectedSignal{}, false
	}
	if err := signal.Validate(); err != nil {
		return realtimev1.WakeWordDetectedSignal{}, false
	}
	return signal, true
}

var _ WakeWordSource = (*pionWakeWordSource)(nil)
