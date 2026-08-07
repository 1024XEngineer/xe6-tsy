package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

// FallbackPlayback contains the immutable translation snapshot accepted from
// the control plane after automatic delivery failed.
type FallbackPlayback struct {
	SessionID             string
	TurnID                string
	AccountID             string
	TraceID               string
	TargetLanguage        string
	TranslatedText        string
	LanguageConfigVersion int64
	PlaybackID            string
}

// PlayFallback synthesizes one previously persisted translation without
// translating or publishing another FinalTurn.
func (s *PipelineService) PlayFallback(ctx context.Context, input FallbackPlayback) (returnErr error) {
	if s == nil || s.tts == nil || s.usage == nil || s.audio == nil || s.runtime == nil {
		return ErrPipelineDependencyRequired
	}
	if input.SessionID == "" || input.TurnID == "" || input.AccountID == "" || input.TraceID == "" ||
		input.TargetLanguage == "" || input.TranslatedText == "" || input.LanguageConfigVersion < 1 || input.PlaybackID == "" {
		return ErrPipelineDependencyRequired
	}
	turn := TurnContext{
		ID: input.TurnID, SessionID: input.SessionID, AccountID: input.AccountID,
		TraceID: input.TraceID,
		LanguageConfig: session.LanguageConfigSnapshot{
			SessionID: input.SessionID, Version: input.LanguageConfigVersion,
		},
	}
	defer func() {
		if err := s.reportListening(ctx, turn); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("restore listening runtime: %w", err))
		}
	}()
	ttsResult, err := s.playTranslatedText(ctx, turn, input.TargetLanguage, input.TranslatedText, input.PlaybackID)
	if err != nil {
		return err
	}
	if err := s.publishUsage(ctx, turn, "tts", ttsResult.Provider, ttsResult.Model, ttsResult.AudioDuration.Milliseconds(), 0, 0, ttsResult.CostAmount, ttsResult.Currency); err != nil {
		return fmt.Errorf("publish TTS usage: %w", err)
	}
	return nil
}

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
