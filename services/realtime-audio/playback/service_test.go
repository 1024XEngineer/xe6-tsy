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

func fixedClock() time.Time { return time.Unix(1700000000, 0).UTC() }

type recordingTrack struct {
	mu     sync.Mutex
	chunks []pipeline.AudioChunk
	stops  int
}

func (t *recordingTrack) Write(_ context.Context, chunk pipeline.AudioChunk) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	chunk.Data = append([]byte(nil), chunk.Data...)
	t.chunks = append(t.chunks, chunk)
	return nil
}

func (t *recordingTrack) Stop(_ context.Context, _ string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stops++
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
	mu     sync.Mutex
	events []Event
}

func (e *recordingEvents) Publish(_ context.Context, event Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, event)
	return nil
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
