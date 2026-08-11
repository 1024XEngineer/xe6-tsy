package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

type fallbackPlaybackNotStartedError struct {
	err error
}

func (e fallbackPlaybackNotStartedError) Error() string { return e.err.Error() }

func (e fallbackPlaybackNotStartedError) Unwrap() error { return e.err }

func (e fallbackPlaybackNotStartedError) FallbackPlaybackNotStarted() {}

// MarkFallbackPlaybackNotStarted marks an error that happened before local
// audio output could begin. The marker is consumed by the control plane to
// release the durable replay claim without changing the original error.
func MarkFallbackPlaybackNotStarted(err error) error {
	if err == nil {
		return nil
	}
	return fallbackPlaybackNotStartedError{err: err}
}

type speechOutputNotStartedError struct {
	err error
}

func (e speechOutputNotStartedError) Error() string { return e.err.Error() }

func (e speechOutputNotStartedError) Unwrap() error { return e.err }

// SpeechOutputDependencies are the provider-neutral media dependencies shared
// by interpretation, assistant, and durable fallback playback.
type SpeechOutputDependencies struct {
	TTS      tts.Provider
	Audio    AudioChunkSink
	Runtime  session.RuntimeStateReporter
	VoiceID  string
	Provider string
	Latency  LatencyLogger
}

// SpeechOutputRequest is immutable text accepted by the common playback path.
// The caller owns the business event and usage boundaries around this request.
type SpeechOutputRequest struct {
	Turn       TurnContext
	Language   string
	Text       string
	PlaybackID string
}

// ErrSpeechOutputRequestInvalid rejects an output request before runtime state or TTS side effects begin.
var ErrSpeechOutputRequestInvalid = errors.New("speech output request is invalid")

// SpeechOutput owns only text synthesis, audio chunks, and playback lifecycle.
// It deliberately does not publish FinalTurn, AssistantReply, or UsageFact.
type SpeechOutput struct {
	tts      tts.Provider
	audio    AudioChunkSink
	runtime  session.RuntimeStateReporter
	voiceID  string
	provider string
	latency  LatencyLogger
}

// NewSpeechOutput creates the shared output boundary used by every speaking mode.
func NewSpeechOutput(deps SpeechOutputDependencies) *SpeechOutput {
	return &SpeechOutput{
		tts: deps.TTS, audio: deps.Audio, runtime: deps.Runtime,
		voiceID: deps.VoiceID, provider: deps.Provider, latency: deps.Latency,
	}
}

// PlayFallback synthesizes one previously persisted translation without
// translating or publishing another FinalTurn.
func (s *PipelineService) PlayFallback(ctx context.Context, input FallbackPlayback) (returnErr error) {
	if s == nil || s.speech == nil || s.usage == nil || s.runtime == nil {
		return MarkFallbackPlaybackNotStarted(ErrPipelineDependencyRequired)
	}
	if input.SessionID == "" || input.TurnID == "" || input.AccountID == "" || input.TraceID == "" ||
		input.TargetLanguage == "" || input.TranslatedText == "" || input.LanguageConfigVersion < 1 || input.PlaybackID == "" {
		return MarkFallbackPlaybackNotStarted(ErrPipelineDependencyRequired)
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
	ttsResult, err := s.speech.Play(ctx, SpeechOutputRequest{
		Turn: turn, Language: input.TargetLanguage, Text: input.TranslatedText, PlaybackID: input.PlaybackID,
	})
	if err != nil {
		var notStarted speechOutputNotStartedError
		if errors.As(err, &notStarted) {
			return MarkFallbackPlaybackNotStarted(err)
		}
		return err
	}
	if err := s.publishUsage(ctx, turn, "tts", ttsResult.Provider, ttsResult.Model, ttsResult.AudioDuration.Milliseconds(), 0, 0, ttsResult.CostAmount, ttsResult.Currency); err != nil {
		return fmt.Errorf("publish TTS usage: %w", err)
	}
	return nil
}

// Play sends immutable text through synthesis and the existing realtime audio
// connection. Callers decide whether an error is recoverable after their own
// business event commit; this method never retries a provider.
func (o *SpeechOutput) Play(ctx context.Context, request SpeechOutputRequest) (tts.Result, error) {
	if err := o.validate(); err != nil {
		return tts.Result{}, speechOutputNotStartedError{err: err}
	}
	if request.Turn.SessionID == "" || request.Turn.ID == "" ||
		strings.TrimSpace(request.Language) == "" || strings.TrimSpace(request.Text) == "" ||
		strings.TrimSpace(request.PlaybackID) == "" {
		return tts.Result{}, speechOutputNotStartedError{err: ErrSpeechOutputRequestInvalid}
	}
	if err := o.reportRuntime(ctx, request.Turn, session.RuntimeTTSProcessing, request.PlaybackID); err != nil {
		return tts.Result{}, speechOutputNotStartedError{err: fmt.Errorf("report TTS runtime: %w", err)}
	}
	ttsStartedAt := time.Now()
	stream, err := o.tts.StartStream(ctx, tts.Request{
		SessionID: request.Turn.SessionID, TurnID: request.Turn.ID, PlaybackID: request.PlaybackID,
		Text: request.Text, TargetLanguage: request.Language, VoiceID: o.voiceID,
	})
	if err != nil {
		o.latency.ProviderFailure("tts_start", request.Turn, o.provider, err)
		return tts.Result{}, speechOutputNotStartedError{err: fmt.Errorf("start TTS: %w", err)}
	}
	o.latency.ProviderCheckpoint("tts_stream_started", request.Turn, ttsStartedAt, o.provider,
		"playback_id", request.PlaybackID,
		"target_language", request.Language,
	)
	defer stream.Close()
	played, err := o.publishChunks(ctx, request, ttsStartedAt, stream.Chunks())
	if err != nil {
		return tts.Result{}, errors.Join(err, o.cancelPlayback(ctx, request.Turn.SessionID, request.PlaybackID, "tts_stream_failed", played))
	}
	ttsResult, err := stream.Finish(ctx)
	if err != nil {
		o.latency.ProviderFailure("tts_finish", request.Turn, o.provider, err)
		return tts.Result{}, errors.Join(err, o.cancelPlayback(ctx, request.Turn.SessionID, request.PlaybackID, "tts_finish_failed", played))
	}
	if err := o.completePlayback(ctx, request.Turn.SessionID, request.PlaybackID, played); err != nil {
		return tts.Result{}, fmt.Errorf("complete playback: %w", err)
	}
	return ttsResult, nil
}

func (o *SpeechOutput) validate() error {
	if o == nil || o.tts == nil || o.audio == nil || o.runtime == nil {
		return ErrPipelineDependencyRequired
	}
	return nil
}

func (o *SpeechOutput) reportRuntime(ctx context.Context, turn TurnContext, state session.RuntimeState, playbackID string) error {
	turnID := turn.ID
	update := session.ProcessingStateUpdate{
		SessionID: turn.SessionID, RuntimeState: state, CurrentTurnID: &turnID,
	}
	update.CurrentPlaybackID = &playbackID
	return o.runtime.SetProcessingState(ctx, update)
}

func (o *SpeechOutput) publishChunks(ctx context.Context, request SpeechOutputRequest, startedAt time.Time, chunks <-chan tts.AudioChunk) (bool, error) {
	playing := false
	firstChunkLogged := false
	for {
		select {
		case <-ctx.Done():
			return playing, ctx.Err()
		case chunk, ok := <-chunks:
			if !ok {
				return playing, nil
			}
			if !playing {
				// A created stream becomes externally visible only with its first audio chunk.
				if err := o.reportRuntime(ctx, request.Turn, session.RuntimePlaying, request.PlaybackID); err != nil {
					return false, fmt.Errorf("report playing runtime: %w", err)
				}
				playing = true
			}
			if err := o.audio.Publish(ctx, AudioChunk{
				SessionID: request.Turn.SessionID, TurnID: request.Turn.ID, PlaybackID: request.PlaybackID,
				SequenceNo: chunk.SequenceNo, Encoding: chunk.Encoding, Data: append([]byte(nil), chunk.Data...),
			}); err != nil {
				return playing, fmt.Errorf("publish audio chunk: %w", err)
			}
			if !firstChunkLogged {
				firstChunkLogged = true
				o.latency.ProviderCheckpoint("tts_first_chunk", request.Turn, startedAt, o.provider,
					"playback_id", request.PlaybackID, "encoding", chunk.Encoding, "bytes", len(chunk.Data),
				)
			}
		}
	}
}

func (o *SpeechOutput) completePlayback(ctx context.Context, sessionID, playbackID string, played bool) error {
	lifecycle, ok := o.audio.(AudioPlaybackLifecycle)
	if !played || !ok {
		return nil
	}
	return lifecycle.Complete(ctx, sessionID, playbackID)
}

func (o *SpeechOutput) cancelPlayback(ctx context.Context, sessionID, playbackID, reason string, played bool) error {
	lifecycle, ok := o.audio.(AudioPlaybackLifecycle)
	if !played || !ok {
		return nil
	}
	// Cleanup must outlive a cancelled provider request but remains time bounded.
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	return lifecycle.Cancel(cleanupCtx, sessionID, playbackID, reason)
}
