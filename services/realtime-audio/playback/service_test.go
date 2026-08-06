package playback

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
)

func TestServicePublishesOrderedPlaybackEventsAndAudio(t *testing.T) {
	track := &recordingTrack{}
	events := &recordingEvents{}
	service, err := NewService(Dependencies{Track: track, Events: events, Now: fixedClock})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	first := pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-1", PlaybackID: "playback-1", SequenceNo: 1, Data: []byte{1, 2}}
	if err := service.Publish(context.Background(), first); err != nil {
		t.Fatalf("Publish(first) error = %v", err)
	}
	first.Data[0] = 9
	duplicate := first
	duplicate.Data = []byte{1, 2}
	if err := service.Publish(context.Background(), duplicate); err != nil {
		t.Fatalf("Publish(duplicate) error = %v", err)
	}
	second := pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-1", PlaybackID: "playback-1", SequenceNo: 2, Data: []byte{3, 4}}
	if err := service.Publish(context.Background(), second); err != nil {
		t.Fatalf("Publish(second) error = %v", err)
	}
	if err := service.Complete(context.Background(), "session-1", "playback-1"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}

	if got := track.Chunks(); !reflect.DeepEqual(got, []pipeline.AudioChunk{
		{SessionID: "session-1", TurnID: "turn-1", PlaybackID: "playback-1", SequenceNo: 1, Data: []byte{1, 2}},
		second,
	}) {
		t.Fatalf("track chunks = %#v", got)
	}
	if got := events.Types(); !reflect.DeepEqual(got, []EventType{EventStarted, EventFinished}) {
		t.Fatalf("event types = %#v", got)
	}
	if got := service.Snapshot("session-1"); got.State != StateFinished || got.LastSequence != 2 {
		t.Fatalf("snapshot = %#v", got)
	}
}

func TestServiceInterruptsOnlyActivePlaybackAndIsIdempotent(t *testing.T) {
	track := &recordingTrack{}
	events := &recordingEvents{}
	service, err := NewService(Dependencies{Track: track, Events: events, Now: fixedClock})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	chunk := pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-1", PlaybackID: "playback-1", SequenceNo: 1, Data: []byte{1, 2}}
	if err := service.Publish(context.Background(), chunk); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := service.Interrupt(context.Background(), "session-1", "playback-other", "user_speaking"); !errors.Is(err, ErrPlaybackNotActive) {
		t.Fatalf("Interrupt(other) error = %v", err)
	}
	if err := service.UserSpeaking(context.Background(), "session-1"); err != nil {
		t.Fatalf("UserSpeaking() error = %v", err)
	}
	if err := service.Interrupt(context.Background(), "session-1", "playback-1", "user_speaking"); err != nil {
		t.Fatalf("Interrupt(active) error = %v", err)
	}
	if err := service.Interrupt(context.Background(), "session-1", "playback-1", "user_speaking"); err != nil {
		t.Fatalf("Interrupt(retry) error = %v", err)
	}
	if track.StopCalls() != 1 {
		t.Fatalf("track stop calls = %d, want 1", track.StopCalls())
	}
	if got := service.Snapshot("session-1"); got.State != StateInterrupted {
		t.Fatalf("snapshot = %#v", got)
	}
	if got := events.Types(); !reflect.DeepEqual(got, []EventType{EventStarted, EventInterrupted}) {
		t.Fatalf("event types = %#v", got)
	}
}

func TestServiceCancelStopsActivePlaybackAndAllowsNextPlayback(t *testing.T) {
	track := &recordingTrack{}
	service, err := NewService(Dependencies{Track: track, Events: &recordingEvents{}, Now: fixedClock})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if err := service.Publish(context.Background(), pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-1", PlaybackID: "playback-1", SequenceNo: 1, Data: []byte{1, 2}}); err != nil {
		t.Fatalf("Publish(first) error = %v", err)
	}
	if err := service.Cancel(context.Background(), "session-1", "playback-1", "turn_cancelled"); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if err := service.Cancel(context.Background(), "session-1", "playback-1", "turn_cancelled"); err != nil {
		t.Fatalf("Cancel(retry) error = %v", err)
	}
	if err := service.Publish(context.Background(), pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-2", PlaybackID: "playback-2", SequenceNo: 1, Data: []byte{3, 4}}); err != nil {
		t.Fatalf("Publish(next) error = %v", err)
	}
	if track.StopCalls() != 1 {
		t.Fatalf("track stop calls = %d, want 1", track.StopCalls())
	}
}

func TestServiceRejectsLateChunkForSettledPlayback(t *testing.T) {
	tests := []struct {
		name      string
		state     State
		eventType EventType
		settle    func(context.Context, *Service) error
	}{
		{
			name:      "finished",
			state:     StateFinished,
			eventType: EventFinished,
			settle: func(ctx context.Context, service *Service) error {
				return service.Complete(ctx, "session-1", "playback-1")
			},
		},
		{
			name:      "interrupted",
			state:     StateInterrupted,
			eventType: EventInterrupted,
			settle: func(ctx context.Context, service *Service) error {
				return service.Interrupt(ctx, "session-1", "playback-1", "user_speaking")
			},
		},
		{
			name:      "cancelled",
			state:     StateCancelled,
			eventType: EventCancelled,
			settle: func(ctx context.Context, service *Service) error {
				return service.Cancel(ctx, "session-1", "playback-1", "turn_cancelled")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			track := &recordingTrack{}
			events := &recordingEvents{}
			service, err := NewService(Dependencies{Track: track, Events: events, Now: fixedClock})
			if err != nil {
				t.Fatalf("NewService() error = %v", err)
			}
			first := pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-1", PlaybackID: "playback-1", SequenceNo: 1, Data: []byte{1, 2}}
			if err := service.Publish(context.Background(), first); err != nil {
				t.Fatalf("Publish(first) error = %v", err)
			}
			if err := tt.settle(context.Background(), service); err != nil {
				t.Fatalf("settle playback error = %v", err)
			}
			late := pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-1", PlaybackID: "playback-1", SequenceNo: 2, Data: []byte{3, 4}}
			if err := service.Publish(context.Background(), late); !errors.Is(err, ErrPlaybackNotActive) {
				t.Fatalf("Publish(late) error = %v, want ErrPlaybackNotActive", err)
			}
			if got := service.Snapshot("session-1"); got.State != tt.state || got.LastSequence != 1 {
				t.Fatalf("snapshot = %#v, want state %q and sequence 1", got, tt.state)
			}
			if got := track.Chunks(); !reflect.DeepEqual(got, []pipeline.AudioChunk{first}) {
				t.Fatalf("track chunks = %#v, want only the first chunk", got)
			}
			if got := events.Types(); !reflect.DeepEqual(got, []EventType{EventStarted, tt.eventType}) {
				t.Fatalf("event types = %#v", got)
			}
		})
	}
}

func TestServiceRetriesSettlementWhenEventPublishFails(t *testing.T) {
	track := &recordingTrack{}
	events := &recordingEvents{}
	service, err := NewService(Dependencies{Track: track, Events: events, Now: fixedClock})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	chunk := pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-1", PlaybackID: "playback-1", SequenceNo: 1, Data: []byte{1, 2}}
	if err := service.Publish(context.Background(), chunk); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	events.mu.Lock()
	events.failures = 1
	events.mu.Unlock()
	if err := service.Interrupt(context.Background(), "session-1", "playback-1", "user_speaking"); err == nil {
		t.Fatal("Interrupt() unexpectedly succeeded when event publish failed")
	}
	if got := service.Snapshot("session-1"); got.State != StatePlaying {
		t.Fatalf("snapshot after failed settlement = %#v, want playing", got)
	}
	if err := service.Publish(context.Background(), pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-1", PlaybackID: "playback-1", SequenceNo: 2, Data: []byte{3, 4}}); !errors.Is(err, ErrPlaybackNotActive) {
		t.Fatalf("Publish while settlement pending error = %v, want ErrPlaybackNotActive", err)
	}
	if err := service.Interrupt(context.Background(), "session-1", "playback-1", "user_speaking"); err != nil {
		t.Fatalf("Interrupt(retry) error = %v", err)
	}
	if got := service.Snapshot("session-1"); got.State != StateInterrupted {
		t.Fatalf("snapshot after retry = %#v", got)
	}
	if got := events.Attempts(); len(got) != 3 || got[1].EventID != got[2].EventID {
		t.Fatalf("settlement event attempts = %#v, want stable event id", got)
	}
	if track.StopCalls() != 1 {
		t.Fatalf("track stop calls = %d, want 1", track.StopCalls())
	}
}

func TestServiceRetriesSettlementWhenTrackStopFails(t *testing.T) {
	track := &recordingTrack{stopFailures: 1}
	service, err := NewService(Dependencies{Track: track, Events: &recordingEvents{}, Now: fixedClock})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	chunk := pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-1", PlaybackID: "playback-1", SequenceNo: 1, Data: []byte{1, 2}}
	if err := service.Publish(context.Background(), chunk); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := service.Interrupt(context.Background(), "session-1", "playback-1", "user_speaking"); err == nil {
		t.Fatal("Interrupt() unexpectedly succeeded when track stop failed")
	}
	if got := service.Snapshot("session-1"); got.State != StatePlaying {
		t.Fatalf("snapshot after failed stop = %#v, want playing", got)
	}
	if err := service.Interrupt(context.Background(), "session-1", "playback-1", "user_speaking"); err != nil {
		t.Fatalf("Interrupt(retry) error = %v", err)
	}
	if got := service.Snapshot("session-1"); got.State != StateInterrupted {
		t.Fatalf("snapshot after retry = %#v", got)
	}
	if track.StopCalls() != 2 {
		t.Fatalf("track stop calls = %d, want 2", track.StopCalls())
	}
}

func TestServiceFlushesCompletingTrackBeforeFinishedEvent(t *testing.T) {
	track := &completingTrack{completeFailures: 1}
	events := &recordingEvents{}
	service, err := NewService(Dependencies{Track: track, Events: events, Now: fixedClock})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	chunk := pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-1", PlaybackID: "playback-1", SequenceNo: 1, Data: []byte{1, 2}}
	if err := service.Publish(context.Background(), chunk); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if err := service.Complete(context.Background(), "session-1", "playback-1"); err == nil {
		t.Fatal("Complete() unexpectedly succeeded when track completion failed")
	}
	if got := events.Types(); !reflect.DeepEqual(got, []EventType{EventStarted}) {
		t.Fatalf("events after failed track completion = %#v", got)
	}
	if err := service.Complete(context.Background(), "session-1", "playback-1"); err != nil {
		t.Fatalf("Complete(retry) error = %v", err)
	}
	if track.CompleteCalls() != 2 {
		t.Fatalf("track complete calls = %d, want 2", track.CompleteCalls())
	}
	if got := events.Types(); !reflect.DeepEqual(got, []EventType{EventStarted, EventFinished}) {
		t.Fatalf("events after completed track = %#v", got)
	}
}

func TestServiceRetriesStartedEventAndAudioAfterInitialEventFailure(t *testing.T) {
	track := &recordingTrack{}
	events := &recordingEvents{failures: 1}
	service, err := NewService(Dependencies{Track: track, Events: events, Now: fixedClock})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	chunk := pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-1", PlaybackID: "playback-1", SequenceNo: 1, Data: []byte{1, 2}}
	if err := service.Publish(context.Background(), chunk); err == nil {
		t.Fatal("Publish() unexpectedly succeeded when started event failed")
	}
	if err := service.Publish(context.Background(), chunk); err != nil {
		t.Fatalf("Publish(retry) error = %v", err)
	}
	if got := track.Chunks(); len(got) != 1 {
		t.Fatalf("track chunks after retry = %#v, want one chunk", got)
	}
	if got := events.Attempts(); len(got) != 2 || got[0].EventID != got[1].EventID {
		t.Fatalf("started event attempts = %#v, want stable event id", got)
	}
}

func TestServiceRetriesChunkWhenTrackWriteFails(t *testing.T) {
	track := &recordingTrack{writeFailures: map[int64]int{2: 1}}
	service, err := NewService(Dependencies{Track: track, Events: &recordingEvents{}, Now: fixedClock})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	first := pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-1", PlaybackID: "playback-1", SequenceNo: 1, Data: []byte{1, 2}}
	second := pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-1", PlaybackID: "playback-1", SequenceNo: 2, Data: []byte{3, 4}}
	if err := service.Publish(context.Background(), first); err != nil {
		t.Fatalf("Publish(first) error = %v", err)
	}
	if err := service.Publish(context.Background(), second); err == nil {
		t.Fatal("Publish(second) unexpectedly succeeded when track write failed")
	}
	if got := service.Snapshot("session-1"); got.LastSequence != 1 {
		t.Fatalf("snapshot after failed chunk = %#v, want sequence 1", got)
	}
	if err := service.Publish(context.Background(), second); err != nil {
		t.Fatalf("Publish(second retry) error = %v", err)
	}
	if got := service.Snapshot("session-1"); got.LastSequence != 2 {
		t.Fatalf("snapshot after chunk retry = %#v, want sequence 2", got)
	}
	if got := track.Chunks(); !reflect.DeepEqual(got, []pipeline.AudioChunk{first, second}) {
		t.Fatalf("track chunks = %#v, want first and retried second chunk", got)
	}
}

func TestServiceRejectsSecondPlaybackWhileOneIsActive(t *testing.T) {
	service, err := NewService(Dependencies{Track: &recordingTrack{}, Events: &recordingEvents{}, Now: fixedClock})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	first := pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-1", PlaybackID: "playback-1", SequenceNo: 1, Data: []byte{1}}
	if err := service.Publish(context.Background(), first); err != nil {
		t.Fatalf("Publish(first) error = %v", err)
	}
	second := pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-2", PlaybackID: "playback-2", SequenceNo: 1, Data: []byte{2}}
	if err := service.Publish(context.Background(), second); !errors.Is(err, ErrPlaybackNotActive) {
		t.Fatalf("Publish(second) error = %v, want ErrPlaybackNotActive", err)
	}
}

func TestServiceDoesNotHoldStateLockWhilePublishingEvent(t *testing.T) {
	events := &blockingEvents{started: make(chan struct{}), release: make(chan struct{})}
	service, err := NewService(Dependencies{Track: &recordingTrack{}, Events: events, Now: fixedClock})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	chunk := pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-1", PlaybackID: "playback-1", SequenceNo: 1, Data: []byte{1, 2}}
	publishDone := make(chan error, 1)
	go func() { publishDone <- service.Publish(context.Background(), chunk) }()
	select {
	case <-events.started:
	case <-time.After(time.Second):
		t.Fatal("Publish() did not reach event sink")
	}
	snapshotDone := make(chan Snapshot, 1)
	go func() { snapshotDone <- service.Snapshot("session-1") }()
	select {
	case <-snapshotDone:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Snapshot() waited for blocked event sink")
	}
	close(events.release)
	if err := <-publishDone; err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
}

func fixedClock() time.Time { return time.Unix(1700000000, 0).UTC() }

type recordingTrack struct {
	mu            sync.Mutex
	chunks        []pipeline.AudioChunk
	stops         int
	stopFailures  int
	writeFailures map[int64]int
}

type completingTrack struct {
	recordingTrack
	completes        int
	completeFailures int
}

func (t *completingTrack) Complete(_ context.Context, _ string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.completes++
	if t.completeFailures > 0 {
		t.completeFailures--
		return errors.New("injected track completion failure")
	}
	return nil
}

func (t *completingTrack) CompleteCalls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.completes
}

func (t *recordingTrack) Write(_ context.Context, chunk pipeline.AudioChunk) error {
	t.mu.Lock()
	if remaining := t.writeFailures[chunk.SequenceNo]; remaining > 0 {
		t.writeFailures[chunk.SequenceNo] = remaining - 1
		t.mu.Unlock()
		return errors.New("injected track write failure")
	}
	chunk.Data = append([]byte(nil), chunk.Data...)
	t.chunks = append(t.chunks, chunk)
	t.mu.Unlock()
	return nil
}

func (t *recordingTrack) Stop(_ context.Context, _ string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stops++
	if t.stopFailures > 0 {
		t.stopFailures--
		return errors.New("injected track stop failure")
	}
	return nil
}

func (t *recordingTrack) Chunks() []pipeline.AudioChunk {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make([]pipeline.AudioChunk, len(t.chunks))
	copy(result, t.chunks)
	return result
}

func (t *recordingTrack) StopCalls() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stops
}

type recordingEvents struct {
	mu       sync.Mutex
	events   []Event
	attempts []Event
	failures int
}

type blockingEvents struct {
	started chan struct{}
	release chan struct{}
}

func (e *blockingEvents) Publish(context.Context, Event) error {
	close(e.started)
	<-e.release
	return nil
}

func (e *recordingEvents) Publish(_ context.Context, event Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.attempts = append(e.attempts, event)
	if e.failures > 0 {
		e.failures--
		return errors.New("injected event publish failure")
	}
	e.events = append(e.events, event)
	return nil
}

func (e *recordingEvents) Attempts() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Event(nil), e.attempts...)
}

func (e *recordingEvents) Types() []EventType {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]EventType, 0, len(e.events))
	for _, event := range e.events {
		result = append(result, event.Type)
	}
	return result
}
