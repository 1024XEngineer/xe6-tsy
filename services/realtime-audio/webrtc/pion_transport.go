package webrtc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/playback"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/segment"
	pion "github.com/pion/webrtc/v4"
)

// PionTransport owns signaling operations for one Pion PeerConnection.
type PionTransport struct {
	mu              sync.Mutex
	mediaSetupMu    sync.Mutex
	peerConnection  pionPeerConnection
	endOfCandidates bool
	closeDone       chan struct{}
	closeErr        error
	audioSource     *PionAudioSource
	wakeWords       *pionWakeWordSource
	ttsTrack        *PionAudioTrack
	events          *PionEventSink
	playback        *playback.Service
	mediaConnection pionMediaPeerConnection
	mediaConfig     MediaConfig
	mediaNow        func() time.Time
}

// WakeWordSource returns the bounded hardware-signal source owned by this
// PeerConnection. It is separate from AudioSource so command control cannot be
// mistaken for microphone media.
func (t *PionTransport) WakeWordSource() WakeWordSource {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.wakeWords == nil {
		t.wakeWords = newPionWakeWordSource(defaultWakeWordQueueCapacity)
		if t.closeDone != nil {
			t.wakeWords.closeSource()
		}
	}
	return t.wakeWords
}

// AudioSource returns the normalized inbound source, when media was enabled.
func (t *PionTransport) AudioSource() segment.FrameSource {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.audioSource
}

// TTSAudioTrack returns the outbound track writer, when media was enabled.
func (t *PionTransport) TTSAudioTrack() *PionAudioTrack {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ttsTrack
}

// TranslationEvents returns the ordered DataChannel event sink, when enabled.
func (t *PionTransport) TranslationEvents() *PionEventSink {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.events
}

// Playback returns the transport-bound playback state machine.
func (t *PionTransport) Playback() *playback.Service {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.playback
}

// Answer applies an offer and returns the fully gathered local answer.
func (t *PionTransport) Answer(ctx context.Context, offer SessionDescription) (SessionDescription, error) {
	if err := ctx.Err(); err != nil {
		return SessionDescription{}, err
	}
	if offer.SDP == "" {
		return SessionDescription{}, ErrOfferSDPRequired
	}
	if offer.Type != "offer" {
		return SessionDescription{}, ErrOfferTypeInvalid
	}
	connection, err := t.openPeerConnection()
	if err != nil {
		return SessionDescription{}, err
	}
	if err := connection.SetRemoteDescription(pion.SessionDescription{Type: pion.SDPTypeOffer, SDP: offer.SDP}); err != nil {
		return SessionDescription{}, fmt.Errorf("set remote SDP offer: %w", err)
	}
	if err := t.validateTTSAudioOffer(offer.SDP); err != nil {
		return SessionDescription{}, err
	}
	if !t.skipTTSTrack() {
		if err := configurePionTTSTrack(t); err != nil {
			return SessionDescription{}, err
		}
	}
	answer, err := connection.CreateAnswer(nil)
	if err != nil {
		return SessionDescription{}, fmt.Errorf("create local SDP answer: %w", err)
	}
	gatherComplete := connection.GatheringComplete()
	if err := connection.SetLocalDescription(answer); err != nil {
		return SessionDescription{}, fmt.Errorf("set local SDP answer: %w", err)
	}
	select {
	case <-ctx.Done():
		return SessionDescription{}, ctx.Err()
	case <-gatherComplete:
		if err := ctx.Err(); err != nil {
			return SessionDescription{}, err
		}
	}
	localDescription := connection.LocalDescription()
	if localDescription == nil {
		return SessionDescription{}, ErrAnswerSDPRequired
	}
	result := SessionDescription{SDP: localDescription.SDP, Type: localDescription.Type.String()}
	if err := validateAnswer(result); err != nil {
		return SessionDescription{}, err
	}
	return result, nil
}

func (t *PionTransport) validateTTSAudioOffer(rawSDP string) error {
	if t == nil {
		return ErrInvalidDependency
	}
	t.mu.Lock()
	mediaEnabled := t.mediaConnection != nil
	config := t.mediaConfig
	t.mu.Unlock()
	if !mediaEnabled || config.SkipTTSTrack {
		return nil
	}
	return validateTTSAudioOffer(rawSDP, config)
}

func (t *PionTransport) skipTTSTrack() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.mediaConfig.SkipTTSTrack
}

// AddCandidate passes one remote trickle candidate into Pion.
func (t *PionTransport) AddCandidate(ctx context.Context, candidate ICECandidate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if candidate.Candidate == "" {
		return ErrCandidateRequired
	}
	if t == nil {
		return ErrInvalidDependency
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.peerConnection == nil {
		return ErrInvalidDependency
	}
	if t.closeDone != nil {
		return ErrTransportClosed
	}
	return t.peerConnection.AddICECandidate(pion.ICECandidateInit{
		Candidate: candidate.Candidate, SDPMid: candidate.SDPMid,
		SDPMLineIndex: candidate.SDPMLineIndex, UsernameFragment: candidate.UsernameFragment,
	})
}

// EndCandidates sends Pion's nil remote-candidate marker once.
func (t *PionTransport) EndCandidates(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t == nil {
		return ErrInvalidDependency
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.peerConnection == nil {
		return ErrInvalidDependency
	}
	if t.closeDone != nil {
		return ErrTransportClosed
	}
	if t.endOfCandidates {
		return nil
	}
	if err := t.peerConnection.AddICECandidate(pion.ICECandidateInit{}); err != nil {
		return err
	}
	t.endOfCandidates = true
	return nil
}

// Close releases the underlying PeerConnection once and shares its result with concurrent callers.
func (t *PionTransport) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if t == nil || t.peerConnection == nil {
		return ErrInvalidDependency
	}
	t.mu.Lock()
	if t.closeDone != nil {
		done := t.closeDone
		t.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			t.mu.Lock()
			err := t.closeErr
			t.mu.Unlock()
			return err
		}
	}
	t.closeDone = make(chan struct{})
	done := t.closeDone
	connection := t.peerConnection
	t.mu.Unlock()

	t.mu.Lock()
	source := t.audioSource
	wakeWords := t.wakeWords
	events := t.events
	t.mu.Unlock()
	if wakeWords != nil {
		wakeWords.closeSource()
	}
	if events != nil {
		events.close(ErrTransportClosed)
	}
	var sourceErr error
	if source != nil {
		sourceErr = source.Close()
	}
	closeErr := errors.Join(sourceErr, connection.Close())
	t.mu.Lock()
	t.closeErr = closeErr
	close(done)
	t.mu.Unlock()
	return closeErr
}

func (t *PionTransport) openPeerConnection() (pionPeerConnection, error) {
	if t == nil {
		return nil, ErrInvalidDependency
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.peerConnection == nil {
		return nil, ErrInvalidDependency
	}
	if t.closeDone != nil {
		return nil, ErrTransportClosed
	}
	return t.peerConnection, nil
}

var _ ConnectionTransport = (*PionTransport)(nil)
var _ MediaTransport = (*PionTransport)(nil)
var _ WakeWordTransport = (*PionTransport)(nil)
