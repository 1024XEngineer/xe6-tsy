package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"

	recordsv1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/records/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/translate"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/tts"
)

var (
	// ErrUnsupportedSourceLanguage indicates that the captured Turn direction rejects the ASR source.
	ErrUnsupportedSourceLanguage = errors.New("unsupported source language")
	// ErrPipelineDependencyRequired indicates that a required processing boundary is missing.
	ErrPipelineDependencyRequired = errors.New("pipeline dependency is required")
)

// AudioChunk is the media-plane chunk emitted to the playback boundary.
type AudioChunk struct {
	SessionID  string
	TurnID     string
	PlaybackID string
	SequenceNo int64
	Data       []byte
}

// AudioChunkSink accepts synthesized chunks for downstream playback.
type AudioChunkSink interface {
	Publish(ctx context.Context, chunk AudioChunk) error
}

// PipelineDependencies wires provider and event boundaries for one service.
type PipelineDependencies struct {
	Translator     translate.Provider
	TTS            tts.Provider
	Speakers       recordsv1.SpeakerAttributionReader
	FinalTurns     recordsv1.FinalTurnSink
	Usage          UsageFactSink
	Audio          AudioChunkSink
	SpeakerTimeout time.Duration
	VoiceID        string
	Now            func() time.Time
}

// PipelineService orchestrates one final ASR result through translation and TTS.
type PipelineService struct {
	translator     translate.Provider
	tts            tts.Provider
	speakers       recordsv1.SpeakerAttributionReader
	finalTurns     recordsv1.FinalTurnSink
	usage          UsageFactSink
	audio          AudioChunkSink
	speakerTimeout time.Duration
	voiceID        string
	now            func() time.Time
}

// NewPipelineService creates a mock-backed translation pipeline.
func NewPipelineService(deps PipelineDependencies) *PipelineService {
	timeout := deps.SpeakerTimeout
	if timeout <= 0 {
		timeout = 50 * time.Millisecond
	}
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &PipelineService{
		translator: deps.Translator, tts: deps.TTS, speakers: deps.Speakers,
		finalTurns: deps.FinalTurns, usage: deps.Usage, audio: deps.Audio,
		speakerTimeout: timeout, voiceID: deps.VoiceID, now: now,
	}
}

// HandleASREvent ignores partial updates and handles only a final recognition result.
func (s *PipelineService) HandleASREvent(ctx context.Context, turn TurnContext, event asr.Event) error {
	if event.Type != asr.EventFinal || event.Final == nil {
		return nil
	}
	return s.HandleASRFinal(ctx, turn, *event.Final)
}

// HandleASRFinal carries one allocated Turn through all final-result stages.
func (s *PipelineService) HandleASRFinal(ctx context.Context, turn TurnContext, result asr.FinalResult) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := s.publishUsage(ctx, turn, "asr", result.Provider, result.Model, result.AudioDuration.Milliseconds(), 0, 0, result.CostAmount, result.Currency); err != nil {
		return fmt.Errorf("publish ASR usage: %w", err)
	}
	target, ok := targetLanguage(turn.LanguageConfig, result.SourceLanguage)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnsupportedSourceLanguage, result.SourceLanguage)
	}
	translationResult, err := s.translator.Translate(ctx, translate.Request{
		SessionID: turn.SessionID, TurnID: turn.ID, Text: result.Text,
		SourceLanguage: result.SourceLanguage, TargetLanguage: target,
	})
	if err != nil {
		return fmt.Errorf("translate Turn %s: %w", turn.ID, err)
	}
	if err := s.publishUsage(ctx, turn, "translation", translationResult.Provider, translationResult.Model, 0, translationResult.InputTokens, translationResult.OutputTokens, translationResult.CostAmount, translationResult.Currency); err != nil {
		return fmt.Errorf("publish translation usage: %w", err)
	}
	startedAt, endedAt := turnBounds(turn, result, s.now())
	attribution := s.resolveSpeaker(ctx, turn, result, startedAt, endedAt)
	finalEvent := FinalTurnEvent{
		EventID: "final_" + turn.ID, TraceID: turn.TraceID, SessionID: turn.SessionID, TurnID: turn.ID,
		SequenceNo: turn.SequenceNo, SourceLanguage: result.SourceLanguage, TargetLanguage: target,
		SourceText: result.Text, TranslatedText: translationResult.Text, SpeakerCode: attribution.SpeakerCode,
		SpeakerLabelSnapshot: attribution.DisplayName, SpeakerConfidence: attribution.Confidence,
		AttributionStatus: attribution.AttributionStatus, LanguageConfigVersion: turn.LanguageConfig.Version,
		StartedAt: startedAt, EndedAt: endedAt, OccurredAt: s.now(),
	}
	finalEvent.ParticipantID = attribution.ParticipantID
	if err := s.finalTurns.Publish(ctx, finalEvent); err != nil {
		return fmt.Errorf("publish FinalTurn: %w", err)
	}
	playbackID := "playback_" + turn.ID
	stream, err := s.tts.StartStream(ctx, tts.Request{SessionID: turn.SessionID, TurnID: turn.ID, PlaybackID: playbackID, Text: translationResult.Text, TargetLanguage: target, VoiceID: s.voiceID})
	if err != nil {
		return fmt.Errorf("start TTS: %w", err)
	}
	defer stream.Close()
	for chunk := range stream.Chunks() {
		if err := s.audio.Publish(ctx, AudioChunk{SessionID: turn.SessionID, TurnID: turn.ID, PlaybackID: playbackID, SequenceNo: chunk.SequenceNo, Data: append([]byte(nil), chunk.Data...)}); err != nil {
			return fmt.Errorf("publish audio chunk: %w", err)
		}
	}
	ttsResult, err := stream.Finish(ctx)
	if err != nil {
		return fmt.Errorf("finish TTS: %w", err)
	}
	if err := s.publishUsage(ctx, turn, "tts", ttsResult.Provider, ttsResult.Model, ttsResult.AudioDuration.Milliseconds(), 0, 0, ttsResult.CostAmount, ttsResult.Currency); err != nil {
		return fmt.Errorf("publish TTS usage: %w", err)
	}
	return nil
}

func (s *PipelineService) validate() error {
	if s == nil || s.translator == nil || s.tts == nil || s.finalTurns == nil || s.usage == nil || s.audio == nil {
		return ErrPipelineDependencyRequired
	}
	return nil
}

func (s *PipelineService) publishUsage(ctx context.Context, turn TurnContext, serviceType, provider, model string, durationMS, inputTokens, outputTokens int64, cost, currency string) error {
	fact := UsageFact{
		EventVersion: UsageEventVersion, ID: fmt.Sprintf("usage_%s_%s", turn.ID, serviceType),
		TraceID: turn.TraceID, IdempotencyKey: fmt.Sprintf("usage:%s:%s", turn.ID, serviceType),
		AccountID: turn.AccountID, SessionID: turn.SessionID, TurnID: turn.ID, ServiceType: serviceType,
		Provider: provider, Model: model, InputTokens: inputTokens, OutputTokens: outputTokens,
		AudioDurationMS: durationMS, CostAmount: cost, Currency: currency, OccurredAt: s.now(),
	}
	if err := fact.Validate(); err != nil {
		return fmt.Errorf("validate UsageFact: %w", err)
	}
	return s.usage.Publish(ctx, fact)
}

func (s *PipelineService) resolveSpeaker(ctx context.Context, turn TurnContext, result asr.FinalResult, startedAt, endedAt time.Time) recordsv1.SpeakerAttribution {
	if s.speakers == nil || result.ProviderSpeakerID == "" {
		return recordsv1.SpeakerAttribution{AttributionStatus: recordsv1.AttributionPending}
	}
	lookupCtx, cancel := context.WithTimeout(ctx, s.speakerTimeout)
	defer cancel()
	attribution, err := s.speakers.GetProvisionalAttribution(lookupCtx, recordsv1.SpeakerObservation{
		SessionID: turn.SessionID, TurnID: turn.ID, ProviderSpeakerID: result.ProviderSpeakerID,
		StartedAt: startedAt, EndedAt: endedAt,
		AudioStartMS: result.AudioStart.Milliseconds(), AudioEndMS: result.AudioEnd.Milliseconds(),
	})
	if err != nil {
		return recordsv1.SpeakerAttribution{AttributionStatus: recordsv1.AttributionPending}
	}
	if attribution.ParticipantID == nil {
		attribution.AttributionStatus = recordsv1.AttributionPending
	}
	if attribution.AttributionStatus == "" {
		attribution.AttributionStatus = recordsv1.AttributionPending
	}
	return attribution
}

func turnBounds(turn TurnContext, result asr.FinalResult, fallback time.Time) (time.Time, time.Time) {
	startedAt := turn.StartedAt
	if startedAt.IsZero() {
		startedAt = fallback
	}
	duration := result.AudioDuration
	if duration <= 0 && result.AudioEnd > result.AudioStart {
		duration = result.AudioEnd - result.AudioStart
	}
	return startedAt, startedAt.Add(duration)
}

func targetLanguage(config session.LanguageConfigSnapshot, source string) (string, bool) {
	for _, pair := range config.LanguagePairs {
		if pair.Source == source {
			return pair.Target, true
		}
	}
	return "", false
}
