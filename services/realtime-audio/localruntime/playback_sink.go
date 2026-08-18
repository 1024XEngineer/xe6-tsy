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
	if errors.Is(err, playback.ErrPlaybackNotActive) {
		// A provider may emit chunks after barge-in, while settlement is still
		// pending, or after a newer playback has taken ownership. The playback
		// service has already rejected the stale chunk; treat that rejection as
		// an idempotent cleanup result rather than failing the realtime session.
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
	err := service.Complete(ctx, sessionID, playbackID)
	if errors.Is(err, playback.ErrPlaybackNotActive) {
		return nil
	}
	return err
}

func (s PlaybackAudioSink) Cancel(ctx context.Context, sessionID, playbackID, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	service := s.currentPlayback(ctx, sessionID)
	if service == nil {
		return nil
	}
	err := service.Cancel(ctx, sessionID, playbackID, reason)
	if errors.Is(err, playback.ErrPlaybackNotActive) {
		return nil
	}
	return err
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
