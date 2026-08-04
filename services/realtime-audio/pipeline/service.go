package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	// ErrFinalTurnAccepted marks a failure after the immutable FinalTurn entered durable delivery.
	// Callers must not retry HandleASRFinal because doing so can rerun providers under the same ID.
	ErrFinalTurnAccepted = errors.New("final turn accepted")
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

// AudioPlaybackLifecycle closes the playback event sequence after chunks have started.
// It is optional so existing sinks remain valid.
type AudioPlaybackLifecycle interface {
	Complete(ctx context.Context, sessionID, playbackID string) error
	Cancel(ctx context.Context, sessionID, playbackID, reason string) error
}

// PipelineDependencies wires provider and event boundaries for one service.
type PipelineDependencies struct {
	Translator translate.Provider
	TTS        tts.Provider
	FinalTurns recordsv1.FinalTurnSink
	Usage      UsageFactSink
	Audio      AudioChunkSink
	Runtime    session.RuntimeStateReporter
	VoiceID    string
	Now        func() time.Time
}

// PipelineService orchestrates one final ASR result through translation and TTS.
type PipelineService struct {
	translator translate.Provider
	tts        tts.Provider
	finalTurns recordsv1.FinalTurnSink
	usage      UsageFactSink
	audio      AudioChunkSink
	runtime    session.RuntimeStateReporter
	voiceID    string
	now        func() time.Time
}

// NewPipelineService creates a provider-neutral Turn orchestrator. Translation,
// persistence, playback, and runtime reporting are injected dependencies, so
// this package does not depend on a vendor protocol or transport.
func NewPipelineService(deps PipelineDependencies) *PipelineService {
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &PipelineService{
		translator: deps.Translator, tts: deps.TTS,
		finalTurns: deps.FinalTurns, usage: deps.Usage, audio: deps.Audio, runtime: deps.Runtime,
		voiceID: deps.VoiceID, now: now,
	}
}

// HandleASREvent intentionally ignores partial recognition updates. Partials
// may drive UI or ASR correction, but are unstable and must not trigger
// translation, billing, TTS, or FinalTurn persistence.
func (s *PipelineService) HandleASREvent(ctx context.Context, turn TurnContext, event asr.Event) error {
	if event.Type != asr.EventFinal || event.Final == nil {
		return nil
	}
	return s.HandleASRFinal(ctx, turn, *event.Final)
}

// HandleASRFinal carries an allocated Turn through final-result stages. An
// ErrFinalTurnAccepted error means FinalTurn was durably accepted but a later
// stage failed; callers must not rerun this method, while Usage and TTS recover
// at their own processing boundaries.
func (s *PipelineService) HandleASRFinal(ctx context.Context, turn TurnContext, result asr.FinalResult) (returnErr error) {
	if err := s.validate(); err != nil {
		return err
	}
	// Runtime state is a media-plane observable fact. Report each long-running
	// stage and restore listening on every exit unless the report itself fails.
	if err := s.reportRuntime(ctx, turn, session.RuntimeTranslating, ""); err != nil {
		return fmt.Errorf("report translating runtime: %w", err)
	}
	acceptedFinalTurn := false
	defer func() {
		if err := s.reportListening(ctx, turn); err != nil {
			restoreErr := fmt.Errorf("restore listening runtime: %w", err)
			if acceptedFinalTurn {
				restoreErr = finalTurnAcceptedError("restore listening runtime", err)
			}
			returnErr = errors.Join(returnErr, restoreErr)
		}
	}()
	// ASR usage describes a completed recognition operation. Record it before
	// calling the next provider so a usage failure cannot hide an untracked call.
	if err := s.publishUsage(ctx, turn, "asr", result.Provider, result.Model, result.AudioDuration.Milliseconds(), 0, 0, result.CostAmount, result.Currency); err != nil {
		return fmt.Errorf("publish ASR usage: %w", err)
	}
	// Turn owns the versioned language snapshot captured at start. Do not reread
	// the current session config or a mid-turn change would alter this direction.
	result.SourceLanguage = asr.NormalizeLanguage(result.SourceLanguage)
	target, route, ok := targetRoute(turn.LanguageConfig, result.SourceLanguage)
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
	translationUsage, err := s.buildUsageFact(
		turn,
		"translation",
		translationResult.Provider,
		translationResult.Model,
		0,
		translationResult.InputTokens,
		translationResult.OutputTokens,
		translationResult.CostAmount,
		translationResult.Currency,
	)
	if err != nil {
		return fmt.Errorf("prepare translation usage: %w", err)
	}
	startedAt, endedAt := turnBounds(turn, result, s.now())
	var providerSpeakerID *string
	if id := strings.TrimSpace(result.ProviderSpeakerID); id != "" {
		providerSpeakerID = &id
	}
	finalEvent := FinalTurnEvent{
		EventVersion: recordsv1.FinalTurnEventVersion,
		EventID:      "final_" + turn.ID, TraceID: turn.TraceID, SessionID: turn.SessionID, TurnID: turn.ID,
		SequenceNo: turn.SequenceNo, SourceLanguage: result.SourceLanguage, TargetLanguage: target,
		SourceText: result.Text, TranslatedText: translationResult.Text, TTSEnabled: route.TTSEnabled,
		DeliveryEnabled: route.DeliveryEnabled, SpeakerCode: recordsv1.PendingSpeakerCode,
		AttributionStatus: recordsv1.AttributionPending, LanguageConfigVersion: turn.LanguageConfig.Version,
		StartedAt: startedAt, EndedAt: endedAt, OccurredAt: s.now(),
		ProviderSpeakerID: providerSpeakerID,
	}
	if err := finalEvent.Validate(); err != nil {
		return fmt.Errorf("validate FinalTurn: %w", err)
	}
	// Reliable FinalTurn publication is the turn commit point. A failure here may
	// retry the whole turn; after success the immutable body is accepted and later
	// failures must not retranslate or publish a second logical turn.
	if err := s.finalTurns.Publish(ctx, finalEvent); err != nil {
		return fmt.Errorf("publish FinalTurn: %w", err)
	}
	acceptedFinalTurn = true
	if err := s.usage.Publish(ctx, translationUsage); err != nil {
		return finalTurnAcceptedError("publish translation usage", err)
	}
	if !route.TTSEnabled {
		return nil
	}
	if s.tts == nil {
		return finalTurnAcceptedError("start TTS", ErrPipelineDependencyRequired)
	}
	// TTS runs after durable text publication. TTS or playback failures therefore
	// return ErrFinalTurnAccepted; callers may show degraded playback but must
	// not create another FinalTurn.
	playbackID := "playback_" + turn.ID
	if err := s.reportRuntime(ctx, turn, session.RuntimeTTSProcessing, playbackID); err != nil {
		return finalTurnAcceptedError("report TTS runtime", err)
	}
	stream, err := s.tts.StartStream(ctx, tts.Request{SessionID: turn.SessionID, TurnID: turn.ID, PlaybackID: playbackID, Text: translationResult.Text, TargetLanguage: target, VoiceID: s.voiceID})
	if err != nil {
		return finalTurnAcceptedError("start TTS", err)
	}
	defer stream.Close()
	played, err := s.publishTTSChunks(ctx, turn, playbackID, stream.Chunks())
	if err != nil {
		return finalTurnAcceptedError("stream TTS audio", errors.Join(err, s.cancelPlayback(ctx, turn.SessionID, playbackID, "tts_stream_failed", played)))
	}
	ttsResult, err := stream.Finish(ctx)
	if err != nil {
		return finalTurnAcceptedError("finish TTS", errors.Join(err, s.cancelPlayback(ctx, turn.SessionID, playbackID, "tts_finish_failed", played)))
	}
	if err := s.completePlayback(ctx, turn.SessionID, playbackID, played); err != nil {
		return finalTurnAcceptedError("complete playback", err)
	}
	if err := s.publishUsage(ctx, turn, "tts", ttsResult.Provider, ttsResult.Model, ttsResult.AudioDuration.Milliseconds(), 0, 0, ttsResult.CostAmount, ttsResult.Currency); err != nil {
		return finalTurnAcceptedError("publish TTS usage", err)
	}
	return nil
}

func finalTurnAcceptedError(operation string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrFinalTurnAccepted, operation, err)
}

func (s *PipelineService) publishTTSChunks(ctx context.Context, turn TurnContext, playbackID string, chunks <-chan tts.AudioChunk) (bool, error) {
	playing := false
	for {
		select {
		case <-ctx.Done():
			return playing, ctx.Err()
		case chunk, ok := <-chunks:
			if !ok {
				return playing, nil
			}
			if !playing {
				// A created TTS stream is not playing until its first real audio chunk;
				// that chunk is the externally observable playback start.
				if err := s.reportRuntime(ctx, turn, session.RuntimePlaying, playbackID); err != nil {
					return false, fmt.Errorf("report playing runtime: %w", err)
				}
				playing = true
			}
			if err := s.audio.Publish(ctx, AudioChunk{SessionID: turn.SessionID, TurnID: turn.ID, PlaybackID: playbackID, SequenceNo: chunk.SequenceNo, Data: append([]byte(nil), chunk.Data...)}); err != nil {
				return playing, fmt.Errorf("publish audio chunk: %w", err)
			}
		}
	}
}

func (s *PipelineService) completePlayback(ctx context.Context, sessionID, playbackID string, played bool) error {
	lifecycle, ok := s.audio.(AudioPlaybackLifecycle)
	if !played || !ok {
		return nil
	}
	return lifecycle.Complete(ctx, sessionID, playbackID)
}

func (s *PipelineService) cancelPlayback(ctx context.Context, sessionID, playbackID, reason string, played bool) error {
	lifecycle, ok := s.audio.(AudioPlaybackLifecycle)
	if !played || !ok {
		return nil
	}
	// Playback cleanup must continue even when the request or provider context is
	// cancelled, otherwise a failed TTS stream may keep playing. Use an
	// independent timeout to bound cleanup.
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	return lifecycle.Cancel(cleanupCtx, sessionID, playbackID, reason)
}

func (s *PipelineService) validate() error {
	if s == nil || s.translator == nil || s.finalTurns == nil || s.usage == nil || s.audio == nil || s.runtime == nil {
		return ErrPipelineDependencyRequired
	}
	return nil
}

func (s *PipelineService) publishUsage(ctx context.Context, turn TurnContext, serviceType, provider, model string, durationMS, inputTokens, outputTokens int64, cost, currency string) error {
	fact, err := s.buildUsageFact(turn, serviceType, provider, model, durationMS, inputTokens, outputTokens, cost, currency)
	if err != nil {
		return err
	}
	return s.usage.Publish(ctx, fact)
}

func (s *PipelineService) buildUsageFact(turn TurnContext, serviceType, provider, model string, durationMS, inputTokens, outputTokens int64, cost, currency string) (UsageFact, error) {
	fact := UsageFact{
		EventVersion: UsageEventVersion, ID: fmt.Sprintf("usage_%s_%s", turn.ID, serviceType),
		TraceID: turn.TraceID, IdempotencyKey: fmt.Sprintf("usage:%s:%s", turn.ID, serviceType),
		AccountID: turn.AccountID, SessionID: turn.SessionID, TurnID: turn.ID, ServiceType: serviceType,
		Provider: provider, Model: model, InputTokens: inputTokens, OutputTokens: outputTokens,
		AudioDurationMS: durationMS, CostAmount: cost, Currency: currency, OccurredAt: s.now(),
	}
	if err := fact.Validate(); err != nil {
		return UsageFact{}, fmt.Errorf("validate UsageFact: %w", err)
	}
	return fact, nil
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

func targetRoute(config session.LanguageConfigSnapshot, source string) (string, session.OutputRoute, bool) {
	source = asr.NormalizeLanguage(source)
	for _, pair := range config.LanguagePairs {
		if asr.NormalizeLanguage(pair.Source) == source {
			route := session.OutputRoute{TargetLanguage: pair.Target, TTSEnabled: true, DeliveryEnabled: false}
			for _, configured := range config.OutputRoutes {
				if configured.TargetLanguage == pair.Target {
					route = configured
					break
				}
			}
			return pair.Target, route, true
		}
	}
	return "", session.OutputRoute{}, false
}
