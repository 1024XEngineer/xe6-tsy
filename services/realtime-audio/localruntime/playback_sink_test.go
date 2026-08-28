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
	if err := sink.WaitForAvailable(context.Background(), "session-1", "playback-1"); err != nil {
		t.Fatalf("WaitForAvailable(first) error = %v", err)
	}
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

	if err := sink.WaitForAvailable(context.Background(), "session-1", "playback-2"); err != nil {
		t.Fatalf("WaitForAvailable(second) error = %v", err)
	}
	if err := sink.ReleaseAvailability(context.Background(), "session-1", "playback-2"); err != nil {
		t.Fatalf("ReleaseAvailability(second) error = %v", err)
	}
	if err := sink.WaitForAvailable(context.Background(), "session-1", "playback-2"); err != nil {
		t.Fatalf("WaitForAvailable(second retry) error = %v", err)
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

func TestPlaybackAudioSinkTargetsCurrentPlayback(t *testing.T) {
	service, err := playback.NewService(playback.Dependencies{
		Track:  &playbackSinkTrack{},
		Events: playbackSinkEvents{},
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	sink := PlaybackAudioSink{Media: mediaLookupFunc(func(context.Context, string) (webrtc.MediaTransport, error) {
		return &playbackSinkMedia{service: service}, nil
	})}
	chunk := pipeline.AudioChunk{SessionID: "session-target", TurnID: "turn-1", PlaybackID: "playback-target", SequenceNo: 1, Data: []byte{1}}
	if err := sink.Publish(context.Background(), chunk); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if got := sink.CurrentPlaybackID(context.Background(), chunk.SessionID); got != chunk.PlaybackID {
		t.Fatalf("CurrentPlaybackID() = %q, want %q", got, chunk.PlaybackID)
	}
	if err := sink.InterruptPlayback(context.Background(), chunk.SessionID, chunk.PlaybackID, "mode_switch"); err != nil {
		t.Fatalf("InterruptPlayback() error = %v", err)
	}
	if got := service.Snapshot(chunk.SessionID).State; got != playback.StateInterrupted {
		t.Fatalf("state after InterruptPlayback = %q, want %q", got, playback.StateInterrupted)
	}
	if got := sink.CurrentPlaybackID(context.Background(), chunk.SessionID); got != "" {
		t.Fatalf("CurrentPlaybackID() after interrupt = %q, want empty", got)
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
