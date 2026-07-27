package pipeline

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
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
	turn, err := p.opener.OpenTurn(ctx, TurnOpenRequest{
		SessionID: request.SessionID, AccountID: request.AccountID,
		TraceID: request.TraceID, StartedAt: request.StartedAt,
	})
	if err != nil {
		return TurnContext{}, fmt.Errorf("open Turn: %w", err)
	}
	stream, err := p.recognizer.StartStream(ctx, asr.StreamRequest{
		SessionID: turn.SessionID, TurnID: turn.ID, SourceLanguage: request.SourceLanguage,
	})
	if err != nil {
		return turn, fmt.Errorf("start ASR stream: %w", err)
	}
	if stream == nil {
		return turn, ErrASRStreamRequired
	}
	defer stream.Close()
	for _, chunk := range request.AudioChunks {
		if err := stream.PushAudio(ctx, append([]byte(nil), chunk...)); err != nil {
			return turn, fmt.Errorf("push audio for Turn %s: %w", turn.ID, err)
		}
	}

	finalEvents := make(chan *asr.FinalResult, 1)
	eventErrors := make(chan error, 1)
	go collectFinalASREvent(stream.Events(), finalEvents, eventErrors)
	result, err := stream.Finish(ctx)
	if err != nil {
		return turn, fmt.Errorf("finish ASR stream: %w", err)
	}
	if err := <-eventErrors; err != nil {
		return turn, err
	}
	select {
	case eventResult := <-finalEvents:
		result = mergeFinalResult(*eventResult, result)
	default:
	}
	if result.Text == "" || result.SourceLanguage == "" {
		return turn, ErrASRFinalRequired
	}
	if err := p.pipeline.HandleASRFinal(ctx, turn, result); err != nil {
		return turn, err
	}
	return turn, nil
}

func collectFinalASREvent(events <-chan asr.Event, finalEvents chan<- *asr.FinalResult, eventErrors chan<- error) {
	var final *asr.FinalResult
	for event := range events {
		if event.Type != asr.EventFinal || event.Final == nil {
			continue
		}
		if final != nil {
			eventErrors <- ErrDuplicateASRFinal
			return
		}
		result := *event.Final
		final = &result
	}
	if final != nil {
		finalEvents <- final
	}
	eventErrors <- nil
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
	if event.CostAmount == "" {
		event.CostAmount = finished.CostAmount
	}
	if event.Currency == "" {
		event.Currency = finished.Currency
	}
	return event
}
