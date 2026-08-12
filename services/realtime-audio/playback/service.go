package playback

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
)

var (
	ErrDependencyRequired = errors.New("playback dependency is required")
	ErrSessionRequired    = errors.New("playback session id is required")
	ErrPlaybackRequired   = errors.New("playback id is required")
	ErrPlaybackNotActive  = errors.New("playback is not active")
	ErrSequenceInvalid    = errors.New("playback sequence is invalid")
)

// State is the local lifecycle of one synthesized playback.
type State string

const (
	StateIdle        State = "idle"
	StatePlaying     State = "playing"
	StateFinished    State = "finished"
	StateInterrupted State = "interrupted"
	StateCancelled   State = "cancelled"
)

// EventType is serialized to the translation-events DataChannel by an adapter.
type EventType string

const (
	EventStarted     EventType = "playback.started"
	EventFinished    EventType = "playback.finished"
	EventInterrupted EventType = "playback.interrupted"
	EventCancelled   EventType = "playback.cancelled"
)

// Event is the ordered local event contract shared by playback and transport adapters.
type Event struct {
	EventID    string    `json:"event_id"`
	Type       EventType `json:"type"`
	SessionID  string    `json:"session_id"`
	TurnID     string    `json:"turn_id"`
	PlaybackID string    `json:"playback_id"`
	SequenceNo int64     `json:"sequence_no"`
	Reason     string    `json:"reason,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

// EventSink is intentionally transport-neutral; the WebRTC adapter owns JSON/DataChannel details.
type EventSink interface {
	Publish(ctx context.Context, event Event) error
}

// AudioTrack accepts one ordered TTS chunk and can stop only the active playback.
type AudioTrack interface {
	Write(ctx context.Context, chunk pipeline.AudioChunk) error
	Stop(ctx context.Context, playbackID string) error
}

type audioTrackCompleter interface {
	Complete(ctx context.Context, playbackID string) error
}

// Dependencies wires the state machine to a media track and an event publisher.
type Dependencies struct {
	Track  AudioTrack
	Events EventSink
	Now    func() time.Time
}

// Snapshot is a read-only view of one session's active or settled playback.
type Snapshot struct {
	SessionID    string
	TurnID       string
	PlaybackID   string
	State        State
	LastSequence int64
}

type Service struct {
	mu      sync.Mutex
	track   AudioTrack
	events  EventSink
	now     func() time.Time
	current map[string]*playback
}

type playback struct {
	opMu       sync.Mutex
	snapshot   Snapshot
	eventSeq   int64
	started    *startedPlayback
	settlement *settlement
}

type startedPlayback struct {
	event      Event
	sequenceNo int64
	eventSent  bool
	audioSent  bool
}

type settlement struct {
	state        State
	event        Event
	stop         bool
	eventSent    bool
	trackStopped bool
}

// NewService creates an isolated playback state machine.
func NewService(deps Dependencies) (*Service, error) {
	if deps.Track == nil || deps.Events == nil {
		return nil, ErrDependencyRequired
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{track: deps.Track, events: deps.Events, now: deps.Now, current: make(map[string]*playback)}, nil
}

// Publish writes one TTS chunk and starts playback on the first chunk.
func (s *Service) Publish(ctx context.Context, chunk pipeline.AudioChunk) error {
	if err := validateChunk(chunk); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	current := s.current[chunk.SessionID]
	if current != nil {
		if current.snapshot.PlaybackID == chunk.PlaybackID {
			if current.snapshot.State != StatePlaying {
				s.mu.Unlock()
				return ErrPlaybackNotActive
			}
		} else if current.snapshot.State == StatePlaying {
			s.mu.Unlock()
			return ErrPlaybackNotActive
		}
	}
	if current == nil || current.snapshot.State != StatePlaying {
		current = &playback{snapshot: Snapshot{SessionID: chunk.SessionID, TurnID: chunk.TurnID, PlaybackID: chunk.PlaybackID, State: StatePlaying}}
		s.current[chunk.SessionID] = current
	}
	s.mu.Unlock()

	current.opMu.Lock()
	defer current.opMu.Unlock()
	s.mu.Lock()
	if current.snapshot.PlaybackID != chunk.PlaybackID || current.snapshot.State != StatePlaying {
		s.mu.Unlock()
		return ErrPlaybackNotActive
	}
	if current.settlement != nil {
		s.mu.Unlock()
		return ErrPlaybackNotActive
	}
	if chunk.SequenceNo <= current.snapshot.LastSequence {
		s.mu.Unlock()
		return nil
	}
	if current.started != nil && current.started.sequenceNo != chunk.SequenceNo {
		s.mu.Unlock()
		return ErrSequenceInvalid
	}
	if current.snapshot.TurnID == "" {
		current.snapshot.TurnID = chunk.TurnID
	}
	if current.snapshot.LastSequence == 0 {
		if current.started == nil {
			current.eventSeq++
			current.started = &startedPlayback{event: s.eventLocked(current, EventStarted, ""), sequenceNo: chunk.SequenceNo}
		}
		started := current.started
		needEvent := !started.eventSent
		s.mu.Unlock()
		if needEvent {
			if err := s.events.Publish(ctx, started.event); err != nil {
				return fmt.Errorf("publish playback started: %w", err)
			}
			s.mu.Lock()
			started.eventSent = true
			s.mu.Unlock()
		}
		if !started.audioSent {
			if err := s.track.Write(ctx, chunk); err != nil {
				return fmt.Errorf("write TTS audio: %w", err)
			}
			s.mu.Lock()
			started.audioSent = true
			current.snapshot.LastSequence = chunk.SequenceNo
			current.started = nil
			s.mu.Unlock()
		}
		return nil
	}
	s.mu.Unlock()

	if err := s.track.Write(ctx, chunk); err != nil {
		return fmt.Errorf("write TTS audio: %w", err)
	}
	s.mu.Lock()
	current.snapshot.LastSequence = chunk.SequenceNo
	s.mu.Unlock()
	return nil
}

// Complete marks the active playback finished. It is safe to retry after completion.
func (s *Service) Complete(ctx context.Context, sessionID, playbackID string) error {
	return s.settle(ctx, sessionID, playbackID, StateFinished, EventFinished, "", false)
}

// Interrupt stops only the requested active playback and keeps the track open.
func (s *Service) Interrupt(ctx context.Context, sessionID, playbackID, reason string) error {
	return s.settle(ctx, sessionID, playbackID, StateInterrupted, EventInterrupted, reason, true)
}

// Cancel stops only the requested active playback after a provider or Turn cancellation.
func (s *Service) Cancel(ctx context.Context, sessionID, playbackID, reason string) error {
	return s.settle(ctx, sessionID, playbackID, StateCancelled, EventCancelled, reason, true)
}

// UserSpeaking interrupts the current active playback for a session.
func (s *Service) UserSpeaking(ctx context.Context, sessionID string) error {
	return s.InterruptCurrent(ctx, sessionID, "user_speaking")
}

// InterruptCurrent stops the active playback without closing the shared media track.
// Callers supply a stable reason so command wake-up and ordinary barge-in remain
// distinguishable in the ordered playback event stream.
func (s *Service) InterruptCurrent(ctx context.Context, sessionID, reason string) error {
	if sessionID == "" {
		return ErrSessionRequired
	}
	s.mu.Lock()
	current := s.current[sessionID]
	var playbackID string
	if current != nil && current.snapshot.State == StatePlaying {
		playbackID = current.snapshot.PlaybackID
	}
	s.mu.Unlock()
	if playbackID == "" {
		return nil
	}
	return s.Interrupt(ctx, sessionID, playbackID, reason)
}

// Snapshot returns the last known playback state for a session.
func (s *Service) Snapshot(sessionID string) Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current := s.current[sessionID]; current != nil {
		return current.snapshot
	}
	return Snapshot{SessionID: sessionID, State: StateIdle}
}

func (s *Service) settle(ctx context.Context, sessionID, playbackID string, state State, eventType EventType, reason string, stop bool) error {
	if sessionID == "" {
		return ErrSessionRequired
	}
	if playbackID == "" {
		return ErrPlaybackRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	current := s.current[sessionID]
	if current == nil || current.snapshot.PlaybackID != playbackID {
		s.mu.Unlock()
		return ErrPlaybackNotActive
	}
	s.mu.Unlock()

	current.opMu.Lock()
	defer current.opMu.Unlock()
	s.mu.Lock()
	if current.snapshot.PlaybackID != playbackID {
		s.mu.Unlock()
		return ErrPlaybackNotActive
	}
	if current.settlement != nil && current.settlement.event.Type != eventType {
		s.mu.Unlock()
		return ErrPlaybackNotActive
	}
	if current.settlement == nil && current.snapshot.State != StatePlaying {
		s.mu.Unlock()
		return nil
	}
	if current.settlement == nil {
		current.eventSeq++
		current.settlement = &settlement{
			state: state, event: s.eventLocked(current, eventType, reason), stop: stop,
		}
	}
	pending := current.settlement
	s.mu.Unlock()
	if !pending.stop && !pending.trackStopped {
		if completer, ok := s.track.(audioTrackCompleter); ok {
			if err := completer.Complete(ctx, playbackID); err != nil {
				return fmt.Errorf("complete playback track: %w", err)
			}
		}
		s.mu.Lock()
		pending.trackStopped = true
		s.mu.Unlock()
	}
	if !pending.eventSent {
		if err := s.events.Publish(ctx, pending.event); err != nil {
			return fmt.Errorf("publish playback settlement: %w", err)
		}
		s.mu.Lock()
		pending.eventSent = true
		s.mu.Unlock()
	}
	if pending.stop && !pending.trackStopped {
		if err := s.track.Stop(ctx, playbackID); err != nil {
			return fmt.Errorf("stop playback: %w", err)
		}
		s.mu.Lock()
		pending.trackStopped = true
		s.mu.Unlock()
	}
	s.mu.Lock()
	if pending.eventSent && pending.trackStopped {
		current.snapshot.State = pending.state
		current.settlement = nil
	}
	s.mu.Unlock()
	return nil
}

func (s *Service) eventLocked(current *playback, eventType EventType, reason string) Event {
	return Event{
		EventID: fmt.Sprintf("%s_%s_%d", current.snapshot.PlaybackID, eventType, current.eventSeq),
		Type:    eventType, SessionID: current.snapshot.SessionID, TurnID: current.snapshot.TurnID,
		PlaybackID: current.snapshot.PlaybackID, SequenceNo: current.eventSeq, Reason: reason, OccurredAt: s.now(),
	}
}

func validateChunk(chunk pipeline.AudioChunk) error {
	switch {
	case chunk.SessionID == "":
		return ErrSessionRequired
	case chunk.PlaybackID == "":
		return ErrPlaybackRequired
	case chunk.SequenceNo <= 0:
		return ErrSequenceInvalid
	case len(chunk.Data) == 0:
		return errors.New("playback audio data is required")
	default:
		return nil
	}
}
