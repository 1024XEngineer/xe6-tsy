package localruntime

import (
	"context"
	"errors"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/playback"
)

// PlaybackAudioSink forwards TTS PCM chunks to the session's playback service.
// When SkipTTSTrack is enabled, Playback() is nil and chunks are discarded.
type PlaybackAudioSink struct {
	Media MediaLookup
}

func (s PlaybackAudioSink) Publish(ctx context.Context, chunk pipeline.AudioChunk) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	service := s.currentPlayback(ctx, chunk.SessionID)
	if service == nil {
		return nil
	}
	err := service.Publish(ctx, chunk)
	if errors.Is(err, playback.ErrPlaybackNotActive) && service.Snapshot(chunk.SessionID).State == playback.StateInterrupted {
		// An ordinary barge-in settles the client output before the provider has
		// necessarily stopped producing PCM. Discarding those late chunks keeps
		// the committed FinalTurn successful instead of reporting cancellation as
		// a pipeline failure.
		return nil
	}
	return err
}

func (s PlaybackAudioSink) Complete(ctx context.Context, sessionID, playbackID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	service := s.currentPlayback(ctx, sessionID)
	if service == nil {
		return nil
	}
	return service.Complete(ctx, sessionID, playbackID)
}

func (s PlaybackAudioSink) Cancel(ctx context.Context, sessionID, playbackID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	service := s.currentPlayback(ctx, sessionID)
	if service == nil {
		return nil
	}
	return service.Cancel(ctx, sessionID, playbackID, reason)
}

// InterruptCurrent stops the active playback while retaining the shared
// WebRTC track. It is used by a wake-word command before command audio starts.
func (s PlaybackAudioSink) InterruptCurrent(ctx context.Context, sessionID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	service := s.currentPlayback(ctx, sessionID)
	if service == nil {
		return nil
	}
	return service.InterruptCurrent(ctx, sessionID, reason)
}

func (s PlaybackAudioSink) currentPlayback(ctx context.Context, sessionID string) *playback.Service {
	if s.Media == nil {
		return nil
	}
	media, err := s.Media.CurrentMedia(ctx, sessionID)
	if err != nil || media == nil {
		return nil
	}
	return media.Playback()
}

var _ pipeline.AudioPlaybackLifecycle = PlaybackAudioSink{}
