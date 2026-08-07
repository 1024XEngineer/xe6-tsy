package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

// playTranslatedText owns the media-plane boundary from immutable translated
// text to synthesized chunks and playback lifecycle. Callers decide whether a
// failure is recoverable after the FinalTurn commit; this method never retries
// providers or publishes a second durable record.
func (s *PipelineService) playTranslatedText(ctx context.Context, turn TurnContext, targetLanguage, text, playbackID string) (tts.Result, error) {
	if s.tts == nil {
		return tts.Result{}, ErrPipelineDependencyRequired
	}
	if err := s.reportRuntime(ctx, turn, session.RuntimeTTSProcessing, playbackID); err != nil {
		return tts.Result{}, fmt.Errorf("report TTS runtime: %w", err)
	}
	ttsStartedAt := time.Now()
	stream, err := s.tts.StartStream(ctx, tts.Request{
		SessionID: turn.SessionID, TurnID: turn.ID, PlaybackID: playbackID,
		Text: text, TargetLanguage: targetLanguage, VoiceID: s.voiceID,
	})
	if err != nil {
		return tts.Result{}, fmt.Errorf("start TTS: %w", err)
	}
	s.logLatencyCheckpoint("tts_stream_started", turn, ttsStartedAt,
		"playback_id", playbackID,
		"target_language", targetLanguage,
	)
	defer stream.Close()
	played, err := s.publishTTSChunks(ctx, turn, playbackID, ttsStartedAt, stream.Chunks())
	if err != nil {
		return tts.Result{}, errors.Join(err, s.cancelPlayback(ctx, turn.SessionID, playbackID, "tts_stream_failed", played))
	}
	ttsResult, err := stream.Finish(ctx)
	if err != nil {
		return tts.Result{}, errors.Join(err, s.cancelPlayback(ctx, turn.SessionID, playbackID, "tts_finish_failed", played))
	}
	if err := s.completePlayback(ctx, turn.SessionID, playbackID, played); err != nil {
		return tts.Result{}, fmt.Errorf("complete playback: %w", err)
	}
	return ttsResult, nil
}
