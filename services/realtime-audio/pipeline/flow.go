package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/session"
)

var (
	// ErrTurnProcessorDependencyRequired indicates that the ASR-to-pipeline boundary is incomplete.
	ErrTurnProcessorDependencyRequired = errors.New("turn processor dependency is required")
	// ErrASRStreamRequired rejects a provider that did not return an owned stream.
	ErrASRStreamRequired = errors.New("ASR stream is required")
	// ErrASRFinalRequired rejects a stream that completed without a usable final result.
	ErrASRFinalRequired = errors.New("ASR final result is required")
	// ErrDuplicateASRFinal rejects a stream that emits more than one final result for a Turn.
	ErrDuplicateASRFinal = errors.New("duplicate ASR final result")
)

// TurnProcessRequest contains the audio and immutable metadata for one member-3 Turn.
type TurnProcessRequest struct {
	SessionID      string
	AccountID      string
	TraceID        string
	SourceLanguage string
	StartedAt      time.Time
	AudioChunks    [][]byte
	// Generation identifies the ingress mode snapshot that produced this
	// request. A stale queued VAD final must not cross a mode boundary.
	Generation uint64
}

// TurnProcessor connects ASR stream completion to the existing Turn and translation pipeline.
type TurnProcessor struct {
	recognizer asr.Provider
	opener     *TurnOpener
	pipeline   *PipelineService
}

// TurnProcessorDependencies wires the offline-capable ASR-to-pipeline flow.
type TurnProcessorDependencies struct {
	ASR      asr.Provider
	Opener   *TurnOpener
	Pipeline *PipelineService
}

// NewTurnProcessor creates a processor for one complete audio Turn.
func NewTurnProcessor(deps TurnProcessorDependencies) *TurnProcessor {
	return &TurnProcessor{recognizer: deps.ASR, opener: deps.Opener, pipeline: deps.Pipeline}
}

// ProcessAudio allocates one Turn, runs ASR, ignores partial events, and handles one final result.
func (p *TurnProcessor) ProcessAudio(ctx context.Context, request TurnProcessRequest) (TurnContext, error) {
	if err := ctx.Err(); err != nil {
		return TurnContext{}, err
	}
	if p == nil || p.recognizer == nil || p.opener == nil || p.pipeline == nil {
		return TurnContext{}, ErrTurnProcessorDependencyRequired
	}
	if err := p.pipeline.validate(); err != nil {
		return TurnContext{}, err
	}
	turn, err := p.opener.OpenTurn(ctx, TurnOpenRequest{
		SessionID: request.SessionID, AccountID: request.AccountID,
		TraceID: request.TraceID, StartedAt: request.StartedAt,
	})
	if err != nil {
		return TurnContext{}, fmt.Errorf("open Turn: %w", err)
	}
	if err := p.pipeline.reportRuntime(ctx, turn, session.RuntimeASRProcessing, ""); err != nil {
		return turn, fmt.Errorf("report ASR runtime: %w", err)
	}
	stream, err := p.recognizer.StartStream(ctx, asr.StreamRequest{
		SessionID: turn.SessionID, TurnID: turn.ID, SourceLanguage: request.SourceLanguage,
	})
	if err != nil {
		return turn, p.pipeline.finishASRWithError(ctx, turn, fmt.Errorf("start ASR stream: %w", err))
	}
	if stream == nil {
		return turn, p.pipeline.finishASRWithError(ctx, turn, ErrASRStreamRequired)
	}
	asrStartedAt := time.Now()
	p.pipeline.logLatencyCheckpoint("asr_stream_started", turn, asrStartedAt)
	defer stream.Close()
	streamCtx, stopEvents := context.WithCancel(ctx)
	defer stopEvents()
	finalEvents := make(chan *asr.FinalResult, 1)
	eventErrors := make(chan error, 1)
	go collectFinalASREvent(streamCtx, p.pipeline.latency, turn, asrStartedAt, stream.Events(), finalEvents, eventErrors)
	for _, chunk := range request.AudioChunks {
		if err := stream.PushAudio(ctx, append([]byte(nil), chunk...)); err != nil {
			return turn, p.pipeline.finishASRWithError(ctx, turn, fmt.Errorf("push audio for Turn %s: %w", turn.ID, err))
		}
	}

	result, err := stream.Finish(ctx)
	if err != nil {
		return turn, p.pipeline.finishASRWithError(ctx, turn, fmt.Errorf("finish ASR stream: %w", err))
	}
	if err := <-eventErrors; err != nil {
		return turn, p.pipeline.finishASRWithError(ctx, turn, err)
	}
	select {
	case eventResult := <-finalEvents:
		result = mergeFinalResult(*eventResult, result)
	default:
	}
	if result.SourceLanguage == "" {
		result.SourceLanguage = request.SourceLanguage
	}
	result.SourceLanguage = asr.NormalizeLanguage(result.SourceLanguage)
	p.pipeline.logLatencyCheckpoint("asr_final", turn, asrStartedAt,
		"source_language", result.SourceLanguage,
		"text_bytes", len(result.Text),
	)
	if strings.TrimSpace(result.Text) == "" || isTrivialASRText(result.Text) {
		// Local VAD / Manual commit can produce empty or filler cuts; keep listening.
		if err := p.pipeline.reportListening(ctx, turn); err != nil {
			return turn, err
		}
		return turn, nil
	}
	if result.SourceLanguage == "" {
		return turn, p.pipeline.finishASRWithError(ctx, turn, ErrASRFinalRequired)
	}
	if err := p.pipeline.HandleASRFinal(ctx, turn, result); err != nil {
		return turn, err
	}
	return turn, nil
}

func isTrivialASRText(text string) bool {
	trimmed := strings.TrimSpace(text)
	trimmed = strings.Trim(trimmed, "。.!！?？…~～、,， ")
	if trimmed == "" {
		return true
	}
	runes := []rune(trimmed)
	if len(runes) <= 1 {
		return true
	}
	switch strings.ToLower(trimmed) {
	case "嗯", "嗯嗯", "啊", "呃", "额", "哎", "欸", "诶", "哦", "噢", "喔",
		"咳", "咳咳", "对", "是", "好", "行", "嗯哼",
		"mm", "mmm", "mhm", "uh", "uhh", "um", "umm", "ah", "oh", "okay", "ok",
		"yes", "yeah", "yep", "hmm", "hm", "huh", "sigh", "ahem", "really":
		return true
	}
	return false
}

func collectFinalASREvent(ctx context.Context, latency LatencyLogger, turn TurnContext, asrStartedAt time.Time, events <-chan asr.Event, finalEvents chan<- *asr.FinalResult, eventErrors chan<- error) {
	var final *asr.FinalResult
	var eventErr error
	partialObserved := false
	for {
		select {
		case <-ctx.Done():
			eventErrors <- ctx.Err()
			return
		case event, ok := <-events:
			if !ok {
				if final != nil {
					finalEvents <- final
				}
				eventErrors <- eventErr
				return
			}
			if event.Type != asr.EventFinal || event.Final == nil {
				if event.Type == asr.EventPartial && !partialObserved {
					partialObserved = true
					latency.Checkpoint("asr_first_partial", turn, asrStartedAt, "text_bytes", len(event.Text))
				}
				continue
			}
			if final != nil {
				if eventErr == nil {
					eventErr = ErrDuplicateASRFinal
				}
				continue
			}
			result := *event.Final
			final = &result
		}
	}
}

func mergeFinalResult(event, finished asr.FinalResult) asr.FinalResult {
	if event.Text == "" {
		event.Text = finished.Text
	}
	if event.SourceLanguage == "" {
		event.SourceLanguage = finished.SourceLanguage
	}
	if event.Provider == "" {
		event.Provider = finished.Provider
	}
	if event.Model == "" {
		event.Model = finished.Model
	}
	if event.AudioDuration == 0 {
		event.AudioDuration = finished.AudioDuration
	}
	if event.Confidence == 0 {
		event.Confidence = finished.Confidence
	}
	if event.ProviderSpeakerID == "" {
		event.ProviderSpeakerID = finished.ProviderSpeakerID
	}
	if event.AudioStart == 0 {
		event.AudioStart = finished.AudioStart
	}
	if event.AudioEnd == 0 {
		event.AudioEnd = finished.AudioEnd
	}
	if event.CostAmount == "" {
		event.CostAmount = finished.CostAmount
	}
	if event.Currency == "" {
		event.Currency = finished.Currency
	}
	return event
}
