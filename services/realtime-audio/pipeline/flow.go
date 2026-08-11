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
	// ErrTurnProcessorDependencyRequired 表示公共 ASR 流程缺少必要的生命周期组件或 final Handler。
	ErrTurnProcessorDependencyRequired = errors.New("turn processor dependency is required")
	// ErrASRStreamRequired 表示 ASR Provider 没有返回可由当前 Turn 管理的流。
	ErrASRStreamRequired = errors.New("ASR stream is required")
	// ErrASRFinalRequired 表示 ASR 流结束时没有产生可处理的 final 结果。
	ErrASRFinalRequired = errors.New("ASR final result is required")
	// ErrDuplicateASRFinal 表示同一个 Turn 收到多个 final；公共流程只允许提交一次。
	ErrDuplicateASRFinal = errors.New("duplicate ASR final result")
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

// TurnProcessor 负责公共 Turn 生命周期，并把 ASR final 交给与模式无关的 Handler 接口。
// 它拥有一次 ASR 读取和一次 final 分发的权责，避免 assistant、同传等模式重复调用 ASR。
type TurnProcessor struct {
	recognizer  asr.Provider
	asrProvider string
	opener      *TurnOpener
	pipeline    *PipelineService
	finals      ASRFinalHandler
}

// TurnProcessorDependencies 注入可离线测试的 ASR、Turn 配置读取、媒体生命周期和 final Handler。
// Pipeline 仍负责公共运行状态/失败收尾；Finals 负责 ASR final 之后的模式处理。
type TurnProcessorDependencies struct {
	ASR         asr.Provider
	ASRProvider string
	Opener      *TurnOpener
	Pipeline    *PipelineService
	Finals      ASRFinalHandler
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
	}
}

// ProcessAudio 分配一个 Turn、执行一次 ASR、忽略 partial，并将唯一 final 交给 Handler。
// 空文本或纯填充词只恢复 listening，不进入模式 Handler，也不会产生翻译或播放副作用。
func (p *TurnProcessor) ProcessAudio(ctx context.Context, request TurnProcessRequest) (TurnContext, error) {
	if err := ctx.Err(); err != nil {
		return TurnContext{}, err
	}
	if p == nil || p.recognizer == nil || p.opener == nil || p.pipeline == nil || p.finals == nil {
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
		p.pipeline.latency.ProviderFailure("asr_start", turn, p.asrProvider, err)
		return turn, p.pipeline.finishASRWithError(ctx, turn, fmt.Errorf("start ASR stream: %w", err))
	}
	if stream == nil {
		p.pipeline.latency.ProviderFailure("asr_stream", turn, p.asrProvider, ErrASRStreamRequired)
		return turn, p.pipeline.finishASRWithError(ctx, turn, ErrASRStreamRequired)
	}
	asrStartedAt := time.Now()
	p.pipeline.latency.ProviderCheckpoint("asr_stream_started", turn, asrStartedAt, p.asrProvider)
	defer stream.Close()
	streamCtx, stopEvents := context.WithCancel(ctx)
	defer stopEvents()
	finalEvents := make(chan *asr.FinalResult, 1)
	eventErrors := make(chan error, 1)
	go collectFinalASREvent(streamCtx, p.pipeline.latency, turn, asrStartedAt, stream.Events(), finalEvents, eventErrors)
	for _, chunk := range request.AudioChunks {
		if err := stream.PushAudio(ctx, append([]byte(nil), chunk...)); err != nil {
			p.pipeline.latency.ProviderFailure("asr_push_audio", turn, p.asrProvider, err)
			return turn, p.pipeline.finishASRWithError(ctx, turn, fmt.Errorf("push audio for Turn %s: %w", turn.ID, err))
		}
	}

	result, err := stream.Finish(ctx)
	if err != nil {
		p.pipeline.latency.ProviderFailure("asr_finish", turn, p.asrProvider, err)
		return turn, p.pipeline.finishASRWithError(ctx, turn, fmt.Errorf("finish ASR stream: %w", err))
	}
	if err := <-eventErrors; err != nil {
		p.pipeline.latency.ProviderFailure("asr_events", turn, p.asrProvider, err)
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
	p.pipeline.latency.ProviderCheckpoint("asr_final", turn, asrStartedAt, observedProvider(p.asrProvider, result.Provider),
		"source_language", result.SourceLanguage,
		"text_bytes", len(result.Text),
	)
	if strings.TrimSpace(result.Text) == "" || isTrivialASRText(result.Text) {
		// 本地 VAD 或手动 commit 可能产生空片段、语气词片段；这类输入不应进入业务模式，
		// 直接恢复 listening，避免生成无意义的 FinalTurn、用量和 TTS。
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

// collectFinalASREvent 独立消费 ASR 事件，过滤 partial，并保证一个 Turn 至多保留一个 final。
// 它通过有缓冲 channel 与 ProcessAudio 汇合，避免 Provider 在 Finish 前发送事件时阻塞；
// duplicate final 通过错误通道返回，不能静默覆盖第一次结果。
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
