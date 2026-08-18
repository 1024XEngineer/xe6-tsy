package localruntime

import (
	"context"
	"testing"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/playback"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/segment"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/webrtc"
)

func TestPlaybackAudioSinkForwardsCompleteAndCancel(t *testing.T) {
	track := &playbackSinkTrack{}
	service, err := playback.NewService(playback.Dependencies{
		Track:  track,
		Events: playbackSinkEvents{},
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	sink := PlaybackAudioSink{Media: mediaLookupFunc(func(context.Context, string) (webrtc.MediaTransport, error) {
		return &playbackSinkMedia{service: service}, nil
	})}
	first := pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-1", PlaybackID: "playback-1", SequenceNo: 1, Data: []byte{1, 2}}
	if err := sink.Publish(context.Background(), first); err != nil {
		t.Fatalf("Publish(first) error = %v", err)
	}
	if err := sink.Complete(context.Background(), "session-1", "playback-1"); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if got := service.Snapshot("session-1").State; got != playback.StateFinished {
		t.Fatalf("state after Complete = %q, want %q", got, playback.StateFinished)
	}

	second := pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-2", PlaybackID: "playback-2", SequenceNo: 1, Data: []byte{3, 4}}
	if err := sink.Publish(context.Background(), second); err != nil {
		t.Fatalf("Publish(second) error = %v", err)
	}
	if err := sink.Cancel(context.Background(), "session-1", "playback-2", "cancelled"); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if got := service.Snapshot("session-1").State; got != playback.StateCancelled {
		t.Fatalf("state after Cancel = %q, want %q", got, playback.StateCancelled)
	}
}

func TestPlaybackAudioSinkDiscardsLateChunksAfterBargeIn(t *testing.T) {
	track := &playbackSinkTrack{}
	service, err := playback.NewService(playback.Dependencies{Track: track, Events: playbackSinkEvents{}})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	sink := PlaybackAudioSink{Media: mediaLookupFunc(func(context.Context, string) (webrtc.MediaTransport, error) {
		return &playbackSinkMedia{service: service}, nil
	})}
	first := pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-1", PlaybackID: "playback-1", SequenceNo: 1, Data: []byte{1, 2}}
	if err := sink.Publish(t.Context(), first); err != nil {
		t.Fatalf("Publish(first) error = %v", err)
	}
	if err := sink.InterruptCurrent(t.Context(), "session-1", "user_speaking"); err != nil {
		t.Fatalf("InterruptCurrent() error = %v", err)
	}
	late := first
	late.SequenceNo = 2
	if err := sink.Publish(t.Context(), late); err != nil {
		t.Fatalf("Publish(late after barge-in) error = %v, want discarded", err)
	}
	if err := sink.Complete(t.Context(), "session-1", "playback-1"); err != nil {
		t.Fatalf("Complete(interrupted) error = %v", err)
	}
	if got := service.Snapshot("session-1").State; got != playback.StateInterrupted {
		t.Fatalf("playback state = %q, want %q", got, playback.StateInterrupted)
	}
}

func TestPlaybackAudioSinkTreatsSupersededLifecycleAsComplete(t *testing.T) {
	track := &playbackSinkTrack{}
	service, err := playback.NewService(playback.Dependencies{Track: track, Events: playbackSinkEvents{}})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	sink := PlaybackAudioSink{Media: mediaLookupFunc(func(context.Context, string) (webrtc.MediaTransport, error) {
		return &playbackSinkMedia{service: service}, nil
	})}
	first := pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-1", PlaybackID: "playback-1", SequenceNo: 1, Data: []byte{1, 2}}
	if err := sink.Publish(t.Context(), first); err != nil {
		t.Fatalf("Publish(first) error = %v", err)
	}
	if err := sink.InterruptCurrent(t.Context(), "session-1", "user_speaking"); err != nil {
		t.Fatalf("InterruptCurrent() error = %v", err)
	}
	second := pipeline.AudioChunk{SessionID: "session-1", TurnID: "turn-2", PlaybackID: "playback-2", SequenceNo: 1, Data: []byte{3, 4}}
	if err := sink.Publish(t.Context(), second); err != nil {
		t.Fatalf("Publish(second) error = %v", err)
	}
	if err := sink.Complete(t.Context(), "session-1", "playback-1"); err != nil {
		t.Fatalf("Complete(superseded) error = %v", err)
	}
	if err := sink.Cancel(t.Context(), "session-1", "playback-1", "tts_stream_failed"); err != nil {
		t.Fatalf("Cancel(superseded) error = %v", err)
	}
	if got := service.Snapshot("session-1"); got.PlaybackID != "playback-2" || got.State != playback.StatePlaying {
		t.Fatalf("superseded lifecycle changed current playback: %#v", got)
	}
}

type playbackSinkTrack struct{}

func (*playbackSinkTrack) Write(context.Context, pipeline.AudioChunk) error { return nil }
func (*playbackSinkTrack) Stop(context.Context, string) error               { return nil }

type playbackSinkEvents struct{}

func (playbackSinkEvents) Publish(context.Context, playback.Event) error { return nil }

type playbackSinkMedia struct {
	service *playback.Service
}

func (*playbackSinkMedia) Answer(context.Context, webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	return webrtc.SessionDescription{}, nil
}
func (*playbackSinkMedia) AddCandidate(context.Context, webrtc.ICECandidate) error { return nil }
func (*playbackSinkMedia) EndCandidates(context.Context) error                     { return nil }
func (*playbackSinkMedia) Close(context.Context) error                             { return nil }
func (*playbackSinkMedia) AudioSource() segment.FrameSource                        { return nil }
func (*playbackSinkMedia) TTSAudioTrack() *webrtc.PionAudioTrack                   { return nil }
func (*playbackSinkMedia) TranslationEvents() *webrtc.PionEventSink                { return nil }
func (m *playbackSinkMedia) Playback() *playback.Service                           { return m.service }
