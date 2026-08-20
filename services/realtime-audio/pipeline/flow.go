package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	realtimev1 "github.com/1024XEngineer/xe6-tsy/packages/contracts/realtime/v1"
	"github.com/1024XEngineer/xe6-tsy/services/realtime-audio/asr"
)

var (
	// ErrTurnProcessorDependencyRequired 表示公共 ASR 流程缺少必要的生命周期组件或 final Handler。
	ErrTurnProcessorDependencyRequired = errors.New("turn processor dependency is required")
	// ErrASRStreamRequired 表示 ASR Provider 没有返回可由当前 Turn 管理的流。
	ErrASRStreamRequired = errors.New("ASR stream is required")
	// ErrASRFinalRequired 表示 ASR 流结束时没有产生可处理的 final 结果。
	ErrASRFinalRequired = errors.New("ASR final result is required")
	// ErrDuplicateASRFinal 表示同一个 Turn 收到多个 final；公共流程只允许提交一次。
	ErrDuplicateASRFinal = errors.New("duplicate ASR final result")
	// ErrTurnSuperseded indicates a later VAD Turn owns the runtime now.
	ErrTurnSuperseded = errors.New("turn superseded")
)

// TurnProcessRequest 保存一个实时 Turn 的音频和不可变元数据。
type TurnProcessRequest struct {
	SessionID      string
	AccountID      string
	TraceID        string
	SourceLanguage string
	StartedAt      time.Time
	AudioChunks    [][]byte
}

// ASRFinalHandler 消费一个已经稳定的 ASR final 结果。
// 实现方只负责 ASR 之后的业务处理，不得重新启动 ASR，也不得把 partial 当作 final。
// Handler 返回的错误会原样回到 TurnProcessor，由上层决定 Runtime 失败或重试策略。
type ASRFinalHandler interface {
	HandleASRFinal(ctx context.Context, turn TurnContext, result asr.FinalResult) error
}

// ASRPartialObserver receives replaceable ASR snapshots for ordinary Turns.
// Implementations must treat delivery as best-effort and must not invoke translation,
// TTS, FinalTurn persistence, command handling, or usage recording.
type ASRPartialObserver interface {
	ObserveASRPartial(ctx context.Context, event realtimev1.ASRPartialEvent)
}

// TurnProcessor 负责公共 Turn 生命周期，并把 ASR final 交给与模式无关的 Handler 接口。
// 它拥有一次 ASR 读取和一次 final 分发的权责，避免 assistant、同传等模式重复调用 ASR。
type TurnProcessor struct {
	recognizer  asr.Provider
	asrProvider string
	opener      *TurnOpener
	pipeline    *PipelineService
	finals      ASRFinalHandler
	partials    ASRPartialObserver
	phrases     *PhraseSubtitleProcessor
}

// TurnProcessorDependencies 注入可离线测试的 ASR、Turn 配置读取、媒体生命周期和 final Handler。
// Pipeline 仍负责公共运行状态/失败收尾；Finals 负责 ASR final 之后的模式处理。
type TurnProcessorDependencies struct {
	ASR         asr.Provider
	ASRProvider string
	Opener      *TurnOpener
	Pipeline    *PipelineService
	Finals      ASRFinalHandler
	Partials    ASRPartialObserver
	Phrases     *PhraseSubtitleProcessor
}

// StreamingTurnProcessor starts an ASR stream when VAD opens and receives
// frames until the corresponding VAD final. The finalized-only ProcessAudio
// method remains available for callers that do not have incremental VAD events.
type StreamingTurnProcessor interface {
	StartAudio(context.Context, TurnProcessRequest) (*AudioTurn, error)
}

// AudioTurn owns one live ASR stream. It is not safe for concurrent PushAudio
// and Finish calls; segment.Service serializes VAD events for a session.
type AudioTurn struct {
	processor      *TurnProcessor
	turn           TurnContext
	request        TurnProcessRequest
	stream         asr.Stream
	asrStartedAt   time.Time
	eventCancel    context.CancelFunc
	finalEvents    chan *asr.FinalResult
	eventErrors    chan error
	settlePartials func()
	closeOnce      sync.Once
}

// NewTurnProcessor 创建一个处理完整音频 Turn 的公共 Runner。
// 构造只保存依赖，真正的 Turn、ASR 流和 Handler 副作用都在 ProcessAudio 中发生。
func NewTurnProcessor(deps TurnProcessorDependencies) *TurnProcessor {
	return &TurnProcessor{
		recognizer:  deps.ASR,
		asrProvider: deps.ASRProvider,
		opener:      deps.Opener,
		pipeline:    deps.Pipeline,
		finals:      deps.Finals,
		partials:    deps.Partials,
		phrases:     deps.Phrases,
	}
}

// ProcessAudio allocates a Turn, performs ASR, publishes optional ephemeral partials, and
// gives the only final result to the Handler. Empty or filler-only text restores listening
// without entering a mode Handler or producing translation or playback side effects.
func (p *TurnProcessor) ProcessAudio(ctx context.Context, request TurnProcessRequest) (TurnContext, error) {
	audioTurn, err := p.StartAudio(ctx, request)
	if err != nil {
		return TurnContext{}, err
	}
	defer audioTurn.Close()
	for _, chunk := range request.AudioChunks {
		if err := audioTurn.PushAudio(ctx, chunk); err != nil {
			return audioTurn.turn, err
		}
	}
	return audioTurn.Finish(ctx)
}

// StartAudio allocates the Turn and starts recognition before VAD closes. This
// makes provider partial events available during the active utterance.
func (p *TurnProcessor) StartAudio(ctx context.Context, request TurnProcessRequest) (*AudioTurn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p == nil || p.recognizer == nil || p.opener == nil || p.pipeline == nil || p.finals == nil {
		return nil, ErrTurnProcessorDependencyRequired
	}
	if err := p.pipeline.validate(); err != nil {
		return nil, err
	}
	turn, err := p.opener.OpenTurn(ctx, TurnOpenRequest{
		SessionID: request.SessionID, AccountID: request.AccountID,
		TraceID: request.TraceID, StartedAt: request.StartedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("open Turn: %w", err)
	}
	if err := p.pipeline.claimASRRuntime(ctx, turn); err != nil {
		return nil, fmt.Errorf("report ASR runtime: %w", err)
	}
	if turn.Mode.Mode == realtimev1.ModeInterpretation {
		p.phrases.Start(turn, request.SourceLanguage)
	}
	stream, err := p.recognizer.StartStream(ctx, asr.StreamRequest{
		SessionID: turn.SessionID, TurnID: turn.ID, SourceLanguage: request.SourceLanguage,
	})
	if err != nil {
		p.phrases.Discard(turn.ID)
		p.pipeline.latency.ProviderFailure("asr_start", turn, p.asrProvider, "", err)
		return nil, p.pipeline.finishASRWithError(ctx, turn, fmt.Errorf("start ASR stream: %w", err))
	}
	if stream == nil {
		p.phrases.Discard(turn.ID)
		p.pipeline.latency.ProviderFailure("asr_stream", turn, p.asrProvider, "", ErrASRStreamRequired)
		return nil, p.pipeline.finishASRWithError(ctx, turn, ErrASRStreamRequired)
	}
	asrStartedAt := time.Now()
	p.pipeline.latency.ProviderCheckpoint("asr_stream_started", turn, asrStartedAt, p.asrProvider, "")
	streamCtx, stopEvents := context.WithCancel(ctx)
	finalEvents := make(chan *asr.FinalResult, 1)
	eventErrors := make(chan error, 1)
	var partialEvents chan asr.Event
	partialSettled := make(chan struct{})
	var settlePartials sync.Once
	settlePartialObserver := func() { settlePartials.Do(func() { close(partialSettled) }) }
	if p.partials != nil || p.phrases != nil {
		partialEvents = make(chan asr.Event, 8)
		go dispatchASRPartials(streamCtx, p.partials, p.phrases, turn, request.SourceLanguage, partialEvents, partialSettled)
	}
	go collectFinalASREvent(streamCtx, p.pipeline.latency, turn, asrStartedAt, stream.Events(), finalEvents, eventErrors, partialEvents, settlePartialObserver)
	return &AudioTurn{processor: p, turn: turn, request: request, stream: stream, eventCancel: stopEvents,
		asrStartedAt: asrStartedAt, finalEvents: finalEvents, eventErrors: eventErrors, settlePartials: settlePartialObserver}, nil
}

// PushAudio forwards a single PCM frame without waiting for VAD final.
func (t *AudioTurn) PushAudio(ctx context.Context, chunk []byte) error {
	if t.stream == nil || t.processor == nil {
		return ErrASRStreamRequired
	}
	if err := t.stream.PushAudio(ctx, append([]byte(nil), chunk...)); err != nil {
		t.processor.pipeline.latency.ProviderFailure("asr_push_audio", t.turn, t.processor.asrProvider, "", err)
		t.Close()
		return t.processor.pipeline.finishASRWithError(ctx, t.turn, fmt.Errorf("push audio for Turn %s: %w", t.turn.ID, err))
	}
	return nil
}

// Finish closes ASR and dispatches only its final result to the mode handler.
func (t *AudioTurn) Finish(ctx context.Context) (TurnContext, error) {
	if t.stream == nil || t.processor == nil {
		return TurnContext{}, ErrASRStreamRequired
	}
	p := t.processor
	turn := t.turn
	request := t.request

	result, err := t.stream.Finish(ctx)
	t.settlePartials()
	if err != nil {
		p.pipeline.latency.ProviderFailure("asr_finish", turn, observedProvider(p.asrProvider, result.Provider), result.Model, err)
		return turn, p.pipeline.finishASRWithError(ctx, turn, fmt.Errorf("finish ASR stream: %w", err))
	}
	if err := <-t.eventErrors; err != nil {
		p.pipeline.latency.ProviderFailure("asr_events", turn, observedProvider(p.asrProvider, result.Provider), result.Model, err)
		return turn, p.pipeline.finishASRWithError(ctx, turn, err)
	}
	select {
	case eventResult := <-t.finalEvents:
		result = mergeFinalResult(*eventResult, result)
	default:
	}
	if result.SourceLanguage == "" {
		result.SourceLanguage = request.SourceLanguage
	}
	result.SourceLanguage = asr.NormalizeLanguage(result.SourceLanguage)
	if turn.Mode.Mode == realtimev1.ModeInterpretation {
		p.phrases.Flush(ctx, turn, result.Text)
	}
	p.pipeline.latency.ProviderCheckpoint("asr_final", turn, t.asrStartedAt, observedProvider(p.asrProvider, result.Provider), result.Model,
		"source_language", result.SourceLanguage,
		"text_bytes", len(result.Text),
	)
	if strings.TrimSpace(result.Text) == "" || isTrivialASRText(result.Text) {
		// 本地 VAD 或手动 commit 可能产生空片段、语气词片段；这类输入不应进入业务模式，
		// 直接恢复 listening，避免生成无意义的 FinalTurn、用量和 TTS。
		if turn.Mode.Mode == realtimev1.ModeInterpretation && p.phrases != nil {
			// Flush removes the stabilizer entry before this branch. Explicitly
			// discard the translation coordinator as well so empty turns do not
			// retain provider work or per-utterance state.
			p.phrases.Discard(turn.ID)
		}
		if err := p.pipeline.reportListening(ctx, turn); err != nil {
			return turn, err
		}
		return turn, nil
	}
	if result.SourceLanguage == "" {
		return turn, p.pipeline.finishASRWithError(ctx, turn, ErrASRFinalRequired)
	}
	// Recognition cost belongs to the shared Turn lifecycle, not to an
	// interpretation or assistant Handler. Publish it exactly once after final
	// validation and before dispatching any mode-specific side effects.
	if err := p.pipeline.publishUsage(ctx, turn, "asr", result.Provider, result.Model, result.AudioDuration.Milliseconds(), 0, 0, result.CostAmount, result.Currency); err != nil {
		return turn, p.pipeline.finishASRWithError(ctx, turn, fmt.Errorf("publish ASR usage: %w", err))
	}
	if err := p.finals.HandleASRFinal(ctx, turn, result); err != nil {
		return turn, err
	}
	return turn, nil
}

// Close is idempotent and releases the event reader on aborted turns.
func (t *AudioTurn) Close() {
	t.closeOnce.Do(func() {
		if t.settlePartials != nil {
			t.settlePartials()
		}
		if t.eventCancel != nil {
			t.eventCancel()
		}
		if t.stream != nil {
			_ = t.stream.Close()
		}
		if t.processor != nil && t.processor.phrases != nil {
			t.processor.phrases.Discard(t.turn.ID)
		}
	})
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

// collectFinalASREvent independently consumes ASR events and keeps at most one final result.
// Partial snapshots use a bounded latest-value queue so an observer can never block provider
// reads or the final-result path. Duplicate finals still reach ProcessAudio as an error.
func collectFinalASREvent(ctx context.Context, latency LatencyLogger, turn TurnContext, asrStartedAt time.Time, events <-chan asr.Event, finalEvents chan<- *asr.FinalResult, eventErrors chan<- error, partialEvents chan asr.Event, settlePartials func()) {
	if partialEvents != nil {
		defer close(partialEvents)
	}
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
				if event.Type == asr.EventPartial {
					if !partialObserved {
						partialObserved = true
						latency.Checkpoint("asr_first_partial", turn, asrStartedAt, "text_bytes", len(event.Text))
					}
					enqueueLatestPartial(partialEvents, event)
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
			settlePartials()
		}
	}
}

func enqueueLatestPartial(queue chan asr.Event, event asr.Event) {
	if strings.TrimSpace(event.Text) == "" {
		return
	}
	select {
	case queue <- event:
		return
	default:
	}
	select {
	case <-queue:
	default:
	}
	select {
	case queue <- event:
	default:
	}
}

func dispatchASRPartials(ctx context.Context, observer ASRPartialObserver, phrases *PhraseSubtitleProcessor, turn TurnContext, sourceLanguage string, events <-chan asr.Event, settled <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-settled:
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			select {
			case <-settled:
				return
			default:
			}
			partial := realtimev1.ASRPartialEvent{
				Type:           realtimev1.ASRPartialTopic,
				EventVersion:   realtimev1.ASRPartialEventVersion,
				SessionID:      turn.SessionID,
				TurnID:         turn.ID,
				Text:           strings.TrimSpace(event.Text),
				SourceLanguage: asr.NormalizeLanguage(sourceLanguage),
				OccurredAt:     time.Now().UTC(),
			}
			if observer != nil {
				observer.ObserveASRPartial(ctx, partial)
			}
			if turn.Mode.Mode == realtimev1.ModeInterpretation {
				phrases.Observe(ctx, partial)
			}
		}
	}
}

// mergeFinalResult 以事件流里的 final 文本和语言为主，再用 Finish 返回的元数据补齐缺失字段。
// 这样既保留实时事件的识别内容，也不会丢失 Provider 在 Finish 阶段才提供的计费、时长和说话人信息。
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
