package localruntime

import (
	"context"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/pipeline"
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
	if s.Media == nil {
		return nil
	}
	media, err := s.Media.CurrentMedia(ctx, chunk.SessionID)
	if err != nil {
		return nil
	}
	service := media.Playback()
	if service == nil {
		return nil
	}
	return service.Publish(ctx, chunk)
}
