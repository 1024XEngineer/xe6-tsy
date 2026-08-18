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
	// ErrUnsupportedSourceLanguage rejects one Turn before translation or durable side effects.
	// Long-lived media loops may discard that Turn and continue processing later audio.
	ErrUnsupportedSourceLanguage = errors.New("unsupported source language")
	// ErrPipelineDependencyRequired indicates that a required processing boundary is missing.
	ErrPipelineDependencyRequired = errors.New("pipeline dependency is required")
	// ErrFinalTurnAccepted marks a failure after the immutable FinalTurn entered durable delivery.
	// Callers must not retry HandleASRFinal because doing so can rerun providers under the same ID.
	ErrFinalTurnAccepted = errors.New("final turn accepted")
)

// IsRecoverableUnsupportedSourceLanguage reports whether unsupported language is the only
// failure in an error chain. A joined error means cleanup or runtime-state recovery also failed
// and must be propagated instead of being discarded with the Turn.
func IsRecoverableUnsupportedSourceLanguage(err error) bool {
	for err != nil {
		if err == ErrUnsupportedSourceLanguage {
			return true
		}
		if _, joined := err.(interface{ Unwrap() []error }); joined {
			return false
		}
		wrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = wrapped.Unwrap()
	}
	return false
}

// AudioChunk is the media-plane chunk emitted to the playback boundary.
type AudioChunk struct {
	SessionID  string
	TurnID     string
	PlaybackID string
	SequenceNo int64
	Encoding   string
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

// FinalTurnCommit publishes one immutable FinalTurn while the runtime mode
// generation remains protected by its coordinator.
type FinalTurnCommit func(ctx context.Context) error

// FinalTurnCommitGate atomically validates a Turn's runtime mode snapshot and
// runs its FinalTurn publication. committed=false with a nil error means the
// Turn was superseded by a runtime restart or mode switch and must be dropped.
type FinalTurnCommitGate interface {
	CommitFinalTurn(ctx context.Context, turn TurnContext, commit FinalTurnCommit) (committed bool, err error)
}

// PipelineDependencies wires provider and event boundaries for one service.
type PipelineDependencies struct {
	Translator          translate.Provider
	TranslationProvider string
	TTS                 tts.Provider
	TTSProvider         string
	FinalTurns          recordsv1.FinalTurnSink
	FinalGate           FinalTurnCommitGate
	Usage               UsageFactSink
	Audio               AudioChunkSink
	Runtime             session.RuntimeStateReporter
	VoiceID             string
	Now                 func() time.Time
	Latency             LatencyLogger
	Speech              *SpeechOutput
	LongDeliveryEnabled bool
}

// PipelineService orchestrates one final ASR result through translation and TTS.
type PipelineService struct {
	translator          translate.Provider
	translationProvider string
	finalTurns          recordsv1.FinalTurnSink
	finalGate           FinalTurnCommitGate
	usage               UsageFactSink
	runtime             session.RuntimeStateReporter
	speech              *SpeechOutput
	now                 func() time.Time
	latency             LatencyLogger
	longDeliveryEnabled bool
}

// NewPipelineService creates a provider-neutral Turn orchestrator. Translation,
// persistence, playback, and runtime reporting are injected dependencies, so
// this package does not depend on a vendor protocol or transport.
func NewPipelineService(deps PipelineDependencies) *PipelineService {
	now := deps.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	speech := deps.Speech
	if speech == nil {
		speech = NewSpeechOutput(SpeechOutputDependencies{
			TTS: deps.TTS, Audio: deps.Audio, Runtime: deps.Runtime,
			VoiceID: deps.VoiceID, Provider: deps.TTSProvider, Latency: deps.Latency,
		})
	}
	return &PipelineService{
		translator: deps.Translator, translationProvider: deps.TranslationProvider,
		finalTurns: deps.FinalTurns, finalGate: deps.FinalGate,
		usage: deps.Usage, runtime: deps.Runtime,
		speech: speech,
		now:    now, latency: deps.Latency,
		longDeliveryEnabled: deps.LongDeliveryEnabled,
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
		if runtimeUpdateSuperseded(err) {
			return fmt.Errorf("%w: report translating runtime: %w", ErrTurnSuperseded, err)
		}
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
	// Turn owns the versioned language snapshot captured at start. Do not reread
	// the current session config or a mid-turn change would alter this direction.
	result.SourceLanguage = asr.NormalizeLanguage(result.SourceLanguage)
	target, route, ok := targetRoute(turn.LanguageConfig, result.SourceLanguage)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnsupportedSourceLanguage, result.SourceLanguage)
	}
	translateStartedAt := time.Now()
	translationResult, err := s.translator.Translate(ctx, translate.Request{
		SessionID: turn.SessionID, TurnID: turn.ID, Text: result.Text,
		SourceLanguage: result.SourceLanguage, TargetLanguage: target,
	})
	if err != nil {
		s.latency.ProviderFailure("translation", turn, observedProvider(s.translationProvider, translationResult.Provider), translationResult.Model, err)
		// Providers may still return token usage on rejected attempts. Publish
		// that consumption before failing so retries cannot hide spend.
		if usageErr := s.publishTranslationUsageIfPresent(ctx, turn, translationResult); usageErr != nil {
			return errors.Join(fmt.Errorf("translate Turn %s: %w", turn.ID, err), usageErr)
		}
		return fmt.Errorf("translate Turn %s: %w", turn.ID, err)
	}
	s.latency.ProviderCheckpoint("translate_done", turn, translateStartedAt, observedProvider(s.translationProvider, translationResult.Provider), translationResult.Model,
		"source_language", result.SourceLanguage,
		"target_language", target,
		"provider_latency_ms", translationResult.LatencyMS,
		"input_tokens", translationResult.InputTokens,
		"output_tokens", translationResult.OutputTokens,
	)
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
	longSource := s.longDeliveryEnabled && recordsv1.IsLongSourceTurn(result.Text, endedAt.Sub(startedAt))
	ttsEnabled := route.TTSEnabled && !longSource
	deliveryEnabled := route.DeliveryEnabled || longSource
	var deliveryTrigger recordsv1.FinalTurnDeliveryTrigger
	if longSource {
		deliveryTrigger = recordsv1.FinalTurnDeliveryTriggerLongSentence
	}
	var providerSpeakerID *string
	if id := strings.TrimSpace(result.ProviderSpeakerID); id != "" {
		providerSpeakerID = &id
	}
	finalEvent := FinalTurnEvent{
		EventVersion: recordsv1.FinalTurnEventVersion,
		EventID:      "final_" + turn.ID, TraceID: turn.TraceID, SessionID: turn.SessionID, TurnID: turn.ID,
		SequenceNo: turn.SequenceNo, SourceLanguage: result.SourceLanguage, TargetLanguage: target,
		SourceText: result.Text, TranslatedText: translationResult.Text, TTSEnabled: ttsEnabled,
		DeliveryEnabled: deliveryEnabled, DeliveryTrigger: deliveryTrigger, SpeakerCode: recordsv1.PendingSpeakerCode,
		AttributionStatus: recordsv1.AttributionPending, LanguageConfigVersion: turn.LanguageConfig.Version,
		StartedAt: startedAt, EndedAt: endedAt, OccurredAt: s.now(),
		ProviderSpeakerID: providerSpeakerID,
	}
	if err := finalEvent.Validate(); err != nil {
		return fmt.Errorf("validate FinalTurn: %w", err)
	}
	// Reliable FinalTurn publication is the turn commit point. The gate keeps the
	// runtime generation stable across validation and publication. A superseded
	// Turn exits normally; after a successful commit later failures must not
	// retranslate or publish a second logical turn.
	committed, err := s.finalGate.CommitFinalTurn(ctx, turn, func(commitCtx context.Context) error {
		return s.finalTurns.Publish(commitCtx, finalEvent)
	})
	if err != nil {
		return fmt.Errorf("commit FinalTurn: %w", err)
	}
	if !committed {
		// Translation already completed before the generation check. Even when a
		// mode switch supersedes the Turn and FinalTurn is dropped, its provider
		// usage remains billable and must be recorded exactly once.
		if err := s.usage.Publish(ctx, translationUsage); err != nil {
			return fmt.Errorf("publish superseded translation usage: %w", err)
		}
		return nil
	}
	acceptedFinalTurn = true
	if err := s.usage.Publish(ctx, translationUsage); err != nil {
		return finalTurnAcceptedError("publish translation usage", err)
	}
	if !ttsEnabled {
		return nil
	}
	playbackID := "playback_" + turn.ID
	ttsResult, err := s.speech.Play(ctx, SpeechOutputRequest{
		Turn: turn, Language: target, Text: translationResult.Text, PlaybackID: playbackID,
	})
	if err != nil {
		return finalTurnAcceptedError("play translated text", err)
	}
	if err := s.publishUsage(ctx, turn, "tts", ttsResult.Provider, ttsResult.Model, ttsResult.AudioDuration.Milliseconds(), 0, 0, ttsResult.CostAmount, ttsResult.Currency); err != nil {
		return finalTurnAcceptedError("publish TTS usage", err)
	}
	return nil
}

func finalTurnAcceptedError(operation string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrFinalTurnAccepted, operation, err)
}

func (s *PipelineService) logLatencyCheckpoint(stage string, turn TurnContext, since time.Time, attrs ...any) {
	if s == nil {
		return
	}
	s.latency.Checkpoint(stage, turn, since, attrs...)
}

func (s *PipelineService) validate() error {
	if s == nil || s.translator == nil || s.finalTurns == nil || s.finalGate == nil || s.usage == nil || s.runtime == nil || s.speech == nil {
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

// publishTranslationUsageIfPresent records provider spend from a failed Translate
// call when the adapter still returned usage metadata.
func (s *PipelineService) publishTranslationUsageIfPresent(ctx context.Context, turn TurnContext, result translate.Result) error {
	if strings.TrimSpace(result.Provider) == "" || strings.TrimSpace(result.Model) == "" {
		return nil
	}
	if result.InputTokens == 0 && result.OutputTokens == 0 {
		return nil
	}
	return s.publishUsage(ctx, turn, "translation", result.Provider, result.Model, 0, result.InputTokens, result.OutputTokens, result.CostAmount, result.Currency)
}

func (s *PipelineService) buildUsageFact(turn TurnContext, serviceType, provider, model string, durationMS, inputTokens, outputTokens int64, cost, currency string) (UsageFact, error) {
	return buildUsageFact(turn, serviceType, provider, model, durationMS, inputTokens, outputTokens, cost, currency, s.now())
}

func buildUsageFact(turn TurnContext, serviceType, provider, model string, durationMS, inputTokens, outputTokens int64, cost, currency string, occurredAt time.Time) (UsageFact, error) {
	fact := UsageFact{
		EventVersion: UsageEventVersion, ID: fmt.Sprintf("usage_%s_%s", turn.ID, serviceType),
		TraceID: turn.TraceID, IdempotencyKey: fmt.Sprintf("usage:%s:%s", turn.ID, serviceType),
		AccountID: turn.AccountID, SessionID: turn.SessionID, TurnID: turn.ID, ServiceType: serviceType,
		Provider: provider, Model: model, InputTokens: inputTokens, OutputTokens: outputTokens,
		AudioDurationMS: durationMS, CostAmount: cost, Currency: currency, OccurredAt: occurredAt,
	}
	if err := fact.Validate(); err != nil {
		return UsageFact{}, fmt.Errorf("validate UsageFact: %w", err)
	}
	return fact, nil
}

// BuildUsageFact creates the shared durable usage contract for non-Turn callers that reuse
// provider infrastructure, such as command confirmation speech.
func BuildUsageFact(turn TurnContext, serviceType, provider, model string, durationMS, inputTokens, outputTokens int64, cost, currency string, occurredAt time.Time) (UsageFact, error) {
	return buildUsageFact(turn, serviceType, provider, model, durationMS, inputTokens, outputTokens, cost, currency, occurredAt)
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
